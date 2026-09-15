package middleware

import (
	"net/http"
	"net/http/httptest"
	"testing"

	"itsm-backend/authentication"

	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/require"
)

func TestGenerateCSRFTokenDoesNotSetSecureCookieForExplicitPlainHTTP(t *testing.T) {
	t.Setenv("ENV", "production")
	gin.SetMode(gin.TestMode)
	w := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(w)
	c.Request = httptest.NewRequest(http.MethodGet, "http://localhost:3010/api/v1/csrf-token", nil)

	no := false
	c.Request = authentication.WithCookieTransportPolicy(c.Request, &no, true)
	GenerateCSRFToken(c, &CSRFConfig{
		TokenLength:  32,
		CookieName:   CSRFTokenCookieName,
		CookieMaxAge: 86400,
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
	})

	cookies := w.Result().Cookies()
	require.Len(t, cookies, 1)
	require.Equal(t, CSRFTokenCookieName, cookies[0].Name)
	require.True(t, cookies[0].Secure)
}

func TestCSRFCookieUsesSessionTransportPolicy(t *testing.T) {
	t.Setenv("ENV", "production")
	for _, tc := range []struct {
		name, url string
		want      bool
	}{
		{name: "production default", url: "http://itsm.internal", want: true},
		{name: "HTTPS", url: "https://itsm.example", want: true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			w := httptest.NewRecorder()
			c, _ := gin.CreateTestContext(w)
			c.Request = authentication.WithCookieTransportPolicy(httptest.NewRequest("GET", tc.url, nil), nil, false)
			GenerateCSRFToken(c, DefaultCSRFConfig())
			cookies := w.Result().Cookies()
			require.Len(t, cookies, 1)
			require.Equal(t, tc.want, cookies[0].Secure)
			require.True(t, cookies[0].HttpOnly)
		})
	}
}
