package change

import (
	"context"
	"fmt"
	"testing"

	"itsm-backend/dto"
	"itsm-backend/ent"
	"itsm-backend/ent/processinstance"
	"itsm-backend/service"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"go.uber.org/zap/zaptest"
)

// mockProcessTriggerService is a minimal in-package fake for
// service.ProcessTriggerServiceInterface. Only TriggerProcess is exercised by
// SubmitChange; the remaining methods exist solely to satisfy the interface.
type mockProcessTriggerService struct {
	triggerCalls []*dto.ProcessTriggerRequest
	triggerErr   error
	cancelCalls  []int // recorded processInstanceID args
	cancelErr    error
}

func (m *mockProcessTriggerService) TriggerProcess(ctx context.Context, req *dto.ProcessTriggerRequest) (*dto.ProcessTriggerResponse, error) {
	m.triggerCalls = append(m.triggerCalls, req)
	if m.triggerErr != nil {
		return nil, m.triggerErr
	}
	return &dto.ProcessTriggerResponse{
		ProcessInstanceID:    1,
		ProcessDefinitionKey: req.ProcessDefinitionKey,
		BusinessKey:          "change:1",
		Status:               dto.ProcessStatusRunning,
	}, nil
}

func (m *mockProcessTriggerService) TriggerByBusinessType(ctx context.Context, businessType dto.BusinessType, businessID int, variables map[string]interface{}, triggeredBy string, tenantID int) (*dto.ProcessTriggerResponse, error) {
	return nil, nil
}

func (m *mockProcessTriggerService) CancelProcess(ctx context.Context, processInstanceID int, reason string, tenantID int) error {
	m.cancelCalls = append(m.cancelCalls, processInstanceID)
	return m.cancelErr
}

func (m *mockProcessTriggerService) SuspendProcess(ctx context.Context, processInstanceID int, reason string, tenantID int) error {
	return nil
}

func (m *mockProcessTriggerService) ResumeProcess(ctx context.Context, processInstanceID int, tenantID int) error {
	return nil
}

func (m *mockProcessTriggerService) GetProcessStatus(ctx context.Context, processInstanceID int, tenantID int) (*dto.ProcessTriggerResponse, error) {
	return nil, nil
}

// deployRealBPMNFixture 部署真实模板、构造真实引擎+触发服务，供需要验证 SubmitChange
// 真正把流程推进到什么状态（而不是只检查 mock 记录了什么调用参数）的测试复用。
// completeAssessmentTask 级联完成 Activity_Assessment 时，ChangeServiceTaskHandler 的
// 回调会直接对 entClient 里的真实 ent.Change 行做 UpdateOneID().Save()——用纯内存
// mockRepository 时这一步会因为找不到对应的真实行而失败，所以这里必须用真实引擎+
// 真实模板部署，不能用 mockProcessTriggerService。
func deployRealBPMNFixture(t *testing.T, entClient *ent.Client, tenantID int) (engine service.ProcessEngine, trigger *service.ProcessTriggerService) {
	t.Helper()
	engine = newTestBPMNEngine(t, entClient, zaptest.NewLogger(t).Sugar())
	deploySvc := service.NewBPMNTemplateService(entClient)
	tenantCtx := service.WithTrustedBPMNTenantContext(context.Background(), tenantID)
	_, err := deploySvc.LoadAndDeployTemplates(tenantCtx, tenantID)
	require.NoError(t, err)
	return engine, service.NewProcessTriggerService(entClient, engine)
}

// TestSubmitChange_TriggersBPMNProcess_Normal/_Emergency 验证 SubmitChange 按 Type
// 正确选择流程定义——用真实引擎断言触发后实际部署出来的 ProcessInstance.ProcessDefinitionKey，
// 比断言一个 mock 记录了什么调用参数更贴近真实行为（mock 记录的是"打算传什么"，
// 这里断言的是"真的触发成了什么"）。
func TestSubmitChange_TriggersBPMNProcess_Normal(t *testing.T) {
	entClient := newChangeBPMNEntClient(t, "change_submit_normal")
	tenantID, actorID := setupChangeBPMNActor(t, entClient, "submit-normal")
	engine, trigger := deployRealBPMNFixture(t, entClient, tenantID)

	workItem := createChangeWorkItemFixture(t, entClient, tenantID, actorID, "测试变更")
	c, err := entClient.Change.Create().SetType("normal").SetRiskLevel("medium").SetImpactScope("low").SetTenantID(tenantID).SetCreatedBy(actorID).SetWorkItemID(workItem.ID).Save(context.Background())
	require.NoError(t, err)

	repo := newTestChangeRepository(entClient, openChangeBPMNRawDB(t, "change_submit_normal"))
	svc := NewService(repo, entClient, zaptest.NewLogger(t).Sugar())
	svc.SetProcessTriggerService(trigger)
	svc.SetProcessEngine(engine)

	updated, err := svc.SubmitChange(context.Background(), c.ID, tenantID, actorID, &dto.SubmitChangeRequest{})
	require.NoError(t, err)
	assert.Equal(t, "pending", updated.Status)

	instance, err := entClient.ProcessInstance.Query().
		Where(processinstance.BusinessKey(fmt.Sprintf("change:%d", workItem.ID)), processinstance.TenantID(tenantID)).
		Only(context.Background())
	require.NoError(t, err)
	assert.Equal(t, "change_normal_flow", instance.ProcessDefinitionKey)
	assert.Equal(t, "Activity_CABApproval", instance.CurrentActivityID, "Assessment 应该已经被自动级联完成")
}

