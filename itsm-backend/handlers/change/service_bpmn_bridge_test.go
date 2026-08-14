package change

import (
	"context"
	"fmt"
	"strconv"
	"testing"
	"time"

	"itsm-backend/dto"
	"itsm-backend/ent"
	"itsm-backend/ent/enttest"
	"itsm-backend/ent/processinstance"
	"itsm-backend/service"
	"itsm-backend/service/bpmn"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"go.uber.org/zap"
	"go.uber.org/zap/zaptest"

	_ "github.com/mattn/go-sqlite3"
)

// newTestBPMNEngine 构造真实的 *CustomProcessEngine（通过 service.NewCustomProcessEngine
// 的导出构造函数，返回值已经是 service.ProcessEngine 接口），不要自己发明构造方式——
// 照抄 service/bpmn_process_engine_approval_assignment_test.go 里 newApprovalAssignmentFixture
// 的用法。
func newTestBPMNEngine(t *testing.T, client *ent.Client, logger *zap.SugaredLogger) service.ProcessEngine {
	t.Helper()
	return service.NewCustomProcessEngine(client, logger)
}

// ==================== TransitionStatus ↔ BPMN 桥接集成测试（P0-1 阶段3） ====================

func newChangeBridgeEntClient(t *testing.T, dbName string) *ent.Client {
	t.Helper()
	client := enttest.Open(t, "sqlite3", fmt.Sprintf("file:%s?mode=memory&cache=shared&_fk=1", dbName))
	t.Cleanup(func() { client.Close() })
	return client
}

func setupChangeBridgeActor(t *testing.T, client *ent.Client, code string) (tenantID, actorID int) {
	t.Helper()
	ctx := context.Background()
	tenant, err := client.Tenant.Create().
		SetName("Change Bridge Tenant " + code).
		SetCode("chg-bridge-" + code).
		SetDomain("chg-bridge-" + code + ".example.com").
		SetStatus("active").
		Save(ctx)
	require.NoError(t, err)
	actor, err := client.User.Create().
		SetUsername("chg-approver-" + code).
		SetEmail("chg-approver-" + code + "@example.com").
		SetName("Change Approver " + code).
		SetPasswordHash("hash").
		SetRole("agent").
		SetActive(true).
		SetTenantID(tenant.ID).
		Save(ctx)
	require.NoError(t, err)
	return tenant.ID, actor.ID
}

// createChangeBridgeProcessFixture 创建 运行中流程实例 + 待办用户任务，
// businessKey 采用与 ProcessTriggerService 相同的 "change:{id}" 约定。
func createChangeBridgeProcessFixture(t *testing.T, client *ent.Client, tenantID int, keySuffix, businessKey string, assigneeID int) (taskID int) {
	t.Helper()
	ctx := context.Background()

	defKey := "change_bridge_approval_" + keySuffix
	bpmnXML := fmt.Sprintf(`<?xml version="1.0" encoding="UTF-8"?><bpmn:definitions xmlns:bpmn="http://www.omg.org/spec/BPMN/20100524/MODEL" xmlns:itsm="https://github.com/heidsoft/itsm/schema/bpmn" id="Definitions_%s" targetNamespace="https://github.com/heidsoft/itsm"><bpmn:process id="%s" name="Change Bridge Approval %s" isExecutable="true"><bpmn:startEvent id="StartEvent_1"/><bpmn:userTask id="Approval_1" name="变更审批" itsm:taskPurpose="approval" itsm:approvalMode="single" itsm:assignee="%d"/><bpmn:endEvent id="EndEvent_1"/><bpmn:sequenceFlow id="Flow_1" sourceRef="StartEvent_1" targetRef="Approval_1"/><bpmn:sequenceFlow id="Flow_2" sourceRef="Approval_1" targetRef="EndEvent_1"/></bpmn:process></bpmn:definitions>`, defKey, defKey, keySuffix, assigneeID)

	deployment, err := client.ProcessDeployment.Create().
		SetDeploymentID("CHG-DEP-" + keySuffix).
		SetDeploymentName("Change Deployment " + keySuffix).
		SetDeploymentTime(time.Now()).
		SetDeployedBy("test").
		SetIsActive(true).
		SetTenantID(tenantID).
		Save(ctx)
	require.NoError(t, err)

	def, err := client.ProcessDefinition.Create().
		SetKey(defKey).
		SetName("Change Bridge Approval " + keySuffix).
		SetVersion("1").
		SetIsLatest(true).
		SetBpmnXML([]byte(bpmnXML)).
		SetDeploymentID(deployment.ID).
		SetDeployedAt(time.Now()).
		SetTenantID(tenantID).
		Save(ctx)
	require.NoError(t, err)

	instance, err := client.ProcessInstance.Create().
		SetProcessInstanceID("CHG-PI-" + keySuffix).
		SetProcessDefinitionKey(def.Key).
		SetProcessDefinitionID(def.ID).
		SetBusinessKey(businessKey).
		SetStatus("running").
		SetVariables(map[string]interface{}{
			"business_type": "change",
			"business_key":  businessKey,
		}).
		SetTenantID(tenantID).
		Save(ctx)
	require.NoError(t, err)

	task, err := client.ProcessTask.Create().
		SetTaskID("CHG-TASK-" + keySuffix).
		SetTaskDefinitionKey("Approval_1").
		SetTaskName("变更审批").
		SetTaskType("user_task").
		SetProcessDefinitionKey(def.Key).
		SetProcessInstanceID(instance.ID).
		SetAssignee(strconv.Itoa(assigneeID)).
		SetStatus("assigned").
		SetTenantID(tenantID).
		Save(ctx)
	require.NoError(t, err)
	return task.ID
}

