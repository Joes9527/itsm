package controller

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"testing"
	"time"

	"itsm-backend/dto"
	"itsm-backend/ent"
	"itsm-backend/ent/enttest"
	"itsm-backend/middleware"
	"itsm-backend/service"

	"github.com/gin-gonic/gin"
	_ "github.com/mattn/go-sqlite3"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"go.uber.org/zap"
)

type contextCapturingProcessInstanceService struct {
	listCtx  context.Context
	statsCtx context.Context
}

func (s *contextCapturingProcessInstanceService) GetProcessInstance(context.Context, string) (*ent.ProcessInstance, error) {
	return nil, nil
}

func (s *contextCapturingProcessInstanceService) ListProcessInstances(ctx context.Context, _ *service.ListProcessInstancesRequest) ([]*ent.ProcessInstance, int, error) {
	s.listCtx = ctx
	return nil, 0, nil
}

func (s *contextCapturingProcessInstanceService) GetProcessInstanceVariables(context.Context, string) (map[string]interface{}, error) {
	return nil, nil
}

func (s *contextCapturingProcessInstanceService) SetProcessInstanceVariables(context.Context, string, map[string]interface{}) error {
	return nil
}

func (s *contextCapturingProcessInstanceService) GetProcessInstanceHistory(context.Context, string) ([]*ent.ProcessExecutionHistory, error) {
	return nil, nil
}

func (s *contextCapturingProcessInstanceService) GetInstanceStatistics(ctx context.Context, _ *service.InstanceStatisticsRequest) (*service.InstanceStatistics, error) {
	s.statsCtx = ctx
	return &service.InstanceStatistics{}, nil
}

func TestGetBPMNTenantContextBuildsTrustedScope(t *testing.T) {
	gin.SetMode(gin.TestMode)
	client := enttest.Open(t, "sqlite3", "file:bpmn_scope?mode=memory&cache=shared&_fk=1")
	t.Cleanup(func() { _ = client.Close() })
	c, _ := gin.CreateTestContext(httptest.NewRecorder())
	c.Request = httptest.NewRequest(http.MethodGet, "/api/v1/bpmn/tasks", nil)
	c.Request = c.Request.WithContext(middleware.WithAuthenticatedTenantID(c.Request.Context(), 42))
	c.Set("tenant_id", 42)
	c.Set("user_id", 7)
	c.Set("role", "super_admin")
	c.Set("client", client)

	workflowCtx, tenantID, ok := getBPMNTenantContext(c)
	require.True(t, ok)
	require.Equal(t, 42, tenantID)
	scope, err := service.BPMNAccessScopeFromContext(workflowCtx)
	require.NoError(t, err)
	assert.Equal(t, 7, scope.UserID)
	assert.True(t, scope.CanReadAllInstances)
	assert.True(t, scope.CanUpdateAllInstances)
	assert.True(t, scope.CanReadAllTasks)
	assert.True(t, scope.CanUpdateAllTasks)
}

func TestGetBPMNTenantContextRejectsMissingTenantOrActor(t *testing.T) {
	for _, input := range []struct{ tenantID, userID int }{{0, 7}, {42, 0}} {
		c, _ := gin.CreateTestContext(httptest.NewRecorder())
		c.Request = httptest.NewRequest(http.MethodGet, "/api/v1/bpmn/tasks", nil)
		c.Request = c.Request.WithContext(middleware.WithAuthenticatedTenantID(c.Request.Context(), input.tenantID))
		c.Set("tenant_id", input.tenantID)
		c.Set("user_id", input.userID)
		_, _, ok := getBPMNTenantContext(c)
		assert.False(t, ok)
	}
}

