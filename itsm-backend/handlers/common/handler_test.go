package common

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"itsm-backend/authentication"

	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/require"
	"go.uber.org/zap"
)

func TestShouldUseSecureCookies(t *testing.T) {
	gin.SetMode(gin.TestMode)

	t.Run("localhost http does not force secure", func(t *testing.T) {
		c, _ := gin.CreateTestContext(httptest.NewRecorder())
		req := httptest.NewRequest("POST", "http://localhost:8090/api/v1/auth/login", nil)
		req.Host = "localhost:8090"
		c.Request = req

		require.False(t, shouldUseSecureCookies(c))
		require.Equal(t, "", cookieDomain(c))
	})

	t.Run("forwarded https enables secure", func(t *testing.T) {
		c, _ := gin.CreateTestContext(httptest.NewRecorder())
		req := httptest.NewRequest("POST", "http://example.com/api/v1/auth/login", nil)
		req.Host = "example.com"
		req.Header.Set("X-Forwarded-Proto", "https")
		c.Request = req

		require.True(t, shouldUseSecureCookies(c))
		require.Equal(t, "", cookieDomain(c))
	})
}

func TestRefreshTokenReturnsServiceUnavailableWhenAuthoritativeStoreIsUnavailable(t *testing.T) {
	gin.SetMode(gin.TestMode)
	const secret = "refresh-handler-unavailable-secret"
	consumer := authentication.NewRefreshTokenConsumer(secret, nil)
	svc := NewService(nil, secret, zap.NewNop().Sugar(), nil, consumer)
	handler := NewHandler(svc)
	router := gin.New()
	router.POST("/api/v1/auth/refresh", handler.RefreshToken)

	request := httptest.NewRequest(http.MethodPost, "/api/v1/auth/refresh", strings.NewReader(`{"refreshToken":"signed-but-store-unavailable"}`))
	request.Header.Set("Content-Type", "application/json")
	response := httptest.NewRecorder()
	router.ServeHTTP(response, request)

	require.Equal(t, http.StatusServiceUnavailable, response.Code)
	require.Contains(t, response.Body.String(), "刷新服务暂不可用")
}

func TestRefreshTokenRotatesHttpOnlyCookieThroughCanonicalHandler(t *testing.T) {
	gin.SetMode(gin.TestMode)
	const secret = "refresh-handler-cookie-secret"
	store := &atomicRefreshTokenStore{consumed: make(map[string]struct{})}
	consumer := authentication.NewRefreshTokenConsumer(secret, store)
	user := &User{ID: 61, Username: "browser-user", Role: "end_user", TenantID: 9, Active: true}
	svc := NewService(&refreshTestRepository{user: user}, secret, zap.NewNop().Sugar(), nil, consumer)
	handler := NewHandler(svc)
	router := gin.New()
	router.POST("/api/v1/auth/refresh", handler.RefreshToken)

	original, err := authentication.GenerateRefreshToken(user.ID, secret, time.Hour)
	require.NoError(t, err)
	request := httptest.NewRequest(http.MethodPost, "/api/v1/auth/refresh", strings.NewReader(`{}`))
	request.Header.Set("Content-Type", "application/json")
	request.AddCookie(&http.Cookie{Name: "refresh_token", Value: original})
	response := httptest.NewRecorder()
	router.ServeHTTP(response, request)

	require.Equal(t, http.StatusOK, response.Code)
	var rotated string
	for _, cookie := range response.Result().Cookies() {
		if cookie.Name == "refresh_token" {
			rotated = cookie.Value
			require.True(t, cookie.HttpOnly)
		}
	}
	require.NotEmpty(t, rotated)
	require.NotEqual(t, original, rotated)
	claims, err := authentication.NewRefreshTokenConsumer(secret, &atomicRefreshTokenStore{consumed: make(map[string]struct{})}).Consume(context.Background(), rotated)
	require.NoError(t, err)
	require.Equal(t, user.ID, claims.UserID)
}
