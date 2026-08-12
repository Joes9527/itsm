package msgraph

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"net/url"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestClient_Token_FetchesAndCaches(t *testing.T) {
	calls := 0
	aad := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls++
		assert.Equal(t, "/test-tenant/oauth2/v2.0/token", r.URL.Path)
		require.NoError(t, r.ParseForm())
		assert.Equal(t, "client_credentials", r.FormValue("grant_type"))
		assert.Equal(t, "https://graph.microsoft.com/.default", r.FormValue("scope"))
		assert.Equal(t, "test-client-id", r.FormValue("client_id"))
		assert.Equal(t, "test-secret", r.FormValue("client_secret"))
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]interface{}{
			"access_token": "tok-123",
			"expires_in":   3599,
			"token_type":   "Bearer",
		})
	}))
	defer aad.Close()

	c := NewClient("test-tenant", "test-client-id", "test-secret", aad.URL, "")
	ctx := context.Background()

	tok, err := c.Token(ctx)
	require.NoError(t, err)
	assert.Equal(t, "tok-123", tok)
	assert.Equal(t, 1, calls)

	tok2, err := c.Token(ctx)
	require.NoError(t, err)
	assert.Equal(t, "tok-123", tok2)
	assert.Equal(t, 1, calls, "second call within TTL must not hit the network")
}

func TestClient_Token_ErrorResponse(t *testing.T) {
	aad := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusBadRequest)
		_ = json.NewEncoder(w).Encode(map[string]interface{}{
			"error":             "invalid_client",
			"error_description": "AADSTS7000215: Invalid client secret",
		})
	}))
	defer aad.Close()

	c := NewClient("test-tenant", "bad-id", "bad-secret", aad.URL, "")
	_, err := c.Token(context.Background())
	require.Error(t, err)
	assert.Contains(t, err.Error(), "invalid_client")
}

func tokenHandler() http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]interface{}{
			"access_token": "tok-123",
			"expires_in":   3599,
		})
	}
}

func TestClient_PollDelta_FirstCallNoLink(t *testing.T) {
	aad := httptest.NewServer(tokenHandler())
	defer aad.Close()

	graph := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		assert.Equal(t, "/users/support@contoso.com/mailFolders('inbox')/messages/delta", r.URL.Path)
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]interface{}{
			"@odata.deltaLink": "https://graph.example/delta-link-1",
			"value": []map[string]interface{}{
				{
					"id":                "msg-1",
					"internetMessageId": "<abc@contoso.com>",
					"subject":           "Help needed",
					"receivedDateTime":  "2026-08-11T10:00:00Z",
					"from": map[string]interface{}{
						"emailAddress": map[string]interface{}{"address": "Alice@Contoso.com"},
					},
					"body": map[string]interface{}{
						"contentType": "text",
						"content":     "Something is broken.",
					},
				},
			},
		})
	}))
	defer graph.Close()

	c := NewClient("test-tenant", "id", "secret", aad.URL, graph.URL)
	msgs, next, err := c.PollDelta(context.Background(), "support@contoso.com", "")
	require.NoError(t, err)
	require.Len(t, msgs, 1)
	assert.Equal(t, "<abc@contoso.com>", msgs[0].InternetMessageID)
	assert.Equal(t, "Help needed", msgs[0].Subject)
	assert.Equal(t, "alice@contoso.com", msgs[0].FromAddress, "from address must be lowercased")
	assert.Equal(t, "Something is broken.", msgs[0].BodyContent)
	assert.Equal(t, "https://graph.example/delta-link-1", next)
}

// TestClient_PollDelta_FirstCallRequestsDeltaTokenLatest is a regression test:
// a delta query with no prior deltaLink returns Graph's documented behavior
// of the FULL current folder contents, not just new-since-now items. Passing
// $deltatoken=latest on the very first call tells Graph to skip straight to
// "caught up" instead of enumerating the mailbox's entire history as if it
// were all brand-new mail (which would create a ticket + confirmation reply
// per pre-existing message the very first time a tenant enables this
// connector).
func TestClient_PollDelta_FirstCallRequestsDeltaTokenLatest(t *testing.T) {
	aad := httptest.NewServer(tokenHandler())
	defer aad.Close()

	var capturedRawQuery string
	graph := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		capturedRawQuery = r.URL.RawQuery
		assert.Equal(t, "/users/support@contoso.com/mailFolders('inbox')/messages/delta", r.URL.Path)
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]interface{}{
			"@odata.deltaLink": "https://graph.example/delta-link-1",
			"value":            []map[string]interface{}{},
		})
	}))
	defer graph.Close()

	c := NewClient("test-tenant", "id", "secret", aad.URL, graph.URL)
	_, _, err := c.PollDelta(context.Background(), "support@contoso.com", "")
	require.NoError(t, err)

	assert.Equal(t, "latest", r2QueryValue(t, capturedRawQuery, "$deltatoken"), "first call (no deltaLink) must request $deltatoken=latest so Graph doesn't enumerate full mailbox history")
}

