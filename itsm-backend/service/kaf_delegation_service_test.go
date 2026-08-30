package service

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"testing"
	"time"

	"itsm-backend/common"
	"itsm-backend/ent"
	"itsm-backend/ent/auditlog"
	"itsm-backend/ent/enttest"
	"itsm-backend/ent/kaftaskactionledger"
	"itsm-backend/ent/kaftaskcompletionreceipt"
	"itsm-backend/ent/outboxevent"
	"itsm-backend/ent/processtask"
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
	client *ent.Client
	calls  int
}

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
	return nil
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
	svc.client.AuditLog.Use(func(ent.Mutator) ent.Mutator {
		return ent.MutateFunc(func(context.Context, ent.Mutation) (ent.Value, error) {
			return nil, errors.New("audit storage unavailable")
		})
	})

	_, err := svc.ExecuteAction(ctx, task.TaskID, req, engine)
	require.ErrorContains(t, err, "audit storage unavailable")
	assert.Equal(t, 1, engine.calls)

	second, err := svc.ExecuteAction(ctx, task.TaskID, req, engine)
	require.NoError(t, err)

	assert.Equal(t, KafActionAlreadyApplied, second.ResultStatus)
	assert.Equal(t, 1, engine.calls)
	ledger, err := svc.client.KafTaskActionLedger.Query().Where(
		kaftaskactionledger.TenantIDEQ(task.TenantID), kaftaskactionledger.IdempotencyKeyEQ(req.Execution.IdempotencyKey),
	).Only(ctx)
	require.NoError(t, err)
	assert.Equal(t, "applied", ledger.ResultStatus)
}

func TestExecuteAction_RejectsActiveLeaseWithoutCallingEngine(t *testing.T) {
	_, svc, task, ctx := newKafActionFixture(t)
	req := validCompleteRequest(task, "run-1", "finish")
	_, claimed, err := svc.ClaimKafAction(ctx, task, req)
	require.NoError(t, err)
	require.True(t, claimed)

	engine := &statefulKafCompletionEngine{client: svc.client}
	_, err = svc.ExecuteAction(ctx, task.TaskID, req, engine)
	require.ErrorIs(t, err, ErrKafActionConflict)
	assert.Zero(t, engine.calls)
}

func TestExecuteAction_RejectsSameScopeWithDifferentKey(t *testing.T) {
	_, svc, task, ctx := newKafActionFixture(t)
	engine := &statefulKafCompletionEngine{client: svc.client}
	_, err := svc.ExecuteAction(ctx, task.TaskID, validCompleteRequest(task, "run-1", "finish"), engine)
	require.NoError(t, err)

	conflicting := validCompleteRequest(task, "run-1", "finish")
	conflicting.Execution.IdempotencyKey = "wrong-key"
	_, err = svc.ExecuteAction(ctx, task.TaskID, conflicting, engine)
	require.ErrorIs(t, err, ErrKafActionInvalid)
}

func TestReconcileCompletedTaskWithoutSuccessfulReceipt_DoesNotCompleteBPMNAgain(t *testing.T) {
	engine, svc, task, ctx := newKafActionFixture(t)
	req := validCompleteRequest(task, "run-1", "finish")
	completedVariables := cloneKafVariables(task.TaskVariables)
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

	result, err := svc.ExecuteAction(ctx, task.TaskID, req, engine)
	require.NoError(t, err)
	assert.Equal(t, KafActionAlreadyApplied, result.ResultStatus)
	receipt, err := svc.client.KafTaskCompletionReceipt.Query().Where(
		kaftaskcompletionreceipt.LedgerIDEQ(ledger.ID),
	).Only(ctx)
	require.NoError(t, err)
	assert.Equal(t, "callback_succeeded", receipt.Status)
}
