package service_request

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"strconv"
	"testing"
	"time"

	"itsm-backend/common"
	"itsm-backend/dto"
	"itsm-backend/ent"
	"itsm-backend/ent/auditlog"
	"itsm-backend/ent/enttest"
	"itsm-backend/ent/incident"
	"itsm-backend/ent/kaftaskactionledger"
	"itsm-backend/ent/kaftaskcompletionreceipt"
	"itsm-backend/ent/outboxevent"
	"itsm-backend/ent/processapprovaldecision"
	"itsm-backend/ent/processinstance"
	"itsm-backend/ent/processtask"
	"itsm-backend/ent/servicerequest"
	"itsm-backend/ent/ticket"
	"itsm-backend/handlers/cmdb"
	"itsm-backend/handlers/service_catalog"
	itsmservice "itsm-backend/service"
	"itsm-backend/service/bpmn"

	_ "github.com/mattn/go-sqlite3"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"go.uber.org/zap/zaptest"
)

const sslvpnDelegationBPMN = `<?xml version="1.0" encoding="UTF-8"?>
<bpmn:definitions xmlns:bpmn="http://www.omg.org/spec/BPMN/20100524/MODEL" targetNamespace="https://itsm.example.test/bpmn">
  <bpmn:process id="%s" name="SSLVPN delegation" isExecutable="true">
    <bpmn:startEvent id="Start" />
    %s
    <bpmn:serviceTask id="Activity_KafDelegate" name="KAF SSLVPN delegation">
      <bpmn:extensionElements>
        <bpmn:metaData name="service_task_type">kaf_delegate</bpmn:metaData>
        <bpmn:metaData name="allowed_actions">complete_bpmn_task</bpmn:metaData>
      </bpmn:extensionElements>
    </bpmn:serviceTask>
    <bpmn:endEvent id="End" />
    %s
  </bpmn:process>
</bpmn:definitions>`

const sslvpnApprovalNodes = `<bpmn:userTask id="Approval_1" name="Manager approval" assignee="%d" taskPurpose="approval" />
    <bpmn:userTask id="Approval_2" name="Security approval" assignee="%d" taskPurpose="approval" />`

const sslvpnApprovalFlows = `<bpmn:sequenceFlow id="Flow_Start_Approval1" sourceRef="Start" targetRef="Approval_1" />
    <bpmn:sequenceFlow id="Flow_Approval1_Approval2" sourceRef="Approval_1" targetRef="Approval_2" />
    <bpmn:sequenceFlow id="Flow_Approval2_Kaf" sourceRef="Approval_2" targetRef="Activity_KafDelegate" />
    <bpmn:sequenceFlow id="Flow_Kaf_End" sourceRef="Activity_KafDelegate" targetRef="End" />`

const sslvpnIncidentFlows = `<bpmn:sequenceFlow id="Flow_Start_Kaf" sourceRef="Start" targetRef="Activity_KafDelegate" />
    <bpmn:sequenceFlow id="Flow_Kaf_End" sourceRef="Activity_KafDelegate" targetRef="End" />`

type sslvpnDelegationFixture struct {
	client     *ent.Client
	engine     itsmservice.ProcessEngine
	delegation *itsmservice.KafDelegationService
	ctx        context.Context
	tenant     *ent.Tenant
	requester  *ent.User
	approver   *ent.User
}

func TestSSLVPNRequest_ApprovalDelegationDeliveryAndCompletion(t *testing.T) {
	fx := newSSLVPNDelegationFixture(t)
	deploySSLVPNDefinition(t, fx, "sslvpn_service_request", fmt.Sprintf(sslvpnApprovalNodes, fx.approver.ID, fx.approver.ID), sslvpnApprovalFlows)

	sr := createSSLVPNServiceRequest(t, fx)
	workItem, err := fx.client.Ticket.Get(fx.ctx, sr.TicketID)
	require.NoError(t, err)
	assert.Equal(t, "service_request_item", workItem.RecordClass)
	assertExclusiveSSLVPNServiceRequestClass(t, fx, workItem.ID)

	instance := awaitSSLVPNInstance(t, fx, "ticket", workItem.ID)
	completeSSLVPNApproval(t, fx, instance, "Approval_1")
	assertNoSSLVPNDelegation(t, fx, instance)
	completeSSLVPNApproval(t, fx, instance, "Approval_2")
	assertTwoSSLVPNApprovalDecisions(t, fx, instance)
	task := assertOneSSLVPNDelegation(t, fx, instance)

	event := dispatchSSLVPNDelegate(t, fx, task)
	kafContext := kafSSLVPNContext(t, fx, event.TaskID)
	assert.Equal(t, "service_request_item", kafContext.RecordClass)
	assert.Equal(t, workItem.ID, kafContext.WorkItem.ID)
	completeSSLVPNDelegate(t, fx, event.TaskID, "run-sslvpn-request", "finish")
	assertSSLVPNProcessAdvancedOnce(t, fx, instance, event.TaskID)
	assertExclusiveSSLVPNServiceRequestClass(t, fx, workItem.ID)
	assertNoSensitiveSSLVPNPayload(t, event)
}

