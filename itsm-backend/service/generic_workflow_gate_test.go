package service

import (
	"context"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
	"itsm-backend/authorization"
	"itsm-backend/dto"
	"itsm-backend/ent"
	"itsm-backend/handlers/shared/workitemmutation"
	ticketrepo "itsm-backend/repository/ticket"
	executionfixture "itsm-backend/tests/fixtures/execution"
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

// A later stage must not erase evidence an earlier stage established. The contract
// lets several tasks declare the same prerequisite, and each queries receipts from
// its own start time, so the narrower window of a later task cannot clear a fact
// that an earlier task already proved.
func TestGenericWorkflowFactsAccumulateAcrossStages(t *testing.T) {
	f, item, task := seedGenericGate(t, "resolved", "resolved")
	item = f.client.Ticket.UpdateOne(item).SetResolution("verified repair").SaveX(f.userCtx)
	instance := f.client.ProcessInstance.GetX(f.userCtx, task.ProcessInstanceID)

	prerequisite := lifecycleMetadata("workItemPrerequisite", "resolved")
	definition := `<definitions><process id="contract" isExecutable="true"><extensionElements>` +
		lifecycleMetadata("workItemLifecycleContract", "generic_fulfillment_v1") +
		`</extensionElements><startEvent id="start"/>` +
		`<userTask id="first" ` + lifecycleOwnerAttrs + `><extensionElements>` + prerequisite + `</extensionElements></userTask>` +
		`<userTask id="second" ` + lifecycleOwnerAttrs + `><extensionElements>` + prerequisite + `</extensionElements></userTask>` +
		`<endEvent id="end"/><sequenceFlow id="a" sourceRef="start" targetRef="first"/>` +
		`<sequenceFlow id="b" sourceRef="first" targetRef="second"/>` +
		`<sequenceFlow id="c" sourceRef="second" targetRef="end"/></process></definitions>`
	f.client.ProcessDefinition.UpdateOneID(instance.ProcessDefinitionID).SetBpmnXML([]byte(definition)).SaveX(f.userCtx)

	// The first stage opens before the resolution receipt, the second after it.
	first := time.Now().Add(-3 * time.Hour)
	second := time.Now().Add(-2 * time.Hour)
	f.client.ProcessInstance.UpdateOne(instance).SetStartTime(first).SaveX(f.userCtx)
	f.client.ProcessTask.UpdateOneID(task.ID).SetTaskDefinitionKey("first").SetStatus("completed").
		SetCreatedTime(first).SetCreatedAt(first).SaveX(f.userCtx)
	f.client.ProcessTask.Create().SetTaskID("task-second-stage").SetProcessInstanceID(instance.ID).
		SetProcessDefinitionKey(instance.ProcessDefinitionKey).SetTaskDefinitionKey("second").
		SetTaskName("Second").SetStatus("completed").SetTenantID(f.tenant.ID).
		SetCreatedTime(second).SetCreatedAt(second).SaveX(f.userCtx)
	f.client.AuditLog.Create().SetTenantID(item.TenantID).SetUserID(f.actor.ID).SetResource("work_item").
		SetPath(strconv.Itoa(item.ID)).SetAction("work_item.edit").SetMethod("ui").SetStatusCode(200).
		SetOperationID("resolution-operation").SetRequestDigest(strings.Repeat("a", 64)).SetResultVersion(2).
		SetResultStatus("resolved").SetRequestBody(`{"previousStatus":"in_progress"}`).
		SetCreatedAt(first.Add(30 * time.Minute)).SaveX(f.userCtx)

	gate, err := loadGenericWorkflowGate(f.userCtx, f.client, item.TenantID, item.ID, false)
	require.NoError(t, err)
	require.True(t, gate.facts.Resolved, "a later stage must not clear the receipt an earlier stage established")
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

func TestGenericWorkflowPendingFrozenIntakeDoesNotPermitBypass(t *testing.T) {
	f, item, task := seedGenericGate(t, "assigned", "open")
	instance := f.client.ProcessInstance.GetX(f.userCtx, task.ProcessInstanceID)
	def := f.client.ProcessDefinition.GetX(f.userCtx, instance.ProcessDefinitionID)
	f.client.ProcessInstance.UpdateOne(instance).SetBusinessID(0).SaveX(f.userCtx)
	receipt := f.client.IntakeRequest.Create().SetTenantID(item.TenantID).SetActorID(f.actor.ID).SetActorTenantID(f.actor.TenantID).SetRequesterID(f.actor.ID).SetChannel("test").SetOperation("create").SetIdempotencyKey("pending-gate").SetRequestDigest("digest").SetDigestVersion("v1").SetStatus("completed").SetWorkItemID(item.ID).SaveX(f.userCtx)
	f.client.IntakeResolutionSnapshot.Create().SetTenantID(item.TenantID).SetIntakeRequestID(receipt.ID).SetWorkItemID(item.ID).SetChannel("test").SetSourceProvider("test").SetRecordClass("generic").SetWorkflowDefinitionID(def.ID).SetWorkflowDefinitionKey(def.Key).SetWorkflowDefinitionVersion(def.Version).SetWorkflowDefinitionDigest(FreezeProcessDefinition(def).Digest).SetResolverVersion("v1").SetRequestDigest("digest").SaveX(f.userCtx)
	tx, err := f.client.Tx(f.userCtx)
	require.NoError(t, err)
	require.ErrorContains(t, RejectGenericWorkflowLegacyMutationTx(f.userCtx, tx.Client(), item.TenantID, item.ID), "versioned")
	require.Error(t, EnforceGenericWorkflowTransitionTx(f.userCtx, tx.Client(), item.TenantID, item.ID, "in_progress"))
	require.NoError(t, tx.Rollback())
	f.client.ProcessDefinition.UpdateOne(def).SetBpmnXML(append(def.BpmnXML, []byte("\n<!-- drift -->")...)).SaveX(f.userCtx)
	_, err = loadGenericWorkflowGate(f.userCtx, f.client, item.TenantID, item.ID, false)
	require.ErrorContains(t, err, "conflicts")
}

func TestGenericWorkflowVersionedEditRejectsStageBypass(t *testing.T) {
	f, item, _ := seedGenericGate(t, "assigned", "open")
	grantAssignmentBoundaryRole(t, f.client, item.TenantID, f.actor.Role)
	svc := NewTicketService(&TicketServiceConfig{Client: f.client, Repository: ticketrepo.NewEntRepository(f.client, f.engine.logger), Logger: f.engine.logger, Execution: executionfixture.Standard(), SessionReader: authorization.NewSessionReader(f.client, callbackFixtureDirectory{})})
	for _, status := range []string{"in_progress", "resolved", "closed"} {
		_, err := svc.UpdateTicket(f.userCtx, dto.TicketEditCommand{WorkItemID: item.ID, Fields: dto.TicketEditFields{Status: status, Resolution: "cannot bypass"}, Meta: workitemmutation.Meta{TenantID: item.TenantID, ActorID: f.actor.ID, ExpectedVersion: item.Version, OperationID: "bypass-" + status, Source: "test"}})
		require.Error(t, err)
		require.Equal(t, item.Version, f.client.Ticket.GetX(f.userCtx, item.ID).Version)
	}
}

// A gate that rejected every transition would also satisfy the bypass test above, so the
// admission direction needs evidence of its own. It exercises the gate directly because
// this fixture denies every status transition downstream on execution scope regardless of
// the lifecycle contract（pending and cancelled behave identically on the unwired
// baseline），which would mask a passing admission.
func TestGenericWorkflowVersionedEditAdmitsStagedTransition(t *testing.T) {
	f, item, _ := seedGenericGate(t, "in_progress", "open")
	tx, err := f.client.Tx(f.userCtx)
	require.NoError(t, err)
	defer func() { _ = tx.Rollback() }()
	require.NoError(t, EnforceGenericWorkflowTransitionTx(f.userCtx, tx.Client(), item.TenantID, item.ID, "in_progress"))
	require.Error(t, EnforceGenericWorkflowTransitionTx(f.userCtx, tx.Client(), item.TenantID, item.ID, "resolved"))
}

func TestGenericWorkflowVersionedAssignmentRequiresAndAuditsReason(t *testing.T) {
	f, item, _ := seedGenericGate(t, "assigned", "open")
	grantAssignmentBoundaryRole(t, f.client, item.TenantID, f.actor.Role)
	svc := NewTicketService(&TicketServiceConfig{Client: f.client, Repository: ticketrepo.NewEntRepository(f.client, f.engine.logger), Logger: f.engine.logger, Execution: executionfixture.Standard(), SessionReader: authorization.NewSessionReader(f.client, callbackFixtureDirectory{})})
	cmd := dto.TicketEditCommand{WorkItemID: item.ID, Fields: dto.TicketEditFields{AssigneeID: &f.outsider.ID}, Meta: workitemmutation.Meta{TenantID: item.TenantID, ActorID: f.actor.ID, ExpectedVersion: item.Version, OperationID: "assignment", Source: "test"}}
	_, err := svc.UpdateTicket(f.userCtx, cmd)
	require.ErrorContains(t, err, "assignmentReason")
	require.Equal(t, item.Version, f.client.Ticket.GetX(f.userCtx, item.ID).Version)
	cmd.Fields.AssignmentReason = "  handover to support  "
	result, err := svc.UpdateTicket(f.userCtx, cmd)
	require.NoError(t, err)
	require.Equal(t, "open", result.Status)
	saved := f.client.Ticket.GetX(f.userCtx, item.ID)
	require.Equal(t, f.outsider.ID, saved.AssigneeID)
	require.Equal(t, "open", saved.Status)
	audit := f.client.TicketWorkflowRecord.Query().OnlyX(f.userCtx)
	require.Equal(t, "handover to support", audit.Reason)
}
