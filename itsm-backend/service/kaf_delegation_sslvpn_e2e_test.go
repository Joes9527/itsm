package service

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"itsm-backend/common"
	"itsm-backend/ent"
	"itsm-backend/ent/enttest"
	"itsm-backend/ent/incident"
	"itsm-backend/ent/outboxevent"
	"itsm-backend/ent/processapprovaldecision"
	"itsm-backend/ent/processtask"
	"itsm-backend/ent/servicerequest"
	"itsm-backend/service/bpmn"

	_ "github.com/mattn/go-sqlite3"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"go.uber.org/zap/zaptest"
)

const sslvpnDelegationBPMN = `<?xml version="1.0" encoding="UTF-8"?>
<bpmn:definitions xmlns:bpmn="http://www.omg.org/spec/BPMN/20100524/MODEL" targetNamespace="https://itsm.example.test/bpmn">
  <bpmn:process id="sslvpn_delegation" name="SSLVPN delegation" isExecutable="true">
    <bpmn:startEvent id="Start"><bpmn:outgoing>Flow_Start_Kaf</bpmn:outgoing></bpmn:startEvent>
    <bpmn:serviceTask id="Activity_KafDelegate" name="KAF SSLVPN delegation"><bpmn:incoming>Flow_Start_Kaf</bpmn:incoming><bpmn:outgoing>Flow_Kaf_End</bpmn:outgoing></bpmn:serviceTask>
    <bpmn:endEvent id="End"><bpmn:incoming>Flow_Kaf_End</bpmn:incoming></bpmn:endEvent>
    <bpmn:sequenceFlow id="Flow_Start_Kaf" sourceRef="Start" targetRef="Activity_KafDelegate" />
    <bpmn:sequenceFlow id="Flow_Kaf_End" sourceRef="Activity_KafDelegate" targetRef="End" />
  </bpmn:process>
</bpmn:definitions>`

type sslvpnDelegationFixture struct {
	client      *ent.Client
	engine      *CustomProcessEngine
	delegation  *KafDelegationService
	ctx         context.Context
	tenant      *ent.Tenant
	automation  *ent.User
	workItem    *ent.Ticket
	instance    *ent.ProcessInstance
	recordClass string
}

func TestSSLVPNRequest_ApprovalDelegationDeliveryAndCompletion(t *testing.T) {
	fx := newSSLVPNDelegationFixture(t, "service_request_item")
	approveBothSSLVPNLevels(t, fx)
	event := createAndDispatchSSLVPNDelegate(t, fx)

	context := kafSSLVPNContext(t, fx, event.TaskID)
	assert.Equal(t, "service_request_item", context.RecordClass)
	assert.Equal(t, fx.workItem.ID, context.WorkItem.ID)

	completeSSLVPNDelegate(t, fx, event.TaskID, "run-sslvpn-request", "finish")
	assertSSLVPNProcessAdvancedOnce(t, fx, event.TaskID)
	assertSSLVPNRequestExtension(t, fx)
	assertNoSensitiveSSLVPNPayload(t, event)
}

func TestSSLVPNIncident_UsesSameDelegationTransportWithoutServiceRequestConversion(t *testing.T) {
	fx := newSSLVPNDelegationFixture(t, "incident")
	event := createAndDispatchSSLVPNDelegate(t, fx)

	context := kafSSLVPNContext(t, fx, event.TaskID)
	assert.Equal(t, "incident", context.RecordClass)
	assert.Equal(t, fx.workItem.ID, context.WorkItem.ID)
	assertSSLVPNIncidentExtension(t, fx)
	assertNoSSLVPNServiceRequestExtension(t, fx)
	assertNoSensitiveSSLVPNPayload(t, event)
}

