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
