package middleware

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"

	"itsm-backend/authorization"
	"itsm-backend/ent/enttest"

	"github.com/gin-gonic/gin"
	_ "github.com/mattn/go-sqlite3"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestRBACMiddleware(t *testing.T) {
	gin.SetMode(gin.TestMode)

	t.Run("No User ID in Context", func(t *testing.T) {
		w := httptest.NewRecorder()
		c, _ := gin.CreateTestContext(w)
		c.Request, _ = http.NewRequest("GET", "/api/v1/tickets", nil)

		RBACMiddleware(nil, nil)(c)

		assert.Equal(t, http.StatusUnauthorized, w.Code)
		assert.Contains(t, w.Body.String(), "用户未认证")
	})

	t.Run("No Tenant ID in Context", func(t *testing.T) {
		w := httptest.NewRecorder()
		c, _ := gin.CreateTestContext(w)
		c.Request, _ = http.NewRequest("GET", "/api/v1/tickets", nil)
		c.Set("user_id", 1)

		RBACMiddleware(nil, nil)(c)

		assert.Equal(t, http.StatusUnauthorized, w.Code)
		assert.Contains(t, w.Body.String(), "租户信息缺失")
	})

	// Note: "No Role in Context" test requires a valid client to query the database
	// The RBACMiddleware will attempt to fetch user from DB which panics with nil client
	// This is expected behavior - in production, client should never be nil
}

func TestRBACMiddleware_NoLongerPerformsPermissionCheck(t *testing.T) {
	gin.SetMode(gin.TestMode)
	// A role/path combination that the (now-deleted) hasPermission() would
	// have denied under the old ResourceActionMap-inference logic must no
	// longer be rejected by RBACMiddleware itself — that job now belongs
	// solely to the route's own RequirePermission/RequireRole call, which
	// isn't present in this bare test context, so this only proves
	// RBACMiddleware itself doesn't short-circuit on it.
	//
	// This test requires a real client because RBACMiddleware queries the
	// user from DB (see the existing "No Role in Context" comment above) —
	// use enttest with a seeded active user.
	client := enttest.Open(t, "sqlite3", "file:rbacmw_test?mode=memory&cache=shared&_fk=1")
	defer client.Close()
	ctx := context.Background()
	tenant, err := client.Tenant.Create().
		SetName("Test Tenant").SetCode("test").SetDomain("test.com").SetStatus("active").
		Save(ctx)
	require.NoError(t, err)
	u, err := client.User.Create().
		SetUsername("test_no_perm_check").
		SetEmail("test_no_perm_check@example.com").
		SetName("Test No Perm Check").
		SetPasswordHash("x").SetRole("end_user").
		SetActive(true).
		SetTenantID(tenant.ID).
		Save(ctx)
	require.NoError(t, err)

	w := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(w)
	c.Request, _ = http.NewRequest("GET", "/api/v1/some-random-unmapped-path", nil)
	c.Set("user_id", u.ID)
	c.Set("tenant_id", tenant.ID)
	c.Set("role", "end_user")

	RBACMiddleware(client, client)(c)

	assert.False(t, c.IsAborted())
}

func TestRequirePermissionForRBAC(t *testing.T) {
	gin.SetMode(gin.TestMode)

	t.Run("Missing Role Returns Error", func(t *testing.T) {
		w := httptest.NewRecorder()
		c, _ := gin.CreateTestContext(w)
		c.Request, _ = http.NewRequest("GET", "/api/v1/tickets", nil)
		// No role set

		RequirePermission("ticket", "read")(c)

		assert.Equal(t, http.StatusUnauthorized, w.Code)
		assert.Contains(t, w.Body.String(), "用户角色信息缺失")
	})

	t.Run("Missing Tenant ID Returns Error", func(t *testing.T) {
		w := httptest.NewRecorder()
		c, _ := gin.CreateTestContext(w)
		c.Request, _ = http.NewRequest("GET", "/api/v1/tickets", nil)
		c.Set("role", "admin")
		// No tenant_id

		RequirePermission("ticket", "read")(c)

		assert.Equal(t, http.StatusUnauthorized, w.Code)
		assert.Contains(t, w.Body.String(), "租户信息缺失")
	})
}

