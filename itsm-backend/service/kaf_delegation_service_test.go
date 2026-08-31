package service

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"testing"
	"time"

	"itsm-backend/common"
	"itsm-backend/dto"
	"itsm-backend/ent"
	"itsm-backend/ent/auditlog"
	"itsm-backend/ent/enttest"
	"itsm-backend/ent/kaftaskactionledger"
	"itsm-backend/ent/kaftaskcompletionreceipt"
	"itsm-backend/ent/outboxevent"
	"itsm-backend/ent/processinstance"
	"itsm-backend/ent/processtask"
	"itsm-backend/ent/ticketcomment"
	"itsm-backend/service/bpmn"

	_ "github.com/mattn/go-sqlite3"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"go.uber.org/zap/zaptest"
)

type failingKafOutboxRepository struct {
	err error
}

func (r failingKafOutboxRepository) Enqueue(context.Context, *ent.Tx, NewOutboxEvent) (*ent.OutboxEvent, error) {
	return nil, r.err
}

func newDelegationFixture(t *testing.T) (*CustomProcessEngine, *KafDelegationService, context.Context, *ent.ProcessInstance) {
	t.Helper()

	client := enttest.Open(t, "sqlite3", "file:kaf_delegation_service?mode=memory&cache=shared&_fk=1")
	t.Cleanup(func() { client.Close() })

	engineIface := NewCustomProcessEngine(client, zaptest.NewLogger(t).Sugar())
	engine, ok := engineIface.(*CustomProcessEngine)
	require.True(t, ok)

	ctx := context.Background()
	tenant, err := client.Tenant.Create().
		SetName("KAF Delegation Tenant").
		SetCode("kaf-delegation").
		SetDomain("kaf-delegation.example.com").
		SetStatus("active").
		Save(ctx)
	require.NoError(t, err)
	actor, err := client.User.Create().
		SetUsername("kaf-delegation-actor").
		SetEmail("kaf-delegation-actor@example.com").
		SetName("KAF Delegation Actor").
		SetPasswordHash("hash").
		SetRole("agent").
		SetActive(true).
		SetTenantID(tenant.ID).
		Save(ctx)
	require.NoError(t, err)

	deployment, err := client.ProcessDeployment.Create().
		SetDeploymentID("DEP-kaf-delegation").
		SetDeploymentName("KAF Delegation Deployment").
		SetDeploymentTime(time.Now()).
		SetDeployedBy("test").
		SetIsActive(true).
		SetTenantID(tenant.ID).
		Save(ctx)
	require.NoError(t, err)
	definition, err := client.ProcessDefinition.Create().
		SetKey("kaf_delegation_flow").
		SetName("KAF Delegation Flow").
		SetVersion("1").
		SetIsLatest(true).
		SetBpmnXML([]byte("<definitions/>")).
		SetDeploymentID(deployment.ID).
		SetDeployedAt(time.Now()).
		SetTenantID(tenant.ID).
		Save(ctx)
	require.NoError(t, err)
	instance, err := client.ProcessInstance.Create().
		SetProcessInstanceID("PI-kaf-delegation").
		SetProcessDefinitionKey(definition.Key).
		SetProcessDefinitionID(definition.ID).
		SetBusinessKey("incident:42").
		SetBusinessType("incident").
		SetBusinessID(42).
		SetStatus("running").
		SetCurrentActivityID("Activity_KafDelegate").
		SetTenantID(tenant.ID).
		Save(ctx)
	require.NoError(t, err)

	ctx = context.WithValue(ctx, bpmn.BPMNTenantIDContextKey, tenant.ID)
	ctx = context.WithValue(ctx, bpmn.BPMNUserIDContextKey, actor.ID)
	return engine, engine.kafDelegationService, ctx, instance
}

func kafDelegateTask(allowedActions string) *BPMNServiceTask {
	return &BPMNServiceTask{
		ID:   "Activity_KafDelegate",
		Name: "KAF delegation",
		ExtensionElements: &BPMNExtensionElements{MetaData: []BPMNMetaData{
			{Name: bpmnMetaDataServiceTaskType, Value: bpmn.KafDelegateTaskType},
			{Name: bpmnMetaDataAllowedActions, Value: allowedActions},
		}},
	}
}

func TestCreateDelegatedTask_RollsBackTaskAndAuditWhenOutboxInsertFails(t *testing.T) {
	engine, svc, ctx, instance := newDelegationFixture(t)
	svc.outbox = failingKafOutboxRepository{err: errors.New("outbox unavailable")}

	err := engine.createDelegatedTask(ctx, instance, kafDelegateTask("resolve"), bpmn.KafDelegateTaskType)
	require.ErrorContains(t, err, "outbox unavailable")

	taskCount, err := svc.client.ProcessTask.Query().
		Where(processtask.ProcessInstanceIDEQ(instance.ID)).
		Count(ctx)
	require.NoError(t, err)
	assert.Zero(t, taskCount)
	auditCount, err := svc.client.AuditLog.Query().
		Where(auditlog.TenantIDEQ(instance.TenantID), auditlog.ActionEQ("kaf_delegate.created")).
		Count(ctx)
	require.NoError(t, err)
	assert.Zero(t, auditCount)
}

