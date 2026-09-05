package service

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strconv"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"itsm-backend/common"
	"itsm-backend/ent"
	"itsm-backend/ent/processcallbackoutbox"
	"itsm-backend/service/bpmn"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"go.uber.org/zap"
	"go.uber.org/zap/zapcore"
	"go.uber.org/zap/zaptest/observer"
)

type countingIdempotentCallbackHandler struct {
	mu                sync.Mutex
	taskType          string
	handlerID         string
	callbackFields    []string
	failuresRemaining int
	attemptKeys       []string
	effectKeys        map[string]struct{}
	sawCompletedTask  bool
}

func TestCallbackWorkerSweepFailureEmitsSanitizedTelemetry(t *testing.T) {
	core, observed := observer.New(zapcore.WarnLevel)
	engine := &CustomProcessEngine{logger: zap.New(core).Sugar()}

	engine.runCallbackOutboxSweep(context.Background(), "callback-worker-test")

	entries := observed.All()
	require.Len(t, entries, 1)
	assert.Equal(t, "BPMN callback sweep incomplete", entries[0].Message)
	assert.Equal(t, map[string]interface{}{
		"worker_id":   "callback-worker-test",
		"error_class": "callback_sweep_error",
	}, entries[0].ContextMap())
}

func TestCCCallbackOutboxVariableRecipientsUseAuthoritativeInitiator(t *testing.T) {
	f := newBPMNAuthorizationFixture(t)
	watcher := f.client.User.Create().
		SetUsername("cc-variable-watcher").
		SetEmail("cc-variable-watcher@example.test").
		SetName("CC Variable Watcher").
		SetPasswordHash("test").
		SetActive(true).
		SetTenantID(f.tenant.ID).
		SaveX(f.userCtx)
	ticket := f.client.Ticket.Create().
		SetTitle("Variable recipient CC").
		SetTicketNumber("CC-VARIABLE-RECIPIENTS").
		SetStatus("open").
		SetRequesterID(f.actor.ID).
		SetTenantID(f.tenant.ID).
		SaveX(f.userCtx)

	definitionXML := []byte(`<?xml version="1.0" encoding="UTF-8"?>
<bpmn:definitions xmlns:bpmn="http://www.omg.org/spec/BPMN/20100524/MODEL" targetNamespace="http://bpmn.io/schema/bpmn">
  <bpmn:process id="cc-variable-recipients" isExecutable="true">
    <bpmn:startEvent id="start" />
    <bpmn:serviceTask id="cc-callback" name="CC callback" ccType="variable" ccVariable="approval_watchers" ccNotify="true" notifyChannels="in_app,email">
      <bpmn:extensionElements><bpmn:metaData name="service_task_type">cc_task</bpmn:metaData></bpmn:extensionElements>
    </bpmn:serviceTask>
    <bpmn:endEvent id="end" />
    <bpmn:sequenceFlow id="to-callback" sourceRef="start" targetRef="cc-callback" />
    <bpmn:sequenceFlow id="to-end" sourceRef="cc-callback" targetRef="end" />
  </bpmn:process>
</bpmn:definitions>`)
	instance := f.createProcessInstance(t, f.tenant, "cc-variable-recipients")
	definition := f.client.ProcessDefinition.GetX(f.userCtx, instance.ProcessDefinitionID)
	definition = f.client.ProcessDefinition.UpdateOne(definition).SetBpmnXML(definitionXML).SaveX(f.userCtx)
	instance = f.client.ProcessInstance.UpdateOne(instance).
		SetStatus("running").
		SetCurrentActivityID("cc-callback").
		SetCurrentActivityName("CC callback").
		SetBusinessType("ticket").
		SetBusinessID(ticket.ID).
		SetInitiator(strconv.Itoa(f.actor.ID)).
		SetVariables(map[string]interface{}{
			"approval_watchers": []interface{}{float64(f.outsider.ID), strconv.Itoa(watcher.ID), float64(f.outsider.ID)},
			"addedBy":           f.otherActor.ID,
			"authorization":     "must-not-persist",
		}).
		SaveX(f.userCtx)

	definitions, err := f.engine.parser.ParseXML(definition.BpmnXML)
	require.NoError(t, err)
	require.Len(t, definitions.Processes, 1)
	require.Len(t, definitions.Processes[0].ServiceTasks, 1)
	serviceTask := definitions.Processes[0].ServiceTasks[0]
	handler := f.engine.findHandlerByTaskType(serviceTask.ServiceTaskType())
	require.NotNil(t, handler)

	executionKeys := make([]string, 0, 1)
	scheduler := f.engine.forClient(f.client, &executionKeys)
	err = scheduler.enqueueServiceTaskCallback(
		f.userCtx,
		instance,
		handler,
		handler.GetTaskType(),
		serviceTask.ID,
		serviceTask.ServiceTaskAction(),
		serviceTask.CallbackConfigRef(),
		mergeServiceTaskVariables(instance.Variables, serviceTask),
		false,
	)
	require.NoError(t, err)
	require.Len(t, executionKeys, 1)

	row := callbackRowForInstance(t, f, instance.ID)
	require.NotContains(t, row.Variables, "approval_watchers")
	require.NotContains(t, row.Variables, "addedBy")
	require.NotContains(t, row.Variables, "authorization")
	require.Contains(t, row.Variables, "ccResolvedUserIds")
	require.ElementsMatch(t, []interface{}{json.Number(strconv.Itoa(f.outsider.ID)), json.Number(strconv.Itoa(watcher.ID))}, row.Variables["ccResolvedUserIds"])
	require.Equal(t, "in_app,email", row.Variables["notifyChannels"])

	completed, err := f.engine.ProcessPendingCallbacks(context.Background(), "cc-variable-recipients-worker", 1)
	require.NoError(t, err)
	require.Equal(t, 1, completed)

	relations := f.client.TicketCC.Query().AllX(f.userCtx)
	require.Len(t, relations, 2)
	for _, relation := range relations {
		assert.Equal(t, ticket.ID, relation.TicketID)
		assert.Equal(t, f.tenant.ID, relation.TenantID)
		assert.Equal(t, f.actor.ID, relation.AddedBy)
	}
	notifications := f.client.TicketNotification.Query().AllX(f.userCtx)
	require.Len(t, notifications, 4)
	channels := make([]string, 0, len(notifications))
	for _, notification := range notifications {
		channels = append(channels, notification.Channel)
	}
	assert.ElementsMatch(t, []string{"in_app", "email", "in_app", "email"}, channels)
}

