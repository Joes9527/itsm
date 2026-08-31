package controller

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"itsm-backend/common"
	"itsm-backend/dto"
	"itsm-backend/ent"
	"itsm-backend/ent/auditlog"
	"itsm-backend/ent/enttest"
	"itsm-backend/ent/kaftaskcompletionreceipt"
	"itsm-backend/ent/processtask"
	"itsm-backend/ent/ticketcomment"
	"itsm-backend/ent/user"
	"itsm-backend/service"
	"itsm-backend/service/bpmn"

	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"go.uber.org/zap/zaptest"

	_ "github.com/mattn/go-sqlite3"
)

type kafHTTPFixture struct {
	actorTenantID           int
	taskTenantID            int
	useForeignRequestTenant bool
	taskType                string
	status                  string
	allowedActions          string
	failingCompletionError  string
}

type failingKafCallbackHandler struct{ err error }

func (h *failingKafCallbackHandler) GetTaskType() string  { return "failing_kaf_callback" }
func (h *failingKafCallbackHandler) GetHandlerID() string { return "failing_kaf_callback_handler" }
func (h *failingKafCallbackHandler) Validate(context.Context, map[string]interface{}) error {
	return nil
}
func (h *failingKafCallbackHandler) Execute(context.Context, *ent.ProcessTask, map[string]interface{}) (*dto.ServiceTaskResult, error) {
	return nil, h.err
}

var _ bpmn.ServiceTaskHandlerInterface = (*failingKafCallbackHandler)(nil)