func TestCreateDelegatedTask_CommitsTaskAuditAndOutboxWithSameCorrelationID(t *testing.T) {
	_, svc, ctx, instance := newDelegationFixture(t)

	task, err := svc.CreateDelegatedTask(ctx, instance.ID, kafDelegateTask("complete_bpmn_task,update_progress"))
	require.NoError(t, err)
	require.NotEmpty(t, task.CorrelationID)
	assert.Equal(t, bpmn.KafDelegateTaskType, task.TaskType)
	assert.Equal(t, "delegated", task.Status)
	assert.Equal(t, "complete_bpmn_task,update_progress", task.TaskVariables[bpmnMetaDataAllowedActions])

	audit, err := svc.client.AuditLog.Query().
		Where(auditlog.TenantIDEQ(instance.TenantID), auditlog.ResourceEQ("process_task"), auditlog.ActionEQ("kaf_delegate.created")).
		Only(ctx)
	require.NoError(t, err)
	require.NotNil(t, audit.RequestBody)
	assert.Contains(t, *audit.RequestBody, fmt.Sprintf(`"correlationId":%q`, task.CorrelationID))
	actorID, _ := ctx.Value(bpmn.BPMNUserIDContextKey).(int)
	assert.Equal(t, actorID, audit.UserID)

	event, err := svc.client.OutboxEvent.Query().
		Where(outboxevent.AggregateIDEQ(task.TaskID)).
		Only(ctx)
	require.NoError(t, err)
	assert.Equal(t, "pending", event.Status)

	var payload KafDelegateRequested
	require.NoError(t, json.Unmarshal(event.Payload, &payload))
	assert.Equal(t, task.CorrelationID, payload.CorrelationID)
	assert.Equal(t, "incident", payload.RecordClass)
}

func TestCreateDelegatedTask_RejectsMissingOrMismatchedTenantContext(t *testing.T) {
	tests := []struct {
		name     string
		buildCtx func(context.Context, *KafDelegationService) context.Context
	}{
		{
			name: "missing tenant",
			buildCtx: func(ctx context.Context, _ *KafDelegationService) context.Context {
				return context.WithValue(context.Background(), bpmn.BPMNUserIDContextKey, ctx.Value(bpmn.BPMNUserIDContextKey))
			},
		},
		{
			name: "mismatched tenant",
			buildCtx: func(ctx context.Context, svc *KafDelegationService) context.Context {
				foreignTenant, err := svc.client.Tenant.Create().
					SetName("Foreign KAF Tenant").
					SetCode("foreign-kaf-delegation").
					SetDomain("foreign-kaf-delegation.example.com").
					SetStatus("active").
					Save(ctx)
				require.NoError(t, err)
				return context.WithValue(ctx, bpmn.BPMNTenantIDContextKey, foreignTenant.ID)
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			_, svc, ctx, instance := newDelegationFixture(t)
			_, err := svc.CreateDelegatedTask(tt.buildCtx(ctx, svc), instance.ID, kafDelegateTask("resolve"))
			require.Error(t, err)
			assertNoKafDelegationRecords(t, svc, ctx, instance)
		})
	}
}

func TestCreateDelegatedTask_RejectsCrossTenantAuditActor(t *testing.T) {
	_, svc, ctx, instance := newDelegationFixture(t)

	foreignTenant, err := svc.client.Tenant.Create().
		SetName("Foreign Audit Tenant").
		SetCode("foreign-audit-tenant").
		SetDomain("foreign-audit.example.com").
		SetStatus("active").
		Save(ctx)
	require.NoError(t, err)
	foreignActor, err := svc.client.User.Create().
		SetUsername("foreign-kaf-actor").
		SetEmail("foreign-kaf-actor@example.com").
		SetName("Foreign KAF Actor").
		SetPasswordHash("hash").
		SetRole("agent").
		SetActive(true).
		SetTenantID(foreignTenant.ID).
		Save(ctx)
	require.NoError(t, err)

	foreignActorCtx := context.WithValue(ctx, bpmn.BPMNUserIDContextKey, foreignActor.ID)
	_, err = svc.CreateDelegatedTask(foreignActorCtx, instance.ID, kafDelegateTask("resolve"))
	require.Error(t, err)
	assertNoKafDelegationRecords(t, svc, ctx, instance)
}

func assertNoKafDelegationRecords(t *testing.T, svc *KafDelegationService, ctx context.Context, instance *ent.ProcessInstance) {
	t.Helper()

	taskCount, err := svc.client.ProcessTask.Query().
		Where(processtask.ProcessInstanceIDEQ(instance.ID)).
		Count(ctx)
	require.NoError(t, err)
	assert.Zero(t, taskCount)

	auditCount, err := svc.client.AuditLog.Query().
		Where(auditlog.TenantIDEQ(instance.TenantID), auditlog.ActionEQ("kaf_delegate.created")).
		Count(ctx)
	require.NoError(t, err)
	assert.Zero(t, auditCount)

	eventCount, err := svc.client.OutboxEvent.Query().
		Where(outboxevent.TenantIDEQ(instance.TenantID), outboxevent.EventTypeEQ("kaf_delegate_requested")).
		Count(ctx)
	require.NoError(t, err)
	assert.Zero(t, eventCount)
}

// statefulKafCompletionEngine persists the generic engine's completed-task
// boundary and records invocations without needing a parsed BPMN definition.
type statefulKafCompletionEngine struct {
	ProcessEngine
	client     *ent.Client
	calls      int
	onComplete func(context.Context) error
}

type blockingKafCompletionEngine struct {
	*statefulKafCompletionEngine
	entered chan<- struct{}
	release <-chan struct{}
}

type scopeCapturingKafCallbackHandler struct {
	scope   bpmn.KafActionScope
	scopeOK bool
	calls   int
}

type failOncePersistingKafCallbackHandler struct {
	client     *ent.Client
	workItemID int
	actorID    int
	calls      int
	scope      bpmn.KafActionScope
	scopeOK    bool
}

func (h *scopeCapturingKafCallbackHandler) GetTaskType() string  { return "kaf_scope_capture" }
func (h *scopeCapturingKafCallbackHandler) GetHandlerID() string { return "kaf_scope_capture_handler" }
func (h *scopeCapturingKafCallbackHandler) Validate(context.Context, map[string]interface{}) error {
	return nil
}
func (h *scopeCapturingKafCallbackHandler) Execute(ctx context.Context, _ *ent.ProcessTask, _ map[string]interface{}) (*dto.ServiceTaskResult, error) {
	h.calls++
	h.scope, h.scopeOK = bpmn.KafActionScopeFromContext(ctx)
	return &dto.ServiceTaskResult{Success: true}, nil
}