func newCountingIdempotentCallbackHandler(taskType, handlerID string, failures int) *countingIdempotentCallbackHandler {
	return &countingIdempotentCallbackHandler{
		taskType: taskType, handlerID: handlerID, failuresRemaining: failures,
		effectKeys: make(map[string]struct{}),
	}
}

func (h *countingIdempotentCallbackHandler) GetTaskType() string  { return h.taskType }
func (h *countingIdempotentCallbackHandler) GetHandlerID() string { return h.handlerID }
func (h *countingIdempotentCallbackHandler) CallbackContract(string) (bpmn.CallbackActionContract, bool) {
	return bpmn.CallbackActionContract{PayloadFields: append([]string(nil), h.callbackFields...)}, true
}
func (h *countingIdempotentCallbackHandler) Execute(ctx context.Context, task *ent.ProcessTask, _ map[string]interface{}) (*bpmn.CallbackEffect, error) {
	key, ok := bpmn.BPMNCallbackExecutionKey(ctx)
	if !ok || key == "" {
		return nil, errors.New("callback execution key missing")
	}
	h.mu.Lock()
	defer h.mu.Unlock()
	h.attemptKeys = append(h.attemptKeys, key)
	if task != nil && task.Status == common.ProcessTaskStatusCompleted {
		h.sawCompletedTask = true
	}
	if h.failuresRemaining > 0 {
		h.failuresRemaining--
		return nil, errors.New("sensitive callback receiver failure")
	}
	h.effectKeys[key] = struct{}{}
	return bpmn.AppliedEffect("", nil), nil
}

func (h *countingIdempotentCallbackHandler) AttemptCount() int {
	h.mu.Lock()
	defer h.mu.Unlock()
	return len(h.attemptKeys)
}

func (h *countingIdempotentCallbackHandler) EffectCount() int {
	h.mu.Lock()
	defer h.mu.Unlock()
	return len(h.effectKeys)
}

func (h *countingIdempotentCallbackHandler) ExecutionKeys() []string {
	h.mu.Lock()
	defer h.mu.Unlock()
	return append([]string(nil), h.attemptKeys...)
}

func (h *countingIdempotentCallbackHandler) SawCompletedTask() bool {
	h.mu.Lock()
	defer h.mu.Unlock()
	return h.sawCompletedTask
}

var _ bpmn.ServiceTaskHandlerInterface = (*countingIdempotentCallbackHandler)(nil)

func seedDurableServiceCallbackTask(
	t *testing.T,
	f *bpmnAuthorizationFixture,
	suffix string,
	handler *countingIdempotentCallbackHandler,
) (*ent.ProcessTask, *ent.ProcessInstance) {
	t.Helper()
	task := f.seedNonParticipantApprovalTask(t, suffix)
	task, err := f.client.ProcessTask.UpdateOne(task).SetCandidateUsers(f.actor.Email).Save(f.userCtx)
	require.NoError(t, err)
	instance := f.client.ProcessInstance.GetX(f.userCtx, task.ProcessInstanceID)
	definition := f.client.ProcessDefinition.GetX(f.userCtx, instance.ProcessDefinitionID)
	xml := fmt.Sprintf(`<?xml version="1.0" encoding="UTF-8"?>
<bpmn:definitions xmlns:bpmn="http://www.omg.org/spec/BPMN/20100524/MODEL" targetNamespace="http://bpmn.io/schema/bpmn">
  <bpmn:process id="durable-service" isExecutable="true">
    <bpmn:startEvent id="start" />
    <bpmn:userTask id="approval" name="Approval" />
    <bpmn:serviceTask id="callback" name="Callback">
      <bpmn:extensionElements><bpmn:metaData name="service_task_type">%s</bpmn:metaData></bpmn:extensionElements>
    </bpmn:serviceTask>
    <bpmn:endEvent id="end" />
    <bpmn:sequenceFlow id="to-approval" sourceRef="start" targetRef="approval" />
    <bpmn:sequenceFlow id="to-callback" sourceRef="approval" targetRef="callback" />
    <bpmn:sequenceFlow id="to-end" sourceRef="callback" targetRef="end" />
  </bpmn:process>
</bpmn:definitions>`, handler.GetTaskType())
	_, err = f.client.ProcessDefinition.UpdateOne(definition).SetBpmnXML([]byte(xml)).Save(f.userCtx)
	require.NoError(t, err)
	f.engine.CallbackRegistry().RegisterHandler(handler)
	return task, instance
}

func seedDurableUserCallbackTask(
	t *testing.T,
	f *bpmnAuthorizationFixture,
	suffix string,
	handler *countingIdempotentCallbackHandler,
) (*ent.ProcessTask, *ent.ProcessInstance) {
	t.Helper()
	task := f.seedNonParticipantApprovalTask(t, suffix)
	task, err := f.client.ProcessTask.UpdateOne(task).
		SetCandidateUsers(f.actor.Email).
		Save(f.userCtx)
	require.NoError(t, err)
	configureLegacyUserCallbackDefinition(t, f, task, handler.GetTaskType(), "record_completion")
	f.engine.CallbackRegistry().RegisterHandler(handler)
	return task, f.client.ProcessInstance.GetX(f.userCtx, task.ProcessInstanceID)
}

