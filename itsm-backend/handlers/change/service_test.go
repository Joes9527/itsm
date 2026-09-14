package change

import (
	"context"
	"fmt"
	"testing"

	"itsm-backend/ent"

	"itsm-backend/service"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestSubmitChange_TriggersBPMNProcess_Normal(t *testing.T) {
	f := newGovernedChangeFixture(t, "normal")
	f.submit(t)
	require.Equal(t, "submitted", f.client.Ticket.GetX(f.ctx, f.record.WorkItemID).Status)
	instance := f.client.ProcessInstance.Query().OnlyX(f.ctx)
	require.Equal(t, "change_normal_flow", instance.ProcessDefinitionKey)
	require.Equal(t, "Activity_Assessment", instance.CurrentActivityID)
	require.Zero(t, f.client.ProcessCallbackOutbox.Query().CountX(f.ctx))
}

func TestSubmitChange_TriggersBPMNProcess_Emergency(t *testing.T) {
	f := newGovernedChangeFixture(t, "emergency")
	f.submit(t)
	require.Equal(t, "submitted", f.client.Ticket.GetX(f.ctx, f.record.WorkItemID).Status)
	instance := f.client.ProcessInstance.Query().OnlyX(f.ctx)
	require.Equal(t, "change_emergency_flow", instance.ProcessDefinitionKey)
	require.Equal(t, "Activity_Assessment", instance.CurrentActivityID)
	require.Zero(t, f.client.ProcessCallbackOutbox.Query().CountX(f.ctx))
}

func TestSubmitChange_RejectsDuplicateWhenRunningInstanceExists(t *testing.T) {
	f := newGovernedChangeFixture(t, "normal")
	f.submit(t)
	_, err := f.svc.ApplyCommand(f.ctx, f.command("submit", f.requester))
	require.Error(t, err)
	require.Equal(t, 1, f.client.ProcessInstance.Query().CountX(f.ctx))
}

func TestSubmitChange_TriggerProcessFailureLeavesChangeDraft(t *testing.T) {
	f := newGovernedChangeFixture(t, "normal")
	f.client.ProcessDefinition.Delete().ExecX(f.ctx)
	_, err := f.svc.ApplyCommand(f.ctx, f.command("submit", f.requester))
	require.Error(t, err)
	require.Equal(t, "draft", f.client.Ticket.GetX(f.ctx, f.record.WorkItemID).Status)
	require.Zero(t, f.client.ProcessInstance.Query().CountX(f.ctx))
}

func TestTransitionStatusRejectsSelfApprovalAndSelfRejection(t *testing.T) {
	f := newGovernedChangeFixture(t, "normal")
	f.submit(t)
	f.assess(t)
	for _, action := range []string{"approve", "reject"} {
		_, err := f.svc.CompleteChangeTask(f.ctx, f.taskCommand(t, action, f.requester))
		require.Error(t, err)
	}
	require.Zero(t, f.client.ProcessApprovalDecision.Query().CountX(f.ctx))
}

func TestTransitionStatusUsesRejectGuardBeforeGenericStateValidation(t *testing.T) {
	f := newGovernedChangeFixture(t, "normal")
	_, err := f.svc.CompleteChangeTask(f.ctx, TaskCommand{Command: f.command("implement", f.requester), TaskID: "missing"})
	require.Error(t, err)
	require.Equal(t, "draft", f.client.Ticket.GetX(f.ctx, f.record.WorkItemID).Status)
	require.Zero(t, f.client.ProcessCallbackOutbox.Query().CountX(f.ctx))
}

func TestSubmitChange_MarkSubmittedFailureCompensatesByCancellingProcess(t *testing.T) {
	f := newGovernedChangeFixture(t, "normal")
	f.client.Use(func(next ent.Mutator) ent.Mutator {
		return ent.MutateFunc(func(ctx context.Context, m ent.Mutation) (ent.Value, error) {
			if _, ok := m.(*ent.AuditLogMutation); ok {
				return nil, fmt.Errorf("receipt failure")
			}
			return next.Mutate(ctx, m)
		})
	})
	_, err := f.svc.ApplyCommand(f.ctx, f.command("submit", f.requester))
	require.ErrorContains(t, err, "receipt failure")
	require.Equal(t, "draft", f.client.Ticket.GetX(f.ctx, f.record.WorkItemID).Status)
	require.Zero(t, f.client.ProcessInstance.Query().CountX(f.ctx))
	require.Zero(t, f.client.ProcessTask.Query().CountX(f.ctx))
}

type cancelAlwaysFailsTriggerService struct {
	*service.ProcessTriggerService
	cancelCalls []int
	cancelErr   error
}

func (w *cancelAlwaysFailsTriggerService) CancelProcess(ctx context.Context, processInstanceID int, reason string) error {
	w.cancelCalls = append(w.cancelCalls, processInstanceID)
	return w.cancelErr
}

func TestSubmitChange_MarkSubmittedFailure_CancelProcessAlsoFails_ReturnsOriginalError(t *testing.T) {
	f := newGovernedChangeFixture(t, "normal")
	f.client.Use(func(next ent.Mutator) ent.Mutator {
		return ent.MutateFunc(func(ctx context.Context, m ent.Mutation) (ent.Value, error) {
			if _, ok := m.(*ent.AuditLogMutation); ok {
				return nil, fmt.Errorf("receipt failure")
			}
			return next.Mutate(ctx, m)
		})
	})
	_, err := f.svc.ApplyCommand(f.ctx, f.command("submit", f.requester))
	require.ErrorContains(t, err, "receipt failure")
	require.Equal(t, "draft", f.client.Ticket.GetX(f.ctx, f.record.WorkItemID).Status)
	require.Zero(t, f.client.ProcessInstance.Query().CountX(f.ctx))
	require.Zero(t, f.client.ProcessTask.Query().CountX(f.ctx))
}

func TestGetApprovalHistory_ReadsFromProcessApprovalDecision(t *testing.T) {
	entClient := newChangeBPMNEntClient(t, "change_approval_history")
	ctx := context.Background()

	tenant, err := entClient.Tenant.Create().SetName("T").SetCode("t-history").SetDomain("t-history.example.com").SetStatus("active").Save(ctx)
	require.NoError(t, err)
	actor, err := entClient.User.Create().SetUsername("cm").SetEmail("cm@example.com").SetName("CM User").SetPasswordHash("h").SetRole("agent").SetActive(true).SetTenantID(tenant.ID).Save(ctx)
	require.NoError(t, err)
	workItem := createChangeWorkItemFixture(t, entClient, tenant.ID, actor.ID, "Approval history")
	changeEntity, err := entClient.Change.Create().SetType("normal").SetRiskLevel("medium").SetImpactScope("low").SetWorkItemID(workItem.ID).Save(ctx)
	require.NoError(t, err)

	_, err = entClient.ProcessApprovalDecision.Create().
		SetProcessInstanceID(1).SetProcessTaskID(1).
		SetProcessInstanceKey("PI-test-1").SetTaskID("TASK-test-1").
		SetProcessDefinitionKey("change_normal_flow").SetNodeKey("Activity_CABApproval").
		SetBusinessType("change_request").SetBusinessID(fmt.Sprintf("%d", workItem.ID)).
		SetActorID(actor.ID).SetActorName(actor.Name).
		SetAction("approve").SetDecision("approved").SetComment("looks good").
		SetVariablesSnapshot(map[string]interface{}{}).SetTenantID(tenant.ID).
		Save(ctx)
	require.NoError(t, err)

	repo := newTestChangeRepository(entClient, nil)
	history, err := repo.GetApprovalHistory(ctx, changeEntity.ID, tenant.ID)
	require.NoError(t, err)
	require.Len(t, history, 1)
	assert.Equal(t, actor.ID, history[0].ApproverID)
	assert.Equal(t, actor.Name, history[0].ApproverName)
	assert.Equal(t, "approved", history[0].Status)
	require.NotNil(t, history[0].Comment)
	assert.Equal(t, "looks good", *history[0].Comment)
	assert.NotNil(t, history[0].ApprovedAt, "通过的决策应该有 ApprovedAt")
}

func TestGetApprovalHistoryUsesOnlyCanonicalWorkItemBusinessID(t *testing.T) {
	entClient := newChangeBPMNEntClient(t, "change_approval_history_canonical_identity")
	ctx := context.Background()
	tenantID, actorID := setupChangeBPMNActor(t, entClient, "history-canonical")
	_ = createChangeWorkItemFixture(t, entClient, tenantID, actorID, "Unrelated WorkItem")
	workItem := createChangeWorkItemFixture(t, entClient, tenantID, actorID, "Canonical identity")
	changeEntity, err := entClient.Change.Create().
		SetType("normal").
		SetRiskLevel("medium").
		SetImpactScope("low").
		SetWorkItemID(workItem.ID).
		Save(ctx)
	require.NoError(t, err)
	require.NotEqual(t, changeEntity.ID, workItem.ID, "fixture must distinguish professional and WorkItem IDs")

	createDecision := func(taskID int, businessID, comment string) {
		t.Helper()
		_, createErr := entClient.ProcessApprovalDecision.Create().
			SetProcessInstanceID(taskID).
			SetProcessTaskID(taskID).
			SetProcessInstanceKey(fmt.Sprintf("PI-%d", taskID)).
			SetTaskID(fmt.Sprintf("TASK-%d", taskID)).
			SetProcessDefinitionKey("change_normal_flow").
			SetNodeKey("Activity_CABApproval").
			SetBusinessType("change_request").
			SetBusinessID(businessID).
			SetActorID(actorID).
			SetAction("approve").
			SetDecision("approved").
			SetComment(comment).
			SetTenantID(tenantID).
			Save(ctx)
		require.NoError(t, createErr)
	}
	createDecision(501, fmt.Sprintf("%d", changeEntity.ID), "obsolete professional id")
	createDecision(502, fmt.Sprintf("%d", workItem.ID), "canonical work item id")

	repo := newTestChangeRepository(entClient, nil)
	history, err := repo.GetApprovalHistory(ctx, changeEntity.ID, tenantID)
	require.NoError(t, err)
	require.Len(t, history, 1)
	require.NotNil(t, history[0].Comment)
	assert.Equal(t, "canonical work item id", *history[0].Comment)
}

func TestGetApprovalHistory_RejectedRecordHasNoApprovedAt(t *testing.T) {
	entClient := newChangeBPMNEntClient(t, "change_approval_history_rejected")
	ctx := context.Background()

	tenant, err := entClient.Tenant.Create().SetName("T").SetCode("t-history-rejected").SetDomain("t-history-rejected.example.com").SetStatus("active").Save(ctx)
	require.NoError(t, err)
	actor, err := entClient.User.Create().SetUsername("cm-r").SetEmail("cm-r@example.com").SetName("CM Rejecter").SetPasswordHash("h").SetRole("agent").SetActive(true).SetTenantID(tenant.ID).Save(ctx)
	require.NoError(t, err)
	workItem := createChangeWorkItemFixture(t, entClient, tenant.ID, actor.ID, "Rejected approval history")
	changeEntity, err := entClient.Change.Create().SetType("normal").SetRiskLevel("medium").SetImpactScope("low").SetWorkItemID(workItem.ID).Save(ctx)
	require.NoError(t, err)

	_, err = entClient.ProcessApprovalDecision.Create().
		SetProcessInstanceID(1).SetProcessTaskID(1).
		SetProcessInstanceKey("PI-test-rejected").SetTaskID("TASK-test-rejected").
		SetProcessDefinitionKey("change_normal_flow").SetNodeKey("Activity_CABApproval").
		SetBusinessType("change_request").SetBusinessID(fmt.Sprintf("%d", workItem.ID)).
		SetActorID(actor.ID).SetActorName(actor.Name).
		SetAction("reject").SetDecision("rejected").SetComment("风险太高").
		SetVariablesSnapshot(map[string]interface{}{}).SetTenantID(tenant.ID).
		Save(ctx)
	require.NoError(t, err)

	repo := newTestChangeRepository(entClient, nil)
	history, err := repo.GetApprovalHistory(ctx, changeEntity.ID, tenant.ID)
	require.NoError(t, err)
	require.Len(t, history, 1)
	assert.Equal(t, "rejected", history[0].Status)
	assert.Nil(t, history[0].ApprovedAt, "驳回记录不应该有 ApprovedAt")
}

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
	workItemA := createChangeWorkItemFixture(t, entClient, tenantA.ID, actorA.ID, "Tenant A approval history")
	changeA, err := entClient.Change.Create().SetType("normal").SetRiskLevel("medium").SetImpactScope("low").SetWorkItemID(workItemA.ID).Save(ctx)
	require.NoError(t, err)
	_, err = entClient.ProcessApprovalDecision.Create().
		SetProcessInstanceID(1).SetProcessTaskID(1).
		SetProcessInstanceKey("PI-iso-a").SetTaskID("TASK-iso-a").
		SetProcessDefinitionKey("change_normal_flow").SetNodeKey("Activity_CABApproval").
		SetBusinessType("change_request").SetBusinessID(fmt.Sprintf("%d", workItemA.ID)).
		SetActorID(actorA.ID).SetActorName(actorA.Name).
		SetAction("approve").SetDecision("approved").SetComment("tenant a").
		SetVariablesSnapshot(map[string]interface{}{}).SetTenantID(tenantA.ID).
		Save(ctx)
	require.NoError(t, err)
	_, err = entClient.ProcessApprovalDecision.Create().
		SetProcessInstanceID(2).SetProcessTaskID(2).
		SetProcessInstanceKey("PI-iso-b").SetTaskID("TASK-iso-b").
		SetProcessDefinitionKey("change_normal_flow").SetNodeKey("Activity_CABApproval").
		SetBusinessType("change_request").SetBusinessID(fmt.Sprintf("%d", workItemA.ID)).
		SetActorID(actorB.ID).SetActorName(actorB.Name).
		SetAction("approve").SetDecision("approved").SetComment("tenant b").
		SetVariablesSnapshot(map[string]interface{}{}).SetTenantID(tenantB.ID).
		Save(ctx)
	require.NoError(t, err)

	repo := newTestChangeRepository(entClient, nil)
	history, err := repo.GetApprovalHistory(ctx, changeA.ID, tenantA.ID)
	require.NoError(t, err)
	require.Len(t, history, 1)
	assert.Equal(t, actorA.ID, history[0].ApproverID)
	assert.Equal(t, "tenant a", *history[0].Comment)
}

