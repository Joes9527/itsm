package service

import (
	"context"
	"errors"
	"testing"

	"itsm-backend/ent"
	"itsm-backend/ent/processauditlog"
	"itsm-backend/ent/processcallbackoutbox"

	"github.com/stretchr/testify/require"
)

func TestStartProcessTxCallerOwnsCommit(t *testing.T) {
	for _, finish := range []string{"rollback", "commit", "commit_failure"} {
		t.Run(finish, func(t *testing.T) {
			f := newBPMNAuthorizationFixture(t)
			handler := newCountingIdempotentCallbackHandler("tx_start", "tx_start_handler", 0)
			f.engine.CallbackRegistry().RegisterHandler(handler)
			configureStartProcessDefinition(t, f, startProcessServiceTaskXML(handler.GetTaskType()))
			ctx := startProcessContext(f)
			tx, err := f.client.Tx(ctx)
			require.NoError(t, err)
			defer tx.Rollback()
			instance, err := f.engine.StartProcessTx(ctx, tx, f.definition.Key, "tx-start", "ticket", 104, map[string]interface{}{})
			require.NoError(t, err)
			require.Zero(t, handler.AttemptCount(), "effects must not run before owner commits")
			require.Equal(t, 1, tx.ProcessCallbackOutbox.Query().CountX(ctx))
			switch finish {
			case "rollback":
				require.NoError(t, tx.Rollback())
				assertNoStartedProcessState(t, f)
			case "commit_failure":
				forced := errors.New("owner commit refusal")
				tx.OnCommit(func(ent.Committer) ent.Committer {
					return ent.CommitFunc(func(context.Context, *ent.Tx) error { return forced })
				})
				require.ErrorIs(t, tx.Commit(), forced)
				require.Zero(t, handler.AttemptCount())
				require.NoError(t, tx.Rollback())
				assertNoStartedProcessState(t, f)
			case "commit":
				require.NoError(t, tx.Commit())
				require.Equal(t, 1, handler.AttemptCount())
				require.Equal(t, bpmnCallbackStatusCompleted, callbackRowForInstance(t, f, instance.ID).Status)
			}
		})
	}
}

func TestCompleteTaskTxCallerOwnsCommit(t *testing.T) {
	for _, commit := range []bool{false, true} {
		t.Run(map[bool]string{false: "rollback", true: "commit"}[commit], func(t *testing.T) {
			f := newBPMNAuthorizationFixture(t)
			handler := newCountingIdempotentCallbackHandler("tx_complete", "tx_complete_handler", 0)
			task, instance := seedDurableServiceCallbackTask(t, f, "tx-completion", handler)
			ctx := f.typedTaskScopeOnlyCtx(f.actor, false)
			tx, err := f.client.Tx(ctx)
			require.NoError(t, err)
			defer tx.Rollback()
			require.NoError(t, f.engine.CompleteTaskTx(ctx, tx, task.TaskID, map[string]interface{}{"approved": true}))
			require.Zero(t, handler.AttemptCount())
			require.Equal(t, "completed", tx.ProcessTask.GetX(ctx, task.ID).Status)
			if !commit {
				require.NoError(t, tx.Rollback())
				require.Equal(t, task.Status, f.client.ProcessTask.GetX(f.userCtx, task.ID).Status)
				require.Zero(t, f.client.ProcessAuditLog.Query().CountX(f.userCtx))
				require.Zero(t, f.client.ProcessCallbackOutbox.Query().CountX(f.userCtx))
				return
			}
			require.NoError(t, tx.Commit())
			require.Equal(t, 1, handler.AttemptCount())
			require.Equal(t, bpmnCallbackStatusCompleted, callbackRowForInstance(t, f, instance.ID).Status)
			require.Equal(t, f.actor.ID, f.client.ProcessAuditLog.Query().Where(processauditlog.Action(AuditActionTaskCompleted)).OnlyX(f.userCtx).UserID)
			duplicate, err := f.client.Tx(ctx)
			require.NoError(t, err)
			require.Error(t, f.engine.CompleteTaskTx(ctx, duplicate, task.TaskID, map[string]interface{}{"approved": true}))
			require.NoError(t, duplicate.Rollback())
			require.Equal(t, 1, f.client.ProcessCallbackOutbox.Query().Where(processcallbackoutbox.ProcessInstanceID(instance.ID)).CountX(f.userCtx))
			require.Equal(t, 1, handler.AttemptCount())
		})
	}
}
