package service

import (
	"context"
	"strconv"
	"testing"

	"itsm-backend/dto"
	"itsm-backend/ent/enttest"
	"itsm-backend/ent/processinstance"
	"itsm-backend/service/bpmn"

	_ "github.com/mattn/go-sqlite3"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"go.uber.org/zap"
)

// TestReleaseFlow_StageBridges_AdvanceProcessEndToEnd 是 P1 的端到端回归：
// release_approval_flow 的 5 个用户任务（技术评审/审批/计划/执行/验证）此前在域侧
// 操作路径上全部拿不到 business_id（引擎刻意不合并实例变量），状态副作用静默失败、
// 网关变量（tech_review_pass/approval_pass）无人写入导致流程停在网关上。修复后：
//
//	ApplyReleaseTechReview → 桥接 Activity_TechReview → 流程到 Activity_Approval
//	ApplyReleaseApproval   → 桥接 Activity_Approval 和 Activity_Schedule → 流程到 Activity_Execute
//	UpdateReleaseStatus    → 桥接 Execute/Verify → 流程走完
//
// 真实浏览器验证（2026-08-18）发现 approve 分支只桥接了 Activity_Approval，
// Activity_Schedule 需要靠"另一次同值 scheduled 调用"才补得上——但审批通过后前端
// 从没有发出这次调用（"提交计划"按钮的显示条件只匹配免审批草稿路径），导致每一个
// 走 CAB 审批的发布，流程实例都会永久悬挂在"计划发布"节点，即使域状态已经走到
// completed。ApplyReleaseApproval 现在在 approve 分支里把 Schedule 桥接一起做掉，
// 审批即代表"已排期"，不再依赖前端后续动作。
func TestReleaseFlow_StageBridges_AdvanceProcessEndToEnd(t *testing.T) {
	client := enttest.Open(t, "sqlite3", testDSN())
	t.Cleanup(func() { _ = client.Close() })

	ctx := context.Background()
	tenant, err := client.Tenant.Create().
		SetName("Release Bridge Tenant").
		SetCode("release-bridge").
		SetDomain("release-bridge.example.com").
		SetStatus("active").
		Save(ctx)
	require.NoError(t, err)

	creator, err := client.User.Create().
		SetUsername("release-creator").SetEmail("release-creator@test.com").SetPasswordHash("x").
		SetName("创建人").SetTenantID(tenant.ID).SetActive(true).
		Save(ctx)
	require.NoError(t, err)

	approver, err := client.User.Create().
		SetUsername("release-approver").SetEmail("release-approver@test.com").SetPasswordHash("x").
		SetName("审批人").SetTenantID(tenant.ID).SetActive(true).
		Save(ctx)
	require.NoError(t, err)

	// Activity_Approval 未配置 assignee/assigneeRole，引擎会回退到
	// approvalFallbackCandidateGroup（"ticket-approvers"）展开候选人。把审批人放进
	// 该组，approval 桥接的 authorizeTaskActor 校验才能通过。
	_, err = client.Group.Create().
		SetName("ticket-approvers").
		SetTenantID(tenant.ID).
		AddMemberIDs(approver.ID).
		Save(ctx)
	require.NoError(t, err)

	_, err = NewBPMNTemplateService(client).LoadAndDeployTemplates(ctx, tenant.ID)
	require.NoError(t, err)

	releaseService := NewReleaseService(client, zap.NewNop().Sugar())
	releaseEntity, err := releaseService.CreateRelease(ctx, &dto.CreateReleaseRequest{
		ReleaseNumber: "REL-BRIDGE-1",
		Title:         "桥接端到端发布",
		Type:          "minor",
	}, creator.ID, tenant.ID)
	require.NoError(t, err)
	assert.Equal(t, "draft", releaseEntity.Status)

	// 模拟 ProcessTriggerService 启动：businessKey 与实例变量按 trigger 约定写入
	engine := NewCustomProcessEngine(client, zap.NewNop().Sugar())
	workflowCtx := context.WithValue(ctx, bpmn.BPMNTenantIDContextKey, tenant.ID)
	workflowCtx = WithTrustedBPMNTenantContext(workflowCtx, tenant.ID)
	instance, err := engine.StartProcess(workflowCtx, "release_approval_flow", "release:"+strconv.Itoa(releaseEntity.ID), "release", releaseEntity.ID, map[string]interface{}{
		"triggered_by": strconv.Itoa(creator.ID),
	})
	require.NoError(t, err)
	started, err := client.ProcessInstance.Get(ctx, instance.ID)
	require.NoError(t, err)
	assert.Equal(t, "Activity_TechReview", started.CurrentActivityID, "流程应停在技术评审节点")

	// 1. 技术评审：桥接完成 Activity_TechReview，意见由 handler 写入 release_notes，
	//    网关按 tech_review_pass=true 路由到 Activity_Approval
	reviewed, err := releaseService.ApplyReleaseTechReview(ctx, releaseEntity.ID, tenant.ID, creator.ID, "架构评审通过")
	require.NoError(t, err)
	require.NotNil(t, reviewed)
	assert.Contains(t, reviewed.ReleaseNotes, "架构评审通过", "技术评审意见应由 handler 写入 release_notes")

	afterReview, err := client.ProcessInstance.Get(ctx, instance.ID)
	require.NoError(t, err)
	assert.Equal(t, "Activity_Approval", afterReview.CurrentActivityID, "评审后流程应推进到审批节点")

	// 2. 审批：桥接完成 Activity_Approval 和 Activity_Schedule（approve 即代表已排期），
	//    网关按 approval_pass=true 路由，流程一次性推进到 Activity_Execute
	approved, err := releaseService.ApplyReleaseApproval(ctx, releaseEntity.ID, tenant.ID, approver.ID, "approve", "同意发布")
	require.NoError(t, err)
	require.NotNil(t, approved)
	assert.Equal(t, "scheduled", approved.Status, "审批通过后域状态应为 scheduled")

	afterApproval, err := client.ProcessInstance.Get(ctx, instance.ID)
	require.NoError(t, err)
	assert.Equal(t, "Activity_Execute", afterApproval.CurrentActivityID, "审批后流程应直接推进到执行发布节点（Schedule 已随审批一并桥接）")

	// 3. 计划发布：域写幂等（scheduled→scheduled）；Activity_Schedule 已在审批时完成，
	//    这里的桥接调用应找不到待办任务、安全空转，流程节点保持不变
	scheduled, err := releaseService.UpdateReleaseStatus(ctx, releaseEntity.ID, tenant.ID, creator.ID, "scheduled")
	require.NoError(t, err)
	assert.Equal(t, "scheduled", scheduled.Status)

	afterSchedule, err := client.ProcessInstance.Get(ctx, instance.ID)
	require.NoError(t, err)
	assert.Equal(t, "Activity_Execute", afterSchedule.CurrentActivityID, "Schedule 桥接已随审批完成，同值调用应保持流程节点不变")

	// 4. 执行发布：桥接完成 Activity_Execute
	inProgress, err := releaseService.UpdateReleaseStatus(ctx, releaseEntity.ID, tenant.ID, creator.ID, "in-progress")
	require.NoError(t, err)
	assert.Equal(t, "in-progress", inProgress.Status)

	afterExecute, err := client.ProcessInstance.Get(ctx, instance.ID)
	require.NoError(t, err)
	assert.Equal(t, "Activity_Verify", afterExecute.CurrentActivityID, "执行后流程应推进到验证确认节点")

	// 5. 验证完成：桥接完成 Activity_Verify，流程走到终点
	completed, err := releaseService.UpdateReleaseStatus(ctx, releaseEntity.ID, tenant.ID, creator.ID, "completed")
	require.NoError(t, err)
	assert.Equal(t, "completed", completed.Status)
	assert.NotNil(t, completed.ActualReleaseDate)

	finished, err := client.ProcessInstance.Get(ctx, instance.ID)
	require.NoError(t, err)
	assert.Equal(t, "completed", finished.Status, "流程应走完全部节点")
}

