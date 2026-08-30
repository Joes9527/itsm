package controller

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"itsm-backend/ent/enttest"
	"itsm-backend/middleware"
	"itsm-backend/service"

	"github.com/gin-gonic/gin"
	_ "github.com/mattn/go-sqlite3"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

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
