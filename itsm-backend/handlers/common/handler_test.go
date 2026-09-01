package common

import (
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"testing"
	"time"

	"itsm-backend/authentication"

	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/require"
	"go.uber.org/zap"
	"golang.org/x/crypto/bcrypt"
)

func TestRefreshTokenReturnsServiceUnavailableWhenAuthoritativeStoreIsUnavailable(t *testing.T) {
	gin.SetMode(gin.TestMode)
	fx := newRefreshServiceFixture(t)
	tenant := fx.tenant("unavailable-handler", "standard", "active")
	user := fx.user(tenant.ID, "unavailable-handler-user", "end_user", "", true)
	consumer := authentication.NewRefreshTokenConsumer(fx.secret, nil)
	svc := NewService(NewEntRepository(fx.client), fx.secret, zap.NewNop().Sugar(), fx.client, consumer)
	handler := NewHandler(svc)
	router := gin.New()
	router.POST("/api/v1/auth/refresh", handler.RefreshToken)

	token := fx.token(user, "end_user", tenant.ID)
	request := httptest.NewRequest(http.MethodPost, "/api/v1/auth/refresh", strings.NewReader(`{}`))
	request.Header.Set("Content-Type", "application/json")
	request.AddCookie(&http.Cookie{Name: "refresh_token", Value: token})
	response := httptest.NewRecorder()
	router.ServeHTTP(response, request)

	require.Equal(t, http.StatusServiceUnavailable, response.Code)
	require.Contains(t, response.Body.String(), "刷新服务暂不可用")
}

func TestRefreshTokenRotatesHttpOnlyCookieThroughCanonicalHandler(t *testing.T) {
	gin.SetMode(gin.TestMode)
	fx := newRefreshServiceFixture(t)
	tenant := fx.tenant("browser-handler", "standard", "active")
	user := fx.user(tenant.ID, "browser-user", "end_user", "", true)
	handler := NewHandler(fx.service)
	router := gin.New()
	router.POST("/api/v1/auth/refresh", handler.RefreshToken)

	original, err := authentication.GenerateRefreshToken(user.ID, user.Username, user.Role, user.TenantID, fx.secret, time.Hour)
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
	require.NotContains(t, response.Body.String(), "accessToken")
	require.NotContains(t, response.Body.String(), "refreshToken")
	require.NotContains(t, response.Body.String(), rotated)
	claims, err := authentication.NewRefreshTokenConsumer(fx.secret, &atomicRefreshTokenStore{consumed: make(map[string]struct{})}).Validate(rotated)
	require.NoError(t, err)
	require.Equal(t, user.ID, claims.Identity().UserID)
}

func TestLoginDeliversJWTsOnlyThroughHttpOnlyCookies(t *testing.T) {
	gin.SetMode(gin.TestMode)
	fx := newRefreshServiceFixture(t)
	tenant := fx.tenant("login-handler", "standard", "active")
	hash, err := bcrypt.GenerateFromPassword([]byte("correct-password"), bcrypt.MinCost)
	require.NoError(t, err)
	user := fx.user(tenant.ID, "login-user", "end_user", "", true)
	fx.client.User.UpdateOneID(user.ID).SetPasswordHash(string(hash)).ExecX(fx.ctx)

	router := gin.New()
	router.POST("/api/v1/auth/login", NewHandler(fx.service).Login)
	request := httptest.NewRequest(http.MethodPost, "/api/v1/auth/login", strings.NewReader(`{"username":"login-user","password":"correct-password","tenantId":`+strconv.Itoa(tenant.ID)+`}`))
	request.Header.Set("Content-Type", "application/json")
	response := httptest.NewRecorder()
	router.ServeHTTP(response, request)

	require.Equal(t, http.StatusOK, response.Code)
	var accessToken, refreshToken string
	for _, cookie := range response.Result().Cookies() {
		switch cookie.Name {
		case "access_token":
			accessToken = cookie.Value
			require.True(t, cookie.HttpOnly)
		case "refresh_token":
			refreshToken = cookie.Value
			require.True(t, cookie.HttpOnly)
		}
	}
	require.NotEmpty(t, accessToken)
	require.NotEmpty(t, refreshToken)
	require.NotContains(t, response.Body.String(), "accessToken")
	require.NotContains(t, response.Body.String(), "refreshToken")
	require.NotContains(t, response.Body.String(), accessToken)
	require.NotContains(t, response.Body.String(), refreshToken)
}