func TestRequireRole(t *testing.T) {
	gin.SetMode(gin.TestMode)

	t.Run("Missing Role Returns Error", func(t *testing.T) {
		w := httptest.NewRecorder()
		c, _ := gin.CreateTestContext(w)
		c.Request, _ = http.NewRequest("GET", "/api/v1/tickets", nil)

		RequireRole("admin", "manager")(c)

		assert.Equal(t, http.StatusForbidden, w.Code)
		assert.Contains(t, w.Body.String(), "缺少角色信息")
	})

	t.Run("Role Not Allowed Returns Error", func(t *testing.T) {
		w := httptest.NewRecorder()
		c, _ := gin.CreateTestContext(w)
		c.Request, _ = http.NewRequest("GET", "/api/v1/tickets", nil)
		c.Set("role", "end_user")

		RequireRole("admin", "manager")(c)

		assert.Equal(t, http.StatusForbidden, w.Code)
		assert.Contains(t, w.Body.String(), "无权限执行该操作")
	})

	t.Run("Role Allowed Succeeds", func(t *testing.T) {
		w := httptest.NewRecorder()
		c, _ := gin.CreateTestContext(w)
		c.Request, _ = http.NewRequest("GET", "/api/v1/tickets", nil)
		c.Set("role", "admin")

		RequireRole("admin", "manager")(c)

		// Should not abort
		assert.False(t, c.IsAborted())
	})

	t.Run("Role Matching Is Case Insensitive", func(t *testing.T) {
		w := httptest.NewRecorder()
		c, _ := gin.CreateTestContext(w)
		c.Request, _ = http.NewRequest("GET", "/api/v1/tickets", nil)
		c.Set("role", "ADMIN")

		RequireRole("admin", "manager")(c)

		// Should not abort
		assert.False(t, c.IsAborted())
	})
}

// TestRequireLegacyBPMNRoles is the single source-of-truth coverage for the
// shared BPMN allowlist gate. It replaces having to duplicate a 7-role
// table-driven test in each of the 5 BPMN controllers that now call
// middleware.RequireLegacyBPMNRoles() (bpmn_workflow_controller.go,
// bpmn_monitoring_controller.go, bpmn_dashboard_controller.go,
// bpmn_ai_generator_controller.go, bpmn_process_trigger_controller.go) — a
// typo dropping one of the 7 roles from the shared helper would be caught
// here instead of only being caught by a per-controller "one disallowed role
// is rejected" test.
func TestRequireLegacyBPMNRoles(t *testing.T) {
	gin.SetMode(gin.TestMode)

	allowedRoles := []string{
		"super_admin", "change_manager", "dept_manager", "end_user",
		"it_director", "ops_director", "sysadmin",
	}

	for _, role := range allowedRoles {
		t.Run("Allows "+role, func(t *testing.T) {
			w := httptest.NewRecorder()
			c, _ := gin.CreateTestContext(w)
			c.Request, _ = http.NewRequest("GET", "/api/v1/bpmn/process-instances", nil)
			c.Set("role", role)

			RequireLegacyBPMNRoles()(c)

			assert.False(t, c.IsAborted())
		})
	}

	disallowedRoles := []string{"l1_support", "dba", "guest"}

	for _, role := range disallowedRoles {
		t.Run("Rejects "+role, func(t *testing.T) {
			w := httptest.NewRecorder()
			c, _ := gin.CreateTestContext(w)
			c.Request, _ = http.NewRequest("GET", "/api/v1/bpmn/process-instances", nil)
			c.Set("role", role)

			RequireLegacyBPMNRoles()(c)

			assert.Equal(t, http.StatusForbidden, w.Code)
			assert.True(t, c.IsAborted())
		})
	}
}

