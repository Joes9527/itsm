package change

import (
	"context"
	"fmt"
	"testing"

	"itsm-backend/dto"

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
	return nil
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

func TestSubmitChange_TriggersBPMNProcess_Normal(t *testing.T) {
	entClient := newChangeBridgeEntClient(t, "change_submit_normal")
	tenantID, actorID := setupChangeBridgeActor(t, entClient, "submit-normal")
	repo := newMockRepository()
	svc := NewService(repo, entClient, zaptest.NewLogger(t).Sugar())
	trigger := &mockProcessTriggerService{}
	svc.SetProcessTriggerService(trigger)

	c := createTestChange(repo, tenantID, actorID)
	c.Type = "normal"

	updated, err := svc.SubmitChange(context.Background(), c.ID, tenantID, actorID, &dto.SubmitChangeRequest{ApproverIDs: []int{actorID}})
	require.NoError(t, err)
	assert.Equal(t, "pending", updated.Status)

	require.Len(t, trigger.triggerCalls, 1)
	assert.Equal(t, "change_normal_flow", trigger.triggerCalls[0].ProcessDefinitionKey)
	assert.Equal(t, dto.BusinessTypeChange, trigger.triggerCalls[0].BusinessType)
	assert.Equal(t, c.ID, trigger.triggerCalls[0].BusinessID)
	assert.Equal(t, true, trigger.triggerCalls[0].Variables["approval_required"])
}

func TestSubmitChange_TriggersBPMNProcess_Emergency(t *testing.T) {
	entClient := newChangeBridgeEntClient(t, "change_submit_emergency")
	tenantID, actorID := setupChangeBridgeActor(t, entClient, "submit-emergency")
	repo := newMockRepository()
	svc := NewService(repo, entClient, zaptest.NewLogger(t).Sugar())
	trigger := &mockProcessTriggerService{}
	svc.SetProcessTriggerService(trigger)

	c := createTestChange(repo, tenantID, actorID)
	c.Type = "emergency"

	_, err := svc.SubmitChange(context.Background(), c.ID, tenantID, actorID, &dto.SubmitChangeRequest{ApproverIDs: []int{actorID}})
	require.NoError(t, err)

	require.Len(t, trigger.triggerCalls, 1)
	assert.Equal(t, "change_emergency_flow", trigger.triggerCalls[0].ProcessDefinitionKey)
}

// TestSubmitChange_RejectsDuplicateWhenRunningInstanceExists 幂等保护回归：
// 同一个 change 已经存在一个运行中的 BPMN 流程实例（businessKey = "change:{id}"）时，
// 重复提交必须被拒绝，且不能再次触发流程。
func TestSubmitChange_RejectsDuplicateWhenRunningInstanceExists(t *testing.T) {
	entClient := newChangeBridgeEntClient(t, "change_submit_duplicate")
	tenantID, actorID := setupChangeBridgeActor(t, entClient, "submit-dup")
	repo := newMockRepository()
	svc := NewService(repo, entClient, zaptest.NewLogger(t).Sugar())
	trigger := &mockProcessTriggerService{}
	svc.SetProcessTriggerService(trigger)

	c := createTestChange(repo, tenantID, actorID)
	c.Type = "normal"
	createChangeBridgeProcessFixture(t, entClient, tenantID, "dup1", fmt.Sprintf("change:%d", c.ID), actorID)

	_, err := svc.SubmitChange(context.Background(), c.ID, tenantID, actorID, &dto.SubmitChangeRequest{ApproverIDs: []int{actorID}})
	require.Error(t, err)
	assert.Empty(t, trigger.triggerCalls)
}

// TestSubmitChange_TriggerProcessFailureLeavesChangeDraft 回归覆盖 Task 2 修复：
// TriggerProcess 失败时，change 必须原地保留在 draft（而不是被写成孤儿 pending），
// 这样才能通过 SubmitChange 的 draft 门槛重新提交。修复前的顺序是先
// MarkSubmittedForApproval 再 TriggerProcess，一旦 TriggerProcess 失败 change 就永久卡在
// pending 且没有任何修复路径。
func TestSubmitChange_TriggerProcessFailureLeavesChangeDraft(t *testing.T) {
	entClient := newChangeBridgeEntClient(t, "change_submit_trigger_fail")
	tenantID, actorID := setupChangeBridgeActor(t, entClient, "submit-trigger-fail")
	repo := newMockRepository()
	svc := NewService(repo, entClient, zaptest.NewLogger(t).Sugar())
	trigger := &mockProcessTriggerService{triggerErr: fmt.Errorf("bpmn engine unavailable")}
	svc.SetProcessTriggerService(trigger)

	c := createTestChange(repo, tenantID, actorID)
	c.Type = "normal"

	_, err := svc.SubmitChange(context.Background(), c.ID, tenantID, actorID, &dto.SubmitChangeRequest{ApproverIDs: []int{actorID}})
	require.Error(t, err)

	// MarkSubmittedForApproval must never have run: the change stays in draft in the
	// repo, so it can be resubmitted through the normal API instead of being stranded.
	stored, getErr := repo.Get(context.Background(), c.ID, tenantID)
	require.NoError(t, getErr)
	assert.Equal(t, "draft", stored.Status)

	require.Len(t, trigger.triggerCalls, 1, "TriggerProcess should have been attempted once")
}

// TestGetApprovalHistory_ReadsFromProcessApprovalDecision covers Task 5: the
// change_approvals SQL table is no longer the source of truth for approval
// history. GetApprovalHistory must read from ent.ProcessApprovalDecision
// (the BPMN engine's own approval-decision audit table), filtered by
// business_type="change" + business_id=<changeID> + tenant_id, and map it
// onto the unchanged ApprovalRecord DTO shape.
func TestGetApprovalHistory_ReadsFromProcessApprovalDecision(t *testing.T) {
	entClient := newChangeBridgeEntClient(t, "change_approval_history")
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

	repo := NewEntRepository(entClient, nil)
	history, err := repo.GetApprovalHistory(ctx, 42, tenant.ID)
	require.NoError(t, err)
	require.Len(t, history, 1)
	assert.Equal(t, actor.ID, history[0].ApproverID)
	assert.Equal(t, actor.Name, history[0].ApproverName)
	assert.Equal(t, "approved", history[0].Status)
	require.NotNil(t, history[0].Comment)
	assert.Equal(t, "looks good", *history[0].Comment)
}

// TestGetApprovalHistory_TenantIsolation guards against a filter that omits
// tenant_id: two tenants each get a ProcessApprovalDecision row with the
// same business_id (42), and a leaky query would return both.
func TestGetApprovalHistory_TenantIsolation(t *testing.T) {
	entClient := newChangeBridgeEntClient(t, "change_approval_history_tenant_iso")
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

	repo := NewEntRepository(entClient, nil)
	history, err := repo.GetApprovalHistory(ctx, 42, tenantA.ID)
	require.NoError(t, err)
	require.Len(t, history, 1)
	assert.Equal(t, actorA.ID, history[0].ApproverID)
	assert.Equal(t, "tenant a", *history[0].Comment)
}