func TestGetBPMNTenantContextRejectsRequestSelectedTenantForTenantlessJWT(t *testing.T) {
	gin.SetMode(gin.TestMode)
	client := enttest.Open(t, "sqlite3", "file:bpmn_scope_request_tenant?mode=memory&cache=shared&_fk=1")
	t.Cleanup(func() { _ = client.Close() })
	requestTenant, err := client.Tenant.Create().
		SetCode("request-selected").
		SetName("Request Selected Tenant").
		SetStatus("active").
		Save(context.Background())
	require.NoError(t, err)

	const jwtSecret = "bpmn-scope-request-tenant-secret"
	token, err := middleware.GenerateAccessToken(7, "operator", "super_admin", 0, jwtSecret, time.Hour)
	require.NoError(t, err)

	c, _ := gin.CreateTestContext(httptest.NewRecorder())
	c.Request = httptest.NewRequest(http.MethodGet, "/api/v1/bpmn/tasks", nil)
	c.Request.Header.Set("Authorization", "Bearer "+token)
	c.Request.Header.Set("X-Tenant-Code", requestTenant.Code)
	c.Set("client", client)

	middleware.AuthMiddleware(jwtSecret)(c)
	require.False(t, c.IsAborted())
	authenticatedTenantID, ok := middleware.AuthenticatedTenantIDFromContext(c.Request.Context())
	require.True(t, ok)
	require.Zero(t, authenticatedTenantID)
	middleware.TenantMiddleware(client)(c)
	require.False(t, c.IsAborted())
	require.Equal(t, requestTenant.ID, c.GetInt("tenant_id"))
	require.Equal(t, "header", c.GetString("tenant_source"))

	_, _, ok = getBPMNTenantContext(c)
	assert.False(t, ok)
}

func TestProcessInstanceListAndStatsPassWorkflowContext(t *testing.T) {
	gin.SetMode(gin.TestMode)
	client := enttest.Open(t, "sqlite3", "file:bpmn_controller_instance_scope?mode=memory&cache=shared&_fk=1")
	t.Cleanup(func() { _ = client.Close() })
	instanceSvc := &contextCapturingProcessInstanceService{}
	controller := NewBPMNWorkflowController(&fakeProcessEngine{
		taskSvc:            &fakeTaskService{},
		processInstanceSvc: instanceSvc,
	}, nil)

	for path, handler := range map[string]gin.HandlerFunc{
		"/api/v1/bpmn/process-instances": controller.ListProcessInstances,
		"/api/v1/bpmn/stats/instances":   controller.GetInstanceStats,
	} {
		recorder := httptest.NewRecorder()
		ctx, _ := gin.CreateTestContext(recorder)
		ctx.Request = httptest.NewRequest(http.MethodGet, path, nil)
		ctx.Request = ctx.Request.WithContext(middleware.WithAuthenticatedTenantID(ctx.Request.Context(), 42))
		ctx.Set("tenant_id", 42)
		ctx.Set("user_id", 7)
		ctx.Set("role", "end_user")
		ctx.Set("client", client)

		handler(ctx)
		require.Equal(t, http.StatusOK, recorder.Code, recorder.Body.String())
	}

	for _, received := range []context.Context{instanceSvc.listCtx, instanceSvc.statsCtx} {
		require.NotNil(t, received)
		scope, err := service.BPMNAccessScopeFromContext(received)
		require.NoError(t, err)
		assert.Equal(t, 7, scope.UserID)
		assert.Equal(t, 42, scope.TenantID)
	}
}

func TestTaskListAndStatsPassWorkflowContext(t *testing.T) {
	gin.SetMode(gin.TestMode)
	client := enttest.Open(t, "sqlite3", "file:bpmn_controller_task_context?mode=memory&cache=shared&_fk=1")
	t.Cleanup(func() { _ = client.Close() })
	taskSvc := &fakeTaskService{}
	controller := NewBPMNWorkflowController(&fakeProcessEngine{taskSvc: taskSvc}, nil)

	for path, handler := range map[string]gin.HandlerFunc{
		"/api/v1/bpmn/tasks":       controller.ListUserTasks,
		"/api/v1/bpmn/stats/tasks": controller.GetTaskStats,
	} {
		recorder := httptest.NewRecorder()
		ctx, _ := gin.CreateTestContext(recorder)
		ctx.Request = httptest.NewRequest(http.MethodGet, path, nil)
		ctx.Request = ctx.Request.WithContext(middleware.WithAuthenticatedTenantID(ctx.Request.Context(), 42))
		ctx.Set("tenant_id", 42)
		ctx.Set("user_id", 7)
		ctx.Set("role", "end_user")
		ctx.Set("client", client)

		handler(ctx)
		require.Equal(t, http.StatusOK, recorder.Code, recorder.Body.String())
	}

	for _, received := range []context.Context{taskSvc.listCtx, taskSvc.statsCtx} {
		require.NotNil(t, received)
		scope, err := service.BPMNAccessScopeFromContext(received)
		require.NoError(t, err)
		assert.Equal(t, 7, scope.UserID)
		assert.Equal(t, 42, scope.TenantID)
	}
}

