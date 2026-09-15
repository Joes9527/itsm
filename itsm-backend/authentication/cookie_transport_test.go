package authentication

import (
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestCookieTransportPolicy(t *testing.T) {
	yes, no := true, false
	for _, tt := range []struct {
		name, env, mode, url, proto string
		secure                      *bool
		release, want               bool
	}{
		{name: "production default", env: "production", want: true},
		{name: "release environment default", mode: "release", want: true},
		{name: "configured release default", release: true, want: true},
		{name: "development default"},
		{name: "explicit HTTP in production", env: "production", mode: "release", release: true, secure: &no},
		{name: "explicit secure in development", secure: &yes, want: true},
		{name: "TLS cannot be downgraded", url: "https://itsm.example", secure: &no, want: true},
		{name: "forwarded HTTPS cannot be downgraded", proto: "https", secure: &no, want: true},
		{name: "forwarded HTTP cannot downgrade production", env: "production", proto: "http", want: true},
	} {
		t.Run(tt.name, func(t *testing.T) {
			t.Setenv("ENV", tt.env)
			t.Setenv("GIN_MODE", tt.mode)
			url := tt.url
			if url == "" {
				url = "http://192.168.31.66:3010"
			}
			req := httptest.NewRequest(http.MethodPost, url, nil)
			req.Header.Set("X-Forwarded-Proto", tt.proto)
			req = WithCookieTransportPolicy(req, tt.secure, tt.release)
			require.Equal(t, tt.want, ShouldUseSecureCookies(req))
			for _, clear := range []bool{false, true} {
				rec := httptest.NewRecorder()
				if clear {
					ClearSessionCookies(rec, req)
					WriteOAuthStateCookie(rec, req, "", -1)
				} else {
					WriteSessionCookies(rec, req, &SessionTokens{AccessToken: "access", RefreshToken: "refresh"})
					WriteOAuthStateCookie(rec, req, "state", 300)
				}
				cookies := rec.Result().Cookies()
				require.Len(t, cookies, 3)
				for _, c := range cookies {
					require.Equal(t, tt.want, c.Secure)
					require.True(t, c.HttpOnly)
					require.Equal(t, "/", c.Path)
					require.Empty(t, c.Domain)
					require.Equal(t, http.SameSiteLaxMode, c.SameSite)
					if clear {
						require.Equal(t, -1, c.MaxAge)
						require.Empty(t, c.Value)
					} else {
						require.Positive(t, c.MaxAge)
					}
				}
			}
		})
	}
}
