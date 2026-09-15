package change

import (
	"testing"

	"github.com/stretchr/testify/require"

	_ "github.com/mattn/go-sqlite3"
)

// TestChangeApprovalE2E_FullApproveFlow 完整走一遍：提交审批 -> 触发 BPMN ->
// 完成变更评估 -> CAB 审批通过 -> 断言 Change.Status/流程实例状态/审批历史
// 全部符合预期，不留孤儿任务。
func TestChangeApprovalE2E_FullApproveFlow(t *testing.T) {
	f := newGovernedChangeFixture(t, "normal")
	f.submit(t)
	f.assess(t)
	cmd := f.taskCommand(t, "approve", f.approver)
	result, err := f.svc.CompleteChangeTask(f.ctx, cmd)
	require.NoError(t, err)
	require.NotNil(t, result.Result)
	require.Equal(t, "approved", result.Result.Status)
	require.Equal(t, "Activity_Schedule", f.client.ProcessInstance.Query().OnlyX(f.ctx).CurrentActivityID)
	require.Equal(t, 1, f.client.ProcessApprovalDecision.Query().CountX(f.ctx))
	again, err := f.svc.CompleteChangeTask(f.ctx, cmd)
	require.NoError(t, err)
	require.True(t, again.Result.Replayed)
	require.Equal(t, result.Result.Version, again.Result.Version)
	require.Equal(t, result.ExecutionKey, again.ExecutionKey)
	require.Equal(t, 1, f.client.ProcessApprovalDecision.Query().CountX(f.ctx))
}

// TestChangeApprovalE2E_FullRejectFlow 同样结构，走驳回分支：断言 Change.Status=="rejected"，
// ProcessInstance.Status=="completed"（驳回节点走 Flow_End 直接结束，流程实例正确终止，
// 不会像 approve 分支那样停在 running）。
func TestChangeApprovalE2E_FullRejectFlow(t *testing.T) {
	f := newGovernedChangeFixture(t, "normal")
	f.submit(t)
	f.assess(t)
	result, err := f.svc.CompleteChangeTask(f.ctx, f.taskCommand(t, "reject", f.approver))
	require.NoError(t, err)
	require.NotNil(t, result.Result)
	require.Equal(t, "rejected", result.Result.Status)
	require.Equal(t, "completed", f.client.ProcessInstance.Query().OnlyX(f.ctx).Status)
}

// TestChangeApprovalE2E_NonCMUserCannotApprove 断言非 change_manager 角色的用户
// 调用 TransitionStatus approve 会失败，Change.Status 不变。
func TestChangeApprovalE2E_NonCMUserCannotApprove(t *testing.T) {
	f := newGovernedChangeFixture(t, "normal")
	f.submit(t)
	f.assess(t)
	outsider := f.client.User.Create().SetTenantID(f.tenant).SetUsername("outsider").SetName("Outsider").SetEmail("outsider@example.test").SetPasswordHash("test").SetRole("agent").SetActive(true).SaveX(f.ctx)
	_, err := f.svc.CompleteChangeTask(f.ctx, f.taskCommand(t, "approve", outsider.ID))
	require.Error(t, err)
	require.Equal(t, "submitted", f.client.Ticket.GetX(f.ctx, f.record.WorkItemID).Status)
	require.Zero(t, f.client.ProcessApprovalDecision.Query().CountX(f.ctx))
}
