package middleware

import (
	"context"
	"fmt"
	"strings"

	"itsm-backend/authentication"
	"itsm-backend/common"

	"github.com/gin-gonic/gin"
	"github.com/golang-jwt/jwt/v5"
	"go.uber.org/zap"
)

type authenticatedTenantIDContextKey struct{}

// WithAuthenticatedTenantID preserves tenant identity established by a trusted
// authentication adapter independently of later request tenant resolution.
func WithAuthenticatedTenantID(ctx context.Context, tenantID int) context.Context {
	return context.WithValue(ctx, authenticatedTenantIDContextKey{}, tenantID)
}

// AuthenticatedTenantIDFromContext returns the immutable tenant identity from authentication.
func AuthenticatedTenantIDFromContext(ctx context.Context) (int, bool) {
	tenantID, ok := ctx.Value(authenticatedTenantIDContextKey{}).(int)
	return tenantID, ok
}

// AuthMiddleware JWT认证中间件
func AuthMiddleware(jwtSecret string) gin.HandlerFunc {
	return func(c *gin.Context) {
		// 获取Authorization header
		authHeader := c.GetHeader("Authorization")

		// 调试日志：记录收到的请求信息
		zap.S().Infow(
			"AuthMiddleware: received request",
			"path", c.Request.URL.Path,
			"method", c.Request.Method,
			"has_auth_header", authHeader != "",
			"auth_header_prefix", strings.HasPrefix(authHeader, "Bearer "),
		)

		// 如果没有 Authorization header，尝试从 cookie 中获取 (支持 httpOnly cookie)
		if authHeader == "" {
			if cookieToken, err := c.Cookie("access_token"); err == nil && cookieToken != "" {
				authHeader = "Bearer " + cookieToken
				zap.S().Infow(
					"AuthMiddleware: using token from cookie",
					"path", c.Request.URL.Path,
				)
			}
		}

		if authHeader == "" {
			zap.S().Warnw(
				"AuthMiddleware: missing Authorization header",
				"path", c.Request.URL.Path,
				"ip", c.ClientIP(),
			)
			common.Fail(c, common.AuthFailedCode, "缺少认证token")
			c.Abort()
			return
		}

		// 检查Bearer前缀
		if !strings.HasPrefix(authHeader, "Bearer ") {
			zap.S().Warnw(
				"AuthMiddleware: invalid token format",
				"path", c.Request.URL.Path,
				"prefix", authHeader[:min(10, len(authHeader))],
			)
			common.Fail(c, common.AuthFailedCode, "token格式错误")
			c.Abort()
			return
		}

		// 提取token
		tokenString := strings.TrimPrefix(authHeader, "Bearer ")
		if tokenString == "" {
			zap.S().Warnw(
				"AuthMiddleware: empty token",
				"path", c.Request.URL.Path,
			)
			common.Fail(c, common.AuthFailedCode, "token不能为空")
			c.Abort()
			return
		}

		// 调试日志：记录token解析前的信息
		zap.S().Infow(
			"AuthMiddleware: parsing token",
			"path", c.Request.URL.Path,
			"token_length", len(tokenString),
		)

		// 解析JWT token
		token, err := jwt.ParseWithClaims(tokenString, &authentication.Claims{}, func(token *jwt.Token) (interface{}, error) {
			// 验证签名算法，防止算法混淆攻击
			if _, ok := token.Method.(*jwt.SigningMethodHMAC); !ok {
				return nil, jwt.ErrSignatureInvalid
			}
			return []byte(jwtSecret), nil
		})
		if err != nil {
			zap.S().Warnw(
				"AuthMiddleware: token parse failed",
				"path", c.Request.URL.Path,
				"error", err.Error(),
				"error_type", fmt.Sprintf("%T", err),
			)
			common.Fail(c, common.AuthFailedCode, "token无效")
			c.Abort()
			return
		}

		if !token.Valid {
			zap.S().Warnw(
				"AuthMiddleware: token invalid",
				"path", c.Request.URL.Path,
			)
			common.Fail(c, common.AuthFailedCode, "token无效")
			c.Abort()
			return
		}

		// 提取用户信息
		if claims, ok := token.Claims.(*authentication.Claims); ok {
			// H4 修复：检查TokenType，必须是access类型
			if claims.TokenType != "access" {
				zap.S().Warnw(
					"AuthMiddleware: invalid token type",
					"path", c.Request.URL.Path,
					"token_type", claims.TokenType,
				)
				common.Fail(c, common.AuthFailedCode, "无效的token类型，请使用access token")
				c.Abort()
				return
			}

			revoked, revocationErr := authentication.IsAccessTokenRevoked(c.Request.Context(), tokenString)
			if revocationErr != nil {
				zap.S().Errorw("AuthMiddleware: token revocation check failed",
					"path", c.Request.URL.Path, "error", revocationErr)
				common.Fail(c, common.AuthFailedCode, "token状态验证失败")
				c.Abort()
				return
			}
			if revoked {
				zap.S().Warnw("AuthMiddleware: revoked token rejected",
					"path", c.Request.URL.Path, "user_id", claims.UserID)
				common.Fail(c, common.AuthFailedCode, "token已失效")
				c.Abort()
				return
			}

			c.Set("user_id", claims.UserID)
			c.Set("username", claims.Username)
			c.Set("role", claims.Role)
			c.Set("tenant_id", claims.TenantID) // 添加租户ID
			c.Set("token", tokenString)
			c.Request = c.Request.WithContext(WithAuthenticatedTenantID(c.Request.Context(), claims.TenantID))

			// 调试日志：认证成功
			zap.S().Infow(
				"AuthMiddleware: authentication successful",
				"path", c.Request.URL.Path,
				"user_id", claims.UserID,
				"username", claims.Username,
				"tenant_id", claims.TenantID,
			)
		} else {
			zap.S().Warnw(
				"AuthMiddleware: failed to extract claims",
				"path", c.Request.URL.Path,
			)
			common.Fail(c, common.AuthFailedCode, "token解析失败")
			c.Abort()
			return
		}

		c.Next()
	}
}