func configureLegacyUserCallbackDefinition(t *testing.T, f *bpmnAuthorizationFixture, task *ent.ProcessTask, taskType, action string) {
	t.Helper()
	instance := f.client.ProcessInstance.GetX(f.userCtx, task.ProcessInstanceID)
	definition := f.client.ProcessDefinition.GetX(f.userCtx, instance.ProcessDefinitionID)
	xml := fmt.Sprintf(`<?xml version="1.0" encoding="UTF-8"?>
<bpmn:definitions xmlns:bpmn="http://www.omg.org/spec/BPMN/20100524/MODEL" targetNamespace="http://bpmn.io/schema/bpmn">
  <bpmn:process id="legacy-user-callback" isExecutable="true">
    <bpmn:startEvent id="start" />
    <bpmn:userTask id="%s" name="Approval">
      <bpmn:extensionElements>
        <bpmn:metaData name="service_task_type">%s</bpmn:metaData>
        <bpmn:metaData name="action">%s</bpmn:metaData>
      </bpmn:extensionElements>
    </bpmn:userTask>
    <bpmn:endEvent id="end" />
    <bpmn:sequenceFlow id="to-approval" sourceRef="start" targetRef="%s" />
    <bpmn:sequenceFlow id="to-end" sourceRef="%s" targetRef="end" />
  </bpmn:process>
</bpmn:definitions>`, task.TaskDefinitionKey, taskType, action, task.TaskDefinitionKey, task.TaskDefinitionKey)
	_, err := f.client.ProcessDefinition.UpdateOne(definition).SetBpmnXML([]byte(xml)).Save(f.userCtx)
	require.NoError(t, err)
}

func seedDurableCCUserCallbackTask(
	t *testing.T,
	f *bpmnAuthorizationFixture,
	suffix string,
) (*ent.ProcessTask, *ent.ProcessInstance, *ent.Ticket) {
	t.Helper()
	task := f.seedNonParticipantApprovalTask(t, suffix)
	task, err := f.client.ProcessTask.UpdateOne(task).
		SetCandidateUsers(f.actor.Email).
		Save(f.userCtx)
	require.NoError(t, err)
	configureLegacyUserCallbackDefinition(t, f, task, "cc_task", "notify_cc")
	ticket := f.client.Ticket.Create().
		SetTitle("Durable CC callback").
		SetTicketNumber("BPMN-CC-" + suffix).
		SetStatus("open").
		SetRequesterID(f.actor.ID).
		SetTenantID(f.tenant.ID).
		SaveX(f.userCtx)
	instance := f.client.ProcessInstance.GetX(f.userCtx, task.ProcessInstanceID)
	instance, err = f.client.ProcessInstance.UpdateOne(instance).
		SetBusinessType("ticket").
		SetBusinessID(ticket.ID).
		SetInitiator(strconv.Itoa(f.actor.ID)).
		Save(f.userCtx)
	require.NoError(t, err)
	return task, instance, ticket
}

func setCallbackTestClock(engine *CustomProcessEngine, now *time.Time) {
	engine.callbackOutbox.now = func() time.Time { return *now }
}

func failNextCallbackCompletion(client *ent.Client, forcedErr error) {
	var failOnce atomic.Bool
	failOnce.Store(true)
	client.Use(func(next ent.Mutator) ent.Mutator {
		return ent.MutateFunc(func(ctx context.Context, mutation ent.Mutation) (ent.Value, error) {
			if outboxMutation, ok := mutation.(*ent.ProcessCallbackOutboxMutation); ok {
				if status, exists := outboxMutation.Status(); exists && status == bpmnCallbackStatusCompleted && failOnce.CompareAndSwap(true, false) {
					return nil, forcedErr
				}
			}
			return next.Mutate(ctx, mutation)
		})
	})
}

func failNextCallbackTokenAdvance(client *ent.Client, targetActivityID string, forcedErr error) {
	var failOnce atomic.Bool
	failOnce.Store(true)
	client.Use(func(next ent.Mutator) ent.Mutator {
		return ent.MutateFunc(func(ctx context.Context, mutation ent.Mutation) (ent.Value, error) {
			if instanceMutation, ok := mutation.(*ent.ProcessInstanceMutation); ok {
				if activityID, exists := instanceMutation.CurrentActivityID(); exists && activityID == targetActivityID && failOnce.CompareAndSwap(true, false) {
					return nil, forcedErr
				}
			}
			return next.Mutate(ctx, mutation)
		})
	})
}

func callbackRowForInstance(t *testing.T, f *bpmnAuthorizationFixture, instanceID int) *ent.ProcessCallbackOutbox {
	t.Helper()
	return f.client.ProcessCallbackOutbox.Query().Where(
		processcallbackoutbox.TenantID(f.tenant.ID),
		processcallbackoutbox.ProcessInstanceID(instanceID),
	).OnlyX(f.userCtx)
}

func TestCallbackWorkerRetriesAfterHandlerFailureWithSameExecutionKey(t *testing.T) {
	f := newBPMNAuthorizationFixture(t)
	now := time.Date(2026, 8, 30, 12, 0, 0, 0, time.UTC)
	setCallbackTestClock(f.engine, &now)
	handler := newCountingIdempotentCallbackHandler("durable_service_task", "durable_service_handler", 1)
	task, instance := seedDurableServiceCallbackTask(t, f, "handler-retry", handler)

	require.NoError(t, f.engine.CompleteTask(f.typedTaskScopeOnlyCtx(f.actor, false), task.TaskID, map[string]interface{}{}))
	row := callbackRowForInstance(t, f, instance.ID)
	require.Equal(t, bpmnCallbackStatusPending, row.Status)
	now = now.Add(time.Second)

	completed, err := f.engine.ProcessPendingCallbacks(context.Background(), "retry-worker", 50)
	require.NoError(t, err)
	require.Equal(t, 1, completed)
	row = callbackRowForInstance(t, f, instance.ID)
	assert.Equal(t, bpmnCallbackStatusCompleted, row.Status)
	assert.Equal(t, []string{row.ExecutionKey, row.ExecutionKey}, handler.ExecutionKeys())
	assert.Equal(t, 1, handler.EffectCount())
}

