//go:build integration_postgres

package integration

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
	"itsm-backend/common/tenantctx"
	"itsm-backend/database"
	"itsm-backend/ent"
	"itsm-backend/ent/auditlog"
	"itsm-backend/ent/workitemrelation"
	"itsm-backend/handlers/shared/workitemmutation"
	"itsm-backend/migration"
	"itsm-backend/service"
)

type relationFixture struct {
	*incidentEffectsFixture
	runtime *database.RuntimeClients
	owner   *service.WorkItemRelationService
	problem *ent.Ticket
}

func newRelationFixture(t *testing.T) *relationFixture {
	t.Helper()
	f := newIncidentEffectsFixture(t)
	_, err := f.db.ExecContext(f.ctx, migration.GetMigrationSQL("032_workitem_sla_cycle"))
	require.NoError(t, err)
	f.actor = f.actor.Update().SetRole("super_admin").SaveX(f.ctx)
	item := f.client.Ticket.Create().SetTenantID(f.tenant.ID).SetRequesterID(f.actor.ID).SetOpenedByID(f.actor.ID).SetTitle("problem").SetTicketNumber("PRB-RELATION").SetRecordClass("problem").SetStatus("open").SetPriority("high").SaveX(f.ctx)
	f.client.Problem.Create().SetWorkItemID(item.ID).SaveX(f.ctx)
	clients, cfg := runtimeClients(t, f)
	for _, table := range []string{"work_item_relations", "process_instances", "process_callback_outboxes", "process_tasks"} {
		_, err = f.db.ExecContext(f.ctx, "GRANT SELECT,INSERT,UPDATE,DELETE ON "+table+" TO "+cfg.User)
		require.NoError(t, err)
		_, err = f.db.ExecContext(f.ctx, "GRANT USAGE ON SEQUENCE "+table+"_id_seq TO "+cfg.User)
		require.NoError(t, err)
	}
	f.ctx = tenantctx.WithTenantID(f.ctx, f.tenant.ID)
	return &relationFixture{f, clients, service.NewWorkItemRelationService(clients.Tenant, clients.IntakeDirectorySnapshot()), item}
}

func (f *relationFixture) command(key string) service.RelationCommand {
	return service.RelationCommand{Meta: workitemmutation.Meta{TenantID: f.tenant.ID, ActorID: f.actor.ID, ExpectedVersion: f.client.Ticket.GetX(f.ctx, f.inc.WorkItemID).Version, Source: "http", OperationID: key, CorrelationID: "relation-test"}, SourceID: f.inc.WorkItemID, TargetID: f.problem.ID, Type: "investigated_by"}
}

func TestWorkItemRelationsConcurrencyReplayAndRelink(t *testing.T) {
	f := newRelationFixture(t)
	second := f.client.Ticket.Create().SetTenantID(f.tenant.ID).SetRequesterID(f.actor.ID).SetOpenedByID(f.actor.ID).SetTitle("other problem").SetTicketNumber("PRB-OTHER").SetRecordClass("problem").SetStatus("open").SetPriority("high").SaveX(f.ctx)
	f.client.Problem.Create().SetWorkItemID(second.ID).SaveX(f.ctx)
	cmds := []service.RelationCommand{f.command("race-a"), f.command("race-b")}
	cmds[1].TargetID = second.ID
	start := make(chan struct{})
	var wg sync.WaitGroup
	errs := make([]error, 2)
	for i := range cmds {
		wg.Add(1)
		go func(i int) { defer wg.Done(); <-start; _, errs[i] = f.owner.Apply(f.ctx, cmds[i], false) }(i)
	}
	close(start)
	wg.Wait()
	winner := 0
	if errs[0] != nil {
		winner = 1
	}
	require.NoError(t, errs[winner])
	require.Error(t, errs[1-winner])
	require.Equal(t, 1, f.client.WorkItemRelation.Query().CountX(f.ctx))
	require.Equal(t, cmds[winner].Meta.ExpectedVersion+1, f.client.Ticket.GetX(f.ctx, f.inc.WorkItemID).Version)
	replay, err := f.owner.Apply(f.ctx, cmds[winner], false)
	require.NoError(t, err)
	require.True(t, replay.Replayed)
	f.client.Ticket.UpdateOneID(cmds[winner].TargetID).SetStatus("closed").ExecX(f.ctx)
	occupied := cmds[1-winner]
	occupied.Meta.ExpectedVersion = replay.Version
	occupied.Meta.OperationID = "occupied-even-when-target-closed"
	_, err = f.owner.Apply(f.ctx, occupied, false)
	require.Error(t, err, "live investigated_by cardinality is independent of target status")
	require.Equal(t, replay.Version, f.client.Ticket.GetX(f.ctx, f.inc.WorkItemID).Version)
	conflict := cmds[winner]
	conflict.TargetID = cmds[1-winner].TargetID
	_, err = f.owner.Apply(f.ctx, conflict, false)
	require.ErrorContains(t, err, "operationId")
	stale := cmds[winner]
	stale.Meta.OperationID = "stale"
	_, err = f.owner.Apply(f.ctx, stale, false)
	require.Error(t, err)
	remove := cmds[winner]
	remove.Meta.ExpectedVersion = replay.Version
	remove.Meta.OperationID = "remove"
	result, err := f.owner.Apply(f.ctx, remove, true)
	require.NoError(t, err)
	add := remove
	add.Meta.ExpectedVersion = result.Version
	add.Meta.OperationID = "relink"
	_, err = f.owner.Apply(f.ctx, add, false)
	require.NoError(t, err)
	require.Equal(t, 2, f.client.WorkItemRelation.Query().CountX(f.ctx))
	require.Equal(t, 1, f.client.WorkItemRelation.Query().Where(workitemrelation.DeletedAtIsNil()).CountX(f.ctx))
	replayed, err := f.owner.Apply(f.ctx, cmds[winner], false)
	require.NoError(t, err)
	require.Equal(t, replay.Version, replayed.Version, "replay returns immutable first result")
}