func newSSLVPNDelegationFixture(t *testing.T, recordClass string) *sslvpnDelegationFixture {
	t.Helper()
	client := enttest.Open(t, "sqlite3", "file:kaf_delegation_sslvpn_e2e?mode=memory&cache=shared&_fk=1")
	t.Cleanup(func() { client.Close() })
	ctx := context.Background()

	tenant, err := client.Tenant.Create().SetName("SSLVPN Tenant").SetCode("sslvpn-kaf").SetDomain("sslvpn.example.test").SetStatus("active").Save(ctx)
	require.NoError(t, err)
	requester, err := client.User.Create().SetUsername("sslvpn-requester").SetEmail("requester@sslvpn.example.test").SetName("SSLVPN Requester").SetPasswordHash("hash").SetRole("end_user").SetActive(true).SetTenantID(tenant.ID).Save(ctx)
	require.NoError(t, err)
	automation, err := client.User.Create().SetUsername("kaf-automation").SetEmail("kaf@sslvpn.example.test").SetName("KAF Automation").SetPasswordHash("hash").SetRole(kafAutomationRole).SetActive(true).SetTenantID(tenant.ID).Save(ctx)
	require.NoError(t, err)

	workItem, err := client.Ticket.Create().SetTitle("SSLVPN access request").SetDescription("VPN profile details must stay in ITSM").SetTicketNumber("TCK-SSLVPN-" + recordClass).SetType(recordClass).SetRecordClass(recordClass).SetRequesterID(requester.ID).SetTenantID(tenant.ID).Save(ctx)
	require.NoError(t, err)
	if recordClass == "service_request_item" {
		_, err = client.ServiceRequest.Create().SetTenantID(tenant.ID).SetTicketID(workItem.ID).SetCatalogID(1).SetRequesterID(requester.ID).SetComplianceAck(true).Save(ctx)
		require.NoError(t, err)
	} else {
		_, err = client.Incident.Create().SetTitle("SSLVPN connection unavailable").SetDescription("VPN client cannot establish a connection").SetStatus("new").SetPriority("high").SetSeverity("high").SetIncidentNumber("INC-SSLVPN-1").SetReporterID(requester.ID).SetWorkItemID(workItem.ID).SetTenantID(tenant.ID).Save(ctx)
		require.NoError(t, err)
	}

	deployment, err := client.ProcessDeployment.Create().SetDeploymentID("DEP-SSLVPN-" + recordClass).SetDeploymentName("SSLVPN KAF deployment").SetDeploymentTime(time.Now()).SetDeployedBy("test").SetIsActive(true).SetTenantID(tenant.ID).Save(ctx)
	require.NoError(t, err)
	definition, err := client.ProcessDefinition.Create().SetKey("sslvpn_kaf_" + recordClass).SetName("SSLVPN KAF flow").SetVersion("1").SetIsLatest(true).SetBpmnXML([]byte(sslvpnDelegationBPMN)).SetDeploymentID(deployment.ID).SetDeployedAt(time.Now()).SetTenantID(tenant.ID).Save(ctx)
	require.NoError(t, err)
	businessType := "service_request"
	if recordClass == "incident" {
		businessType = "incident"
	}
	instance, err := client.ProcessInstance.Create().SetProcessInstanceID("PI-SSLVPN-" + recordClass).SetProcessDefinitionKey(definition.Key).SetProcessDefinitionID(definition.ID).SetBusinessKey(businessType + ":" + workItem.TicketNumber).SetBusinessType(businessType).SetBusinessID(workItem.ID).SetStatus("running").SetCurrentActivityID("Activity_KafDelegate").SetCurrentActivityName("KAF SSLVPN delegation").SetTenantID(tenant.ID).Save(ctx)
	require.NoError(t, err)

	workflowCtx := context.WithValue(ctx, bpmn.BPMNTenantIDContextKey, tenant.ID)
	workflowCtx = context.WithValue(workflowCtx, bpmn.BPMNUserIDContextKey, automation.ID)
	engine, ok := NewCustomProcessEngine(client, zaptest.NewLogger(t).Sugar()).(*CustomProcessEngine)
	require.True(t, ok)
	return &sslvpnDelegationFixture{client: client, engine: engine, delegation: engine.kafDelegationService, ctx: workflowCtx, tenant: tenant, automation: automation, workItem: workItem, instance: instance, recordClass: recordClass}
}

