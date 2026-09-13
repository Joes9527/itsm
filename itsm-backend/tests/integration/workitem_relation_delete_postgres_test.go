//go:build integration_postgres

package integration

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	executionfixture "itsm-backend/tests/fixtures/execution"
	"testing"

	"github.com/stretchr/testify/require"
	"go.uber.org/zap"
	"itsm-backend/common"
	"itsm-backend/ent"
	"itsm-backend/ent/permission"
	"itsm-backend/ent/problem"
	"itsm-backend/ent/rolepermission"
	changeDomain "itsm-backend/handlers/change"
	problemDomain "itsm-backend/handlers/problem"
	"itsm-backend/handlers/shared/workitemmutation"
	"itsm-backend/service"
)

func TestWorkItemRelationsProblemDeletionRejectsActiveReference(t *testing.T) {
	f := newRelationFixture(t)
	_, err := f.db.ExecContext(f.ctx, fmt.Sprintf("GRANT SELECT ON problems TO %q", f.runtimeRole))
	require.NoError(t, err)
	cmd := f.command("deletion-reference")
	_, err = f.owner.Apply(f.ctx, cmd, false)
	require.NoError(t, err)
	p := f.client.Problem.Query().Where(problem.WorkItemID(f.problem.ID)).OnlyX(f.ctx)
	owner := problemDomain.NewService(problemDomain.NewEntRepository(f.runtime.Tenant), zap.NewNop().Sugar(), executionfixture.Standard())
	owner.SetDirectorySnapshot(f.runtime.IntakeDirectorySnapshot())
	err = owner.Delete(f.ctx, p.ID, cmd.Meta)
	require.Error(t, err, "an active reference must reject deletion rather than silently remove the relation")
	app, ok := common.AsAppError(err)
	require.True(t, ok, "expected explicit dependency conflict, got %v", err)
	require.Equal(t, common.ErrCodeConflict, app.Code)
	require.Nil(t, f.client.Ticket.GetX(f.ctx, f.problem.ID).DeletedAt)
	require.Nil(t, f.client.WorkItemRelation.Query().OnlyX(f.ctx).DeletedAt)
}

func deletionOwner(t *testing.T, f *relationFixture, class string) (*ent.Ticket, func(workitemmutation.Meta) error) {
	t.Helper()
	_, err := f.db.ExecContext(f.ctx, fmt.Sprintf("GRANT SELECT ON problems,changes TO %q", f.runtimeRole))
	require.NoError(t, err)
	if class == "problem" {
		p := f.client.Problem.Query().Where(problem.WorkItemID(f.problem.ID)).OnlyX(f.ctx)
		s := problemDomain.NewService(problemDomain.NewEntRepository(f.runtime.Tenant), zap.NewNop().Sugar(), executionfixture.Standard())
		s.SetDirectorySnapshot(f.runtime.IntakeDirectorySnapshot())
		return f.problem, func(m workitemmutation.Meta) error { return s.Delete(f.ctx, p.ID, m) }
	}
	item := f.client.Ticket.Create().SetTenantID(f.tenant.ID).SetRequesterID(f.actor.ID).SetOpenedByID(f.actor.ID).SetTicketNumber("CHG-DELETE").SetTitle("change deletion").SetRecordClass("change_request").SetStatus("draft").SaveX(f.ctx)
	c := f.client.Change.Create().SetWorkItemID(item.ID).SaveX(f.ctx)
	s := changeDomain.NewService(changeDomain.NewEntRepository(f.runtime.Tenant, nil), f.runtime.Tenant, zap.NewNop().Sugar(), executionfixture.Standard())
	s.SetDirectorySnapshot(f.runtime.IntakeDirectorySnapshot())
	return item, func(m workitemmutation.Meta) error { return s.DeleteChange(f.ctx, c.ID, m) }
}

