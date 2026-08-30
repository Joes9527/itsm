package middleware

import (
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/require"
)

func TestGenerateCSRFTokenDoesNotSetSecureCookieForPlainHTTP(t *testing.T) {
	gin.SetMode(gin.TestMode)
	w := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(w)
	c.Request = httptest.NewRequest(http.MethodGet, "http://localhost:3010/api/v1/csrf-token", nil)

	GenerateCSRFToken(c, &CSRFConfig{
		TokenLength:  32,
		CookieName:   CSRFTokenCookieName,
		CookieMaxAge: 86400,
		Secure:       true,
	})

	cookies := w.Result().Cookies()
	require.Len(t, cookies, 1)
	require.Equal(t, CSRFTokenCookieName, cookies[0].Name)
	require.False(t, cookies[0].Secure)
}

func TestGenerateCSRFTokenSetsSecureCookieForForwardedHTTPS(t *testing.T) {
	gin.SetMode(gin.TestMode)
	w := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(w)
	c.Request = httptest.NewRequest(http.MethodGet, "http://itsm.example.com/api/v1/csrf-token", nil)
	c.Request.Header.Set("X-Forwarded-Proto", "https")

	GenerateCSRFToken(c, &CSRFConfig{
		TokenLength:  32,
		CookieName:   CSRFTokenCookieName,
		CookieMaxAge: 86400,
		Secure:       true,
	})

	cookies := w.Result().Cookies()
	require.Len(t, cookies, 1)
	require.Equal(t, CSRFTokenCookieName, cookies[0].Name)
	require.True(t, cookies[0].Secure)
}