func TestListUserTasksHTTPRejectsFilterOverride(t *testing.T) {
	gin.SetMode(gin.TestMode)
	client := enttest.Open(t, "sqlite3", "file:bpmn_controller_task_scope?mode=memory&cache=shared&_fk=1")
	t.Cleanup(func() { _ = client.Close() })
	ctx := context.Background()
	tenant, err := client.Tenant.Create().
		SetCode("controller-task-scope").
		SetName("Controller Task Scope").
		SetStatus("active").
		Save(ctx)
	require.NoError(t, err)
	actor, err := client.User.Create().
		SetUsername("controller.actor").
		SetEmail("controller.actor@example.test").
		SetName("Controller Actor").
		SetPasswordHash("test").
		SetRole("end_user").
		SetActive(true).
		SetTenantID(tenant.ID).
		Save(ctx)
	require.NoError(t, err)
	other, err := client.User.Create().
		SetUsername("controller.other").
		SetEmail("controller.other@example.test").
		SetName("Controller Other").
		SetPasswordHash("test").
		SetRole("end_user").
		SetActive(true).
		SetTenantID(tenant.ID).
		Save(ctx)
	require.NoError(t, err)
	deployment, err := client.ProcessDeployment.Create().
		SetDeploymentID("controller-task-deployment").
		SetDeploymentName("Controller Task Deployment").
		SetTenantID(tenant.ID).
		Save(ctx)
	require.NoError(t, err)
	definition, err := client.ProcessDefinition.Create().
		SetKey("controller_task_scope").
		SetName("Controller Task Scope").
		SetBpmnXML([]byte("<definitions/>")).
		SetDeploymentID(deployment.ID).
		SetTenantID(tenant.ID).
		Save(ctx)
	require.NoError(t, err)
	instance, err := client.ProcessInstance.Create().
		SetProcessInstanceID("controller-task-instance").
		SetProcessDefinitionKey(definition.Key).
		SetProcessDefinitionID(definition.ID).
		SetTenantID(tenant.ID).
		Save(ctx)
	require.NoError(t, err)
	createTask := func(taskID, assignee string) {
		_, createErr := client.ProcessTask.Create().
			SetTaskID(taskID).
			SetProcessInstanceID(instance.ID).
			SetProcessDefinitionKey(definition.Key).
			SetTaskDefinitionKey(taskID).
			SetTaskName(taskID).
			SetAssignee(assignee).
			SetTenantID(tenant.ID).
			Save(ctx)
		require.NoError(t, createErr)
	}
	createTask("controller-task-mine", strconv.Itoa(actor.ID))
	createTask("controller-task-other", strconv.Itoa(other.ID))

	engine := service.NewCustomProcessEngine(client, zap.NewNop().Sugar())
	controller := NewBPMNWorkflowController(engine, nil)
	recorder := httptest.NewRecorder()
	ginCtx, _ := gin.CreateTestContext(recorder)
	path := "/api/v1/bpmn/tasks?userId=" + strconv.Itoa(other.ID) + "&Assignee=" + strconv.Itoa(other.ID)
	ginCtx.Request = httptest.NewRequest(http.MethodGet, path, nil)
	ginCtx.Request = ginCtx.Request.WithContext(middleware.WithAuthenticatedTenantID(ginCtx.Request.Context(), tenant.ID))
	ginCtx.Set("tenant_id", tenant.ID)
	ginCtx.Set("user_id", actor.ID)
	ginCtx.Set("role", "end_user")
	ginCtx.Set("client", client)

	controller.ListUserTasks(ginCtx)
	require.Equal(t, http.StatusOK, recorder.Code, recorder.Body.String())
	var response struct {
		Data struct {
			Data       []dto.BPMNTaskResponse `json:"data"`
			Pagination struct {
				Total int `json:"total"`
			} `json:"pagination"`
		} `json:"data"`
	}
	require.NoError(t, json.Unmarshal(recorder.Body.Bytes(), &response))
	require.Equal(t, 1, response.Data.Pagination.Total)
	require.Len(t, response.Data.Data, 1)
	assert.Equal(t, "controller-task-mine", response.Data.Data[0].TaskID)
}