var _ bpmn.ServiceTaskHandlerInterface = (*scopeCapturingKafCallbackHandler)(nil)

func (h *failOncePersistingKafCallbackHandler) GetTaskType() string {
	return "kaf_fail_once_persisting_callback"
}
func (h *failOncePersistingKafCallbackHandler) GetHandlerID() string {
	return "kaf_fail_once_persisting_callback_handler"
}
func (h *failOncePersistingKafCallbackHandler) Validate(context.Context, map[string]interface{}) error {
	return nil
}
func (h *failOncePersistingKafCallbackHandler) Execute(ctx context.Context, _ *ent.ProcessTask, _ map[string]interface{}) (*dto.ServiceTaskResult, error) {
	h.calls++
	h.scope, h.scopeOK = bpmn.KafActionScopeFromContext(ctx)
	if !h.scopeOK {
		return nil, errors.New("missing KAF action scope")
	}
	content := "KAF callback effect " + h.scope.IdempotencyKey()
	applied, err := h.client.TicketComment.Query().Where(
		ticketcomment.TicketIDEQ(h.workItemID), ticketcomment.ContentEQ(content),
	).Exist(ctx)
	if err != nil {
		return nil, err
	}
	if applied {
		return &dto.ServiceTaskResult{Success: true}, nil
	}
	err = h.client.TicketComment.Create().
		SetTicketID(h.workItemID).
		SetUserID(h.actorID).
		SetContent(content).
		SetIsInternal(true).
		SetTenantID(h.scope.TenantID()).
		Exec(ctx)
	if err != nil {
		return nil, err
	}
	if h.calls == 1 {
		return nil, errors.New("forced callback error after committed effect")
	}
	return &dto.ServiceTaskResult{Success: true}, nil
}

var _ bpmn.ServiceTaskHandlerInterface = (*failOncePersistingKafCallbackHandler)(nil)

func (e *statefulKafCompletionEngine) CompleteTask(ctx context.Context, taskID string, variables map[string]interface{}) error {
	e.calls++
	tenantID, _ := ctx.Value(bpmn.BPMNTenantIDContextKey).(int)
	task, err := e.client.ProcessTask.Query().Where(
		processtask.TaskIDEQ(taskID), processtask.TenantIDEQ(tenantID),
	).Only(ctx)
	if err != nil {
		return err
	}
	updated, err := e.client.ProcessTask.Update().Where(
		processtask.TaskIDEQ(taskID), processtask.TenantIDEQ(tenantID),
	).SetStatus(common.ProcessTaskStatusCompleted).SetTaskVariables(variables).Save(ctx)
	if err != nil {
		return err
	}
	if updated != 1 {
		return errors.New("stateful KAF engine could not complete task")
	}
	return e.client.ProcessInstance.UpdateOneID(task.ProcessInstanceID).
		SetStatus("completed").SetCurrentActivityID("End").SetCurrentActivityName("End").Exec(ctx)
}

func (e *statefulKafCompletionEngine) CompleteKafDelegatedTask(ctx context.Context, ledgerID int, _ string, taskID string, variables map[string]interface{}) error {
	if err := e.CompleteTask(ctx, taskID, variables); err != nil {
		return err
	}
	ledger, err := e.client.KafTaskActionLedger.Get(ctx, ledgerID)
	if err != nil {
		return err
	}
	_, err = e.client.KafTaskCompletionReceipt.Create().
		SetLedgerID(ledgerID).SetTenantID(ledger.TenantID).SetTaskID(taskID).
		SetStatus("callback_succeeded").
		Save(ctx)
	if err != nil && !ent.IsConstraintError(err) {
		return err
	}
	if e.onComplete != nil {
		return e.onComplete(ctx)
	}
	return nil
}

func (e *blockingKafCompletionEngine) CompleteKafDelegatedTask(ctx context.Context, ledgerID int, leaseOwner, taskID string, variables map[string]interface{}) error {
	e.entered <- struct{}{}
	<-e.release
	return e.statefulKafCompletionEngine.CompleteKafDelegatedTask(ctx, ledgerID, leaseOwner, taskID, variables)
}

func newKafActionFixture(t *testing.T) (*CustomProcessEngine, *KafDelegationService, *ent.ProcessTask, context.Context) {
	t.Helper()

	engine, svc, ctx, instance := newDelegationFixture(t)
	actorID, _ := ctx.Value(bpmn.BPMNUserIDContextKey).(int)
	require.NoError(t, svc.client.User.UpdateOneID(actorID).SetRole(kafAutomationRole).Exec(ctx))
	task, err := svc.client.ProcessTask.Create().
		SetTaskID("PT-kaf-action").
		SetProcessInstanceID(instance.ID).
		SetProcessDefinitionKey(instance.ProcessDefinitionKey).
		SetTaskDefinitionKey("Activity_KafDelegate").
		SetTaskName("KAF action").
		SetTaskType(bpmn.KafDelegateTaskType).
		SetStatus(common.ProcessTaskStatusDelegated).
		SetCorrelationID("correlation-kaf-action").
		SetTenantID(instance.TenantID).
		SetTaskVariables(map[string]interface{}{bpmnMetaDataAllowedActions: kafActionComplete}).
		Save(ctx)
	require.NoError(t, err)
	return engine, svc, task, ctx
}