func TestWorkItemRelationsSameOperationRace(t *testing.T) {
	f := newRelationFixture(t)
	cmd := f.command("same-operation")
	// Force both business snapshots to authorize before either attempts CAS.
	ready := make(chan struct{}, 2)
	release := make(chan struct{})
	f.runtime.Tenant.Ticket.Use(func(next ent.Mutator) ent.Mutator {
		return ent.MutateFunc(func(ctx context.Context, m ent.Mutation) (ent.Value, error) {
			ready <- struct{}{}
			<-release
			return next.Mutate(ctx, m)
		})
	})
	var wg sync.WaitGroup
	errs := make([]error, 2)
	results := make([]workitemmutation.Result, 2)
	for i := range errs {
		wg.Add(1)
		go func(i int) { defer wg.Done(); results[i], errs[i] = f.owner.Apply(f.ctx, cmd, false) }(i)
	}
	for i := 0; i < 2; i++ {
		select {
		case <-ready:
		case <-time.After(10 * time.Second):
			t.Fatal("CAS barrier was not reached")
		}
	}
	close(release)
	wg.Wait()
	for _, err := range errs {
		require.NoError(t, err)
	}
	require.NotEqual(t, results[0].Replayed, results[1].Replayed)
	require.Equal(t, results[0].Version, results[1].Version)
	require.Equal(t, 1, f.client.WorkItemRelation.Query().CountX(f.ctx))
	require.Equal(t, 1, f.client.AuditLog.Query().CountX(f.ctx))
}