func TestCompleteTaskCanonicalizesValidCCChannelsBeforeEnqueue(t *testing.T) {
	f := newBPMNAuthorizationFixture(t)
	task, instance, _ := seedDurableCCUserCallbackTask(t, f, "canonical-channels")

	err := f.engine.CompleteTask(f.typedTaskScopeOnlyCtx(f.actor, false), task.TaskID, map[string]interface{}{
		"ccType":         "user",
		"ccUserIds":      strconv.Itoa(f.outsider.ID),
		"ccNotify":       true,
		"notifyChannels": " email, in_app,email ",
	})
	require.NoError(t, err)

	row := callbackRowForInstance(t, f, instance.ID)
	assert.Equal(t, "email,in_app", row.Variables["notifyChannels"])
}

func TestCallbackWorkerRecoversExpiredLeaseAfterSimulatedCrash(t *testing.T) {
	f := newBPMNAuthorizationFixture(t)
	now := time.Date(2026, 8, 30, 12, 0, 0, 0, time.UTC)
	setCallbackTestClock(f.engine, &now)
	handler := newCountingIdempotentCallbackHandler("durable_service_task", "durable_service_handler", 1)
	task, instance := seedDurableServiceCallbackTask(t, f, "expired-lease", handler)
	require.NoError(t, f.engine.CompleteTask(f.typedTaskScopeOnlyCtx(f.actor, false), task.TaskID, map[string]interface{}{}))

	now = now.Add(time.Second)
	row := callbackRowForInstance(t, f, instance.ID)
	claimed, err := f.engine.callbackOutbox.claim(context.Background(), "crashed-worker", row)
	require.NoError(t, err)
	require.True(t, claimed)
	now = now.Add(bpmnCallbackLeaseDuration + time.Second)

	completed, err := f.engine.ProcessPendingCallbacks(context.Background(), "recovery-worker", 50)
	require.NoError(t, err)
	require.Equal(t, 1, completed)
	row = callbackRowForInstance(t, f, instance.ID)
	assert.Equal(t, bpmnCallbackStatusCompleted, row.Status)
	assert.Equal(t, 3, row.AttemptCount)
	assert.Equal(t, 1, handler.EffectCount())
}

func TestCallbackHandlerSuccessThenAdvanceFailureRetriesAndCompletesToken(t *testing.T) {
	f := newBPMNAuthorizationFixture(t)
	now := time.Date(2026, 8, 30, 12, 0, 0, 0, time.UTC)
	setCallbackTestClock(f.engine, &now)
	handler := newCountingIdempotentCallbackHandler("durable_service_task", "durable_service_handler", 0)
	task, instance := seedDurableServiceCallbackTask(t, f, "advance-retry", handler)
	failNextCallbackTokenAdvance(f.client, "end", errors.New("forced process token advancement rollback"))

	require.NoError(t, f.engine.CompleteTask(f.typedTaskScopeOnlyCtx(f.actor, false), task.TaskID, map[string]interface{}{}))
	row := callbackRowForInstance(t, f, instance.ID)
	assert.Equal(t, bpmnCallbackStatusPending, row.Status)
	assert.Equal(t, "advance_error", row.LastErrorClass)
	assert.Equal(t, common.ProcessTaskStatusCompleted, f.client.ProcessTask.GetX(f.userCtx, task.ID).Status)
	assert.Equal(t, "callback", f.client.ProcessInstance.GetX(f.userCtx, instance.ID).CurrentActivityID)
	assert.Equal(t, 1, handler.EffectCount())

	now = now.Add(time.Second)
	completed, err := f.engine.ProcessPendingCallbacks(context.Background(), "advance-retry-worker", 50)
	require.NoError(t, err)
	require.Equal(t, 1, completed)
	row = callbackRowForInstance(t, f, instance.ID)
	assert.Equal(t, bpmnCallbackStatusCompleted, row.Status)
	assert.Equal(t, "end", f.client.ProcessInstance.GetX(f.userCtx, instance.ID).CurrentActivityID)
	assert.Equal(t, "completed", f.client.ProcessInstance.GetX(f.userCtx, instance.ID).Status)
	assert.Equal(t, 1, handler.EffectCount())
	assert.Equal(t, []string{row.ExecutionKey, row.ExecutionKey}, handler.ExecutionKeys())
}

