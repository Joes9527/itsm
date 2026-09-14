//go:build integration_postgres

package integration

import (
	"context"
	"errors"
	"fmt"
	"github.com/stretchr/testify/require"
	"go.uber.org/zap"
	relationmeta "itsm-backend/common/workitemrelation"
	"itsm-backend/ent"
	"itsm-backend/ent/auditlog"
	"itsm-backend/ent/workitemrelation"
	creation "itsm-backend/handlers/common/workitemcreation"
	"itsm-backend/handlers/intake"
	problemdomain "itsm-backend/handlers/problem"
	catalogdomain "itsm-backend/handlers/service_catalog"
	"itsm-backend/repository/workitemnumber"
	"itsm-backend/service"
	executionfixture "itsm-backend/tests/fixtures/execution"
	"sync"
	"testing"
)

func newIntakeRelationFixture(t *testing.T) (*relationFixture, *intake.Service, creation.Identity, creation.CreateWorkItemCommand) {
	t.Helper()
	f := newRelationFixture(t)
	// The initial effects fixture has its own completed intake graph.
	for _, table := range []string{"problems", "work_item_number_sequences", "intake_resolution_snapshots", "process_bindings", "sla_definitions", "field_definitions", "field_values", "service_catalogs", "configuration_items", "groups"} {
		var runtimeRole string
		tx, err := f.runtime.Tenant.Tx(f.ctx)
		require.NoError(t, err)
		rows, err := tx.QueryContext(f.ctx, "SELECT current_user")
		require.NoError(t, err)
		require.True(t, rows.Next())
		require.NoError(t, rows.Scan(&runtimeRole))
		require.NoError(t, rows.Close())
		require.NoError(t, tx.Rollback())
		_, err = f.db.ExecContext(f.ctx, "GRANT SELECT,INSERT,UPDATE,DELETE ON "+table+" TO "+runtimeRole)
		require.NoError(t, err)
		var sequence *string
		require.NoError(t, f.db.QueryRowContext(f.ctx, "SELECT pg_get_serial_sequence($1,'id')", table).Scan(&sequence))
		if sequence != nil {
			_, err = f.db.ExecContext(f.ctx, "GRANT USAGE ON SEQUENCE "+*sequence+" TO "+runtimeRole)
			require.NoError(t, err)
		}
	}
	f.client.ProcessBinding.Create().SetTenantID(f.tenant.ID).SetBusinessType("problem").SetIsDefault(true).SetProcessDefinitionKey("none").SetConditions(map[string]any{"no_process": true}).SaveX(f.ctx)
	logger := zap.NewNop().Sugar()
	registry := intake.NewCreatorRegistry()
	require.NoError(t, registry.Register(problemdomain.NewService(problemdomain.NewEntRepository(f.runtime.Tenant), logger, executionfixture.Standard())))
	resolver := intake.NewResolver(catalogdomain.NewService(nil, f.runtime.Tenant, logger, nil), service.NewProcessBindingService(f.runtime.Tenant), service.NewConfigurationItemService(f.runtime.Tenant, logger, nil, nil), service.NewTicketCategoryService(f.runtime.Tenant))
	app := intake.NewService(f.runtime.Tenant, resolver, registry, intake.NewWorkItemCreator(workitemnumber.NewPostgreSQLAllocator()), f.runtime.IntakeDirectorySnapshot(), executionfixture.Standard())
	identity := creation.Identity{TenantID: f.tenant.ID, ActorID: f.actor.ID, RequesterID: f.actor.ID, Role: f.actor.Role, Channel: "http"}
	command := creation.CreateWorkItemCommand{RecordClass: "problem", IntakeKind: "problem", Confirmation: "confirmed", Title: "new investigation", IdempotencyKey: "intake-relations", SourceRelations: []creation.SourceRelationInput{{SourceWorkItemID: f.inc.WorkItemID, ExpectedVersion: 1, RelationType: "investigated_by"}}}
	return f, app, identity, command
}

