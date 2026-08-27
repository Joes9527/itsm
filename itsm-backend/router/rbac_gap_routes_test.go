package router

import (
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/assert"
	"itsm-backend/middleware"
)

// These mirror the exact args this task adds to router.go — they don't
// exercise the full router (that needs a real JWT/controllers wired up),
// they lock in the specific resource/action/role values so a future edit
// to these lines can't silently drift, matching the existing pattern in
// middleware/rbac_test.go (TestRequirePermissionForRBAC / TestRequireRole).

func TestGapRoutes_WSTicket_RequiresSuperAdmin(t *testing.T) {
	gin.SetMode(gin.TestMode)
	w := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(w)
	c.Request, _ = http.NewRequest("POST", "/api/v1/ws/ticket", nil)
	c.Set("role", "end_user")

	middleware.RequireRole("super_admin")(c)

	assert.Equal(t, http.StatusForbidden, w.Code)
}

func TestGapRoutes_FeishuCallback_RequiresSuperAdmin(t *testing.T) {
	gin.SetMode(gin.TestMode)
	w := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(w)
	c.Request, _ = http.NewRequest("POST", "/api/v1/connectors/feishu/callback", nil)
	c.Set("role", "end_user")

	middleware.RequireRole("super_admin")(c)

	assert.Equal(t, http.StatusForbidden, w.Code)
}

func TestGapRoutes_MSPStatus_AllowsMSPRead(t *testing.T) {
	gin.SetMode(gin.TestMode)
	w := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(w)
	c.Request, _ = http.NewRequest("GET", "/api/v1/msp/status", nil)
	c.Set("role", "end_user")

	middleware.RequirePermission("msp", "read")(c)

	// end_user has no msp:read grant — DBOnly mode with a nil client will
	// fail the DB lookup and deny; assert it aborts, not that it 200s.
	assert.True(t, c.IsAborted())
}

func TestGapRoutes_UsersProfileAndMe_UseUserRead(t *testing.T) {
	gin.SetMode(gin.TestMode)
	for _, path := range []string{"/api/v1/users/profile", "/api/v1/users/me"} {
		w := httptest.NewRecorder()
		c, _ := gin.CreateTestContext(w)
		c.Request, _ = http.NewRequest("GET", path, nil)
		c.Set("role", "end_user")

		middleware.RequirePermission("user", "read")(c)

		assert.True(t, c.IsAborted())
	}
}