func TestChangeCallbackBusinessEffectSurvivesAdvanceFailureWithoutReplay(t *testing.T) {
	f := newBPMNAuthorizationFixture(t)
	now := time.Date(2026, 8, 30, 12, 0, 0, 0, time.UTC)
	setCallbackTestClock(f.engine, &now)

	workItem := f.client.Ticket.Create().
		SetTitle("Durable callback change").
		SetStatus("pending").
		SetType("change").
		SetRecordClass("change_request").
		SetPriority("medium").
		SetTicketNumber("BPMN-CALLBACK-CHANGE-1").
		SetRequesterID(f.actor.ID).
		SetTenantID(f.tenant.ID).
		SaveX(f.userCtx)
	changeEntity := f.client.Change.Create().
		SetType("normal").
		SetRiskLevel("medium").
		SetImpactScope("low").
		SetWorkItemID(workItem.ID).
		SaveX(f.userCtx)

	task := f.seedNonParticipantApprovalTask(t, "real-change-advance-retry")
	task = f.client.ProcessTask.UpdateOne(task).
		SetCandidateUsers(f.actor.Email).
		SaveX(f.userCtx)
	instance := f.client.ProcessInstance.GetX(f.userCtx, task.ProcessInstanceID)
	instance = f.client.ProcessInstance.UpdateOne(instance).
		SetBusinessKey(fmt.Sprintf("change:%d", workItem.ID)).
		SetBusinessType("change").
		SetBusinessID(workItem.ID).
		SaveX(f.userCtx)
	definition := f.client.ProcessDefinition.GetX(f.userCtx, instance.ProcessDefinitionID)
	definitionXML := `<?xml version="1.0" encoding="UTF-8"?>
<bpmn:definitions xmlns:bpmn="http://www.omg.org/spec/BPMN/20100524/MODEL" targetNamespace="http://bpmn.io/schema/bpmn">
  <bpmn:process id="durable-change" isExecutable="true">
    <bpmn:startEvent id="start" />
    <bpmn:userTask id="approval" name="Approval" />
    <bpmn:serviceTask id="callback" name="Schedule change">
      <bpmn:extensionElements>
        <bpmn:metaData name="service_task_type">change_task</bpmn:metaData>
        <bpmn:metaData name="action">schedule_change</bpmn:metaData>
      </bpmn:extensionElements>
    </bpmn:serviceTask>
    <bpmn:endEvent id="end" />
    <bpmn:sequenceFlow id="to-approval" sourceRef="start" targetRef="approval" />
    <bpmn:sequenceFlow id="to-callback" sourceRef="approval" targetRef="callback" />
    <bpmn:sequenceFlow id="to-end" sourceRef="callback" targetRef="end" />
  </bpmn:process>
</bpmn:definitions>`
	f.client.ProcessDefinition.UpdateOne(definition).SetBpmnXML([]byte(definitionXML)).ExecX(f.userCtx)
	failNextCallbackTokenAdvance(f.client, "end", errors.New("forced process token advancement rollback"))

	require.NoError(t, f.engine.CompleteTask(f.typedTaskScopeOnlyCtx(f.actor, false), task.TaskID, nil))
	firstEffect := f.client.Change.GetX(f.userCtx, changeEntity.ID)
	require.Equal(t, "scheduled", requireChangeWorkItem(t, f.client, firstEffect).Status)
	row := callbackRowForInstance(t, f, instance.ID)
	require.Equal(t, bpmnCallbackStatusPending, row.Status)
	require.Equal(t, "advance_error", row.LastErrorClass)

	now = now.Add(time.Second)
	completed, err := f.engine.ProcessPendingCallbacks(context.Background(), "real-change-retry-worker", 50)
	require.NoError(t, err)
	require.Equal(t, 1, completed)
	afterRetry := f.client.Change.GetX(f.userCtx, changeEntity.ID)
	assert.Equal(t, "scheduled", requireChangeWorkItem(t, f.client, afterRetry).Status)
	assert.Equal(t, firstEffect.PlannedStartDate, afterRetry.PlannedStartDate)
	assert.Equal(t, firstEffect.PlannedEndDate, afterRetry.PlannedEndDate)
	assert.Equal(t, bpmnCallbackStatusCompleted, callbackRowForInstance(t, f, instance.ID).Status)
	assert.Equal(t, "completed", f.client.ProcessInstance.GetX(f.userCtx, instance.ID).Status)
}

func TestCallbackCompletionAndTokenAdvanceRollbackTogether(t *testing.T) {
	f := newBPMNAuthorizationFixture(t)
	now := time.Date(2026, 8, 30, 12, 0, 0, 0, time.UTC)
	setCallbackTestClock(f.engine, &now)
	handler := newCountingIdempotentCallbackHandler("durable_service_task", "durable_service_handler", 0)
	task, instance := seedDurableServiceCallbackTask(t, f, "atomic-advance-rollback", handler)
	failNextCallbackCompletion(f.client, errors.New("forced callback completion failure"))

	require.NoError(t, f.engine.CompleteTask(f.typedTaskScopeOnlyCtx(f.actor, false), task.TaskID, map[string]interface{}{}))

	row := callbackRowForInstance(t, f, instance.ID)
	assert.Equal(t, bpmnCallbackStatusPending, row.Status)
	assert.Zero(t, row.CompletedAt)
	processInstance := f.client.ProcessInstance.GetX(f.userCtx, instance.ID)
	assert.Equal(t, "callback", processInstance.CurrentActivityID)
	assert.NotEqual(t, "completed", processInstance.Status)
}

func TestUserTaskCallbackFailureRemainsDurablyRetryable(t *testing.T) {
	f := newBPMNAuthorizationFixture(t)
	now := time.Date(2026, 8, 30, 12, 0, 0, 0, time.UTC)
	setCallbackTestClock(f.engine, &now)
	handler := newCountingIdempotentCallbackHandler("durable_user_task", "durable_user_handler", 1)
	task, instance := seedDurableUserCallbackTask(t, f, "user-callback-retry", handler)

	require.NoError(t, f.engine.CompleteTask(
		f.typedTaskScopeOnlyCtx(f.actor, false), task.TaskID, map[string]interface{}{"business_id": 42},
	))
	row := callbackRowForInstance(t, f, instance.ID)
	assert.Equal(t, "user_task_callback", row.CallbackKind)
	assert.Equal(t, task.ID, row.ProcessTaskID)
	assert.Equal(t, bpmnCallbackStatusPending, row.Status)
	assert.Equal(t, common.ProcessTaskStatusCompleted, f.client.ProcessTask.GetX(f.userCtx, task.ID).Status)

	now = now.Add(time.Second)
	completed, err := f.engine.ProcessPendingCallbacks(context.Background(), "user-callback-worker", 50)
	require.NoError(t, err)
	require.Equal(t, 1, completed)
	row = callbackRowForInstance(t, f, instance.ID)
	assert.Equal(t, bpmnCallbackStatusCompleted, row.Status)
	assert.True(t, handler.SawCompletedTask())
	assert.Equal(t, 1, handler.EffectCount())
}