func TestWorkItemRelationsIntakeAtomicRollback(t *testing.T) {
	for _, stage := range []string{"relation", "relation_audit"} {
		t.Run(stage, func(t *testing.T) {
			f, app, identity, command := newIntakeRelationFixture(t)
			tickets, problems, receipts, audits := f.client.Ticket.Query().CountX(f.ctx), f.client.Problem.Query().CountX(f.ctx), f.client.IntakeRequest.Query().CountX(f.ctx), f.client.AuditLog.Query().CountX(f.ctx)
			reached := false
			hook := func(next ent.Mutator) ent.Mutator {
				return ent.MutateFunc(func(ctx context.Context, m ent.Mutation) (ent.Value, error) {
					if stage == "relation_audit" {
						action, _ := m.Field("action")
						if action != "work_item.relation_added" {
							return next.Mutate(ctx, m)
						}
					}
					value, err := next.Mutate(ctx, m)
					if err != nil {
						return value, err
					}
					reached = true
					return nil, errors.New("injected post-extension relation failure")
				})
			}
			if stage == "relation" {
				f.runtime.Tenant.WorkItemRelation.Use(hook)
			} else {
				f.runtime.Tenant.AuditLog.Use(hook)
			}
			_, err := app.Create(f.ctx, identity, command)
			require.Error(t, err)
			require.True(t, reached, fmt.Sprint(err))
			require.Equal(t, tickets, f.client.Ticket.Query().CountX(f.ctx))
			require.Equal(t, problems, f.client.Problem.Query().CountX(f.ctx))
			require.Equal(t, receipts, f.client.IntakeRequest.Query().CountX(f.ctx))
			require.Equal(t, audits, f.client.AuditLog.Query().CountX(f.ctx))
			require.Zero(t, f.client.WorkItemRelation.Query().CountX(f.ctx))
			require.Equal(t, 1, f.client.Ticket.GetX(f.ctx, f.inc.WorkItemID).Version)
		})
	}
}

func TestWorkItemRelationsIntakeReplayAndMissingReceipt(t *testing.T) {
	f, app, identity, command := newIntakeRelationFixture(t)
	first, err := app.Create(f.ctx, identity, command)
	require.NoError(t, err)
	require.Equal(t, 2, f.client.Ticket.GetX(f.ctx, f.inc.WorkItemID).Version)
	replay, err := app.Create(f.ctx, identity, command)
	require.NoError(t, err)
	require.True(t, replay.Replayed)
	require.Equal(t, first.WorkItemID, replay.WorkItemID)
	// Missing immutable relation evidence must never trigger AddTx on replay.
	// Corrupt only this disposable fixture through the schema owner to test
	// fail-closed replay of incomplete durable evidence (runtime cannot do this).
	_, err = f.db.ExecContext(f.ctx, "ALTER TABLE audit_logs DISABLE TRIGGER USER")
	require.NoError(t, err)
	_, err = f.db.ExecContext(f.ctx, "UPDATE audit_logs SET request_body='{}' WHERE action='work_item.relation_added'")
	require.NoError(t, err)
	_, err = f.db.ExecContext(f.ctx, "ALTER TABLE audit_logs ENABLE TRIGGER USER")
	require.NoError(t, err)
	count := f.client.WorkItemRelation.Query().CountX(f.ctx)
	_, err = app.Create(f.ctx, identity, command)
	require.ErrorIs(t, err, creation.ErrInternalFailure)
	require.Equal(t, count, f.client.WorkItemRelation.Query().CountX(f.ctx))
	require.Equal(t, 2, f.client.Ticket.GetX(f.ctx, f.inc.WorkItemID).Version)
	_, err = f.db.ExecContext(f.ctx, "ALTER TABLE audit_logs DISABLE TRIGGER USER")
	require.NoError(t, err)
	_, err = f.db.ExecContext(f.ctx, "DELETE FROM audit_logs WHERE action='work_item.relation_added'")
	require.NoError(t, err)
	_, err = f.db.ExecContext(f.ctx, "ALTER TABLE audit_logs ENABLE TRIGGER USER")
	require.NoError(t, err)
	_, err = app.Create(f.ctx, identity, command)
	require.ErrorIs(t, err, creation.ErrInternalFailure)
	require.Equal(t, count, f.client.WorkItemRelation.Query().CountX(f.ctx))
	require.Equal(t, 2, f.client.Ticket.GetX(f.ctx, f.inc.WorkItemID).Version)
}

