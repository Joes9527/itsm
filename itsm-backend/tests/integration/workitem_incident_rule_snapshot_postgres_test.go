//go:build integration_postgres

package integration

import (
	"context"
	"database/sql"
	"fmt"
	"github.com/lib/pq"
	"github.com/stretchr/testify/require"
	"go.uber.org/zap"
	"itsm-backend/authorization"
	"itsm-backend/common/tenantctx"
	"itsm-backend/database"
	"itsm-backend/ent"
	"itsm-backend/service"
	"sync"
	"testing"
	"time"
)

// This barrier commits a second connection's authorization change after native
// actor/allocation reads but before the owning transaction's permission reads.
// There is no wall-clock scheduling assumption.
type ruleAuthorizationBarrier struct {
	database.DirectorySnapshot
	once   sync.Once
	change func() error
}

func (b *ruleAuthorizationBarrier) Open(ctx context.Context, tx *ent.Tx, tenantID int) (*ent.Client, func() error, error) {
	directory, closeDirectory, err := b.DirectorySnapshot.Open(ctx, tx, tenantID)
	if err != nil {
		return nil, nil, err
	}
	return directory, func() error {
		if err := closeDirectory(); err != nil {
			return err
		}
		var changeErr error
		b.once.Do(func() { changeErr = b.change() })
		return changeErr
	}, nil
}

func TestWorkItemIncidentRuleAuthorizationSnapshot(t *testing.T) {
	f := newIncidentEffectsFixture(t)
	clients, _ := runtimeClients(t, f)
	f.ctx = tenantctx.WithTenantID(f.ctx, f.tenant.ID)
	f.client.Tenant.UpdateOneID(f.tenant.ID).SetType("msp_customer").ExecX(f.ctx)
	provider := f.client.Tenant.Create().SetCode("snapshot-provider").SetName("Provider").SetType("msp_provider").SaveX(f.ctx)
	actor := f.client.User.Create().SetTenantID(provider.ID).SetUsername("snapshot-provider").SetName("Provider").SetEmail("snapshot@example.test").SetPasswordHash("test").SetRole("admin").SetMspRole("provider_agent").SetActive(true).SaveX(f.ctx)
	allocation := f.client.MSPAllocation.Create().SetMspUserID(actor.ID).SetCustomerTenantID(f.tenant.ID).SetRole("primary").SaveX(f.ctx)
	role := f.client.Role.Create().SetTenantID(f.tenant.ID).SetCode("msp_tech").SetName("MSP tech").SetIsActive(true).SaveX(f.ctx)
	permission := f.client.Permission.Create().SetTenantID(f.tenant.ID).SetCode("snapshot-incident-write").SetName("Incident write").SetResource("incident").SetAction("write").SaveX(f.ctx)
	authorization.InvalidateAllPermissionCaches()
	t.Cleanup(authorization.InvalidateAllPermissionCaches)
	changed := false
	barrier := &ruleAuthorizationBarrier{DirectorySnapshot: clients.IntakeDirectorySnapshot(), change: func() error {
		tx, err := f.client.Tx(f.ctx)
		if err != nil {
			return err
		}
		defer tx.Rollback()
		if err = tx.MSPAllocation.UpdateOneID(allocation.ID).SetDeassignedAt(time.Now()).Exec(f.ctx); err != nil {
			return err
		}
		if _, err = tx.RolePermission.Create().SetTenantID(f.tenant.ID).SetRoleID(role.ID).SetPermissionID(permission.ID).Save(f.ctx); err != nil {
			return err
		}
		if err = tx.Commit(); err == nil {
			changed = true
		}
		return err
	}}
	svc := service.NewIncidentService(clients.Tenant, zap.NewNop().Sugar())
	svc.SetDirectorySnapshot(barrier)
	rule := f.client.IncidentRule.Create().SetTenantID(f.tenant.ID).SetName("Snapshot status action").SetRuleType("automation").SetIsActive(true).SetConditions(map[string]interface{}{}).SetActions([]map[string]interface{}{{"type": "change_status", "status": "in_progress"}}).SaveX(f.ctx)
	before := f.client.Ticket.GetX(f.ctx, f.inc.WorkItemID)
	f.inc.Edges.WorkItem = before
	err := svc.RuleEngine().ExecuteRule(service.WithIncidentAlertActor(f.ctx, actor.ID, "incident_rule", "mixed-snapshot"), rule, f.inc, f.tenant.ID)
	require.True(t, changed, "barrier must atomically revoke allocation and grant permission")
	require.Error(t, err, "no database snapshot ever authorized both allocation and permission")
	after := f.client.Ticket.GetX(f.ctx, before.ID)
	require.Equal(t, before.Status, after.Status)
	require.Equal(t, before.Version, after.Version)
	require.Zero(t, f.client.AuditLog.Query().CountX(f.ctx))
	require.Zero(t, f.client.IncidentRuleActionReceipt.Query().CountX(f.ctx))
	require.False(t, f.client.MSPAllocation.GetX(f.ctx, allocation.ID).DeassignedAt.IsZero())
	require.Equal(t, 1, f.client.RolePermission.Query().CountX(f.ctx))
}

