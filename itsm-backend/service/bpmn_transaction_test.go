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

// C1: 保留变量属于流程身份（设计 §15.2.2 规则 6），BPMN 表单/脚本不能覆盖。
//
// 两条边界行为不同且都是有意的：
//   - 参与者表单路径（rejectReserved=false）会剥离保留键：提交能成功，但身份键绝不进入
//     完成变量，因此覆盖不可能生效；
//   - 实例变量端点（rejectReserved=true）显式报错，因为那不是普通表单提交。
//
// 内部完成管线（KAF/SSLVPN/任务完成）本身会携带 ticket_id/work_item_id/action 等键，
// 所以参与者路径不能改成一律报错——实测那样会打断 22 条既有流程。
func TestReservedIdentityVariablesCannotBeOverriddenByTaskForms(t *testing.T) {
	reservedKeys := []string{"business_id", "business_type", "business_key", "tenant_id"}

	stripped, err := validateAndCloneBPMNParticipantVariables(map[string]interface{}{
		"approved":      true,
		"business_id":   999,
		"business_type": "tampered",
		"business_key":  "generic:999",
		"tenant_id":     4242,
	}, false)
	require.NoError(t, err)
	require.Equal(t, true, stripped["approved"], "普通表单字段必须保留")
	for _, reserved := range reservedKeys {
		require.NotContains(t, stripped, reserved, "保留键 %q 不得进入流程完成变量", reserved)
	}

	for _, reserved := range reservedKeys {
		_, err := validateAndCloneBPMNParticipantVariables(map[string]interface{}{reserved: "tampered"}, true)
		require.Error(t, err, "实例变量端点必须拒绝保留键 %q", reserved)
	}
}

// 普通表单变量在两条边界都按原样通过：拒绝逻辑只针对保留键，不能误伤业务字段。
func TestOrdinaryFormVariablesSurviveValidation(t *testing.T) {
	for _, rejectReserved := range []bool{false, true} {
		participant, err := validateAndCloneBPMNParticipantVariables(map[string]interface{}{
			"approved":         true,
			"approved_comment": "looks fine",
		}, rejectReserved)
		require.NoError(t, err)
		require.Equal(t, true, participant["approved"])
		require.Equal(t, "looks fine", participant["approved_comment"])
	}
}
