package controller

import (
	"net/http"
	"net/http/httptest"
	"testing"

	"itsm-backend/authorization"
	"itsm-backend/ent/enttest"

	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/assert"
)

func TestSLATemplateController_RoutesRequireSLARead(t *testing.T) {
	gin.SetMode(gin.TestMode)

	// Create a test database client
	client := enttest.Open(t, "sqlite3", "file:sla_template_test_read?mode=memory&cache=shared&_fk=1")
	defer client.Close()

	// Set permission config to DBOnly mode for strict testing
	authorization.PermissionConfig.Mode = authorization.PermissionConfigModeDBOnly

	c := &SLATemplateController{}
	r := gin.New()
	group := r.Group("/api/v1")
	group.Use(func(ctx *gin.Context) {
		ctx.Set("role", "end_user")
		ctx.Set("tenant_id", 1)
		ctx.Set("client", client)
	})
	c.RegisterRoutes(group)

	w := httptest.NewRecorder()
	req, _ := http.NewRequest("GET", "/api/v1/sla/templates", nil)
	r.ServeHTTP(w, req)

	// end_user has no sla:read grant in a nil-client DBOnly lookup — expect
	// the request to be rejected before reaching the (nil-service) handler.
	assert.Equal(t, http.StatusForbidden, w.Code)
}

func TestSLATemplateController_InstallRequiresSLAWrite(t *testing.T) {
	gin.SetMode(gin.TestMode)

	// Create a test database client
	client := enttest.Open(t, "sqlite3", "file:sla_template_test_write?mode=memory&cache=shared&_fk=1")
	defer client.Close()

	// Set permission config to DBOnly mode for strict testing
	authorization.PermissionConfig.Mode = authorization.PermissionConfigModeDBOnly

	c := &SLATemplateController{}
	r := gin.New()
	group := r.Group("/api/v1")
	group.Use(func(ctx *gin.Context) {
		ctx.Set("role", "end_user")
		ctx.Set("tenant_id", 1)
		ctx.Set("client", client)
	})
	c.RegisterRoutes(group)

	w := httptest.NewRecorder()
	req, _ := http.NewRequest("POST", "/api/v1/sla/templates/some-key/install", nil)
	r.ServeHTTP(w, req)

	assert.Equal(t, http.StatusForbidden, w.Code)
}
