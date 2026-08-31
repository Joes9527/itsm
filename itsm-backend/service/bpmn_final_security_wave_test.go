package service

import (
	"context"
	"strconv"
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

func TestLegacyCallbackDescriptorCompletionRejectsInvalidDefinitionsAtomically(t *testing.T) {
	tests := []struct {
		name       string
		taskKey    string
		bpmnXML    []byte
		wantErr    string
		wantStored string
	}{
		{
			name:    "task node missing from deployed definition",
			taskKey: "removed_legacy_task",
			bpmnXML: []byte(`<?xml version="1.0" encoding="UTF-8"?>
<bpmn:definitions xmlns:bpmn="http://www.omg.org/spec/BPMN/20100524/MODEL" targetNamespace="http://bpmn.io/schema/bpmn">
  <bpmn:process id="missing-legacy-task" isExecutable="true">
    <bpmn:startEvent id="start" />
    <bpmn:userTask id="unrelated_approval" name="Unrelated approval" />
    <bpmn:endEvent id="end" />
    <bpmn:sequenceFlow id="to-unrelated" sourceRef="start" targetRef="unrelated_approval" />
    <bpmn:sequenceFlow id="to-end" sourceRef="unrelated_approval" targetRef="end" />
  </bpmn:process>
</bpmn:definitions>`),
			wantErr: "任务节点不存在于已部署流程定义",
		},
		{
			name:    "declared legacy service reference is unregistered",
			taskKey: "legacy_service",
			bpmnXML: []byte(`<?xml version="1.0" encoding="UTF-8"?>
<bpmn:definitions xmlns:bpmn="http://www.omg.org/spec/BPMN/20100524/MODEL" targetNamespace="http://bpmn.io/schema/bpmn">
  <bpmn:process id="unresolved-legacy-service" isExecutable="true">
    <bpmn:startEvent id="start" />
    <bpmn:serviceTask id="legacy_service" name="Legacy service" implementation="retired_change_handler" />
    <bpmn:endEvent id="end" />
    <bpmn:sequenceFlow id="to-legacy-service" sourceRef="start" targetRef="legacy_service" />
    <bpmn:sequenceFlow id="to-end" sourceRef="legacy_service" targetRef="end" />
  </bpmn:process>
</bpmn:definitions>`),
			wantErr:    "回调描述符无法解析",
			wantStored: bpmnUnresolvedUserTaskCallbackHandlerID,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			f := newBPMNAuthorizationFixture(t)
			task := f.seedNonParticipantApprovalTask(t, tt.taskKey)
			task, err := f.client.ProcessTask.UpdateOne(task).
				SetCandidateUsers(f.actor.Email).
				SetTaskDefinitionKey(tt.taskKey).
				SetTaskVariables(map[string]interface{}{"before": "kept"}).
				Save(f.userCtx)
			require.NoError(t, err)

			instance := f.client.ProcessInstance.GetX(f.userCtx, task.ProcessInstanceID)
			definition := f.client.ProcessDefinition.GetX(f.userCtx, instance.ProcessDefinitionID)
			_, err = f.client.ProcessDefinition.UpdateOne(definition).SetBpmnXML(tt.bpmnXML).Save(f.userCtx)
			require.NoError(t, err)

			beforeTask := f.client.ProcessTask.GetX(f.userCtx, task.ID)
			beforeInstance := f.client.ProcessInstance.GetX(f.userCtx, instance.ID)
			beforeAuditCount := f.client.ProcessAuditLog.Query().CountX(f.userCtx)
			beforeOutboxCount := f.client.ProcessCallbackOutbox.Query().CountX(f.userCtx)

			err = f.engine.CompleteTask(
				f.typedTaskScopeOnlyCtx(f.actor, false),
				task.TaskID,
				map[string]interface{}{"approvalComment": "must roll back"},
			)
			require.Error(t, err)
			assert.Contains(t, err.Error(), tt.wantErr)

			afterTask := f.client.ProcessTask.GetX(f.userCtx, task.ID)
			afterInstance := f.client.ProcessInstance.GetX(f.userCtx, instance.ID)
			assert.Equal(t, beforeTask.Status, afterTask.Status)
			assert.Equal(t, beforeTask.TaskVariables, afterTask.TaskVariables)
			assert.Equal(t, beforeTask.CallbackHandlerID, afterTask.CallbackHandlerID)
			assert.Equal(t, beforeTask.CallbackTaskType, afterTask.CallbackTaskType)
			assert.Equal(t, beforeInstance.Version, afterInstance.Version)
			assert.Equal(t, beforeInstance.Status, afterInstance.Status)
			assert.Equal(t, beforeInstance.CurrentActivityID, afterInstance.CurrentActivityID)
			assert.Equal(t, beforeInstance.Variables, afterInstance.Variables)
			assert.Equal(t, beforeAuditCount, f.client.ProcessAuditLog.Query().CountX(f.userCtx))
			assert.Equal(t, beforeOutboxCount, f.client.ProcessCallbackOutbox.Query().CountX(f.userCtx))

			if tt.wantStored == "" {
				return
			}
			definitions, err := f.engine.parser.ParseXML(tt.bpmnXML)
			require.NoError(t, err)
			require.Len(t, definitions.Processes, 1)
			descriptor, err := f.engine.descriptorForProcessTask(f.userCtx, f.client, afterTask, definitions.Processes[0])
			require.NoError(t, err)
			assert.Equal(t, tt.wantStored, descriptor.HandlerID)
			assert.Equal(t, "retired_change_handler", descriptor.TaskType)
		})
	}
}

