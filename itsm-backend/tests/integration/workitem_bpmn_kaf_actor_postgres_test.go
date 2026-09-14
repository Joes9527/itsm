//go:build integration_postgres

package integration

import (
	"context"
	"errors"
	"fmt"
	"testing"
	"time"

	"github.com/lib/pq"
	"github.com/stretchr/testify/require"
	"itsm-backend/ent"
	"itsm-backend/ent/processauditlog"
	"itsm-backend/service"
	"itsm-backend/service/bpmn"
)

func TestWorkItemBPMNKafCompletionDirectoryOwner(t *testing.T) {
	for _, rollback := range []bool{false, true} {
		t.Run(fmt.Sprintf("rollback_%t", rollback), func(t *testing.T) {
			f := newChangeLifecycleFixture(t, "normal")
			lookup, err := f.runtime.Tx(f.ctx)
			require.NoError(t, err)
			defer lookup.Rollback()
			rows, err := lookup.QueryContext(f.ctx, "SELECT current_user")
			require.NoError(t, err)
			require.True(t, rows.Next())
			var runtimeRole string
			require.NoError(t, rows.Scan(&runtimeRole))
			require.NoError(t, rows.Close())
			require.NoError(t, lookup.Rollback())
			for _, table := range []string{"kaf_task_action_ledgers", "kaf_task_completion_receipts"} {
				_, err = f.db.ExecContext(f.ctx, "GRANT SELECT,INSERT,UPDATE,DELETE ON "+pq.QuoteIdentifier(table)+" TO "+pq.QuoteIdentifier(runtimeRole))
				require.NoError(t, err)
				var sequence string
				require.NoError(t, f.db.QueryRowContext(f.ctx, "SELECT pg_get_serial_sequence($1,'id')", table).Scan(&sequence))
				_, err = f.db.ExecContext(f.ctx, "GRANT USAGE ON SEQUENCE "+sequence+" TO "+pq.QuoteIdentifier(runtimeRole))
				require.NoError(t, err)
			}
			f.apply(t, f.command("submit", "kaf-source"))
			instance := f.client.ProcessInstance.Query().OnlyX(f.ctx)
			task := f.client.ProcessTask.Query().OnlyX(f.ctx)
			// Seed the delegated transport boundary, as existing KAF completion
			// tests do. The actual public completion owns all subsequent writes.
			task = task.Update().SetTaskType(bpmn.KafDelegateTaskType).SetStatus("delegated").SetCorrelationID("directory-kaf").SaveX(f.ctx)
			actor := f.client.User.Create().SetTenantID(f.tenant.ID).SetUsername("kaf-agent").SetName("KAF actor").SetEmail("kaf@example.test").SetPasswordHash("test").SetRole("kaf_automation").SetActive(true).SaveX(f.ctx)
			ledger := f.client.KafTaskActionLedger.Create().SetTenantID(f.tenant.ID).SetTaskID(task.TaskID).
				SetRunID("run-directory").SetStepID("finish").SetAction("complete_bpmn_task").
				SetIdempotencyKey(fmt.Sprintf("%d:%s:run-directory:finish", f.tenant.ID, task.TaskID)).
				SetCorrelationID(task.CorrelationID).SetProcedureRef("directory-test").SetProcedureVersion("1").
				SetResultStatus("executing").SetLeaseOwner("directory-owner").SetLeaseExpiresAt(time.Now().Add(time.Minute)).SaveX(f.ctx)
			forced := errors.New("refused KAF audit")
			if rollback {
				f.runtime.ProcessAuditLog.Use(func(next ent.Mutator) ent.Mutator {
					return ent.MutateFunc(func(ctx context.Context, mutation ent.Mutation) (ent.Value, error) { return nil, forced })
				})
			}
			ctx := changeCallbackContext(f, actor)
			err = f.engine.CompleteKafDelegatedTask(ctx, ledger.ID, ledger.LeaseOwner, task.TaskID, nil)
			if rollback {
				require.ErrorIs(t, err, forced)
				require.Equal(t, "delegated", f.client.ProcessTask.GetX(f.ctx, task.ID).Status)
				require.Zero(t, f.client.KafTaskCompletionReceipt.Query().CountX(f.ctx))
				require.Zero(t, f.client.ProcessAuditLog.Query().Where(processauditlog.UserID(actor.ID)).CountX(f.ctx))
				require.Equal(t, instance.Version, f.client.ProcessInstance.GetX(f.ctx, instance.ID).Version)
				return
			}
			require.NoError(t, err)
			require.Equal(t, "completed", f.client.ProcessTask.GetX(f.ctx, task.ID).Status)
			require.Equal(t, "callback_succeeded", f.client.KafTaskCompletionReceipt.Query().OnlyX(f.ctx).Status)
			audit := f.client.ProcessAuditLog.Query().Where(processauditlog.UserID(actor.ID), processauditlog.Action(service.AuditActionTaskCompleted)).OnlyX(f.ctx)
			require.Equal(t, f.tenant.ID, audit.TenantID)
			require.NoError(t, f.engine.CompleteKafDelegatedTask(ctx, ledger.ID, ledger.LeaseOwner, task.TaskID, nil))
			require.Equal(t, 1, f.client.KafTaskCompletionReceipt.Query().CountX(f.ctx))
			require.Equal(t, 1, f.client.ProcessAuditLog.Query().Where(processauditlog.UserID(actor.ID), processauditlog.Action(service.AuditActionTaskCompleted)).CountX(f.ctx))
		})
	}
}
