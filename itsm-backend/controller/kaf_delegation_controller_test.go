package controller

import (
	"bytes"
	"context"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"itsm-backend/common"
	"itsm-backend/ent"
	"itsm-backend/ent/enttest"
	"itsm-backend/ent/processtask"
	"itsm-backend/service"

	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"go.uber.org/zap/zaptest"

	_ "github.com/mattn/go-sqlite3"
)

type kafHTTPFixture struct {
	actorTenantID  int
	taskTenantID   int
	taskType       string
	status         string
	allowedActions string
}

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
	task, err := client.ProcessTask.Create().
		SetTaskID("TASK-kaf-http").SetProcessInstanceID(instance.ID).SetProcessDefinitionKey(instance.ProcessDefinitionKey).
		SetTaskDefinitionKey("Activity_Kaf").SetTaskName("KAF task").SetTaskType(fixture.taskType).
		SetStatus(fixture.status).SetTaskVariables(map[string]interface{}{"allowed_actions": allowedActions}).
		SetCorrelationID("corr-kaf-http").SetTenantID(taskTenant.ID).Save(ctx)
	require.NoError(t, err)

	engine := service.NewCustomProcessEngine(client, zaptest.NewLogger(t).Sugar())
	controller := NewKafDelegationController(client, engine)
	router := gin.New()
	router.Use(func(c *gin.Context) {
		c.Set("tenant_id", actorTenantID)
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

func TestKafContext_RejectsDifferentTenantAutomationActor(t *testing.T) {
	router, taskID, _ := newKafDelegationHTTPFixture(t, kafHTTPFixture{actorTenantID: 1, taskTenantID: 2, taskType: "kaf_delegate", status: common.ProcessTaskStatusDelegated})
	response := doKafRequest(t, router, http.MethodGet, "/api/v1/bpmn/process-tasks/"+taskID+"/kaf-context", "")
	assert.Equal(t, http.StatusForbidden, response.Code)
}

func TestKafContext_RejectsNonDelegatedTaskType(t *testing.T) {
	router, taskID, _ := newKafDelegationHTTPFixture(t, kafHTTPFixture{actorTenantID: 1, taskTenantID: 1, taskType: "user_task", status: common.ProcessTaskStatusDelegated})
	response := doKafRequest(t, router, http.MethodGet, "/api/v1/bpmn/process-tasks/"+taskID+"/kaf-context", "")
	assert.Equal(t, http.StatusForbidden, response.Code)
}

func TestKafDelegatedList_ReturnsOnlyCurrentTenantDelegatedKafTasks(t *testing.T) {
	router, taskID, _ := newKafDelegationHTTPFixture(t, kafHTTPFixture{actorTenantID: 1, taskTenantID: 1, taskType: "kaf_delegate", status: common.ProcessTaskStatusDelegated})
	response := doKafRequest(t, router, http.MethodGet, "/api/v1/bpmn/process-tasks/kaf-delegated?status=delegated", "")
	require.Equal(t, http.StatusOK, response.Code)
	assert.Contains(t, response.Body.String(), taskID)
}

func TestKafAction_RejectsResolveUntilIncidentTypedActionExists(t *testing.T) {
	router, taskID, _ := newKafDelegationHTTPFixture(t, kafHTTPFixture{actorTenantID: 1, taskTenantID: 1, taskType: "kaf_delegate", status: common.ProcessTaskStatusDelegated})
	response := doKafRequest(t, router, http.MethodPost, "/api/v1/bpmn/process-tasks/"+taskID+"/actions", `{"action":"resolve"}`)
	assert.Equal(t, http.StatusUnprocessableEntity, response.Code)
}

func TestKafAction_RejectsActionNotAllowedByTheDelegatedTask(t *testing.T) {
	router, taskID, _ := newKafDelegationHTTPFixture(t, kafHTTPFixture{actorTenantID: 1, taskTenantID: 1, taskType: "kaf_delegate", status: common.ProcessTaskStatusDelegated, allowedActions: "update_progress"})
	body := `{"action":"complete_bpmn_task","expectedVersion":3,"execution":{"runId":"run-1","stepId":"finish","idempotencyKey":"1:` + taskID + `:run-1:finish","correlationId":"corr-kaf-http","procedureRef":"vpn-grant","procedureVersion":"1"},"payload":{"resultSummary":"completed"}}`
	response := doKafRequest(t, router, http.MethodPost, "/api/v1/bpmn/process-tasks/"+taskID+"/actions", body)
	assert.Equal(t, http.StatusUnprocessableEntity, response.Code)
}

func TestKafAction_CompleteIsIdempotentForACompletedDelegatedTask(t *testing.T) {
	router, taskID, client := newKafDelegationHTTPFixture(t, kafHTTPFixture{actorTenantID: 1, taskTenantID: 1, taskType: "kaf_delegate", status: common.ProcessTaskStatusDelegated})
	body := `{"action":"complete_bpmn_task","expectedVersion":3,"execution":{"runId":"run-1","stepId":"finish","idempotencyKey":"1:` + taskID + `:run-1:finish","correlationId":"corr-kaf-http","procedureRef":"vpn-grant","procedureVersion":"1"},"payload":{"resultSummary":"completed","evidenceRefs":[]}}`
	first := doKafRequest(t, router, http.MethodPost, "/api/v1/bpmn/process-tasks/"+taskID+"/actions", body)
	assert.Equal(t, http.StatusOK, first.Code)
	second := doKafRequest(t, router, http.MethodPost, "/api/v1/bpmn/process-tasks/"+taskID+"/actions", body)
	assert.Equal(t, http.StatusOK, second.Code)
	completed, err := client.ProcessTask.Query().Where(processtask.TaskIDEQ(taskID), processtask.StatusEQ("completed")).Count(context.Background())
	require.NoError(t, err)
	assert.Equal(t, 1, completed)
}
