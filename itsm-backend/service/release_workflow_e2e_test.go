package service

import (
	"context"
	"strconv"
	"testing"

	"itsm-backend/dto"
	"itsm-backend/ent/enttest"
	"itsm-backend/ent/processapprovaldecision"
	"itsm-backend/ent/processinstance"
	"itsm-backend/ent/processtask"
	"itsm-backend/service/bpmn"

	_ "github.com/mattn/go-sqlite3"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"go.uber.org/zap"
)

// TestReleaseFlow_ProcessTaskDecisionDrivesLifecycleEndToEnd proves the only
// approval path: submit release -> ProcessTask -> immutable decision -> BPMN
// callback -> professional release lifecycle.
func TestReleaseFlow_ProcessTaskDecisionDrivesLifecycleEndToEnd(t *testing.T) {
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
	// 该组，ProcessTask 的 authorizeTaskActor 校验才能通过。
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
		Title:         "流程端到端发布",
		Type:          "minor",
	}, creator.ID, tenant.ID)
	require.NoError(t, err)
	assert.Equal(t, "draft", releaseEntity.Status)

	// 模拟 ProcessTriggerService 启动：businessKey 与实例变量按 trigger 约定写入
	engine := NewCustomProcessEngine(client, zap.NewNop().Sugar())
	releaseService.SetProcessEngine(engine)
	engine.(*CustomProcessEngine).CallbackRegistry().
		GetHandler("release_service_handler").(*bpmn.ReleaseServiceTaskHandler).
		SetReleaseService(releaseService)
	workflowCtx := context.WithValue(ctx, bpmn.BPMNTenantIDContextKey, tenant.ID)
	workflowCtx = WithTrustedBPMNTenantContext(workflowCtx, tenant.ID)
	instance, err := engine.StartProcess(workflowCtx, "release_approval_flow", "release:"+strconv.Itoa(releaseEntity.ID), "release", releaseEntity.ID, map[string]interface{}{
		"triggered_by": strconv.Itoa(creator.ID),
	})
	require.NoError(t, err)
	started, err := client.ProcessInstance.Get(ctx, instance.ID)
	require.NoError(t, err)
	assert.Equal(t, "Activity_TechReview", started.CurrentActivityID, "流程应停在技术评审节点")

	// 1. 技术评审：完成唯一 ProcessTask，意见由 handler 写入 release_notes，
	//    网关按 tech_review_pass=true 路由到 Activity_Approval
	reviewed, err := releaseService.ApplyReleaseTechReview(ctx, releaseEntity.ID, tenant.ID, creator.ID, "架构评审通过")
	require.NoError(t, err)
	require.NotNil(t, reviewed)
	assert.Contains(t, reviewed.ReleaseNotes, "架构评审通过", "技术评审意见应由 handler 写入 release_notes")

	afterReview, err := client.ProcessInstance.Get(ctx, instance.ID)
	require.NoError(t, err)
	assert.Equal(t, "Activity_Approval", afterReview.CurrentActivityID, "评审后流程应推进到审批节点")

	// 2. 审批只通过 canonical ProcessTask decision command。
	approvalTask := client.ProcessTask.Query().Where(
		processtask.ProcessInstanceID(instance.ID), processtask.TaskDefinitionKey("Activity_Approval"),
	).OnlyX(ctx)
	approvalCtx := context.WithValue(ctx, bpmn.BPMNTenantIDContextKey, tenant.ID)
	approvalCtx = context.WithValue(approvalCtx, bpmn.BPMNUserIDContextKey, approver.ID)
	approvalCtx = WithBPMNAccessScope(approvalCtx, BPMNAccessScope{UserID: approver.ID, TenantID: tenant.ID})
	require.NoError(t, engine.TaskService().CompleteTask(approvalCtx, approvalTask.TaskID, map[string]interface{}{
		"approvalAction": "approve", "approvalResult": "approved", "approvalComment": "同意发布",
	}))
	require.Equal(t, 1, client.ProcessApprovalDecision.Query().Where(
		processapprovaldecision.ProcessTaskID(approvalTask.ID), processapprovaldecision.Action("approve"),
	).CountX(ctx))

	afterApproval, err := client.ProcessInstance.Get(ctx, instance.ID)
	require.NoError(t, err)
	assert.Equal(t, "Activity_Schedule", afterApproval.CurrentActivityID)
	assert.Equal(t, "draft", client.Release.GetX(ctx, releaseEntity.ID).Status,
		"审批决策不能绕过独立的计划发布任务")

	// 3. 计划发布命令完成 Activity_Schedule，callback 是唯一状态写入点。
	scheduled, err := releaseService.UpdateReleaseStatus(ctx, releaseEntity.ID, tenant.ID, creator.ID, "scheduled")
	require.NoError(t, err)
	assert.Equal(t, "scheduled", scheduled.Status)

	afterSchedule, err := client.ProcessInstance.Get(ctx, instance.ID)
	require.NoError(t, err)
	assert.Equal(t, "Activity_Execute", afterSchedule.CurrentActivityID)

	// 4. 执行发布：完成 Activity_Execute
	inProgress, err := releaseService.UpdateReleaseStatus(ctx, releaseEntity.ID, tenant.ID, creator.ID, "in-progress")
	require.NoError(t, err)
	assert.Equal(t, "in-progress", inProgress.Status)

	afterExecute, err := client.ProcessInstance.Get(ctx, instance.ID)
	require.NoError(t, err)
	assert.Equal(t, "Activity_Verify", afterExecute.CurrentActivityID, "执行后流程应推进到验证确认节点")

	// 5. 验证完成：完成 Activity_Verify，流程走到终点
	completed, err := releaseService.UpdateReleaseStatus(ctx, releaseEntity.ID, tenant.ID, creator.ID, "completed")
	require.NoError(t, err)
	assert.Equal(t, "completed", completed.Status)
	assert.NotNil(t, completed.ActualReleaseDate)

	finished, err := client.ProcessInstance.Get(ctx, instance.ID)
	require.NoError(t, err)
	assert.Equal(t, "completed", finished.Status, "流程应走完全部节点")
}

