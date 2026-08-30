package service

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"itsm-backend/ent"
	"itsm-backend/ent/auditlog"
	"itsm-backend/ent/hook"
	"itsm-backend/ent/outboxevent"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestKafOutboxDispatcher_DispatchesSignedEventAndMarksPublished(t *testing.T) {
	repo, client := newOutboxRepository(t)
	event := validKafDelegateRequested()
	payload, err := json.Marshal(event)
	require.NoError(t, err)
	seedPendingEventWithPayload(t, repo, event.EventID, time.Now().UTC().Add(-time.Second), payload)

	var receivedBody []byte
	var receivedHeader http.Header
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var readErr error
		receivedBody, readErr = io.ReadAll(r.Body)
		require.NoError(t, readErr)
		receivedHeader = r.Header.Clone()
		w.WriteHeader(http.StatusAccepted)
	}))
	defer server.Close()

	dispatcher, err := NewKafOutboxDispatcher(repo, KafOutboxConfig{
		WebhookURL:    server.URL,
		WebhookSecret: "test-secret",
		BatchSize:     1,
		PollInterval:  time.Second,
	})
	require.NoError(t, err)

	require.NoError(t, dispatcher.DispatchOnce(context.Background()))

	persisted, err := client.OutboxEvent.Query().
		Where(outboxevent.EventIDEQ(event.EventID)).
		Only(context.Background())
	require.NoError(t, err)
	assert.Equal(t, outboxEventStatusPublished, persisted.Status)
	assert.Equal(t, payload, receivedBody)
	assert.Equal(t, "application/json", receivedHeader.Get("Content-Type"))
	assert.Equal(t, event.EventID, receivedHeader.Get("X-Event-ID"))
	assert.Equal(t, "sha256="+expectedKafDelegateHMAC(payload, "test-secret"), receivedHeader.Get("X-Webhook-Signature"))
}

func TestKafOutboxDispatcher_SchedulesRetryAfterTransportFailure(t *testing.T) {
	repo, client := newOutboxRepository(t)
	event := validKafDelegateRequested()
	payload, err := json.Marshal(event)
	require.NoError(t, err)
	now := time.Now().UTC().Truncate(time.Millisecond)
	seedPendingEventWithPayload(t, repo, event.EventID, now.Add(-time.Second), payload)

	unavailable := httptest.NewServer(http.NotFoundHandler())
	webhookURL := unavailable.URL
	unavailable.Close()
	dispatcher, err := NewKafOutboxDispatcher(repo, KafOutboxConfig{
		WebhookURL:    webhookURL,
		WebhookSecret: "test-secret",
		BatchSize:     1,
		PollInterval:  time.Second,
	})
	require.NoError(t, err)
	dispatcher.now = func() time.Time { return now }

	require.NoError(t, dispatcher.DispatchOnce(context.Background()))

	persisted, err := client.OutboxEvent.Query().
		Where(outboxevent.EventIDEQ(event.EventID)).
		Only(context.Background())
	require.NoError(t, err)
	assert.Equal(t, outboxEventStatusPending, persisted.Status)
	assert.Equal(t, 1, persisted.AttemptCount)
	assert.WithinDuration(t, now.Add(2*time.Second), persisted.NextAttemptAt, time.Millisecond)
	assert.Contains(t, persisted.LastError, "connection refused")
}

func TestKafOutboxDispatcher_RejectsURLWithoutSecret(t *testing.T) {
	repo, _ := newOutboxRepository(t)

	_, err := NewKafOutboxDispatcher(repo, KafOutboxConfig{
		WebhookURL:   "https://kaf.example.test/webhooks/itsm",
		BatchSize:    20,
		PollInterval: time.Second,
	})

	require.ErrorContains(t, err, "KAF_WEBHOOK_SECRET")
}