func TestWorkItemRelationsSymmetricAndCallerTransaction(t *testing.T) {
	f := newRelationFixture(t)
	cmd := f.command("symmetric")
	cmd.Type = "related_to"
	result, err := f.owner.Apply(f.ctx, cmd, false)
	require.NoError(t, err)
	reverse := cmd
	reverse.SourceID, reverse.TargetID = cmd.TargetID, cmd.SourceID
	reverse.Meta.ExpectedVersion = f.problem.Version
	reverse.Meta.OperationID = "reverse"
	_, err = f.owner.Apply(f.ctx, reverse, false)
	require.Error(t, err)
	require.Equal(t, f.problem.Version, f.client.Ticket.GetX(f.ctx, f.problem.ID).Version, "failed reverse add must roll back its own source")
	tx, err := f.runtime.Tenant.BeginTx(f.ctx, &sql.TxOptions{Isolation: sql.LevelRepeatableRead})
	require.NoError(t, err)
	rows, err := f.owner.ListTx(f.ctx, tx, cmd.Meta, f.problem.ID)
	require.NoError(t, err)
	require.Len(t, rows, 1)
	require.NoError(t, tx.Rollback())
	removed, err := f.owner.Apply(f.ctx, reverse, true)
	require.NoError(t, err)
	reverse.Meta.ExpectedVersion = removed.Version
	reverse.Meta.OperationID = "relate-again"
	_, err = f.owner.Apply(f.ctx, reverse, false)
	require.NoError(t, err)
	row := f.client.WorkItemRelation.Query().Where(workitemrelation.DeletedAtIsNil()).OnlyX(f.ctx)
	require.Less(t, row.SourceWorkItemID, row.TargetWorkItemID)
	var facts service.RelationFacts
	audit := f.client.AuditLog.Query().Where(auditlog.OperationID("relate-again")).OnlyX(f.ctx)
	require.NoError(t, json.Unmarshal([]byte(*audit.RequestBody), &facts))
	require.Equal(t, row.SourceWorkItemID, facts.SourceID, "receipt must identify the stored tuple independently of the mutated endpoint")
	require.Equal(t, row.TargetWorkItemID, facts.TargetID)
	require.Equal(t, reverse.SourceID, facts.MutationWorkItemID)

	// External owner sees both effects inside its RR transaction, then can
	// roll back relation, soft removal, source versions and receipts together.
	tx, err = f.runtime.Tenant.BeginTx(f.ctx, &sql.TxOptions{Isolation: sql.LevelRepeatableRead})
	require.NoError(t, err)
	defer tx.Rollback()
	inner := cmd
	inner.Type = "investigated_by"
	inner.Meta.OperationID = "caller-add"
	inner.Meta.ExpectedVersion = result.Version
	require.NoError(t, f.owner.AddTx(f.ctx, tx, inner))
	inner.Meta.ExpectedVersion++
	inner.Meta.OperationID = "caller-remove"
	require.NoError(t, f.owner.RemoveTx(f.ctx, tx, inner))
	require.Equal(t, 3, tx.WorkItemRelation.Query().CountX(f.ctx))
	require.NoError(t, tx.Rollback())
	require.Equal(t, 2, f.client.WorkItemRelation.Query().CountX(f.ctx))
	require.Equal(t, result.Version, f.client.Ticket.GetX(f.ctx, cmd.SourceID).Version)
	require.Zero(t, f.client.AuditLog.Query().Where(auditlog.OperationIDIn("caller-add", "caller-remove")).CountX(f.ctx))
}

func TestWorkItemRelationsAuthorityAndAtomicRollback(t *testing.T) {
	f := newRelationFixture(t)
	before := f.client.Ticket.GetX(f.ctx, f.inc.WorkItemID)
	foreign := f.client.Tenant.Create().SetCode("relation-foreign").SetName("foreign").SaveX(f.ctx)
	target := f.client.Ticket.Create().SetTenantID(foreign.ID).SetRequesterID(f.actor.ID).SetOpenedByID(f.actor.ID).SetTitle("hidden").SetTicketNumber("PRB-HIDDEN").SetRecordClass("problem").SetStatus("open").SetPriority("high").SaveX(f.ctx)
	bad := f.command("foreign")
	bad.TargetID = target.ID
	_, err := f.owner.Apply(f.ctx, bad, false)
	require.Error(t, err)
	bad = f.command("deleted")
	f.problem.Update().SetDeletedAt(time.Now()).ExecX(f.ctx)
	_, err = f.owner.Apply(f.ctx, bad, false)
	require.Error(t, err)
	f.problem.Update().ClearDeletedAt().ExecX(f.ctx)
	for _, fault := range []string{"relation", "audit"} {
		t.Run(fault, func(t *testing.T) {
			var enabled = true
			hook := func(next ent.Mutator) ent.Mutator {
				return ent.MutateFunc(func(ctx context.Context, m ent.Mutation) (ent.Value, error) {
					if enabled {
						return nil, errors.New("injected " + fault + " fault")
					}
					return next.Mutate(ctx, m)
				})
			}
			if fault == "relation" {
				f.runtime.Tenant.WorkItemRelation.Use(hook)
			} else {
				f.runtime.Tenant.AuditLog.Use(hook)
			}
			_, err := f.owner.Apply(f.ctx, f.command("fault-"+fault), false)
			require.ErrorContains(t, err, "injected "+fault+" fault")
			enabled = false
			require.Zero(t, f.client.WorkItemRelation.Query().CountX(f.ctx))
			require.Equal(t, before.Version, f.client.Ticket.GetX(f.ctx, before.ID).Version)
			require.Zero(t, f.client.AuditLog.Query().CountX(f.ctx))
		})
	}
	cmd := f.command("authorized")
	_, err = f.owner.Apply(f.ctx, cmd, false)
	require.NoError(t, err)
	f.actor.Update().SetActive(false).ExecX(f.ctx)
	_, err = f.owner.Apply(f.ctx, cmd, false)
	require.Error(t, err, "replay must authorize current actor")
}

