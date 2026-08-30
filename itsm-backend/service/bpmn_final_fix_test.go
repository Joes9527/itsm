package service

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"strconv"
	"strings"
	"sync/atomic"
	"testing"

	"itsm-backend/common"
	"itsm-backend/dto"
	"itsm-backend/ent"
	"itsm-backend/ent/processapprovaldecision"
	"itsm-backend/ent/processauditlog"
	"itsm-backend/ent/processcallbackoutbox"
	"itsm-backend/ent/processinstance"
	"itsm-backend/ent/processtask"
	"itsm-backend/service/bpmn"

	"entgo.io/ent/dialect"
	entsql "entgo.io/ent/dialect/sql"
	_ "github.com/mattn/go-sqlite3"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func startProcessServiceTaskXML(taskType string) []byte {
	return []byte(fmt.Sprintf(`<?xml version="1.0" encoding="UTF-8"?>
<bpmn:definitions xmlns:bpmn="http://www.omg.org/spec/BPMN/20100524/MODEL" targetNamespace="http://bpmn.io/schema/bpmn">
  <bpmn:process id="start-callback" isExecutable="true">
    <bpmn:startEvent id="start" />
    <bpmn:serviceTask id="callback" name="Callback">
      <bpmn:extensionElements><bpmn:metaData name="service_task_type">%s</bpmn:metaData></bpmn:extensionElements>
    </bpmn:serviceTask>
    <bpmn:endEvent id="end" />
    <bpmn:sequenceFlow id="to-callback" sourceRef="start" targetRef="callback" />
    <bpmn:sequenceFlow id="to-end" sourceRef="callback" targetRef="end" />
  </bpmn:process>
</bpmn:definitions>`, taskType))
}

func startProcessLegacyServiceTaskXML(handlerID string) []byte {
	return []byte(fmt.Sprintf(`<?xml version="1.0" encoding="UTF-8"?>
<bpmn:definitions xmlns:bpmn="http://www.omg.org/spec/BPMN/20100524/MODEL" targetNamespace="http://bpmn.io/schema/bpmn">
  <bpmn:process id="start-legacy-callback" isExecutable="true">
    <bpmn:startEvent id="start" />
    <bpmn:serviceTask id="callback" name="Callback" implementation="%s" />
    <bpmn:endEvent id="end" />
    <bpmn:sequenceFlow id="to-callback" sourceRef="start" targetRef="callback" />
    <bpmn:sequenceFlow id="to-end" sourceRef="callback" targetRef="end" />
  </bpmn:process>
</bpmn:definitions>`, handlerID))
}

func startProcessUserTaskXML() []byte {
	return []byte(`<?xml version="1.0" encoding="UTF-8"?>
<bpmn:definitions xmlns:bpmn="http://www.omg.org/spec/BPMN/20100524/MODEL" targetNamespace="http://bpmn.io/schema/bpmn">
  <bpmn:process id="start-user-task" isExecutable="true">
    <bpmn:startEvent id="start" />
    <bpmn:userTask id="approval" name="Approval" />
    <bpmn:endEvent id="end" />
    <bpmn:sequenceFlow id="to-approval" sourceRef="start" targetRef="approval" />
    <bpmn:sequenceFlow id="to-end" sourceRef="approval" targetRef="end" />
  </bpmn:process>
</bpmn:definitions>`)
}

func approvalThenServiceTaskXML(taskType string) []byte {
	return []byte(fmt.Sprintf(`<?xml version="1.0" encoding="UTF-8"?>
<bpmn:definitions xmlns:bpmn="http://www.omg.org/spec/BPMN/20100524/MODEL" targetNamespace="http://bpmn.io/schema/bpmn">
  <bpmn:process id="approval-callback" isExecutable="true">
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
</bpmn:definitions>`, taskType))
}

func configureStartProcessDefinition(t *testing.T, f *bpmnAuthorizationFixture, xml []byte) {
	t.Helper()
	_, err := f.client.ProcessDefinition.UpdateOne(f.definition).SetBpmnXML(xml).Save(f.userCtx)
	require.NoError(t, err)
}

func startProcessContext(f *bpmnAuthorizationFixture) context.Context {
	ctx := context.WithValue(f.userCtx, bpmn.BPMNTenantIDContextKey, f.tenant.ID)
	ctx = context.WithValue(ctx, bpmn.BPMNUserIDContextKey, f.actor.ID)
	return context.WithValue(ctx, "user", f.actor)
}

func assertNoStartedProcessState(t *testing.T, f *bpmnAuthorizationFixture) {
	t.Helper()
	assert.Zero(t, f.client.ProcessInstance.Query().CountX(f.userCtx))
	assert.Zero(t, f.client.ProcessExecutionHistory.Query().CountX(f.userCtx))
	assert.Zero(t, f.client.ProcessTask.Query().CountX(f.userCtx))
	assert.Zero(t, f.client.ProcessCallbackOutbox.Query().CountX(f.userCtx))
	assert.Zero(t, f.client.ProcessAuditLog.Query().CountX(f.userCtx))
}

