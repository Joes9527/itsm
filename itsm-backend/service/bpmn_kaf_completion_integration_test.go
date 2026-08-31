//go:build integration

package service

import (
	"context"
	"sync"
	"testing"
	"time"

	"itsm-backend/common"
	"itsm-backend/ent"
	"itsm-backend/ent/auditlog"
	"itsm-backend/ent/kaftaskactionledger"
	"itsm-backend/ent/migrate"
	"itsm-backend/ent/outboxevent"
	"itsm-backend/ent/processcallbackoutbox"
	"itsm-backend/ent/processtask"
	"itsm-backend/service/bpmn"

	"github.com/google/uuid"
	"github.com/stretchr/testify/require"
	"go.uber.org/zap"
)

const postgresKafFenceBPMN = `<?xml version="1.0" encoding="UTF-8"?>
<bpmn:definitions xmlns:bpmn="http://www.omg.org/spec/BPMN/20100524/MODEL" targetNamespace="https://itsm.example.test/bpmn">
  <bpmn:process id="postgres_kaf_fence" isExecutable="true">
    <bpmn:startEvent id="Start" />
    <bpmn:serviceTask id="Activity_Current" name="Current KAF task" />
    <bpmn:serviceTask id="Activity_Successor" name="Successor KAF task">
      <bpmn:extensionElements>
        <bpmn:metaData name="service_task_type">kaf_delegate</bpmn:metaData>
        <bpmn:metaData name="allowed_actions">complete_bpmn_task</bpmn:metaData>
      </bpmn:extensionElements>
    </bpmn:serviceTask>
    <bpmn:endEvent id="End" />
    <bpmn:sequenceFlow id="Flow_Start_Current" sourceRef="Start" targetRef="Activity_Current" />
    <bpmn:sequenceFlow id="Flow_Current_Successor" sourceRef="Activity_Current" targetRef="Activity_Successor" />
    <bpmn:sequenceFlow id="Flow_Successor_End" sourceRef="Activity_Successor" targetRef="End" />
  </bpmn:process>
</bpmn:definitions>`

type postgresKafFenceBarrier struct {
	arrived chan struct{}
	release chan struct{}
	once    sync.Once
}

func (b *postgresKafFenceBarrier) hook() ent.Hook {
	return func(next ent.Mutator) ent.Mutator {
		return ent.MutateFunc(func(ctx context.Context, mutation ent.Mutation) (ent.Value, error) {
			if _, ok := mutation.(*ent.KafTaskActionLedgerMutation); ok {
				b.once.Do(func() {
					close(b.arrived)
					select {
					case <-b.release:
					case <-ctx.Done():
					}
				})
			}
			return next.Mutate(ctx, mutation)
		})
	}
}

