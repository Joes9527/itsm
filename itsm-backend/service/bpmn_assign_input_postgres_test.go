//go:build integration_postgres

package service

import (
	"context"
	"fmt"
	"net/url"
	"os"
	"sync"
	"testing"
	"time"

	_ "github.com/lib/pq"
	"github.com/stretchr/testify/require"
	"itsm-backend/ent"
)

// a1AssignUserTaskBPMNTemplate is the shape the Dev incident used: a UserTask
// whose ticket_task/assign callback takes its target from the actor.
const a1AssignUserTaskBPMNTemplate = `<?xml version="1.0" encoding="UTF-8"?>
<bpmn:definitions xmlns:bpmn="http://www.omg.org/spec/BPMN/20100524/MODEL" targetNamespace="http://bpmn.io/schema/bpmn">
  <bpmn:process id="%s" isExecutable="true">
    <bpmn:startEvent id="StartEvent_1"><bpmn:outgoing>Flow_1</bpmn:outgoing></bpmn:startEvent>
    <bpmn:userTask id="Activity_Assign" name="确认接单">
      <bpmn:extensionElements>
        <bpmn:metaData name="service_task_type">ticket_task</bpmn:metaData>
        <bpmn:metaData name="action">assign</bpmn:metaData>
      </bpmn:extensionElements>
      <bpmn:incoming>Flow_1</bpmn:incoming>
      <bpmn:outgoing>Flow_2</bpmn:outgoing>
    </bpmn:userTask>
    <bpmn:endEvent id="EndEvent_1"><bpmn:incoming>Flow_2</bpmn:incoming></bpmn:endEvent>
    <bpmn:sequenceFlow id="Flow_1" sourceRef="StartEvent_1" targetRef="Activity_Assign" />
    <bpmn:sequenceFlow id="Flow_2" sourceRef="Activity_Assign" targetRef="EndEvent_1" />
  </bpmn:process>
</bpmn:definitions>`

// newA1PostgresClient opens the isolated disposable Postgres used by the
// integration suite, in its own schema so the test cannot touch another target.
func newA1PostgresClient(t *testing.T) *ent.Client {
	t.Helper()
	parsed, err := url.Parse(os.Getenv("INTAKE_POSTGRES_TEST_DSN"))
	require.NoError(t, err)
	require.Equal(t, "127.0.0.1:36444", parsed.Host)
	require.Equal(t, "/sslvpn_test", parsed.Path)

	schema := fmt.Sprintf("a1_assign_input_%d", time.Now().UnixNano())
	admin, err := ent.Open("postgres", parsed.String())
	require.NoError(t, err)
	_, err = admin.ExecContext(context.Background(), "CREATE SCHEMA "+schema)
	require.NoError(t, err)
	t.Cleanup(func() {
		_, dropErr := admin.ExecContext(context.Background(), "DROP SCHEMA "+schema+" CASCADE")
		require.NoError(t, dropErr)
		require.NoError(t, admin.Close())
	})

	params := parsed.Query()
	params.Set("search_path", schema)
	parsed.RawQuery = params.Encode()
	client, err := ent.Open("postgres", parsed.String())
	require.NoError(t, err)
	require.NoError(t, client.Schema.Create(context.Background()))
	return client
}

// a1AssignFixture drives the production start path into a pending
// ticket_task/assign UserTask and returns the actor scope plus the identifiers
// the assertions need.
type a1AssignFixture struct {
	client   *ent.Client
	engine   *CustomProcessEngine
	fixture  *bpmnAuthorizationFixture
	actorCtx context.Context
	instance *ent.ProcessInstance
	task     *ent.ProcessTask
	item     *ent.Ticket
}

func newA1AssignFixture(t *testing.T) *a1AssignFixture {
	t.Helper()
	client := newA1PostgresClient(t)
	f := newBPMNAuthorizationFixtureWithClient(t, client)
	ctx := context.Background()

	f.definition = client.ProcessDefinition.UpdateOneID(f.definition.ID).
		SetBpmnXML([]byte(fmt.Sprintf(a1AssignUserTaskBPMNTemplate, f.definition.Key))).
		SaveX(ctx)

	requester := client.User.Create().
		SetUsername("a1-requester").SetEmail("a1-requester@example.test").SetName("A1 requester").
		SetPasswordHash("x").SetActive(true).SetTenantID(f.tenant.ID).SaveX(ctx)
	item := client.Ticket.Create().
		SetTitle("A1 assign input gate").SetTicketNumber("TKT-A1-000001").
		SetRecordClass("generic").SetStatus("open").
		SetRequesterID(requester.ID).SetOpenedByID(f.actor.ID).SetTenantID(f.tenant.ID).SaveX(ctx)

	actorCtx := WithBPMNAccessScope(ctx, BPMNAccessScope{UserID: f.actor.ID, TenantID: f.tenant.ID, CanUpdateAllTasks: true})
	instance, err := f.engine.StartProcess(actorCtx, f.definition.Key, fmt.Sprintf("generic:%d", item.ID), "generic", item.ID,
		map[string]interface{}{"business_id": item.ID})
	require.NoError(t, err)
	task := findTaskByDefinitionKey(t, client, actorCtx, instance.ID, "Activity_Assign")
	require.Zero(t, client.ProcessCallbackOutbox.Query().CountX(actorCtx), "the fixture starts with no callback")

	return &a1AssignFixture{
		client: client, engine: f.engine, fixture: f, actorCtx: actorCtx,
		instance: instance, task: task, item: item,
	}
}