func TestSSLVPNKafDelegation_OneAppliedActionAdvancesBPMNOnce(t *testing.T) {
	fx := newSSLVPNDelegationFixture(t)
	deploySSLVPNDefinition(t, fx, "sslvpn_execution_integrity", fmt.Sprintf(sslvpnApprovalNodes, fx.approver.ID, fx.approver.ID), sslvpnApprovalFlows)

	sr := createSSLVPNServiceRequestForDefinition(t, fx, "sslvpn_execution_integrity")
	instance := awaitSSLVPNInstance(t, fx, "ticket", sr.TicketID)
	require.NoError(t, completeSSLVPNApproval(t, fx, instance, "Approval_1"))
	require.NoError(t, completeSSLVPNApproval(t, fx, instance, "Approval_2"))
	task := assertOneSSLVPNDelegation(t, fx, instance)
	event := dispatchSSLVPNDelegate(t, fx, task)
	kafContext := kafSSLVPNContext(t, fx, event.TaskID)
	req := sslvpnCompletionRequest(fx, event.TaskID, kafContext, "run-sslvpn", "finish")

	first, err := fx.delegation.ExecuteAction(fx.ctx, event.TaskID, req, fx.engine)
	require.NoError(t, err)
	replay, err := fx.delegation.ExecuteAction(fx.ctx, event.TaskID, req, fx.engine)
	require.NoError(t, err)
	require.Equal(t, itsmservice.KafActionApplied, first.ResultStatus)
	require.Equal(t, itsmservice.KafActionAlreadyApplied, replay.ResultStatus)

	completedTaskCount, err := fx.client.ProcessTask.Query().Where(
		processtask.TaskIDEQ(event.TaskID),
		processtask.StatusEQ(common.ProcessTaskStatusCompleted),
	).Count(fx.ctx)
	require.NoError(t, err)
	assert.Equal(t, 1, completedTaskCount)
	completedInstanceCount, err := fx.client.ProcessInstance.Query().Where(
		processinstance.IDEQ(instance.ID),
		processinstance.StatusEQ("completed"),
	).Count(fx.ctx)
	require.NoError(t, err)
	assert.Equal(t, 1, completedInstanceCount)
	ledgerCount, err := fx.client.KafTaskActionLedger.Query().Where(
		kaftaskactionledger.TenantIDEQ(fx.tenant.ID),
		kaftaskactionledger.TaskIDEQ(event.TaskID),
		kaftaskactionledger.ResultStatusEQ("applied"),
	).Count(fx.ctx)
	require.NoError(t, err)
	assert.Equal(t, 1, ledgerCount)
	receiptCount, err := fx.client.KafTaskCompletionReceipt.Query().Where(
		kaftaskcompletionreceipt.TenantIDEQ(fx.tenant.ID),
		kaftaskcompletionreceipt.TaskIDEQ(event.TaskID),
		kaftaskcompletionreceipt.StatusEQ("callback_succeeded"),
	).Count(fx.ctx)
	require.NoError(t, err)
	assert.Equal(t, 1, receiptCount)
	auditCount, err := fx.client.AuditLog.Query().Where(
		auditlog.TenantIDEQ(fx.tenant.ID),
		auditlog.ActionEQ("kaf_delegate.complete_bpmn_task"),
	).Count(fx.ctx)
	require.NoError(t, err)
	assert.Equal(t, 1, auditCount)
	persistedTask, err := fx.client.ProcessTask.Get(fx.ctx, task.ID)
	require.NoError(t, err)
	assert.NotContains(t, persistedTask.TaskVariables, "kaf_action_results")
}