func TestSubmitChange_TriggersBPMNProcess_Emergency(t *testing.T) {
	entClient := newChangeBPMNEntClient(t, "change_submit_emergency")
	tenantID, actorID := setupChangeBPMNActor(t, entClient, "submit-emergency")
	engine, trigger := deployRealBPMNFixture(t, entClient, tenantID)

	workItem := createChangeWorkItemFixture(t, entClient, tenantID, actorID, "测试变更")
	c, err := entClient.Change.Create().SetType("emergency").SetRiskLevel("medium").SetImpactScope("low").SetTenantID(tenantID).SetCreatedBy(actorID).SetWorkItemID(workItem.ID).Save(context.Background())
	require.NoError(t, err)

	repo := newTestChangeRepository(entClient, openChangeBPMNRawDB(t, "change_submit_emergency"))
	svc := NewService(repo, entClient, zaptest.NewLogger(t).Sugar())
	svc.SetProcessTriggerService(trigger)
	svc.SetProcessEngine(engine)

	_, err = svc.SubmitChange(context.Background(), c.ID, tenantID, actorID, &dto.SubmitChangeRequest{})
	require.NoError(t, err)

	instance, err := entClient.ProcessInstance.Query().
		Where(processinstance.BusinessKey(fmt.Sprintf("change:%d", workItem.ID)), processinstance.TenantID(tenantID)).
		Only(context.Background())
	require.NoError(t, err)
	assert.Equal(t, "change_emergency_flow", instance.ProcessDefinitionKey)
}

// TestSubmitChange_RejectsDuplicateWhenRunningInstanceExists 幂等保护回归：
// 同一个 change 已经存在一个运行中的 BPMN 流程实例（businessKey = "change:{id}"）时，
// 重复提交必须被拒绝，且不能再次触发流程。
func TestSubmitChange_RejectsDuplicateWhenRunningInstanceExists(t *testing.T) {
	entClient := newChangeBPMNEntClient(t, "change_submit_duplicate")
	tenantID, actorID := setupChangeBPMNActor(t, entClient, "submit-dup")
	repo := newMockRepository()
	svc := NewService(repo, entClient, zaptest.NewLogger(t).Sugar())
	trigger := &mockProcessTriggerService{}
	svc.SetProcessTriggerService(trigger)

	c := seedChangeInMockAndEnt(t, repo, entClient, tenantID, actorID, "normal")
	require.NotNil(t, c.WorkItemID)
	createChangeBPMNProcessFixture(t, entClient, tenantID, "dup1", fmt.Sprintf("change:%d", *c.WorkItemID), actorID)

	_, err := svc.SubmitChange(context.Background(), c.ID, tenantID, actorID, &dto.SubmitChangeRequest{})
	require.Error(t, err)
	assert.Empty(t, trigger.triggerCalls)
}