func failProcessCallbackOutboxCreation(client *ent.Client, forcedErr error) {
	var failOnce atomic.Bool
	failOnce.Store(true)
	client.Use(func(next ent.Mutator) ent.Mutator {
		return ent.MutateFunc(func(ctx context.Context, mutation ent.Mutation) (ent.Value, error) {
			if _, ok := mutation.(*ent.ProcessCallbackOutboxMutation); ok && failOnce.CompareAndSwap(true, false) {
				return nil, forcedErr
			}
			return next.Mutate(ctx, mutation)
		})
	})
}

func TestStartProcessAuditFailureRollsBackAllRecoverableState(t *testing.T) {
	f := newBPMNAuthorizationFixture(t)
	configureStartProcessDefinition(t, f, startProcessUserTaskXML())
	forcedErr := errors.New("forced process started audit failure")
	failProcessAuditCreation(f.client, forcedErr)

	_, err := f.engine.StartProcess(startProcessContext(f), f.definition.Key, "start-audit-rollback", "ticket", 101, map[string]interface{}{})
	require.ErrorIs(t, err, forcedErr)
	assertNoStartedProcessState(t, f)
}

func TestStartProcessAuditFailureRollsBackInitialCallbackOutbox(t *testing.T) {
	f := newBPMNAuthorizationFixture(t)
	handler := newCountingIdempotentCallbackHandler("start_audit_failure", "start_audit_failure_handler", 0)
	f.engine.CallbackRegistry().RegisterHandler(handler)
	configureStartProcessDefinition(t, f, startProcessServiceTaskXML(handler.GetTaskType()))
	forcedErr := errors.New("forced process started audit failure after enqueue")
	failProcessAuditCreation(f.client, forcedErr)

	_, err := f.engine.StartProcess(startProcessContext(f), f.definition.Key, "start-audit-outbox-rollback", "ticket", 105, map[string]interface{}{})
	require.ErrorIs(t, err, forcedErr)
	assertNoStartedProcessState(t, f)
	assert.Zero(t, handler.AttemptCount())
}

func TestStartProcessOutboxFailureRollsBackAllRecoverableState(t *testing.T) {
	f := newBPMNAuthorizationFixture(t)
	handler := newCountingIdempotentCallbackHandler("start_outbox_failure", "start_outbox_failure_handler", 0)
	f.engine.CallbackRegistry().RegisterHandler(handler)
	configureStartProcessDefinition(t, f, startProcessServiceTaskXML(handler.GetTaskType()))
	failProcessCallbackOutboxCreation(f.client, errors.New("forced initial callback outbox failure"))

	_, err := f.engine.StartProcess(startProcessContext(f), f.definition.Key, "start-outbox-rollback", "ticket", 102, map[string]interface{}{})
	require.Error(t, err)
	assertNoStartedProcessState(t, f)
	assert.Zero(t, handler.AttemptCount())
}

func TestStartProcessMissingDeclaredServiceTaskHandlerRollsBackScheduling(t *testing.T) {
	f := newBPMNAuthorizationFixture(t)
	configureStartProcessDefinition(t, f, startProcessServiceTaskXML("missing_declared_handler"))

	_, err := f.engine.StartProcess(startProcessContext(f), f.definition.Key, "start-missing-handler", "ticket", 103, map[string]interface{}{})
	require.Error(t, err)
	assertNoStartedProcessState(t, f)
}

func TestStartProcessMissingLegacyServiceTaskHandlerRollsBackScheduling(t *testing.T) {
	f := newBPMNAuthorizationFixture(t)
	configureStartProcessDefinition(t, f, startProcessLegacyServiceTaskXML("missing_legacy_handler"))

	_, err := f.engine.StartProcess(startProcessContext(f), f.definition.Key, "start-missing-legacy-handler", "ticket", 108, map[string]interface{}{})
	require.Error(t, err)
	assertNoStartedProcessState(t, f)
}

func TestStartProcessRunsInitialCallbackOnlyAfterAtomicCommit(t *testing.T) {
	f := newBPMNAuthorizationFixture(t)
	handler := &startProcessCommitProbeHandler{
		client: f.client, tenantID: f.tenant.ID, businessKey: "start-post-commit",
	}
	f.engine.CallbackRegistry().RegisterHandler(handler)
	configureStartProcessDefinition(t, f, startProcessServiceTaskXML(handler.GetTaskType()))

	instance, err := f.engine.StartProcess(startProcessContext(f), f.definition.Key, handler.businessKey, "ticket", 104, map[string]interface{}{})
	require.NoError(t, err)
	assert.True(t, handler.observedCommittedState)
	assert.Equal(t, common.ProcessTaskStatusCompleted, f.client.ProcessInstance.GetX(f.userCtx, instance.ID).Status)
	row := callbackRowForInstance(t, f, instance.ID)
	assert.Equal(t, bpmnCallbackStatusCompleted, row.Status)
}

