package middleware

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"

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

		RBACMiddleware(nil)(c)

		assert.Equal(t, http.StatusUnauthorized, w.Code)
		assert.Contains(t, w.Body.String(), "用户未认证")
	})

	t.Run("No Tenant ID in Context", func(t *testing.T) {
		w := httptest.NewRecorder()
		c, _ := gin.CreateTestContext(w)
		c.Request, _ = http.NewRequest("GET", "/api/v1/tickets", nil)
		c.Set("user_id", 1)

		RBACMiddleware(nil)(c)

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
		SetPasswordHash("x").
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

	RBACMiddleware(client)(c)

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

func TestHasResourcePermission(t *testing.T) {
	gin.SetMode(gin.TestMode)
	original := PermissionConfig.Mode
	PermissionConfig.Mode = PermissionConfigModeHardcodeOnly
	t.Cleanup(func() { PermissionConfig.Mode = original })

	t.Run("Super Admin Has All Permissions", func(t *testing.T) {
		result := hasResourcePermission(nil, "super_admin", "any_resource", "any_action", 1)
		assert.True(t, result)
	})

	t.Run("End User Has Ticket Read Permission", func(t *testing.T) {
		result := hasResourcePermission(nil, "end_user", "ticket", "read", 1)
		assert.True(t, result)
	})

	t.Run("End User Does Not Have Ticket Delete Permission", func(t *testing.T) {
		result := hasResourcePermission(nil, "end_user", "ticket", "delete", 1)
		assert.False(t, result)
	})

	t.Run("Unknown Role Has No Permissions", func(t *testing.T) {
		result := hasResourcePermission(nil, "unknown_role", "ticket", "read", 1)
		assert.False(t, result)
	})

	t.Run("Wildcard Permission", func(t *testing.T) {
		// Super admin has wildcard "*" permission
		result := hasResourcePermission(nil, "super_admin", "ticket", "delete", 1)
		assert.True(t, result)

		result = hasResourcePermission(nil, "super_admin", "anything", "anything", 1)
		assert.True(t, result)
	})
}

func TestMatchPath(t *testing.T) {
	t.Run("Exact Match", func(t *testing.T) {
		assert.True(t, matchPath("/api/v1/tickets", "/api/v1/tickets"))
		assert.False(t, matchPath("/api/v1/tickets", "/api/v1/users"))
	})

	t.Run("Wildcard Match", func(t *testing.T) {
		assert.True(t, matchPath("/api/v1/tickets/*", "/api/v1/tickets/123"))
		assert.True(t, matchPath("/api/v1/tickets/*", "/api/v1/tickets/abc/edit"))
		assert.True(t, matchPath("/api/v1/tickets/*/assign", "/api/v1/tickets/123/assign"))
		assert.False(t, matchPath("/api/v1/tickets/*/assign", "/api/v1/tickets/123/close"))
		assert.False(t, matchPath("/api/v1/tickets/*", "/api/v1/users/123"))
	})

	t.Run("No Match", func(t *testing.T) {
		assert.False(t, matchPath("/api/v1/tickets", "/api/v1/users"))
		assert.False(t, matchPath("/api/v1/tickets/*", "/api/v1/tickets"))
	})
}

func TestGetPermissionFromPath(t *testing.T) {
	t.Run("GET Tickets Returns Read Permission", func(t *testing.T) {
		perm := getPermissionFromPath("GET", "/api/v1/tickets")
		assert.NotNil(t, perm)
		assert.Equal(t, "ticket", perm.Resource)
		assert.Equal(t, "read", perm.Action)
	})

	t.Run("POST Tickets Returns Write Permission", func(t *testing.T) {
		perm := getPermissionFromPath("POST", "/api/v1/tickets")
		assert.NotNil(t, perm)
		assert.Equal(t, "ticket", perm.Resource)
		assert.Equal(t, "write", perm.Action)
	})

	t.Run("Assign Ticket Returns Assign Permission", func(t *testing.T) {
		perm := getPermissionFromPath("POST", "/api/v1/tickets/123/assign")
		assert.NotNil(t, perm)
		assert.Equal(t, "ticket", perm.Resource)
		assert.Equal(t, "assign", perm.Action)
	})

	t.Run("DELETE Tickets Returns Delete Permission", func(t *testing.T) {
		perm := getPermissionFromPath("DELETE", "/api/v1/tickets/123")
		assert.NotNil(t, perm)
		assert.Equal(t, "ticket", perm.Resource)
		assert.Equal(t, "delete", perm.Action)
	})

	t.Run("Unknown Path Returns Nil", func(t *testing.T) {
		perm := getPermissionFromPath("GET", "/api/v1/unknown/path")
		assert.Nil(t, perm)
	})

	// 回归：/api/v1/releases 曾缺失于 ResourceActionMap，导致全局 RBACMiddleware
	// 对所有非 super_admin 用户直接 2003（路由级 RequirePermission 根本轮不到）。
	t.Run("GET Releases Returns Read Permission", func(t *testing.T) {
		perm := getPermissionFromPath("GET", "/api/v1/releases")
		assert.NotNil(t, perm)
		assert.Equal(t, "release", perm.Resource)
		assert.Equal(t, "read", perm.Action)
	})

	t.Run("GET Releases Stats Returns Read Permission", func(t *testing.T) {
		perm := getPermissionFromPath("GET", "/api/v1/releases/stats")
		assert.NotNil(t, perm)
		assert.Equal(t, "release", perm.Resource)
		assert.Equal(t, "read", perm.Action)
	})

	t.Run("POST Release Approve Returns Approve Permission", func(t *testing.T) {
		// 动作型子路由需要比 /releases/* 通配符更具体的映射，否则只被授予
		// release:approve（没有 release:write）的审批人会被全局中间件挡在路由自己
		// 声明的 RequirePermission("release","approve") 之前，见 tickets/*/assign 同类先例。
		perm := getPermissionFromPath("POST", "/api/v1/releases/1/approve")
		assert.NotNil(t, perm)
		assert.Equal(t, "release", perm.Resource)
		assert.Equal(t, "approve", perm.Action)
	})

	t.Run("DELETE Release Returns Delete Permission", func(t *testing.T) {
		perm := getPermissionFromPath("DELETE", "/api/v1/releases/1")
		assert.NotNil(t, perm)
		assert.Equal(t, "release", perm.Resource)
		assert.Equal(t, "delete", perm.Action)
	})
}

func TestCheckPermissionMatch_ResourceAdminIncludesActions(t *testing.T) {
	permissions := []Permission{{Resource: "ticket", Action: "admin"}}

	assert.True(t, checkPermissionMatch(permissions, "ticket", "assign"))
	assert.False(t, checkPermissionMatch(permissions, "incident", "assign"))
}

func TestDBOnlyPermissionModeDoesNotUseHardcodedFallback(t *testing.T) {
	original := PermissionConfig.Mode
	PermissionConfig.Mode = PermissionConfigModeDBOnly
	t.Cleanup(func() { PermissionConfig.Mode = original })

	permissions := loadPermissionsByMode(nil, "admin", 1)
	assert.Empty(t, permissions)
	assert.False(t, checkPermissionMatch(permissions, "ticket", "read"))
}
