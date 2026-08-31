package middleware

import (
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/golang-jwt/jwt/v5"
	"github.com/stretchr/testify/require"
)

func TestIntakeTokenIsolation(t *testing.T) {
	gin.SetMode(gin.TestMode)
	const secret = "intake-auth-test-secret"
	token, claims, err := GenerateIntakeToken(IntakeTokenIdentity{
		UserID: 17, Username: "mapped-user", Role: "end_user", TenantID: 9,
		Channel: "teams", Provider: "microsoft",
	}, secret, 5*time.Minute)
	require.NoError(t, err)
	require.Equal(t, "intake", claims.TokenType)
	require.Equal(t, jwt.ClaimStrings{"itsm-intake"}, claims.Audience)
	require.Equal(t, []string{"intake:create"}, claims.Scope)
	require.NotEmpty(t, claims.ID)
	require.WithinDuration(t, time.Now().Add(5*time.Minute), claims.ExpiresAt.Time, 2*time.Second)

	t.Run("accepted on intake auth and context is derived from claims", func(t *testing.T) {
		router := gin.New()
		router.GET("/intake", IntakeAuthMiddleware(secret), func(c *gin.Context) {
			c.JSON(http.StatusOK, gin.H{
				"userId": c.GetInt("user_id"), "tenantId": c.GetInt("tenant_id"),
				"channel": c.GetString("channel"), "provider": c.GetString("provider"),
				"tokenId": c.GetString("token_id"),
			})
		})
		request := httptest.NewRequest(http.MethodGet, "/intake", nil)
		request.Header.Set("Authorization", "Bearer "+token)
		response := httptest.NewRecorder()
		router.ServeHTTP(response, request)
		require.Equal(t, http.StatusOK, response.Code)
		require.Contains(t, response.Body.String(), `"channel":"teams"`)
		require.Contains(t, response.Body.String(), `"tenantId":9`)
	})

	t.Run("rejected by general auth", func(t *testing.T) {
		router := gin.New()
		router.GET("/general", AuthMiddleware(secret), func(c *gin.Context) { c.Status(http.StatusOK) })
		request := httptest.NewRequest(http.MethodGet, "/general", nil)
		request.Header.Set("Authorization", "Bearer "+token)
		response := httptest.NewRecorder()
		router.ServeHTTP(response, request)
		require.Equal(t, http.StatusUnauthorized, response.Code)
	})

	t.Run("wrong audience or scope is rejected", func(t *testing.T) {
		for _, mutate := range []func(*Claims){
			func(c *Claims) { c.Audience = jwt.ClaimStrings{"somewhere-else"} },
			func(c *Claims) { c.Scope = []string{"ticket:create"} },
			func(c *Claims) { c.ExpiresAt = jwt.NewNumericDate(c.IssuedAt.Add(6 * time.Minute)) },
		} {
			bad := *claims
			mutate(&bad)
			signed, signErr := jwt.NewWithClaims(jwt.SigningMethodHS256, &bad).SignedString([]byte(secret))
			require.NoError(t, signErr)
			router := gin.New()
			router.GET("/intake", IntakeAuthMiddleware(secret), func(c *gin.Context) { c.Status(http.StatusOK) })
			request := httptest.NewRequest(http.MethodGet, "/intake", nil)
			request.Header.Set("Authorization", "Bearer "+signed)
			response := httptest.NewRecorder()
			router.ServeHTTP(response, request)
			require.Equal(t, http.StatusUnauthorized, response.Code)
		}
	})

	t.Run("normal access token remains accepted", func(t *testing.T) {
		access, err := GenerateAccessToken(17, "mapped-user", "end_user", 9, secret, time.Hour)
		require.NoError(t, err)
		router := gin.New()
		router.GET("/intake", IntakeAuthMiddleware(secret), func(c *gin.Context) { c.Status(http.StatusOK) })
		request := httptest.NewRequest(http.MethodGet, "/intake", nil)
		request.Header.Set("Authorization", "Bearer "+access)
		response := httptest.NewRecorder()
		router.ServeHTTP(response, request)
		require.Equal(t, http.StatusOK, response.Code)
	})
}