func TestStartProcessReturnsSuccessWhenInlineCallbackAttemptFails(t *testing.T) {
	f := newBPMNAuthorizationFixture(t)
	handler := newCountingIdempotentCallbackHandler("start_inline_failure", "start_inline_failure_handler", 1)
	f.engine.CallbackRegistry().RegisterHandler(handler)
	configureStartProcessDefinition(t, f, startProcessServiceTaskXML(handler.GetTaskType()))

	instance, err := f.engine.StartProcess(startProcessContext(f), f.definition.Key, "start-inline-failure", "ticket", 106, map[string]interface{}{})
	require.NoError(t, err)
	row := callbackRowForInstance(t, f, instance.ID)
	assert.Equal(t, bpmnCallbackStatusPending, row.Status)
	assert.Equal(t, "handler_error", row.LastErrorClass)
	assert.Equal(t, "callback", f.client.ProcessInstance.GetX(f.userCtx, instance.ID).CurrentActivityID)
	assert.Equal(t, 1, f.client.ProcessAuditLog.Query().Where(
		processauditlog.TenantID(f.tenant.ID),
		processauditlog.ProcessInstanceID(instance.ID),
		processauditlog.Action(AuditActionProcessStarted),
	).CountX(f.userCtx))
}

func TestStartProcessNormalizesPersistedTaskTypeWhenDefinitionUsesHandlerID(t *testing.T) {
	f := newBPMNAuthorizationFixture(t)
	handler := newCountingIdempotentCallbackHandler("normalized_start_type", "declared_start_handler_id", 0)
	f.engine.CallbackRegistry().RegisterHandler(handler)
	configureStartProcessDefinition(t, f, startProcessServiceTaskXML(handler.GetHandlerID()))

	instance, err := f.engine.StartProcess(startProcessContext(f), f.definition.Key, "start-handler-id", "ticket", 107, map[string]interface{}{})
	require.NoError(t, err)
	row := callbackRowForInstance(t, f, instance.ID)
	assert.Equal(t, handler.GetHandlerID(), row.HandlerID)
	assert.Equal(t, handler.GetTaskType(), row.TaskType)
	assert.Equal(t, bpmnCallbackStatusCompleted, row.Status)
	assert.Equal(t, 1, handler.EffectCount())
}

type startProcessCommitProbeHandler struct {
	client                 *ent.Client
	tenantID               int
	businessKey            string
	observedCommittedState bool
}

func (h *startProcessCommitProbeHandler) GetTaskType() string  { return "start_commit_probe" }
func (h *startProcessCommitProbeHandler) GetHandlerID() string { return "start_commit_probe_handler" }
func (h *startProcessCommitProbeHandler) Validate(context.Context, map[string]interface{}) error {
	return nil
}
func (h *startProcessCommitProbeHandler) Execute(ctx context.Context, _ *ent.ProcessTask, _ map[string]interface{}) (*dto.ServiceTaskResult, error) {
	instance, err := h.client.ProcessInstance.Query().Where(
		processinstance.TenantID(h.tenantID),
		processinstance.BusinessKey(h.businessKey),
	).Only(ctx)
	if err != nil {
		return nil, err
	}
	audits, err := h.client.ProcessAuditLog.Query().Where(
		processauditlog.TenantID(h.tenantID),
		processauditlog.ProcessInstanceID(instance.ID),
		processauditlog.Action(AuditActionProcessStarted),
	).Count(ctx)
	if err != nil {
		return nil, err
	}
	h.observedCommittedState = audits == 1
	if !h.observedCommittedState {
		return nil, errors.New("initial callback observed uncommitted start state")
	}
	return &dto.ServiceTaskResult{Success: true}, nil
}

var _ bpmn.ServiceTaskHandlerInterface = (*startProcessCommitProbeHandler)(nil)