func approveBothSSLVPNLevels(t *testing.T, fx *sslvpnDelegationFixture) {
	t.Helper()
	for level := 1; level <= 2; level++ {
		task, err := fx.client.ProcessTask.Create().SetTaskID("TASK-SSLVPN-APPROVAL-" + string(rune('0'+level))).SetProcessInstanceID(fx.instance.ID).SetProcessDefinitionKey(fx.instance.ProcessDefinitionKey).SetTaskDefinitionKey("Approval_" + string(rune('0'+level))).SetTaskName("SSLVPN approval").SetTaskType("user_task").SetStatus("completed").SetTaskVariables(map[string]interface{}{"decision": "approved"}).SetTenantID(fx.tenant.ID).Save(fx.ctx)
		require.NoError(t, err)
		_, err = fx.client.ProcessApprovalDecision.Create().SetProcessInstanceID(fx.instance.ID).SetProcessTaskID(task.ID).SetProcessInstanceKey(fx.instance.ProcessInstanceID).SetTaskID(task.TaskID).SetProcessDefinitionKey(fx.instance.ProcessDefinitionKey).SetNodeKey(task.TaskDefinitionKey).SetBusinessType("service_request").SetBusinessID(fx.workItem.TicketNumber).SetActorID(fx.automation.ID).SetActorName(fx.automation.Name).SetAction("approve").SetDecision("approved").SetTenantID(fx.tenant.ID).Save(fx.ctx)
		require.NoError(t, err)
	}
	count, err := fx.client.ProcessApprovalDecision.Query().Where(processapprovaldecision.ProcessInstanceIDEQ(fx.instance.ID), processapprovaldecision.DecisionEQ("approved")).Count(fx.ctx)
	require.NoError(t, err)
	assert.Equal(t, 2, count)
}

func createAndDispatchSSLVPNDelegate(t *testing.T, fx *sslvpnDelegationFixture) KafDelegateRequested {
	t.Helper()
	task, err := fx.delegation.CreateDelegatedTask(fx.ctx, fx.instance.ID, kafDelegateTask("complete_bpmn_task"))
	require.NoError(t, err)

	var received []byte
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, readErr := io.ReadAll(r.Body)
		require.NoError(t, readErr)
		received = body
		assert.Equal(t, "application/json", r.Header.Get("Content-Type"))
		assert.NotEmpty(t, r.Header.Get("X-Event-ID"))
		assert.NotEmpty(t, r.Header.Get("X-Webhook-Signature"))
		w.WriteHeader(http.StatusAccepted)
	}))
	t.Cleanup(server.Close)
	pending, err := fx.client.OutboxEvent.Query().Where(outboxevent.AggregateIDEQ(task.TaskID)).Only(fx.ctx)
	require.NoError(t, err)
	now := time.Now().UTC().Truncate(time.Millisecond)
	_, err = fx.client.OutboxEvent.UpdateOneID(pending.ID).SetNextAttemptAt(now.Add(-time.Second)).Save(fx.ctx)
	require.NoError(t, err)
	dispatcher, err := NewKafOutboxDispatcher(NewOutboxEventRepository(fx.client), KafOutboxConfig{WebhookURL: server.URL, WebhookSecret: "sslvpn-test-secret", BatchSize: 1, PollInterval: time.Second})
	require.NoError(t, err)
	dispatcher.now = func() time.Time { return now }
	require.NoError(t, dispatcher.DispatchOnce(fx.ctx))
	persisted, err := fx.client.OutboxEvent.Query().Where(outboxevent.AggregateIDEQ(task.TaskID)).Only(fx.ctx)
	require.NoError(t, err)
	require.NotEmptyf(t, received, "outbox event status=%s attempts=%d lastError=%q", persisted.Status, persisted.AttemptCount, persisted.LastError)

	var event KafDelegateRequested
	require.NoError(t, json.Unmarshal(received, &event))
	assert.Equal(t, task.TaskID, event.TaskID)
	assert.Equal(t, fx.recordClass, event.RecordClass)
	persisted, err = fx.client.OutboxEvent.Query().Where(outboxevent.EventIDEQ(event.EventID)).Only(fx.ctx)
	require.NoError(t, err)
	assert.Equal(t, outboxEventStatusPublished, persisted.Status)
	return event
}