func validCompleteRequest(task *ent.ProcessTask, runID, stepID string) KafActionRequest {
	return KafActionRequest{
		Action:          kafActionComplete,
		ExpectedVersion: 1,
		Execution: KafActionExecution{
			RunID: runID, StepID: stepID,
			IdempotencyKey: fmt.Sprintf("%d:%s:%s:%s", task.TenantID, task.TaskID, runID, stepID),
			CorrelationID:  task.CorrelationID, ProcedureRef: "ssl-vpn", ProcedureVersion: "v1",
		},
		Payload: KafActionPayload{ResultSummary: "KAF completed the delegated task"},
	}
}

func countKafActionLedgers(t *testing.T, client *ent.Client, tenantID int) int {
	t.Helper()
	count, err := client.KafTaskActionLedger.Query().Where(kaftaskactionledger.TenantIDEQ(tenantID)).Count(context.Background())
	require.NoError(t, err)
	return count
}

func TestExecuteAction_RecoversCompletedTaskAfterAuditFailureWithoutSecondEngineCall(t *testing.T) {
	_, svc, task, ctx := newKafActionFixture(t)
	req := validCompleteRequest(task, "run-1", "finish")
	engine := &statefulKafCompletionEngine{client: svc.client}
	auditAttempts := 0
	svc.client.AuditLog.Use(func(next ent.Mutator) ent.Mutator {
		return ent.MutateFunc(func(hookCtx context.Context, mutation ent.Mutation) (ent.Value, error) {
			auditAttempts++
			if auditAttempts == 1 {
				return nil, errors.New("audit storage unavailable")
			}
			return next.Mutate(hookCtx, mutation)
		})
	})

	_, err := svc.ExecuteAction(ctx, task.TaskID, req, engine)
	require.ErrorContains(t, err, "audit storage unavailable")
	assert.Equal(t, 1, engine.calls)

	second, err := svc.ExecuteAction(ctx, task.TaskID, req, engine)
	require.NoError(t, err)

	assert.Equal(t, KafActionApplied, second.ResultStatus)
	assert.Equal(t, 1, engine.calls)
	assert.Equal(t, 2, auditAttempts)
	ledger, err := svc.client.KafTaskActionLedger.Query().Where(
		kaftaskactionledger.TenantIDEQ(task.TenantID), kaftaskactionledger.IdempotencyKeyEQ(req.Execution.IdempotencyKey),
	).Only(ctx)
	require.NoError(t, err)
	assert.Equal(t, "applied", ledger.ResultStatus)
}

func TestExecuteAction_RetryAfterAppliedFinalizationFailureCreatesOneAudit(t *testing.T) {
	_, svc, task, ctx := newKafActionFixture(t)
	req := validCompleteRequest(task, "run-1", "finish")
	engine := &statefulKafCompletionEngine{client: svc.client}
	finalizationFailures := 0
	svc.client.KafTaskActionLedger.Use(func(next ent.Mutator) ent.Mutator {
		return ent.MutateFunc(func(hookCtx context.Context, mutation ent.Mutation) (ent.Value, error) {
			if ledgerMutation, ok := mutation.(*ent.KafTaskActionLedgerMutation); ok {
				if status, exists := ledgerMutation.ResultStatus(); exists && status == "applied" && finalizationFailures == 0 {
					finalizationFailures++
					return nil, errors.New("ledger finalization unavailable")
				}
			}
			return next.Mutate(hookCtx, mutation)
		})
	})

	_, err := svc.ExecuteAction(ctx, task.TaskID, req, engine)
	require.ErrorContains(t, err, "ledger finalization unavailable")
	auditCount, err := svc.client.AuditLog.Query().Where(
		auditlog.TenantIDEQ(task.TenantID), auditlog.ActionEQ("kaf_delegate."+req.Action),
	).Count(ctx)
	require.NoError(t, err)
	assert.Zero(t, auditCount)
	ledger, err := svc.client.KafTaskActionLedger.Query().Where(
		kaftaskactionledger.TenantIDEQ(task.TenantID), kaftaskactionledger.IdempotencyKeyEQ(req.Execution.IdempotencyKey),
	).Only(ctx)
	require.NoError(t, err)
	require.NoError(t, svc.client.KafTaskActionLedger.UpdateOneID(ledger.ID).SetLeaseExpiresAt(time.Now().Add(-time.Second)).Exec(ctx))

	result, err := svc.ExecuteAction(ctx, task.TaskID, req, engine)
	require.NoError(t, err)
	assert.Equal(t, KafActionApplied, result.ResultStatus)
	auditCount, err = svc.client.AuditLog.Query().Where(
		auditlog.TenantIDEQ(task.TenantID), auditlog.ActionEQ("kaf_delegate."+req.Action),
	).Count(ctx)
	require.NoError(t, err)
	assert.Equal(t, 1, auditCount)
}