type bpmnHTTPAuthorizationFixture struct {
	client      *ent.Client
	router      *gin.Engine
	tenant      *ent.Tenant
	otherTenant *ent.Tenant
	actors      map[string]*ent.User
	task        *ent.ProcessTask
}

func newBPMNHTTPAuthorizationFixture(t *testing.T) *bpmnHTTPAuthorizationFixture {
	t.Helper()
	gin.SetMode(gin.TestMode)
	client := enttest.Open(t, "sqlite3", fmt.Sprintf("file:bpmn_http_authorization_%d?mode=memory&cache=shared&_fk=1", time.Now().UnixNano()))
	t.Cleanup(func() { require.NoError(t, client.Close()) })
	middleware.InvalidateAllPermissionCaches()
	t.Cleanup(middleware.InvalidateAllPermissionCaches)

	dbCtx := context.Background()
	createTenant := func(code string) *ent.Tenant {
		tenant, err := client.Tenant.Create().SetCode(code).SetName(code).SetStatus("active").Save(dbCtx)
		require.NoError(t, err)
		return tenant
	}
	tenant := createTenant("bpmn-http-auth")
	otherTenant := createTenant("bpmn-http-auth-other")
	createUser := func(name, role string, tenantID int) *ent.User {
		user, err := client.User.Create().
			SetUsername(name).
			SetEmail(name + "@example.test").
			SetName(name).
			SetPasswordHash("test").
			SetRole(role).
			SetActive(true).
			SetTenantID(tenantID).
			Save(dbCtx)
		require.NoError(t, err)
		return user
	}
	actors := map[string]*ent.User{
		"participant":         createUser("http.participant", "end_user", tenant.ID),
		"outsider":            createUser("http.outsider", "end_user", tenant.ID),
		"other_tenant":        createUser("http.other.tenant", "end_user", otherTenant.ID),
		"other_tenant_reader": createUser("http.other.tenant.reader", "super_admin", otherTenant.ID),
		"instance_updater":    createUser("http.instance.updater", "super_admin", tenant.ID),
		"instance_reader":     createUser("http.instance.reader", "super_admin", tenant.ID),
		"task_reader":         createUser("http.task.reader", "super_admin", tenant.ID),
		"task_owner":          createUser("http.task.owner", "end_user", tenant.ID),
	}

	deployment, err := client.ProcessDeployment.Create().
		SetDeploymentID("HTTP-AUTH-DEPLOYMENT").
		SetDeploymentName("HTTP authorization deployment").
		SetTenantID(tenant.ID).
		Save(dbCtx)
	require.NoError(t, err)
	definition, err := client.ProcessDefinition.Create().
		SetKey("http_authorization").
		SetName("HTTP authorization").
		SetBpmnXML([]byte("<definitions/>")).
		SetDeploymentID(deployment.ID).
		SetTenantID(tenant.ID).
		Save(dbCtx)
	require.NoError(t, err)
	instance, err := client.ProcessInstance.Create().
		SetProcessInstanceID("PI-1").
		SetProcessDefinitionKey(definition.Key).
		SetProcessDefinitionID(definition.ID).
		SetCurrentActivityID("approval").
		SetCurrentActivityName("Approval").
		SetVariables(map[string]interface{}{"privateVariable": "task-variable-secret"}).
		SetTenantID(tenant.ID).
		Save(dbCtx)
	require.NoError(t, err)
	_, err = client.ProcessTask.Create().
		SetTaskID("TASK-PARTICIPANT").
		SetProcessInstanceID(instance.ID).
		SetProcessDefinitionKey(definition.Key).
		SetTaskDefinitionKey("participant-task").
		SetTaskName("Participant task").
		SetCandidateUsers(actors["participant"].Username).
		SetTenantID(tenant.ID).
		Save(dbCtx)
	require.NoError(t, err)
	task, err := client.ProcessTask.Create().
		SetTaskID("TASK-OTHER-ACTOR").
		SetProcessInstanceID(instance.ID).
		SetProcessDefinitionKey(definition.Key).
		SetTaskDefinitionKey("other-actor-task").
		SetTaskName("Other actor task").
		SetAssignee(strconv.Itoa(actors["task_owner"].ID)).
		SetCandidateGroups("sensitive-candidate-expression").
		SetTaskVariables(map[string]interface{}{"privateVariable": "task-variable-secret"}).
		SetTenantID(tenant.ID).
		Save(dbCtx)
	require.NoError(t, err)

	engine := service.NewCustomProcessEngine(client, zap.NewNop().Sugar())
	controller := NewBPMNWorkflowController(engine, nil)
	router := gin.New()
	api := router.Group("/api/v1")
	api.Use(func(ctx *gin.Context) {
		actorName := ctx.GetHeader("X-Test-Actor")
		actor := actors[actorName]
		require.NotNil(t, actor, "unknown test actor %q", actorName)
		actorTenant := tenant
		if actorName == "other_tenant" || actorName == "other_tenant_reader" {
			actorTenant = otherTenant
		}
		requestCtx := middleware.WithAuthenticatedTenantID(ctx.Request.Context(), actorTenant.ID)
		ctx.Request = ctx.Request.WithContext(requestCtx)
		ctx.Set("tenant_id", actorTenant.ID)
		ctx.Set("user_id", actor.ID)
		ctx.Set("role", actor.Role)
		ctx.Set("client", client)
		ctx.Next()
	})
	controller.RegisterRoutes(api)
	workflow := api.Group("/workflow")
	workflow.GET("/instances", middleware.RequirePermission("process_instance", "read"), controller.ListProcessInstances)
	workflow.GET("/instances/:id", middleware.RequirePermission("process_instance", "read"), controller.GetProcessInstance)
	workflow.POST("/instances", middleware.RequirePermission("process_instance", "create"), controller.StartProcess)
	workflow.PUT("/instances/:id/terminate", middleware.RequirePermission("process_instance", "update"), controller.TerminateProcess)
	workflow.PUT("/instances/:id/suspend", middleware.RequirePermission("process_instance", "update"), controller.SuspendProcess)
	workflow.PUT("/instances/:id/resume", middleware.RequirePermission("process_instance", "update"), controller.ResumeProcess)
	workflow.GET("/tasks", middleware.RequirePermission("task", "read"), controller.ListUserTasks)
	workflow.PUT("/tasks/:id/complete", middleware.RequirePermission("task", "update"), controller.CompleteTask)
	workflow.POST("/tasks/:id/claim", middleware.RequirePermission("task", "update"), controller.ClaimTask)

	return &bpmnHTTPAuthorizationFixture{
		client: client, router: router, tenant: tenant, otherTenant: otherTenant, actors: actors, task: task,
	}
}