func TestUserTaskCallbackWithoutRegisteredHandlerFailsClosedUntilHandlerIsRegistered(t *testing.T) {
	f := newBPMNAuthorizationFixture(t)
	now := time.Date(2026, 8, 30, 12, 0, 0, 0, time.UTC)
	setCallbackTestClock(f.engine, &now)
	task := f.seedNonParticipantApprovalTask(t, "user-callback-missing-handler")
	task, err := f.client.ProcessTask.UpdateOne(task).
		SetCandidateUsers(f.actor.Email).
		Save(f.userCtx)
	require.NoError(t, err)
	configureLegacyUserCallbackDefinition(t, f, task, "future_user_callback", "record_completion")

	beforeTask := f.client.ProcessTask.GetX(f.userCtx, task.ID)
	beforeInstance := f.client.ProcessInstance.GetX(f.userCtx, task.ProcessInstanceID)
	beforeAuditCount := f.client.ProcessAuditLog.Query().CountX(f.userCtx)

	err = f.engine.CompleteTask(
		f.typedTaskScopeOnlyCtx(f.actor, false), task.TaskID, map[string]interface{}{"business_id": 42},
	)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "回调描述符无法解析")

	afterFailedTask := f.client.ProcessTask.GetX(f.userCtx, task.ID)
	afterFailedInstance := f.client.ProcessInstance.GetX(f.userCtx, task.ProcessInstanceID)
	assert.Equal(t, beforeTask.Status, afterFailedTask.Status)
	assert.Equal(t, beforeTask.TaskVariables, afterFailedTask.TaskVariables)
	assert.Empty(t, afterFailedTask.CallbackHandlerID)
	assert.Empty(t, afterFailedTask.CallbackTaskType)
	assert.Equal(t, beforeInstance.Version, afterFailedInstance.Version)
	assert.Equal(t, beforeInstance.Status, afterFailedInstance.Status)
	assert.Equal(t, beforeInstance.CurrentActivityID, afterFailedInstance.CurrentActivityID)
	assert.Equal(t, beforeInstance.Variables, afterFailedInstance.Variables)
	assert.Equal(t, beforeAuditCount, f.client.ProcessAuditLog.Query().CountX(f.userCtx))
	assert.Zero(t, f.client.ProcessCallbackOutbox.Query().Where(
		processcallbackoutbox.TenantID(f.tenant.ID),
		processcallbackoutbox.ProcessTaskID(task.ID),
	).CountX(f.userCtx))

	handler := newCountingIdempotentCallbackHandler("future_user_callback", "future_user_handler", 0)
	f.engine.CallbackRegistry().RegisterHandler(handler)

	require.NoError(t, f.engine.CompleteTask(
		f.typedTaskScopeOnlyCtx(f.actor, false), task.TaskID, map[string]interface{}{"business_id": 42},
	))

	row, err := f.client.ProcessCallbackOutbox.Query().Where(
		processcallbackoutbox.TenantID(f.tenant.ID),
		processcallbackoutbox.ProcessTaskID(task.ID),
	).Only(f.userCtx)
	require.NoError(t, err)
	assert.Equal(t, "user_task_callback", row.CallbackKind)
	assert.Equal(t, "future_user_handler", row.HandlerID)
	assert.Equal(t, "future_user_callback", row.TaskType)
	assert.Equal(t, bpmnCallbackStatusCompleted, row.Status)
	assert.NotEmpty(t, row.ExecutionKey)
	assert.Equal(t, "future_user_handler", f.client.ProcessTask.GetX(f.userCtx, task.ID).CallbackHandlerID)
	assert.Equal(t, "future_user_callback", f.client.ProcessTask.GetX(f.userCtx, task.ID).CallbackTaskType)
	assert.Equal(t, 1, handler.EffectCount())
}

func TestStoredUserTaskCallbackRequiresCurrentHandlerBeforeMutation(t *testing.T) {
	f := newBPMNAuthorizationFixture(t)
	handler := newCountingIdempotentCallbackHandler("stored_user_callback", "stored_user_handler", 0)
	handler.callbackFields = []string{"decision", "note"}
	task := f.seedNonParticipantApprovalTask(t, "stored-user-callback-missing-handler")
	task = f.client.ProcessTask.UpdateOne(task).
		SetCandidateUsers(f.actor.Email).
		SetCallbackHandlerID(handler.GetHandlerID()).
		SetCallbackTaskType(handler.GetTaskType()).
		SetCallbackAction("record_completion").
		SaveX(f.userCtx)
	instance := f.client.ProcessInstance.GetX(f.userCtx, task.ProcessInstanceID)
	beforeAuditCount := f.client.ProcessAuditLog.Query().CountX(f.userCtx)
	payload := map[string]interface{}{
		"decision":      "approve",
		"note":          "preserve this allowlisted payload",
		"authorization": "must-not-persist",
	}

	err := f.engine.CompleteTask(f.typedTaskScopeOnlyCtx(f.actor, false), task.TaskID, payload)
	require.ErrorContains(t, err, "回调处理器不可用")

	afterFailedTask := f.client.ProcessTask.GetX(f.userCtx, task.ID)
	afterFailedInstance := f.client.ProcessInstance.GetX(f.userCtx, instance.ID)
	assert.Equal(t, task.Status, afterFailedTask.Status)
	assert.Equal(t, task.TaskVariables, afterFailedTask.TaskVariables)
	assert.Equal(t, instance.Version, afterFailedInstance.Version)
	assert.Equal(t, instance.Variables, afterFailedInstance.Variables)
	assert.Equal(t, beforeAuditCount, f.client.ProcessAuditLog.Query().CountX(f.userCtx))
	assert.Zero(t, f.client.ProcessCallbackOutbox.Query().Where(
		processcallbackoutbox.TenantID(f.tenant.ID),
		processcallbackoutbox.ProcessTaskID(task.ID),
	).CountX(f.userCtx))

	f.engine.CallbackRegistry().RegisterHandler(handler)
	require.NoError(t, f.engine.CompleteTask(f.typedTaskScopeOnlyCtx(f.actor, false), task.TaskID, payload))
	row := f.client.ProcessCallbackOutbox.Query().Where(
		processcallbackoutbox.TenantID(f.tenant.ID),
		processcallbackoutbox.ProcessTaskID(task.ID),
	).OnlyX(f.userCtx)
	assert.Equal(t, map[string]interface{}{
		"decision": "approve",
		"note":     "preserve this allowlisted payload",
	}, map[string]any(row.Variables))
}

