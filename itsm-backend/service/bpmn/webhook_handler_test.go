package bpmn

import (
	"context"
	"errors"
	"fmt"
	"net"
	"net/http"
	"net/http/httptest"
	"testing"

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
	handler := NewWebhookHandler(nil, zap.New(core).Sugar())
	serverAddress := server.Listener.Addr().String()
	handler.httpClient = &http.Client{Transport: &http.Transport{
		DialContext: func(ctx context.Context, network, _ string) (net.Conn, error) {
			return (&net.Dialer{}).DialContext(ctx, network, serverAddress)
		},
	}}

	ctx := WithBPMNCallbackExecutionKey(context.Background(), "callback-idempotency-key")
	variables := map[string]interface{}{
		"webhook_url": "http://hooks.example.com/event",
		"headers":     `{"Idempotency-Key":"caller-supplied-key"}`,
	}
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

func TestWebhookExecuteSanitizesTransportFailureLog(t *testing.T) {
	const (
		endpointURL   = "http://hooks.example.com/sensitive-endpoint"
		errorSentinel = "tenant-7-secret-sql"
	)
	core, logs := observer.New(zap.DebugLevel)
	handler := NewWebhookHandler(nil, zap.New(core).Sugar())
	handler.httpClient = &http.Client{Transport: roundTripperFunc(func(*http.Request) (*http.Response, error) {
		return nil, errors.New(errorSentinel)
	})}

	_, err := handler.Execute(context.Background(), nil, map[string]interface{}{"webhook_url": endpointURL})
	require.Error(t, err)
	for _, entry := range logs.All() {
		require.NotContains(t, entry.Message, errorSentinel)
		require.NotContains(t, fmt.Sprint(entry.ContextMap()), errorSentinel)
		require.NotContains(t, entry.Message, endpointURL)
		require.NotContains(t, fmt.Sprint(entry.ContextMap()), endpointURL)
	}
}

type roundTripperFunc func(*http.Request) (*http.Response, error)

func (f roundTripperFunc) RoundTrip(req *http.Request) (*http.Response, error) {
	return f(req)
}