func TestCompleteTaskRollsBackDatabaseStateWhenAuditFails(t *testing.T) {
	f := newBPMNAuthorizationFixture(t)
	task := f.seedNonParticipantApprovalTask(t, "complete-audit-rollback")
	task, err := f.client.ProcessTask.UpdateOne(task).
		SetCandidateUsers(f.actor.Email).
		SetTaskVariables(map[string]interface{}{"before": "kept"}).
		Save(f.userCtx)
	require.NoError(t, err)
	instance := f.client.ProcessInstance.GetX(f.userCtx, task.ProcessInstanceID)
	forcedErr := errors.New("forced complete audit failure")
	failProcessAuditCreation(f.client, forcedErr)

	err = f.engine.CompleteTask(f.typedTaskScopeOnlyCtx(f.actor, false), task.TaskID, map[string]interface{}{
		"approvalAction": "approve",
		"approved":       true,
	})
	require.ErrorIs(t, err, forcedErr)

	afterTask := f.client.ProcessTask.GetX(f.userCtx, task.ID)
	afterInstance := f.client.ProcessInstance.GetX(f.userCtx, instance.ID)
	assert.Equal(t, common.ProcessTaskStatusCreated, afterTask.Status)
	assert.Equal(t, map[string]interface{}{"before": "kept"}, afterTask.TaskVariables)
	assert.Equal(t, instance.CurrentActivityID, afterInstance.CurrentActivityID)
	assert.Equal(t, instance.Status, afterInstance.Status)
	assert.Equal(t, instance.Variables, afterInstance.Variables)
	assert.Zero(t, f.client.ProcessApprovalDecision.Query().CountX(f.userCtx))
	assert.Zero(t, f.client.ProcessAuditLog.Query().CountX(f.userCtx))
}

func TestCompleteTaskCommitsCallbackOutboxAtomically(t *testing.T) {
	f := newBPMNAuthorizationFixture(t)
	handler := newCountingIdempotentCallbackHandler("durable_service_task", "durable_service_handler", 0)
	task, instance := seedDurableServiceCallbackTask(t, f, "atomic-commit", handler)

	require.NoError(t, f.engine.CompleteTask(
		f.typedTaskScopeOnlyCtx(f.actor, false), task.TaskID, map[string]interface{}{"approved": true},
	))

	row := f.client.ProcessCallbackOutbox.Query().Where(
		processcallbackoutbox.TenantID(f.tenant.ID),
		processcallbackoutbox.ProcessInstanceID(instance.ID),
	).OnlyX(f.userCtx)
	assert.Equal(t, bpmnCallbackStatusCompleted, row.Status)
	assert.Equal(t, "service_task", row.CallbackKind)
	assert.Equal(t, "callback", row.ElementID)
	assert.NotEmpty(t, row.ExecutionKey)
	assert.Equal(t, common.ProcessTaskStatusCompleted, f.client.ProcessTask.GetX(f.userCtx, task.ID).Status)
	assert.Equal(t, 1, f.client.ProcessAuditLog.Query().Where(
		processauditlog.ProcessInstanceID(instance.ID),
		processauditlog.Action(AuditActionTaskCompleted),
	).CountX(f.userCtx))
}

func TestCompleteTaskAuditFailureLeavesNoCallbackOutbox(t *testing.T) {
	f := newBPMNAuthorizationFixture(t)
	handler := newCountingIdempotentCallbackHandler("durable_service_task", "durable_service_handler", 0)
	task, instance := seedDurableServiceCallbackTask(t, f, "audit-outbox-rollback", handler)
	forcedErr := errors.New("forced callback audit rollback")
	failProcessAuditCreation(f.client, forcedErr)

	err := f.engine.CompleteTask(
		f.typedTaskScopeOnlyCtx(f.actor, false), task.TaskID, map[string]interface{}{"approved": true},
	)
	require.ErrorIs(t, err, forcedErr)

	assert.Equal(t, common.ProcessTaskStatusCreated, f.client.ProcessTask.GetX(f.userCtx, task.ID).Status)
	assert.Equal(t, "approval", f.client.ProcessInstance.GetX(f.userCtx, instance.ID).CurrentActivityID)
	assert.Zero(t, f.client.ProcessCallbackOutbox.Query().Where(
		processcallbackoutbox.TenantID(f.tenant.ID),
		processcallbackoutbox.ProcessInstanceID(instance.ID),
	).CountX(f.userCtx))
	assert.Zero(t, handler.AttemptCount())
}

func TestCompleteTaskReturnsSuccessWhenInlineCallbackAttemptFails(t *testing.T) {
	f := newBPMNAuthorizationFixture(t)
	handler := newCountingIdempotentCallbackHandler("durable_service_task", "durable_service_handler", 1)
	task, instance := seedDurableServiceCallbackTask(t, f, "inline-failure", handler)

	require.NoError(t, f.engine.CompleteTask(
		f.typedTaskScopeOnlyCtx(f.actor, false), task.TaskID, map[string]interface{}{"approved": true},
	))

	row := f.client.ProcessCallbackOutbox.Query().Where(
		processcallbackoutbox.TenantID(f.tenant.ID),
		processcallbackoutbox.ProcessInstanceID(instance.ID),
	).OnlyX(f.userCtx)
	assert.Equal(t, bpmnCallbackStatusPending, row.Status)
	assert.Equal(t, "handler_error", row.LastErrorClass)
	assert.Equal(t, common.ProcessTaskStatusCompleted, f.client.ProcessTask.GetX(f.userCtx, task.ID).Status)
	assert.Equal(t, "callback", f.client.ProcessInstance.GetX(f.userCtx, instance.ID).CurrentActivityID)
}

