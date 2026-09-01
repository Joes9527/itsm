package middleware

import (
	"context"
	"strings"

	"itsm-backend/authorization"
	"itsm-backend/common"
	"itsm-backend/ent"
	"itsm-backend/ent/user"

	"github.com/gin-gonic/gin"
	"go.uber.org/zap"
)

// RBACMiddleware is a session/tenant guard, NOT a fine-grained permission
// check. It validates that the caller is authenticated, that the DB user
// record exists and is active, resolves and validates the tenant ID, and
// enriches the gin.Context (user_entity, client, tenant_id) for downstream
// handlers. It does NOT refresh the authorization role from DB: the
// userEntity it loads is used only for the Active check and is stashed in
// "user_entity" — the "role" key used by every downstream RequirePermission /
// RequireRole / RequireMSPPermission check is set earlier from the JWT claim
// and is never overwritten with userEntity.Role here. So a role change in
// the DB does not take effect for a given caller until their JWT is
// refreshed (re-login or token renewal). Actual resource:action
// authorization is the sole responsibility of the RequirePermission /
// RequireRole / RequireMSPPermission call attached to each specific
// route — a route mounted under a group this middleware guards is NOT
// automatically permission-protected.
func RBACMiddleware(client *ent.Client) gin.HandlerFunc {
	return func(c *gin.Context) {
		// 调试日志
		zap.S().Infow(
			"RBACMiddleware: received request",
			"path", c.Request.URL.Path,
			"method", c.Request.Method,
		)

		// 将 Ent 客户端放入上下文，供资源级别检查使用（例如工单所有权校验）
		if client != nil {
			c.Set("client", client)
		}
		// 获取用户信息
		userIDInterface, exists := c.Get("user_id")
		if !exists {
			zap.S().Warnw(
				"RBACMiddleware: user_id not found in context",
				"path", c.Request.URL.Path,
			)
			common.Fail(c, common.AuthFailedCode, "用户未认证")
			c.Abort()
			return
		}

		userID, ok := userIDInterface.(int)
		if !ok {
			zap.S().Warnw(
				"RBACMiddleware: user_id format error",
				"path", c.Request.URL.Path,
				"user_id_interface", userIDInterface,
			)
			common.Fail(c, common.AuthFailedCode, "用户ID格式错误")
			c.Abort()
			return
		}

		// 获取租户ID
		// 特殊路径：认证相关端点不需要租户ID检查
		authPaths := map[string]bool{
			"/api/v1/auth/me":      true,
			"/api/v1/auth/tenants": true,
			"/api/v1/auth/menus":   true,
			"/api/v1/auth/profile": true,
		}
		isAuthPath := authPaths[c.Request.URL.Path]

		tenantIDInterface, exists := c.Get("tenant_id")
		if !exists {
			// 对于认证端点，尝试从JWT claim获取租户ID
			if isAuthPath {
				// 仅从JWT的tenant_id claim获取，不信任请求头
				if jwtTenantID, ok := c.Get("tenant_id"); ok {
					if tid, ok := jwtTenantID.(int); ok && tid > 0 {
						c.Set("tenant_id", tid)
						tenantIDInterface = tid
					}
				}
				// 如果仍然没有租户ID，拒绝请求而非默认分配
				if tenantIDInterface == nil {
					zap.S().Warnw(
						"RBACMiddleware: tenant_id not found in context for auth path",
						"path", c.Request.URL.Path,
						"user_id", userID,
					)
					common.Fail(c, common.AuthFailedCode, "租户信息缺失")
					c.Abort()
					return
				}
			} else {
				zap.S().Warnw(
					"RBACMiddleware: tenant_id not found in context",
					"path", c.Request.URL.Path,
					"user_id", userID,
				)
				common.Fail(c, common.AuthFailedCode, "租户信息缺失")
				c.Abort()
				return
			}
		}
		tenantID, ok := tenantIDInterface.(int)
		if !ok {
			zap.S().Warnw(
				"RBACMiddleware: tenant_id format error",
				"path", c.Request.URL.Path,
				"tenant_id_interface", tenantIDInterface,
			)
			common.Fail(c, common.AuthFailedCode, "租户ID格式错误")
			c.Abort()
			return
		}

		// 从数据库获取用户最新角色信息
		userEntity, err := client.User.Query().
			Where(user.ID(userID)).
			Only(context.Background())
		if err != nil {
			zap.S().Warnw(
				"RBACMiddleware: user not found in DB",
				"path", c.Request.URL.Path,
				"user_id", userID,
				"error", err.Error(),
			)
			common.Fail(c, common.AuthFailedCode, "用户不存在")
			c.Abort()
			return
		}

		// 检查用户是否被禁用
		if !userEntity.Active {
			zap.S().Warnw(
				"RBACMiddleware: user is disabled",
				"path", c.Request.URL.Path,
				"user_id", userID,
			)
			common.Fail(c, common.ForbiddenCode, "用户已被禁用")
			c.Abort()
			return
		}

		// 从JWT中获取角色信息
		roleInterface, exists := c.Get("role")
		if !exists {
			zap.S().Warnw(
				"RBACMiddleware: role not found in context",
				"path", c.Request.URL.Path,
				"user_id", userID,
			)
			common.Fail(c, common.AuthFailedCode, "角色信息缺失")
			c.Abort()
			return
		}

		role, ok := roleInterface.(string)
		if !ok {
			zap.S().Warnw(
				"RBACMiddleware: role format error",
				"path", c.Request.URL.Path,
				"role_interface", roleInterface,
			)
			common.Fail(c, common.AuthFailedCode, "角色格式错误")
			c.Abort()
			return
		}

		// 调试日志：RBAC检查通过
		zap.S().Infow(
			"RBACMiddleware: access granted",
			"path", c.Request.URL.Path,
			"user_id", userID,
			"role", role,
			"tenant_id", tenantID,
		)

		// 将用户实体信息存储到上下文中
		c.Set("user_entity", userEntity)

		c.Next()
	}
}

