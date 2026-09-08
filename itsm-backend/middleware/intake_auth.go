package middleware

import (
	"context"
	"errors"
	"github.com/gin-gonic/gin"
	"itsm-backend/authentication"
	"itsm-backend/common/tenantctx"
	"itsm-backend/handlers/common/intakehttp"
	creation "itsm-backend/handlers/common/workitemcreation"
	"strings"
)

type IntakeIdentityValidator func(context.Context, *authentication.IntakeClaims) (creation.Identity, error)

// IntakeAuthMiddleware is installed only on exact capability routes. The normal
// AuthMiddleware continues to require tokenType=access on all other APIs.
func IntakeAuthMiddleware(secret, scope string, validate IntakeIdentityValidator) gin.HandlerFunc {
	return func(c *gin.Context) {
		fail := func(err error) {
			var e *creation.IntakeError
			if !errors.As(err, &e) {
				e = creation.NewAuthenticationRequired("invalid intake credential", nil)
			}
			intakehttp.Fail(c, e)
			c.Abort()
		}
		header := c.GetHeader("Authorization")
		if !strings.HasPrefix(header, "Bearer ") {
			fail(creation.NewAuthenticationRequired("intake credential required", nil))
			return
		}
		claims, err := authentication.ValidateIntakeToken(strings.TrimPrefix(header, "Bearer "), secret)
		if err != nil {
			fail(err)
			return
		}
		matched := false
		for _, s := range claims.Scope {
			if s == scope {
				matched = true
			}
		}
		if !matched {
			fail(creation.NewPermissionDenied("intake scope required", nil))
			return
		}
		if validate == nil {
			fail(creation.NewInfrastructureUnavailable("identity validation unavailable", nil))
			return
		}
		ctx := tenantctx.WithTenantID(WithAuthenticatedTenantID(c.Request.Context(), claims.TenantID), claims.TenantID)
		identity, err := validate(ctx, claims)
		if err != nil {
			fail(err)
			return
		}
		c.Request = c.Request.WithContext(ctx)
		c.Set("intake_identity", identity)
		c.Set("intake_claims", claims)
		c.Set("user_id", identity.ActorID)
		c.Set("tenant_id", identity.TenantID)
		c.Set("role", identity.Role)
		c.Next()
	}
}
