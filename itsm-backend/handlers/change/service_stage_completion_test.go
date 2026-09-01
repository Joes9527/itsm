package change

import (
	"context"
	"fmt"
	"testing"

	"itsm-backend/service"

	_ "github.com/mattn/go-sqlite3"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"go.uber.org/zap"
)

// TestTransitionStatus_StageCompletion_AdvanceProcessEndToEnd 是阶段流转原生化（Track4
// 收尾）的 change 侧端到端回归：change_normal_flow 的排期/实施/验证/关闭节点此前只能靠
// approvalBridge 完成，现在 completeChangeStageTasks 直接用 processEngine 完成同样的节点，
// 不再经过 BPMNApprovalBridge。
//
//	TransitionStatus(in_progress) → 原生完成 Activity_Schedule + Activity_Implement
//	TransitionStatus(completed)   → 原生完成 Activity_Verify（带 verify_passed）+ Activity_Close
//
// 流程从排期一路走完，域写覆盖 handler 中间值，最终状态以域为准。
func TestTransitionStatus_StageCompletion_AdvanceProcessEndToEnd(t *testing.T) {
	entClient := newChangeBPMNEntClient(t, "change_stage_e2e")
	tenantID, actorID := setupChangeBPMNActor(t, entClient, "stage")

	// 模板部署（change_normal_flow 的网关路由需要）
	deployCtx := service.WithTrustedBPMNTenantContext(context.Background(), tenantID)
	_, err := service.NewBPMNTemplateService(entClient).LoadAndDeployTemplates(deployCtx, tenantID)
	require.NoError(t, err)

	// 域侧 mock：approved 状态（已过审批），ID 与 DB 行对齐。
	// Type=standard：标准变更预授权，approved 可直接 in_progress（normal 走严格 ITIL
	// 要求 approved→scheduled，而域 API 没有 schedule 动作）。
	repo := newMockRepository()
	mockChange := createTestChange(repo, tenantID, actorID)
	mockChange.Status = "approved"
	mockChange.Type = "standard"
	repo.changes[mockChange.ID] = mockChange

	// 先建两条无关的 WorkItem，把 tickets.id 序列往前顶两步。changes.id 和 tickets.id
	// 是两张表各自独立的自增序列，如果这里只建一条 Change
	// 和一条 WorkItem，在全新的内存库里两者都会恰好是各自表的第 1 行，数值碰巧相等——这样
	// 即使 completeChangeStageTasks 里错误地直接用裸 changeID 拼 businessKey（而不是解析出
	// 真正的 workItemID），也会因为两个数字碰巧一样而拼出同一个字符串，测试照样通过，
	// 完全测不出这个 bug（这正是这个 bug 最初能在评审前混进来的原因）。垫场行确保下面
	// dbChange.ID 和 workItem.ID 是两个不同的数字，只有真的按 workItemID 解析才能拼出
	// engine.StartProcess 用的那个 businessKey。
	for i := 0; i < 2; i++ {
		_, err := entClient.Ticket.Create().
			SetTitle(fmt.Sprintf("ID pad %d", i)).
			SetTicketNumber(fmt.Sprintf("TKT-STAGE-PAD-%d", i)).
			SetRequesterID(actorID).
			SetTenantID(tenantID).
			Save(context.Background())
		require.NoError(t, err)
	}

	// Wave 2 起 completeChangeStageTasks 通过 resolveWorkItemID 查真实 entClient.Change 的
	// work_item_id 来构造 businessKey——必须先建一条 WorkItem 并回填，否则会被当成"没有
	// 关联的 WorkItem"静默跳过阶段完成（这个测试恰恰要验证阶段完成确实生效）。
	workItem := createChangeWorkItemFixture(t, entClient, tenantID, actorID, "Stage Completion Change", "approved")

	// DB 侧真实 Change 行：handler 写侧（change_handler.go）直接操作 entClient，
	// 状态必须与域侧一致（approved），否则 handler 状态机白名单会拒绝
	dbChange, err := entClient.Change.Create().
		SetWorkItemID(workItem.ID).
		Save(context.Background())
	require.NoError(t, err)
	require.NotEqual(t, workItem.ID, dbChange.ID,
		"垫场行必须真的让两个 ID 不同，否则这个测试无法证明 businessKey 是按 workItemID 而不是 changeID 解析的")
	// 让 mock 与 DB 使用同一个 ID（TransitionStatus 按 ID 操作 mock，handler 按 change_id 操作 DB）
	delete(repo.changes, mockChange.ID)
	mockChange.ID = dbChange.ID
	mockChange.WorkItemID = &workItem.ID
	repo.changes[dbChange.ID] = mockChange

	// 模拟 ProcessTriggerService 启动：businessKey 按 "change:{id}" 约定
	engine := service.NewCustomProcessEngine(entClient, zap.NewNop().Sugar())
	workflowCtx := service.WithBPMNAccessScope(context.Background(), service.BPMNAccessScope{
		UserID: actorID, TenantID: tenantID, CanUpdateAllTasks: true,
	})
	instance, err := engine.StartProcess(workflowCtx, "change_normal_flow", fmt.Sprintf("change:%d", workItem.ID), "change", workItem.ID, map[string]interface{}{
		"approval_required": false,
		"business_type":     "change",
		"business_id":       workItem.ID,
		"tenant_id":         tenantID,
	})
	require.NoError(t, err)
	started, err := entClient.ProcessInstance.Get(context.Background(), instance.ID)
	require.NoError(t, err)
	assert.Equal(t, "Activity_Assessment", started.CurrentActivityID, "流程启动后停在评估节点")

	// 完成评估节点 → 网关按 approval_required=false 路由到排期节点
	assessmentTask, err := entClient.ProcessTask.Query().First(context.Background())
	require.NoError(t, err)
	require.NoError(t, engine.CompleteTask(workflowCtx, assessmentTask.TaskID, map[string]interface{}{
		"change_id": dbChange.ID,
	}))
	afterAssessment, err := entClient.ProcessInstance.Get(context.Background(), instance.ID)
	require.NoError(t, err)
	assert.Equal(t, "Activity_Schedule", afterAssessment.CurrentActivityID,
		"approval_required=false 时评估后应直达排期节点")

	svc := NewService(repo, entClient, zap.NewNop().Sugar())
	svc.SetProcessEngine(engine)

	// 1. start（in_progress）：原生完成 Activity_Schedule → Activity_Implement，域写 in_progress
	updated, err := svc.TransitionStatus(context.Background(), dbChange.ID, tenantID, actorID, "in_progress", "")
	require.NoError(t, err)
	assert.Equal(t, "in_progress", updated.Status)

	afterStart, err := entClient.ProcessInstance.Get(context.Background(), instance.ID)
	require.NoError(t, err)
	assert.Equal(t, "Activity_Verify", afterStart.CurrentActivityID, "start 后流程应推进到验证节点")

	// handler 写侧：实施动作把 DB 状态推进到 in_progress
	dbAfter, err := entClient.Change.Get(context.Background(), dbChange.ID)
	require.NoError(t, err)
	dbAfterWorkItem, err := entClient.Ticket.Get(context.Background(), dbAfter.WorkItemID)
	require.NoError(t, err)
	assert.Equal(t, "in_progress", dbAfterWorkItem.Status, "implement_change 应以注入的 change_id 生效")

	// 2. complete（completed）：原生完成 Activity_Verify（带 verify_passed）→ Activity_Close → End
	updated, err = svc.TransitionStatus(context.Background(), dbChange.ID, tenantID, actorID, "completed", "")
	require.NoError(t, err)
	assert.Equal(t, "completed", updated.Status)

	finished, err := entClient.ProcessInstance.Get(context.Background(), instance.ID)
	require.NoError(t, err)
	assert.Equal(t, "completed", finished.Status, "流程应走完全部节点")

	dbFinal, err := entClient.Change.Get(context.Background(), dbChange.ID)
	require.NoError(t, err)
	dbFinalWorkItem, err := entClient.Ticket.Get(context.Background(), dbFinal.WorkItemID)
	require.NoError(t, err)
	assert.Equal(t, "completed", dbFinalWorkItem.Status, "verify/close 动作应把 DB 状态推进到 completed")
	assert.False(t, dbFinal.ActualEndDate.IsZero(), "关闭动作应写入实际结束时间")
}