func TestExecuteAction_NonCompletingFinalizationFailureRollsBackEffectAndRetriesOnce(t *testing.T) {
	_, svc, task, ctx := newKafActionFixture(t)
	workItemID := attachKafActionWorkItem(t, svc, task, ctx)
	task, err := svc.client.ProcessTask.UpdateOneID(task.ID).
		SetTaskVariables(map[string]interface{}{bpmnMetaDataAllowedActions: kafActionComplete + "," + kafActionProgress}).
		Save(ctx)
	require.NoError(t, err)
	req := validCompleteRequest(task, "run-progress", "progress")
	req.Action = kafActionProgress
	req.Payload = KafActionPayload{ResultSummary: "halfway"}
	failures := 0
	svc.client.KafTaskActionLedger.Use(func(next ent.Mutator) ent.Mutator {
		return ent.MutateFunc(func(hookCtx context.Context, mutation ent.Mutation) (ent.Value, error) {
			if ledgerMutation, ok := mutation.(*ent.KafTaskActionLedgerMutation); ok {
				if status, exists := ledgerMutation.ResultStatus(); exists && status == "applied" && failures == 0 {
					failures++
					return nil, errors.New("forced non-completing finalization failure")
				}
			}
			return next.Mutate(hookCtx, mutation)
		})
	})

	_, err = svc.ExecuteAction(ctx, task.TaskID, req, nil)
	require.ErrorContains(t, err, "forced non-completing finalization failure")
	instance, err := svc.client.ProcessInstance.Get(ctx, task.ProcessInstanceID)
	require.NoError(t, err)
	assert.Equal(t, req.ExpectedVersion, instance.Version)
	commentCount, err := svc.client.TicketComment.Query().Where(ticketcomment.TicketIDEQ(workItemID)).Count(ctx)
	require.NoError(t, err)
	assert.Zero(t, commentCount)

	result, err := svc.ExecuteAction(ctx, task.TaskID, req, nil)
	require.NoError(t, err)
	assert.Equal(t, KafActionApplied, result.ResultStatus)
	instance, err = svc.client.ProcessInstance.Get(ctx, task.ProcessInstanceID)
	require.NoError(t, err)
	assert.Equal(t, req.ExpectedVersion+1, instance.Version)
	commentCount, err = svc.client.TicketComment.Query().Where(ticketcomment.TicketIDEQ(workItemID)).Count(ctx)
	require.NoError(t, err)
	assert.Equal(t, 1, commentCount)
	auditCount, err := svc.client.AuditLog.Query().Where(
		auditlog.TenantIDEQ(task.TenantID), auditlog.ActionEQ("kaf_delegate."+req.Action),
	).Count(ctx)
	require.NoError(t, err)
	assert.Equal(t, 1, auditCount)
}

func TestExecuteAction_RejectsActiveLeaseWithoutCallingEngine(t *testing.T) {
	_, svc, task, ctx := newKafActionFixture(t)
	workItemID := attachKafActionWorkItem(t, svc, task, ctx)
	req := validCompleteRequest(task, "run-1", "finish")
	_, claimed, err := svc.ClaimKafAction(ctx, task, req)
	require.NoError(t, err)
	require.True(t, claimed)

	engine := &statefulKafCompletionEngine{client: svc.client}
	_, err = svc.ExecuteAction(ctx, task.TaskID, req, engine)
	require.ErrorIs(t, err, ErrKafActionConflict)
	assert.Zero(t, engine.calls)
	ledgerCount, err := svc.client.KafTaskActionLedger.Query().Where(
		kaftaskactionledger.TenantIDEQ(task.TenantID),
		kaftaskactionledger.TaskIDEQ(task.TaskID),
		kaftaskactionledger.ResultStatusEQ("executing"),
	).Count(ctx)
	require.NoError(t, err)
	assert.Equal(t, 1, ledgerCount)
	auditCount, err := svc.client.AuditLog.Query().Where(
		auditlog.TenantIDEQ(task.TenantID), auditlog.ActionEQ("kaf_delegate."+req.Action),
	).Count(ctx)
	require.NoError(t, err)
	assert.Zero(t, auditCount)
	commentCount, err := svc.client.TicketComment.Query().Where(ticketcomment.TicketIDEQ(workItemID)).Count(ctx)
	require.NoError(t, err)
	assert.Zero(t, commentCount)
	delegatedCount, err := svc.client.ProcessTask.Query().Where(
		processtask.IDEQ(task.ID), processtask.StatusEQ(common.ProcessTaskStatusDelegated),
	).Count(ctx)
	require.NoError(t, err)
	assert.Equal(t, 1, delegatedCount)
}

func TestExecuteAction_ConcurrentClaimsCompleteExactlyOnce(t *testing.T) {
	_, svc, task, ctx := newKafActionFixture(t)
	workItemID := attachKafActionWorkItem(t, svc, task, ctx)
	req := validCompleteRequest(task, "run-1", "finish")
	entered := make(chan struct{}, 1)
	release := make(chan struct{})
	actorID, _ := ctx.Value(bpmn.BPMNUserIDContextKey).(int)
	engine := &blockingKafCompletionEngine{
		statefulKafCompletionEngine: &statefulKafCompletionEngine{
			client: svc.client,
			onComplete: func(callbackCtx context.Context) error {
				return svc.client.TicketComment.Create().SetTicketID(workItemID).SetUserID(actorID).
					SetContent("KAF completion callback applied").SetIsInternal(true).SetTenantID(task.TenantID).Exec(callbackCtx)
			},
		},
		entered: entered,
		release: release,
	}
	type executionResult struct {
		result *KafActionResult
		err    error
	}
	firstResult := make(chan executionResult, 1)
	go func() {
		result, err := svc.ExecuteAction(ctx, task.TaskID, req, engine)
		firstResult <- executionResult{result: result, err: err}
	}()
	<-entered
	secondResult := make(chan executionResult, 1)
	go func() {
		result, err := svc.ExecuteAction(ctx, task.TaskID, req, engine)
		secondResult <- executionResult{result: result, err: err}
	}()
	loser := <-secondResult
	require.ErrorIs(t, loser.err, ErrKafActionInProgress)
	close(release)
	winner := <-firstResult
	require.NoError(t, winner.err)
	require.NotNil(t, winner.result)
	assert.Equal(t, KafActionApplied, winner.result.ResultStatus)
	assert.Equal(t, 1, engine.calls)
	replay, err := svc.ExecuteAction(ctx, task.TaskID, req, engine)
	require.NoError(t, err)
	assert.Equal(t, KafActionAlreadyApplied, replay.ResultStatus)
	ledgerCount, err := svc.client.KafTaskActionLedger.Query().Where(
		kaftaskactionledger.TenantIDEQ(task.TenantID),
		kaftaskactionledger.TaskIDEQ(task.TaskID),
		kaftaskactionledger.RunIDEQ(req.Execution.RunID),
		kaftaskactionledger.StepIDEQ(req.Execution.StepID),
	).Count(ctx)
	require.NoError(t, err)
	assert.Equal(t, 1, ledgerCount)
	auditCount, err := svc.client.AuditLog.Query().Where(
		auditlog.TenantIDEQ(task.TenantID), auditlog.ActionEQ("kaf_delegate."+req.Action),
	).Count(ctx)
	require.NoError(t, err)
	assert.Equal(t, 1, auditCount)
	commentCount, err := svc.client.TicketComment.Query().Where(ticketcomment.TicketIDEQ(workItemID)).Count(ctx)
	require.NoError(t, err)
	assert.Equal(t, 1, commentCount)
	completedCount, err := svc.client.ProcessTask.Query().Where(
		processtask.IDEQ(task.ID), processtask.StatusEQ(common.ProcessTaskStatusCompleted),
	).Count(ctx)
	require.NoError(t, err)
	assert.Equal(t, 1, completedCount)
}