func TestAlreadyEnqueuedCallbackRemainsRetryableWhenHandlerDisappears(t *testing.T) {
	f := newBPMNAuthorizationFixture(t)
	now := time.Date(2026, 8, 31, 9, 0, 0, 0, time.UTC)
	setCallbackTestClock(f.engine, &now)
	handler := newCountingIdempotentCallbackHandler("disappearing_callback", "disappearing_handler", 1)
	task, instance := seedDurableServiceCallbackTask(t, f, "handler-disappears", handler)

	require.NoError(t, f.engine.CompleteTask(f.typedTaskScopeOnlyCtx(f.actor, false), task.TaskID, map[string]interface{}{}))
	row := callbackRowForInstance(t, f, instance.ID)
	require.Equal(t, bpmnCallbackStatusPending, row.Status)
	require.Equal(t, 1, row.AttemptCount)

	f.engine.CallbackRegistry().UnregisterHandler(handler.GetHandlerID())
	now = row.NextAttemptAt
	completed, err := f.engine.ProcessPendingCallbacks(context.Background(), "missing-handler-retry-worker", 10)
	require.Error(t, err)
	require.Zero(t, completed)
	row = callbackRowForInstance(t, f, instance.ID)
	assert.Equal(t, bpmnCallbackStatusPending, row.Status)
	assert.Equal(t, "handler_error", row.LastErrorClass)
	assert.Equal(t, 2, row.AttemptCount)
}

func TestUserTaskCallbackNormalizesPersistedTaskTypeWhenMetadataUsesHandlerID(t *testing.T) {
	f := newBPMNAuthorizationFixture(t)
	handler := newCountingIdempotentCallbackHandler("normalized_user_type", "declared_user_handler_id", 0)
	f.engine.CallbackRegistry().RegisterHandler(handler)
	task := f.seedNonParticipantApprovalTask(t, "user-callback-handler-id")
	task, err := f.client.ProcessTask.UpdateOne(task).
		SetCandidateUsers(f.actor.Email).
		Save(f.userCtx)
	require.NoError(t, err)
	configureLegacyUserCallbackDefinition(t, f, task, handler.GetHandlerID(), "record_completion")

	require.NoError(t, f.engine.CompleteTask(f.typedTaskScopeOnlyCtx(f.actor, false), task.TaskID, map[string]interface{}{}))
	row := callbackRowForInstance(t, f, task.ProcessInstanceID)
	assert.Equal(t, handler.GetHandlerID(), row.HandlerID)
	assert.Equal(t, handler.GetTaskType(), row.TaskType)
	assert.Equal(t, bpmnCallbackStatusCompleted, row.Status)
	assert.Equal(t, 1, handler.EffectCount())
}

func TestCallbackExecutorUsesPersistedHandlerIDWhenTaskTypesMatch(t *testing.T) {
	f := newBPMNAuthorizationFixture(t)
	now := time.Date(2026, 8, 30, 12, 0, 0, 0, time.UTC)
	setCallbackTestClock(f.engine, &now)
	exact := newCountingIdempotentCallbackHandler("shared_callback_type", "exact_shared_handler", 1)
	task, instance := seedDurableServiceCallbackTask(t, f, "exact-handler-id", exact)

	require.NoError(t, f.engine.CompleteTask(f.typedTaskScopeOnlyCtx(f.actor, false), task.TaskID, map[string]interface{}{}))
	row := callbackRowForInstance(t, f, instance.ID)
	require.Equal(t, bpmnCallbackStatusPending, row.Status)
	require.Equal(t, exact.GetHandlerID(), row.HandlerID)

	wrong := newCountingIdempotentCallbackHandler(exact.GetTaskType(), exact.GetTaskType(), 0)
	f.engine.CallbackRegistry().RegisterHandler(wrong)
	now = now.Add(time.Second)
	completed, err := f.engine.ProcessPendingCallbacks(context.Background(), "exact-handler-worker", 50)
	require.NoError(t, err)
	require.Equal(t, 1, completed)
	row = callbackRowForInstance(t, f, instance.ID)
	assert.Equal(t, bpmnCallbackStatusCompleted, row.Status)
	assert.Equal(t, 2, exact.AttemptCount())
	assert.Equal(t, 1, exact.EffectCount())
	assert.Zero(t, wrong.AttemptCount())
}

func TestCallbackExecutorRejectsPersistedHandlerTypeMismatchWithoutWrongExecution(t *testing.T) {
	f := newBPMNAuthorizationFixture(t)
	now := time.Date(2026, 8, 30, 12, 0, 0, 0, time.UTC)
	setCallbackTestClock(f.engine, &now)
	original := newCountingIdempotentCallbackHandler("replaceable_handler", "replaceable_handler", 1)
	task, instance := seedDurableServiceCallbackTask(t, f, "handler-type-mismatch", original)

	require.NoError(t, f.engine.CompleteTask(f.typedTaskScopeOnlyCtx(f.actor, false), task.TaskID, map[string]interface{}{}))
	row := callbackRowForInstance(t, f, instance.ID)
	require.Equal(t, bpmnCallbackStatusPending, row.Status)

	wrong := newCountingIdempotentCallbackHandler("different_task_type", original.GetHandlerID(), 0)
	f.engine.CallbackRegistry().RegisterHandler(wrong)
	now = now.Add(time.Second)
	completed, err := f.engine.ProcessPendingCallbacks(context.Background(), "mismatched-handler-worker", 50)
	require.Error(t, err)
	assert.Zero(t, completed)
	row = callbackRowForInstance(t, f, instance.ID)
	assert.Equal(t, bpmnCallbackStatusPending, row.Status)
	assert.Equal(t, "handler_error", row.LastErrorClass)
	assert.Equal(t, "callback", f.client.ProcessInstance.GetX(f.userCtx, instance.ID).CurrentActivityID)
	assert.Zero(t, wrong.AttemptCount())
}

