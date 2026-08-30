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

func seedBPMNRolePermissions(t *testing.T, client *ent.Client, tenantID int, roleCode string, permissions ...string) {
	t.Helper()
	dbCtx := context.Background()
	role, err := client.Role.Create().
		SetName(roleCode).
		SetCode(roleCode).
		SetTenantID(tenantID).
		Save(dbCtx)
	require.NoError(t, err)
	for _, permission := range permissions {
		parts := strings.SplitN(permission, ":", 2)
		require.Len(t, parts, 2)
		definition, createErr := client.Permission.Create().
			SetCode(roleCode + ":" + permission).
			SetName(permission).
			SetResource(parts[0]).
			SetAction(parts[1]).
			SetTenantID(tenantID).
			Save(dbCtx)
		require.NoError(t, createErr)
		_, createErr = client.RolePermission.Create().
			SetRoleID(role.ID).
			SetPermissionID(definition.ID).
			SetTenantID(tenantID).
			Save(dbCtx)
		require.NoError(t, createErr)
	}
	middleware.InvalidateAllPermissionCaches()
}

func TestGetBPMNTenantContextBuildsSelectiveOrdinaryRoleScope(t *testing.T) {
	cases := []struct {
		name        string
		role        string
		permissions []string
		setRole     bool
		setClient   bool
		want        service.BPMNAccessScope
	}{
		{
			name:        "selective permissions",
			role:        "change_manager",
			permissions: []string{"process_instance:read", "task:update"},
			setRole:     true,
			setClient:   true,
			want: service.BPMNAccessScope{
				CanReadAllInstances: true,
				CanUpdateAllTasks:   true,
			},
		},
		{name: "ordinary role without permissions", role: "end_user", setRole: true, setClient: true},
		{name: "missing role", setClient: true},
		{name: "missing client", role: "change_manager", permissions: []string{"process_instance:read", "task:update"}, setRole: true},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			gin.SetMode(gin.TestMode)
			client := enttest.Open(t, "sqlite3", fmt.Sprintf("file:bpmn_scope_%d?mode=memory&cache=shared&_fk=1", time.Now().UnixNano()))
			t.Cleanup(func() { require.NoError(t, client.Close()) })
			t.Cleanup(middleware.InvalidateAllPermissionCaches)
			dbCtx := context.Background()
			tenant, err := client.Tenant.Create().SetCode("scope-tenant").SetName("scope-tenant").SetStatus("active").Save(dbCtx)
			require.NoError(t, err)
			if len(tc.permissions) > 0 {
				seedBPMNRolePermissions(t, client, tenant.ID, tc.role, tc.permissions...)
			}

			c, _ := gin.CreateTestContext(httptest.NewRecorder())
			c.Request = httptest.NewRequest(http.MethodGet, "/api/v1/bpmn/tasks", nil)
			c.Request = c.Request.WithContext(middleware.WithAuthenticatedTenantID(c.Request.Context(), tenant.ID))
			c.Set("tenant_id", tenant.ID)
			c.Set("user_id", 7)
			if tc.setRole {
				c.Set("role", tc.role)
			}
			if tc.setClient {
				c.Set("client", client)
			}

			workflowCtx, _, ok := getBPMNTenantContext(c)
			require.True(t, ok)
			scope, err := service.BPMNAccessScopeFromContext(workflowCtx)
			require.NoError(t, err)
			assert.Equal(t, 7, scope.UserID)
			assert.Equal(t, tenant.ID, scope.TenantID)
			assert.Equal(t, tc.want.CanReadAllInstances, scope.CanReadAllInstances)
			assert.Equal(t, tc.want.CanUpdateAllInstances, scope.CanUpdateAllInstances)
			assert.Equal(t, tc.want.CanReadAllTasks, scope.CanReadAllTasks)
			assert.Equal(t, tc.want.CanUpdateAllTasks, scope.CanUpdateAllTasks)
		})
	}
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
	instance    *ent.ProcessInstance
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
	allPermissions := []string{
		"process_instance:read", "process_instance:update", "task:read", "task:update",
	}
	seedBPMNRolePermissions(t, client, tenant.ID, "sysadmin", allPermissions...)
	seedBPMNRolePermissions(t, client, otherTenant.ID, "sysadmin", allPermissions...)
	seedBPMNRolePermissions(t, client, tenant.ID, "change_manager", "process_instance:read")
	seedBPMNRolePermissions(t, client, tenant.ID, "dept_manager", "task:read")
	actors := map[string]*ent.User{
		"participant":     createUser("http.participant", "end_user", tenant.ID),
		"outsider":        createUser("http.outsider", "end_user", tenant.ID),
		"elevated":        createUser("http.elevated", "sysadmin", tenant.ID),
		"instance_reader": createUser("http.instance.reader", "change_manager", tenant.ID),
		"task_reader":     createUser("http.task.reader", "dept_manager", tenant.ID),
		"cross_tenant":    createUser("http.cross.tenant", "sysadmin", otherTenant.ID),
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
		SetBpmnXML([]byte(`<?xml version="1.0" encoding="UTF-8"?>
<bpmn:definitions xmlns:bpmn="http://www.omg.org/spec/BPMN/20100524/MODEL" targetNamespace="http://bpmn.io/schema/bpmn">
  <bpmn:process id="http_authorization" isExecutable="true">
    <bpmn:startEvent id="start" />
    <bpmn:userTask id="participant-task" name="Participant task" />
    <bpmn:endEvent id="end" />
    <bpmn:sequenceFlow id="to-task" sourceRef="start" targetRef="participant-task" />
    <bpmn:sequenceFlow id="to-end" sourceRef="participant-task" targetRef="end" />
  </bpmn:process>
</bpmn:definitions>`)).
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
	task, err := client.ProcessTask.Create().
		SetTaskID("TASK-PARTICIPANT").
		SetProcessInstanceID(instance.ID).
		SetProcessDefinitionKey(definition.Key).
		SetTaskDefinitionKey("participant-task").
		SetTaskName("Participant task").
		SetCandidateUsers(actors["participant"].Username).
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
		if actorName == "cross_tenant" {
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
	controller.RegisterWorkflowAliasRoutes(api)

	return &bpmnHTTPAuthorizationFixture{
		client: client, router: router, tenant: tenant, otherTenant: otherTenant, actors: actors, instance: instance, task: task,
	}
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

type bpmnAuthorizationOperation struct {
	method  string
	path    func(*bpmnHTTPAuthorizationFixture) string
	body    func(*bpmnHTTPAuthorizationFixture) string
	prepare func(*testing.T, *bpmnHTTPAuthorizationFixture, string)
	want    map[string]int
}

func bpmnAuthorizationOperations() map[string]bpmnAuthorizationOperation {
	instanceRead := map[string]int{"participant": http.StatusOK, "outsider": http.StatusForbidden, "elevated": http.StatusOK, "cross_tenant": http.StatusNotFound}
	instanceUpdate := map[string]int{"participant": http.StatusForbidden, "outsider": http.StatusForbidden, "elevated": http.StatusOK, "cross_tenant": http.StatusNotFound}
	taskRead := map[string]int{"participant": http.StatusOK, "outsider": http.StatusForbidden, "elevated": http.StatusOK, "cross_tenant": http.StatusNotFound}
	taskUpdate := map[string]int{"participant": http.StatusOK, "outsider": http.StatusForbidden, "elevated": http.StatusOK, "cross_tenant": http.StatusNotFound}
	instancePath := func(suffix string) func(*bpmnHTTPAuthorizationFixture) string {
		return func(*bpmnHTTPAuthorizationFixture) string { return "/api/v1/bpmn/process-instances/PI-1" + suffix }
	}
	taskPath := func(suffix string) func(*bpmnHTTPAuthorizationFixture) string {
		return func(f *bpmnHTTPAuthorizationFixture) string { return "/api/v1/bpmn/tasks/" + f.task.TaskID + suffix }
	}
	allLists := map[string]int{"participant": http.StatusOK, "outsider": http.StatusOK, "elevated": http.StatusOK, "cross_tenant": http.StatusOK}
	taskLists := map[string]int{"participant": http.StatusOK, "outsider": http.StatusOK, "elevated": http.StatusOK, "cross_tenant": http.StatusNotFound}
	aggregate := map[string]int{"participant": http.StatusForbidden, "outsider": http.StatusForbidden, "elevated": http.StatusOK, "cross_tenant": http.StatusOK}

	return map[string]bpmnAuthorizationOperation{
		"list_instances": {method: http.MethodGet, path: func(*bpmnHTTPAuthorizationFixture) string {
			return "/api/v1/bpmn/process-instances"
		}, want: allLists},
		"get_instance":     {method: http.MethodGet, path: instancePath(""), want: instanceRead},
		"approval_history": {method: http.MethodGet, path: instancePath("/approval-history"), want: instanceRead},
		"instance_variables": {
			method: http.MethodPut, path: instancePath("/variables"),
			body: func(*bpmnHTTPAuthorizationFixture) string { return "{\"variables\":{\"matrix\":true}}" }, want: instanceUpdate,
		},
		"instance_stats": {method: http.MethodGet, path: func(*bpmnHTTPAuthorizationFixture) string {
			return "/api/v1/bpmn/stats/instances"
		}, want: aggregate},
		"suspend_instance": {
			method: http.MethodPut, path: instancePath("/suspend"),
			body: func(*bpmnHTTPAuthorizationFixture) string { return "{\"reason\":\"maintenance\"}" }, want: instanceUpdate,
		},
		"resume_instance": {
			method: http.MethodPut, path: instancePath("/resume"), want: instanceUpdate,
			prepare: func(t *testing.T, f *bpmnHTTPAuthorizationFixture, _ string) {
				_, err := f.client.ProcessInstance.UpdateOne(f.instance).SetStatus("suspended").Save(context.Background())
				require.NoError(t, err)
			},
		},
		"terminate_instance": {
			method: http.MethodPut, path: instancePath("/terminate"),
			body: func(*bpmnHTTPAuthorizationFixture) string { return "{\"reason\":\"cancelled\"}" }, want: instanceUpdate,
		},
		"list_tasks_with_override": {method: http.MethodGet, path: func(f *bpmnHTTPAuthorizationFixture) string {
			outsiderID := strconv.Itoa(f.actors["outsider"].ID)
			return "/api/v1/bpmn/tasks?userId=" + outsiderID + "&Assignee=" + outsiderID
		}, want: taskLists},
		"get_task": {method: http.MethodGet, path: taskPath(""), want: taskRead},
		"task_stats": {method: http.MethodGet, path: func(*bpmnHTTPAuthorizationFixture) string {
			return "/api/v1/bpmn/stats/tasks"
		}, want: aggregate},
		"assign_task": {
			method: http.MethodPut, path: taskPath("/assign"),
			body: func(f *bpmnHTTPAuthorizationFixture) string {
				return fmt.Sprintf("{\"assignee\":\"%d\"}", f.actors["outsider"].ID)
			}, want: taskUpdate,
		},
		"cancel_task": {
			method: http.MethodPut, path: taskPath("/cancel"),
			body: func(*bpmnHTTPAuthorizationFixture) string { return "{\"reason\":\"cancelled\"}" }, want: taskUpdate,
		},
		"set_task_variables": {
			method: http.MethodPut, path: taskPath("/variables"),
			body: func(*bpmnHTTPAuthorizationFixture) string { return "{\"variables\":{\"matrix\":true}}" }, want: taskUpdate,
		},
		"create_counter_sign": {
			method: http.MethodPost, path: taskPath("/counter-sign"),
			body: func(f *bpmnHTTPAuthorizationFixture) string {
				return fmt.Sprintf("{\"approvers\":[\"%d\"],\"approvalType\":\"parallel\",\"threshold\":1}", f.actors["participant"].ID)
			}, want: taskUpdate,
		},
		"get_counter_sign": {method: http.MethodGet, path: taskPath("/counter-sign-status"), want: taskRead},
		"claim_task":       {method: http.MethodPut, path: taskPath("/claim"), want: taskUpdate},
		"complete_task": {
			method: http.MethodPut, path: taskPath("/complete"),
			body: func(*bpmnHTTPAuthorizationFixture) string { return "{\"variables\":{\"approved\":true}}" }, want: taskUpdate,
		},
		"submit_decision": {
			method: http.MethodPost, path: taskPath("/decisions"),
			body: func(*bpmnHTTPAuthorizationFixture) string { return "{\"action\":\"approve\",\"comment\":\"matrix\"}" }, want: taskUpdate,
		},
		"vote": {
			method: http.MethodPut, path: taskPath("/vote"),
			body: func(*bpmnHTTPAuthorizationFixture) string { return "{\"approved\":true,\"comment\":\"matrix\"}" }, want: taskUpdate,
			prepare: func(t *testing.T, f *bpmnHTTPAuthorizationFixture, _ string) {
				assignee := f.actors["participant"]
				_, err := f.client.ProcessTask.UpdateOne(f.task).
					SetAssignee(strconv.Itoa(assignee.ID)).
					SetStatus("assigned").
					Save(context.Background())
				require.NoError(t, err)
			},
		},
	}
}

func runBPMNAuthorizationMatrixCase(t *testing.T, actor, operation string, wantStatus int) {
	t.Helper()
	f := newBPMNHTTPAuthorizationFixture(t)
	op, ok := bpmnAuthorizationOperations()[operation]
	require.True(t, ok, "unknown BPMN authorization operation %q", operation)
	if op.prepare != nil {
		op.prepare(t, f, actor)
	}
	body := ""
	if op.body != nil {
		body = op.body(f)
	}
	response := f.doAsActor(t, actor, op.method, op.path(f), body)
	require.Equal(t, wantStatus, response.Code, response.Body.String())
	if wantStatus == http.StatusForbidden || wantStatus == http.StatusNotFound {
		assertBPMNDenialBodyIsSafe(t, response,
			f.tenant.Code, f.otherTenant.Code,
			fmt.Sprintf(`"tenantId":%d`, f.tenant.ID), fmt.Sprintf(`"tenantId":%d`, f.otherTenant.ID),
			fmt.Sprintf("tenant_id=%d", f.tenant.ID), fmt.Sprintf("tenant_id=%d", f.otherTenant.ID),
			"sensitive-candidate-expression", "select ", "sql", "task-variable-secret", "privateVariable",
		)
	}
}

func TestBPMNAuthorizationMatrix(t *testing.T) {
	operations := []string{
		"list_instances", "get_instance", "approval_history", "instance_variables",
		"instance_stats", "suspend_instance", "resume_instance",
		"terminate_instance", "list_tasks_with_override", "get_task",
		"task_stats", "assign_task", "cancel_task", "set_task_variables",
		"create_counter_sign", "get_counter_sign", "claim_task", "complete_task",
		"submit_decision", "vote",
	}
	actors := []string{"participant", "outsider", "elevated", "cross_tenant"}
	operationMap := bpmnAuthorizationOperations()
	require.Len(t, operationMap, len(operations))
	for _, operation := range operations {
		op, ok := operationMap[operation]
		require.True(t, ok, "operation %q is missing from the route matrix", operation)
		for _, actor := range actors {
			wantStatus, ok := op.want[actor]
			require.True(t, ok, "operation %q has no %s expectation", operation, actor)
			t.Run(operation+"/"+actor, func(t *testing.T) {
				runBPMNAuthorizationMatrixCase(t, actor, operation, wantStatus)
			})
		}
	}
}

func TestBPMNWorkflowAliasesRetainStricterRBAC(t *testing.T) {
	cases := []struct {
		name, actor, path string
		want              int
	}{
		{"participant instance alias denied", "participant", "/api/v1/workflow/instances", http.StatusForbidden},
		{"instance reader alias allowed", "instance_reader", "/api/v1/workflow/instances", http.StatusOK},
		{"task reader cannot use instance alias", "task_reader", "/api/v1/workflow/instances", http.StatusForbidden},
		{"participant task alias denied", "participant", "/api/v1/workflow/tasks", http.StatusForbidden},
		{"task reader alias allowed", "task_reader", "/api/v1/workflow/tasks", http.StatusOK},
		{"instance reader cannot use task alias", "instance_reader", "/api/v1/workflow/tasks", http.StatusForbidden},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			f := newBPMNHTTPAuthorizationFixture(t)
			response := f.doAsActor(t, tc.actor, http.MethodGet, tc.path, "")
			require.Equal(t, tc.want, response.Code, response.Body.String())
			if tc.want == http.StatusForbidden {
				assertBPMNDenialBodyIsSafe(t, response, f.tenant.Code, f.otherTenant.Code, "sensitive-candidate-expression", "select ", "sql", "task-variable-secret", "privateVariable")
			}
		})
	}
}
