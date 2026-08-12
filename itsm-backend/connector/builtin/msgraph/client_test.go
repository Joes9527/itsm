package msgraph

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
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
}
