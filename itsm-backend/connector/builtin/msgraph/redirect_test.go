package msgraph

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"

	"github.com/stretchr/testify/assert"
)

func TestGraphDestinationDoesNotFollowRedirect(t *testing.T) {
	for _, route := range []string{"token", "send"} {
		t.Run(route, func(t *testing.T) {
			var redirected atomic.Int32
			target := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				redirected.Add(1)
				if route == "token" {
					w.Header().Set("Content-Type", "application/json")
					w.Write([]byte(`{"access_token":"test-token","expires_in":3600}`))
					return
				}
				w.WriteHeader(http.StatusAccepted)
			}))
			defer target.Close()
			source := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if route == "send" && strings.Contains(r.URL.Path, "/oauth2/") {
					w.Header().Set("Content-Type", "application/json")
					w.Write([]byte(`{"access_token":"test-token","expires_in":3600}`))
					return
				}
				w.Header().Set("Location", target.URL+"/redirected")
				w.WriteHeader(http.StatusTemporaryRedirect)
				if route == "token" {
					w.Write([]byte(`{"access_token":"must-not-accept","expires_in":3600}`))
				}
			}))
			defer source.Close()
			client := NewClient("tenant", "app", "private-test-secret", source.URL, source.URL)
			var err error
			if route == "token" {
				_, err = client.Token(context.Background())
			} else {
				err = client.SendMail(context.Background(), "mailbox@example.invalid", "recipient@example.invalid", "test", "body", "delivery")
			}
			assert.Error(t, err)
			assert.Zero(t, redirected.Load(), "declared endpoint must not transfer a request to another endpoint")
		})
	}
}