func TestWorkItemRelationsDeletionOwnersAuthorizationAndRollback(t *testing.T) {
	for _, class := range []string{"problem", "change"} {
		for _, scenario := range []string{"clean", "active", "revoked_delete", "revoked_read", "row_hidden", "inactive", "cross_tenant", "query_failure", "write_failure"} {
			t.Run(class+"/"+scenario, func(t *testing.T) {
				f := newRelationFixture(t)
				item, remove := deletionOwner(t, f, class)
				cmd := f.command("guarded-delete")
				cmd.TargetID = item.ID
				if class == "change" {
					cmd.Type = "resolved_by_change"
				}
				if scenario == "active" {
					_, err := f.owner.Apply(f.ctx, cmd, false)
					require.NoError(t, err)
				}
				if scenario == "revoked_delete" || scenario == "revoked_read" || scenario == "row_hidden" {
					f.actor.Update().SetRole("deletion_operator").ExecX(f.ctx)
					role := f.client.Role.Create().SetTenantID(f.tenant.ID).SetCode("deletion_operator").SetName("deletion operator").SaveX(f.ctx)
					for _, verb := range []string{"read", "delete"} {
						grant := f.client.Permission.Create().SetTenantID(f.tenant.ID).SetCode(class + "_delete_test_" + verb).SetName(verb).SetResource(class).SetAction(verb).SaveX(f.ctx)
						f.client.RolePermission.Create().SetTenantID(f.tenant.ID).SetRoleID(role.ID).SetPermissionID(grant.ID).ExecX(f.ctx)
					}
					if scenario == "row_hidden" {
						other := f.client.User.Create().SetTenantID(f.tenant.ID).SetUsername("other-requester").SetName("other").SetRole("agent").SetPasswordHash("test").SetEmail("other@example.test").SetActive(true).SaveX(f.ctx)
						f.client.Ticket.UpdateOneID(item.ID).SetRequesterID(other.ID).ClearAssigneeID().ExecX(f.ctx)
					} else {
						verb := "delete"
						if scenario == "revoked_read" {
							verb = "read"
						}
						grant := f.client.Permission.Query().Where(permission.Resource(class), permission.Action(verb)).OnlyX(f.ctx)
						f.client.RolePermission.Delete().Where(rolepermission.PermissionID(grant.ID)).ExecX(f.ctx)
					}
				}
				if scenario == "inactive" {
					f.actor.Update().SetActive(false).ExecX(f.ctx)
				}
				if scenario == "cross_tenant" {
					cmd.Meta.TenantID++
				}
				if scenario == "query_failure" {
					f.runtime.Tenant.WorkItemRelation.Intercept(ent.InterceptFunc(func(next ent.Querier) ent.Querier {
						return ent.QuerierFunc(func(context.Context, ent.Query) (ent.Value, error) {
							return nil, errors.New("injected relation read failure")
						})
					}))
				}
				if scenario == "write_failure" {
					f.runtime.Tenant.Ticket.Use(func(next ent.Mutator) ent.Mutator {
						return ent.MutateFunc(func(context.Context, ent.Mutation) (ent.Value, error) {
							return nil, errors.New("injected delete write failure")
						})
					})
				}
				before := f.client.Ticket.GetX(f.ctx, item.ID)
				err := remove(cmd.Meta)
				after := f.client.Ticket.GetX(f.ctx, item.ID)
				if scenario == "clean" {
					require.NoError(t, err)
					require.NotNil(t, after.DeletedAt)
					require.Equal(t, before.Version+1, after.Version)
				} else {
					require.Error(t, err)
					require.Nil(t, after.DeletedAt)
					require.Equal(t, before.Version, after.Version)
					if scenario == "active" {
						app, ok := common.AsAppError(err)
						require.True(t, ok)
						require.Equal(t, common.ErrCodeConflict, app.Code)
						require.Nil(t, f.client.WorkItemRelation.Query().OnlyX(f.ctx).DeletedAt)
					}
				}
			})
		}
	}
}