// TestSubmitChange_TriggerProcessFailureLeavesChangeDraft 回归覆盖 Task 2 修复：
// TriggerProcess 失败时，change 必须原地保留在 draft（而不是被写成孤儿 pending），
// 这样才能通过 SubmitChange 的 draft 门槛重新提交。修复前的顺序是先
// MarkSubmittedForApproval 再 TriggerProcess，一旦 TriggerProcess 失败 change 就永久卡在
// pending 且没有任何修复路径。
func TestSubmitChange_TriggerProcessFailureLeavesChangeDraft(t *testing.T) {
	entClient := newChangeBPMNEntClient(t, "change_submit_trigger_fail")
	tenantID, actorID := setupChangeBPMNActor(t, entClient, "submit-trigger-fail")
	repo := newMockRepository()
	svc := NewService(repo, entClient, zaptest.NewLogger(t).Sugar())
	trigger := &mockProcessTriggerService{triggerErr: fmt.Errorf("bpmn engine unavailable")}
	svc.SetProcessTriggerService(trigger)

	c := seedChangeInMockAndEnt(t, repo, entClient, tenantID, actorID, "normal")

	_, err := svc.SubmitChange(context.Background(), c.ID, tenantID, actorID, &dto.SubmitChangeRequest{})
	require.Error(t, err)

	// MarkSubmittedForApproval must never have run: the change stays in draft in the
	// repo, so it can be resubmitted through the normal API instead of being stranded.
	stored, getErr := repo.Get(context.Background(), c.ID, tenantID)
	require.NoError(t, getErr)
	assert.Equal(t, "draft", stored.Status)

	require.Len(t, trigger.triggerCalls, 1, "TriggerProcess should have been attempted once")
}

func TestTransitionStatusRejectsSelfApprovalAndSelfRejection(t *testing.T) {
	client, svc, tenant, _, c := setupChangeForTransitionStatusTest(t, "transition_self_guard")
	ctx := context.Background()

	_, err := svc.TransitionStatus(ctx, c.ID, tenant.ID, c.CreatedBy, "approved", "我批准我自己")
	require.Error(t, err)
	require.ErrorContains(t, err, "不能审批自己提交的变更")

	updated, err := client.Change.Get(ctx, c.ID)
	require.NoError(t, err)
	updatedWorkItem, err := client.Ticket.Get(ctx, updated.WorkItemID)
	require.NoError(t, err)
	require.Equal(t, "pending", updatedWorkItem.Status)

	_, err = svc.TransitionStatus(ctx, c.ID, tenant.ID, c.CreatedBy, "rejected", "我驳回我自己")
	require.Error(t, err)
	require.ErrorContains(t, err, "不能驳回自己提交的变更")

	updated, err = client.Change.Get(ctx, c.ID)
	require.NoError(t, err)
	updatedWorkItem, err = client.Ticket.Get(ctx, updated.WorkItemID)
	require.NoError(t, err)
	require.Equal(t, "pending", updatedWorkItem.Status)
}

func TestTransitionStatusUsesRejectGuardBeforeGenericStateValidation(t *testing.T) {
	client, svc, tenant, changeManager, c := setupChangeForTransitionStatusTest(t, "transition_reject_state_reason")
	ctx := context.Background()

	_, err := client.Ticket.UpdateOneID(c.WorkItemID).SetStatus("draft").Save(ctx)
	require.NoError(t, err)

	_, err = svc.TransitionStatus(ctx, c.ID, tenant.ID, changeManager.ID, "rejected", "风险不可接受")
	require.ErrorContains(t, err, "只有已提交待审批的变更可以驳回")
}

// seedChangeInMockAndEnt 双写一个 change：一份进 mockRepository（供 s.repo.Get/
// MarkSubmittedForApproval 用，submitErr 可控失败），一份进真实 ent.Client（供
// completeAssessmentTask 触发的真实 BPMN 回调——ChangeServiceTaskHandler.updateChange
// 直接对 entClient 里的真实行做 UpdateOneID().Save()，纯内存 mock 满足不了这一步）。
// 两边用同一个 ID，保持一致。
//
// Wave 2 起也在 entClient 里先建一条真实的 tickets 行（WorkItem）并让 real Change 的
// work_item_id 指向它——Service.resolveWorkItemID（SubmitChange/completeAssessmentTask/
// completeChangeApprovalTask/cancelRunningProcessInstance 等businessKey 解析都靠它）
// 永远查真实 entClient，不读 mockRepository，所以这一步是这些方法在纯 mock repo 测试里
// 依然能正确工作的前提；mock 结构体上的 WorkItemID 只是为了跟真实行保持一致，不参与
// businessKey 解析。
func seedChangeInMockAndEnt(t *testing.T, repo *mockRepository, entClient *ent.Client, tenantID, actorID int, changeType string) *Change {
	t.Helper()
	workItem := createChangeWorkItemFixture(t, entClient, tenantID, actorID, "测试变更")
	real, err := entClient.Change.Create().
		SetType(changeType).
		SetRiskLevel("medium").
		SetImpactScope("low").
		SetTenantID(tenantID).
		SetCreatedBy(actorID).
		SetWorkItemID(workItem.ID).
		Save(context.Background())
	require.NoError(t, err)

	workItemID := workItem.ID
	c := &Change{
		ID:          real.ID,
		Title:       workItem.Title,
		Type:        changeType,
		Status:      "draft",
		RiskLevel:   "medium",
		ImpactScope: "low",
		TenantID:    tenantID,
		CreatedBy:   actorID,
		WorkItemID:  &workItemID,
	}
	repo.changes[c.ID] = c
	if c.ID >= repo.nextID {
		repo.nextID = c.ID + 1
	}
	return c
}