// RequirePermission 要求特定权限的中间件
func RequirePermission(resource, action string) gin.HandlerFunc {
	return func(c *gin.Context) {
		role, exists := c.Get("role")
		if !exists {
			common.Fail(c, common.AuthFailedCode, "用户角色信息缺失")
			c.Abort()
			return
		}

		// 获取租户ID
		tenantIDInterface, exists := c.Get("tenant_id")
		if !exists {
			common.Fail(c, common.AuthFailedCode, "租户信息缺失")
			c.Abort()
			return
		}
		tenantID := tenantIDInterface.(int)

		// 获取客户端
		clientInterface, exists := c.Get("client")
		if !exists {
			common.Fail(c, common.InternalErrorCode, "客户端缺失")
			c.Abort()
			return
		}
		client := clientInterface.(*ent.Client)

		if !authorization.HasResourcePermission(client, role.(string), resource, action, tenantID) {
			common.Fail(c, common.ForbiddenCode, "权限不足")
			c.Abort()
			return
		}

		c.Next()
	}
}

// RequireRole enforces that the authenticated user role is one of the allowed roles
func RequireRole(allowedRoles ...string) gin.HandlerFunc {
	normalized := make([]string, 0, len(allowedRoles))
	for _, r := range allowedRoles {
		normalized = append(normalized, strings.ToLower(strings.TrimSpace(r)))
	}
	return func(c *gin.Context) {
		roleAny, exists := c.Get("role")
		if !exists {
			common.Fail(c, common.ForbiddenCode, "缺少角色信息")
			c.Abort()
			return
		}
		role, _ := roleAny.(string)
		role = strings.ToLower(strings.TrimSpace(role))
		for _, ar := range normalized {
			if role == ar {
				c.Next()
				return
			}
		}
		common.Fail(c, common.ForbiddenCode, "无权限执行该操作")
		c.Abort()
	}
}

// RequireLegacyBPMNRoles gates the /api/v1/bpmn/* controller group (process
// definitions/instances/tasks, monitoring, dashboard, AI generator, process
// trigger). The 7-role list here exactly reproduces what the deleted global
// inference layer's ResourceActionMap wildcard for /api/v1/bpmn/* used to
// grant — it is NOT a deliberately designed permission model, just a
// preserve-current-behavior allowlist captured during the RBAC
// dual-declaration convergence. The real BPMN permission model (including
// instance-level authorization) is a backlog item — see
// docs/superpowers/specs/2026-08-24-rbac-dual-declaration-convergence-design.md.
func RequireLegacyBPMNRoles() gin.HandlerFunc {
	return RequireRole("super_admin", "change_manager", "dept_manager", "end_user", "it_director", "ops_director", "sysadmin")
}