// TestReleaseFlow_RejectApproval_EndsFlowAndCancelsRelease 锁定拒绝路径：
// 审批驳回（approval_pass=false）→ Gateway_ApprovalResult 走 Flow_ApprovalRejected →
// EndEvent_Reject 正常结束，域状态 cancelled，流程不再悬挂。
func TestReleaseFlow_RejectApproval_EndsFlowAndCancelsRelease(t *testing.T) {
	client := enttest.Open(t, "sqlite3", testDSN())
	t.Cleanup(func() { _ = client.Close() })

	ctx := context.Background()
	tenant, err := client.Tenant.Create().
		SetName("Release Reject Tenant").
		SetCode("release-reject").
		SetDomain("release-reject.example.com").
		SetStatus("active").
		Save(ctx)
	require.NoError(t, err)

	creator, err := client.User.Create().
		SetUsername("release-reject-creator").SetEmail("rr-creator@test.com").SetPasswordHash("x").
		SetName("创建人").SetTenantID(tenant.ID).SetActive(true).
		Save(ctx)
	require.NoError(t, err)
	approver, err := client.User.Create().
		SetUsername("release-reject-approver").SetEmail("rr-approver@test.com").SetPasswordHash("x").
		SetName("审批人").SetTenantID(tenant.ID).SetActive(true).
		Save(ctx)
	require.NoError(t, err)
	_, err = client.Group.Create().
		SetName("ticket-approvers").
		SetTenantID(tenant.ID).
		AddMemberIDs(approver.ID).
		Save(ctx)
	require.NoError(t, err)

	_, err = NewBPMNTemplateService(client).LoadAndDeployTemplates(ctx, tenant.ID)
	require.NoError(t, err)

	releaseService := NewReleaseService(client, zap.NewNop().Sugar())
	releaseEntity, err := releaseService.CreateRelease(ctx, &dto.CreateReleaseRequest{
		ReleaseNumber: "REL-REJECT-1",
		Title:         "拒绝路径发布",
		Type:          "minor",
	}, creator.ID, tenant.ID)
	require.NoError(t, err)

	engine := NewCustomProcessEngine(client, zap.NewNop().Sugar())
	workflowCtx := context.WithValue(ctx, bpmn.BPMNTenantIDContextKey, tenant.ID)
	workflowCtx = WithTrustedBPMNTenantContext(workflowCtx, tenant.ID)
	instance, err := engine.StartProcess(workflowCtx, "release_approval_flow", "release:"+strconv.Itoa(releaseEntity.ID), "release", releaseEntity.ID, map[string]interface{}{
		"triggered_by": strconv.Itoa(creator.ID),
	})
	require.NoError(t, err)

	// 技术评审通过 → 审批节点
	_, err = releaseService.ApplyReleaseTechReview(ctx, releaseEntity.ID, tenant.ID, creator.ID, "评审通过")
	require.NoError(t, err)

	// 审批驳回 → 流程走 EndEvent_Reject 结束，域状态 cancelled
	rejected, err := releaseService.ApplyReleaseApproval(ctx, releaseEntity.ID, tenant.ID, approver.ID, "reject", "风险过高")
	require.NoError(t, err)
	assert.Equal(t, "cancelled", rejected.Status)

	finished, err := client.ProcessInstance.Get(ctx, instance.ID)
	require.NoError(t, err)
	assert.Equal(t, "completed", finished.Status, "拒绝后流程应正常走到 EndEvent_Reject 结束，不再悬挂")
}