func newKafDelegationHTTPFixture(t *testing.T, fixture kafHTTPFixture) (*gin.Engine, string, *ent.Client) {
	t.Helper()
	gin.SetMode(gin.TestMode)
	client := enttest.Open(t, "sqlite3", "file:kaf_delegation_controller?mode=memory&cache=shared&_fk=1")
	t.Cleanup(func() { client.Close() })
	ctx := context.Background()

	actorTenant, err := client.Tenant.Create().
		SetName("KAF actor tenant").SetCode("kaf-actor").SetDomain("actor.example.test").SetStatus("active").Save(ctx)
	require.NoError(t, err)
	taskTenant, err := client.Tenant.Create().
		SetName("KAF task tenant").SetCode("kaf-task").SetDomain("task.example.test").SetStatus("active").Save(ctx)
	require.NoError(t, err)
	actorTenantID := actorTenant.ID
	if fixture.actorTenantID == fixture.taskTenantID {
		actorTenantID = taskTenant.ID
	}
	requestTenantID := actorTenantID
	if fixture.useForeignRequestTenant {
		requestTenantID = actorTenant.ID
	}
	actor, err := client.User.Create().
		SetUsername("kaf-automation").SetEmail("kaf@example.test").SetName("KAF Automation").
		SetPasswordHash("hash").SetRole("kaf_automation").SetActive(true).SetTenantID(actorTenantID).Save(ctx)
	require.NoError(t, err)
	deployment, err := client.ProcessDeployment.Create().
		SetDeploymentID("DEP-kaf-http").SetDeploymentName("KAF HTTP deployment").SetDeploymentTime(time.Now()).
		SetDeployedBy("test").SetIsActive(true).SetTenantID(taskTenant.ID).Save(ctx)
	require.NoError(t, err)
	definition, err := client.ProcessDefinition.Create().
		SetKey("kaf-http").SetName("KAF HTTP flow").SetVersion("1").SetIsLatest(true).SetBpmnXML([]byte("<definitions/>")).
		SetDeploymentID(deployment.ID).SetDeployedAt(time.Now()).SetTenantID(taskTenant.ID).Save(ctx)
	require.NoError(t, err)
	_, err = client.ProcessDefinition.UpdateOneID(definition.ID).SetBpmnXML([]byte(kafDelegationHTTPBPMN)).Save(ctx)
	require.NoError(t, err)
	instance, err := client.ProcessInstance.Create().
		SetProcessInstanceID("PI-kaf-http").SetProcessDefinitionKey("kaf-http").SetProcessDefinitionID(definition.ID).
		SetBusinessKey("incident:42").SetBusinessType("incident").SetBusinessID(42).SetStatus("running").
		SetCurrentActivityID("Activity_Kaf").SetCurrentActivityName("KAF wait").SetVersion(3).SetTenantID(taskTenant.ID).Save(ctx)
	require.NoError(t, err)
	allowedActions := fixture.allowedActions
	if allowedActions == "" {
		allowedActions = "complete_bpmn_task,update_progress,record_execution_failure"
	}
	taskVariables := map[string]interface{}{"allowed_actions": allowedActions}
	if fixture.failingCompletionError != "" {
		taskVariables["service_task_type"] = "failing_kaf_callback"
		taskVariables["action"] = "complete"
	}
	task, err := client.ProcessTask.Create().
		SetTaskID("TASK-kaf-http").SetProcessInstanceID(instance.ID).SetProcessDefinitionKey(instance.ProcessDefinitionKey).
		SetTaskDefinitionKey("Activity_Kaf").SetTaskName("KAF task").SetTaskType(fixture.taskType).
		SetStatus(fixture.status).SetTaskVariables(taskVariables).
		SetCorrelationID("corr-kaf-http").SetTenantID(taskTenant.ID).Save(ctx)
	require.NoError(t, err)
	if fixture.failingCompletionError != "" {
		task, err = client.ProcessTask.UpdateOneID(task.ID).
			SetCallbackHandlerID("failing_kaf_callback_handler").
			SetCallbackTaskType("failing_kaf_callback").
			SetCallbackAction("complete").
			Save(ctx)
		require.NoError(t, err)
	}

	engine := service.NewCustomProcessEngine(client, zaptest.NewLogger(t).Sugar()).(*service.CustomProcessEngine)
	if fixture.failingCompletionError != "" {
		engine.CallbackRegistry().RegisterHandler(&failingKafCallbackHandler{err: errors.New(fixture.failingCompletionError)})
	}
	controller := NewKafDelegationController(client, engine)
	router := gin.New()
	router.Use(func(c *gin.Context) {
		c.Set("tenant_id", requestTenantID)
		c.Set("user_id", actor.ID)
		c.Next()
	})
	controller.RegisterRoutes(router.Group("/api/v1"))
	return router, task.TaskID, client
}

const kafDelegationHTTPBPMN = `<?xml version="1.0" encoding="UTF-8"?>
<bpmn:definitions xmlns:bpmn="http://www.omg.org/spec/BPMN/20100524/MODEL" targetNamespace="http://bpmn.io/schema/bpmn">
  <bpmn:process id="kaf_http" name="KAF HTTP" isExecutable="true">
    <bpmn:startEvent id="Start_1" name="Start"><bpmn:outgoing>Flow_0</bpmn:outgoing></bpmn:startEvent>
    <bpmn:serviceTask id="Activity_Kaf" name="KAF delegation"><bpmn:incoming>Flow_0</bpmn:incoming><bpmn:outgoing>Flow_1</bpmn:outgoing></bpmn:serviceTask>
    <bpmn:endEvent id="End_1" name="End"><bpmn:incoming>Flow_1</bpmn:incoming></bpmn:endEvent>
    <bpmn:sequenceFlow id="Flow_0" sourceRef="Start_1" targetRef="Activity_Kaf" />
    <bpmn:sequenceFlow id="Flow_1" sourceRef="Activity_Kaf" targetRef="End_1" />
  </bpmn:process>
</bpmn:definitions>`

func doKafRequest(t *testing.T, router http.Handler, method, target, body string) *httptest.ResponseRecorder {
	t.Helper()
	req := httptest.NewRequest(method, target, bytes.NewBufferString(body))
	if body != "" {
		req.Header.Set("Content-Type", "application/json")
	}
	response := httptest.NewRecorder()
	router.ServeHTTP(response, req)
	return response
}

