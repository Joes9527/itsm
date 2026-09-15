package authentication

import (
	"context"
	"net/http"
	"os"
	"strings"
)

type cookieTransportKey struct{}

// WithCookieTransportPolicy binds startup configuration to a request without
// mutable package state. An omitted setting preserves safe production defaults.
func WithCookieTransportPolicy(request *http.Request, secure *bool, release bool) *http.Request {
	value := release || defaultSecureCookies()
	if secure != nil {
		value = *secure
	}
	return request.WithContext(context.WithValue(request.Context(), cookieTransportKey{}, value))
}

func defaultSecureCookies() bool {
	return strings.EqualFold(os.Getenv("ENV"), "production") || strings.EqualFold(os.Getenv("GIN_MODE"), "release")
}

// ShouldUseSecureCookies is shared by session, OAuth state and CSRF cookies.
// TLS and forwarded HTTPS only strengthen the policy: client-supplied forwarding
// headers can never opt an HTTP request out of Secure. The ingress must replace
// forwarding headers with its observed transport, never append client values.
func ShouldUseSecureCookies(request *http.Request) bool {
	if request != nil {
		if request.TLS != nil || strings.EqualFold(request.Header.Get("X-Forwarded-Proto"), "https") {
			return true
		}
		if secure, ok := request.Context().Value(cookieTransportKey{}).(bool); ok {
			return secure
		}
	}
	return defaultSecureCookies()
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