func TestWorkItemIncidentRuleCallerTransactionSnapshot(t *testing.T) {
	f := newIncidentEffectsFixture(t)
	clients, _ := runtimeClients(t, f)
	f.ctx = tenantctx.WithTenantID(f.ctx, f.tenant.ID)
	f.client.User.UpdateOneID(f.actor.ID).SetRole("super_admin").ExecX(f.ctx)
	before := f.client.Ticket.GetX(f.ctx, f.inc.WorkItemID)
	f.inc.Edges.WorkItem = before
	rule := f.client.IncidentRule.Create().SetTenantID(f.tenant.ID).SetName("Caller-owned status action").SetRuleType("automation").SetIsActive(true).SaveX(f.ctx)
	execution := f.client.IncidentRuleExecution.Create().SetTenantID(f.tenant.ID).SetRuleID(rule.ID).SetIncidentID(f.inc.ID).SetStatus("running").SetStartedAt(time.Now()).SetExecutionKind("rule").SetActorID(f.actor.ID).SetSource("http").SetSourceEventID(f.event.ID).SetExecutionKey(fmt.Sprintf("%s:rule:%d", f.event.EventID, rule.ID)).SetOutputData(map[string]interface{}{"conditionsMet": true}).SetFrozenActions([]map[string]interface{}{{"type": "change_status", "status": "in_progress"}}).SaveX(f.ctx)
	for _, isolation := range []sql.IsolationLevel{sql.LevelReadCommitted, sql.LevelRepeatableRead, sql.LevelSerializable} {
		t.Run(isolation.String(), func(t *testing.T) {
			tx, err := clients.Tenant.BeginTx(f.ctx, &sql.TxOptions{Isolation: isolation})
			require.NoError(t, err)
			defer tx.Rollback()
			// Caller work starts before ExecuteTx. The action must neither reset this
			// transaction's isolation nor commit its action receipt independently.
			_, err = tx.IncidentRuleActionReceipt.Create().SetTenantID(f.tenant.ID).SetExecutionID(execution.ID).SetActionIndex(0).Save(f.ctx)
			require.NoError(t, err)
			action := &service.StatusChangeAction{Status: "in_progress"}
			action.SetDirectorySnapshot(clients.IntakeDirectorySnapshot())
			err = action.ExecuteTx(service.WithIncidentAlertActor(f.ctx, f.actor.ID, "incident_rule", "caller-"+isolation.String()), tx, f.inc, f.tenant.ID)
			if isolation == sql.LevelReadCommitted {
				require.ErrorContains(t, err, "requires repeatable read or serializable")
			} else {
				require.NoError(t, err)
				require.Equal(t, "in_progress", tx.Ticket.GetX(f.ctx, before.ID).Status)
			}
			require.Equal(t, before.Status, f.client.Ticket.GetX(f.ctx, before.ID).Status, "action must not commit caller transaction")
			require.NoError(t, tx.Rollback())
			after := f.client.Ticket.GetX(f.ctx, before.ID)
			require.Equal(t, before.Status, after.Status)
			require.Equal(t, before.Version, after.Version)
			require.Zero(t, f.client.AuditLog.Query().CountX(f.ctx))
			require.Zero(t, f.client.IncidentRuleActionReceipt.Query().CountX(f.ctx))
			require.Equal(t, 1, f.client.OutboxEvent.Query().CountX(f.ctx), "only the fixture's initial created event remains")
		})
	}
}

func TestWorkItemIncidentRuleSnapshotRetryExhaustion(t *testing.T) {
	f := newIncidentEffectsFixture(t)
	f.rule(metricAction("retry-rollback"))
	attempts := 0
	f.client.IncidentRuleActionReceipt.Use(func(next ent.Mutator) ent.Mutator {
		return ent.MutateFunc(func(ctx context.Context, m ent.Mutation) (ent.Value, error) {
			if _, err := next.Mutate(ctx, m); err != nil {
				return nil, err
			}
			attempts++
			return nil, &pq.Error{Code: "40001", Message: "injected serialization failure after action receipt"}
		})
	})
	err := f.engine.Deliver(f.ctx, f.event)
	require.ErrorContains(t, err, "injected serialization failure")
	require.Equal(t, 50, attempts, "existing Outbox local contention bound must not become an unbounded action loop")
	require.Zero(t, f.client.IncidentMetric.Query().CountX(f.ctx))
	require.Zero(t, f.client.IncidentRuleActionReceipt.Query().CountX(f.ctx))
	require.Zero(t, f.client.AuditLog.Query().CountX(f.ctx))
}