func TestAsyncKafHandlerIsNotEnqueuedAsSynchronousCallback(t *testing.T) {
	f := newBPMNAuthorizationFixture(t)
	task := f.seedNonParticipantApprovalTask(t, "async-kaf-outbox")
	handler := &fakeAsyncServiceTaskHandler{taskType: bpmn.KafDelegateTaskType, handlerID: "kaf_delegate_handler"}
	f.engine.CallbackRegistry().RegisterHandler(handler)
	task, err := f.client.ProcessTask.UpdateOne(task).
		SetStatus(common.ProcessTaskStatusDelegated).
		SetTaskType(bpmn.KafDelegateTaskType).
		SetCallbackHandlerID(handler.GetHandlerID()).
		SetCallbackTaskType(handler.GetTaskType()).
		Save(f.userCtx)
	require.NoError(t, err)
	kafActor, err := f.client.User.Create().
		SetUsername("kaf-callback-worker").
		SetEmail("kaf-callback-worker@example.test").
		SetName("KAF callback worker").
		SetPasswordHash("test").
		SetRole(kafAutomationRole).
		SetActive(true).
		SetTenantID(f.tenant.ID).
		Save(f.userCtx)
	require.NoError(t, err)
	ctx := context.WithValue(f.userCtx, bpmn.BPMNTenantIDContextKey, f.tenant.ID)
	ctx = context.WithValue(ctx, bpmn.BPMNUserIDContextKey, kafActor.ID)
	ctx = WithBPMNAccessScope(ctx, BPMNAccessScope{UserID: kafActor.ID, TenantID: f.tenant.ID})

	require.NoError(t, f.engine.CompleteTask(ctx, task.TaskID, map[string]interface{}{"result": "complete"}))
	assert.Equal(t, 1, handler.executed)
	assert.Zero(t, f.client.ProcessCallbackOutbox.Query().Where(
		processcallbackoutbox.TenantID(f.tenant.ID),
		processcallbackoutbox.ProcessTaskID(task.ID),
	).CountX(f.userCtx))
}

func TestCallbackWorkerRunsImmediateSweepAndStopsOnCancellation(t *testing.T) {
	f := newBPMNAuthorizationFixture(t)
	now := time.Date(2026, 8, 30, 12, 0, 0, 0, time.UTC)
	setCallbackTestClock(f.engine, &now)
	handler := newCountingIdempotentCallbackHandler("durable_service_task", "durable_service_handler", 1)
	task, instance := seedDurableServiceCallbackTask(t, f, "worker-lifecycle", handler)
	require.NoError(t, f.engine.CompleteTask(f.typedTaskScopeOnlyCtx(f.actor, false), task.TaskID, map[string]interface{}{}))
	now = now.Add(time.Second)

	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})
	go func() {
		defer close(done)
		f.engine.RunCallbackOutboxWorker(ctx, "lifecycle-worker", time.Hour)
	}()
	require.Eventually(t, func() bool {
		return callbackRowForInstance(t, f, instance.ID).Status == bpmnCallbackStatusCompleted
	}, 2*time.Second, 10*time.Millisecond)
	cancel()
	select {
	case <-done:
	case <-time.After(time.Second):
		t.Fatal("callback worker did not stop after cancellation")
	}
}

func TestCallbackRecoveryNeverReclaimsRequiredBlockedOutcome(t *testing.T) {
	client := openBPMNCallbackOutboxClient(t)
	now := time.Date(2026, 9, 1, 13, 0, 0, 0, time.UTC)
	blockingExecutor := &fakeBPMNCallbackExecutor{
		effect:    bpmn.BlockedEffect(bpmn.CallbackBlockTargetMissing, "target-secret"),
		effectSet: true,
	}
	firstWorker := newBPMNCallbackOutboxForTest(client, blockingExecutor, now)
	row := enqueueBPMNCallbackOutboxForTest(t, firstWorker, "callback-recovery-blocked")

	processed, err := firstWorker.processPending(context.Background(), "first-worker", 1)
	require.NoError(t, err)
	require.Zero(t, processed)
	saved := client.ProcessCallbackOutbox.GetX(context.Background(), row.ID)
	require.Equal(t, bpmnCallbackStatusBlocked, saved.Status)

	restartExecutor := &fakeBPMNCallbackExecutor{}
	restartedWorker := newBPMNCallbackOutboxForTest(client, restartExecutor, now.Add(24*time.Hour))
	processed, err = restartedWorker.processPending(context.Background(), "restarted-worker", 10)
	require.NoError(t, err)
	require.Zero(t, processed)
	require.Empty(t, restartExecutor.keys)
	saved = client.ProcessCallbackOutbox.GetX(context.Background(), row.ID)
	assert.Equal(t, bpmnCallbackStatusBlocked, saved.Status)
	assert.Equal(t, string(bpmn.CallbackBlockTargetMissing), saved.LastErrorClass)
}

func TestCallbackRecoveryIdempotentEffectCompletesOnlyOnce(t *testing.T) {
	client := openBPMNCallbackOutboxClient(t)
	now := time.Date(2026, 9, 1, 14, 0, 0, 0, time.UTC)
	executor := &fakeBPMNCallbackExecutor{
		effect:    bpmn.IdempotentEffect("delivery already exists", nil),
		effectSet: true,
	}
	outbox := newBPMNCallbackOutboxForTest(client, executor, now)
	row := enqueueBPMNCallbackOutboxForTest(t, outbox, "callback-recovery-idempotent")

	processed, err := outbox.processPending(context.Background(), "first-worker", 1)
	require.NoError(t, err)
	require.Equal(t, 1, processed)
	saved := client.ProcessCallbackOutbox.GetX(context.Background(), row.ID)
	require.Equal(t, bpmnCallbackStatusCompleted, saved.Status)

	processed, err = outbox.processPending(context.Background(), "restarted-worker", 10)
	require.NoError(t, err)
	require.Zero(t, processed)
	require.Len(t, executor.keys, 1)
}