func kafActionKey(t *testing.T, client *ent.Client, taskID, runID, stepID string) string {
	t.Helper()
	task, err := client.ProcessTask.Query().Where(processtask.TaskIDEQ(taskID)).Only(context.Background())
	require.NoError(t, err)
	return fmt.Sprintf("%d:%s:%s:%s", task.TenantID, taskID, runID, stepID)
}

func TestKafContext_RejectsDifferentTenantAutomationActor(t *testing.T) {
	router, taskID, _ := newKafDelegationHTTPFixture(t, kafHTTPFixture{actorTenantID: 1, taskTenantID: 2, taskType: "kaf_delegate", status: common.ProcessTaskStatusDelegated})
	response := doKafRequest(t, router, http.MethodGet, "/api/v1/bpmn/process-tasks/"+taskID+"/kaf-context", "")
	assert.Equal(t, http.StatusNotFound, response.Code)
}

func TestKafContext_RejectsValidKafActorWithDifferentRequestTenant(t *testing.T) {
	router, taskID, _ := newKafDelegationHTTPFixture(t, kafHTTPFixture{actorTenantID: 1, taskTenantID: 1, useForeignRequestTenant: true, taskType: "kaf_delegate", status: common.ProcessTaskStatusDelegated})
	response := doKafRequest(t, router, http.MethodGet, "/api/v1/bpmn/process-tasks/"+taskID+"/kaf-context", "")
	assert.Equal(t, http.StatusNotFound, response.Code)
}

func TestKafContext_RejectsNonDelegatedTaskType(t *testing.T) {
	router, taskID, _ := newKafDelegationHTTPFixture(t, kafHTTPFixture{actorTenantID: 1, taskTenantID: 1, taskType: "user_task", status: common.ProcessTaskStatusDelegated})
	response := doKafRequest(t, router, http.MethodGet, "/api/v1/bpmn/process-tasks/"+taskID+"/kaf-context", "")
	assert.Equal(t, http.StatusForbidden, response.Code)
}

func TestKafContext_ReturnsOnlyOpaqueAttachmentReferences(t *testing.T) {
	router, taskID, client := newKafDelegationHTTPFixture(t, kafHTTPFixture{actorTenantID: 1, taskTenantID: 1, taskType: "kaf_delegate", status: common.ProcessTaskStatusDelegated})
	workItemID := attachKafWorkItem(t, client, taskID)
	ctx := context.Background()
	task, err := client.ProcessTask.Query().Where(processtask.TaskIDEQ(taskID)).Only(ctx)
	require.NoError(t, err)
	actor, err := client.User.Query().Where(user.TenantIDEQ(task.TenantID)).Only(ctx)
	require.NoError(t, err)
	attachment, err := client.TicketAttachment.Create().
		SetTicketID(workItemID).
		SetFileName("vpn-passwords.txt").
		SetFilePath("tenant/private/vpn-passwords.txt").
		SetFileURL("https://storage.example.test/signed-secret").
		SetFileSize(42).
		SetFileType("text/plain").
		SetUploadedBy(actor.ID).
		SetTenantID(task.TenantID).
		Save(ctx)
	require.NoError(t, err)

	response := doKafRequest(t, router, http.MethodGet, "/api/v1/bpmn/process-tasks/"+taskID+"/kaf-context", "")
	require.Equal(t, http.StatusOK, response.Code)
	var body struct {
		Data service.KafTaskContext `json:"data"`
	}
	require.NoError(t, json.Unmarshal(response.Body.Bytes(), &body))
	require.Equal(t, []service.KafAttachmentRef{{ID: attachment.ID}}, body.Data.Attachments)
	assert.NotContains(t, response.Body.String(), "vpn-passwords.txt")
	assert.NotContains(t, response.Body.String(), "tenant/private")
	assert.NotContains(t, response.Body.String(), "signed-secret")
}