// TestTransitionStatus_StageCompletion_NoRunningInstanceIsNoop 锁定没有关联运行中流程实例
// 时（比如紧急变更走 emergency 流程分支、或流程已经在别的分支提前结束）
// completeChangeStageTasks 静默跳过，域状态照常推进，不报错。
func TestTransitionStatus_StageCompletion_NoRunningInstanceIsNoop(t *testing.T) {
	entClient := newChangeBPMNEntClient(t, "change_stage_noop")
	tenantID, actorID := setupChangeBPMNActor(t, entClient, "noop")

	repo := newMockRepository()
	mockChange := createTestChange(repo, tenantID, actorID)
	mockChange.Status = "approved"
	mockChange.Type = "standard"
	repo.changes[mockChange.ID] = mockChange

	svc := NewService(repo, entClient, zap.NewNop().Sugar())
	svc.SetProcessEngine(service.NewCustomProcessEngine(entClient, zap.NewNop().Sugar()))

	// 没有为这个变更启动过任何流程实例——completeChangeStageTasks 应该找不到运行中实例，
	// 直接跳过，域状态正常推进到 in_progress，不报错。
	updated, err := svc.TransitionStatus(context.Background(), mockChange.ID, tenantID, actorID, "in_progress", "")
	require.NoError(t, err)
	assert.Equal(t, "in_progress", updated.Status)
}