// TestSubmitChange_MarkSubmittedFailureCompensatesByCancellingProcess covers the Finding 2a
// fix: TriggerProcess succeeds (a running process instance now exists), but the subsequent
// MarkSubmittedForApproval (draft -> pending) fails. Without compensation, that running
// instance would sit forever and the idempotency guard at the top of SubmitChange would
// permanently reject any resubmission attempt for this change. SubmitChange must call
// CancelProcess on the just-created instance before returning the original error.
func TestSubmitChange_MarkSubmittedFailureCompensatesByCancellingProcess(t *testing.T) {
	entClient := newChangeBPMNEntClient(t, "change_submit_mark_fail_compensate")
	tenantID, actorID := setupChangeBPMNActor(t, entClient, "submit-mark-fail")
	engine, trigger := deployRealBPMNFixture(t, entClient, tenantID)

	repo := newMockRepository()
	repo.submitErr = fmt.Errorf("simulated concurrent write conflict")
	c := seedChangeInMockAndEnt(t, repo, entClient, tenantID, actorID, "normal")

	svc := NewService(repo, entClient, zaptest.NewLogger(t).Sugar())
	svc.SetProcessTriggerService(trigger)
	svc.SetProcessEngine(engine)

	_, err := svc.SubmitChange(context.Background(), c.ID, tenantID, actorID, &dto.SubmitChangeRequest{})
	require.Error(t, err)
	assert.Contains(t, err.Error(), "提交变更审批失败")

	// 补偿真的生效了：流程实例应该被 CancelProcess（= TerminateProcess）标记成
	// terminated，不会一直卡在 running 挡住后续重新提交的幂等检查。
	require.NotNil(t, c.WorkItemID)
	instance, err := entClient.ProcessInstance.Query().
		Where(processinstance.BusinessKey(fmt.Sprintf("change:%d", *c.WorkItemID)), processinstance.TenantID(tenantID)).
		Only(context.Background())
	require.NoError(t, err)
	assert.Equal(t, "terminated", instance.Status, "MarkSubmittedForApproval 失败后应该补偿取消刚创建的流程实例")

	// The change itself must remain in draft in the repo (MarkSubmittedForApproval never
	// committed the transition), consistent with the pre-existing draft-preservation guarantee.
	stored, getErr := repo.Get(context.Background(), c.ID, tenantID)
	require.NoError(t, getErr)
	assert.Equal(t, "draft", stored.Status)
}

// cancelAlwaysFailsTriggerService 包一层真实的 *service.ProcessTriggerService，只覆盖
// CancelProcess 让它总是失败——TriggerProcess 等其余方法透传给真实实现，这样
// completeAssessmentTask 需要的真实 ProcessInstance/ProcessTask 照样会被真实创建出来，
// 只有"补偿失败"这一件事是可控的。
type cancelAlwaysFailsTriggerService struct {
	*service.ProcessTriggerService
	cancelCalls []int
	cancelErr   error
}

func (w *cancelAlwaysFailsTriggerService) CancelProcess(ctx context.Context, processInstanceID int, reason string, tenantID int) error {
	w.cancelCalls = append(w.cancelCalls, processInstanceID)
	return w.cancelErr
}