func attachKafActionWorkItem(t *testing.T, svc *KafDelegationService, task *ent.ProcessTask, ctx context.Context) int {
	t.Helper()
	actorID, _ := ctx.Value(bpmn.BPMNUserIDContextKey).(int)
	workItem, err := svc.client.Ticket.Create().
		SetTitle("KAF action WorkItem").SetTicketNumber("TCK-kaf-action").
		SetRequesterID(actorID).SetTenantID(task.TenantID).Save(ctx)
	require.NoError(t, err)
	require.NoError(t, svc.client.ProcessInstance.UpdateOneID(task.ProcessInstanceID).SetBusinessID(workItem.ID).Exec(ctx))
	return workItem.ID
}

func TestExecuteAction_AuditReferencesAppliedLedger(t *testing.T) {
	_, svc, task, ctx := newKafActionFixture(t)
	req := validCompleteRequest(task, "run-1", "finish")
	engine := &statefulKafCompletionEngine{client: svc.client}

	result, err := svc.ExecuteAction(ctx, task.TaskID, req, engine)
	require.NoError(t, err)
	assert.Equal(t, KafActionApplied, result.ResultStatus)
	ledger, err := svc.client.KafTaskActionLedger.Query().Where(
		kaftaskactionledger.TenantIDEQ(task.TenantID), kaftaskactionledger.IdempotencyKeyEQ(req.Execution.IdempotencyKey),
	).Only(ctx)
	require.NoError(t, err)
	audit, err := svc.client.AuditLog.Query().Where(
		auditlog.TenantIDEQ(task.TenantID), auditlog.ActionEQ("kaf_delegate."+req.Action),
	).Only(ctx)
	require.NoError(t, err)
	require.NotNil(t, audit.RequestBody)
	var body map[string]interface{}
	require.NoError(t, json.Unmarshal([]byte(*audit.RequestBody), &body))
	assert.Equal(t, float64(ledger.ID), body["ledgerId"])
	assert.Equal(t, task.TaskID, body["taskId"])
	assert.Equal(t, req.Execution.CorrelationID, body["correlationId"])
	assert.Equal(t, req.Execution.ProcedureRef, body["procedureRef"])
	assert.Equal(t, req.Execution.ProcedureVersion, body["procedureVersion"])
	assert.Equal(t, KafActionApplied, body["resultStatus"])
}

func TestExecuteAction_RejectsSameScopeWithDifferentKey(t *testing.T) {
	_, svc, task, ctx := newKafActionFixture(t)
	workItemID := attachKafActionWorkItem(t, svc, task, ctx)
	actorID, _ := ctx.Value(bpmn.BPMNUserIDContextKey).(int)
	engine := &statefulKafCompletionEngine{
		client: svc.client,
		onComplete: func(callbackCtx context.Context) error {
			return svc.client.TicketComment.Create().SetTicketID(workItemID).SetUserID(actorID).
				SetContent("KAF canonical action applied").SetIsInternal(true).SetTenantID(task.TenantID).Exec(callbackCtx)
		},
	}
	_, err := svc.ExecuteAction(ctx, task.TaskID, validCompleteRequest(task, "run-1", "finish"), engine)
	require.NoError(t, err)

	conflicting := validCompleteRequest(task, "run-1", "finish")
	conflicting.Execution.IdempotencyKey = "wrong-key"
	_, err = svc.ExecuteAction(ctx, task.TaskID, conflicting, engine)
	require.ErrorIs(t, err, ErrKafActionInvalid)
	assert.Equal(t, 1, engine.calls)
	ledgerCount, err := svc.client.KafTaskActionLedger.Query().Where(
		kaftaskactionledger.TenantIDEQ(task.TenantID), kaftaskactionledger.TaskIDEQ(task.TaskID),
	).Count(ctx)
	require.NoError(t, err)
	assert.Equal(t, 1, ledgerCount)
	auditCount, err := svc.client.AuditLog.Query().Where(
		auditlog.TenantIDEQ(task.TenantID), auditlog.ActionEQ("kaf_delegate."+conflicting.Action),
	).Count(ctx)
	require.NoError(t, err)
	assert.Equal(t, 1, auditCount)
	commentCount, err := svc.client.TicketComment.Query().Where(ticketcomment.TicketIDEQ(workItemID)).Count(ctx)
	require.NoError(t, err)
	assert.Equal(t, 1, commentCount)
	completedCount, err := svc.client.ProcessTask.Query().Where(
		processtask.IDEQ(task.ID), processtask.StatusEQ(common.ProcessTaskStatusCompleted),
	).Count(ctx)
	require.NoError(t, err)
	assert.Equal(t, 1, completedCount)
}

