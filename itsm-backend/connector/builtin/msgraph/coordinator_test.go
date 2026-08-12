package msgraph

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"go.uber.org/zap/zaptest"

	"itsm-backend/connector"
)

// fakeStore is an in-memory TicketStore for coordinator tests.
type fakeStore struct {
	mu                 sync.Mutex
	usersByEmail       map[string]int // email -> userID (present = found)
	existingExternalID map[string]bool
	created            []InboundTicketRequest
	comments           []string
	nextTicketID       int
}

func newFakeStore() *fakeStore {
	return &fakeStore{
		usersByEmail:       map[string]int{},
		existingExternalID: map[string]bool{},
		nextTicketID:       1,
	}
}

func (f *fakeStore) FindActiveUserByEmail(_ context.Context, _ int, email string) (int, bool, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	id, ok := f.usersByEmail[email]
	return id, ok, nil
}

func (f *fakeStore) TicketExistsForExternalMessage(_ context.Context, _ int, externalMessageID string) (bool, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.existingExternalID[externalMessageID], nil
}

func (f *fakeStore) CreateTicket(_ context.Context, _ int, req InboundTicketRequest) (int, string, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	id := f.nextTicketID
	f.nextTicketID++
	f.created = append(f.created, req)
	f.existingExternalID[req.ExternalMessageID] = true
	return id, fmt.Sprintf("TCK-%04d", id), nil
}

func (f *fakeStore) PostSystemComment(_ context.Context, _, _, _ int, content string) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.comments = append(f.comments, content)
	return nil
}

// fakeTriager returns a fixed suggestion.
type fakeTriager struct{ suggestion TriageSuggestion }

func (f fakeTriager) Suggest(_ context.Context, _ int, _, _ string) TriageSuggestion {
	return f.suggestion
}