func TestKafDelegatedList_ReturnsOnlyCurrentTenantDelegatedKafTasks(t *testing.T) {
	router, taskID, _ := newKafDelegationHTTPFixture(t, kafHTTPFixture{actorTenantID: 1, taskTenantID: 1, taskType: "kaf_delegate", status: common.ProcessTaskStatusDelegated})
	response := doKafRequest(t, router, http.MethodGet, "/api/v1/bpmn/process-tasks/kaf-delegated?status=delegated", "")
	require.Equal(t, http.StatusOK, response.Code)
	assert.Contains(t, response.Body.String(), taskID)
}

func TestKafDelegatedList_PaginatesBeyondOneHundredTasks(t *testing.T) {
	router, _, client := newKafDelegationHTTPFixture(t, kafHTTPFixture{actorTenantID: 1, taskTenantID: 1, taskType: "kaf_delegate", status: common.ProcessTaskStatusDelegated})
	ctx := context.Background()
	instance, err := client.ProcessInstance.Query().Only(ctx)
	require.NoError(t, err)

	for index := 0; index < 101; index++ {
		_, err := client.ProcessTask.Create().
			SetTaskID(fmt.Sprintf("TASK-kaf-recovery-%03d", index)).
			SetProcessInstanceID(instance.ID).
			SetProcessDefinitionKey(instance.ProcessDefinitionKey).
			SetTaskDefinitionKey("Activity_Kaf").
			SetTaskName("KAF recovery task").
			SetTaskType("kaf_delegate").
			SetStatus(common.ProcessTaskStatusDelegated).
			SetTaskVariables(map[string]interface{}{"allowed_actions": "update_progress"}).
			SetCorrelationID(fmt.Sprintf("corr-kaf-recovery-%03d", index)).
			SetTenantID(instance.TenantID).
			Save(ctx)
		require.NoError(t, err)
	}

	first := doKafRequest(t, router, http.MethodGet, "/api/v1/bpmn/process-tasks/kaf-delegated?status=delegated&limit=100", "")
	require.Equal(t, http.StatusOK, first.Code)
	var firstPage struct {
		Code int `json:"code"`
		Data struct {
			Items      []service.KafTaskContext `json:"items"`
			Limit      int                      `json:"limit"`
			NextCursor string                   `json:"nextCursor"`
		} `json:"data"`
	}
	require.NoError(t, json.Unmarshal(first.Body.Bytes(), &firstPage))
	assert.Equal(t, common.SuccessCode, firstPage.Code)
	assert.Len(t, firstPage.Data.Items, 100)
	assert.Equal(t, 100, firstPage.Data.Limit)
	require.NotEmpty(t, firstPage.Data.NextCursor)

	second := doKafRequest(t, router, http.MethodGet, "/api/v1/bpmn/process-tasks/kaf-delegated?status=delegated&limit=100&cursor="+firstPage.Data.NextCursor, "")
	require.Equal(t, http.StatusOK, second.Code)
	var secondPage struct {
		Code int `json:"code"`
		Data struct {
			Items      []service.KafTaskContext `json:"items"`
			NextCursor string                   `json:"nextCursor"`
		} `json:"data"`
	}
	require.NoError(t, json.Unmarshal(second.Body.Bytes(), &secondPage))
	assert.Equal(t, common.SuccessCode, secondPage.Code)
	assert.Len(t, secondPage.Data.Items, 2)
	assert.Empty(t, secondPage.Data.NextCursor)
}

func TestKafAction_RejectsResolveUntilIncidentTypedActionExists(t *testing.T) {
	router, taskID, _ := newKafDelegationHTTPFixture(t, kafHTTPFixture{actorTenantID: 1, taskTenantID: 1, taskType: "kaf_delegate", status: common.ProcessTaskStatusDelegated})
	response := doKafRequest(t, router, http.MethodPost, "/api/v1/bpmn/process-tasks/"+taskID+"/actions", `{"action":"resolve"}`)
	assert.Equal(t, http.StatusUnprocessableEntity, response.Code)
}

