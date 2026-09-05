package controller

import (
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"itsm-backend/ent"
	"itsm-backend/ent/enttest"
	"itsm-backend/service"
)

func newTestTriggerController() *BPMNProcessTriggerController {
	return &BPMNProcessTriggerController{}
}

func TestBPMNProcessTriggerController_InvalidBindingBodyDoesNotEchoInput(t *testing.T) {
	gin.SetMode(gin.TestMode)
	recorder := httptest.NewRecorder()
	ctx, _ := gin.CreateTestContext(recorder)
	ctx.Request = httptest.NewRequest(http.MethodPost, "/api/v1/process-bindings", strings.NewReader(
		`{"processDefinitionKey":"binding-body-secret","broken":`,
	))
	ctx.Request.Header.Set("Content-Type", "application/json")

	newTestTriggerController().CreateBinding(ctx)

	assert.Contains(t, recorder.Body.String(), "流程绑定请求格式无效")
	assert.NotContains(t, recorder.Body.String(), "binding-body-secret")
}

func TestBPMNProcessTriggerController_InstanceStatusDoesNotUseLegacyRoleGate(t *testing.T) {
	gin.SetMode(gin.TestMode)
	c := newTestTriggerController()
	r := gin.New()
	group := r.Group("/api/v1")
	group.Use(func(ctx *gin.Context) { ctx.Set("role", "l1_support") })
	c.RegisterRoutes(group)

	w := httptest.NewRecorder()
	req, _ := http.NewRequest("GET", "/api/v1/process-trigger/status/123", nil)
	r.ServeHTTP(w, req)

	// Status now reaches the authoritative instance-scope handler. This fixture
	// deliberately omits authenticated tenant/actor context, so it fails there
	// (401) rather than at the obsolete coarse role gate (403).
	assert.Equal(t, http.StatusUnauthorized, w.Code)
}

func TestBPMNProcessTriggerController_BindingsRead_UsesBPMNRoleGate(t *testing.T) {
	gin.SetMode(gin.TestMode)
	c := newTestTriggerController()
	r := gin.New()
	group := r.Group("/api/v1")
	group.Use(func(ctx *gin.Context) { ctx.Set("role", "l1_support") })
	c.RegisterRoutes(group)

	w := httptest.NewRecorder()
	req, _ := http.NewRequest("GET", "/api/v1/process-bindings", nil)
	r.ServeHTTP(w, req)

	assert.Equal(t, http.StatusForbidden, w.Code)
}

func TestBPMNProcessTriggerController_BindingsUpdate_RequiresSuperAdminEvenForBPMNRole(t *testing.T) {
	gin.SetMode(gin.TestMode)
	c := newTestTriggerController()
	r := gin.New()
	group := r.Group("/api/v1")
	// change_manager passes the group-level BPMN gate but must still be
	// rejected on PUT/DELETE /process-bindings/:id — those are D-group
	// (super_admin only), stricter than the surrounding C-group routes.
	group.Use(func(ctx *gin.Context) { ctx.Set("role", "change_manager") })
	c.RegisterRoutes(group)

	w := httptest.NewRecorder()
	req, _ := http.NewRequest("PUT", "/api/v1/process-bindings/123", nil)
	r.ServeHTTP(w, req)

	assert.Equal(t, http.StatusForbidden, w.Code)
}

func TestBPMNProcessTriggerController_BindingsDelete_SuperAdminPasses(t *testing.T) {
	gin.SetMode(gin.TestMode)
	ctx := context.Background()
	client := enttest.Open(t, "sqlite3", fmt.Sprintf("file:%s?mode=memory&cache=shared&_fk=1", t.Name()))
	t.Cleanup(func() { require.NoError(t, client.Close()) })
	tenant := client.Tenant.Create().SetName("Binding tenant").SetCode("binding-tenant").SaveX(ctx)
	binding := client.ProcessBinding.Create().SetTenantID(tenant.ID).SetBusinessType("incident").SetProcessDefinitionKey("incident-flow").SetIsActive(true).SaveX(ctx)
	c := NewBPMNProcessTriggerController(nil, service.NewProcessBindingService(client), nil)
	r := gin.New()
	group := r.Group("/api/v1")
	group.Use(func(ctx *gin.Context) {
		ctx.Set("user_id", 1)
		ctx.Set("role", "super_admin")
		ctx.Set("tenant_id", tenant.ID)
	})
	c.RegisterRoutes(group)

	w := httptest.NewRecorder()
	req, _ := http.NewRequest("DELETE", fmt.Sprintf("/api/v1/process-bindings/%d", binding.ID), nil)
	r.ServeHTTP(w, req)

	require.Equal(t, http.StatusOK, w.Code, w.Body.String())
	_, err := client.ProcessBinding.Get(ctx, binding.ID)
	require.True(t, ent.IsNotFound(err), "configured binding must be deleted")
}

func TestBPMNProcessTriggerController_Departments_RequiresSuperAdminOnly(t *testing.T) {
	gin.SetMode(gin.TestMode)
	c := newTestTriggerController()
	r := gin.New()
	group := r.Group("/api/v1")
	group.Use(func(ctx *gin.Context) { ctx.Set("role", "change_manager") })
	c.RegisterRoutes(group)

	w := httptest.NewRecorder()
	req, _ := http.NewRequest("GET", "/api/v1/departments/1/processes", nil)
	r.ServeHTTP(w, req)

	assert.Equal(t, http.StatusForbidden, w.Code)
}

func TestBPMNProcessTriggerController_DomainConfigs_RequiresSuperAdminOnly(t *testing.T) {
	gin.SetMode(gin.TestMode)
	c := newTestTriggerController()
	r := gin.New()
	group := r.Group("/api/v1")
	group.Use(func(ctx *gin.Context) { ctx.Set("role", "change_manager") })
	c.RegisterRoutes(group)

	w := httptest.NewRecorder()
	req, _ := http.NewRequest("GET", "/api/v1/domain-configs", nil)
	r.ServeHTTP(w, req)

	assert.Equal(t, http.StatusForbidden, w.Code)
}