// TestTransitionStatus_BridgesBPMNTask 变更审批端到端：
// 审批通过时应同时完成绑定的 BPMN 待办任务，并更新业务审批记录与变更状态。
func TestTransitionStatus_BridgesBPMNTask(t *testing.T) {
	entClient := newChangeBridgeEntClient(t, "change_bridge_e2e")
	tenantID, actorID := setupChangeBridgeActor(t, entClient, "e2e")
	repo := newMockRepository()
	svc := NewService(repo, entClient, zaptest.NewLogger(t).Sugar())
	ctx := context.Background()

	c := createTestChange(repo, tenantID, actorID)
	c.Status = "pending"
	rec, err := repo.CreateApprovalRecord(ctx, &ApprovalRecord{
		ChangeID:   c.ID,
		ApproverID: actorID,
		Status:     "pending",
	})
	require.NoError(t, err)
	taskID := createChangeBridgeProcessFixture(t, entClient, tenantID, "e2e1",
		fmt.Sprintf("change:%d", c.ID), actorID)

	updated, err := svc.TransitionStatus(ctx, c.ID, tenantID, actorID, "approved", "同意实施")
	require.NoError(t, err)
	assert.Equal(t, "approved", updated.Status)

	// BPMN 任务已完成
	task, err := entClient.ProcessTask.Get(ctx, taskID)
	require.NoError(t, err)
	assert.Equal(t, "completed", task.Status)

	// 业务审批记录已更新
	assert.Equal(t, "approved", repo.approvals[rec.ID].Status)

	// 流程审批决策带正确的业务上下文
	decisions, err := entClient.ProcessApprovalDecision.Query().All(ctx)
	require.NoError(t, err)
	require.Len(t, decisions, 1)
	assert.Equal(t, "approve", decisions[0].Action)
	assert.Equal(t, actorID, decisions[0].ActorID)
	assert.Equal(t, "change", decisions[0].BusinessType)
	assert.Equal(t, "同意实施", decisions[0].Comment)
}

// TestTransitionStatus_BridgeFailClosed 失败关闭回归：
// 存在待办流程任务但操作人不是流程审批人时，变更审批必须整体中止，双轨状态均不变。
func TestTransitionStatus_BridgeFailClosed(t *testing.T) {
	entClient := newChangeBridgeEntClient(t, "change_bridge_failclosed")
	tenantID, actorID := setupChangeBridgeActor(t, entClient, "fc")
	repo := newMockRepository()
	svc := NewService(repo, entClient, zaptest.NewLogger(t).Sugar())
	ctx := context.Background()

	c := createTestChange(repo, tenantID, actorID)
	c.Status = "pending"
	rec, err := repo.CreateApprovalRecord(ctx, &ApprovalRecord{
		ChangeID:   c.ID,
		ApproverID: actorID,
		Status:     "pending",
	})
	require.NoError(t, err)
	// 流程任务指派给其他人，业务审批人无权完成流程任务
	taskID := createChangeBridgeProcessFixture(t, entClient, tenantID, "fc1",
		fmt.Sprintf("change:%d", c.ID), actorID+1000)

	_, err = svc.TransitionStatus(ctx, c.ID, tenantID, actorID, "rejected", "不同意")
	require.Error(t, err)
	assert.Contains(t, err.Error(), "同步流程审批任务失败")

	// 双轨状态均未被修改
	task, err := entClient.ProcessTask.Get(ctx, taskID)
	require.NoError(t, err)
	assert.Equal(t, "assigned", task.Status)
	assert.Equal(t, "pending", repo.changes[c.ID].Status)
	assert.Equal(t, "pending", repo.approvals[rec.ID].Status)

	decisionCount, err := entClient.ProcessApprovalDecision.Query().Count(ctx)
	require.NoError(t, err)
	assert.Equal(t, 0, decisionCount)
}