func TestKafAction_RejectsActionNotAllowedByTheDelegatedTask(t *testing.T) {
	router, taskID, client := newKafDelegationHTTPFixture(t, kafHTTPFixture{actorTenantID: 1, taskTenantID: 1, taskType: "kaf_delegate", status: common.ProcessTaskStatusDelegated, allowedActions: "update_progress"})
	body := `{"action":"complete_bpmn_task","expectedVersion":3,"execution":{"runId":"run-1","stepId":"finish","idempotencyKey":"` + kafActionKey(t, client, taskID, "run-1", "finish") + `","correlationId":"corr-kaf-http","procedureRef":"vpn-grant","procedureVersion":"1"},"payload":{"resultSummary":"completed"}}`
	response := doKafRequest(t, router, http.MethodPost, "/api/v1/bpmn/process-tasks/"+taskID+"/actions", body)
	assert.Equal(t, http.StatusUnprocessableEntity, response.Code)
}

func TestKafAction_RejectsValidKafActorWithDifferentRequestTenant(t *testing.T) {
	router, taskID, client := newKafDelegationHTTPFixture(t, kafHTTPFixture{actorTenantID: 1, taskTenantID: 1, useForeignRequestTenant: true, taskType: "kaf_delegate", status: common.ProcessTaskStatusDelegated})
	attachKafWorkItem(t, client, taskID)
	body := `{"action":"update_progress","expectedVersion":3,"execution":{"runId":"run-1","stepId":"progress","idempotencyKey":"` + kafActionKey(t, client, taskID, "run-1", "progress") + `","correlationId":"corr-kaf-http","procedureRef":"vpn-grant","procedureVersion":"1"},"payload":{"resultSummary":"queued"}}`
	response := doKafRequest(t, router, http.MethodPost, "/api/v1/bpmn/process-tasks/"+taskID+"/actions", body)
	assert.Equal(t, http.StatusNotFound, response.Code)
}

func TestKafAction_IdempotentReplayRejectsValidKafActorWithDifferentRequestTenant(t *testing.T) {
	router, taskID, client := newKafDelegationHTTPFixture(t, kafHTTPFixture{actorTenantID: 1, taskTenantID: 1, useForeignRequestTenant: true, taskType: "kaf_delegate", status: common.ProcessTaskStatusDelegated})
	attachKafWorkItem(t, client, taskID)
	body := `{"action":"update_progress","expectedVersion":3,"execution":{"runId":"run-1","stepId":"progress","idempotencyKey":"` + kafActionKey(t, client, taskID, "run-1", "progress") + `","correlationId":"corr-kaf-http","procedureRef":"vpn-grant","procedureVersion":"1"},"payload":{"resultSummary":"queued"}}`
	task, err := client.ProcessTask.Query().Where(processtask.TaskIDEQ(taskID)).Only(context.Background())
	require.NoError(t, err)
	actor, err := client.User.Query().Where(user.TenantIDEQ(task.TenantID)).Only(context.Background())
	require.NoError(t, err)
	workflowCtx := context.WithValue(context.Background(), bpmn.BPMNTenantIDContextKey, task.TenantID)
	workflowCtx = context.WithValue(workflowCtx, bpmn.BPMNUserIDContextKey, actor.ID)
	_, err = service.NewKafDelegationService(client).ExecuteAction(workflowCtx, taskID, service.KafActionRequest{
		Action: "update_progress", ExpectedVersion: 3,
		Execution: service.KafActionExecution{RunID: "run-1", StepID: "progress", IdempotencyKey: kafActionKey(t, client, taskID, "run-1", "progress"), CorrelationID: "corr-kaf-http", ProcedureRef: "vpn-grant", ProcedureVersion: "1"},
		Payload:   service.KafActionPayload{ResultSummary: "queued"},
	}, nil)
	require.NoError(t, err)

	response := doKafRequest(t, router, http.MethodPost, "/api/v1/bpmn/process-tasks/"+taskID+"/actions", body)
	assert.Equal(t, http.StatusNotFound, response.Code)
}