func TestExecuteStepRejectsOrphanElement(t *testing.T) {
	tests := []struct {
		name    string
		element string
		process *BPMNProcess
	}{
		{
			name:    "user task without outgoing flow",
			element: "orphan_user_task",
			process: &BPMNProcess{UserTasks: []*BPMNUserTask{{ID: "orphan_user_task", Name: "Orphan user task"}}},
		},
		{
			name:    "service task without outgoing flow",
			element: "orphan_service_task",
			process: &BPMNProcess{ServiceTasks: []*BPMNServiceTask{{ID: "orphan_service_task", Name: "Orphan service task"}}},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			f := newBPMNAuthorizationFixture(t)
			instance := f.createProcessInstance(t, f.tenant, tt.element)
			instance, err := f.client.ProcessInstance.UpdateOne(instance).
				SetStatus("running").
				SetCurrentActivityID(tt.element).
				SetCurrentActivityName(tt.element).
				Save(f.userCtx)
			require.NoError(t, err)

			err = f.engine.executeStep(f.userCtx, instance, tt.process, tt.element, map[string]interface{}{})
			require.Error(t, err)
			assert.Contains(t, err.Error(), tt.element)

			after := f.client.ProcessInstance.GetX(f.userCtx, instance.ID)
			assert.Equal(t, "running", after.Status)
			assert.Equal(t, tt.element, after.CurrentActivityID)
		})
	}
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

func TestCCCallbackPayloadNormalizesVariableRecipients(t *testing.T) {
	f := newBPMNAuthorizationFixture(t)
	handler := f.engine.findHandlerByTaskType("cc_task")
	require.NotNil(t, handler)

	t.Run("stores only the resolved recipient IDs", func(t *testing.T) {
		watchers := []interface{}{float64(12), "13", float64(12)}
		payload, err := filterBPMNCallbackPayload(handler, "", map[string]interface{}{
			"ccType":            "variable",
			"ccVariable":        "approval_watchers",
			"approval_watchers": watchers,
			"notifyChannels":    "in_app,email",
			"addedBy":           999999,
			"authorization":     "must-not-persist",
		})

		require.NoError(t, err)
		assert.Equal(t, "variable", payload["ccType"])
		assert.Equal(t, "approval_watchers", payload["ccVariable"])
		assert.Equal(t, []int{12, 13}, payload["ccResolvedUserIds"])
		assert.Equal(t, "in_app,email", payload["notifyChannels"])
		assert.NotContains(t, payload, "approval_watchers")
		assert.NotContains(t, payload, "addedBy")
		assert.NotContains(t, payload, "authorization")

		watchers[0] = float64(99)
		assert.Equal(t, []int{12, 13}, payload["ccResolvedUserIds"])
	})

	for _, tt := range []struct {
		name      string
		variables map[string]interface{}
	}{
		{
			name: "missing source variable",
			variables: map[string]interface{}{
				"ccType":     "variable",
				"ccVariable": "approval_watchers",
			},
		},
		{
			name: "object source variable",
			variables: map[string]interface{}{
				"ccType":            "variable",
				"ccVariable":        "approval_watchers",
				"approval_watchers": map[string]interface{}{"id": 12},
			},
		},
		{
			name: "non-positive recipient",
			variables: map[string]interface{}{
				"ccType":            "variable",
				"ccVariable":        "approval_watchers",
				"approval_watchers": []interface{}{float64(0)},
			},
		},
		{
			name: "non-numeric recipient",
			variables: map[string]interface{}{
				"ccType":            "variable",
				"ccVariable":        "approval_watchers",
				"approval_watchers": []interface{}{"not-a-user-id"},
			},
		},
	} {
		t.Run(tt.name, func(t *testing.T) {
			_, err := filterBPMNCallbackPayload(handler, "", tt.variables)
			require.Error(t, err)
		})
	}
}

func TestCCCallbackAuthoritativeVariablesRequireValidInitiator(t *testing.T) {
	f := newBPMNAuthorizationFixture(t)
	ticket := f.client.Ticket.Create().
		SetTitle("CC callback attribution").
		SetTicketNumber("CC-CALLBACK-ATTRIBUTION").
		SetStatus("open").
		SetRequesterID(f.actor.ID).
		SetTenantID(f.tenant.ID).
		SaveX(f.userCtx)
	inactiveUser := f.client.User.Create().
		SetUsername("inactive-cc-initiator").
		SetEmail("inactive-cc-initiator@example.test").
		SetName("Inactive CC Initiator").
		SetPasswordHash("test").
		SetActive(false).
		SetTenantID(f.tenant.ID).
		SaveX(f.userCtx)
	instance := f.createProcessInstance(t, f.tenant, "cc-callback-attribution")
	instance = f.client.ProcessInstance.UpdateOne(instance).
		SetBusinessType("ticket").
		SetBusinessID(ticket.ID).
		SetInitiator(strconv.Itoa(f.actor.ID)).
		SaveX(f.userCtx)
	handler := f.engine.findHandlerByTaskType("cc_task")
	require.NotNil(t, handler)

	payload := map[string]interface{}{"addedBy": f.otherActor.ID}
	variables, err := f.engine.authoritativeCallbackVariables(f.userCtx, instance, handler, payload)
	require.NoError(t, err)
	assert.Equal(t, f.actor.ID, variables["addedBy"])
	assert.Equal(t, f.otherActor.ID, payload["addedBy"])

	for _, initiator := range []string{"", "not-a-user-id", "0", strconv.Itoa(f.otherActor.ID), strconv.Itoa(inactiveUser.ID)} {
		t.Run("rejects "+initiator, func(t *testing.T) {
			instance.Initiator = initiator
			_, err := f.engine.authoritativeCallbackVariables(f.userCtx, instance, handler, map[string]interface{}{})
			require.EqualError(t, err, "CC回调流程发起人无效")
		})
	}
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