func TestSSLVPNRequest_CreateRollsBackWorkItemAndDoesNotStartBPMNWhenExtensionPersistenceFails(t *testing.T) {
	fx := newSSLVPNDelegationFixture(t)
	deploySSLVPNDefinition(t, fx, "sslvpn_extension_failure", fmt.Sprintf(sslvpnApprovalNodes, fx.approver.ID, fx.approver.ID), sslvpnApprovalFlows)
	fx.client.ServiceRequest.Use(func(next ent.Mutator) ent.Mutator {
		return ent.MutateFunc(func(ctx context.Context, mutation ent.Mutation) (ent.Value, error) {
			if mutation.Op().Is(ent.OpCreate) {
				return nil, fmt.Errorf("injected service request persistence failure")
			}
			return next.Mutate(ctx, mutation)
		})
	})

	logger := zaptest.NewLogger(t).Sugar()
	scRepo := service_catalog.NewEntRepository(fx.client)
	catalog, err := service_catalog.NewService(scRepo, fx.client, logger).Create(fx.ctx, "SSLVPN access", "SSLVPN access request", "Delegated SSLVPN access", 1, fx.tenant.ID, "enabled", 0, 0, nil, "sslvpn_extension_failure", "access")
	require.NoError(t, err)
	ticketSvc := itsmservice.NewTicketServiceForTest(fx.client, logger)
	ticketSvc.SetProcessTriggerService(itsmservice.NewProcessTriggerService(fx.client, fx.engine))
	svc := NewService(NewEntRepository(fx.client), scRepo, cmdb.NewEntRepository(fx.client), fx.client, logger, ticketSvc, nil, nil)

	_, err = svc.Create(fx.ctx, fx.tenant.ID, fx.requester.ID, catalog.ID, &ServiceRequest{ComplianceAck: true, FormData: map[string]interface{}{"title": "SSLVPN extension failure", "reason": "verify atomic creation"}})
	require.ErrorContains(t, err, "Failed to create service request")

	classifiedCount, err := fx.client.Ticket.Query().Where(ticket.TenantIDEQ(fx.tenant.ID), ticket.RecordClassEQ("service_request_item")).Count(fx.ctx)
	require.NoError(t, err)
	assert.Zero(t, classifiedCount)
	extensionCount, err := fx.client.ServiceRequest.Query().Where(servicerequest.TenantIDEQ(fx.tenant.ID)).Count(fx.ctx)
	require.NoError(t, err)
	assert.Zero(t, extensionCount)
	processCount, err := fx.client.ProcessInstance.Query().Where(processinstance.TenantIDEQ(fx.tenant.ID)).Count(fx.ctx)
	require.NoError(t, err)
	assert.Zero(t, processCount)
}

func TestSSLVPNRequest_ConflictingRecordClassVariableCannotReachKAF(t *testing.T) {
	fx := newSSLVPNDelegationFixture(t)
	deploySSLVPNDefinition(t, fx, "sslvpn_record_class_conflict", fmt.Sprintf(sslvpnApprovalNodes, fx.approver.ID, fx.approver.ID), sslvpnApprovalFlows)

	sr := createSSLVPNServiceRequestForDefinition(t, fx, "sslvpn_record_class_conflict")
	instance := awaitSSLVPNInstance(t, fx, "ticket", sr.TicketID)
	require.NoError(t, completeSSLVPNApproval(t, fx, instance, "Approval_1"))
	err := completeSSLVPNApprovalWithVariables(t, fx, instance, "Approval_2", map[string]interface{}{"approvalAction": "approve", "approvalResult": "approved", "record_class": "incident"})
	require.ErrorContains(t, err, "record class variable conflicts")
	assertSSLVPNApprovalRemainsActionableWithoutConflictingRecordClass(t, fx, instance, "Approval_2")

	// The same approval task must remain retryable after its invalid input is rejected.
	require.NoError(t, completeSSLVPNApproval(t, fx, instance, "Approval_2"))
	assertTwoSSLVPNApprovalDecisions(t, fx, instance)
	assertOneSSLVPNDelegation(t, fx, instance)
}

