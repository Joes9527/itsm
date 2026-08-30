package service

import (
	"context"
	"errors"
	"fmt"
	"strconv"
	"testing"

	"itsm-backend/common"
	"itsm-backend/ent"
	"itsm-backend/ent/processauditlog"
	"itsm-backend/ent/processcallbackoutbox"
	"itsm-backend/ent/processtask"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func automaticCounterSignBPMNXML(approvalMode, sourceApprover, counterSignApprovers string) []byte {
	return []byte(fmt.Sprintf(`<?xml version="1.0" encoding="UTF-8"?>
<bpmn:definitions xmlns:bpmn="http://www.omg.org/spec/BPMN/20100524/MODEL" targetNamespace="http://bpmn.io/schema/bpmn">
  <bpmn:process id="automatic-counter-sign" isExecutable="true">
    <bpmn:startEvent id="start" />
    <bpmn:userTask id="source" name="Source" candidateUsers="%s">
      <bpmn:extensionElements><bpmn:metaData name="service_task_type">counter_sign_transaction_callback</bpmn:metaData></bpmn:extensionElements>
    </bpmn:userTask>
    <bpmn:userTask id="counter-sign" name="Counter sign" taskPurpose="approval" approvalMode="%s" candidateUsers="%s" />
    <bpmn:endEvent id="end" />
    <bpmn:sequenceFlow id="to-source" sourceRef="start" targetRef="source" />
    <bpmn:sequenceFlow id="to-counter-sign" sourceRef="source" targetRef="counter-sign" />
    <bpmn:sequenceFlow id="to-end" sourceRef="counter-sign" targetRef="end" />
  </bpmn:process>
</bpmn:definitions>`, sourceApprover, approvalMode, counterSignApprovers))
}

func startAutomaticCounterSignProcess(t *testing.T, approvalMode string) (*bpmnAuthorizationFixture, context.Context, *ent.ProcessInstance, *ent.ProcessTask) {
	t.Helper()
	f := newBPMNAuthorizationFixture(t)
	configureStartProcessDefinition(t, f, automaticCounterSignBPMNXML(
		approvalMode,
		strconv.Itoa(f.actor.ID),
		strconv.Itoa(f.actor.ID)+","+strconv.Itoa(f.outsider.ID),
	))
	ctx := f.typedTaskScopeOnlyCtx(f.actor, false)
	instance, err := f.engine.StartProcess(ctx, f.definition.Key, "counter-sign", "ticket", 1, map[string]interface{}{"before": "kept"})
	require.NoError(t, err)
	source := f.client.ProcessTask.Query().Where(
		processtask.ProcessInstanceID(instance.ID),
		processtask.TaskDefinitionKey("source"),
	).OnlyX(f.userCtx)
	return f, ctx, instance, source
}

func assertAutomaticCounterSignCommit(t *testing.T, f *bpmnAuthorizationFixture, instance *ent.ProcessInstance, source *ent.ProcessTask, approvalType string) {
	t.Helper()
	updatedInstance := f.client.ProcessInstance.GetX(f.userCtx, instance.ID)
	updatedSource := f.client.ProcessTask.GetX(f.userCtx, source.ID)
	parent := f.client.ProcessTask.Query().Where(
		processtask.ProcessInstanceID(instance.ID),
		processtask.TaskDefinitionKey("counter-sign"),
	).OnlyX(f.userCtx)
	children := f.client.ProcessTask.Query().Where(
		processtask.TenantID(f.tenant.ID),
		processtask.ParentTaskID(parent.TaskID),
	).Order(ent.Asc(processtask.FieldID)).AllX(f.userCtx)

	assert.Equal(t, common.ProcessTaskStatusCompleted, updatedSource.Status)
	assert.Equal(t, "counter-sign", updatedInstance.CurrentActivityID)
	assert.Equal(t, instance.Version+1, updatedInstance.Version)
	assert.Equal(t, true, updatedInstance.Variables["approved"])
	assert.Equal(t, common.ProcessTaskStatusCreated, parent.Status)
	assert.Equal(t, approvalType, parent.TaskVariables["approval_type"])
	total, ok := numericInt(parent.TaskVariables["total"])
	assert.True(t, ok)
	assert.Equal(t, 2, total)
	require.Len(t, children, 2)
	assert.Equal(t, parent.TaskID, children[0].ParentTaskID)
	assert.Equal(t, parent.TaskID, children[1].ParentTaskID)
	assert.Equal(t, common.ProcessTaskStatusAssigned, children[0].Status)
	if approvalType == "serial" {
		assert.Equal(t, common.ProcessTaskStatusCreated, children[1].Status)
	} else {
		assert.Equal(t, common.ProcessTaskStatusAssigned, children[1].Status)
	}
	assert.Equal(t, 1, f.client.ProcessAuditLog.Query().Where(
		processauditlog.ProcessInstanceID(instance.ID),
		processauditlog.Action(AuditActionCounterSignCreated),
	).CountX(f.userCtx))
	assert.Equal(t, 1, f.client.ProcessCallbackOutbox.Query().Where(
		processcallbackoutbox.ProcessInstanceID(instance.ID),
		processcallbackoutbox.TaskID(source.TaskID),
	).CountX(f.userCtx))
}

func TestCompleteTaskCreatesParallelCounterSignInsideOwningTransaction(t *testing.T) {
	f, ctx, instance, source := startAutomaticCounterSignProcess(t, "all")

	require.NoError(t, f.engine.CompleteTask(ctx, source.TaskID, map[string]interface{}{"approved": true}))

	assertAutomaticCounterSignCommit(t, f, instance, source, "parallel")
}

func TestCompleteTaskCreatesSerialCounterSignInsideOwningTransaction(t *testing.T) {
	f, ctx, instance, source := startAutomaticCounterSignProcess(t, "sequential")

	require.NoError(t, f.engine.CompleteTask(ctx, source.TaskID, map[string]interface{}{"approved": true}))

	assertAutomaticCounterSignCommit(t, f, instance, source, "serial")
}

func TestCounterSignCreationFailureRollsBackSourceCompletionAndChildren(t *testing.T) {
	f, ctx, instance, source := startAutomaticCounterSignProcess(t, "all")
	beforeInstance := f.client.ProcessInstance.GetX(f.userCtx, instance.ID)
	forcedErr := errors.New("forced counter-sign audit failure after child creation")
	failProcessAuditCreation(f.client, forcedErr)

	err := f.engine.CompleteTask(ctx, source.TaskID, map[string]interface{}{"approved": true})
	require.ErrorIs(t, err, forcedErr)

	afterInstance := f.client.ProcessInstance.GetX(f.userCtx, instance.ID)
	afterSource := f.client.ProcessTask.GetX(f.userCtx, source.ID)
	assert.Equal(t, common.ProcessTaskStatusCreated, afterSource.Status)
	assert.Equal(t, beforeInstance.CurrentActivityID, afterInstance.CurrentActivityID)
	assert.Equal(t, beforeInstance.Version, afterInstance.Version)
	assert.Equal(t, beforeInstance.Variables, afterInstance.Variables)
	assert.Zero(t, f.client.ProcessTask.Query().Where(
		processtask.ProcessInstanceID(instance.ID),
		processtask.TaskDefinitionKey("counter-sign"),
	).CountX(f.userCtx))
	assert.Zero(t, f.client.ProcessTask.Query().Where(
		processtask.ProcessInstanceID(instance.ID),
		processtask.ParentTaskID(source.TaskID),
	).CountX(f.userCtx))
	assert.Zero(t, f.client.ProcessAuditLog.Query().Where(
		processauditlog.ProcessInstanceID(instance.ID),
		processauditlog.Action(AuditActionCounterSignCreated),
	).CountX(f.userCtx))
	assert.Zero(t, f.client.ProcessAuditLog.Query().Where(
		processauditlog.ProcessInstanceID(instance.ID),
		processauditlog.Action(AuditActionTaskCompleted),
	).CountX(f.userCtx))
	assert.Zero(t, f.client.ProcessCallbackOutbox.Query().Where(
		processcallbackoutbox.ProcessInstanceID(instance.ID),
	).CountX(f.userCtx))
}

func TestTransactionEngineRebindsTaskServiceToTransactionClient(t *testing.T) {
	f := newBPMNAuthorizationFixture(t)
	tx, err := f.client.Tx(f.userCtx)
	require.NoError(t, err)
	defer func() { _ = tx.Rollback() }()

	executionKeys := make([]string, 0)
	clone := f.engine.forClient(tx.Client(), &executionKeys)

	assert.Same(t, tx.Client(), clone.client)
	assert.Same(t, tx.Client(), clone.taskService.client)
	assert.Same(t, clone, clone.taskService.engine)
	assert.NotSame(t, f.engine.groupResolver, clone.groupResolver)
	assert.Same(t, clone.groupResolver, clone.taskService.groupResolver)
	assert.Same(t, tx.Client(), clone.participationResolver.client)
	assert.Same(t, tx.Client(), clone.auditService.client)
	assert.Same(t, f.engine.callbackRegistry, clone.callbackRegistry)
	assert.Same(t, f.engine.callbackOutbox, clone.callbackOutbox)
	assert.Same(t, &executionKeys, clone.callbackExecutionKeys)
}
