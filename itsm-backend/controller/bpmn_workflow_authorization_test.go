package controller

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"itsm-backend/ent"
	"itsm-backend/ent/enttest"
	"itsm-backend/middleware"
	"itsm-backend/service"

	"github.com/gin-gonic/gin"
	_ "github.com/mattn/go-sqlite3"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
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