func TestWorkItemRelationsRequiredMetadataAndReadVisibility(t *testing.T) {
	f := newRelationFixture(t)
	change := f.client.Ticket.Create().SetTenantID(f.tenant.ID).SetRequesterID(f.actor.ID).SetOpenedByID(f.actor.ID).SetTitle("fix").SetTicketNumber("CHG-AUTHORITY").SetRecordClass("change_request").SetStatus("draft").SetPriority("high").SaveX(f.ctx)
	cmd := f.command("required")
	cmd.SourceID = f.problem.ID
	cmd.TargetID = change.ID
	cmd.Type = "resolved_by_change"
	cmd.Required = true
	cmd.Meta.ExpectedVersion = f.problem.Version
	_, err := f.owner.Apply(f.ctx, cmd, false)
	require.NoError(t, err)
	read := func() ([]service.RelationView, error) {
		tx, err := f.runtime.Tenant.BeginTx(f.ctx, &sql.TxOptions{Isolation: sql.LevelRepeatableRead})
		require.NoError(t, err)
		defer tx.Rollback()
		return f.owner.ListTx(f.ctx, tx, cmd.Meta, f.problem.ID)
	}
	rows, err := read()
	require.NoError(t, err)
	require.Len(t, rows, 1)
	require.True(t, rows[0].Required)
	require.Equal(t, "CHG-AUTHORITY", rows[0].Target.Number)
	audit := f.client.AuditLog.Query().Where(auditlog.OperationID("required")).OnlyX(f.ctx)
	var facts service.RelationFacts
	require.NoError(t, json.Unmarshal([]byte(*audit.RequestBody), &facts))
	require.True(t, facts.Required)
	require.Equal(t, rows[0].ID, facts.RelationID)
	require.Equal(t, f.actor.ID, facts.ActorID)
	require.Equal(t, f.tenant.ID, facts.ActorTenantID)
	changed := cmd
	changed.Required = false
	_, err = f.owner.Apply(f.ctx, changed, false)
	require.ErrorContains(t, err, "operationId")
	role := f.client.Role.Create().SetTenantID(f.tenant.ID).SetCode("relation-reader").SetName("reader").SetIsActive(true).SaveX(f.ctx)
	permission := f.client.Permission.Create().SetTenantID(f.tenant.ID).SetCode("problem:read").SetName("read").SetResource("problem").SetAction("read").SaveX(f.ctx)
	f.client.RolePermission.Create().SetTenantID(f.tenant.ID).SetRoleID(role.ID).SetPermissionID(permission.ID).SaveX(f.ctx)
	f.actor.Update().SetRole(role.Code).ExecX(f.ctx)
	_, err = read()
	require.Error(t, err, "target class permission required")
	_, err = f.owner.Apply(f.ctx, cmd, false)
	require.Error(t, err, "replay requires source write permission")
}

func TestWorkItemRelationsAllocatedMSP(t *testing.T) {
	f := newRelationFixture(t)
	f.client.Tenant.UpdateOneID(f.tenant.ID).SetType("msp_customer").ExecX(f.ctx)
	provider := f.client.Tenant.Create().SetCode("relation-provider").SetName("provider").SetType("msp_provider").SaveX(f.ctx)
	actor := f.client.User.Create().SetTenantID(provider.ID).SetUsername("relation-msp").SetName("operator").SetEmail("relation-msp@example.test").SetPasswordHash("test").SetRole("admin").SetMspRole("provider_agent").SetActive(true).SaveX(f.ctx)
	allocation := f.client.MSPAllocation.Create().SetMspUserID(actor.ID).SetCustomerTenantID(f.tenant.ID).SetRole("primary").SaveX(f.ctx)
	role := f.client.Role.Create().SetTenantID(f.tenant.ID).SetCode("msp_tech").SetName("MSP tech").SetIsActive(true).SaveX(f.ctx)
	for _, grant := range []struct{ resource, action string }{{"incident", "read"}, {"incident", "write"}, {"problem", "read"}} {
		permission := f.client.Permission.Create().SetTenantID(f.tenant.ID).SetCode(grant.resource + ":" + grant.action).SetName(fmt.Sprint(grant)).SetResource(grant.resource).SetAction(grant.action).SaveX(f.ctx)
		f.client.RolePermission.Create().SetTenantID(f.tenant.ID).SetRoleID(role.ID).SetPermissionID(permission.ID).SaveX(f.ctx)
	}
	_, err := f.runtime.Tenant.User.Get(f.ctx, actor.ID)
	require.True(t, ent.IsNotFound(err))
	cmd := f.command("msp")
	cmd.Meta.ActorID = actor.ID
	_, err = f.owner.Apply(f.ctx, cmd, false)
	require.Error(t, err, "allocation does not expand existing row policy")
	// This directly populated assignment is a row-predicate fixture, not proof
	// that customer intake or the assignment owner accepts a foreign recipient.
	f.client.Ticket.UpdateOneID(cmd.SourceID).SetAssigneeID(actor.ID).ExecX(f.ctx)
	f.client.Ticket.UpdateOneID(cmd.TargetID).SetAssigneeID(actor.ID).ExecX(f.ctx)
	_, err = f.owner.Apply(f.ctx, cmd, false)
	require.NoError(t, err)
	audit := f.client.AuditLog.Query().Where(auditlog.OperationID("msp")).OnlyX(f.ctx)
	var facts service.RelationFacts
	require.NoError(t, json.Unmarshal([]byte(*audit.RequestBody), &facts))
	require.Equal(t, provider.ID, facts.ActorTenantID)
	f.client.MSPAllocation.DeleteOne(allocation).ExecX(f.ctx)
	_, err = f.owner.Apply(f.ctx, cmd, false)
	require.Error(t, err, "revoked allocation must deny replay")
}