func TestCompleteTaskMissingDeclaredServiceTaskHandlerRollsBackScheduling(t *testing.T) {
	f := newBPMNAuthorizationFixture(t)
	task := f.seedNonParticipantApprovalTask(t, "missing-declared-service-handler")
	task, err := f.client.ProcessTask.UpdateOne(task).SetCandidateUsers(f.actor.Email).Save(f.userCtx)
	require.NoError(t, err)
	instance := f.client.ProcessInstance.GetX(f.userCtx, task.ProcessInstanceID)
	definition := f.client.ProcessDefinition.GetX(f.userCtx, instance.ProcessDefinitionID)
	_, err = f.client.ProcessDefinition.UpdateOne(definition).
		SetBpmnXML(approvalThenServiceTaskXML("missing_declared_handler_after_user_task")).
		Save(f.userCtx)
	require.NoError(t, err)

	err = f.engine.CompleteTask(f.typedTaskScopeOnlyCtx(f.actor, false), task.TaskID, map[string]interface{}{})
	require.Error(t, err)
	assert.Equal(t, common.ProcessTaskStatusCreated, f.client.ProcessTask.GetX(f.userCtx, task.ID).Status)
	assert.Equal(t, "approval", f.client.ProcessInstance.GetX(f.userCtx, instance.ID).CurrentActivityID)
	assert.Zero(t, f.client.ProcessCallbackOutbox.Query().Where(
		processcallbackoutbox.TenantID(f.tenant.ID),
		processcallbackoutbox.ProcessInstanceID(instance.ID),
	).CountX(f.userCtx))
	assert.Zero(t, f.client.ProcessAuditLog.Query().Where(
		processauditlog.TenantID(f.tenant.ID),
		processauditlog.ProcessInstanceID(instance.ID),
	).CountX(f.userCtx))
}

func TestCompleteTaskAuditUsesTypedScopeActor(t *testing.T) {
	f := newBPMNAuthorizationFixture(t)
	task := f.seedNonParticipantApprovalTask(t, "complete-audit-actor")
	task, err := f.client.ProcessTask.UpdateOne(task).SetCandidateUsers(f.actor.Email).Save(f.userCtx)
	require.NoError(t, err)

	require.NoError(t, f.engine.CompleteTask(f.typedTaskScopeOnlyCtx(f.actor, false), task.TaskID, map[string]interface{}{}))
	audit := f.client.ProcessAuditLog.Query().Where(
		processauditlog.ProcessInstanceID(task.ProcessInstanceID),
		processauditlog.Action(AuditActionTaskCompleted),
	).OnlyX(f.userCtx)
	assert.Equal(t, f.actor.ID, audit.UserID)
	assert.Equal(t, f.actor.Name, audit.UserName)
}

func TestClaimTaskAuditFailureRollsBackBothClaimVariants(t *testing.T) {
	variants := map[string]func(*bpmnAuthorizationFixture, context.Context, *ent.ProcessTask) error{
		"task key": func(f *bpmnAuthorizationFixture, ctx context.Context, task *ent.ProcessTask) error {
			return f.engine.TaskService().ClaimTask(ctx, task.TaskID, strconv.Itoa(f.actor.ID))
		},
		"database id": func(f *bpmnAuthorizationFixture, ctx context.Context, task *ent.ProcessTask) error {
			return f.engine.TaskService().ClaimTaskByID(ctx, task.ID, f.actor.ID)
		},
	}
	for name, claim := range variants {
		t.Run(name, func(t *testing.T) {
			f := newBPMNAuthorizationFixture(t)
			instance := f.createProcessInstance(t, f.tenant, "claim-audit-rollback-"+strings.ReplaceAll(name, " ", "-"))
			task := f.createProcessTask(t, instance, f.tenant.ID, "claim-audit-rollback-"+strings.ReplaceAll(name, " ", "-"), "", "", f.actor.Role)
			forcedErr := errors.New("forced claim audit failure")
			failProcessAuditCreation(f.client, forcedErr)

			err := claim(f, f.typedTaskScopeOnlyCtx(f.actor, false), task)
			require.ErrorIs(t, err, forcedErr)
			after := f.client.ProcessTask.GetX(f.userCtx, task.ID)
			assert.Empty(t, after.Assignee)
			assert.Equal(t, common.ProcessTaskStatusCreated, after.Status)
			assert.Zero(t, f.client.ProcessAuditLog.Query().CountX(f.userCtx))
		})
	}
}