// TestSubmitChange_MarkSubmittedFailure_CancelProcessAlsoFails_ReturnsOriginalError ensures
// a failure in the compensation call itself does not mask or replace the original
// MarkSubmittedForApproval error — the caller still needs to see why the submit failed.
func TestSubmitChange_MarkSubmittedFailure_CancelProcessAlsoFails_ReturnsOriginalError(t *testing.T) {
	entClient := newChangeBPMNEntClient(t, "change_submit_mark_fail_cancel_fail")
	tenantID, actorID := setupChangeBPMNActor(t, entClient, "submit-mark-fail-cancel-fail")
	engine, realTrigger := deployRealBPMNFixture(t, entClient, tenantID)
	trigger := &cancelAlwaysFailsTriggerService{
		ProcessTriggerService: realTrigger,
		cancelErr:             fmt.Errorf("bpmn engine unreachable"),
	}

	repo := newMockRepository()
	repo.submitErr = fmt.Errorf("simulated concurrent write conflict")
	c := seedChangeInMockAndEnt(t, repo, entClient, tenantID, actorID, "normal")

	svc := NewService(repo, entClient, zaptest.NewLogger(t).Sugar())
	svc.SetProcessTriggerService(trigger)
	svc.SetProcessEngine(engine)

	_, err := svc.SubmitChange(context.Background(), c.ID, tenantID, actorID, &dto.SubmitChangeRequest{})
	require.Error(t, err)
	assert.Contains(t, err.Error(), "提交变更审批失败")
	assert.Contains(t, err.Error(), "simulated concurrent write conflict")

	require.Len(t, trigger.cancelCalls, 1, "CancelProcess should still have been attempted despite the original error")
}

// TestGetApprovalHistory_ReadsFromProcessApprovalDecision covers Task 5: the
// change_approvals SQL table is no longer the source of truth for approval
// history. GetApprovalHistory must read from ent.ProcessApprovalDecision
// (the BPMN engine's own approval-decision audit table), filtered by
// business_type="change" + business_id=<changeID> + tenant_id, and map it
// onto the unchanged ApprovalRecord DTO shape.
func TestGetApprovalHistory_ReadsFromProcessApprovalDecision(t *testing.T) {
	entClient := newChangeBPMNEntClient(t, "change_approval_history")
	ctx := context.Background()

	tenant, err := entClient.Tenant.Create().SetName("T").SetCode("t-history").SetDomain("t-history.example.com").SetStatus("active").Save(ctx)
	require.NoError(t, err)
	actor, err := entClient.User.Create().SetUsername("cm").SetEmail("cm@example.com").SetName("CM User").SetPasswordHash("h").SetRole("agent").SetActive(true).SetTenantID(tenant.ID).Save(ctx)
	require.NoError(t, err)

	_, err = entClient.ProcessApprovalDecision.Create().
		SetProcessInstanceID(1).SetProcessTaskID(1).
		SetProcessInstanceKey("PI-test-1").SetTaskID("TASK-test-1").
		SetProcessDefinitionKey("change_normal_flow").SetNodeKey("Activity_CABApproval").
		SetBusinessType("change").SetBusinessID("42").
		SetActorID(actor.ID).SetActorName(actor.Name).
		SetAction("approve").SetDecision("approved").SetComment("looks good").
		SetVariablesSnapshot(map[string]interface{}{}).SetTenantID(tenant.ID).
		Save(ctx)
	require.NoError(t, err)

	repo := newTestChangeRepository(entClient, nil)
	history, err := repo.GetApprovalHistory(ctx, 42, tenant.ID)
	require.NoError(t, err)
	require.Len(t, history, 1)
	assert.Equal(t, actor.ID, history[0].ApproverID)
	assert.Equal(t, actor.Name, history[0].ApproverName)
	assert.Equal(t, "approved", history[0].Status)
	require.NotNil(t, history[0].Comment)
	assert.Equal(t, "looks good", *history[0].Comment)
	assert.NotNil(t, history[0].ApprovedAt, "通过的决策应该有 ApprovedAt")
}

// TestGetApprovalHistory_RejectedRecordHasNoApprovedAt 驳回记录不应该带 ApprovedAt——
// 那个字段只在决策是"通过"时才有意义，套用同一个 CreatedAt 时间戳会让调用方误以为
// 一条 rejected 记录同时也是"批准时间"。
func TestGetApprovalHistory_RejectedRecordHasNoApprovedAt(t *testing.T) {
	entClient := newChangeBPMNEntClient(t, "change_approval_history_rejected")
	ctx := context.Background()

	tenant, err := entClient.Tenant.Create().SetName("T").SetCode("t-history-rejected").SetDomain("t-history-rejected.example.com").SetStatus("active").Save(ctx)
	require.NoError(t, err)
	actor, err := entClient.User.Create().SetUsername("cm-r").SetEmail("cm-r@example.com").SetName("CM Rejecter").SetPasswordHash("h").SetRole("agent").SetActive(true).SetTenantID(tenant.ID).Save(ctx)
	require.NoError(t, err)

	_, err = entClient.ProcessApprovalDecision.Create().
		SetProcessInstanceID(1).SetProcessTaskID(1).
		SetProcessInstanceKey("PI-test-rejected").SetTaskID("TASK-test-rejected").
		SetProcessDefinitionKey("change_normal_flow").SetNodeKey("Activity_CABApproval").
		SetBusinessType("change").SetBusinessID("43").
		SetActorID(actor.ID).SetActorName(actor.Name).
		SetAction("reject").SetDecision("rejected").SetComment("风险太高").
		SetVariablesSnapshot(map[string]interface{}{}).SetTenantID(tenant.ID).
		Save(ctx)
	require.NoError(t, err)

	repo := newTestChangeRepository(entClient, nil)
	history, err := repo.GetApprovalHistory(ctx, 43, tenant.ID)
	require.NoError(t, err)
	require.Len(t, history, 1)
	assert.Equal(t, "rejected", history[0].Status)
	assert.Nil(t, history[0].ApprovedAt, "驳回记录不应该有 ApprovedAt")
}