// TestReleaseService_CreateRelease_TriggersWorkflow 锁定"创建发布自动启动流程"：
// 发布域此前没有接入 ProcessTriggerService（工单/事件/问题域都有），S1 手工测试
// 找不到流程实例。现在 CreateRelease 按 ProcessBinding 默认绑定触发
// release_approval_flow，实例停在第一个节点（技术评审）。
func TestReleaseService_CreateRelease_TriggersWorkflow(t *testing.T) {
	client := enttest.Open(t, "sqlite3", testDSN())
	t.Cleanup(func() { _ = client.Close() })
	ctx := context.Background()

	tenant, err := client.Tenant.Create().
		SetName("Release Trigger Tenant").
		SetCode("release-trigger").
		SetDomain("release-trigger.example.com").
		SetStatus("active").
		Save(ctx)
	require.NoError(t, err)

	creator, err := client.User.Create().
		SetUsername("release-trigger-creator").SetEmail("rt-creator@test.com").SetPasswordHash("x").
		SetName("创建人").SetTenantID(tenant.ID).SetActive(true).
		Save(ctx)
	require.NoError(t, err)

	_, err = NewBPMNTemplateService(client).LoadAndDeployTemplates(ctx, tenant.ID)
	require.NoError(t, err)
	require.NoError(t, NewProcessBindingService(client).InitDefaultBindings(ctx, tenant.ID))

	engine := NewCustomProcessEngine(client, zap.NewNop().Sugar())
	triggerSvc := NewProcessTriggerService(client, engine)

	releaseService := NewReleaseService(client, zap.NewNop().Sugar())
	releaseService.SetProcessTriggerService(triggerSvc)

	releaseEntity, err := releaseService.CreateRelease(ctx, &dto.CreateReleaseRequest{
		ReleaseNumber: "REL-TRIGGER-1",
		Title:         "自动触发发布",
		Type:          "minor",
	}, creator.ID, tenant.ID)
	require.NoError(t, err)

	instance, err := client.ProcessInstance.Query().
		Where(processinstance.BusinessKey("release:"+strconv.Itoa(releaseEntity.ID)),
			processinstance.TenantID(tenant.ID)).
		Only(ctx)
	require.NoError(t, err, "创建发布应自动启动 release_approval_flow 实例")
	assert.Equal(t, "release_approval_flow", instance.ProcessDefinitionKey)
	assert.Equal(t, "Activity_TechReview", instance.CurrentActivityID,
		"流程应停在第一个节点（技术评审）")
}