func TestKafCompletionFinalFenceRollsBackAllEffectsAfterPostgresLeaseReclaim(t *testing.T) {
	setupClient, setupDB := openBPMNPostgresIntegrationClient(t)
	migrateBPMNPostgresIntegrationTables(t, setupClient,
		migrate.AuditLogsTable,
		migrate.ProcessDeploymentsTable,
		migrate.ProcessDefinitionsTable,
		migrate.ProcessInstancesTable,
		migrate.ProcessTasksTable,
		migrate.ProcessCallbackOutboxesTable,
		migrate.OutboxEventsTable,
		migrate.KafTaskActionLedgersTable,
		migrate.KafTaskCompletionReceiptsTable,
	)

	ctx, cancel := context.WithTimeout(context.Background(), postgresIntegrationTimeout)
	defer cancel()
	namespace := uuid.NewString()
	tenant := createPostgresIntegrationTenant(t, setupClient, namespace+"-kaf-fence")
	t.Cleanup(func() {
		cleanupCtx, cleanupCancel := context.WithTimeout(context.Background(), postgresIntegrationTimeout)
		defer cleanupCancel()
		for _, statement := range []string{
			"DELETE FROM kaf_task_completion_receipts WHERE tenant_id = $1",
			"DELETE FROM kaf_task_action_ledgers WHERE tenant_id = $1",
			"DELETE FROM process_callback_outboxes WHERE tenant_id = $1",
			"DELETE FROM outbox_events WHERE tenant_id = $1",
			"DELETE FROM audit_logs WHERE tenant_id = $1",
			"DELETE FROM process_tasks WHERE tenant_id = $1",
			"DELETE FROM process_instances WHERE tenant_id = $1",
			"DELETE FROM process_definitions WHERE tenant_id = $1",
			"DELETE FROM process_deployments WHERE tenant_id = $1",
			"DELETE FROM users WHERE tenant_id = $1",
			"DELETE FROM tenants WHERE id = $1",
		} {
			_, err := setupDB.ExecContext(cleanupCtx, statement, tenant.ID)
			require.NoError(t, err)
		}
	})

	actor := setupClient.User.Create().
		SetUsername("kaf-fence-" + namespace).
		SetEmail("kaf-fence-" + namespace + "@example.test").
		SetName("PostgreSQL KAF fence actor").
		SetPasswordHash("integration-test-only").
		SetRole(kafAutomationRole).
		SetActive(true).
		SetTenantID(tenant.ID).
		SaveX(ctx)
	deployment := setupClient.ProcessDeployment.Create().
		SetDeploymentID("deployment-" + namespace).
		SetDeploymentName("PostgreSQL KAF completion fence").
		SetTenantID(tenant.ID).
		SaveX(ctx)
	definition := setupClient.ProcessDefinition.Create().
		SetKey("kaf-fence-" + namespace).
		SetName("PostgreSQL KAF completion fence").
		SetVersion("1").
		SetIsLatest(true).
		SetBpmnXML([]byte(postgresKafFenceBPMN)).
		SetDeploymentID(deployment.ID).
		SetTenantID(tenant.ID).
		SaveX(ctx)
	instance := setupClient.ProcessInstance.Create().
		SetProcessInstanceID("instance-" + namespace).
		SetProcessDefinitionKey(definition.Key).
		SetProcessDefinitionID(definition.ID).
		SetBusinessKey("incident:42").
		SetBusinessType("incident").
		SetBusinessID(42).
		SetStatus("running").
		SetCurrentActivityID("Activity_Current").
		SetVersion(1).
		SetTenantID(tenant.ID).
		SaveX(ctx)
	task := setupClient.ProcessTask.Create().
		SetTaskID("task-" + namespace).
		SetProcessInstanceID(instance.ID).
		SetProcessDefinitionKey(definition.Key).
		SetTaskDefinitionKey("Activity_Current").
		SetTaskName("Current KAF task").
		SetTaskType(bpmn.KafDelegateTaskType).
		SetStatus(common.ProcessTaskStatusDelegated).
		SetTaskVariables(map[string]interface{}{bpmnMetaDataAllowedActions: kafActionComplete}).
		SetCallbackHandlerID("postgres_kaf_callback_handler").
		SetCallbackTaskType("postgres_kaf_callback").
		SetCallbackAction(kafActionComplete).
		SetCorrelationID("correlation-" + namespace).
		SetTenantID(tenant.ID).
		SaveX(ctx)
	ledger := setupClient.KafTaskActionLedger.Create().
		SetTenantID(tenant.ID).SetTaskID(task.TaskID).
		SetRunID("run-fence").SetStepID("finish").SetAction(kafActionComplete).
		SetIdempotencyKey(tenant.Code + ":fence").SetCorrelationID(task.CorrelationID).
		SetProcedureRef("ssl-vpn").SetProcedureVersion("v1").
		SetResultStatus("executing").SetLeaseOwner("owner-before-reclaim").
		SetLeaseExpiresAt(time.Now().Add(time.Minute)).
		SaveX(ctx)
	setupClient.KafTaskCompletionReceipt.Create().
		SetLedgerID(ledger.ID).SetTenantID(tenant.ID).SetTaskID(task.TaskID).
		SetStatus("callback_pending").
		SaveX(ctx)

	clientA, _ := openBPMNPostgresIntegrationClient(t)
	clientB, _ := openBPMNPostgresIntegrationClient(t)
	barrier := &postgresKafFenceBarrier{arrived: make(chan struct{}), release: make(chan struct{})}
	clientA.KafTaskActionLedger.Use(barrier.hook())
	engine := NewCustomProcessEngine(clientA, zap.NewNop().Sugar()).(*CustomProcessEngine)
	engine.CallbackRegistry().RegisterHandler(&failingUserTaskCallbackHandler{
		taskType: "postgres_kaf_callback", handlerID: "postgres_kaf_callback_handler",
	})
	engine.CallbackRegistry().RegisterHandler(&fakeAsyncServiceTaskHandler{
		taskType: bpmn.KafDelegateTaskType, handlerID: "kaf_delegate_handler",
	})
	actionCtx := context.WithValue(ctx, bpmn.BPMNTenantIDContextKey, tenant.ID)
	actionCtx = context.WithValue(actionCtx, bpmn.BPMNUserIDContextKey, actor.ID)
	actionCtx = WithBPMNAccessScope(actionCtx, BPMNAccessScope{UserID: actor.ID, TenantID: tenant.ID})

	completionResult := make(chan error, 1)
	go func() {
		completionResult <- engine.CompleteKafDelegatedTask(
			actionCtx, ledger.ID, ledger.LeaseOwner, task.TaskID, map[string]interface{}{"result": "done"},
		)
	}()
	select {
	case <-barrier.arrived:
	case err := <-completionResult:
		t.Fatalf("completion returned before final lease fence: %v", err)
	case <-ctx.Done():
		t.Fatal("completion did not reach final lease fence")
	}

	updated, err := clientB.KafTaskActionLedger.Update().Where(
		kaftaskactionledger.IDEQ(ledger.ID),
		kaftaskactionledger.LeaseOwnerEQ(ledger.LeaseOwner),
	).SetLeaseOwner("owner-after-reclaim").SetLeaseExpiresAt(time.Now().Add(time.Minute)).Save(ctx)
	require.NoError(t, err)
	require.Equal(t, 1, updated)
	close(barrier.release)
	select {
	case err := <-completionResult:
		require.ErrorContains(t, err, "lease owner")
	case <-ctx.Done():
		t.Fatal("completion did not return after lease reclaim")
	}

	persistedTask := setupClient.ProcessTask.GetX(ctx, task.ID)
	require.Equal(t, common.ProcessTaskStatusDelegated, persistedTask.Status)
	persistedInstance := setupClient.ProcessInstance.GetX(ctx, instance.ID)
	require.Equal(t, "Activity_Current", persistedInstance.CurrentActivityID)
	require.Equal(t, 1, persistedInstance.Version)
	require.Zero(t, setupClient.ProcessTask.Query().Where(
		processtask.ProcessInstanceIDEQ(instance.ID),
		processtask.TaskDefinitionKeyEQ("Activity_Successor"),
	).CountX(ctx))
	require.Zero(t, setupClient.ProcessCallbackOutbox.Query().Where(
		processcallbackoutbox.TenantIDEQ(tenant.ID),
	).CountX(ctx))
	require.Zero(t, setupClient.OutboxEvent.Query().Where(
		outboxevent.TenantIDEQ(tenant.ID),
	).CountX(ctx))
	require.Zero(t, setupClient.AuditLog.Query().Where(
		auditlog.TenantIDEQ(tenant.ID), auditlog.ActionEQ("kaf_delegate.created"),
	).CountX(ctx))
	reclaimed := setupClient.KafTaskActionLedger.GetX(ctx, ledger.ID)
	require.Equal(t, "owner-after-reclaim", reclaimed.LeaseOwner)
}