func TestClaimTaskConcurrentClaimersUseCAS(t *testing.T) {
	rawDB, err := sql.Open("sqlite3", testDSN())
	require.NoError(t, err)
	rawDB.SetMaxOpenConns(1)
	client := ent.NewClient(ent.Driver(entsql.OpenDB(dialect.SQLite, rawDB)))
	require.NoError(t, client.Schema.Create(context.Background()))
	f := newBPMNAuthorizationFixtureWithClient(t, client)
	instance := f.createProcessInstance(t, f.tenant, "claim-concurrent")
	task := f.createProcessTask(t, instance, f.tenant.ID, "claim-concurrent", "", "", "service_agent")

	start := make(chan struct{})
	results := make(chan error, 2)
	claim := func(actor *ent.User) {
		<-start
		results <- f.engine.TaskService().ClaimTask(f.typedTaskScopeOnlyCtx(actor, false), task.TaskID, strconv.Itoa(actor.ID))
	}
	go claim(f.actor)
	go claim(f.outsider)
	close(start)

	errs := []error{<-results, <-results}
	successes, conflicts := 0, 0
	for _, claimErr := range errs {
		if claimErr == nil {
			successes++
			continue
		}
		var appErr *common.AppError
		if errors.As(claimErr, &appErr) && appErr.Code == common.ErrCodeConflict {
			conflicts++
		}
	}
	assert.Equal(t, 1, successes, "claim errors: %v", errs)
	assert.Equal(t, 1, conflicts, "claim errors: %v", errs)
	after := f.client.ProcessTask.GetX(f.userCtx, task.ID)
	assert.Contains(t, []string{strconv.Itoa(f.actor.ID), strconv.Itoa(f.outsider.ID)}, after.Assignee)
	assert.Equal(t, 1, f.client.ProcessAuditLog.Query().Where(processauditlog.Action(AuditActionTaskClaimed)).CountX(f.userCtx))
}

func TestTaskMutationsUseAuthoritativeParticipantTokens(t *testing.T) {
	tokens := map[string]func(*bpmnAuthorizationFixture) (string, string, string){
		"email":           func(f *bpmnAuthorizationFixture) (string, string, string) { return "", f.actor.Email, "" },
		"primary-role":    func(f *bpmnAuthorizationFixture) (string, string, string) { return "", "", f.actor.Role },
		"additional-role": func(*bpmnAuthorizationFixture) (string, string, string) { return "", "", "network_eng" },
		"group":           func(*bpmnAuthorizationFixture) (string, string, string) { return "", "", "vpn-operators" },
	}
	mutations := map[string]func(*bpmnAuthorizationFixture, context.Context, *ent.ProcessTask) error{
		"claim": func(f *bpmnAuthorizationFixture, ctx context.Context, task *ent.ProcessTask) error {
			return f.engine.TaskService().ClaimTask(ctx, task.TaskID, strconv.Itoa(f.actor.ID))
		},
		"claim-by-id": func(f *bpmnAuthorizationFixture, ctx context.Context, task *ent.ProcessTask) error {
			return f.engine.TaskService().ClaimTaskByID(ctx, task.ID, f.actor.ID)
		},
		"complete": func(f *bpmnAuthorizationFixture, ctx context.Context, task *ent.ProcessTask) error {
			return f.engine.CompleteTask(ctx, task.TaskID, map[string]interface{}{})
		},
		"vote": func(f *bpmnAuthorizationFixture, ctx context.Context, task *ent.ProcessTask) error {
			_, err := f.client.ProcessTask.UpdateOne(task).SetStatus(common.ProcessTaskStatusAssigned).Save(f.userCtx)
			if err != nil {
				return err
			}
			return f.engine.TaskService().Vote(ctx, task.TaskID, &VoteRequest{Approved: true})
		},
	}

	for mutationName, mutate := range mutations {
		for tokenName, candidates := range tokens {
			t.Run(mutationName+" by "+tokenName, func(t *testing.T) {
				f := newBPMNAuthorizationFixture(t)
				task := f.seedNonParticipantApprovalTask(t, strings.ReplaceAll(mutationName+"-"+tokenName, " ", "-"))
				assignee, users, groups := candidates(f)
				task, err := f.client.ProcessTask.UpdateOne(task).
					SetAssignee(assignee).SetCandidateUsers(users).SetCandidateGroups(groups).Save(f.userCtx)
				require.NoError(t, err)
				require.NoError(t, mutate(f, f.typedTaskScopeOnlyCtx(f.actor, false), task))
			})
		}
	}
}