func TestWorkItemRelationsAllScopeSelectedTenantActor(t *testing.T) {
	f := newRelationFixture(t)
	provider := f.client.Tenant.Create().SetCode("relation-super-provider").SetName("provider").SetType("msp_provider").SaveX(f.ctx)
	actor := f.client.User.Create().SetTenantID(provider.ID).SetUsername("relation-super").SetName("operator").SetEmail("relation-super@example.test").SetPasswordHash("test").SetRole("super_admin").SetActive(true).SaveX(f.ctx)
	cmd := f.command("all-scope-selected-tenant")
	cmd.Meta.ActorID = actor.ID
	_, err := f.runtime.Tenant.User.Get(f.ctx, actor.ID)
	require.True(t, ent.IsNotFound(err))
	_, err = f.owner.Apply(f.ctx, cmd, false)
	require.NoError(t, err)
	require.Equal(t, f.actor.ID, f.client.Ticket.GetX(f.ctx, cmd.SourceID).RequesterID, "actor need not be requester under existing all-scope policy")
}

func TestWorkItemRelationsOwnedOrAssignedReadScope(t *testing.T) {
	f := newRelationFixture(t)
	role := f.client.Role.Create().SetTenantID(f.tenant.ID).SetCode("relation-agent").SetName("agent").SetIsActive(true).SaveX(f.ctx)
	for _, grant := range []struct{ resource, action string }{{"incident", "read"}, {"incident", "write"}, {"problem", "read"}} {
		permission := f.client.Permission.Create().SetTenantID(f.tenant.ID).SetCode(grant.resource + ":" + grant.action).SetName(fmt.Sprint(grant)).SetResource(grant.resource).SetAction(grant.action).SaveX(f.ctx)
		f.client.RolePermission.Create().SetTenantID(f.tenant.ID).SetRoleID(role.ID).SetPermissionID(permission.ID).SaveX(f.ctx)
	}
	viewer := f.client.User.Create().SetTenantID(f.tenant.ID).SetUsername("row-agent").SetName("row agent").SetEmail("row-agent@example.test").SetPasswordHash("test").SetRole(role.Code).SetActive(true).SaveX(f.ctx)
	cmd := f.command("hidden-source")
	cmd.Meta.ActorID = viewer.ID
	_, err := f.owner.Apply(f.ctx, cmd, false)
	require.Error(t, err, "class ACL cannot expose another requester's unassigned source")
	f.client.Ticket.UpdateOneID(cmd.SourceID).SetAssigneeID(viewer.ID).ExecX(f.ctx)
	cmd.Meta.OperationID = "hidden-target"
	_, err = f.owner.Apply(f.ctx, cmd, false)
	require.Error(t, err, "read scope must cover target too")
	f.problem.Update().SetAssigneeID(viewer.ID).ExecX(f.ctx)
	cmd.Meta.OperationID = "assigned-both"
	_, err = f.owner.Apply(f.ctx, cmd, false)
	require.NoError(t, err)
	read := func() error {
		tx, err := f.runtime.Tenant.BeginTx(f.ctx, &sql.TxOptions{Isolation: sql.LevelRepeatableRead})
		require.NoError(t, err)
		defer tx.Rollback()
		_, err = f.owner.ListTx(f.ctx, tx, cmd.Meta, cmd.SourceID)
		return err
	}
	require.NoError(t, read())
	f.problem.Update().ClearAssigneeID().ExecX(f.ctx)
	require.Error(t, read(), "read projection cannot leak hidden target")
	_, err = f.owner.Apply(f.ctx, cmd, false)
	require.Error(t, err, "replay cannot bypass revoked row visibility")
}