func TestSSLVPNIncident_UsesSameDelegationTransportWithoutServiceRequestConversion(t *testing.T) {
	fx := newSSLVPNDelegationFixture(t)
	deploySSLVPNDefinition(t, fx, "incident_emergency_flow", "", sslvpnIncidentFlows)
	incidentService := itsmservice.NewIncidentService(fx.client, zaptest.NewLogger(t).Sugar())
	incidentService.SetProcessTriggerService(itsmservice.NewProcessTriggerService(fx.client, fx.engine))
	incidentResponse, err := incidentService.CreateIncident(fx.ctx, &dto.CreateIncidentRequest{Title: "SSLVPN connection unavailable", Description: "VPN client cannot establish a connection", Priority: "high", Severity: "high"}, fx.tenant.ID, fx.requester.ID)
	require.NoError(t, err)
	require.NotNil(t, incidentResponse.WorkItemID)
	workItem, err := fx.client.Ticket.Get(fx.ctx, *incidentResponse.WorkItemID)
	require.NoError(t, err)
	assert.Equal(t, "incident", workItem.RecordClass)
	assertExclusiveSSLVPNIncidentClass(t, fx, workItem.ID)

	instance := awaitSSLVPNInstance(t, fx, "incident", workItem.ID)
	task := assertOneSSLVPNDelegation(t, fx, instance)
	event := dispatchSSLVPNDelegate(t, fx, task)
	kafContext := kafSSLVPNContext(t, fx, event.TaskID)
	assert.Equal(t, "incident", kafContext.RecordClass)
	assert.Equal(t, workItem.ID, kafContext.WorkItem.ID)
	assertExclusiveSSLVPNIncidentClass(t, fx, workItem.ID)
	assertNoSensitiveSSLVPNPayload(t, event)
}

func newSSLVPNDelegationFixture(t *testing.T) *sslvpnDelegationFixture {
	t.Helper()
	client := enttest.Open(t, "sqlite3", "file:kaf_delegation_sslvpn_e2e?mode=memory&cache=shared&_fk=1")
	t.Cleanup(func() { client.Close() })
	ctx := context.Background()
	tenant, err := client.Tenant.Create().SetName("SSLVPN Tenant").SetCode("sslvpn-kaf").SetDomain("sslvpn.example.test").SetStatus("active").Save(ctx)
	require.NoError(t, err)
	requester, err := client.User.Create().SetUsername("sslvpn-requester").SetEmail("requester@sslvpn.example.test").SetName("SSLVPN Requester").SetPasswordHash("hash").SetRole("end_user").SetActive(true).SetTenantID(tenant.ID).Save(ctx)
	require.NoError(t, err)
	approver, err := client.User.Create().SetUsername("sslvpn-approver").SetEmail("approver@sslvpn.example.test").SetName("SSLVPN Approver").SetPasswordHash("hash").SetRole("agent").SetActive(true).SetTenantID(tenant.ID).Save(ctx)
	require.NoError(t, err)
	automation, err := client.User.Create().SetUsername("kaf-automation").SetEmail("kaf@sslvpn.example.test").SetName("KAF Automation").SetPasswordHash("hash").SetRole("kaf_automation").SetActive(true).SetTenantID(tenant.ID).Save(ctx)
	require.NoError(t, err)
	workflowCtx := context.WithValue(context.WithValue(ctx, bpmn.BPMNTenantIDContextKey, tenant.ID), bpmn.BPMNUserIDContextKey, automation.ID)
	return &sslvpnDelegationFixture{client: client, engine: itsmservice.NewCustomProcessEngine(client, zaptest.NewLogger(t).Sugar()), delegation: itsmservice.NewKafDelegationService(client), ctx: workflowCtx, tenant: tenant, requester: requester, approver: approver}
}