func TestReconcileTaskOnlyCompletion_DoesNotReportApplied(t *testing.T) {
	engine, svc, task, ctx := newKafActionFixture(t)
	req := validCompleteRequest(task, "run-1", "finish")
	completedVariables := cloneKafVariables(task.TaskVariables)
	completedVariables[bpmnMetaDataServiceTaskType] = "kaf_scope_capture"
	completedVariables["kaf_execution"] = map[string]string{
		"run_id": "run-1", "step_id": "finish", "procedure_ref": "ssl-vpn", "procedure_version": "v1",
	}
	require.NoError(t, svc.client.ProcessTask.UpdateOneID(task.ID).
		SetStatus(common.ProcessTaskStatusCompleted).
		SetTaskVariables(completedVariables).
		Exec(ctx))
	ledger, err := svc.client.KafTaskActionLedger.Create().
		SetTenantID(task.TenantID).SetTaskID(task.TaskID).
		SetRunID(req.Execution.RunID).SetStepID(req.Execution.StepID).
		SetAction(req.Action).SetIdempotencyKey(req.Execution.IdempotencyKey).
		SetCorrelationID(req.Execution.CorrelationID).
		SetProcedureRef(req.Execution.ProcedureRef).SetProcedureVersion(req.Execution.ProcedureVersion).
		SetResultStatus("failed_retryable").
		Save(ctx)
	require.NoError(t, err)
	_, err = svc.client.KafTaskCompletionReceipt.Create().
		SetLedgerID(ledger.ID).SetTenantID(task.TenantID).SetTaskID(task.TaskID).
		SetStatus("callback_pending").
		Save(ctx)
	require.NoError(t, err)
	handler := &scopeCapturingKafCallbackHandler{}
	engine.callbackRegistry.RegisterHandler(handler)
	completionAttempts := 0
	svc.client.ProcessTask.Use(func(next ent.Mutator) ent.Mutator {
		return ent.MutateFunc(func(hookCtx context.Context, mutation ent.Mutation) (ent.Value, error) {
			if processMutation, ok := mutation.(*ent.ProcessTaskMutation); ok {
				if status, exists := processMutation.Status(); exists && status == common.ProcessTaskStatusCompleted {
					completionAttempts++
				}
			}
			return next.Mutate(hookCtx, mutation)
		})
	})

	result, err := svc.ExecuteAction(ctx, task.TaskID, req, engine)
	require.ErrorContains(t, err, "durable successor or terminal process")
	assert.Nil(t, result)
	assert.Zero(t, completionAttempts)
	assert.Zero(t, handler.calls)
	receipt, err := svc.client.KafTaskCompletionReceipt.Query().Where(
		kaftaskcompletionreceipt.LedgerIDEQ(ledger.ID),
	).Only(ctx)
	require.NoError(t, err)
	assert.Equal(t, "callback_pending", receipt.Status)
	ledger, err = svc.client.KafTaskActionLedger.Get(ctx, ledger.ID)
	require.NoError(t, err)
	assert.Equal(t, "failed_retryable", ledger.ResultStatus)
}

func assertStrandedKafActivityDoesNotReconcile(t *testing.T, activityID string) {
	t.Helper()
	engine, svc, task, ctx := newKafActionFixture(t)
	req := validCompleteRequest(task, "run-stranded", "finish")
	completedVariables := cloneKafVariables(task.TaskVariables)
	completedVariables["kaf_execution"] = map[string]string{
		"run_id": req.Execution.RunID, "step_id": req.Execution.StepID,
		"procedure_ref": req.Execution.ProcedureRef, "procedure_version": req.Execution.ProcedureVersion,
	}
	require.NoError(t, svc.client.ProcessTask.UpdateOneID(task.ID).
		SetStatus(common.ProcessTaskStatusCompleted).
		SetTaskVariables(completedVariables).
		Exec(ctx))
	require.NoError(t, svc.client.ProcessInstance.UpdateOneID(task.ProcessInstanceID).
		SetStatus("running").
		SetCurrentActivityID(activityID).
		Exec(ctx))
	ledger, err := svc.client.KafTaskActionLedger.Create().
		SetTenantID(task.TenantID).SetTaskID(task.TaskID).
		SetRunID(req.Execution.RunID).SetStepID(req.Execution.StepID).
		SetAction(req.Action).SetIdempotencyKey(req.Execution.IdempotencyKey).
		SetCorrelationID(req.Execution.CorrelationID).
		SetProcedureRef(req.Execution.ProcedureRef).SetProcedureVersion(req.Execution.ProcedureVersion).
		SetResultStatus("failed_retryable").
		Save(ctx)
	require.NoError(t, err)
	_, err = svc.client.KafTaskCompletionReceipt.Create().
		SetLedgerID(ledger.ID).SetTenantID(task.TenantID).SetTaskID(task.TaskID).
		SetStatus("callback_succeeded").
		Save(ctx)
	require.NoError(t, err)

	result, err := svc.ExecuteAction(ctx, task.TaskID, req, engine)
	require.ErrorContains(t, err, "durable successor or terminal process")
	assert.Nil(t, result)
	ledger, err = svc.client.KafTaskActionLedger.Get(ctx, ledger.ID)
	require.NoError(t, err)
	assert.Equal(t, "failed_retryable", ledger.ResultStatus)
}

func TestReconcileActivityWrittenWithoutSuccessor_DoesNotReportApplied(t *testing.T) {
	assertStrandedKafActivityDoesNotReconcile(t, "Activity_Successor")
}

func TestReconcileEndActivityWrittenWithoutTerminalProcess_DoesNotReportApplied(t *testing.T) {
	assertStrandedKafActivityDoesNotReconcile(t, "End")
}

