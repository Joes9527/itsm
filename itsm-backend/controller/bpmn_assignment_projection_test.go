package controller

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http/httptest"
	"strconv"
	"testing"

	executionfixture "itsm-backend/tests/fixtures/execution"

	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/require"
	"go.uber.org/zap"
	"itsm-backend/ent/enttest"
	"itsm-backend/middleware"
	"itsm-backend/service"
)

func TestBPMNBoundTaskDetailUsesAssignmentDTO(t *testing.T) {
	gin.SetMode(gin.TestMode)
	client := enttest.Open(t, "sqlite3", "file:"+t.Name()+"?mode=memory&cache=shared&_fk=1")
	defer client.Close()
	ctx := context.Background()
	tenant := client.Tenant.Create().SetName("Bound").SetCode("bound").SaveX(ctx)
	actor := client.User.Create().SetUsername("bound-actor").SetEmail("actor@example.test").SetPasswordHash("fixture").SetName("Actor").SetRole("super_admin").SetTenantID(tenant.ID).SetActive(true).SaveX(ctx)
	item := client.Ticket.Create().SetTitle("Bound").SetDescription("Fixture").SetTicketNumber("BOUND-DETAIL").SetRecordClass("service_request_item").SetRequesterID(actor.ID).SetAssigneeID(actor.ID).SetTenantID(tenant.ID).SaveX(ctx)
	deployment := client.ProcessDeployment.Create().SetDeploymentID("bound").SetDeploymentName("Bound").SetTenantID(tenant.ID).SaveX(ctx)
	definition := client.ProcessDefinition.Create().SetKey("bound").SetName("Bound").SetBpmnXML([]byte("<definitions/>")).SetDeploymentID(deployment.ID).SetTenantID(tenant.ID).SaveX(ctx)
	instance := client.ProcessInstance.Create().SetProcessInstanceID("bound").SetProcessDefinitionID(definition.ID).SetProcessDefinitionKey(definition.Key).SetBusinessType("service_request_item").SetBusinessID(item.ID).SetTenantID(tenant.ID).SaveX(ctx)
	task := client.ProcessTask.Create().SetTaskID("bound-detail-task").SetTaskName("Bound").SetTaskDefinitionKey("fulfill").SetTaskType("user_task").SetProcessInstanceID(instance.ID).SetProcessDefinitionKey(definition.Key).SetAssigneeSource("work_item_assignee").SetTaskVariables(map[string]interface{}{"taskPurpose": "fulfillment"}).SetTenantID(tenant.ID).SaveX(ctx)
	engine := service.NewCustomProcessEngine(client, zap.NewNop().Sugar(), executionfixture.Standard())
	controller := &BPMNWorkflowController{processEngine: engine}
	for _, id := range []string{strconv.Itoa(task.ID), task.TaskID} {
		recorder := httptest.NewRecorder()
		request, _ := gin.CreateTestContext(recorder)
		request.Request = httptest.NewRequest("GET", "/tasks/"+id, nil).WithContext(middleware.WithAuthenticatedTenantID(ctx, tenant.ID))
		request.Set("client", client)
		request.Params = gin.Params{{Key: "id", Value: id}}
		request.Set("user_id", actor.ID)
		request.Set("tenant_id", tenant.ID)
		request.Set("role", "super_admin")
		controller.GetTask(request)
		require.Equal(t, 200, recorder.Code, recorder.Body.String())
		var envelope map[string]interface{}
		require.NoError(t, json.Unmarshal(recorder.Body.Bytes(), &envelope))
		data := envelope["data"].(map[string]interface{})
		require.Equal(t, "work_item_assignee", data["assigneeSource"])
		require.Equal(t, "assigned", data["assignmentState"])
		require.Equal(t, strconv.Itoa(actor.ID), data["assignee"])
		require.Equal(t, float64(task.ID), data["id"])
		require.NotContains(t, data, "assignee_source")
	}
	for _, id := range []string{strconv.Itoa(task.ID), task.TaskID} {
		for _, operation := range []string{"claim", "assign", "complete"} {
			if operation == "complete" {
				client.Ticket.UpdateOne(item).ClearAssigneeID().SaveX(ctx)
			}
			recorder := httptest.NewRecorder()
			request, _ := gin.CreateTestContext(recorder)
			request.Request = httptest.NewRequest("POST", "/tasks/"+id+"/"+operation, bytes.NewBufferString("{\"assignee\":\"bound-actor\",\"variables\":{}}")).WithContext(middleware.WithAuthenticatedTenantID(ctx, tenant.ID))
			request.Request.Header.Set("Content-Type", "application/json")
			request.Params = gin.Params{{Key: "id", Value: id}}
			request.Set("client", client)
			request.Set("user_id", actor.ID)
			request.Set("tenant_id", tenant.ID)
			request.Set("role", "super_admin")
			switch operation {
			case "claim":
				controller.ClaimTask(request)
			case "assign":
				controller.AssignTask(request)
			case "complete":
				controller.CompleteTask(request)
			}
			require.Equal(t, 403, recorder.Code, operation+" "+id+": "+recorder.Body.String())
		}
	}
	require.Empty(t, client.ProcessTask.GetX(ctx, task.ID).Assignee)
}