func TestWorkItemRelationsIntakeMultiSourceCanonicalReplay(t *testing.T) {
	f, app, identity, command := newIntakeRelationFixture(t)
	// A second existing source owns a distinct source CAS within the same creation.
	command.SourceRelations = append(command.SourceRelations, creation.SourceRelationInput{SourceWorkItemID: f.problem.ID, ExpectedVersion: 1, RelationType: "related_to"})
	first, err := app.Create(f.ctx, identity, command)
	require.NoError(t, err)
	require.Equal(t, 2, f.client.Ticket.GetX(f.ctx, f.inc.WorkItemID).Version)
	require.Equal(t, 2, f.client.Ticket.GetX(f.ctx, f.problem.ID).Version)
	require.Equal(t, 1, f.client.Ticket.GetX(f.ctx, first.WorkItemID).Version)
	command.SourceRelations[0], command.SourceRelations[1] = command.SourceRelations[1], command.SourceRelations[0]
	replay, err := app.Create(f.ctx, identity, command)
	require.NoError(t, err)
	require.True(t, replay.Replayed)
	require.Equal(t, first.WorkItemID, replay.WorkItemID)
	require.Equal(t, 2, f.client.WorkItemRelation.Query().CountX(f.ctx))
	for _, change := range []func(*creation.SourceRelationInput){
		func(r *creation.SourceRelationInput) { r.ExpectedVersion++ },
		func(r *creation.SourceRelationInput) { r.RelationType = "parent_child" },
		func(r *creation.SourceRelationInput) { r.Metadata.Required = true },
	} {
		changed := command
		changed.SourceRelations = append([]creation.SourceRelationInput(nil), command.SourceRelations...)
		change(&changed.SourceRelations[0])
		_, err = app.Create(f.ctx, identity, changed)
		require.ErrorIs(t, err, creation.ErrIdempotencyConflict)
	}
	duplicate := command
	duplicate.IdempotencyKey = "duplicate-source"
	duplicate.SourceRelations = append(append([]creation.SourceRelationInput(nil), command.SourceRelations...), command.SourceRelations[0])
	_, err = app.Create(f.ctx, identity, duplicate)
	require.ErrorIs(t, err, creation.ErrInvalidCommand)
	// Later legitimate changes do not alter the immutable creation relation result.
	f.client.Ticket.UpdateOneID(f.problem.ID).AddVersion(1).ExecX(f.ctx)
	replay, err = app.Create(f.ctx, identity, command)
	require.NoError(t, err)
	require.Equal(t, first.WorkItemID, replay.WorkItemID)
}

func TestWorkItemRelationsIntakeLaterSourceFailureRollsBackAll(t *testing.T) {
	f, app, identity, command := newIntakeRelationFixture(t)
	command.SourceRelations = append(command.SourceRelations, creation.SourceRelationInput{SourceWorkItemID: f.problem.ID, ExpectedVersion: 1, RelationType: "related_to"})
	beforeTickets, beforeProblems := f.client.Ticket.Query().CountX(f.ctx), f.client.Problem.Query().CountX(f.ctx)
	reached := false
	f.runtime.Tenant.WorkItemRelation.Use(func(next ent.Mutator) ent.Mutator {
		return ent.MutateFunc(func(ctx context.Context, m ent.Mutation) (ent.Value, error) {
			value, err := next.Mutate(ctx, m)
			if err != nil {
				return value, err
			}
			source, _ := m.Field("source_work_item_id")
			if source == f.problem.ID {
				reached = true
				return nil, errors.New("second source relation failure")
			}
			return value, nil
		})
	})
	_, err := app.Create(f.ctx, identity, command)
	require.Error(t, err)
	require.True(t, reached)
	require.Equal(t, beforeTickets, f.client.Ticket.Query().CountX(f.ctx))
	require.Equal(t, beforeProblems, f.client.Problem.Query().CountX(f.ctx))
	require.Equal(t, 1, f.client.Ticket.GetX(f.ctx, f.inc.WorkItemID).Version)
	require.Equal(t, 1, f.client.Ticket.GetX(f.ctx, f.problem.ID).Version)
	require.Zero(t, f.client.WorkItemRelation.Query().CountX(f.ctx))
	require.Zero(t, f.client.AuditLog.Query().Where(auditlog.Action("work_item.relation_added")).CountX(f.ctx))
}