func kafSSLVPNContext(t *testing.T, fx *sslvpnDelegationFixture, taskID string) *KafTaskContext {
	t.Helper()
	context, err := fx.delegation.GetTaskContext(fx.ctx, taskID)
	require.NoError(t, err)
	return context
}

func completeSSLVPNDelegate(t *testing.T, fx *sslvpnDelegationFixture, taskID, runID, stepID string) {
	t.Helper()
	context := kafSSLVPNContext(t, fx, taskID)
	_, err := fx.delegation.ExecuteAction(fx.ctx, taskID, KafActionRequest{Action: kafActionComplete, ExpectedVersion: context.ExpectedVersion, Execution: KafActionExecution{RunID: runID, StepID: stepID, IdempotencyKey: fx.tenant.Code + ":" + taskID + ":" + runID + ":" + stepID, CorrelationID: context.CorrelationID, ProcedureRef: "sslvpn-grant", ProcedureVersion: "test-v1"}, Payload: KafActionPayload{ResultSummary: "SSLVPN grant completed"}}, fx.engine)
	require.NoError(t, err)
}

func assertSSLVPNProcessAdvancedOnce(t *testing.T, fx *sslvpnDelegationFixture, taskID string) {
	t.Helper()
	completed, err := fx.client.ProcessTask.Query().Where(processtask.TaskIDEQ(taskID), processtask.StatusEQ("completed")).Count(fx.ctx)
	require.NoError(t, err)
	assert.Equal(t, 1, completed)
	delegated, err := fx.client.ProcessTask.Query().Where(processtask.ProcessInstanceIDEQ(fx.instance.ID), processtask.TaskTypeEQ(bpmn.KafDelegateTaskType), processtask.StatusEQ(common.ProcessTaskStatusDelegated)).Count(fx.ctx)
	require.NoError(t, err)
	assert.Zero(t, delegated)
}

func assertSSLVPNRequestExtension(t *testing.T, fx *sslvpnDelegationFixture) {
	t.Helper()
	count, err := fx.client.ServiceRequest.Query().Where(servicerequest.TicketIDEQ(fx.workItem.ID), servicerequest.TenantIDEQ(fx.tenant.ID)).Count(fx.ctx)
	require.NoError(t, err)
	assert.Equal(t, 1, count)
	assertNoSSLVPNIncidentExtension(t, fx)
}

func assertSSLVPNIncidentExtension(t *testing.T, fx *sslvpnDelegationFixture) {
	t.Helper()
	count, err := fx.client.Incident.Query().Where(incident.WorkItemIDEQ(fx.workItem.ID), incident.TenantIDEQ(fx.tenant.ID)).Count(fx.ctx)
	require.NoError(t, err)
	assert.Equal(t, 1, count)
}

func assertNoSSLVPNIncidentExtension(t *testing.T, fx *sslvpnDelegationFixture) {
	t.Helper()
	count, err := fx.client.Incident.Query().Where(incident.WorkItemIDEQ(fx.workItem.ID), incident.TenantIDEQ(fx.tenant.ID)).Count(fx.ctx)
	require.NoError(t, err)
	assert.Zero(t, count)
}

func assertNoSSLVPNServiceRequestExtension(t *testing.T, fx *sslvpnDelegationFixture) {
	t.Helper()
	count, err := fx.client.ServiceRequest.Query().Where(servicerequest.TicketIDEQ(fx.workItem.ID), servicerequest.TenantIDEQ(fx.tenant.ID)).Count(fx.ctx)
	require.NoError(t, err)
	assert.Zero(t, count)
}

func assertNoSensitiveSSLVPNPayload(t *testing.T, event KafDelegateRequested) {
	t.Helper()
	payload, err := json.Marshal(event)
	require.NoError(t, err)
	assert.NotContains(t, string(payload), "description")
	assert.NotContains(t, string(payload), "VPN profile details")
	assert.NotContains(t, string(payload), "password")
}