func TestKafOutboxDispatcher_RetriesNon2xxWithoutDroppingEvent(t *testing.T) {
	repo, client := newOutboxRepository(t)
	event := validKafDelegateRequested()
	payload, err := json.Marshal(event)
	require.NoError(t, err)
	now := time.Now().UTC().Truncate(time.Millisecond)
	_, err = repo.Enqueue(context.Background(), nil, NewOutboxEvent{
		EventID:       event.EventID,
		EventType:     event.EventType,
		TenantID:      7,
		AggregateType: "process_task",
		AggregateID:   event.TaskID,
		Payload:       payload,
		NextAttemptAt: now.Add(-time.Second),
	})
	require.NoError(t, err)

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusUnauthorized)
		_, _ = w.Write([]byte(`{"detail":"invalid_webhook_signature"}`))
	}))
	defer server.Close()
	dispatcher, err := NewKafOutboxDispatcher(repo, KafOutboxConfig{
		WebhookURL:    server.URL,
		WebhookSecret: "test-secret",
		BatchSize:     1,
		PollInterval:  time.Second,
	})
	require.NoError(t, err)
	dispatcher.now = func() time.Time { return now }

	require.NoError(t, dispatcher.DispatchOnce(context.Background()))

	persisted, err := client.OutboxEvent.Query().
		Where(outboxevent.EventIDEQ(event.EventID)).
		Only(context.Background())
	require.NoError(t, err)
	assert.Equal(t, outboxEventStatusPending, persisted.Status)
	assert.Equal(t, 1, persisted.AttemptCount)
	assert.WithinDuration(t, now.Add(2*time.Second), persisted.NextAttemptAt, time.Millisecond)
	assert.Contains(t, persisted.LastError, "401")
	assert.Contains(t, persisted.LastError, "invalid_webhook_signature")

	audit, err := client.AuditLog.Query().
		Where(auditlog.TenantIDEQ(7), auditlog.ActionEQ("kaf_outbox.delivery_rejected")).
		Only(context.Background())
	require.NoError(t, err)
	assert.Equal(t, http.StatusUnauthorized, audit.StatusCode)
	assert.Equal(t, event.EventID, audit.RequestID)
	assert.Nil(t, audit.RequestBody)
}

func TestKafOutboxDispatcher_KeepsClientRejectionAuditAfterSuccessfulRetry(t *testing.T) {
	repo, client := newOutboxRepository(t)
	event := validKafDelegateRequested()
	payload, err := json.Marshal(event)
	require.NoError(t, err)
	now := time.Now().UTC().Truncate(time.Millisecond)
	seedPendingEventWithPayload(t, repo, event.EventID, now.Add(-time.Second), payload)

	attempts := 0
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		attempts++
		if attempts == 1 {
			w.WriteHeader(http.StatusUnauthorized)
			return
		}
		w.WriteHeader(http.StatusAccepted)
	}))
	defer server.Close()
	dispatcher, err := NewKafOutboxDispatcher(repo, KafOutboxConfig{
		WebhookURL:    server.URL,
		WebhookSecret: "test-secret",
		BatchSize:     1,
		PollInterval:  time.Second,
	})
	require.NoError(t, err)
	dispatcher.now = func() time.Time { return now }

	require.NoError(t, dispatcher.DispatchOnce(context.Background()))
	now = now.Add(2 * time.Second)
	require.NoError(t, dispatcher.DispatchOnce(context.Background()))

	persisted, err := client.OutboxEvent.Query().
		Where(outboxevent.EventIDEQ(event.EventID)).
		Only(context.Background())
	require.NoError(t, err)
	assert.Equal(t, outboxEventStatusPublished, persisted.Status)

	audit, err := client.AuditLog.Query().
		Where(auditlog.RequestIDEQ(event.EventID), auditlog.ActionEQ("kaf_outbox.delivery_rejected")).
		Only(context.Background())
	require.NoError(t, err)
	assert.Equal(t, http.StatusUnauthorized, audit.StatusCode)
	assert.Nil(t, audit.RequestBody)
}

func TestKafOutboxDispatcher_RollsBackRetryWhenClientRejectionAuditFails(t *testing.T) {
	repo, client := newOutboxRepository(t)
	event := validKafDelegateRequested()
	payload, err := json.Marshal(event)
	require.NoError(t, err)
	now := time.Now().UTC().Truncate(time.Millisecond)
	seedPendingEventWithPayload(t, repo, event.EventID, now.Add(-time.Second), payload)
	client.AuditLog.Use(hook.On(hook.FixedError(errors.New("audit unavailable")), ent.OpCreate))

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusUnauthorized)
	}))
	defer server.Close()
	dispatcher, err := NewKafOutboxDispatcher(repo, KafOutboxConfig{
		WebhookURL:    server.URL,
		WebhookSecret: "test-secret",
		BatchSize:     1,
		PollInterval:  time.Second,
	})
	require.NoError(t, err)
	dispatcher.now = func() time.Time { return now }

	err = dispatcher.DispatchOnce(context.Background())
	require.ErrorContains(t, err, "audit unavailable")

	persisted, err := client.OutboxEvent.Query().
		Where(outboxevent.EventIDEQ(event.EventID)).
		Only(context.Background())
	require.NoError(t, err)
	assert.Equal(t, outboxEventStatusPublishing, persisted.Status)
	assert.Zero(t, persisted.AttemptCount)
	assert.Empty(t, persisted.LastError)

	auditCount, err := client.AuditLog.Query().
		Where(auditlog.RequestIDEQ(event.EventID), auditlog.ActionEQ("kaf_outbox.delivery_rejected")).
		Count(context.Background())
	require.NoError(t, err)
	assert.Zero(t, auditCount)
}

