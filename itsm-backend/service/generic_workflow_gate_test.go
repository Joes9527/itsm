package service

import (
	"context"
	"github.com/stretchr/testify/require"
	"itsm-backend/ent"
	"strconv"
	"strings"
	"testing"
	"time"
)

func TestGenericWorkflowPrerequisite(t *testing.T) {
	for _, tc := range []struct {
		name         string
		prerequisite WorkItemPrerequisite
		facts        genericWorkflowFacts
		note         string
		valid        bool
	}{
		{"assigned", WorkItemPrerequisiteAssigned, genericWorkflowFacts{OwnerID: 1}, "", true},
		{"no owner", WorkItemPrerequisiteAssigned, genericWorkflowFacts{}, "", false},
		{"handle", WorkItemPrerequisiteInProgress, genericWorkflowFacts{Status: "in_progress"}, " evidence ", true},
		{"handle no evidence", WorkItemPrerequisiteInProgress, genericWorkflowFacts{Status: "in_progress"}, " ", false},
		{"handle excessive evidence", WorkItemPrerequisiteInProgress, genericWorkflowFacts{Status: "in_progress"}, strings.Repeat("x", 4001), false},
		{"handle wrong status", WorkItemPrerequisiteInProgress, genericWorkflowFacts{Status: "open"}, "evidence", false},
		{"escalation receipt", WorkItemPrerequisiteEscalated, genericWorkflowFacts{Escalated: true}, "", true},
		{"escalation state insufficient", WorkItemPrerequisiteEscalated, genericWorkflowFacts{Status: "in_progress"}, "", false},
		{"resolved", WorkItemPrerequisiteResolved, genericWorkflowFacts{Status: "resolved", Resolution: "fixed", Resolved: true}, "", true},
		{"resolved no receipt", WorkItemPrerequisiteResolved, genericWorkflowFacts{Status: "resolved", Resolution: "fixed"}, "", false},
		{"resolved no resolution", WorkItemPrerequisiteResolved, genericWorkflowFacts{Status: "resolved", Resolved: true}, "", false},
		{"closed", WorkItemPrerequisiteClosed, genericWorkflowFacts{Status: "closed", Closed: true}, "", true},
		{"closed state insufficient", WorkItemPrerequisiteClosed, genericWorkflowFacts{Status: "closed"}, "", false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			err := evaluateGenericWorkflowPrerequisite(tc.prerequisite, tc.facts, tc.note, true)
			if tc.valid {
				require.NoError(t, err)
			} else {
				require.Error(t, err)
			}
		})
	}
}
func TestGenericWorkflowTransitions(t *testing.T) {
	base := genericWorkflowFacts{Running: true, Waiting: WorkItemPrerequisiteInProgress}
	require.NoError(t, evaluateGenericWorkflowTransition(base, GenericFulfillmentConfig{}, "in_progress"))
	require.Error(t, evaluateGenericWorkflowTransition(base, GenericFulfillmentConfig{ApprovalRequired: true}, "in_progress"))
	base.Approved = true
	require.NoError(t, evaluateGenericWorkflowTransition(base, GenericFulfillmentConfig{ApprovalRequired: true}, "in_progress"))
	base.Waiting = WorkItemPrerequisiteResolved
	require.Error(t, evaluateGenericWorkflowTransition(base, GenericFulfillmentConfig{}, "resolved"))
	base.Handled = true
	require.NoError(t, evaluateGenericWorkflowTransition(base, GenericFulfillmentConfig{}, "resolved"))
	require.Error(t, evaluateGenericWorkflowTransition(base, GenericFulfillmentConfig{NeedEscalate: true}, "resolved"))
	base.Escalated = true
	require.NoError(t, evaluateGenericWorkflowTransition(base, GenericFulfillmentConfig{NeedEscalate: true}, "resolved"))
	base.Waiting = WorkItemPrerequisiteClosed
	require.Error(t, evaluateGenericWorkflowTransition(base, GenericFulfillmentConfig{}, "closed"))
	base.ResolveConfirmed = true
	require.NoError(t, evaluateGenericWorkflowTransition(base, GenericFulfillmentConfig{}, "closed"))
	base.Running = false
	for _, status := range []string{"in_progress", "resolved", "closed"} {
		require.Error(t, evaluateGenericWorkflowTransition(base, GenericFulfillmentConfig{}, status))
	}
}