// TestTransitionStatus_NoBoundInstanceFallsBack 无绑定流程实例时回退纯业务审批。
func TestTransitionStatus_NoBoundInstanceFallsBack(t *testing.T) {
	entClient := newChangeBridgeEntClient(t, "change_bridge_fallback")
	tenantID, actorID := setupChangeBridgeActor(t, entClient, "fb")
	repo := newMockRepository()
	svc := NewService(repo, entClient, zaptest.NewLogger(t).Sugar())
	ctx := context.Background()

	c := createTestChange(repo, tenantID, actorID)
	c.Status = "pending"
	_, err := repo.CreateApprovalRecord(ctx, &ApprovalRecord{
		ChangeID:   c.ID,
		ApproverID: actorID,
		Status:     "pending",
	})
	require.NoError(t, err)

	updated, err := svc.TransitionStatus(ctx, c.ID, tenantID, actorID, "approved", "同意")
	require.NoError(t, err)
	assert.Equal(t, "approved", updated.Status)
}

// ==================== completeChangeApprovalTask：CAB 审批完成 + 级联完成排期/驳回节点 ====================

// TestCompleteChangeApprovalTask_ApproveCompletesScheduleNode CAB 审批通过时，应该：
// 1. 完成 Activity_CABApproval 这个待办任务；
// 2. 级联完成网关路由到的 Activity_Schedule（排期节点），避免它变成孤儿任务；
// 3. Activity_Schedule 的 change_task/schedule_change 回调把 Change.Status 改成 approved；
// 4. 流程实例推进到 Activity_Schedule 之后的下一个节点 Activity_Implement（Track4 范围之外，故意停在那）。
func TestCompleteChangeApprovalTask_ApproveCompletesScheduleNode(t *testing.T) {
	client := newChangeBridgeEntClient(t, "complete_approval_approve")
	logger := zaptest.NewLogger(t).Sugar()
	ctx := context.Background()

	tenant, err := client.Tenant.Create().SetName("CAB Tenant").SetCode("cab-approve").SetDomain("cab-approve.example.com").SetStatus("active").Save(ctx)
	require.NoError(t, err)
	cmUser, err := client.User.Create().SetUsername("cm1").SetEmail("cm1@example.com").SetName("CM1").SetPasswordHash("h").SetRole("change_manager").SetActive(true).SetTenantID(tenant.ID).Save(ctx)
	require.NoError(t, err)
	// requester 必须跟 cmUser 是不同的人：createUserTask 的按角色候选人解析会把申请人
	// 自己从候选列表里剔除，如果 CM 恰好就是申请人，change_manager 角色下就一个人可选，
	// 排除之后候选人列表会变空，CAB 审批任务就分不出候选人。
	requester, err := client.User.Create().SetUsername("requester1").SetEmail("requester1@example.com").SetName("Requester1").SetPasswordHash("h").SetRole("agent").SetActive(true).SetTenantID(tenant.ID).Save(ctx)
	require.NoError(t, err)

	c, err := client.Change.Create().SetTitle("测试变更").SetType("normal").SetStatus("pending").SetRiskLevel("medium").SetImpactScope("low").SetTenantID(tenant.ID).SetCreatedBy(requester.ID).Save(ctx)
	require.NoError(t, err)

	engine := newTestBPMNEngine(t, client, logger)
	deploySvc := service.NewBPMNTemplateService(client)
	tenantCtx := context.WithValue(ctx, bpmn.BPMNTenantIDContextKey, tenant.ID)
	_, err = deploySvc.LoadAndDeployTemplates(tenantCtx, tenant.ID)
	require.NoError(t, err)

	trigger := service.NewProcessTriggerService(client, engine)
	_, err = trigger.TriggerProcess(tenantCtx, &dto.ProcessTriggerRequest{
		BusinessType:         dto.BusinessTypeChange,
		BusinessID:           c.ID,
		ProcessDefinitionKey: "change_normal_flow",
		Variables:            map[string]interface{}{"approval_required": true, "requester_id": float64(requester.ID)},
		TenantID:             tenant.ID,
	})
	require.NoError(t, err)

	// 完成"变更评估"这一步，推进到 CAB 审批
	assessmentTasks, _, err := engine.TaskService().ListUserTasks(tenantCtx, &service.ListUserTasksRequest{PageSize: 10})
	require.NoError(t, err)
	require.NoError(t, engine.CompleteTask(tenantCtx, assessmentTasks[0].TaskID, map[string]interface{}{}))

	repo := newMockRepository()
	svc := NewService(repo, client, logger)
	svc.SetProcessEngine(engine)

	err = svc.completeChangeApprovalTask(tenantCtx, tenant.ID, cmUser.ID, c.ID, "approve", "looks good")
	require.NoError(t, err)

	updated, err := client.Change.Get(ctx, c.ID)
	require.NoError(t, err)
	assert.Equal(t, "approved", updated.Status, "CAB 通过后应该级联完成排期节点，Change 状态应该变成 approved")

	instance, err := client.ProcessInstance.Query().Where(processinstance.BusinessKey(fmt.Sprintf("change:%d", c.ID)), processinstance.TenantID(tenant.ID)).Only(ctx)
	require.NoError(t, err)
	assert.Equal(t, "Activity_Implement", instance.CurrentActivityID,
		"级联完成排期节点后，流程应该停在变更实施这个新任务上——这是 Track4 范围边界之外的预期停留点，不是 bug")
}