// TestGetApprovalHistory_TenantIsolation guards against a filter that omits
// tenant_id: two tenants each get a ProcessApprovalDecision row with the
// same business_id (42), and a leaky query would return both.
func TestGetApprovalHistory_TenantIsolation(t *testing.T) {
	entClient := newChangeBPMNEntClient(t, "change_approval_history_tenant_iso")
	ctx := context.Background()

	tenantA, err := entClient.Tenant.Create().SetName("Tenant A").SetCode("t-iso-a").SetDomain("t-iso-a.example.com").SetStatus("active").Save(ctx)
	require.NoError(t, err)
	tenantB, err := entClient.Tenant.Create().SetName("Tenant B").SetCode("t-iso-b").SetDomain("t-iso-b.example.com").SetStatus("active").Save(ctx)
	require.NoError(t, err)
	actorA, err := entClient.User.Create().SetUsername("cm-a").SetEmail("cm-a@example.com").SetName("CM A").SetPasswordHash("h").SetRole("agent").SetActive(true).SetTenantID(tenantA.ID).Save(ctx)
	require.NoError(t, err)
	actorB, err := entClient.User.Create().SetUsername("cm-b").SetEmail("cm-b@example.com").SetName("CM B").SetPasswordHash("h").SetRole("agent").SetActive(true).SetTenantID(tenantB.ID).Save(ctx)
	require.NoError(t, err)

	// 两个租户各自一条 ProcessApprovalDecision，business_id 相同（都是 42）——
	// 这是租户隔离测试的关键：如果查询漏了 tenant_id 过滤，会把两条都返回。
	_, err = entClient.ProcessApprovalDecision.Create().
		SetProcessInstanceID(1).SetProcessTaskID(1).
		SetProcessInstanceKey("PI-iso-a").SetTaskID("TASK-iso-a").
		SetProcessDefinitionKey("change_normal_flow").SetNodeKey("Activity_CABApproval").
		SetBusinessType("change").SetBusinessID("42").
		SetActorID(actorA.ID).SetActorName(actorA.Name).
		SetAction("approve").SetDecision("approved").SetComment("tenant a").
		SetVariablesSnapshot(map[string]interface{}{}).SetTenantID(tenantA.ID).
		Save(ctx)
	require.NoError(t, err)
	_, err = entClient.ProcessApprovalDecision.Create().
		SetProcessInstanceID(2).SetProcessTaskID(2).
		SetProcessInstanceKey("PI-iso-b").SetTaskID("TASK-iso-b").
		SetProcessDefinitionKey("change_normal_flow").SetNodeKey("Activity_CABApproval").
		SetBusinessType("change").SetBusinessID("42").
		SetActorID(actorB.ID).SetActorName(actorB.Name).
		SetAction("approve").SetDecision("approved").SetComment("tenant b").
		SetVariablesSnapshot(map[string]interface{}{}).SetTenantID(tenantB.ID).
		Save(ctx)
	require.NoError(t, err)

	repo := newTestChangeRepository(entClient, nil)
	history, err := repo.GetApprovalHistory(ctx, 42, tenantA.ID)
	require.NoError(t, err)
	require.Len(t, history, 1)
	assert.Equal(t, actorA.ID, history[0].ApproverID)
	assert.Equal(t, "tenant a", *history[0].Comment)
}