// TestClient_PollDelta_SubsequentCallDoesNotAddDeltaTokenLatest asserts that
// once a deltaLink from a prior response is passed in, PollDelta hits that
// literal URL as-is — it must NOT re-append $deltatoken=latest (Graph's
// returned @odata.deltaLink/@odata.nextLink already encode whatever token
// semantics are needed for that call).
func TestClient_PollDelta_SubsequentCallDoesNotAddDeltaTokenLatest(t *testing.T) {
	aad := httptest.NewServer(tokenHandler())
	defer aad.Close()

	var capturedPath, capturedRawQuery string
	graph := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		capturedPath = r.URL.Path
		capturedRawQuery = r.URL.RawQuery
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]interface{}{
			"@odata.deltaLink": "https://graph.example/delta-link-2",
			"value":            []map[string]interface{}{},
		})
	}))
	defer graph.Close()

	c := NewClient("test-tenant", "id", "secret", aad.URL, graph.URL)
	priorDeltaLink := graph.URL + "/prior-delta-link?$deltatoken=abc123"
	_, _, err := c.PollDelta(context.Background(), "support@contoso.com", priorDeltaLink)
	require.NoError(t, err)

	assert.Equal(t, "/prior-delta-link", capturedPath, "a non-empty deltaLink must be used verbatim, without appending $deltatoken=latest")
	assert.Equal(t, "$deltatoken=abc123", capturedRawQuery, "must not append/replace the token on a call that already carries a deltaLink")
}

// r2QueryValue is a tiny helper to pull a single query param's value out of
// a raw query string, without pulling in net/url.ParseQuery duplication at
// every call site.
func r2QueryValue(t *testing.T, rawQuery, key string) string {
	t.Helper()
	values, err := url.ParseQuery(rawQuery)
	require.NoError(t, err)
	return values.Get(key)
}

func TestClient_PollDelta_FollowsNextLinkUntilDeltaLink(t *testing.T) {
	aad := httptest.NewServer(tokenHandler())
	defer aad.Close()

	pageTwoURL := ""
	mux := http.NewServeMux()
	mux.HandleFunc("/users/support@contoso.com/mailFolders('inbox')/messages/delta", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]interface{}{
			"@odata.nextLink": pageTwoURL,
			"value": []map[string]interface{}{
				{"id": "msg-1", "internetMessageId": "<p1@contoso.com>", "subject": "Page 1",
					"from": map[string]interface{}{"emailAddress": map[string]interface{}{"address": "a@contoso.com"}},
					"body": map[string]interface{}{"contentType": "text", "content": "p1"}},
			},
		})
	})
	mux.HandleFunc("/page2", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]interface{}{
			"@odata.deltaLink": "https://graph.example/delta-link-final",
			"value": []map[string]interface{}{
				{"id": "msg-2", "internetMessageId": "<p2@contoso.com>", "subject": "Page 2",
					"from": map[string]interface{}{"emailAddress": map[string]interface{}{"address": "b@contoso.com"}},
					"body": map[string]interface{}{"contentType": "text", "content": "p2"}},
			},
		})
	})
	graph := httptest.NewServer(mux)
	defer graph.Close()
	pageTwoURL = graph.URL + "/page2"

	c := NewClient("test-tenant", "id", "secret", aad.URL, graph.URL)
	msgs, next, err := c.PollDelta(context.Background(), "support@contoso.com", "")
	require.NoError(t, err)
	require.Len(t, msgs, 2, "must accumulate messages across pages")
	assert.Equal(t, "<p1@contoso.com>", msgs[0].InternetMessageID)
	assert.Equal(t, "<p2@contoso.com>", msgs[1].InternetMessageID)
	assert.Equal(t, "https://graph.example/delta-link-final", next)
}

func TestClient_SendMail(t *testing.T) {
	aad := httptest.NewServer(tokenHandler())
	defer aad.Close()

	var captured map[string]interface{}
	graph := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		assert.Equal(t, "/users/support@contoso.com/sendMail", r.URL.Path)
		require.NoError(t, json.NewDecoder(r.Body).Decode(&captured))
		w.WriteHeader(http.StatusAccepted)
	}))
	defer graph.Close()

	c := NewClient("test-tenant", "id", "secret", aad.URL, graph.URL)
	err := c.SendMail(context.Background(), "support@contoso.com", "alice@contoso.com", "Re: Help", "We got it, ticket #123")
	require.NoError(t, err)

	message := captured["message"].(map[string]interface{})
	assert.Equal(t, "Re: Help", message["subject"])
	recipients := message["toRecipients"].([]interface{})
	require.Len(t, recipients, 1)
	addr := recipients[0].(map[string]interface{})["emailAddress"].(map[string]interface{})["address"]
	assert.Equal(t, "alice@contoso.com", addr)

	// Regression: mail sent through this connector must be marked
	// Auto-Submitted so receiving systems (and any out-of-office
	// auto-responders) don't bounce a reply back into the shared mailbox,
	// which would otherwise create a mail loop.
	headers, ok := message["internetMessageHeaders"].([]interface{})
	require.True(t, ok, "message must include internetMessageHeaders")
	var found bool
	for _, h := range headers {
		hm := h.(map[string]interface{})
		if hm["name"] == "Auto-Submitted" {
			found = true
			assert.Equal(t, "auto-replied", hm["value"])
		}
	}
	assert.True(t, found, "internetMessageHeaders must include an Auto-Submitted header")
}