func newTestGraphServer(t *testing.T, messages []map[string]interface{}) (*httptest.Server, *httptest.Server) {
	t.Helper()
	aad := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"access_token":"tok","expires_in":3599}`))
	}))
	polled := false
	graph := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		if polled {
			_, _ = w.Write([]byte(`{"@odata.deltaLink":"` + graphDeltaLinkPlaceholder + `","value":[]}`))
			return
		}
		polled = true
		body := map[string]interface{}{
			"@odata.deltaLink": graphDeltaLinkPlaceholder,
			"value":            messages,
		}
		_ = writeJSON(w, body)
	}))
	return aad, graph
}

const graphDeltaLinkPlaceholder = "https://graph.example/delta-link"

func TestCoordinator_HandleMessage_CreatesTicketAndReplies(t *testing.T) {
	store := newFakeStore()
	store.usersByEmail["alice@contoso.com"] = 42
	triager := fakeTriager{suggestion: TriageSuggestion{Category: "network", Priority: "high", Confidence: 0.8, Explanation: "mentions VPN"}}

	var sentMail map[string]interface{}
	aad := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte(`{"access_token":"tok","expires_in":3599}`))
	}))
	defer aad.Close()
	graph := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method == http.MethodPost {
			_ = decodeJSON(r, &sentMail)
			w.WriteHeader(http.StatusAccepted)
			return
		}
		w.WriteHeader(http.StatusOK)
	}))
	defer graph.Close()

	conn := New()
	require.NoError(t, conn.Init(context.Background(), connectorConfigFor("support@contoso.com", aad.URL, graph.URL)))

	coord := NewEmailPollingCoordinator(store, triager, zaptest.NewLogger(t).Sugar())
	coord.handleMessage(context.Background(), 7, conn, Message{
		ID:                "m1",
		InternetMessageID: "<abc@contoso.com>",
		Subject:           "VPN broken",
		BodyContent:       "Cannot connect to VPN.",
		FromAddress:       "alice@contoso.com",
	})

	require.Len(t, store.created, 1)
	assert.Equal(t, "VPN broken", store.created[0].Title)
	assert.Equal(t, "Cannot connect to VPN.", store.created[0].Description)
	assert.Equal(t, "high", store.created[0].Priority, "AI priority must be auto-applied")
	assert.Equal(t, 42, store.created[0].RequesterID)
	assert.Equal(t, "email", store.created[0].Source)
	assert.Equal(t, "<abc@contoso.com>", store.created[0].ExternalMessageID)
	assert.Equal(t, "alice@contoso.com", store.created[0].CreatorEmail)

	require.Len(t, store.comments, 1)
	assert.Contains(t, store.comments[0], "network", "category must be recorded as advisory, not applied to the ticket")
	assert.Contains(t, store.comments[0], "80%")

	require.NotNil(t, sentMail, "confirmation reply must be sent")
	message := sentMail["message"].(map[string]interface{})
	assert.Contains(t, message["subject"], "TCK-0001")
}

func TestCoordinator_HandleMessage_SkipsDuplicateExternalMessageID(t *testing.T) {
	store := newFakeStore()
	store.usersByEmail["alice@contoso.com"] = 42
	store.existingExternalID["<abc@contoso.com>"] = true
	triager := fakeTriager{suggestion: TriageSuggestion{Priority: "medium"}}

	conn := New()
	require.NoError(t, conn.Init(context.Background(), connectorConfigFor("support@contoso.com", "http://unused", "http://unused")))

	coord := NewEmailPollingCoordinator(store, triager, zaptest.NewLogger(t).Sugar())
	coord.handleMessage(context.Background(), 7, conn, Message{
		InternetMessageID: "<abc@contoso.com>",
		Subject:           "Duplicate",
		FromAddress:       "alice@contoso.com",
	})

	assert.Empty(t, store.created, "must not create a second ticket for an already-processed message")
}

func TestCoordinator_HandleMessage_SkipsUnregisteredSender(t *testing.T) {
	store := newFakeStore() // no users registered
	triager := fakeTriager{suggestion: TriageSuggestion{Priority: "medium"}}

	conn := New()
	require.NoError(t, conn.Init(context.Background(), connectorConfigFor("support@contoso.com", "http://unused", "http://unused")))

	coord := NewEmailPollingCoordinator(store, triager, zaptest.NewLogger(t).Sugar())
	coord.handleMessage(context.Background(), 7, conn, Message{
		InternetMessageID: "<xyz@contoso.com>",
		Subject:           "From nobody",
		FromAddress:       "unknown@contoso.com",
	})

	assert.Empty(t, store.created, "must not create a ticket for a sender not found in this tenant")
}

func TestCoordinator_StartStop_CancelsPolling(t *testing.T) {
	store := newFakeStore()
	triager := fakeTriager{suggestion: TriageSuggestion{Priority: "medium"}}
	coord := NewEmailPollingCoordinator(store, triager, zaptest.NewLogger(t).Sugar())

	aad, graph := newTestGraphServer(t, nil)
	defer aad.Close()
	defer graph.Close()

	conn := New()
	cfg := connectorConfigFor("support@contoso.com", aad.URL, graph.URL)
	cfg.Settings["poll_interval_seconds"] = float64(60)
	require.NoError(t, conn.Init(context.Background(), cfg))

	coord.Start(context.Background(), 7, conn)
	time.Sleep(50 * time.Millisecond) // let the immediate first poll run
	coord.Stop(7)

	coord.mu.Lock()
	_, stillRunning := coord.cancels[7]
	coord.mu.Unlock()
	assert.False(t, stillRunning)
}

func writeJSON(w http.ResponseWriter, v interface{}) error {
	return json.NewEncoder(w).Encode(v)
}

func decodeJSON(r *http.Request, v interface{}) error {
	return json.NewDecoder(r.Body).Decode(v)
}

func connectorConfigFor(mailbox, aadURL, graphURL string) connector.Config {
	return connector.Config{
		Settings: map[string]interface{}{
			"azure_tenant_id": "test-tenant",
			"mailbox":         mailbox,
			"aad_base_url":    aadURL,
			"graph_base_url":  graphURL,
		},
		Credentials: map[string]string{
			"azure_client_id":     "id",
			"azure_client_secret": "secret",
		},
	}
}