func seedGenericGate(t *testing.T, pre, status string) (*bpmnAuthorizationFixture, *ent.Ticket, *ent.ProcessTask) {
	t.Helper()
	f := newBPMNAuthorizationFixture(t)
	task := f.seedNonParticipantApprovalTask(t, "generic-gate")
	instance := f.client.ProcessInstance.GetX(f.userCtx, task.ProcessInstanceID)
	item := f.client.Ticket.Create().SetTenantID(f.tenant.ID).SetTicketNumber("gate-ticket").SetTitle("gate").SetRequesterID(f.actor.ID).SetAssigneeID(f.actor.ID).SetRecordClass("generic").SetStatus(status).SetVersion(2).SaveX(f.userCtx)
	f.client.ProcessInstance.UpdateOne(instance).SetBusinessID(item.ID).SetBusinessType("generic").SetStatus("running").SetStartTime(time.Now().Add(-time.Hour)).SaveX(f.userCtx)
	xml := lifecycleXML(lifecycleMetadata("workItemLifecycleContract", "generic_fulfillment_v1"), lifecycleMetadata("workItemPrerequisite", pre), lifecycleOwnerAttrs, "")
	f.client.ProcessDefinition.UpdateOneID(instance.ProcessDefinitionID).SetBpmnXML(xml).SaveX(f.userCtx)
	task = f.client.ProcessTask.UpdateOne(task).SetTaskDefinitionKey("work").SaveX(f.userCtx)
	return f, item, task
}
func TestGenericWorkflowReceiptRequiresCurrentStageSuccessfulTransition(t *testing.T) {
	f, item, task := seedGenericGate(t, "resolved", "resolved")
	item = f.client.Ticket.UpdateOne(item).SetResolution("verified repair").SaveX(f.userCtx)
	gate, err := loadGenericWorkflowGate(f.userCtx, f.client, item.TenantID, item.ID, false)
	require.NoError(t, err)
	require.False(t, gate.facts.Resolved)
	base := f.client.AuditLog.Create().SetTenantID(item.TenantID).SetUserID(f.actor.ID).SetResource("work_item").SetPath(strconv.Itoa(item.ID)).SetAction("work_item.edit").SetMethod("ui").SetStatusCode(200).SetOperationID("resolution-operation").SetRequestDigest(strings.Repeat("a", 64)).SetResultVersion(2).SetResultStatus("resolved").SetRequestBody(`{"previousStatus":"in_progress"}`).SetCreatedAt(task.CreatedTime.Add(-time.Hour)).SaveX(f.userCtx)
	gate, err = loadGenericWorkflowGate(f.userCtx, f.client, item.TenantID, item.ID, false)
	require.NoError(t, err)
	require.False(t, gate.facts.Resolved, "earlier stage receipt must not authorize completion")
	f.client.AuditLog.UpdateOne(base).SetCreatedAt(time.Now()).SetRequestBody(`{"previousStatus":"resolved"}`).SaveX(f.userCtx)
	gate, err = loadGenericWorkflowGate(f.userCtx, f.client, item.TenantID, item.ID, false)
	require.NoError(t, err)
	require.False(t, gate.facts.Resolved, "unchanged status is not resolution evidence")
	f.client.AuditLog.UpdateOne(base).SetRequestBody(`{"previousStatus":"in_progress"}`).SaveX(f.userCtx)
	gate, err = loadGenericWorkflowGate(f.userCtx, f.client, item.TenantID, item.ID, false)
	require.NoError(t, err)
	require.True(t, gate.facts.Resolved)
	otherCtx := context.Background()
	_, err = loadGenericWorkflowGate(otherCtx, f.client, item.TenantID+1, item.ID, false)
	require.Error(t, err)
}
func TestGenericWorkflowReadOnlyGateRequiresProspectiveNote(t *testing.T) {
	f, _, task := seedGenericGate(t, "in_progress", "in_progress")
	before := f.client.ProcessTask.GetX(f.userCtx, task.ID)
	reason, noteRequired, err := GenericWorkflowTaskGate(f.userCtx, f.client, task)
	require.NoError(t, err)
	require.Empty(t, reason)
	require.True(t, noteRequired)
	after := f.client.ProcessTask.GetX(f.userCtx, task.ID)
	require.Equal(t, before.AggregationVersion, after.AggregationVersion)
	require.Equal(t, before.TaskVariables, after.TaskVariables)
}
func TestGenericWorkflowLegacyGuardIncludesStoppedHistory(t *testing.T) {
	f, item, task := seedGenericGate(t, "assigned", "open")
	f.client.ProcessInstance.UpdateOneID(task.ProcessInstanceID).SetStatus("terminated").SaveX(f.userCtx)
	tx, err := f.client.Tx(f.userCtx)
	require.NoError(t, err)
	defer tx.Rollback()
	require.ErrorContains(t, RejectGenericWorkflowLegacyMutationTx(f.userCtx, tx.Client(), item.TenantID, item.ID), "versioned")
	require.Error(t, EnforceGenericWorkflowTransitionTx(f.userCtx, tx.Client(), item.TenantID, item.ID, "in_progress"))
}