func (f *bpmnHTTPAuthorizationFixture) seedTaskForOtherActor() *ent.ProcessTask {
	return f.task
}

func (f *bpmnHTTPAuthorizationFixture) doAsActor(t *testing.T, actor, method, path, body string) *httptest.ResponseRecorder {
	t.Helper()
	if body == "" && method == http.MethodPut {
		body = `{"reason":"maintenance"}`
	}
	recorder := httptest.NewRecorder()
	request := httptest.NewRequest(method, path, bytes.NewBufferString(body))
	request.Header.Set("Content-Type", "application/json")
	request.Header.Set("X-Test-Actor", actor)
	f.router.ServeHTTP(recorder, request)
	return recorder
}

func assertBPMNDenialBodyIsSafe(t *testing.T, response *httptest.ResponseRecorder, secrets ...string) {
	t.Helper()
	body := strings.ToLower(response.Body.String())
	for _, secret := range secrets {
		assert.NotContains(t, body, strings.ToLower(secret), response.Body.String())
	}
}

func TestBPMNTaskHTTPAuthorizationStatus(t *testing.T) {
	f := newBPMNHTTPAuthorizationFixture(t)
	task := f.seedTaskForOtherActor()
	cases := []struct{ method, path, body string }{
		{http.MethodGet, "/api/v1/bpmn/tasks/" + task.TaskID, ""},
		{http.MethodPut, "/api/v1/bpmn/tasks/" + task.TaskID + "/assign", `{"assignee":"7"}`},
		{http.MethodPut, "/api/v1/bpmn/tasks/" + task.TaskID + "/cancel", `{"reason":"x"}`},
		{http.MethodPut, "/api/v1/bpmn/tasks/" + task.TaskID + "/variables", `{"variables":{"x":1}}`},
	}
	for _, tc := range cases {
		resp := f.doAsActor(t, "outsider", tc.method, tc.path, tc.body)
		assert.Equal(t, http.StatusForbidden, resp.Code, tc.path+": "+resp.Body.String())
		assertBPMNDenialBodyIsSafe(t, resp,
			f.tenant.Code, f.otherTenant.Code,
			fmt.Sprintf(`"tenantId":%d`, f.tenant.ID), fmt.Sprintf(`"tenantId":%d`, f.otherTenant.ID),
			fmt.Sprintf("tenant_id=%d", f.tenant.ID), fmt.Sprintf("tenant_id=%d", f.otherTenant.ID),
			"sensitive-candidate-expression", "select ", "sql", "task-variable-secret", "privateVariable",
		)
	}
}