// TestGetApprovalHistory_IncludesPendingCABTask 覆盖审批人模型收尾：ProcessApprovalDecision
// 只记录已经做出的决策，CAB 审批人还没决定之前，审批历史不该是空的——调用方（前端审批
// 详情页）需要知道"正在等谁审批"，不是"看起来没人在审批"。
func TestGetApprovalHistory_IncludesPendingCABTask(t *testing.T) {
	entClient := newChangeBPMNEntClient(t, "change_pending_history")
	tenantID, actorID := setupChangeBPMNActor(t, entClient, "pending-history")
	engine, trigger := deployRealBPMNFixture(t, entClient, tenantID)

	// change_manager 角色候选人——CAB 任务需要能解析出至少一个候选人，
	// CandidateUsers 才会是这个用户名而不是空字符串/候选组兜底。
	tenantCtx := service.WithBPMNAccessScope(context.Background(), service.BPMNAccessScope{UserID: actorID, TenantID: tenantID})
	_, err := entClient.User.Create().SetUsername("pending-cm").SetEmail("pending-cm@example.com").SetName("Pending CM").SetPasswordHash("h").SetRole("change_manager").SetActive(true).SetTenantID(tenantID).Save(context.Background())
	require.NoError(t, err)

	workItem := createChangeWorkItemFixture(t, entClient, tenantID, actorID, "测试变更")
	c, err := entClient.Change.Create().SetType("normal").SetRiskLevel("medium").SetImpactScope("low").SetTenantID(tenantID).SetCreatedBy(actorID).SetWorkItemID(workItem.ID).Save(context.Background())
	require.NoError(t, err)

	repo := newTestChangeRepository(entClient, openChangeBPMNRawDB(t, "change_pending_history"))
	svc := NewService(repo, entClient, zaptest.NewLogger(t).Sugar())
	svc.SetProcessTriggerService(trigger)
	svc.SetProcessEngine(engine)

	_, err = svc.SubmitChange(tenantCtx, c.ID, tenantID, actorID, &dto.SubmitChangeRequest{})
	require.NoError(t, err)

	history, err := svc.GetApprovalHistory(context.Background(), c.ID, tenantID)
	require.NoError(t, err)
	require.Len(t, history, 1, "没有任何真实决策，但流程正卡在 CAB 审批这一步，历史里应该有一条合成的 pending 记录")
	assert.Equal(t, "pending", history[0].Status)
	assert.Equal(t, "pending-cm", history[0].ApproverName, "ApproverName 应该是 CAB 任务解析出来的候选审批人")
	assert.Equal(t, c.ID, history[0].ChangeID)
}

// TestGetApprovalHistory_NoPendingEntryAfterDecisionMade 确认 pending 合成记录只在
// "真的没有决策、流程还卡在 CAB 审批"时出现——CAB 决定做出之后（流程已经推进过去），
// 不应该同时看到一条真实决策记录 + 一条合成的 pending 记录，那会让前端以为审批还没结束。
func TestGetApprovalHistory_NoPendingEntryAfterDecisionMade(t *testing.T) {
	entClient := newChangeBPMNEntClient(t, "change_pending_history_decided")
	tenantID, actorID := setupChangeBPMNActor(t, entClient, "pending-history-decided")
	engine, trigger := deployRealBPMNFixture(t, entClient, tenantID)

	tenantCtx := service.WithBPMNAccessScope(context.Background(), service.BPMNAccessScope{UserID: actorID, TenantID: tenantID})
	cmUser, err := entClient.User.Create().SetUsername("decided-cm").SetEmail("decided-cm@example.com").SetName("Decided CM").SetPasswordHash("h").SetRole("change_manager").SetActive(true).SetTenantID(tenantID).Save(context.Background())
	require.NoError(t, err)

	workItem := createChangeWorkItemFixture(t, entClient, tenantID, actorID, "测试变更")
	c, err := entClient.Change.Create().SetType("normal").SetRiskLevel("medium").SetImpactScope("low").SetTenantID(tenantID).SetCreatedBy(actorID).SetWorkItemID(workItem.ID).Save(context.Background())
	require.NoError(t, err)

	repo := newTestChangeRepository(entClient, openChangeBPMNRawDB(t, "change_pending_history_decided"))
	svc := NewService(repo, entClient, zaptest.NewLogger(t).Sugar())
	svc.SetProcessTriggerService(trigger)
	svc.SetProcessEngine(engine)

	_, err = svc.SubmitChange(tenantCtx, c.ID, tenantID, actorID, &dto.SubmitChangeRequest{})
	require.NoError(t, err)

	_, err = svc.TransitionStatus(tenantCtx, c.ID, tenantID, cmUser.ID, "approved", "同意")
	require.NoError(t, err)

	history, err := svc.GetApprovalHistory(context.Background(), c.ID, tenantID)
	require.NoError(t, err)
	require.Len(t, history, 1, "CAB 已经做出决定，级联完成排期节点后流程实例不再停在 Activity_CABApproval，不应该再合成 pending 记录")
	assert.Equal(t, "approved", history[0].Status)
}