func TestKafOutboxDispatcher_RetriesServerErrorsWithoutClientErrorAudit(t *testing.T) {
	repo, client := newOutboxRepository(t)
	event := validKafDelegateRequested()
	payload, err := json.Marshal(event)
	require.NoError(t, err)
	now := time.Now().UTC().Truncate(time.Millisecond)
	seedPendingEventWithPayload(t, repo, event.EventID, now.Add(-time.Second), payload)

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusBadGateway)
	}))
	defer server.Close()
	dispatcher, err := NewKafOutboxDispatcher(repo, KafOutboxConfig{
		WebhookURL:    server.URL,
		WebhookSecret: "test-secret",
		BatchSize:     1,
		PollInterval:  time.Second,
	})
	require.NoError(t, err)
	dispatcher.now = func() time.Time { return now }

	require.NoError(t, dispatcher.DispatchOnce(context.Background()))

	persisted, err := client.OutboxEvent.Query().
		Where(outboxevent.EventIDEQ(event.EventID)).
		Only(context.Background())
	require.NoError(t, err)
	assert.Equal(t, outboxEventStatusPending, persisted.Status)
	assert.Equal(t, 1, persisted.AttemptCount)
	assert.WithinDuration(t, now.Add(2*time.Second), persisted.NextAttemptAt, time.Millisecond)
	assert.Contains(t, persisted.LastError, "502")

	auditCount, err := client.AuditLog.Query().
		Where(auditlog.TenantIDEQ(1), auditlog.ActionEQ("kaf_outbox.delivery_rejected")).
		Count(context.Background())
	require.NoError(t, err)
	assert.Zero(t, auditCount)
}

func TestKafOutboxRetryDelayCapsAtFiveMinutes(t *testing.T) {
	assert.Equal(t, 2*time.Second, kafOutboxRetryDelay(1))
	assert.Equal(t, 5*time.Minute, kafOutboxRetryDelay(9))
}

func TestKafOutboxDispatcher_ClaimsOnlyKafDelegationEvents(t *testing.T) {
	repo, client := newOutboxRepository(t)
	now := time.Now().UTC().Truncate(time.Millisecond)
	_, err := repo.Enqueue(context.Background(), nil, NewOutboxEvent{
		EventID:       "evt-unrelated",
		EventType:     "ticket.updated",
		TenantID:      2,
		AggregateType: "ticket",
		AggregateID:   "TICKET-2",
		Payload:       json.RawMessage(`{"eventId":"evt-unrelated"}`),
		NextAttemptAt: now.Add(-time.Second),
	})
	require.NoError(t, err)
	kafEvent := validKafDelegateRequested()
	kafPayload, err := json.Marshal(kafEvent)
	require.NoError(t, err)
	seedPendingEventWithPayload(t, repo, kafEvent.EventID, now.Add(-time.Second), kafPayload)

	deliveries := 0
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		deliveries++
		w.WriteHeader(http.StatusAccepted)
	}))
	defer server.Close()
	dispatcher, err := NewKafOutboxDispatcher(repo, KafOutboxConfig{
		WebhookURL:    server.URL,
		WebhookSecret: "test-secret",
		BatchSize:     2,
		PollInterval:  time.Second,
	})
	require.NoError(t, err)
	dispatcher.now = func() time.Time { return now }

	require.NoError(t, dispatcher.DispatchOnce(context.Background()))

	unrelated, err := client.OutboxEvent.Query().
		Where(outboxevent.EventIDEQ("evt-unrelated")).
		Only(context.Background())
	require.NoError(t, err)
	publishedKaf, err := client.OutboxEvent.Query().
		Where(outboxevent.EventIDEQ(kafEvent.EventID)).
		Only(context.Background())
	require.NoError(t, err)
	assert.Equal(t, outboxEventStatusPending, unrelated.Status)
	assert.Equal(t, outboxEventStatusPublished, publishedKaf.Status)
	assert.Equal(t, 1, deliveries)
}

func TestKafOutboxDispatcher_RunStopsWhenContextCancelled(t *testing.T) {
	repo, _ := newOutboxRepository(t)
	dispatcher, err := NewKafOutboxDispatcher(repo, KafOutboxConfig{
		WebhookURL:    "https://kaf.example.test/webhooks/itsm",
		WebhookSecret: "test-secret",
		BatchSize:     1,
		PollInterval:  time.Second,
	})
	require.NoError(t, err)

	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})
	go func() {
		dispatcher.Run(ctx)
		close(done)
	}()
	cancel()

	select {
	case <-done:
	case <-time.After(time.Second):
		t.Fatal("dispatcher did not stop after its context was cancelled")
	}
}
