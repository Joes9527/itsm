package bpmn

import (
	"context"
	"errors"
	"fmt"
	"net"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"itsm-backend/ent"
	"itsm-backend/ent/enttest"

	_ "github.com/mattn/go-sqlite3"
	"github.com/stretchr/testify/require"
	"go.uber.org/zap"
	"go.uber.org/zap/zaptest/observer"
)

func TestValidateWebhookURLBlocksSSRF(t *testing.T) {
	blocked := []string{
		"file:///etc/passwd",
		"http://localhost/admin",
		"http://127.0.0.1/admin",
		"http://10.0.0.1/",
		"http://169.254.169.254/latest/meta-data/",
		"http://[::1]/",
	}
	for _, target := range blocked {
		if err := validateWebhookURL(target); err == nil {
			t.Errorf("expected %q to be blocked", target)
		}
	}
	if err := validateWebhookURL("https://hooks.example.com/event"); err != nil {
		t.Fatalf("expected public HTTPS URL to be accepted: %v", err)
	}
}

func TestWebhookExecutePropagatesIdempotencyKey(t *testing.T) {
	const responseBodySentinel = "tenant-7-secret-sql"
	keys := make([]string, 0, 2)
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		keys = append(keys, r.Header.Get("Idempotency-Key"))
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(responseBodySentinel))
	}))
	defer server.Close()

	core, logs := observer.New(zap.DebugLevel)
	client, handler, variables, ctx := newTrustedWebhookTestHandler(t, zap.New(core).Sugar(), "http://hooks.example.com/event")
	_ = client
	serverAddress := server.Listener.Addr().String()
	handler.httpClient = &http.Client{Transport: &http.Transport{
		DialContext: func(ctx context.Context, network, _ string) (net.Conn, error) {
			return (&net.Dialer{}).DialContext(ctx, network, serverAddress)
		},
	}}

	ctx = WithBPMNCallbackExecutionKey(ctx, "callback-idempotency-key")
	variables["webhook_url"] = "http://attacker.example.invalid/hook"
	variables["headers"] = `{"Idempotency-Key":"caller-supplied-key"}`
	_, err := handler.Execute(ctx, nil, variables)
	require.NoError(t, err)
	_, err = handler.Execute(ctx, nil, variables)
	require.NoError(t, err)
	require.Equal(t, []string{"callback-idempotency-key", "callback-idempotency-key"}, keys)
	for _, entry := range logs.All() {
		require.NotContains(t, entry.Message, responseBodySentinel)
		require.NotContains(t, fmt.Sprint(entry.ContextMap()), responseBodySentinel)
	}
}

func TestWebhookExecuteTreatsEveryNon2xxAsFailureAndReusesIdempotencyKey(t *testing.T) {
	keys := make([]string, 0, 2)
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		keys = append(keys, r.Header.Get("Idempotency-Key"))
		w.WriteHeader(http.StatusTemporaryRedirect)
	}))
	defer server.Close()

	_, handler, variables, ctx := newTrustedWebhookTestHandler(t, zap.NewNop().Sugar(), "http://hooks.example.com/event")
	serverAddress := server.Listener.Addr().String()
	handler.httpClient = &http.Client{Transport: &http.Transport{
		DialContext: func(ctx context.Context, network, _ string) (net.Conn, error) {
			return (&net.Dialer{}).DialContext(ctx, network, serverAddress)
		},
	}}
	ctx = WithBPMNCallbackExecutionKey(ctx, "stable-retry-key")

	_, err := handler.Execute(ctx, nil, variables)
	require.Error(t, err)
	_, err = handler.Execute(ctx, nil, variables)
	require.Error(t, err)
	require.Equal(t, []string{"stable-retry-key", "stable-retry-key"}, keys)
}

func TestWebhookExecuteSanitizesTransportFailureLog(t *testing.T) {
	const (
		endpointURL   = "http://hooks.example.com/sensitive-endpoint"
		errorSentinel = "tenant-7-secret-sql"
	)
	core, logs := observer.New(zap.DebugLevel)
	_, handler, variables, ctx := newTrustedWebhookTestHandler(t, zap.New(core).Sugar(), endpointURL)
	handler.httpClient = &http.Client{Transport: roundTripperFunc(func(*http.Request) (*http.Response, error) {
		return nil, errors.New(errorSentinel)
	})}

	ctx = WithBPMNCallbackExecutionKey(ctx, "transport-failure-key")
	_, err := handler.Execute(ctx, nil, variables)
	require.Error(t, err)
	for _, entry := range logs.All() {
		require.NotContains(t, entry.Message, errorSentinel)
		require.NotContains(t, fmt.Sprint(entry.ContextMap()), errorSentinel)
		require.NotContains(t, entry.Message, endpointURL)
		require.NotContains(t, fmt.Sprint(entry.ContextMap()), endpointURL)
	}
}

func newTrustedWebhookTestHandler(t *testing.T, logger *zap.SugaredLogger, endpoint string) (*ent.Client, *WebhookHandler, map[string]interface{}, context.Context) {
	t.Helper()
	dsn := "file:" + strings.NewReplacer("/", "-", " ", "-").Replace(t.Name()) + "?mode=memory&cache=shared&_fk=1"
	client := enttest.Open(t, "sqlite3", dsn)
	t.Cleanup(func() { _ = client.Close() })
	ctx := context.Background()
	tenant := client.Tenant.Create().
		SetName("Webhook tenant").
		SetCode("webhook-" + strings.ToLower(strings.ReplaceAll(t.Name(), "/", "-"))).
		SetStatus("active").
		SaveX(ctx)
	client.ConnectorConfig.Create().
		SetTenantID(tenant.ID).
		SetName("bpmn-events").
		SetProvider("generic").
		SetEnabled(true).
		SetSettings(fmt.Sprintf(`{"url":%q,"timeoutSeconds":5}`, endpoint)).
		SetCredentials(`{"secret":"configured-at-execution"}`).
		SaveX(ctx)
	ctx = context.WithValue(ctx, BPMNTenantIDContextKey, tenant.ID)
	variables := map[string]interface{}{
		"action":              "call_webhook",
		"callback_config_ref": "bpmn-events",
		"business_type":       "ticket",
		"business_id":         42,
		"event_type":          "ticket.updated",
		"title":               "Ticket updated",
		"content":             "safe typed content",
	}
	return client, NewWebhookHandler(client, logger), variables, ctx
}

type roundTripperFunc func(*http.Request) (*http.Response, error)

func (f roundTripperFunc) RoundTrip(req *http.Request) (*http.Response, error) {
	return f(req)
}
