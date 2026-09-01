package middleware

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"itsm-backend/authentication"

	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/require"
)

func TestAuthMiddlewareRejectsRevokedAccessToken(t *testing.T) {
	gin.SetMode(gin.TestMode)
	const secret = "token-revocation-test-secret"
	token, err := authentication.GenerateAccessToken(7, "operator", "admin", 3, secret, time.Hour)
	require.NoError(t, err)

	router := gin.New()
	router.GET("/protected", AuthMiddleware(secret), func(c *gin.Context) {
		c.Status(http.StatusNoContent)
	})

	first := httptest.NewRecorder()
	firstRequest := httptest.NewRequest(http.MethodGet, "/protected", nil)
	firstRequest.Header.Set("Authorization", "Bearer "+token)
	router.ServeHTTP(first, firstRequest)
	require.Equal(t, http.StatusNoContent, first.Code)

	claims, err := authentication.ValidateAccessToken(context.Background(), token, secret)
	require.NoError(t, err)
	require.NotNil(t, claims.ExpiresAt)
	require.NoError(t, authentication.RevokeAccessToken(context.Background(), token, claims.ExpiresAt.Time))

	second := httptest.NewRecorder()
	secondRequest := httptest.NewRequest(http.MethodGet, "/protected", nil)
	secondRequest.Header.Set("Authorization", "Bearer "+token)
	router.ServeHTTP(second, secondRequest)
	require.NotEqual(t, http.StatusNoContent, second.Code)
	require.Contains(t, second.Body.String(), "token已失效")
}
