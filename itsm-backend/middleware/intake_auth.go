package middleware

import (
	"crypto/rand"
	"encoding/hex"
	"fmt"
	"strings"
	"time"

	"itsm-backend/common"

	"github.com/gin-gonic/gin"
	"github.com/golang-jwt/jwt/v5"
)

const (
	IntakeTokenType     = "intake"
	IntakeTokenAudience = "itsm-intake"
	IntakeCreateScope   = "intake:create"
	MaxIntakeTokenTTL   = 5 * time.Minute
)

type IntakeTokenIdentity struct {
	UserID   int
	Username string
	Role     string
	TenantID int
	Channel  string
	Provider string
}

func GenerateIntakeToken(identity IntakeTokenIdentity, jwtSecret string, ttl time.Duration) (string, *Claims, error) {
	if ttl <= 0 || ttl > MaxIntakeTokenTTL {
		return "", nil, fmt.Errorf("intake token TTL must be positive and no greater than five minutes")
	}
	jtiBytes := make([]byte, 16)
	if _, err := rand.Read(jtiBytes); err != nil {
		return "", nil, err
	}
	now := time.Now().UTC()
	claims := &Claims{
		UserID: identity.UserID, Username: identity.Username, Role: identity.Role,
		TenantID: identity.TenantID, TokenType: IntakeTokenType,
		Channel: identity.Channel, Provider: identity.Provider, Scope: []string{IntakeCreateScope},
		RegisteredClaims: jwt.RegisteredClaims{
			ID: hex.EncodeToString(jtiBytes), Audience: jwt.ClaimStrings{IntakeTokenAudience},
			IssuedAt: jwt.NewNumericDate(now), ExpiresAt: jwt.NewNumericDate(now.Add(ttl)),
		},
	}
	signed, err := jwt.NewWithClaims(jwt.SigningMethodHS256, claims).SignedString([]byte(jwtSecret))
	if err != nil {
		return "", nil, err
	}
	return signed, claims, nil
}

func hasExactIntakeAudienceAndScope(claims *Claims) bool {
	return claims != nil && claims.IssuedAt != nil && claims.ExpiresAt != nil &&
		claims.ExpiresAt.Time.After(claims.IssuedAt.Time) &&
		claims.ExpiresAt.Time.Sub(claims.IssuedAt.Time) <= MaxIntakeTokenTTL &&
		len(claims.Audience) == 1 && claims.Audience[0] == IntakeTokenAudience &&
		len(claims.Scope) == 1 && claims.Scope[0] == IntakeCreateScope
}

// IntakeAuthMiddleware accepts ordinary access tokens and the narrowly scoped
// connector token. AuthMiddleware remains access-only, so intake credentials
// cannot be used on any other API group.
func IntakeAuthMiddleware(jwtSecret string) gin.HandlerFunc {
	return func(c *gin.Context) {
		authHeader := c.GetHeader("Authorization")
		fromCookie := false
		if authHeader == "" {
			if cookieToken, err := c.Cookie("access_token"); err == nil && cookieToken != "" {
				authHeader = "Bearer " + cookieToken
				fromCookie = true
			}
		}
		if !strings.HasPrefix(authHeader, "Bearer ") || strings.TrimSpace(strings.TrimPrefix(authHeader, "Bearer ")) == "" {
			common.Fail(c, common.AuthFailedCode, "缺少或无效的认证token")
			c.Abort()
			return
		}
		tokenString := strings.TrimSpace(strings.TrimPrefix(authHeader, "Bearer "))
		claims, err := parseJWTClaims(tokenString, jwtSecret)
		if err != nil || claims.UserID <= 0 || claims.TenantID <= 0 || strings.TrimSpace(claims.Role) == "" {
			common.Fail(c, common.AuthFailedCode, "token无效")
			c.Abort()
			return
		}
		switch claims.TokenType {
		case "access":
			revoked, revocationErr := isAccessTokenRevoked(c.Request.Context(), tokenString)
			if revocationErr != nil || revoked {
				common.Fail(c, common.AuthFailedCode, "token状态验证失败")
				c.Abort()
				return
			}
			if fromCookie {
				claims.Channel = "itsm_web"
			} else {
				claims.Channel = "itsm_api"
			}
		case IntakeTokenType:
			if !hasExactIntakeAudienceAndScope(claims) || strings.TrimSpace(claims.Channel) == "" ||
				strings.TrimSpace(claims.Provider) == "" || strings.TrimSpace(claims.ID) == "" {
				common.Fail(c, common.AuthFailedCode, "intake token scope无效")
				c.Abort()
				return
			}
		default:
			common.Fail(c, common.AuthFailedCode, "无效的token类型")
			c.Abort()
			return
		}

		c.Set("user_id", claims.UserID)
		c.Set("username", claims.Username)
		c.Set("role", claims.Role)
		c.Set("tenant_id", claims.TenantID)
		c.Set("channel", claims.Channel)
		c.Set("provider", claims.Provider)
		c.Set("token_id", claims.ID)
		c.Set("token", tokenString)
		c.Next()
	}
}
