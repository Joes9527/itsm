package change

import (
	"context"
	"database/sql"
	"fmt"
	"strconv"
	"testing"
	"time"

	"itsm-backend/dto"
	"itsm-backend/ent"
	"itsm-backend/ent/enttest"
	"itsm-backend/ent/processinstance"
	"itsm-backend/ent/processtask"
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

// ==================== TransitionStatus ↔ BPMN 集成测试 ====================

func newChangeBPMNEntClient(t *testing.T, dbName string) *ent.Client {
	t.Helper()
	client := enttest.Open(t, "sqlite3", fmt.Sprintf("file:%s?mode=memory&cache=shared&_fk=1", dbName))
	t.Cleanup(func() { client.Close() })
	return client
}

// openChangeBPMNRawDB 打开一个指向跟 newChangeBPMNEntClient(t, dbName) 相同 sqlite
// 内存库的原生 *sql.DB 连接（相同 DSN，mode=memory&cache=shared 让它们共享同一份数据）。
// MarkSubmittedForApproval 用的是原生 database/sql，不是 ent，NewEntRepository 需要这个
// 连接才能真的执行 draft->pending 的 UPDATE。
func openChangeBPMNRawDB(t *testing.T, dbName string) *sql.DB {
	t.Helper()
	db, err := sql.Open("sqlite3", fmt.Sprintf("file:%s?mode=memory&cache=shared&_fk=1", dbName))
	require.NoError(t, err)
	t.Cleanup(func() { db.Close() })
	return db
}

func setupChangeBPMNActor(t *testing.T, client *ent.Client, code string) (tenantID, actorID int) {
	t.Helper()
	ctx := context.Background()
	tenant, err := client.Tenant.Create().
		SetName("Change BPMN Tenant " + code).
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

// createChangeBPMNProcessFixture 创建 运行中流程实例 + 待办用户任务，
// businessKey 采用与 ProcessTriggerService 相同的 "change:{id}" 约定。
func createChangeBPMNProcessFixture(t *testing.T, client *ent.Client, tenantID int, keySuffix, businessKey string, assigneeID int) (taskID int) {
	t.Helper()
	ctx := context.Background()

	defKey := "change_bridge_approval_" + keySuffix
	bpmnXML := fmt.Sprintf(`<?xml version="1.0" encoding="UTF-8"?><bpmn:definitions xmlns:bpmn="http://www.omg.org/spec/BPMN/20100524/MODEL" xmlns:itsm="https://github.com/heidsoft/itsm/schema/bpmn" id="Definitions_%s" targetNamespace="https://github.com/heidsoft/itsm"><bpmn:process id="%s" name="Change BPMN Approval %s" isExecutable="true"><bpmn:startEvent id="StartEvent_1"/><bpmn:userTask id="Approval_1" name="变更审批" itsm:taskPurpose="approval" itsm:approvalMode="single" itsm:assignee="%d"/><bpmn:endEvent id="EndEvent_1"/><bpmn:sequenceFlow id="Flow_1" sourceRef="StartEvent_1" targetRef="Approval_1"/><bpmn:sequenceFlow id="Flow_2" sourceRef="Approval_1" targetRef="EndEvent_1"/></bpmn:process></bpmn:definitions>`, defKey, defKey, keySuffix, assigneeID)

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
		SetName("Change BPMN Approval " + keySuffix).
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

// NOTE: TestTransitionStatus_BridgesBPMNTask / TestTransitionStatus_BridgeFailClosed /
// TestTransitionStatus_NoBoundInstanceFallsBack used to live here. They were deleted in
// Track4 Task 4 because they locked in the mechanism details of a since-removed
// bridge shim (BPMNApprovalBridge.CompleteBusinessApprovalTask), all of which this
// task intentionally replaces:
//   - BridgesBPMNTask asserted a ProcessApprovalDecision row shaped by the removed
//     bridge call (action/actorID/businessType/comment) — that call no longer exists
//     in TransitionStatus.
//   - BridgeFailClosed asserted the literal error string "同步流程审批任务失败", which was
//     the removed bridge's own error-wrapping text, not a stable contract.
//   - NoBoundInstanceFallsBack asserted a "fall back to pure business approval when
//     no BPMN process instance is bound" semantic — this is the exact behavior the task
//     brief says to remove; the replacement requires a running BPMN process instance and
//     fails closed without one ("该变更没有正在运行的审批流程").
//
// The behavior these tests protected (actor authorization, fail-closed on unauthorized
// actor, approve/reject actually taking effect) is still covered — by
// TestTransitionStatus_Approve_UsesCompleteChangeApprovalTask,
// TestTransitionStatus_Approve_WrongActorRejected, and
// TestTransitionStatus_Reject_RequiresComment below, plus the completeChangeApprovalTask-
// specific test group above.

// ==================== completeChangeApprovalTask：CAB 审批完成 + 级联完成排期/驳回节点 ====================

// TestCompleteChangeApprovalTask_ApproveCompletesScheduleNode CAB 审批通过时，应该：
//  1. 完成 Activity_CABApproval 这个待办任务；
//  2. 级联完成网关路由到的 Activity_Schedule（排期节点），避免它变成孤儿任务；
//  3. Activity_Schedule 的 change_task/schedule_change 回调把 Change.Status 推进两跳，
//     从 pending 经过 approved 最终落到 scheduled（normal 类型的状态机里 approved 不是
//     终点——approved 只能到 {scheduled, cancelled}，不直接到 in_progress，所以
//     scheduleChange 必须走完两跳，否则变更会永久卡在 approved，见 Finding 4）；
//  4. 流程实例推进到 Activity_Schedule 之后的下一个节点 Activity_Implement（Track4 范围之外，故意停在那）。
func TestCompleteChangeApprovalTask_ApproveCompletesScheduleNode(t *testing.T) {
	client := newChangeBPMNEntClient(t, "complete_approval_approve")
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
	assert.Equal(t, "scheduled", updated.Status, "CAB 通过后应该级联完成排期节点，Change 状态应该推进到 scheduled（经过 approved 中间态）")

	instance, err := client.ProcessInstance.Query().Where(processinstance.BusinessKey(fmt.Sprintf("change:%d", c.ID)), processinstance.TenantID(tenant.ID)).Only(ctx)
	require.NoError(t, err)
	assert.Equal(t, "Activity_Implement", instance.CurrentActivityID,
		"级联完成排期节点后，流程应该停在变更实施这个新任务上——这是 Track4 范围边界之外的预期停留点，不是 bug")
}

// TestCompleteChangeApprovalTask_RejectEndsProcess CAB 审批驳回时，应该级联完成
// Activity_Reject，把 Change.Status 改成 rejected，并且流程实例直接走到 EndEvent 结束
// （不应该卡在 running）。
func TestCompleteChangeApprovalTask_RejectEndsProcess(t *testing.T) {
	client := newChangeBPMNEntClient(t, "complete_approval_reject")
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
	client := newChangeBPMNEntClient(t, "complete_approval_wrong_actor")
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

// TestCompleteChangeApprovalTask_FiltersDecoyTaskByDefinitionKey 用一个跟 CAB 审批
// 无关、但状态同样是"待办"的伪装任务（不同 TaskDefinitionKey，挂在同一个流程实例上）
// 验证两处查询都显式按 TaskDefinitionKey 过滤——如果查询退化成"随便捞一个待办
// user_task"，这个伪装任务会让 Only() 因为命中两行而报错。这是给 completeChangeApprovalTask
// 自身契约上的硬化测试：即使调用方违反"每次调用前流程实例最多只有一个待办 user_task"
// 的隐含假设，这个方法自己也不应该选错任务。
func TestCompleteChangeApprovalTask_FiltersDecoyTaskByDefinitionKey(t *testing.T) {
	client := newChangeBPMNEntClient(t, "complete_approval_decoy")
	logger := zaptest.NewLogger(t).Sugar()
	ctx := context.Background()

	tenant, err := client.Tenant.Create().SetName("CAB Tenant Decoy").SetCode("cab-decoy").SetDomain("cab-decoy.example.com").SetStatus("active").Save(ctx)
	require.NoError(t, err)
	cmUser, err := client.User.Create().SetUsername("cm4").SetEmail("cm4@example.com").SetName("CM4").SetPasswordHash("h").SetRole("change_manager").SetActive(true).SetTenantID(tenant.ID).Save(ctx)
	require.NoError(t, err)
	requester, err := client.User.Create().SetUsername("requester4").SetEmail("requester4@example.com").SetName("Requester4").SetPasswordHash("h").SetRole("agent").SetActive(true).SetTenantID(tenant.ID).Save(ctx)
	require.NoError(t, err)

	c, err := client.Change.Create().SetTitle("测试变更-伪装任务").SetType("normal").SetStatus("pending").SetRiskLevel("medium").SetImpactScope("low").SetTenantID(tenant.ID).SetCreatedBy(requester.ID).Save(ctx)
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

	instance, err := client.ProcessInstance.Query().Where(processinstance.BusinessKey(fmt.Sprintf("change:%d", c.ID)), processinstance.TenantID(tenant.ID)).Only(ctx)
	require.NoError(t, err)

	// 伪装任务：跟真实的 Activity_CABApproval 挂在同一个流程实例上，TaskType/Status
	// 都满足旧查询（无 TaskDefinitionKey 过滤）的匹配条件，唯独 TaskDefinitionKey
	// 不是 CAB 审批节点——如果两处查询没有显式按 TaskDefinitionKey 过滤，Only() 会
	// 因为命中两行而报错。
	decoyTask, err := client.ProcessTask.Create().
		SetTaskID("CHG-DECOY-" + strconv.Itoa(c.ID)).
		SetTaskDefinitionKey("Activity_SomeUnrelatedNode").
		SetTaskName("无关的伪装任务").
		SetTaskType("user_task").
		SetProcessDefinitionKey(instance.ProcessDefinitionKey).
		SetProcessInstanceID(instance.ID).
		SetAssignee(strconv.Itoa(cmUser.ID)).
		SetStatus("assigned").
		SetTenantID(tenant.ID).
		Save(ctx)
	require.NoError(t, err)

	repo := newMockRepository()
	svc := NewService(repo, client, logger)
	svc.SetProcessEngine(engine)

	err = svc.completeChangeApprovalTask(tenantCtx, tenant.ID, cmUser.ID, c.ID, "approve", "looks good")
	require.NoError(t, err, "查询应该被 TaskDefinitionKey 过滤精确命中 Activity_CABApproval，不受同实例下伪装任务干扰")

	updated, err := client.Change.Get(ctx, c.ID)
	require.NoError(t, err)
	assert.Equal(t, "scheduled", updated.Status)

	// 伪装任务应该原封不动——它既没被当成 CAB 审批任务完成，也没被当成级联的排期/驳回节点完成。
	decoyAfter, err := client.ProcessTask.Get(ctx, decoyTask.ID)
	require.NoError(t, err)
	assert.Equal(t, "assigned", decoyAfter.Status, "伪装任务不应该被查询命中或被完成")
}

// ==================== completeChangeApprovalTask 的重试/续做语义（代码审查发现：CAB 决定
// 已经落库，级联收尾若失败则无法重试） ====================

// TestCompleteChangeApprovalTask_ResumesCascadeAfterInterruptedCall 模拟"上一次调用已经
// 完成了 CAB 决定，但紧接着的级联步骤没有跑完"（比如级联那一步瞬时失败，或者进程在两步
// 之间崩溃）——直接绕开 completeChangeApprovalTask，手工把 CAB 任务完成（复现"决定已经
// 生效，级联还没做"这个中间态），再调用 completeChangeApprovalTask 一次，断言它能识别出
// 这是可以续做的场景，把级联做完，而不是报错"没有待处理的审批任务"。
func TestCompleteChangeApprovalTask_ResumesCascadeAfterInterruptedCall(t *testing.T) {
	client := newChangeBPMNEntClient(t, "complete_approval_resume")
	logger := zaptest.NewLogger(t).Sugar()
	ctx := context.Background()

	tenant, err := client.Tenant.Create().SetName("CAB Tenant Resume").SetCode("cab-resume").SetDomain("cab-resume.example.com").SetStatus("active").Save(ctx)
	require.NoError(t, err)
	cmUser, err := client.User.Create().SetUsername("cm-resume").SetEmail("cm-resume@example.com").SetName("CM Resume").SetPasswordHash("h").SetRole("change_manager").SetActive(true).SetTenantID(tenant.ID).Save(ctx)
	require.NoError(t, err)
	requester, err := client.User.Create().SetUsername("requester-resume").SetEmail("requester-resume@example.com").SetName("Requester Resume").SetPasswordHash("h").SetRole("agent").SetActive(true).SetTenantID(tenant.ID).Save(ctx)
	require.NoError(t, err)

	c, err := client.Change.Create().SetTitle("测试变更-续做级联").SetType("normal").SetStatus("pending").SetRiskLevel("medium").SetImpactScope("low").SetTenantID(tenant.ID).SetCreatedBy(requester.ID).Save(ctx)
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

	// 手工完成 CAB 任务——绕开 completeChangeApprovalTask，只做它原本会做的"第一步"，
	// 模拟"决定已经生效，级联还没做"这个中间态。
	cabTask, err := client.ProcessTask.Query().
		Where(processtask.HasProcessInstanceWith(processinstance.BusinessKey(fmt.Sprintf("change:%d", c.ID))), processtask.TaskDefinitionKey("Activity_CABApproval")).
		Only(ctx)
	require.NoError(t, err)
	actorCtx := context.WithValue(tenantCtx, bpmn.BPMNUserIDContextKey, cmUser.ID)
	require.NoError(t, engine.CompleteTask(actorCtx, cabTask.TaskID, map[string]interface{}{
		"change_id":       c.ID,
		"approvalAction":  "approve",
		"approvalResult":  "approved",
		"approvalComment": "looks good",
	}))

	// 确认中间态：CAB 任务已完成，级联的排期节点还卡在待办。
	scheduleTask, err := client.ProcessTask.Query().
		Where(processtask.HasProcessInstanceWith(processinstance.BusinessKey(fmt.Sprintf("change:%d", c.ID))), processtask.TaskDefinitionKey("Activity_Schedule")).
		Only(ctx)
	require.NoError(t, err)
	require.Contains(t, []string{"created", "assigned"}, scheduleTask.Status, "前置条件：级联步骤应该还没做")

	repo := newMockRepository()
	svc := NewService(repo, client, logger)
	svc.SetProcessEngine(engine)

	// 续做：这次调用应该识别出 CAB 决定已经生效，直接把级联做完，而不是报"没有待处理的审批任务"。
	err = svc.completeChangeApprovalTask(tenantCtx, tenant.ID, cmUser.ID, c.ID, "approve", "looks good")
	require.NoError(t, err, "应该能识别出这是可以续做的重试场景，不应该报错")

	updated, err := client.Change.Get(ctx, c.ID)
	require.NoError(t, err)
	assert.Equal(t, "scheduled", updated.Status, "续做级联后变更应该正确推进到 scheduled")
}

// TestCompleteChangeApprovalTask_RetryAfterFullSuccessIsNoop 覆盖"整个操作其实已经成功，
// 调用方只是没收到成功响应就重试"——不应该报错，按幂等处理。
func TestCompleteChangeApprovalTask_RetryAfterFullSuccessIsNoop(t *testing.T) {
	client := newChangeBPMNEntClient(t, "complete_approval_retry_noop")
	logger := zaptest.NewLogger(t).Sugar()
	ctx := context.Background()

	tenant, err := client.Tenant.Create().SetName("CAB Tenant RetryNoop").SetCode("cab-retry-noop").SetDomain("cab-retry-noop.example.com").SetStatus("active").Save(ctx)
	require.NoError(t, err)
	cmUser, err := client.User.Create().SetUsername("cm-retry-noop").SetEmail("cm-retry-noop@example.com").SetName("CM RetryNoop").SetPasswordHash("h").SetRole("change_manager").SetActive(true).SetTenantID(tenant.ID).Save(ctx)
	require.NoError(t, err)
	requester, err := client.User.Create().SetUsername("requester-retry-noop").SetEmail("requester-retry-noop@example.com").SetName("Requester RetryNoop").SetPasswordHash("h").SetRole("agent").SetActive(true).SetTenantID(tenant.ID).Save(ctx)
	require.NoError(t, err)

	c, err := client.Change.Create().SetTitle("测试变更-幂等重试").SetType("normal").SetStatus("pending").SetRiskLevel("medium").SetImpactScope("low").SetTenantID(tenant.ID).SetCreatedBy(requester.ID).Save(ctx)
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

	// 第一次调用：真正完成审批 + 级联。
	require.NoError(t, svc.completeChangeApprovalTask(tenantCtx, tenant.ID, cmUser.ID, c.ID, "approve", "looks good"))

	// 第二次调用：整个操作其实已经成功了，调用方只是重试——不应该报错。
	err = svc.completeChangeApprovalTask(tenantCtx, tenant.ID, cmUser.ID, c.ID, "approve", "looks good")
	require.NoError(t, err, "已经完整成功过的操作被重复调用，应该按幂等处理而不是报错")
}

// TestCompleteChangeApprovalTask_RetryWithMismatchedActionRejected 覆盖"重试请求的
// action 跟已经生效的决定不一致"——必须明确报错，不能静默按旧决定生效，也不能悄悄改判。
func TestCompleteChangeApprovalTask_RetryWithMismatchedActionRejected(t *testing.T) {
	client := newChangeBPMNEntClient(t, "complete_approval_retry_mismatch")
	logger := zaptest.NewLogger(t).Sugar()
	ctx := context.Background()

	tenant, err := client.Tenant.Create().SetName("CAB Tenant Mismatch").SetCode("cab-retry-mismatch").SetDomain("cab-retry-mismatch.example.com").SetStatus("active").Save(ctx)
	require.NoError(t, err)
	cmUser, err := client.User.Create().SetUsername("cm-retry-mismatch").SetEmail("cm-retry-mismatch@example.com").SetName("CM Mismatch").SetPasswordHash("h").SetRole("change_manager").SetActive(true).SetTenantID(tenant.ID).Save(ctx)
	require.NoError(t, err)
	requester, err := client.User.Create().SetUsername("requester-retry-mismatch").SetEmail("requester-retry-mismatch@example.com").SetName("Requester Mismatch").SetPasswordHash("h").SetRole("agent").SetActive(true).SetTenantID(tenant.ID).Save(ctx)
	require.NoError(t, err)

	c, err := client.Change.Create().SetTitle("测试变更-矛盾重试").SetType("normal").SetStatus("pending").SetRiskLevel("medium").SetImpactScope("low").SetTenantID(tenant.ID).SetCreatedBy(requester.ID).Save(ctx)
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

	// 手工完成 CAB 任务为 approve，让级联卡在 Activity_Schedule。
	cabTask, err := client.ProcessTask.Query().
		Where(processtask.HasProcessInstanceWith(processinstance.BusinessKey(fmt.Sprintf("change:%d", c.ID))), processtask.TaskDefinitionKey("Activity_CABApproval")).
		Only(ctx)
	require.NoError(t, err)
	actorCtx := context.WithValue(tenantCtx, bpmn.BPMNUserIDContextKey, cmUser.ID)
	require.NoError(t, engine.CompleteTask(actorCtx, cabTask.TaskID, map[string]interface{}{
		"change_id":       c.ID,
		"approvalAction":  "approve",
		"approvalResult":  "approved",
		"approvalComment": "looks good",
	}))

	repo := newMockRepository()
	svc := NewService(repo, client, logger)
	svc.SetProcessEngine(engine)

	// 用跟已经生效的决定（approve）矛盾的 action（reject）重试。
	err = svc.completeChangeApprovalTask(tenantCtx, tenant.ID, cmUser.ID, c.ID, "reject", "改主意了")
	require.Error(t, err, "跟已经生效的决定矛盾的重试必须明确报错，不能静默生效")
	assert.Contains(t, err.Error(), "已经生效")

	// 排期节点应该原封不动，没有被误判成驳回节点完成掉。
	scheduleTask, err := client.ProcessTask.Query().
		Where(processtask.HasProcessInstanceWith(processinstance.BusinessKey(fmt.Sprintf("change:%d", c.ID))), processtask.TaskDefinitionKey("Activity_Schedule")).
		Only(ctx)
	require.NoError(t, err)
	assert.Contains(t, []string{"created", "assigned"}, scheduleTask.Status, "矛盾的重试被拒绝后，排期节点不应该被完成")
}

// ==================== TransitionStatus approve/reject 完全交给 BPMN（Track4 Task 4） ====================

// setupChangeForTransitionStatusTest 是本任务三条测试共用的 fixture 搭建：建
// tenant/change_manager 用户/change，部署 BPMN 模板，触发 change_normal_flow，
// 完成变更评估任务，推进到 CAB 审批节点。返回 client/svc/tenant/cmUser/change 供
// 各测试按需使用。dbName 必须每个测试唯一，避免 sqlite 内存库互相污染。
func setupChangeForTransitionStatusTest(t *testing.T, dbName string) (*ent.Client, *Service, *ent.Tenant, *ent.User, *ent.Change) {
	t.Helper()
	client := newChangeBPMNEntClient(t, dbName)
	logger := zaptest.NewLogger(t).Sugar()
	ctx := context.Background()

	tenant, err := client.Tenant.Create().SetName("TransitionStatus Tenant").SetCode(dbName).SetDomain(dbName + ".example.com").SetStatus("active").Save(ctx)
	require.NoError(t, err)
	// change_manager 角色是 User.Role 这个平铺字段（这个代码库里没有单独的 UserRole 关联表），
	// authorizeTaskActor 靠 resolveRoleCandidates 按 tenant + role="change_manager" 查询候选人。
	cmUser, err := client.User.Create().SetUsername(dbName + "-cm").SetEmail(dbName + "-cm@example.com").SetName("CM").SetPasswordHash("h").SetRole("change_manager").SetActive(true).SetTenantID(tenant.ID).Save(ctx)
	require.NoError(t, err)
	// requester 必须跟 cmUser 是不同的人：按角色候选人解析会把申请人自己从候选列表里剔除，
	// 如果 CM 恰好就是申请人，change_manager 角色下就一个人可选，排除之后候选人列表会变空，
	// CAB 审批任务就分不出候选人（同 completeChangeApprovalTask 系列测试里的既有教训）。
	requester, err := client.User.Create().SetUsername(dbName + "-req").SetEmail(dbName + "-req@example.com").SetName("Requester").SetPasswordHash("h").SetRole("agent").SetActive(true).SetTenantID(tenant.ID).Save(ctx)
	require.NoError(t, err)

	c, err := client.Change.Create().SetTitle("TransitionStatus 测试变更").SetType("normal").SetStatus("pending").SetRiskLevel("medium").SetImpactScope("low").SetTenantID(tenant.ID).SetCreatedBy(requester.ID).Save(ctx)
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

	// db=nil：TransitionStatus 的 approve/reject 路径不再触碰 ApprovalHistory/ApprovalRecord
	// 相关的原始 SQL 方法（那些方法用 r.db），完全走 ent 的 Get/Update，所以这里不需要真的
	// database/sql 连接。
	repo := NewEntRepository(client, nil)
	svc := NewService(repo, client, logger)
	svc.SetProcessEngine(engine)
	svc.SetProcessTriggerService(trigger)

	return client, svc, tenant, cmUser, c
}

func TestTransitionStatus_Approve_UsesCompleteChangeApprovalTask(t *testing.T) {
	client, svc, tenant, cmUser, c := setupChangeForTransitionStatusTest(t, "transition_approve")
	ctx := context.Background()

	_, err := svc.TransitionStatus(ctx, c.ID, tenant.ID, cmUser.ID, "approved", "looks good")
	require.NoError(t, err, "不再要求 ApprovalHistory 里有一条 pending 记录——审批人校验完全交给 BPMN authorizeTaskActor")

	updated, err := client.Change.Get(ctx, c.ID)
	require.NoError(t, err)
	assert.Equal(t, "scheduled", updated.Status, "状态由 BPMN 回调写入，不是 TransitionStatus 自己手动 set 的；normal 类型两跳推进到 scheduled")
}

func TestTransitionStatus_Reject_RequiresComment(t *testing.T) {
	client, svc, tenant, cmUser, c := setupChangeForTransitionStatusTest(t, "transition_reject_comment")
	ctx := context.Background()

	_, err := svc.TransitionStatus(ctx, c.ID, tenant.ID, cmUser.ID, "rejected", "")
	require.Error(t, err, "驳回必须填写意见，跟 SubmitTaskDecision 的既有约束保持一致")

	updated, err := client.Change.Get(ctx, c.ID)
	require.NoError(t, err)
	assert.Equal(t, "pending", updated.Status, "comment 为空时应该在调用 BPMN 之前就被拒绝，状态不应该变")
}

func TestTransitionStatus_Approve_WrongActorRejected(t *testing.T) {
	client, svc, tenant, _, c := setupChangeForTransitionStatusTest(t, "transition_wrong_actor")
	ctx := context.Background()

	outsider, err := client.User.Create().SetUsername("transition-outsider").SetEmail("transition-outsider@example.com").SetName("Outsider").SetPasswordHash("h").SetRole("agent").SetActive(true).SetTenantID(tenant.ID).Save(ctx)
	require.NoError(t, err)

	_, err = svc.TransitionStatus(ctx, c.ID, tenant.ID, outsider.ID, "approved", "我批准")
	require.Error(t, err, "非 change_manager 角色的用户不应该能通过 authorizeTaskActor 校验")

	updated, err := client.Change.Get(ctx, c.ID)
	require.NoError(t, err)
	assert.Equal(t, "pending", updated.Status, "越权调用失败后不应该残留任何状态变化")
}

// TestTransitionStatus_Approve_NoRunningProcessInstanceFailsClosed 覆盖移除旧的桥接
// 机制之后的行为反转：旧实现在没有关联运行中流程实例时会静默回退为纯业务审批（approve 照样成功）；
// 新实现完全交给 completeChangeApprovalTask，没有运行中的 ProcessInstance 时必须 fail-closed，
// 不能再有任何"没有流程就自己批"的隐藏路径。这个 change 是直接建库记录，从未跑过
// SubmitChange/TriggerProcess，所以底下压根没有 ProcessInstance 行——故意不调用
// setupChangeForTransitionStatusTest（那个 fixture 会部署模板+触发流程+推进到 CAB 节点，
// 正是这个测试要排除的前提）。
func TestTransitionStatus_Approve_NoRunningProcessInstanceFailsClosed(t *testing.T) {
	client := newChangeBPMNEntClient(t, "transition_no_running_instance")
	logger := zaptest.NewLogger(t).Sugar()
	ctx := context.Background()

	tenant, err := client.Tenant.Create().SetName("NoInstance Tenant").SetCode("transition_no_running_instance").SetDomain("transition-no-running-instance.example.com").SetStatus("active").Save(ctx)
	require.NoError(t, err)
	cmUser, err := client.User.Create().SetUsername("no-instance-cm").SetEmail("no-instance-cm@example.com").SetName("CM").SetPasswordHash("h").SetRole("change_manager").SetActive(true).SetTenantID(tenant.ID).Save(ctx)
	require.NoError(t, err)

	// 直接建库记录，不走 SubmitChange/TriggerProcess——不部署模板、不触发流程，
	// 所以这个 change 底下没有任何 ProcessInstance。
	c, err := client.Change.Create().SetTitle("无绑定流程实例的变更").SetType("normal").SetStatus("pending").SetRiskLevel("medium").SetImpactScope("low").SetTenantID(tenant.ID).SetCreatedBy(cmUser.ID).Save(ctx)
	require.NoError(t, err)

	// processEngine 必须非 nil，否则会在 completeChangeApprovalTask 里更早地因为
	// "流程引擎未初始化" 返回，测不到这个用例真正要覆盖的"没有运行中流程实例"分支。
	engine := newTestBPMNEngine(t, client, logger)
	repo := NewEntRepository(client, nil)
	svc := NewService(repo, client, logger)
	svc.SetProcessEngine(engine)

	_, err = svc.TransitionStatus(ctx, c.ID, tenant.ID, cmUser.ID, "approved", "批准")
	require.Error(t, err, "没有运行中的 BPMN 流程实例时，approve 必须 fail-closed，不能静默回退为纯业务审批")
	assert.Contains(t, err.Error(), "该变更没有正在运行的审批流程", "断言的是 completeChangeApprovalTask 真实返回的错误文案，不是臆造的错误")

	updated, err := client.Change.Get(ctx, c.ID)
	require.NoError(t, err)
	assert.Equal(t, "pending", updated.Status, "fail-closed 之后 Change.Status 不应该被悄悄改成 approved")
}

// ==================== SubmitChange 自动级联完成 Activity_Assessment ====================
//
// 生产环境里没有任何业务入口能完成"变更评估"这个 userTask——handlers/change 包本身
// 没有对应的端点，唯一能完成它的是通用 BPMN 工作流控制台（权限模型、UI 都不一样）。
// 这意味着 SubmitChange 触发流程之后，流程会永远停在 Activity_Assessment，CAB 审批任务
// 根本不会产生。这里让 SubmitChange 用系统身份（不是 requester 的身份）自动级联完成
// Activity_Assessment，把流程直接推进到 CAB 审批——跟 completeChangeApprovalTask 级联
// 完成 Activity_Schedule/Activity_Reject 是同一个模式：这不是一次独立的人工决定，
// 是"提交审批"这个动作本身自然应该做完的事。

func TestSubmitChange_AutoCompletesAssessmentTask(t *testing.T) {
	dsn := "file:submit_change_assessment_cascade?mode=memory&cache=shared&_fk=1"
	client := enttest.Open(t, "sqlite3", dsn)
	defer client.Close()
	// MarkSubmittedForApproval 用的是原生 database/sql，不是 ent——另开一个指向同一个
	// sqlite 内存库的 *sql.DB 连接，跟 change_bpmn_e2e_test.go 的做法一致。
	db, err := sql.Open("sqlite3", dsn)
	require.NoError(t, err)
	defer db.Close()
	logger := zaptest.NewLogger(t).Sugar()
	ctx := context.Background()

	tenant, err := client.Tenant.Create().SetName("Assessment Cascade Tenant").SetCode("assess-cascade").SetDomain("assess-cascade.example.com").SetStatus("active").Save(ctx)
	require.NoError(t, err)
	requester, err := client.User.Create().SetUsername("assess-requester").SetEmail("assess-requester@example.com").SetName("Requester").SetPasswordHash("h").SetRole("agent").SetActive(true).SetTenantID(tenant.ID).Save(ctx)
	require.NoError(t, err)
	cmUser, err := client.User.Create().SetUsername("assess-cm").SetEmail("assess-cm@example.com").SetName("CM").SetPasswordHash("h").SetRole("change_manager").SetActive(true).SetTenantID(tenant.ID).Save(ctx)
	require.NoError(t, err)

	engine := newTestBPMNEngine(t, client, logger)
	deploySvc := service.NewBPMNTemplateService(client)
	tenantCtx := context.WithValue(ctx, bpmn.BPMNTenantIDContextKey, tenant.ID)
	_, err = deploySvc.LoadAndDeployTemplates(tenantCtx, tenant.ID)
	require.NoError(t, err)

	trigger := service.NewProcessTriggerService(client, engine)
	repo := NewEntRepository(client, db)
	svc := NewService(repo, client, logger)
	svc.SetProcessTriggerService(trigger)
	svc.SetProcessEngine(engine)

	created, err := repo.Create(ctx, &Change{Title: "自动推进评估的变更", Type: "normal", Status: "draft", RiskLevel: "medium", ImpactScope: "low", TenantID: tenant.ID, CreatedBy: requester.ID})
	require.NoError(t, err)

	_, err = svc.SubmitChange(tenantCtx, created.ID, tenant.ID, requester.ID, &dto.SubmitChangeRequest{})
	require.NoError(t, err)

	instance, err := client.ProcessInstance.Query().
		Where(processinstance.BusinessKey(fmt.Sprintf("change:%d", created.ID)), processinstance.TenantID(tenant.ID)).
		Only(ctx)
	require.NoError(t, err)
	assert.Equal(t, "Activity_CABApproval", instance.CurrentActivityID,
		"SubmitChange 应该自动完成 Activity_Assessment，流程实例应该直接停在 CAB 审批节点上，不需要任何人工干预")

	// 断言 CAB 审批任务真的存在且可以正常走完——证明级联完成 Assessment 没有留下
	// 残留状态或者错误的 task 记录，CAB 审批本身完全不受影响。
	_, err = svc.TransitionStatus(tenantCtx, created.ID, tenant.ID, cmUser.ID, "approved", "自动推进后正常审批")
	require.NoError(t, err)
}

// ==================== BackfillLegacyPendingChange（代码审查发现：上线切换时刻的存量
// pending 变更没有回填路径） ====================

func TestBackfillLegacyPendingChange_CreatesInstanceAndAdvancesToCAB(t *testing.T) {
	client := newChangeBPMNEntClient(t, "backfill_legacy_pending")
	tenantID, actorID := setupChangeBPMNActor(t, client, "backfill-legacy")
	engine, trigger := deployRealBPMNFixture(t, client, tenantID)
	logger := zaptest.NewLogger(t).Sugar()

	// 模拟旧审批链流程下已经提交到 pending、但从来没有对应 BPMN 流程实例的存量变更。
	c, err := client.Change.Create().SetTitle("上线前用旧流程提交的变更").SetType("normal").SetStatus("pending").SetRiskLevel("medium").SetImpactScope("low").SetTenantID(tenantID).SetCreatedBy(actorID).Save(context.Background())
	require.NoError(t, err)

	repo := NewEntRepository(client, nil)
	svc := NewService(repo, client, logger)
	svc.SetProcessTriggerService(trigger)
	svc.SetProcessEngine(engine)

	err = svc.BackfillLegacyPendingChange(context.Background(), c.ID, tenantID)
	require.NoError(t, err)

	instance, err := client.ProcessInstance.Query().
		Where(processinstance.BusinessKey(fmt.Sprintf("change:%d", c.ID)), processinstance.TenantID(tenantID)).
		Only(context.Background())
	require.NoError(t, err)
	assert.Equal(t, "running", instance.Status)
	assert.Equal(t, "Activity_CABApproval", instance.CurrentActivityID,
		"回填之后应该跟正常提交一样自动推进到 CAB 审批节点，不需要额外的人工干预")

	// 变更本身的状态不应该被回填动作改变——它已经是 pending，回填只是补上缺失的
	// 流程实例，不是重新走一遍提交流程。
	stillPending, err := client.Change.Get(context.Background(), c.ID)
	require.NoError(t, err)
	assert.Equal(t, "pending", stillPending.Status)
}

func TestBackfillLegacyPendingChange_SkipsChangeWithExistingInstance(t *testing.T) {
	client := newChangeBPMNEntClient(t, "backfill_existing_instance")
	tenantID, actorID := setupChangeBPMNActor(t, client, "backfill-existing")
	engine, trigger := deployRealBPMNFixture(t, client, tenantID)
	logger := zaptest.NewLogger(t).Sugar()

	c, err := client.Change.Create().SetTitle("已经有流程实例的变更").SetType("normal").SetStatus("pending").SetRiskLevel("medium").SetImpactScope("low").SetTenantID(tenantID).SetCreatedBy(actorID).Save(context.Background())
	require.NoError(t, err)

	tenantCtx := context.WithValue(context.Background(), bpmn.BPMNTenantIDContextKey, tenantID)
	_, err = trigger.TriggerProcess(tenantCtx, &dto.ProcessTriggerRequest{
		BusinessType:         dto.BusinessTypeChange,
		BusinessID:           c.ID,
		ProcessDefinitionKey: "change_normal_flow",
		Variables:            map[string]interface{}{"approval_required": true, "requester_id": float64(actorID)},
		TenantID:             tenantID,
	})
	require.NoError(t, err)

	repo := NewEntRepository(client, nil)
	svc := NewService(repo, client, logger)
	svc.SetProcessTriggerService(trigger)
	svc.SetProcessEngine(engine)

	err = svc.BackfillLegacyPendingChange(context.Background(), c.ID, tenantID)
	require.Error(t, err, "已经有流程实例（不管是不是这次 Track4 之前建的）就不该重复回填")

	count, err := client.ProcessInstance.Query().
		Where(processinstance.BusinessKey(fmt.Sprintf("change:%d", c.ID)), processinstance.TenantID(tenantID)).
		Count(context.Background())
	require.NoError(t, err)
	assert.Equal(t, 1, count, "不应该产生第二个流程实例")
}

func TestBackfillLegacyPendingChange_RejectsNonPendingChange(t *testing.T) {
	client := newChangeBPMNEntClient(t, "backfill_non_pending")
	tenantID, actorID := setupChangeBPMNActor(t, client, "backfill-non-pending")
	engine, trigger := deployRealBPMNFixture(t, client, tenantID)
	logger := zaptest.NewLogger(t).Sugar()

	c, err := client.Change.Create().SetTitle("还是草稿的变更").SetType("normal").SetStatus("draft").SetRiskLevel("medium").SetImpactScope("low").SetTenantID(tenantID).SetCreatedBy(actorID).Save(context.Background())
	require.NoError(t, err)

	repo := NewEntRepository(client, nil)
	svc := NewService(repo, client, logger)
	svc.SetProcessTriggerService(trigger)
	svc.SetProcessEngine(engine)

	err = svc.BackfillLegacyPendingChange(context.Background(), c.ID, tenantID)
	require.Error(t, err, "不是 pending 状态的变更不需要回填，应该明确拒绝而不是静默处理")

	exists, err := client.ProcessInstance.Query().
		Where(processinstance.BusinessKey(fmt.Sprintf("change:%d", c.ID)), processinstance.TenantID(tenantID)).
		Exist(context.Background())
	require.NoError(t, err)
	assert.False(t, exists, "被拒绝的回填不应该留下任何流程实例")
}
