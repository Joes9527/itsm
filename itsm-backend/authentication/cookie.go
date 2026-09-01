package authentication

import (
	"net/http"
	"os"
	"strings"
)

// ShouldUseSecureCookies is the single transport policy for session and OAuth
// state cookies. Production/release mode fails safe even if a reverse proxy
// omits X-Forwarded-Proto.
func ShouldUseSecureCookies(request *http.Request) bool {
	if request != nil {
		if request.TLS != nil || strings.EqualFold(request.Header.Get("X-Forwarded-Proto"), "https") {
			return true
		}
	}
	return strings.EqualFold(os.Getenv("ENV"), "production") || strings.EqualFold(os.Getenv("GIN_MODE"), "release")
}

func WriteSessionCookies(writer http.ResponseWriter, request *http.Request, tokens *SessionTokens) {
	if writer == nil || tokens == nil {
		return
	}
	writeCookie(writer, request, "access_token", tokens.AccessToken, int(AccessTokenTTL.Seconds()))
	if tokens.RefreshToken != "" {
		writeCookie(writer, request, "refresh_token", tokens.RefreshToken, int(RefreshTokenTTL.Seconds()))
	}
}

func ClearSessionCookies(writer http.ResponseWriter, request *http.Request) {
	writeCookie(writer, request, "access_token", "", -1)
	writeCookie(writer, request, "refresh_token", "", -1)
}

func WriteOAuthStateCookie(writer http.ResponseWriter, request *http.Request, value string, maxAge int) {
	writeCookie(writer, request, "azure_oauth_state", value, maxAge)
}

func writeCookie(writer http.ResponseWriter, request *http.Request, name, value string, maxAge int) {
	http.SetCookie(writer, &http.Cookie{
		Name: name, Value: value, Path: "/", MaxAge: maxAge,
		HttpOnly: true, Secure: ShouldUseSecureCookies(request), SameSite: http.SameSiteLaxMode,
	})
}