// assertNoPartialEffect proves a rejected completion left no trace at all.
func (fx *a1AssignFixture) assertNoPartialEffect(t *testing.T, taskStatus, instanceStatus string) {
	t.Helper()
	require.Equal(t, taskStatus, fx.client.ProcessTask.GetX(fx.actorCtx, fx.task.ID).Status,
		"a rejected completion must leave the task in its original state")
	require.Zero(t, fx.client.ProcessCallbackOutbox.Query().CountX(fx.actorCtx),
		"a rejected completion must not persist a durable callback")
	require.Equal(t, instanceStatus, fx.client.ProcessInstance.GetX(fx.actorCtx, fx.instance.ID).Status,
		"a rejected completion must not advance the process instance")
	require.Equal(t, "open", fx.client.Ticket.GetX(fx.actorCtx, fx.item.ID).Status,
		"a rejected completion must not change business state")
}

// TestActorCompletionRejectsMissingAssignInputPostgres is the real-transaction
// proof for A1: the actor completion of a ticket_task/assign node without the
// value the contract binds must fail inside the completion transaction, leaving
// the task, the callback outbox and the process instance exactly as they were.
// It uses the production entry point (engine.CompleteTask), not a helper.
func TestActorCompletionRejectsMissingAssignInputPostgres(t *testing.T) {
	fx := newA1AssignFixture(t)
	taskStatusBefore := fx.task.Status
	instanceStatusBefore := fx.instance.Status

	err := fx.engine.CompleteTask(fx.actorCtx, fx.task.TaskID, map[string]interface{}{"business_id": fx.item.ID})
	require.Error(t, err, "a completion without assignee_id must be rejected instead of enqueueing")

	fx.assertNoPartialEffect(t, taskStatusBefore, instanceStatusBefore)
}

// Repeating a rejected completion must stay free of side effects, so a retrying
// client cannot accumulate callbacks or advance the instance.
func TestActorCompletionRepeatedRejectionHasNoEffectPostgres(t *testing.T) {
	fx := newA1AssignFixture(t)
	taskStatusBefore := fx.task.Status
	instanceStatusBefore := fx.instance.Status

	for attempt := 1; attempt <= 3; attempt++ {
		require.Error(t, fx.engine.CompleteTask(fx.actorCtx, fx.task.TaskID, map[string]interface{}{"business_id": fx.item.ID}),
			"attempt %d must be rejected", attempt)
	}

	fx.assertNoPartialEffect(t, taskStatusBefore, instanceStatusBefore)
}

// Concurrent rejected completions of the same task must not interleave into a
// partial write: either effect must be absent entirely.
func TestActorCompletionConcurrentRejectionHasNoEffectPostgres(t *testing.T) {
	fx := newA1AssignFixture(t)
	taskStatusBefore := fx.task.Status
	instanceStatusBefore := fx.instance.Status

	var wg sync.WaitGroup
	errs := make([]error, 2)
	for i := range errs {
		wg.Add(1)
		go func(slot int) {
			defer wg.Done()
			errs[slot] = fx.engine.CompleteTask(fx.actorCtx, fx.task.TaskID, map[string]interface{}{"business_id": fx.item.ID})
		}(i)
	}
	wg.Wait()

	for slot, err := range errs {
		require.Error(t, err, "concurrent completion %d must be rejected", slot)
	}
	fx.assertNoPartialEffect(t, taskStatusBefore, instanceStatusBefore)
}

// A tenant that does not own the task must not be able to complete it, and the
// isolation failure must fail closed without touching the owner's rows.
func TestActorCompletionCrossTenantIsRejectedPostgres(t *testing.T) {
	fx := newA1AssignFixture(t)
	taskStatusBefore := fx.task.Status
	instanceStatusBefore := fx.instance.Status

	otherTenantCtx := WithBPMNAccessScope(context.Background(), BPMNAccessScope{
		UserID: fx.fixture.otherActor.ID, TenantID: fx.fixture.otherTenant.ID, CanUpdateAllTasks: true,
	})

	err := fx.engine.CompleteTask(otherTenantCtx, fx.task.TaskID, map[string]interface{}{"business_id": fx.item.ID})
	require.Error(t, err, "a foreign tenant must not complete another tenant's task")

	fx.assertNoPartialEffect(t, taskStatusBefore, instanceStatusBefore)
}
