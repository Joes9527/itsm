package service

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"testing"
	"time"

	"itsm-backend/ent"
	"itsm-backend/ent/auditlog"
	"itsm-backend/ent/enttest"
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