func TestKafAction_CompleteIsIdempotentForACompletedDelegatedTask(t *testing.T) {
	router, taskID, client := newKafDelegationHTTPFixture(t, kafHTTPFixture{actorTenantID: 1, taskTenantID: 1, taskType: "kaf_delegate", status: common.ProcessTaskStatusDelegated})
	body := `{"action":"complete_bpmn_task","expectedVersion":3,"execution":{"runId":"run-1","stepId":"finish","idempotencyKey":"` + kafActionKey(t, client, taskID, "run-1", "finish") + `","correlationId":"corr-kaf-http","procedureRef":"vpn-grant","procedureVersion":"1"},"payload":{"resultSummary":"completed","evidenceRefs":[]}}`
	first := doKafRequest(t, router, http.MethodPost, "/api/v1/bpmn/process-tasks/"+taskID+"/actions", body)
	assert.Equal(t, http.StatusOK, first.Code)
	second := doKafRequest(t, router, http.MethodPost, "/api/v1/bpmn/process-tasks/"+taskID+"/actions", body)
	assert.Equal(t, http.StatusOK, second.Code)
	completed, err := client.ProcessTask.Query().Where(processtask.TaskIDEQ(taskID), processtask.StatusEQ("completed")).Count(context.Background())
	require.NoError(t, err)
	assert.Equal(t, 1, completed)
}

func TestExecuteAction_ReturnsResultStatusForReplay(t *testing.T) {
	router, taskID, client := newKafDelegationHTTPFixture(t, kafHTTPFixture{actorTenantID: 1, taskTenantID: 1, taskType: "kaf_delegate", status: common.ProcessTaskStatusDelegated})
	task, err := client.ProcessTask.Query().Where(processtask.TaskIDEQ(taskID)).Only(context.Background())
	require.NoError(t, err)
	key := fmt.Sprintf("%d:%s:run-1:finish", task.TenantID, taskID)
	body := `{"action":"complete_bpmn_task","expectedVersion":3,"execution":{"runId":"run-1","stepId":"finish","idempotencyKey":"` + key + `","correlationId":"corr-kaf-http","procedureRef":"vpn-grant","procedureVersion":"1"},"payload":{"resultSummary":"completed"}}`

	first := doKafRequest(t, router, http.MethodPost, "/api/v1/bpmn/process-tasks/"+taskID+"/actions", body)
	require.Equal(t, http.StatusOK, first.Code)
	response := doKafRequest(t, router, http.MethodPost, "/api/v1/bpmn/process-tasks/"+taskID+"/actions", body)

	assert.Equal(t, http.StatusOK, response.Code)
	assert.JSONEq(t, `{"code":0,"message":"success","data":{"action":"complete_bpmn_task","idempotencyKey":"`+key+`","resultStatus":"already_applied","expectedVersion":3}}`, response.Body.String())
}

