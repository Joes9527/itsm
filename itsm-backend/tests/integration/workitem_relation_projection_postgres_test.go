//go:build integration_postgres

package integration

import (
	"context"
	"errors"
	"fmt"
	executionfixture "itsm-backend/tests/fixtures/execution"
	"testing"

	"github.com/stretchr/testify/require"
	"go.uber.org/zap"
	relationmeta "itsm-backend/common/workitemrelation"
	"itsm-backend/ent"
	"itsm-backend/ent/permission"
	"itsm-backend/ent/problem"
	"itsm-backend/ent/rolepermission"
	changeDomain "itsm-backend/handlers/change"
	problemDomain "itsm-backend/handlers/problem"
	"itsm-backend/handlers/shared/workitemmutation"
)

func projectionOwners(t *testing.T, f *relationFixture) (*problemDomain.Service, *changeDomain.Service, *ent.Change) {
	t.Helper()
	_, err := f.db.ExecContext(f.ctx, fmt.Sprintf("GRANT SELECT ON problems,changes,ticket_categories TO %q", f.runtimeRole))
	require.NoError(t, err)
	item := f.client.Ticket.Create().SetTenantID(f.tenant.ID).SetRequesterID(f.actor.ID).SetOpenedByID(f.actor.ID).SetTicketNumber("CHG-PROJECT").SetTitle("change projection").SetRecordClass("change_request").SetStatus("draft").SaveX(f.ctx)
	c := f.client.Change.Create().SetWorkItemID(item.ID).SaveX(f.ctx)
	pOwner := problemDomain.NewService(problemDomain.NewEntRepository(f.runtime.Tenant), zap.NewNop().Sugar(), executionfixture.Standard())
	pOwner.SetDirectorySnapshot(f.runtime.IntakeDirectorySnapshot())
	cOwner := changeDomain.NewService(changeDomain.NewEntRepository(f.runtime.Tenant, nil), f.runtime.Tenant, zap.NewNop().Sugar(), executionfixture.Standard())
	cOwner.SetDirectorySnapshot(f.runtime.IntakeDirectorySnapshot())
	return pOwner, cOwner, c
}