func TestBPMNInstanceAndRouteHTTPAuthorizationStatus(t *testing.T) {
	f := newBPMNHTTPAuthorizationFixture(t)
	cases := []struct {
		actor, method, path string
		want                int
	}{
		{"participant", http.MethodGet, "/api/v1/bpmn/process-instances/PI-1", http.StatusOK},
		{"outsider", http.MethodGet, "/api/v1/bpmn/process-instances/PI-1", http.StatusForbidden},
		{"other_tenant", http.MethodGet, "/api/v1/bpmn/process-instances/PI-1", http.StatusNotFound},
		{"outsider", http.MethodGet, "/api/v1/bpmn/process-instances/PI-1/approval-history", http.StatusForbidden},
		{"other_tenant", http.MethodGet, "/api/v1/bpmn/process-instances/PI-1/approval-history", http.StatusNotFound},
		{"instance_updater", http.MethodPut, "/api/v1/bpmn/process-instances/PI-1/suspend", http.StatusOK},
		{"participant", http.MethodPut, "/api/v1/bpmn/process-instances/PI-1/suspend", http.StatusForbidden},
		{"participant", http.MethodGet, "/api/v1/bpmn/stats/instances", http.StatusForbidden},
		{"task_reader", http.MethodGet, "/api/v1/bpmn/stats/tasks", http.StatusOK},
		{"participant", http.MethodGet, "/api/v1/workflow/instances", http.StatusForbidden},
		{"instance_reader", http.MethodGet, "/api/v1/workflow/instances", http.StatusOK},
		{"other_tenant_reader", http.MethodGet, "/api/v1/workflow/instances/PI-1", http.StatusNotFound},
	}
	for _, tc := range cases {
		t.Run(tc.actor+"_"+tc.method+"_"+strings.TrimPrefix(tc.path, "/api/v1/"), func(t *testing.T) {
			resp := f.doAsActor(t, tc.actor, tc.method, tc.path, "")
			assert.Equal(t, tc.want, resp.Code, tc.path+": "+resp.Body.String())
			if tc.want == http.StatusForbidden || tc.want == http.StatusNotFound {
				assertBPMNDenialBodyIsSafe(t, resp,
					f.tenant.Code, f.otherTenant.Code,
					fmt.Sprintf(`"tenantId":%d`, f.tenant.ID), fmt.Sprintf(`"tenantId":%d`, f.otherTenant.ID),
					fmt.Sprintf("tenant_id=%d", f.tenant.ID), fmt.Sprintf("tenant_id=%d", f.otherTenant.ID),
					"sensitive-candidate-expression", "select ", "sql", "task-variable-secret", "privateVariable",
				)
			}
		})
	}
}
