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

type recoveringKafCallbackEngine struct {
	ProcessEngine
	client           *ent.Client
	workItemID       int
	actorID          int
	callbackCalls    int
	completionWrites int
}

type scopeCapturingKafCallbackHandler struct {
	scope   bpmn.KafActionScope
	scopeOK bool
	calls   int
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

func (e *statefulKafCompletionEngine) CompleteTask(ctx context.Context, taskID string, variables map[string]interface{}) error {
	e.calls++
	tenantID, _ := ctx.Value(bpmn.BPMNTenantIDContextKey).(int)
	updated, err := e.client.ProcessTask.Update().Where(
		processtask.TaskIDEQ(taskID), processtask.TenantIDEQ(tenantID),
	).SetStatus(common.ProcessTaskStatusCompleted).SetTaskVariables(variables).Save(ctx)
	if err != nil {
		return err
	}
	if updated != 1 {
		return errors.New("stateful KAF engine could not complete task")
	}
	return nil
}

func (e *statefulKafCompletionEngine) CompleteKafDelegatedTask(ctx context.Context, ledgerID int, taskID string, variables map[string]interface{}) error {
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

func (e *blockingKafCompletionEngine) CompleteKafDelegatedTask(ctx context.Context, ledgerID int, taskID string, variables map[string]interface{}) error {
	e.entered <- struct{}{}
	<-e.release
	return e.statefulKafCompletionEngine.CompleteKafDelegatedTask(ctx, ledgerID, taskID, variables)
}

func (e *recoveringKafCallbackEngine) CompleteTask(context.Context, string, map[string]interface{}) error {
	return errors.New("generic completion must not be used for KAF callback recovery")
}

func (e *recoveringKafCallbackEngine) CompleteKafDelegatedTask(ctx context.Context, ledgerID int, taskID string, variables map[string]interface{}) error {
	e.callbackCalls++
	if e.callbackCalls == 1 {
		e.completionWrites++
		updated, err := e.client.ProcessTask.Update().Where(
			processtask.TaskIDEQ(taskID),
			processtask.StatusNEQ(common.ProcessTaskStatusCompleted),
		).SetStatus(common.ProcessTaskStatusCompleted).SetTaskVariables(variables).Save(ctx)
		if err != nil {
			return err
		}
		if updated != 1 {
			return errors.New("KAF callback fixture could not complete task")
		}
		ledger, err := e.client.KafTaskActionLedger.Get(ctx, ledgerID)
		if err != nil {
			return err
		}
		_, err = e.client.KafTaskCompletionReceipt.Create().
			SetLedgerID(ledgerID).SetTenantID(ledger.TenantID).SetTaskID(taskID).
			SetStatus("callback_failed").SetErrorCode("callback_failed").
			Save(ctx)
		if err != nil {
			return err
		}
		return errors.New("forced callback failure")
	}
	receipt, err := e.client.KafTaskCompletionReceipt.Query().Where(
		kaftaskcompletionreceipt.LedgerIDEQ(ledgerID),
	).Only(ctx)
	if err != nil {
		return err
	}
	if err := e.client.KafTaskCompletionReceipt.UpdateOneID(receipt.ID).
		SetStatus("callback_succeeded").ClearErrorCode().Exec(ctx); err != nil {
		return err
	}
	return e.client.TicketComment.Create().SetTicketID(e.workItemID).SetUserID(e.actorID).
		SetContent("KAF callback recovery applied").SetIsInternal(true).
		SetTenantID(receipt.TenantID).Exec(ctx)
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

func TestReconcileCompletedTaskWithoutSuccessfulReceipt_DoesNotCompleteBPMNAgain(t *testing.T) {
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
	require.NoError(t, err)
	assert.Equal(t, KafActionApplied, result.ResultStatus)
	assert.Zero(t, completionAttempts)
	assert.Equal(t, 1, handler.calls)
	require.True(t, handler.scopeOK)
	assert.Equal(t, ledger.ID, handler.scope.LedgerID())
	assert.Equal(t, task.TaskID, handler.scope.TaskID())
	assert.Equal(t, req.Execution.IdempotencyKey, handler.scope.IdempotencyKey())
	receipt, err := svc.client.KafTaskCompletionReceipt.Query().Where(
		kaftaskcompletionreceipt.LedgerIDEQ(ledger.ID),
	).Only(ctx)
	require.NoError(t, err)
	assert.Equal(t, "callback_succeeded", receipt.Status)
}

func TestExecuteAction_CallbackFailureRecoversWithoutSecondBPMNCompletion(t *testing.T) {
	_, svc, task, ctx := newKafActionFixture(t)
	workItemID := attachKafActionWorkItem(t, svc, task, ctx)
	actorID, _ := ctx.Value(bpmn.BPMNUserIDContextKey).(int)
	req := validCompleteRequest(task, "run-callback-recovery", "finish")
	engine := &recoveringKafCallbackEngine{
		client: svc.client, workItemID: workItemID, actorID: actorID,
	}

	_, err := svc.ExecuteAction(ctx, task.TaskID, req, engine)
	require.ErrorContains(t, err, "forced callback failure")
	failedReceipt, err := svc.client.KafTaskCompletionReceipt.Query().Where(
		kaftaskcompletionreceipt.TaskIDEQ(task.TaskID),
	).Only(ctx)
	require.NoError(t, err)
	assert.Equal(t, "callback_failed", failedReceipt.Status)
	assert.Equal(t, "callback_failed", failedReceipt.ErrorCode)

	result, err := svc.ExecuteAction(ctx, task.TaskID, req, engine)
	require.NoError(t, err)
	assert.Equal(t, KafActionApplied, result.ResultStatus)
	assert.Equal(t, 2, engine.callbackCalls)
	assert.Equal(t, 1, engine.completionWrites)
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
	assert.Equal(t, 1, auditCount)
}