func TestWorkItemRelationsIntakeCompetitionAndStaleVersion(t *testing.T) {
	f, app, identity, command := newIntakeRelationFixture(t)
	before := f.client.Problem.Query().CountX(f.ctx)
	stale := command
	stale.SourceRelations = append([]creation.SourceRelationInput(nil), command.SourceRelations...)
	stale.SourceRelations[0].ExpectedVersion = 2
	_, err := app.Create(f.ctx, identity, stale)
	require.ErrorIs(t, err, creation.ErrSourceVersionConflict)
	start := make(chan struct{})
	var wg sync.WaitGroup
	results := make([]error, 2)
	for i := range 2 {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			<-start
			cmd := command
			cmd.IdempotencyKey = fmt.Sprintf("competitor-%d", i)
			_, results[i] = app.Create(f.ctx, identity, cmd)
		}(i)
	}
	close(start)
	wg.Wait()
	successes := 0
	for _, err := range results {
		if err == nil {
			successes++
		}
	}
	require.Equal(t, 1, successes)
	require.Equal(t, before+1, f.client.Problem.Query().CountX(f.ctx))
	require.Equal(t, 1, f.client.WorkItemRelation.Query().CountX(f.ctx))
	require.Equal(t, 2, f.client.Ticket.GetX(f.ctx, f.inc.WorkItemID).Version)
}

func TestWorkItemRelationsIntakeCurrentAuthorization(t *testing.T) {
	f, app, identity, command := newIntakeRelationFixture(t)
	f.actor = f.actor.Update().SetRole("relation_operator").SaveX(f.ctx)
	identity.Role = f.actor.Role
	role := f.client.Role.Create().SetTenantID(f.tenant.ID).SetCode(identity.Role).SetName("Relation operator").SaveX(f.ctx)
	grants := map[string]*ent.RolePermission{}
	for _, resource := range []string{"incident", "problem"} {
		for _, action := range []string{"read", "write"} {
			p := f.client.Permission.Create().SetTenantID(f.tenant.ID).SetCode(resource + ":" + action).SetName(resource + action).SetResource(resource).SetAction(action).SaveX(f.ctx)
			grants[resource+":"+action] = f.client.RolePermission.Create().SetTenantID(f.tenant.ID).SetRoleID(role.ID).SetPermissionID(p.ID).SaveX(f.ctx)
		}
	}
	first, err := app.Create(f.ctx, identity, command)
	require.NoError(t, err)
	for _, name := range []string{"incident:read", "incident:write", "problem:read", "problem:write"} {
		grant := grants[name]
		f.client.RolePermission.DeleteOneID(grant.ID).ExecX(f.ctx)
		_, err = app.Create(f.ctx, identity, command)
		require.ErrorIs(t, err, creation.ErrPermissionDenied, name)
		f.client.RolePermission.Create().SetTenantID(f.tenant.ID).SetRoleID(role.ID).SetPermissionID(grant.PermissionID).SaveX(f.ctx)
	}
	other := f.client.User.Create().SetTenantID(f.tenant.ID).SetUsername("other").SetName("Other").SetEmail("other@example.test").SetPasswordHash("test").SetRole("requester").SaveX(f.ctx)
	for _, id := range []int{f.inc.WorkItemID, first.WorkItemID} {
		f.client.Ticket.UpdateOneID(id).SetRequesterID(other.ID).ExecX(f.ctx)
		_, err = app.Create(f.ctx, identity, command)
		require.ErrorIs(t, err, creation.ErrReferenceNotFound)
		f.client.Ticket.UpdateOneID(id).SetRequesterID(f.actor.ID).ExecX(f.ctx)
	}
	f.client.User.UpdateOneID(f.actor.ID).SetActive(false).ExecX(f.ctx)
	_, err = app.Create(f.ctx, identity, command)
	require.ErrorIs(t, err, creation.ErrAuthenticationRequired)
	require.Equal(t, 1, f.client.WorkItemRelation.Query().CountX(f.ctx))
	require.Equal(t, 2, f.client.Ticket.GetX(f.ctx, f.inc.WorkItemID).Version)
}