func TestProfessionalRelationProjectionCurrentAuthority(t *testing.T) {
	for _, domain := range []string{"problem", "change"} {
		t.Run(domain, func(t *testing.T) {
			f := newRelationFixture(t)
			pOwner, cOwner, c := projectionOwners(t, f)
			p := f.client.Problem.Query().Where(problem.WorkItemID(f.problem.ID)).OnlyX(f.ctx)
			cmd := f.command("projection-link")
			root, other := f.problem.ID, f.inc.WorkItemID
			if domain == "change" {
				root, other = c.WorkItemID, f.problem.ID
				cmd.SourceID, cmd.TargetID, cmd.Type, cmd.Required = f.problem.ID, c.WorkItemID, "resolved_by_change", true
			}
			_, err := f.owner.Apply(f.ctx, cmd, false)
			require.NoError(t, err)
			read := func() ([]relationmeta.View, error) {
				if domain == "problem" {
					v, e := pOwner.Get(f.ctx, p.ID, cmd.Meta)
					if e != nil {
						return nil, e
					}
					return v.Relations, nil
				}
				v, e := cOwner.GetChange(f.ctx, c.ID, cmd.Meta)
				if e != nil {
					return nil, e
				}
				return v.Relations, nil
			}
			views, err := read()
			require.NoError(t, err)
			require.Len(t, views, 1)
			require.Equal(t, cmd.Type, views[0].Type)
			require.Equal(t, cmd.Required, views[0].Required)
			require.Equal(t, cmd.SourceID, views[0].Source.WorkItemID)
			require.Equal(t, cmd.TargetID, views[0].Target.WorkItemID)
			require.Equal(t, f.client.Ticket.GetX(f.ctx, cmd.SourceID).TicketNumber, views[0].Source.Number)
			require.Equal(t, 2, views[0].Source.Version)
			require.Equal(t, f.client.Ticket.GetX(f.ctx, root).RecordClass, map[int]string{views[0].Source.WorkItemID: views[0].Source.RecordClass, views[0].Target.WorkItemID: views[0].Target.RecordClass}[root])
			f.actor.Update().SetRole("projection_reader").ExecX(f.ctx)
			role := f.client.Role.Create().SetTenantID(f.tenant.ID).SetCode("projection_reader").SetName("reader").SaveX(f.ctx)
			for _, resource := range []string{"problem", "incident", "change"} {
				grant := f.client.Permission.Create().SetTenantID(f.tenant.ID).SetCode(resource + "_projection_read").SetName(resource).SetResource(resource).SetAction("read").SaveX(f.ctx)
				f.client.RolePermission.Create().SetTenantID(f.tenant.ID).SetRoleID(role.ID).SetPermissionID(grant.ID).ExecX(f.ctx)
			}
			_, err = read()
			require.NoError(t, err)
			otherActor := f.client.User.Create().SetTenantID(f.tenant.ID).SetUsername("hidden-requester").SetName("other").SetEmail("hidden@example.test").SetPasswordHash("test").SetRole("end_user").SetActive(true).SaveX(f.ctx)
			f.client.Ticket.UpdateOneID(other).SetRequesterID(otherActor.ID).ClearAssigneeID().ExecX(f.ctx)
			_, err = read()
			require.Error(t, err, "an unreadable active endpoint must not become an empty projection")
			f.client.Ticket.UpdateOneID(other).SetRequesterID(f.actor.ID).ExecX(f.ctx)
			grant := f.client.Permission.Query().Where(permission.Resource(domain), permission.Action("read")).OnlyX(f.ctx)
			f.client.RolePermission.Delete().Where(rolepermission.PermissionID(grant.ID)).ExecX(f.ctx)
			_, err = read()
			require.Error(t, err, "current permission revocation must take effect immediately")
			f.client.RolePermission.Create().SetTenantID(f.tenant.ID).SetRoleID(role.ID).SetPermissionID(grant.ID).ExecX(f.ctx)
			f.runtime.Tenant.WorkItemRelation.Intercept(ent.InterceptFunc(func(next ent.Querier) ent.Querier {
				return ent.QuerierFunc(func(context.Context, ent.Query) (ent.Value, error) { return nil, errors.New("projection query failed") })
			}))
			_, err = read()
			require.Error(t, err, "query failures must not fabricate empty relations")
		})
	}
}

func TestProfessionalListScopesBeforePaginationAndCount(t *testing.T) {
	for _, domain := range []string{"problem", "change"} {
		t.Run(domain, func(t *testing.T) {
			f := newRelationFixture(t)
			pOwner, cOwner, c := projectionOwners(t, f)
			f.actor.Update().SetRole("limited_reader").ExecX(f.ctx)
			role := f.client.Role.Create().SetTenantID(f.tenant.ID).SetCode("limited_reader").SetName("reader").SaveX(f.ctx)
			grant := f.client.Permission.Create().SetTenantID(f.tenant.ID).SetCode(domain + "_read").SetName("read").SetResource(domain).SetAction("read").SaveX(f.ctx)
			f.client.RolePermission.Create().SetTenantID(f.tenant.ID).SetRoleID(role.ID).SetPermissionID(grant.ID).ExecX(f.ctx)
			other := f.client.User.Create().SetTenantID(f.tenant.ID).SetUsername("other").SetName("other").SetEmail("other@example.test").SetPasswordHash("test").SetRole("agent").SetActive(true).SaveX(f.ctx)
			visible := []int{f.problem.ID}
			class := "problem"
			if domain == "change" {
				class = "change_request"
				visible = []int{c.WorkItemID}
			}
			for i, requester := range []int{other.ID, f.actor.ID, other.ID} {
				item := f.client.Ticket.Create().SetTenantID(f.tenant.ID).SetRequesterID(requester).SetTicketNumber(fmt.Sprintf("PAGE-%d", i)).SetTitle("page").SetRecordClass(class).SetStatus("draft").SaveX(f.ctx)
				if domain == "problem" {
					f.client.Problem.Create().SetWorkItemID(item.ID).ExecX(f.ctx)
				} else {
					f.client.Change.Create().SetWorkItemID(item.ID).ExecX(f.ctx)
				}
				if requester == f.actor.ID {
					visible = append(visible, item.ID)
				}
			}
			meta := workitemmutation.Meta{TenantID: f.tenant.ID, ActorID: f.actor.ID, Source: "http"}
			observed := []int{}
			for page := 1; page <= 2; page++ {
				if domain == "problem" {
					rows, total, err := pOwner.List(f.ctx, meta, page, 1, nil)
					require.NoError(t, err)
					require.Equal(t, 2, total)
					require.Len(t, rows, 1)
					require.NotNil(t, rows[0].Relations)
					observed = append(observed, *rows[0].WorkItemID)
				} else {
					rows, total, err := cOwner.ListChanges(f.ctx, meta, page, 1, "", "", "")
					require.NoError(t, err)
					require.Equal(t, 2, total)
					require.Len(t, rows, 1)
					require.NotNil(t, rows[0].Relations)
					observed = append(observed, *rows[0].WorkItemID)
				}
			}
			require.ElementsMatch(t, visible, observed)
		})
	}
}