func TestExecuteAction_RealEngineCallbackFailureRecoversWithoutSecondBPMNCompletion(t *testing.T) {
	engine, svc, task, ctx := newKafActionFixture(t)
	workItemID := attachKafActionWorkItem(t, svc, task, ctx)
	actorID, _ := ctx.Value(bpmn.BPMNUserIDContextKey).(int)
	req := validCompleteRequest(task, "run-callback-recovery", "finish")

	instance, err := svc.client.ProcessInstance.Get(ctx, task.ProcessInstanceID)
	require.NoError(t, err)
	const callbackRecoveryBPMN = `<?xml version="1.0" encoding="UTF-8"?>
<bpmn:definitions xmlns:bpmn="http://www.omg.org/spec/BPMN/20100524/MODEL" targetNamespace="https://itsm.example.test/bpmn">
  <bpmn:process id="kaf_delegation_flow" name="KAF callback recovery" isExecutable="true">
    <bpmn:startEvent id="Start" />
    <bpmn:serviceTask id="Activity_KafDelegate" name="KAF delegated task" />
    <bpmn:endEvent id="End" />
    <bpmn:sequenceFlow id="Flow_Start_Kaf" sourceRef="Start" targetRef="Activity_KafDelegate" />
    <bpmn:sequenceFlow id="Flow_Kaf_End" sourceRef="Activity_KafDelegate" targetRef="End" />
  </bpmn:process>
</bpmn:definitions>`
	require.NoError(t, svc.client.ProcessDefinition.UpdateOneID(instance.ProcessDefinitionID).
		SetBpmnXML([]byte(callbackRecoveryBPMN)).
		Exec(ctx))

	handler := &failOncePersistingKafCallbackHandler{
		client: svc.client, workItemID: workItemID, actorID: actorID,
	}
	engine.callbackRegistry.RegisterHandler(handler)
	task, err = svc.client.ProcessTask.UpdateOneID(task.ID).
		SetTaskVariables(map[string]interface{}{
			bpmnMetaDataAllowedActions:  kafActionComplete,
			bpmnMetaDataServiceTaskType: handler.GetTaskType(),
			bpmnMetaDataAction:          "complete",
		}).
		Save(ctx)
	require.NoError(t, err)

	completionWrites := 0
	svc.client.ProcessTask.Use(func(next ent.Mutator) ent.Mutator {
		return ent.MutateFunc(func(hookCtx context.Context, mutation ent.Mutation) (ent.Value, error) {
			if taskMutation, ok := mutation.(*ent.ProcessTaskMutation); ok {
				if status, exists := taskMutation.Status(); exists && status == common.ProcessTaskStatusCompleted {
					completionWrites++
				}
			}
			return next.Mutate(hookCtx, mutation)
		})
	})

	_, err = svc.ExecuteAction(ctx, task.TaskID, req, engine)
	require.ErrorContains(t, err, "user task callback failed")
	failedReceipt, err := svc.client.KafTaskCompletionReceipt.Query().Where(
		kaftaskcompletionreceipt.TaskIDEQ(task.TaskID),
	).Only(ctx)
	require.NoError(t, err)
	assert.Equal(t, "callback_failed", failedReceipt.Status)
	assert.Equal(t, "callback_failed", failedReceipt.ErrorCode)
	assert.Equal(t, 1, completionWrites)
	assert.Equal(t, 1, handler.calls)
	commentCount, err := svc.client.TicketComment.Query().Where(
		ticketcomment.TicketIDEQ(workItemID),
	).Count(ctx)
	require.NoError(t, err)
	assert.Equal(t, 1, commentCount)
	auditCount, err := svc.client.AuditLog.Query().Where(
		auditlog.TenantIDEQ(task.TenantID),
		auditlog.ActionEQ("kaf_delegate."+req.Action),
	).Count(ctx)
	require.NoError(t, err)
	assert.Zero(t, auditCount)

	result, err := svc.ExecuteAction(ctx, task.TaskID, req, engine)
	require.NoError(t, err)
	assert.Equal(t, KafActionApplied, result.ResultStatus)
	assert.Equal(t, 2, handler.calls)
	require.True(t, handler.scopeOK)
	assert.Equal(t, req.Execution.IdempotencyKey, handler.scope.IdempotencyKey())
	assert.Equal(t, 1, completionWrites)
	completedCount, err := svc.client.ProcessTask.Query().Where(
		processtask.IDEQ(task.ID), processtask.StatusEQ(common.ProcessTaskStatusCompleted),
	).Count(ctx)
	require.NoError(t, err)
	assert.Equal(t, 1, completedCount)
	receiptCount, err := svc.client.KafTaskCompletionReceipt.Query().Where(
		kaftaskcompletionreceipt.TaskIDEQ(task.TaskID),
		kaftaskcompletionreceipt.StatusEQ("callback_succeeded"),
	).Count(ctx)
	require.NoError(t, err)
	assert.Equal(t, 1, receiptCount)
	commentCount, err = svc.client.TicketComment.Query().Where(
		ticketcomment.TicketIDEQ(workItemID),
	).Count(ctx)
	require.NoError(t, err)
	assert.Equal(t, 1, commentCount)
	auditCount, err = svc.client.AuditLog.Query().Where(
		auditlog.TenantIDEQ(task.TenantID),
		auditlog.ActionEQ("kaf_delegate."+req.Action),
	).Count(ctx)
	require.NoError(t, err)
	assert.Equal(t, 1, auditCount)
	completedInstanceCount, err := svc.client.ProcessInstance.Query().Where(
		processinstance.IDEQ(instance.ID),
		processinstance.StatusEQ("completed"),
	).Count(ctx)
	require.NoError(t, err)
	assert.Equal(t, 1, completedInstanceCount)

	replay, err := svc.ExecuteAction(ctx, task.TaskID, req, engine)
	require.NoError(t, err)
	assert.Equal(t, KafActionAlreadyApplied, replay.ResultStatus)
	assert.Equal(t, 2, handler.calls)
	assert.Equal(t, 1, completionWrites)
}
