package authentication

import (
	"crypto/tls"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestShouldUseSecureCookies(t *testing.T) {
	t.Run("TLS", func(t *testing.T) {
		req := httptest.NewRequest("GET", "https://itsm.example", nil)
		req.TLS = &tls.ConnectionState{}
		require.True(t, ShouldUseSecureCookies(req))
	})
	t.Run("forwarded HTTPS", func(t *testing.T) {
		req := httptest.NewRequest("GET", "http://itsm.internal", nil)
		req.Header.Set("X-Forwarded-Proto", "https")
		require.True(t, ShouldUseSecureCookies(req))
	})
	t.Run("production environment", func(t *testing.T) {
		t.Setenv("ENV", "production")
		t.Setenv("GIN_MODE", "debug")
		require.True(t, ShouldUseSecureCookies(httptest.NewRequest("GET", "http://itsm.internal", nil)))
	})
	t.Run("local HTTP", func(t *testing.T) {
		t.Setenv("ENV", "development")
		t.Setenv("GIN_MODE", "debug")
		require.False(t, ShouldUseSecureCookies(httptest.NewRequest("GET", "http://localhost", nil)))
	})
}

func TestWriteSessionCookiesUsesCanonicalAttributesAndTTLs(t *testing.T) {
	req := httptest.NewRequest(http.MethodPost, "https://itsm.example/api/v1/auth/login", nil)
	rec := httptest.NewRecorder()
	WriteSessionCookies(rec, req, &SessionTokens{AccessToken: "access-value", RefreshToken: "refresh-value"})

	cookies := rec.Result().Cookies()
	require.Len(t, cookies, 2)
	for _, cookie := range cookies {
		require.True(t, cookie.HttpOnly)
		require.True(t, cookie.Secure)
		require.Equal(t, http.SameSiteLaxMode, cookie.SameSite)
		require.Equal(t, "/", cookie.Path)
	}
	require.Equal(t, int(AccessTokenTTL.Seconds()), cookies[0].MaxAge)
	require.Equal(t, int(RefreshTokenTTL.Seconds()), cookies[1].MaxAge)
}