func TestVoteRollsBackFinalVoteWhenParentAdvancementFailsAndCanRetry(t *testing.T) {
	f := newBPMNAuthorizationFixture(t)
	parent := f.seedNonParticipantApprovalTask(t, "vote-parent-retry")
	parent, err := f.client.ProcessTask.UpdateOne(parent).SetCandidateUsers(f.actor.Email).Save(f.userCtx)
	require.NoError(t, err)
	children, err := f.engine.TaskService().CreateCounterSignTasks(
		f.typedTaskScopeOnlyCtx(f.actor, false), parent.TaskID,
		&CounterSignRequest{Approvers: []string{strconv.Itoa(f.actor.ID)}, ApprovalType: "parallel", Threshold: 1},
	)
	require.NoError(t, err)
	require.Len(t, children, 1)

	forcedErr := errors.New("forced parent advancement failure")
	var failOnce atomic.Bool
	failOnce.Store(true)
	f.client.Use(func(next ent.Mutator) ent.Mutator {
		return ent.MutateFunc(func(ctx context.Context, mutation ent.Mutation) (ent.Value, error) {
			if _, ok := mutation.(*ent.ProcessInstanceMutation); ok && failOnce.CompareAndSwap(true, false) {
				return nil, forcedErr
			}
			return next.Mutate(ctx, mutation)
		})
	})
	voteCtx := f.typedTaskScopeOnlyCtx(f.actor, false)

	err = f.engine.TaskService().Vote(voteCtx, children[0].TaskID, &VoteRequest{Approved: true})
	require.ErrorIs(t, err, forcedErr)
	assert.Equal(t, common.ProcessTaskStatusAssigned, f.client.ProcessTask.GetX(f.userCtx, children[0].ID).Status)
	assert.Equal(t, common.ProcessTaskStatusCreated, f.client.ProcessTask.GetX(f.userCtx, parent.ID).Status)
	assert.Zero(t, f.client.ProcessApprovalDecision.Query().Where(processapprovaldecision.ProcessTaskID(children[0].ID)).CountX(f.userCtx))

	require.NoError(t, f.engine.TaskService().Vote(voteCtx, children[0].TaskID, &VoteRequest{Approved: true}))
	assert.Equal(t, common.ProcessTaskStatusCompleted, f.client.ProcessTask.GetX(f.userCtx, children[0].ID).Status)
	assert.Equal(t, common.ProcessTaskStatusCompleted, f.client.ProcessTask.GetX(f.userCtx, parent.ID).Status)
	assert.Equal(t, common.ProcessTaskStatusCompleted, f.client.ProcessInstance.GetX(f.userCtx, parent.ProcessInstanceID).Status)
}

func TestInternalCABCascadeIsNarrowTenantBoundAndAudited(t *testing.T) {
	f := newBPMNAuthorizationFixture(t)
	task := seedInternalCascadeTask(t, f, "Activity_Schedule")
	req := BPMNInternalCascadeRequest{
		TenantID: f.tenant.ID, InstanceID: task.ProcessInstanceID, TaskID: task.TaskID,
		NodeKey: task.TaskDefinitionKey, Source: BPMNInternalSourceChangeCABCascade,
		Variables: map[string]interface{}{"change_id": 42},
	}

	badNode := req
	badNode.NodeKey = "Activity_Implement"
	requireBPMNForbidden(t, CompleteBPMNInternalCascade(f.userCtx, f.engine, badNode))
	badTenant := req
	badTenant.TenantID = f.otherTenant.ID
	requireBPMNForbidden(t, CompleteBPMNInternalCascade(f.userCtx, f.engine, badTenant))
	badSource := req
	badSource.Source = BPMNInternalSource("untrusted_caller")
	requireBPMNForbidden(t, CompleteBPMNInternalCascade(f.userCtx, f.engine, badSource))
	require.NoError(t, CompleteBPMNInternalCascade(f.userCtx, f.engine, req))

	audit := f.client.ProcessAuditLog.Query().Where(processauditlog.ProcessInstanceID(task.ProcessInstanceID)).OnlyX(f.userCtx)
	assert.Equal(t, "system", audit.UserName)
	assert.Equal(t, string(BPMNInternalSourceChangeCABCascade), audit.Metadata["source"])
	assert.Equal(t, task.TaskDefinitionKey, audit.Metadata["node_key"])
}