// TestTransitionStatus_Cancel_TerminatesRunningProcessInstance 覆盖收尾清理：变更被取消时，
// 如果还有一个运行中的 BPMN 流程实例挂在它身上（比如卡在 CAB 审批这一步，还没人处理），
// 不清理会在工作流控制台堆积孤儿实例——包括一个理论上还能被人误操作完成的 CAB 待办任务。
func TestTransitionStatus_Cancel_TerminatesRunningProcessInstance(t *testing.T) {
	entClient := newChangeBPMNEntClient(t, "change_cancel_terminates_instance")
	tenantID, actorID := setupChangeBPMNActor(t, entClient, "cancel-terminate")
	engine, trigger := deployRealBPMNFixture(t, entClient, tenantID)

	tenantCtx := service.WithBPMNAccessScope(context.Background(), service.BPMNAccessScope{UserID: actorID, TenantID: tenantID})
	workItem := createChangeWorkItemFixture(t, entClient, tenantID, actorID, "测试变更")
	c, err := entClient.Change.Create().SetType("normal").SetRiskLevel("medium").SetImpactScope("low").SetTenantID(tenantID).SetCreatedBy(actorID).SetWorkItemID(workItem.ID).Save(context.Background())
	require.NoError(t, err)

	repo := newTestChangeRepository(entClient, openChangeBPMNRawDB(t, "change_cancel_terminates_instance"))
	svc := NewService(repo, entClient, zaptest.NewLogger(t).Sugar())
	svc.SetProcessTriggerService(trigger)
	svc.SetProcessEngine(engine)

	_, err = svc.SubmitChange(tenantCtx, c.ID, tenantID, actorID, &dto.SubmitChangeRequest{})
	require.NoError(t, err)

	instanceBefore, err := entClient.ProcessInstance.Query().
		Where(processinstance.BusinessKey(fmt.Sprintf("change:%d", workItem.ID)), processinstance.TenantID(tenantID)).
		Only(context.Background())
	require.NoError(t, err)
	require.Equal(t, "running", instanceBefore.Status, "取消之前流程实例应该还在运行中——正卡在 CAB 审批这一步没人处理")

	updated, err := svc.TransitionStatus(context.Background(), c.ID, tenantID, actorID, "cancelled", "不需要了")
	require.NoError(t, err)
	assert.Equal(t, "cancelled", updated.Status)

	instanceAfter, err := entClient.ProcessInstance.Query().
		Where(processinstance.BusinessKey(fmt.Sprintf("change:%d", workItem.ID)), processinstance.TenantID(tenantID)).
		Only(context.Background())
	require.NoError(t, err)
	assert.Equal(t, "terminated", instanceAfter.Status, "取消变更应该顺带终止还挂着的运行中流程实例，不留孤儿")
}

// TestTransitionStatus_Cancel_NoRunningInstanceIsNoop 确认没有运行中流程实例时（比如
// CAB 已经审批通过、流程早就走完了，或者 processTriggerService 压根没注入）取消操作
// 本身不受影响，不会因为找不到实例/没有触发服务而报错——cancelRunningProcessInstance
// 是收尾清理，不应该反过来挡住真正的业务侧终态转换。
func TestTransitionStatus_Cancel_NoRunningInstanceIsNoop(t *testing.T) {
	entClient := newChangeBPMNEntClient(t, "change_cancel_no_instance")
	tenantID, actorID := setupChangeBPMNActor(t, entClient, "cancel-no-instance")
	logger := zaptest.NewLogger(t).Sugar()

	workItem := createChangeWorkItemFixture(t, entClient, tenantID, actorID, "测试变更")
	c, err := entClient.Change.Create().SetType("normal").SetRiskLevel("medium").SetImpactScope("low").SetTenantID(tenantID).SetCreatedBy(actorID).SetWorkItemID(workItem.ID).Save(context.Background())
	require.NoError(t, err)

	repo := newTestChangeRepository(entClient, nil)
	svc := NewService(repo, entClient, logger)
	// 故意不调用 SetProcessTriggerService——覆盖"引擎没注入"这个分支，跟"引擎注入了
	// 但确实查不到运行中实例"是两条不同的早退路径，都不应该影响业务侧终态转换。

	updated, err := svc.TransitionStatus(context.Background(), c.ID, tenantID, actorID, "cancelled", "不需要了")
	require.NoError(t, err)
	assert.Equal(t, "cancelled", updated.Status)
}