func TestGetApprovalHistory_IncludesPendingCABTask(t *testing.T) {
	f := newGovernedChangeFixture(t, "normal")
	f.submit(t)
	f.assess(t)
	records, err := f.svc.GetApprovalHistory(f.ctx, f.record.ID, f.tenant)
	require.NoError(t, err)
	require.Len(t, records, 1)
	require.Equal(t, "pending", records[0].Status)
}

func TestGetApprovalHistory_NoPendingEntryAfterDecisionMade(t *testing.T) {
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
	records, err := f.svc.GetApprovalHistory(f.ctx, f.record.ID, f.tenant)
	require.NoError(t, err)
	require.Len(t, records, 1)
	require.Equal(t, "approved", records[0].Status)
}

func TestTransitionStatus_Cancel_TerminatesRunningProcessInstance(t *testing.T) {
	f := newGovernedChangeFixture(t, "normal")
	f.submit(t)
	result, err := f.svc.ApplyCommand(f.ctx, f.command("cancel", f.requester))
	require.NoError(t, err)
	require.Equal(t, "cancelled", result.Status)
	require.Equal(t, "terminated", f.client.ProcessInstance.Query().OnlyX(f.ctx).Status)
	require.Equal(t, "cancelled", f.client.ProcessTask.Query().OnlyX(f.ctx).Status)
}

func TestTransitionStatus_Cancel_NoRunningInstanceIsNoop(t *testing.T) {
	f := newGovernedChangeFixture(t, "normal")
	result, err := f.svc.ApplyCommand(f.ctx, f.command("cancel", f.requester))
	require.NoError(t, err)
	require.Equal(t, "cancelled", result.Status)
	require.Zero(t, f.client.ProcessInstance.Query().CountX(f.ctx))
}