// TestCompleteChangeApprovalTask_RejectEndsProcess CAB 审批驳回时，应该级联完成
// Activity_Reject，把 Change.Status 改成 rejected，并且流程实例直接走到 EndEvent 结束
// （不应该卡在 running）。
func TestCompleteChangeApprovalTask_RejectEndsProcess(t *testing.T) {
	client := newChangeBridgeEntClient(t, "complete_approval_reject")
	logger := zaptest.NewLogger(t).Sugar()
	ctx := context.Background()

	tenant, err := client.Tenant.Create().SetName("CAB Tenant Reject").SetCode("cab-reject").SetDomain("cab-reject.example.com").SetStatus("active").Save(ctx)
	require.NoError(t, err)
	cmUser, err := client.User.Create().SetUsername("cm2").SetEmail("cm2@example.com").SetName("CM2").SetPasswordHash("h").SetRole("change_manager").SetActive(true).SetTenantID(tenant.ID).Save(ctx)
	require.NoError(t, err)
	// requester 必须跟 cmUser 是不同的人，理由同上一个测试。
	requester, err := client.User.Create().SetUsername("requester2").SetEmail("requester2@example.com").SetName("Requester2").SetPasswordHash("h").SetRole("agent").SetActive(true).SetTenantID(tenant.ID).Save(ctx)
	require.NoError(t, err)

	c, err := client.Change.Create().SetTitle("测试变更-驳回").SetType("normal").SetStatus("pending").SetRiskLevel("medium").SetImpactScope("low").SetTenantID(tenant.ID).SetCreatedBy(requester.ID).Save(ctx)
	require.NoError(t, err)

	engine := newTestBPMNEngine(t, client, logger)
	deploySvc := service.NewBPMNTemplateService(client)
	tenantCtx := context.WithValue(ctx, bpmn.BPMNTenantIDContextKey, tenant.ID)
	_, err = deploySvc.LoadAndDeployTemplates(tenantCtx, tenant.ID)
	require.NoError(t, err)

	trigger := service.NewProcessTriggerService(client, engine)
	_, err = trigger.TriggerProcess(tenantCtx, &dto.ProcessTriggerRequest{
		BusinessType:         dto.BusinessTypeChange,
		BusinessID:           c.ID,
		ProcessDefinitionKey: "change_normal_flow",
		Variables:            map[string]interface{}{"approval_required": true, "requester_id": float64(requester.ID)},
		TenantID:             tenant.ID,
	})
	require.NoError(t, err)

	assessmentTasks, _, err := engine.TaskService().ListUserTasks(tenantCtx, &service.ListUserTasksRequest{PageSize: 10})
	require.NoError(t, err)
	require.NoError(t, engine.CompleteTask(tenantCtx, assessmentTasks[0].TaskID, map[string]interface{}{}))

	repo := newMockRepository()
	svc := NewService(repo, client, logger)
	svc.SetProcessEngine(engine)

	err = svc.completeChangeApprovalTask(tenantCtx, tenant.ID, cmUser.ID, c.ID, "reject", "风险过高，驳回")
	require.NoError(t, err)

	updated, err := client.Change.Get(ctx, c.ID)
	require.NoError(t, err)
	assert.Equal(t, "rejected", updated.Status)

	instance, err := client.ProcessInstance.Query().Where(processinstance.BusinessKey(fmt.Sprintf("change:%d", c.ID)), processinstance.TenantID(tenant.ID)).Only(ctx)
	require.NoError(t, err)
	assert.Equal(t, "completed", instance.Status, "驳回节点走 Flow_End 直接结束流程实例，不应该卡在 running")
}