func deploySSLVPNDefinition(t *testing.T, fx *sslvpnDelegationFixture, key, nodes, flows string) {
	t.Helper()
	deployment, err := fx.client.ProcessDeployment.Create().SetDeploymentID("DEP-" + key).SetDeploymentName("SSLVPN KAF deployment").SetDeploymentTime(time.Now()).SetDeployedBy("test").SetIsActive(true).SetTenantID(fx.tenant.ID).Save(fx.ctx)
	require.NoError(t, err)
	_, err = fx.client.ProcessDefinition.Create().SetKey(key).SetName("SSLVPN KAF flow").SetVersion("1").SetIsLatest(true).SetIsActive(true).SetBpmnXML([]byte(fmt.Sprintf(sslvpnDelegationBPMN, key, nodes, flows))).SetDeploymentID(deployment.ID).SetDeployedAt(time.Now()).SetTenantID(fx.tenant.ID).Save(fx.ctx)
	require.NoError(t, err)
}

func createSSLVPNServiceRequest(t *testing.T, fx *sslvpnDelegationFixture) *ServiceRequest {
	return createSSLVPNServiceRequestForDefinition(t, fx, "sslvpn_service_request")
}

func createSSLVPNServiceRequestForDefinition(t *testing.T, fx *sslvpnDelegationFixture, definitionKey string) *ServiceRequest {
	t.Helper()
	logger := zaptest.NewLogger(t).Sugar()
	scRepo := service_catalog.NewEntRepository(fx.client)
	catalog, err := service_catalog.NewService(scRepo, fx.client, logger).Create(fx.ctx, "SSLVPN access", "SSLVPN access request", "Delegated SSLVPN access", 1, fx.tenant.ID, "enabled", 0, 0, nil, definitionKey, "access")
	require.NoError(t, err)
	ticketSvc := itsmservice.NewTicketServiceForTest(fx.client, logger)
	ticketSvc.SetProcessTriggerService(itsmservice.NewProcessTriggerService(fx.client, fx.engine))
	svc := NewService(NewEntRepository(fx.client), scRepo, cmdb.NewEntRepository(fx.client), fx.client, logger, ticketSvc, nil, nil)
	created, err := svc.Create(fx.ctx, fx.tenant.ID, fx.requester.ID, catalog.ID, &ServiceRequest{ComplianceAck: true, FormData: map[string]interface{}{"title": "SSLVPN access request", "reason": "VPN profile details must stay in ITSM"}})
	require.NoError(t, err)
	return created
}

func awaitSSLVPNInstance(t *testing.T, fx *sslvpnDelegationFixture, businessType string, workItemID int) *ent.ProcessInstance {
	t.Helper()
	deadline := time.Now().Add(2 * time.Second)
	businessKey := businessType + ":" + strconv.Itoa(workItemID)
	for time.Now().Before(deadline) {
		instance, err := fx.client.ProcessInstance.Query().Where(processinstance.BusinessKeyEQ(businessKey), processinstance.TenantIDEQ(fx.tenant.ID)).Only(fx.ctx)
		if err == nil {
			return instance
		}
		time.Sleep(10 * time.Millisecond)
	}
	require.Failf(t, "workflow instance was not created", "business key %q", businessKey)
	return nil
}

func completeSSLVPNApproval(t *testing.T, fx *sslvpnDelegationFixture, instance *ent.ProcessInstance, definitionKey string) error {
	return completeSSLVPNApprovalWithVariables(t, fx, instance, definitionKey, map[string]interface{}{"approvalAction": "approve", "approvalResult": "approved"})
}

func completeSSLVPNApprovalWithVariables(t *testing.T, fx *sslvpnDelegationFixture, instance *ent.ProcessInstance, definitionKey string, variables map[string]interface{}) error {
	t.Helper()
	task, err := fx.client.ProcessTask.Query().Where(processtask.ProcessInstanceIDEQ(instance.ID), processtask.TaskDefinitionKeyEQ(definitionKey), processtask.StatusIn(common.ProcessTaskStatusAssigned, common.ProcessTaskStatusCreated)).Only(fx.ctx)
	require.NoError(t, err)
	approvalCtx := context.WithValue(context.WithValue(context.Background(), bpmn.BPMNTenantIDContextKey, fx.tenant.ID), bpmn.BPMNUserIDContextKey, fx.approver.ID)
	return fx.engine.CompleteTask(approvalCtx, task.TaskID, variables)
}