func TestCompleteTaskRunsServiceHandlerOnlyAfterTaskAndAuditCommit(t *testing.T) {
	f := newBPMNAuthorizationFixture(t)
	task := f.seedNonParticipantApprovalTask(t, "post-commit-handler")
	task, err := f.client.ProcessTask.UpdateOne(task).SetCandidateUsers(f.actor.Email).Save(f.userCtx)
	require.NoError(t, err)
	instance := f.client.ProcessInstance.GetX(f.userCtx, task.ProcessInstanceID)
	definition := f.client.ProcessDefinition.GetX(f.userCtx, instance.ProcessDefinitionID)
	xml := `<?xml version="1.0" encoding="UTF-8"?>
<bpmn:definitions xmlns:bpmn="http://www.omg.org/spec/BPMN/20100524/MODEL" targetNamespace="http://bpmn.io/schema/bpmn">
  <bpmn:process id="post-commit" isExecutable="true">
    <bpmn:startEvent id="start" />
    <bpmn:userTask id="approval" name="Approval" />
    <bpmn:serviceTask id="probe" name="Probe">
      <bpmn:extensionElements><bpmn:metaData name="service_task_type">post_commit_probe</bpmn:metaData></bpmn:extensionElements>
    </bpmn:serviceTask>
    <bpmn:endEvent id="end" />
    <bpmn:sequenceFlow id="to-approval" sourceRef="start" targetRef="approval" />
    <bpmn:sequenceFlow id="to-probe" sourceRef="approval" targetRef="probe" />
    <bpmn:sequenceFlow id="to-end" sourceRef="probe" targetRef="end" />
  </bpmn:process>
</bpmn:definitions>`
	_, err = f.client.ProcessDefinition.UpdateOne(definition).SetBpmnXML([]byte(xml)).Save(f.userCtx)
	require.NoError(t, err)
	probe := &postCommitProbeHandler{client: f.client, taskID: task.TaskID}
	f.engine.callbackRegistry.RegisterHandler(probe)

	require.NoError(t, f.engine.CompleteTask(f.typedTaskScopeOnlyCtx(f.actor, false), task.TaskID, map[string]interface{}{}))
	assert.True(t, probe.observedCommittedState)
	assert.Equal(t, common.ProcessTaskStatusCompleted, f.client.ProcessInstance.GetX(f.userCtx, task.ProcessInstanceID).Status)
}

type postCommitProbeHandler struct {
	client                 *ent.Client
	taskID                 string
	observedCommittedState bool
}

func (h *postCommitProbeHandler) GetTaskType() string  { return "post_commit_probe" }
func (h *postCommitProbeHandler) GetHandlerID() string { return "post_commit_probe" }
func (h *postCommitProbeHandler) Validate(context.Context, map[string]interface{}) error {
	return nil
}
func (h *postCommitProbeHandler) Execute(ctx context.Context, _ *ent.ProcessTask, _ map[string]interface{}) (*dto.ServiceTaskResult, error) {
	task, err := h.client.ProcessTask.Query().Where(processtask.TaskID(h.taskID)).Only(ctx)
	if err != nil {
		return nil, err
	}
	audits, err := h.client.ProcessAuditLog.Query().Where(
		processauditlog.ProcessInstanceID(task.ProcessInstanceID),
		processauditlog.Action(AuditActionTaskCompleted),
	).Count(ctx)
	if err != nil {
		return nil, err
	}
	h.observedCommittedState = task.Status == common.ProcessTaskStatusCompleted && audits == 1
	if !h.observedCommittedState {
		return nil, fmt.Errorf("service handler observed uncommitted task state")
	}
	return &dto.ServiceTaskResult{Success: true}, nil
}

var _ bpmn.ServiceTaskHandlerInterface = (*postCommitProbeHandler)(nil)

func seedInternalCascadeTask(t *testing.T, f *bpmnAuthorizationFixture, nodeKey string) *ent.ProcessTask {
	t.Helper()
	instance := f.createProcessInstance(t, f.tenant, "internal-cascade-"+nodeKey)
	definition := f.client.ProcessDefinition.GetX(f.userCtx, instance.ProcessDefinitionID)
	xml := fmt.Sprintf(`<?xml version="1.0" encoding="UTF-8"?>
<bpmn:definitions xmlns:bpmn="http://www.omg.org/spec/BPMN/20100524/MODEL" targetNamespace="http://bpmn.io/schema/bpmn">
  <bpmn:process id="internal-cascade" isExecutable="true">
    <bpmn:startEvent id="start" />
    <bpmn:userTask id="%s" name="Cascade" />
    <bpmn:endEvent id="end" />
    <bpmn:sequenceFlow id="to-cascade" sourceRef="start" targetRef="%s" />
    <bpmn:sequenceFlow id="to-end" sourceRef="%s" targetRef="end" />
  </bpmn:process>
</bpmn:definitions>`, nodeKey, nodeKey, nodeKey)
	_, err := f.client.ProcessDefinition.UpdateOne(definition).SetBpmnXML([]byte(xml)).Save(f.userCtx)
	require.NoError(t, err)
	_, err = f.client.ProcessInstance.UpdateOne(instance).SetCurrentActivityID(nodeKey).SetCurrentActivityName("Cascade").Save(f.userCtx)
	require.NoError(t, err)
	task := f.createProcessTask(t, instance, f.tenant.ID, "internal-cascade-task", "", "", "")
	task, err = f.client.ProcessTask.UpdateOne(task).SetTaskDefinitionKey(nodeKey).SetTaskName("Cascade").Save(f.userCtx)
	require.NoError(t, err)
	return task
}