// TestCompleteChangeApprovalTask_WrongActorRejected 没有 change_manager 角色的用户
// 不是 Activity_CABApproval 的候选人，尝试完成审批任务必须失败关闭：
// CompleteTask 内部的 authorizeTaskActor 校验会拒绝，Change.Status 不能被改动。
func TestCompleteChangeApprovalTask_WrongActorRejected(t *testing.T) {
	client := newChangeBridgeEntClient(t, "complete_approval_wrong_actor")
	logger := zaptest.NewLogger(t).Sugar()
	ctx := context.Background()

	tenant, err := client.Tenant.Create().SetName("CAB Tenant WrongActor").SetCode("cab-wrong-actor").SetDomain("cab-wrong-actor.example.com").SetStatus("active").Save(ctx)
	require.NoError(t, err)
	// cmUser 有 change_manager 角色，用来让 Activity_CABApproval 能解析出候选人；outsider 没有该角色，
	// 测试只需要它存在于候选人列表所在的租户里，Go 变量本身不需要再被引用。
	_, err = client.User.Create().SetUsername("cm3").SetEmail("cm3@example.com").SetName("CM3").SetPasswordHash("h").SetRole("change_manager").SetActive(true).SetTenantID(tenant.ID).Save(ctx)
	require.NoError(t, err)
	outsider, err := client.User.Create().SetUsername("outsider").SetEmail("outsider@example.com").SetName("Outsider").SetPasswordHash("h").SetRole("agent").SetActive(true).SetTenantID(tenant.ID).Save(ctx)
	require.NoError(t, err)
	// requester 必须跟 cmUser 是不同的人，理由同前两个测试：按角色候选人解析会把申请人自己排除掉。
	requester, err := client.User.Create().SetUsername("requester3").SetEmail("requester3@example.com").SetName("Requester3").SetPasswordHash("h").SetRole("agent").SetActive(true).SetTenantID(tenant.ID).Save(ctx)
	require.NoError(t, err)

	c, err := client.Change.Create().SetTitle("测试变更-越权").SetType("normal").SetStatus("pending").SetRiskLevel("medium").SetImpactScope("low").SetTenantID(tenant.ID).SetCreatedBy(requester.ID).Save(ctx)
	require.NoError(t, err)

	engine := newTestBPMNEngine(t, client, logger)
	deploySvc := service.NewBPMNTemplateService(client)
	tenantCtx := context.WithValue(ctx, bpmn.BPMNTenantIDContextKey, tenant.ID)
	_, err = deploySvc.LoadAndDeployTemplates(tenantCtx, tenant.ID)
	require.NoError(t, err)

	trigger := service.NewProcessTriggerService(client, engine)
	_, err = trigger.TriggerProcess(tenantCtx, &dto.ProcessTriggerRequest{
		BusinessType:         dto.BusinessTypeChange,
		BusinessID:           c.ID,
		ProcessDefinitionKey: "change_normal_flow",
		Variables:            map[string]interface{}{"approval_required": true, "requester_id": float64(requester.ID)},
		TenantID:             tenant.ID,
	})
	require.NoError(t, err)

	assessmentTasks, _, err := engine.TaskService().ListUserTasks(tenantCtx, &service.ListUserTasksRequest{PageSize: 10})
	require.NoError(t, err)
	require.NoError(t, engine.CompleteTask(tenantCtx, assessmentTasks[0].TaskID, map[string]interface{}{}))

	repo := newMockRepository()
	svc := NewService(repo, client, logger)
	svc.SetProcessEngine(engine)

	err = svc.completeChangeApprovalTask(tenantCtx, tenant.ID, outsider.ID, c.ID, "approve", "我批准")
	require.Error(t, err, "authorizeTaskActor 应该拒绝没有 change_manager 角色/不在候选人列表里的用户")

	updated, err := client.Change.Get(ctx, c.ID)
	require.NoError(t, err)
	assert.Equal(t, "pending", updated.Status, "越权调用失败后 Change.Status 不应该被改动")
}