func TestWorkItemRelationsFencePreservesTargetAndReverseOrdering(t *testing.T) {
	f := newRelationFixture(t)
	cmd := f.command("fence-fields")
	var before string
	require.NoError(t, f.db.QueryRowContext(f.ctx, "SELECT to_jsonb(t)::text FROM tickets t WHERE id=$1", cmd.TargetID).Scan(&before))
	result, err := f.owner.Apply(f.ctx, cmd, false)
	require.NoError(t, err)
	remove := cmd
	remove.Meta.ExpectedVersion = result.Version
	remove.Meta.OperationID = "fence-remove"
	_, err = f.owner.Apply(f.ctx, remove, true)
	require.NoError(t, err)
	replay, err := f.owner.Apply(f.ctx, cmd, false)
	require.NoError(t, err)
	require.True(t, replay.Replayed)
	var after string
	require.NoError(t, f.db.QueryRowContext(f.ctx, "SELECT to_jsonb(t)::text FROM tickets t WHERE id=$1", cmd.TargetID).Scan(&after))
	require.Equal(t, before, after, "physical fence must preserve every public target column")
	require.Equal(t, 2, f.client.AuditLog.Query().CountX(f.ctx), "only the two source operation receipts are emitted")
	cmd.Meta.ExpectedVersion = f.client.Ticket.GetX(f.ctx, cmd.SourceID).Version
	cmd.Type = "related_to"
	cmd.Meta.OperationID = "forward-order"
	reverse := cmd
	reverse.SourceID, reverse.TargetID = cmd.TargetID, cmd.SourceID
	reverse.Meta.ExpectedVersion = f.client.Ticket.GetX(f.ctx, reverse.SourceID).Version
	reverse.Meta.OperationID = "reverse-order"
	start := make(chan struct{})
	done := make(chan error, 2)
	for _, command := range []service.RelationCommand{cmd, reverse} {
		go func(c service.RelationCommand) { <-start; _, err := f.owner.Apply(f.ctx, c, false); done <- err }(command)
	}
	close(start)
	first, second := <-done, <-done
	require.NotEqual(t, first == nil, second == nil, "one symmetric tuple wins across reverse endpoint contenders")
}

func TestWorkItemRelationsDeleteSnapshotCannotMissIncomingReference(t *testing.T) {
	for _, class := range []string{"problem", "change"} {
		t.Run(class, func(t *testing.T) {
			f := newRelationFixture(t)
			item, remove := deletionOwner(t, f, class)
			ready, release := make(chan struct{}), make(chan struct{})
			barrier := ent.InterceptFunc(func(next ent.Querier) ent.Querier {
				return ent.QuerierFunc(func(ctx context.Context, q ent.Query) (ent.Value, error) {
					v, err := next.Query(ctx, q)
					close(ready)
					<-release
					return v, err
				})
			})
			if class == "problem" {
				f.runtime.Tenant.Problem.Intercept(barrier)
			} else {
				f.runtime.Tenant.Change.Intercept(barrier)
			}
			done := make(chan error, 1)
			cmd := f.command("reference-after-delete-snapshot")
			cmd.TargetID = item.ID
			if class == "change" {
				cmd.Type = "resolved_by_change"
			}
			go func() { done <- remove(cmd.Meta) }()
			<-ready
			_, err := f.owner.Apply(f.ctx, cmd, false)
			close(release)
			require.NoError(t, err)
			require.Error(t, <-done, "old RR deletion snapshot must not commit across incoming reference")
			require.Nil(t, f.client.Ticket.GetX(f.ctx, item.ID).DeletedAt)
			require.Nil(t, f.client.WorkItemRelation.Query().OnlyX(f.ctx).DeletedAt)
		})
	}
}

func TestWorkItemRelationsOldReferenceSnapshotCannotLinkDeletedTarget(t *testing.T) {
	for _, class := range []string{"problem", "change"} {
		t.Run(class, func(t *testing.T) {
			f := newRelationFixture(t)
			item, remove := deletionOwner(t, f, class)
			tx, err := f.runtime.Tenant.BeginTx(f.ctx, &sql.TxOptions{Isolation: sql.LevelRepeatableRead})
			require.NoError(t, err)
			defer tx.Rollback()
			_, err = tx.Ticket.Get(f.ctx, item.ID)
			require.NoError(t, err)
			cmd := f.command("old-reference-snapshot")
			cmd.TargetID = item.ID
			if class == "change" {
				cmd.Type = "resolved_by_change"
			}
			require.NoError(t, remove(cmd.Meta))
			err = f.owner.AddTx(f.ctx, tx, cmd)
			if err == nil {
				err = tx.Commit()
			}
			require.Error(t, err, "a pre-deletion RR reference snapshot must fail rather than commit a dangling target")
			require.NoError(t, tx.Rollback())
			require.Zero(t, f.client.WorkItemRelation.Query().CountX(f.ctx))
			require.Equal(t, cmd.Meta.ExpectedVersion, f.client.Ticket.GetX(f.ctx, cmd.SourceID).Version)
		})
	}
}
