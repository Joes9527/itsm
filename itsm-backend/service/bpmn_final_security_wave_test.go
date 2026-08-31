package service

import (
	"context"
	"testing"

	"itsm-backend/common"
	"itsm-backend/ent/processcallbackoutbox"
	"itsm-backend/ent/processtask"
	"itsm-backend/service/bpmn"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"go.uber.org/zap"
)

func TestStartProcessRejectsMissingTypedOrTrustedTenantScope(t *testing.T) {
	f := newBPMNAuthorizationFixture(t)

	_, err := f.engine.StartProcess(
		context.Background(),
		f.definition.Key,
		"unscoped-start",
		"ticket",
		101,
		map[string]interface{}{},
	)

	require.Error(t, err)
	assert.Zero(t, f.client.ProcessInstance.Query().CountX(f.userCtx))
}

func TestProcessDefinitionServiceRequiresTypedTenantScopeAndIgnoresRequestedTenant(t *testing.T) {
	f := newBPMNAuthorizationFixture(t)
	definitions := f.engine.ProcessDefinitionService()

	_, err := definitions.GetProcessDefinition(context.Background(), f.definition.Key, f.definition.Version)
	require.Error(t, err)

	request := &CreateProcessDefinitionRequest{
		Key:      "scope_authoritative_definition",
		Name:     "Scope authoritative definition",
		BPMNXML:  string(f.definition.BpmnXML),
		TenantID: f.otherTenant.ID,
	}
	_, err = definitions.CreateProcessDefinition(context.Background(), request)
	require.Error(t, err)

	created, err := definitions.CreateProcessDefinition(f.scopedCtx(true, true, true, true), request)
	require.NoError(t, err)
	assert.Equal(t, f.tenant.ID, created.TenantID)

	rows, total, err := definitions.ListProcessDefinitions(
		f.scopedCtx(true, true, true, true),
		&ListProcessDefinitionsRequest{TenantID: f.otherTenant.ID},
	)
	require.NoError(t, err)
	assert.Equal(t, 2, total)
	for _, definition := range rows {
		assert.Equal(t, f.tenant.ID, definition.TenantID)
	}
}

func TestBPMNVersionServiceRequiresAuthoritativeTenantContext(t *testing.T) {
	f := newBPMNAuthorizationFixture(t)
	versions := NewBPMNVersionService(f.client, zap.NewNop().Sugar())
	request := &CreateVersionRequest{
		ProcessDefinitionKey: "scoped_version_definition",
		Name:                 "Scoped version definition",
		BPMNXML:              string(f.definition.BpmnXML),
		ChangeLog:            "Create scoped version definition",
		TenantID:             f.tenant.ID,
		CreatedBy:            "1",
	}

	_, err := versions.CreateVersion(context.Background(), request)
	require.Error(t, err)

	request.TenantID = f.otherTenant.ID
	_, err = versions.CreateVersion(f.scopedCtx(true, true, true, true), request)
	require.Error(t, err)

	_, err = versions.GetChangeLogsByProcessDefinitionID(context.Background(), f.definition.ID)
	require.Error(t, err)
}

func TestCompleteTaskRejectsActorlessOrdinaryMutation(t *testing.T) {
	f := newBPMNAuthorizationFixture(t)
	task := f.seedNonParticipantApprovalTask(t, "actorless-complete")
	ctx := context.WithValue(context.Background(), bpmn.BPMNTenantIDContextKey, f.tenant.ID)

	err := f.engine.CompleteTask(ctx, task.TaskID, map[string]interface{}{"approved": true})

	require.Error(t, err)
	assert.Equal(t, common.ProcessTaskStatusCreated, f.client.ProcessTask.GetX(f.userCtx, task.ID).Status)
}

func TestSetTaskVariablesRejectsReservedCallbackAndIdentityKeysAtomically(t *testing.T) {
	f := newBPMNAuthorizationFixture(t)
	task := f.seedNonParticipantApprovalTask(t, "reserved-variables")
	task = f.client.ProcessTask.UpdateOne(task).
		SetCandidateUsers(f.actor.Email).
		SetTaskVariables(map[string]interface{}{"existing_form_value": "kept"}).
		SaveX(f.userCtx)

	err := f.engine.TaskService().SetTaskVariables(
		f.typedTaskScopeOnlyCtx(f.actor, false),
		task.TaskID,
		map[string]interface{}{
			"safe_form_value":   "accepted only without reserved keys",
			"service_task_type": "webhook",
			"action":            "schedule_change",
			"tenant_id":         f.otherTenant.ID,
			"business_id":       999999,
		},
	)

	require.Error(t, err)
	assert.Equal(t, map[string]interface{}{"existing_form_value": "kept"}, f.client.ProcessTask.GetX(f.userCtx, task.ID).TaskVariables)
}