func TestExecuteAction_ReturnsConflictForLiveLedgerLease(t *testing.T) {
	router, taskID, client := newKafDelegationHTTPFixture(t, kafHTTPFixture{actorTenantID: 1, taskTenantID: 1, taskType: "kaf_delegate", status: common.ProcessTaskStatusDelegated})
	task, err := client.ProcessTask.Query().Where(processtask.TaskIDEQ(taskID)).Only(context.Background())
	require.NoError(t, err)
	_, err = client.KafTaskActionLedger.Create().
		SetTenantID(task.TenantID).SetTaskID(taskID).
		SetRunID("run-1").SetStepID("finish").SetAction("complete_bpmn_task").
		SetIdempotencyKey(fmt.Sprintf("%d:%s:run-1:finish", task.TenantID, taskID)).
		SetCorrelationID(task.CorrelationID).SetProcedureRef("vpn-grant").SetProcedureVersion("1").
		SetResultStatus("executing").SetLeaseOwner("private-lease-owner").SetLeaseExpiresAt(time.Now().Add(time.Minute)).
		Save(context.Background())
	require.NoError(t, err)
	body := `{"action":"complete_bpmn_task","expectedVersion":3,"execution":{"runId":"run-1","stepId":"finish","idempotencyKey":"` + fmt.Sprintf("%d:%s:run-1:finish", task.TenantID, taskID) + `","correlationId":"corr-kaf-http","procedureRef":"vpn-grant","procedureVersion":"1"},"payload":{"resultSummary":"completed"}}`

	response := doKafRequest(t, router, http.MethodPost, "/api/v1/bpmn/process-tasks/"+taskID+"/actions", body)

	assert.Equal(t, http.StatusConflict, response.Code)
	assert.JSONEq(t, `{"code":4090,"message":"KAF action is in progress","data":{"code":"in_progress"}}`, response.Body.String())
	assert.NotContains(t, response.Body.String(), "private-lease-owner")
}

func TestExecuteAction_RedactsCompletionFailureAcrossPersistenceAndResponse(t *testing.T) {
	const token = "Bearer fake-kaf-token"
	router, taskID, client := newKafDelegationHTTPFixture(t, kafHTTPFixture{
		actorTenantID: 1, taskTenantID: 1, taskType: "kaf_delegate", status: common.ProcessTaskStatusDelegated,
		failingCompletionError: "callback failed with " + token,
	})
	task, err := client.ProcessTask.Query().Where(processtask.TaskIDEQ(taskID)).Only(context.Background())
	require.NoError(t, err)
	ticketID := attachKafWorkItem(t, client, taskID)
	body := `{"action":"complete_bpmn_task","expectedVersion":3,"execution":{"runId":"run-1","stepId":"finish","idempotencyKey":"` + fmt.Sprintf("%d:%s:run-1:finish", task.TenantID, taskID) + `","correlationId":"corr-kaf-http","procedureRef":"vpn-grant","procedureVersion":"1"},"payload":{"resultSummary":"completed"}}`

	response := doKafRequest(t, router, http.MethodPost, "/api/v1/bpmn/process-tasks/"+taskID+"/actions", body)

	assert.Equal(t, http.StatusInternalServerError, response.Code)
	assert.NotContains(t, response.Body.String(), token)
	receipt, err := client.KafTaskCompletionReceipt.Query().Where(kaftaskcompletionreceipt.TaskIDEQ(taskID)).Only(context.Background())
	require.NoError(t, err)
	assert.Equal(t, "callback_failed", receipt.ErrorCode)
	assert.NotContains(t, receipt.ErrorCode, token)
	audits, err := client.AuditLog.Query().Where(auditlog.TenantIDEQ(task.TenantID)).All(context.Background())
	require.NoError(t, err)
	assert.Empty(t, audits)
	for _, audit := range audits {
		if audit.RequestBody != nil {
			assert.NotContains(t, *audit.RequestBody, token)
		}
	}
	comments, err := client.TicketComment.Query().Where(ticketcomment.TicketIDEQ(ticketID)).All(context.Background())
	require.NoError(t, err)
	assert.Empty(t, comments)
	for _, comment := range comments {
		assert.NotContains(t, comment.Content, token)
	}
}