// TestReleaseFlow_RejectApproval_EndsFlowAndCancelsRelease 锁定拒绝路径：
// 审批驳回（approvalResult=rejected）→ Gateway_ApprovalResult 走 Flow_ApprovalRejected →
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
	releaseService.SetProcessEngine(engine)
	engine.(*CustomProcessEngine).CallbackRegistry().
		GetHandler("release_service_handler").(*bpmn.ReleaseServiceTaskHandler).
		SetReleaseService(releaseService)
	workflowCtx := context.WithValue(ctx, bpmn.BPMNTenantIDContextKey, tenant.ID)
	workflowCtx = WithTrustedBPMNTenantContext(workflowCtx, tenant.ID)
	instance, err := engine.StartProcess(workflowCtx, "release_approval_flow", "release:"+strconv.Itoa(releaseEntity.ID), "release", releaseEntity.ID, map[string]interface{}{
		"triggered_by": strconv.Itoa(creator.ID),
	})
	require.NoError(t, err)

	// 技术评审通过 → 审批节点
	_, err = releaseService.ApplyReleaseTechReview(ctx, releaseEntity.ID, tenant.ID, creator.ID, "评审通过")
	require.NoError(t, err)

	// 审批驳回只走 ProcessTask decision；callback 取消发布。
	approvalTask := client.ProcessTask.Query().Where(
		processtask.ProcessInstanceID(instance.ID), processtask.TaskDefinitionKey("Activity_Approval"),
	).OnlyX(ctx)
	approvalCtx := context.WithValue(ctx, bpmn.BPMNTenantIDContextKey, tenant.ID)
	approvalCtx = context.WithValue(approvalCtx, bpmn.BPMNUserIDContextKey, approver.ID)
	approvalCtx = WithBPMNAccessScope(approvalCtx, BPMNAccessScope{UserID: approver.ID, TenantID: tenant.ID})
	require.NoError(t, engine.TaskService().CompleteTask(approvalCtx, approvalTask.TaskID, map[string]interface{}{
		"approvalAction": "reject", "approvalResult": "rejected", "approvalComment": "风险过高",
	}))
	assert.Equal(t, "cancelled", client.Release.GetX(ctx, releaseEntity.ID).Status)

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