func TestRetiredProblemStorageExcludedAndSchemaPreserved(t *testing.T) {
	f := newRelationFixture(t)
	pOwner, cOwner, c := projectionOwners(t, f)
	p := f.client.Problem.Query().Where(problem.WorkItemID(f.problem.ID)).OnlyX(f.ctx)
	for _, statement := range []string{
		"CREATE TABLE problem_incidents (problem_id bigint REFERENCES problems(id),incident_id bigint REFERENCES incidents(id))",
		"CREATE TABLE problem_changes (problem_id bigint REFERENCES problems(id),change_id bigint REFERENCES changes(id))",
		"ALTER TABLE tickets ADD COLUMN problem_tickets bigint",
		"ALTER TABLE tickets ADD CONSTRAINT tickets_problems_tickets FOREIGN KEY (problem_tickets) REFERENCES problems(id) ON DELETE SET NULL",
	} {
		_, err := f.db.ExecContext(f.ctx, statement)
		require.NoError(t, err)
	}
	_, err := f.db.ExecContext(f.ctx, "INSERT INTO problem_incidents VALUES ($1,$2)", p.ID, f.inc.ID)
	require.NoError(t, err)
	_, err = f.db.ExecContext(f.ctx, "INSERT INTO problem_changes VALUES ($1,$2)", p.ID, c.ID)
	require.NoError(t, err)
	_, err = f.db.ExecContext(f.ctx, "UPDATE tickets SET problem_tickets=$1 WHERE id=$2", p.ID, c.WorkItemID)
	require.NoError(t, err)
	// Same default schema invocation as bootstrap must not physically retire unregistered old storage.
	require.NoError(t, f.client.Schema.Create(f.ctx))
	for _, table := range []string{"problem_incidents", "problem_changes"} {
		var count int
		require.NoError(t, f.db.QueryRowContext(f.ctx, "SELECT count(*) FROM "+table).Scan(&count))
		require.Equal(t, 1, count)
	}
	var oldFK int
	require.NoError(t, f.db.QueryRowContext(f.ctx, "SELECT problem_tickets FROM tickets WHERE id=$1", c.WorkItemID).Scan(&oldFK))
	require.Equal(t, p.ID, oldFK)
	meta := f.command("read").Meta
	projected, err := pOwner.Get(f.ctx, p.ID, meta)
	require.NoError(t, err)
	require.NotNil(t, projected.Relations)
	require.Empty(t, projected.Relations)
	change, err := cOwner.GetChange(f.ctx, c.ID, meta)
	require.NoError(t, err)
	require.NotNil(t, change.Relations)
	require.Empty(t, change.Relations)
	require.Zero(t, f.client.WorkItemRelation.Query().CountX(f.ctx))
}