func TestKafAction_UpdateProgressWritesRedactedInternalTimelineRecord(t *testing.T) {
	router, taskID, client := newKafDelegationHTTPFixture(t, kafHTTPFixture{actorTenantID: 1, taskTenantID: 1, taskType: "kaf_delegate", status: common.ProcessTaskStatusDelegated})
	ticketID := attachKafWorkItem(t, client, taskID)
	body := `{"action":"update_progress","expectedVersion":3,"execution":{"runId":"run-1","stepId":"progress","idempotencyKey":"` + kafActionKey(t, client, taskID, "run-1", "progress") + `","correlationId":"corr-kaf-http","procedureRef":"vpn-grant","procedureVersion":"1"},"payload":{"resultSummary":"VPN grant queued authorization: Bearer super-secret access_token=also-secret"}}`

	response := doKafRequest(t, router, http.MethodPost, "/api/v1/bpmn/process-tasks/"+taskID+"/actions", body)
	require.Equal(t, http.StatusOK, response.Code)
	comment, err := client.TicketComment.Query().Where(ticketcomment.TicketIDEQ(ticketID)).Only(context.Background())
	require.NoError(t, err)
	assert.True(t, comment.IsInternal)
	assert.Contains(t, comment.Content, "KAF progress:")
	assert.Contains(t, comment.Content, "VPN grant queued")
	assert.Contains(t, comment.Content, "[redacted]")
	assert.NotContains(t, comment.Content, "super-secret")
	assert.NotContains(t, comment.Content, "also-secret")
}

func TestKafAction_RecordExecutionFailureWritesRedactedTimelineRecordAndKeepsDelegated(t *testing.T) {
	router, taskID, client := newKafDelegationHTTPFixture(t, kafHTTPFixture{actorTenantID: 1, taskTenantID: 1, taskType: "kaf_delegate", status: common.ProcessTaskStatusDelegated})
	ticketID := attachKafWorkItem(t, client, taskID)
	body := `{"action":"record_execution_failure","expectedVersion":3,"execution":{"runId":"run-1","stepId":"execute","idempotencyKey":"` + kafActionKey(t, client, taskID, "run-1", "execute") + `","correlationId":"corr-kaf-http","procedureRef":"vpn-grant","procedureVersion":"1"},"payload":{"failureSummary":"VPN grant failed password=super-secret api_key=also-secret"}}`

	response := doKafRequest(t, router, http.MethodPost, "/api/v1/bpmn/process-tasks/"+taskID+"/actions", body)
	require.Equal(t, http.StatusOK, response.Code)
	comment, err := client.TicketComment.Query().Where(ticketcomment.TicketIDEQ(ticketID)).Only(context.Background())
	require.NoError(t, err)
	assert.True(t, comment.IsInternal)
	assert.Contains(t, comment.Content, "KAF execution failure:")
	assert.Contains(t, comment.Content, "VPN grant failed")
	assert.Contains(t, comment.Content, "[redacted]")
	assert.NotContains(t, comment.Content, "super-secret")
	assert.NotContains(t, comment.Content, "also-secret")

	task, err := client.ProcessTask.Query().Where(processtask.TaskIDEQ(taskID)).Only(context.Background())
	require.NoError(t, err)
	assert.Equal(t, common.ProcessTaskStatusDelegated, task.Status)
}

func attachKafWorkItem(t *testing.T, client *ent.Client, taskID string) int {
	t.Helper()
	ctx := context.Background()
	task, err := client.ProcessTask.Query().Where(processtask.TaskIDEQ(taskID)).Only(ctx)
	require.NoError(t, err)
	actor, err := client.User.Query().Where(user.TenantIDEQ(task.TenantID)).Only(ctx)
	require.NoError(t, err)
	workItem, err := client.Ticket.Create().
		SetTitle("KAF delegated VPN access").
		SetTicketNumber("TCK-kaf-" + taskID).
		SetRequesterID(actor.ID).
		SetTenantID(task.TenantID).
		Save(ctx)
	require.NoError(t, err)
	_, err = client.ProcessInstance.UpdateOneID(task.ProcessInstanceID).SetBusinessID(workItem.ID).Save(ctx)
	require.NoError(t, err)
	return workItem.ID
}