func TestHasResourcePermission(t *testing.T) {
	gin.SetMode(gin.TestMode)
	original := authorization.PermissionConfig.Mode
	authorization.PermissionConfig.Mode = authorization.PermissionConfigModeHardcodeOnly
	t.Cleanup(func() { authorization.PermissionConfig.Mode = original })

	t.Run("Super Admin Has All Permissions", func(t *testing.T) {
		result := authorization.HasResourcePermission(nil, "super_admin", "any_resource", "any_action", 1)
		assert.True(t, result)
	})

	t.Run("End User Has Ticket Read Permission", func(t *testing.T) {
		result := authorization.HasResourcePermission(nil, "end_user", "ticket", "read", 1)
		assert.True(t, result)
	})

	t.Run("End User Does Not Have Ticket Delete Permission", func(t *testing.T) {
		result := authorization.HasResourcePermission(nil, "end_user", "ticket", "delete", 1)
		assert.False(t, result)
	})

	t.Run("Unknown Role Has No Permissions", func(t *testing.T) {
		result := authorization.HasResourcePermission(nil, "unknown_role", "ticket", "read", 1)
		assert.False(t, result)
	})

	t.Run("Wildcard Permission", func(t *testing.T) {
		// Super admin has wildcard "*" permission
		result := authorization.HasResourcePermission(nil, "super_admin", "ticket", "delete", 1)
		assert.True(t, result)

		result = authorization.HasResourcePermission(nil, "super_admin", "anything", "anything", 1)
		assert.True(t, result)
	})
}

func TestCheckPermissionMatch_ResourceAdminIncludesActions(t *testing.T) {
	permissions := []authorization.Permission{{Resource: "ticket", Action: "admin"}}

	assert.True(t, authorization.CheckPermissionMatch(permissions, "ticket", "assign"))
	assert.False(t, authorization.CheckPermissionMatch(permissions, "incident", "assign"))
}

func TestDBOnlyPermissionModeDoesNotUseHardcodedFallback(t *testing.T) {
	original := authorization.PermissionConfig.Mode
	authorization.PermissionConfig.Mode = authorization.PermissionConfigModeDBOnly
	t.Cleanup(func() { authorization.PermissionConfig.Mode = original })

	permissions := authorization.LoadPermissionsByMode(nil, "admin", 1)
	assert.Empty(t, permissions)
	assert.False(t, authorization.CheckPermissionMatch(permissions, "ticket", "read"))
}

func TestRBACMiddlewareRejectsStaleSignedRole(t *testing.T) {
	client := enttest.Open(t, "sqlite3", "file:rbac_stale?mode=memory&cache=shared&_fk=1")
	defer client.Close()
	ctx := context.Background()
	tenant := client.Tenant.Create().SetCode("stale").SetName("Stale").SaveX(ctx)
	actor := client.User.Create().SetTenantID(tenant.ID).SetUsername("stale").SetEmail("stale@example.test").SetName("Stale").SetPasswordHash("unused").SetRole("end_user").SaveX(ctx)
	c, _ := gin.CreateTestContext(httptest.NewRecorder())
	c.Request = httptest.NewRequest("GET", "/api/v1/tickets", nil)
	c.Set("user_id", actor.ID)
	c.Set("tenant_id", tenant.ID)
	c.Set("role", "admin")
	RBACMiddleware(client, client)(c)
	require.True(t, c.IsAborted(), "a stale token role must be rejected")
}

func TestRBACMiddlewareMissingDirectoryIsUnavailable(t *testing.T) {
	w := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(w)
	c.Request = httptest.NewRequest("GET", "/api/v1/tickets", nil)
	c.Set("user_id", 1)
	c.Set("tenant_id", 1)
	c.Set("role", "end_user")
	RBACMiddleware(nil, nil)(c)
	require.Equal(t, http.StatusServiceUnavailable, w.Code)
}