func assertNoSSLVPNDelegation(t *testing.T, fx *sslvpnDelegationFixture, instance *ent.ProcessInstance) {
	t.Helper()
	count, err := fx.client.ProcessTask.Query().Where(processtask.ProcessInstanceIDEQ(instance.ID), processtask.TaskTypeEQ(bpmn.KafDelegateTaskType)).Count(fx.ctx)
	require.NoError(t, err)
	assert.Zero(t, count)
}

func assertSSLVPNApprovalRemainsActionableWithoutConflictingRecordClass(t *testing.T, fx *sslvpnDelegationFixture, instance *ent.ProcessInstance, definitionKey string) {
	t.Helper()
	task, err := fx.client.ProcessTask.Query().
		Where(processtask.ProcessInstanceIDEQ(instance.ID), processtask.TaskDefinitionKeyEQ(definitionKey)).
		Only(fx.ctx)
	require.NoError(t, err)
	assert.Contains(t, []string{common.ProcessTaskStatusAssigned, common.ProcessTaskStatusCreated}, task.Status)
	assert.NotContains(t, task.TaskVariables, "record_class")

	persisted, err := fx.client.ProcessInstance.Get(fx.ctx, instance.ID)
	require.NoError(t, err)
	assert.NotEqual(t, "incident", persisted.Variables["record_class"])
	assertNoSSLVPNDelegation(t, fx, persisted)

	approvalCount, err := fx.client.ProcessApprovalDecision.Query().
		Where(processapprovaldecision.ProcessInstanceIDEQ(instance.ID), processapprovaldecision.DecisionEQ("approved")).
		Count(fx.ctx)
	require.NoError(t, err)
	assert.Equal(t, 1, approvalCount)
}

func assertTwoSSLVPNApprovalDecisions(t *testing.T, fx *sslvpnDelegationFixture, instance *ent.ProcessInstance) {
	t.Helper()
	count, err := fx.client.ProcessApprovalDecision.Query().
		Where(processapprovaldecision.ProcessInstanceIDEQ(instance.ID), processapprovaldecision.DecisionEQ("approved")).
		Count(fx.ctx)
	require.NoError(t, err)
	assert.Equal(t, 2, count, "both real approval task completions must persist their BPMN decisions")
}

func assertOneSSLVPNDelegation(t *testing.T, fx *sslvpnDelegationFixture, instance *ent.ProcessInstance) *ent.ProcessTask {
	t.Helper()
	tasks, err := fx.client.ProcessTask.Query().Where(processtask.ProcessInstanceIDEQ(instance.ID), processtask.TaskTypeEQ(bpmn.KafDelegateTaskType), processtask.StatusEQ(common.ProcessTaskStatusDelegated)).All(fx.ctx)
	require.NoError(t, err)
	require.Len(t, tasks, 1, "the workflow must create exactly one delegated KAF task")
	events, err := fx.client.OutboxEvent.Query().Where(outboxevent.AggregateIDEQ(tasks[0].TaskID), outboxevent.EventTypeEQ("kaf_delegate_requested")).All(fx.ctx)
	require.NoError(t, err)
	require.Len(t, events, 1, "the delegated KAF task must create exactly one outbox event")
	return tasks[0]
}

func dispatchSSLVPNDelegate(t *testing.T, fx *sslvpnDelegationFixture, task *ent.ProcessTask) itsmservice.KafDelegateRequested {
	t.Helper()
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
	dispatcher, err := itsmservice.NewKafOutboxDispatcher(itsmservice.NewOutboxEventRepository(fx.client), itsmservice.KafOutboxConfig{WebhookURL: server.URL, WebhookSecret: "sslvpn-test-secret", BatchSize: 1, PollInterval: time.Second})
	require.NoError(t, err)
	require.NoError(t, dispatcher.DispatchOnce(fx.ctx))
	var event itsmservice.KafDelegateRequested
	require.NotEmpty(t, received)
	require.NoError(t, json.Unmarshal(received, &event))
	assert.Equal(t, task.TaskID, event.TaskID)
	persisted, err := fx.client.OutboxEvent.Query().Where(outboxevent.EventIDEQ(event.EventID)).Only(fx.ctx)
	require.NoError(t, err)
	assert.Equal(t, "published", persisted.Status)
	return event
}