func TestCompleteTaskMergesParticipantValuesWithoutErasingTaskSummary(t *testing.T) {
	f := newBPMNAuthorizationFixture(t)
	task := f.seedNonParticipantApprovalTask(t, "merge-task-variables")
	task = f.client.ProcessTask.UpdateOne(task).
		SetCandidateUsers(f.actor.Email).
		SetTaskVariables(map[string]interface{}{
			"approval_type": "parallel",
			"threshold":     2,
			"approved":      1,
		}).
		SaveX(f.userCtx)

	require.NoError(t, f.engine.CompleteTask(
		f.typedTaskScopeOnlyCtx(f.actor, false),
		task.TaskID,
		map[string]interface{}{"approvalComment": "looks good"},
	))

	updated := f.client.ProcessTask.GetX(f.userCtx, task.ID)
	assert.Equal(t, "parallel", updated.TaskVariables["approval_type"])
	assert.EqualValues(t, 2, updated.TaskVariables["threshold"])
	assert.EqualValues(t, 1, updated.TaskVariables["approved"])
	assert.Equal(t, "looks good", updated.TaskVariables["approvalComment"])
}

func TestCallbackOutboxDoesNotPersistArbitraryOrSensitiveProcessVariables(t *testing.T) {
	f := newBPMNAuthorizationFixture(t)
	handler := newCountingIdempotentCallbackHandler("allowlist_probe", "allowlist_probe_handler", 1)
	f.engine.CallbackRegistry().RegisterHandler(handler)
	configureStartProcessDefinition(t, f, startProcessServiceTaskXML(handler.GetTaskType()))

	instance, err := f.engine.StartProcess(
		startProcessContext(f),
		f.definition.Key,
		"allowlist-probe",
		"ticket",
		321,
		map[string]interface{}{
			"safe_form_value": "not declared by the handler",
			"password":        "must-not-persist",
			"webhook_url":     "https://secret.example.invalid/hook",
			"headers":         map[string]interface{}{"Authorization": "Bearer secret"},
			"payload":         map[string]interface{}{"token": "secret"},
			"tenant_id":       f.otherTenant.ID,
			"business_id":     999999,
		},
	)
	require.NoError(t, err)

	row := f.client.ProcessCallbackOutbox.Query().Where(
		processcallbackoutbox.ProcessInstanceID(instance.ID),
	).OnlyX(f.userCtx)
	assert.Empty(t, row.Variables)
}

func TestBuiltInCallbackPayloadPoliciesRejectArbitraryFormObjects(t *testing.T) {
	f := newBPMNAuthorizationFixture(t)

	ticketPayload, err := filterBPMNCallbackPayload(
		f.engine.findHandlerByTaskType("ticket_task"),
		"update_status",
		map[string]interface{}{
			"new_status": "in_progress",
			"form_fields": map[string]interface{}{
				"password": "must-not-persist",
			},
		},
	)
	require.NoError(t, err)
	assert.Equal(t, map[string]interface{}{"new_status": "in_progress"}, ticketPayload)

	requestPayload, err := filterBPMNCallbackPayload(
		f.engine.findHandlerByTaskType("service_request_task"),
		"update_request",
		map[string]interface{}{
			"cost_center": "CC-100",
			"form_data": map[string]interface{}{
				"api_key": "must-not-persist",
			},
		},
	)
	require.NoError(t, err)
	assert.Equal(t, map[string]interface{}{"cost_center": "CC-100"}, requestPayload)
}

func TestCounterSignStatusDoesNotCountCancelledChildrenAsPending(t *testing.T) {
	f := newBPMNAuthorizationFixture(t)
	parent := f.seedNonParticipantApprovalTask(t, "cancelled-counter-sign-child")
	parent = f.client.ProcessTask.UpdateOne(parent).
		SetTaskVariables(map[string]interface{}{"threshold": 1}).
		SaveX(f.userCtx)

	f.client.ProcessTask.Create().
		SetTaskID(parent.TaskID + "-approved").
		SetProcessInstanceID(parent.ProcessInstanceID).
		SetProcessDefinitionKey(parent.ProcessDefinitionKey).
		SetTaskDefinitionKey(parent.TaskDefinitionKey + "_counter").
		SetTaskName("Approved child").
		SetParentTaskID(parent.TaskID).
		SetStatus(common.ProcessTaskStatusCompleted).
		SetTaskVariables(map[string]interface{}{"approved": true}).
		SetTenantID(parent.TenantID).
		SaveX(f.userCtx)
	f.client.ProcessTask.Create().
		SetTaskID(parent.TaskID + "-cancelled").
		SetProcessInstanceID(parent.ProcessInstanceID).
		SetProcessDefinitionKey(parent.ProcessDefinitionKey).
		SetTaskDefinitionKey(parent.TaskDefinitionKey + "_counter").
		SetTaskName("Cancelled child").
		SetParentTaskID(parent.TaskID).
		SetStatus(common.ProcessTaskStatusCancelled).
		SetTenantID(parent.TenantID).
		SaveX(f.userCtx)

	status, err := getCounterSignStatus(f.userCtx, f.client, parent)
	require.NoError(t, err)
	assert.Equal(t, 0, status.Pending)
	assert.Equal(t, 1, status.Completed)
	assert.Equal(t, "approved", status.Status)
	assert.Equal(t, 2, f.client.ProcessTask.Query().Where(processtask.ParentTaskID(parent.TaskID)).CountX(f.userCtx))
}