func TestWorkItemRelationsIntakeSelectedTenantAllScope(t *testing.T) {
	f, app, identity, command := newIntakeRelationFixture(t)
	provider := f.client.Tenant.Create().SetCode("relation-provider").SetName("Provider").SetType("msp_provider").SetStatus("active").SaveX(f.ctx)
	actor := f.client.User.Create().SetTenantID(provider.ID).SetUsername("selected-admin").SetName("Selected admin").SetEmail("selected@example.test").SetPasswordHash("test").SetRole("super_admin").SetActive(true).SaveX(f.ctx)
	identity.ActorID, identity.Role = actor.ID, actor.Role
	first, err := app.Create(f.ctx, identity, command)
	require.NoError(t, err)
	replay, err := app.Create(f.ctx, identity, command)
	require.NoError(t, err)
	require.True(t, replay.Replayed)
	require.Equal(t, first.WorkItemID, replay.WorkItemID)
	relation := f.client.WorkItemRelation.Query().Where(workitemrelation.TargetWorkItemID(first.WorkItemID)).OnlyX(f.ctx)
	require.Equal(t, actor.ID, relation.CreatedByID)
	target := f.client.Ticket.GetX(f.ctx, first.WorkItemID)
	require.Equal(t, f.actor.ID, target.RequesterID)
	require.Zero(t, target.AssigneeID)
	f.client.User.UpdateOneID(actor.ID).SetActive(false).ExecX(f.ctx)
	_, err = app.Create(f.ctx, identity, command)
	require.ErrorIs(t, err, creation.ErrAuthenticationRequired)
}

func TestWorkItemRelationsIntakePreflightAndUnreadableTarget(t *testing.T) {
	f, app, identity, command := newIntakeRelationFixture(t)
	beforeTickets, beforeProblems := f.client.Ticket.Query().CountX(f.ctx), f.client.Problem.Query().CountX(f.ctx)
	for _, input := range []creation.SourceRelationInput{
		{SourceWorkItemID: f.inc.WorkItemID, ExpectedVersion: 1, RelationType: "unknown"},
		{SourceWorkItemID: f.problem.ID, ExpectedVersion: 1, RelationType: "investigated_by"},
		{SourceWorkItemID: f.inc.WorkItemID, ExpectedVersion: 1, RelationType: "investigated_by", Metadata: relationmeta.Metadata{Required: true}},
	} {
		cmd := command
		cmd.SourceRelations = []creation.SourceRelationInput{input}
		_, err := app.Create(f.ctx, identity, cmd)
		require.ErrorIs(t, err, creation.ErrDomainValidationFailed)
	}
	foreign := f.client.Tenant.Create().SetName("Foreign").SetCode("foreign-source").SaveX(f.ctx)
	other := f.client.User.Create().SetTenantID(foreign.ID).SetUsername("foreign").SetName("Foreign").SetEmail("foreign@example.test").SetPasswordHash("test").SaveX(f.ctx)
	source := f.client.Ticket.Create().SetTenantID(foreign.ID).SetRequesterID(other.ID).SetTitle("foreign").SetTicketNumber("FOREIGN-REL").SetRecordClass("incident").SetStatus("new").SetPriority("high").SaveX(f.ctx)
	cmd := command
	cmd.SourceRelations = []creation.SourceRelationInput{{SourceWorkItemID: source.ID, ExpectedVersion: 1, RelationType: "investigated_by"}}
	_, err := app.Create(f.ctx, identity, cmd)
	require.ErrorIs(t, err, creation.ErrReferenceNotFound)
	// Native operator can read the source and create on behalf, but cannot read
	// the new unassigned target. AddTx must roll back the complete creation.
	f.actor = f.actor.Update().SetRole("limited_operator").SaveX(f.ctx)
	identity.Role = f.actor.Role
	role := f.client.Role.Create().SetTenantID(f.tenant.ID).SetCode(identity.Role).SetName("Limited").SaveX(f.ctx)
	grant := f.client.Permission.Create().SetTenantID(f.tenant.ID).SetCode("all").SetName("All").SetResource("*").SetAction("*").SaveX(f.ctx)
	f.client.RolePermission.Create().SetTenantID(f.tenant.ID).SetRoleID(role.ID).SetPermissionID(grant.ID).SaveX(f.ctx)
	requester := f.client.User.Create().SetTenantID(f.tenant.ID).SetUsername("delegated").SetName("Delegated").SetEmail("delegated@example.test").SetPasswordHash("test").SetRole("requester").SaveX(f.ctx)
	identity.RequesterID = requester.ID
	_, err = app.Create(f.ctx, identity, command)
	require.ErrorIs(t, err, creation.ErrReferenceNotFound)
	require.Equal(t, beforeTickets+1, f.client.Ticket.Query().CountX(f.ctx))
	require.Equal(t, beforeProblems, f.client.Problem.Query().CountX(f.ctx))
	require.Zero(t, f.client.WorkItemRelation.Query().CountX(f.ctx))
	require.Equal(t, 1, f.client.Ticket.GetX(f.ctx, f.inc.WorkItemID).Version)
}