func kafSSLVPNContext(t *testing.T, fx *sslvpnDelegationFixture, taskID string) *itsmservice.KafTaskContext {
	t.Helper()
	kafContext, err := fx.delegation.GetTaskContext(fx.ctx, taskID)
	require.NoError(t, err)
	return kafContext
}

func completeSSLVPNDelegate(t *testing.T, fx *sslvpnDelegationFixture, taskID, runID, stepID string) {
	t.Helper()
	executeSSLVPNDelegate(t, fx, taskID, runID, stepID)
}

func executeSSLVPNDelegate(t *testing.T, fx *sslvpnDelegationFixture, taskID, runID, stepID string) *itsmservice.KafActionResult {
	t.Helper()
	kafContext := kafSSLVPNContext(t, fx, taskID)
	result, err := fx.delegation.ExecuteAction(fx.ctx, taskID, sslvpnCompletionRequest(fx, taskID, kafContext, runID, stepID), fx.engine)
	require.NoError(t, err)
	return result
}

func sslvpnCompletionRequest(fx *sslvpnDelegationFixture, taskID string, kafContext *itsmservice.KafTaskContext, runID, stepID string) itsmservice.KafActionRequest {
	return itsmservice.KafActionRequest{Action: "complete_bpmn_task", ExpectedVersion: kafContext.ExpectedVersion, Execution: itsmservice.KafActionExecution{RunID: runID, StepID: stepID, IdempotencyKey: strconv.Itoa(fx.tenant.ID) + ":" + taskID + ":" + runID + ":" + stepID, CorrelationID: kafContext.CorrelationID, ProcedureRef: "sslvpn-grant", ProcedureVersion: "test-v1"}, Payload: itsmservice.KafActionPayload{ResultSummary: "SSLVPN grant completed"}}
}

func assertSSLVPNProcessAdvancedOnce(t *testing.T, fx *sslvpnDelegationFixture, instance *ent.ProcessInstance, taskID string) {
	t.Helper()
	completed, err := fx.client.ProcessTask.Query().Where(processtask.TaskIDEQ(taskID), processtask.StatusEQ("completed")).Count(fx.ctx)
	require.NoError(t, err)
	assert.Equal(t, 1, completed)
	delegated, err := fx.client.ProcessTask.Query().Where(processtask.ProcessInstanceIDEQ(instance.ID), processtask.TaskTypeEQ(bpmn.KafDelegateTaskType), processtask.StatusEQ(common.ProcessTaskStatusDelegated)).Count(fx.ctx)
	require.NoError(t, err)
	assert.Zero(t, delegated)
}

func assertExclusiveSSLVPNServiceRequestClass(t *testing.T, fx *sslvpnDelegationFixture, workItemID int) {
	t.Helper()
	srCount, err := fx.client.ServiceRequest.Query().Where(servicerequest.TicketIDEQ(workItemID), servicerequest.TenantIDEQ(fx.tenant.ID)).Count(fx.ctx)
	require.NoError(t, err)
	assert.Equal(t, 1, srCount)
	incidentCount, err := fx.client.Incident.Query().Where(incident.WorkItemIDEQ(workItemID), incident.TenantIDEQ(fx.tenant.ID)).Count(fx.ctx)
	require.NoError(t, err)
	assert.Zero(t, incidentCount)
}

func assertExclusiveSSLVPNIncidentClass(t *testing.T, fx *sslvpnDelegationFixture, workItemID int) {
	t.Helper()
	incidentCount, err := fx.client.Incident.Query().Where(incident.WorkItemIDEQ(workItemID), incident.TenantIDEQ(fx.tenant.ID)).Count(fx.ctx)
	require.NoError(t, err)
	assert.Equal(t, 1, incidentCount)
	srCount, err := fx.client.ServiceRequest.Query().Where(servicerequest.TicketIDEQ(workItemID), servicerequest.TenantIDEQ(fx.tenant.ID)).Count(fx.ctx)
	require.NoError(t, err)
	assert.Zero(t, srCount)
}

func assertNoSensitiveSSLVPNPayload(t *testing.T, event itsmservice.KafDelegateRequested) {
	t.Helper()
	payload, err := json.Marshal(event)
	require.NoError(t, err)
	assert.NotContains(t, string(payload), "description")
	assert.NotContains(t, string(payload), "VPN profile details")
	assert.NotContains(t, string(payload), "password")
}
