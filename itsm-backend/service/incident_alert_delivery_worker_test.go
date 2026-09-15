package service

import (
	"context"
	"encoding/json"
	"errors"
	"net/smtp"
	"testing"
	"time"

	"itsm-backend/common/executionscope"
	"itsm-backend/config"
	"itsm-backend/database"
	"itsm-backend/ent"
	"itsm-backend/ent/auditlog"
	"itsm-backend/ent/outboxevent"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"go.uber.org/zap/zaptest"
)

type recordingIncidentAlertEmailSender struct {
	failuresRemaining int
	deliveries        []incidentAlertEmailDelivery
}

type blockingIncidentAlertEmailSender struct{}

func (blockingIncidentAlertEmailSender) SendToTarget(ctx context.Context, _ int, _ string, _ EmailTarget, _ *EmailMessage) error {
	<-ctx.Done()
	return newEmailTransportError("smtp", "before_send", emailNotAccepted, ctx.Err())
}

type incidentAlertEmailDelivery struct {
	tenantID int
	message  EmailMessage
}

func testOutboxRegistry(t *testing.T, sender incidentAlertEmailSender) *OutboxEventTypeRegistry {
	t.Helper()
	registry, err := NewOutboxEventTypeRegistry([]OutboxDeliveryHandler{outboxWorkerEmailDomainDouble{sender: sender}}, KafDelegateRequestedEventType)
	require.NoError(t, err)
	return registry
}

// These SQLite tests exercise the shared worker's retry/terminal/recovery
// policy through an explicit domain double. Real Incident source, row locks,
// target binding and receipts are tested in incident_email_target_postgres_test.go.
type outboxWorkerEmailDomainDouble struct{ sender incidentAlertEmailSender }

func (outboxWorkerEmailDomainDouble) EventType() string { return incidentAlertDeliveryEventType }
func (h outboxWorkerEmailDomainDouble) Deliver(ctx context.Context, event *ent.OutboxEvent) error {
	var fixture incidentAlertDeliveryPayload
	if err := json.Unmarshal(event.Payload, &fixture); err != nil {
		return blockOutboxDelivery("invalid worker fixture")
	}
	if fixture.Channel != "email" {
		return blockOutboxDelivery("unsupported incident alert delivery channel")
	}
	message := &EmailMessage{To: fixture.Recipients, Subject: "[ITSM Alert] " + fixture.Subject, BodyText: fixture.Message, DeliveryID: event.EventID, DisableProviderFallback: true}
	var err error
	if transport, ok := h.sender.(*EmailService); ok {
		// A transport-only worker test has no domain source fixture. Target/source
		// validation is covered by the separate real PostgreSQL owner/worker tests.
		err = transport.SendForTenant(ctx, fixture.TenantID, message)
	} else {
		err = h.sender.SendToTarget(ctx, fixture.TenantID, "outbox", fixture.Target, message)
	}
	if err != nil && emailTransportOutcomeOf(err) == emailAcceptanceUnknown {
		return blockOutboxDelivery("delivery_unknown: email transport result is ambiguous")
	}
	return err
}

func (s *recordingIncidentAlertEmailSender) SendToTarget(_ context.Context, tenantID int, _ string, _ EmailTarget, message *EmailMessage) error {
	if s.failuresRemaining > 0 {
		s.failuresRemaining--
		return newEmailTransportError("smtp", "dial", emailNotAccepted, errors.New("temporary email route failure"))
	}
	s.deliveries = append(s.deliveries, incidentAlertEmailDelivery{tenantID: tenantID, message: *message})
	return nil
}

func TestOutboxDeliveryWorkerRetriesThenPublishesExactlyOnce(t *testing.T) {
	repository, client := newOutboxRepository(t)
	ctx := context.Background()
	now := time.Now().UTC().Truncate(time.Millisecond)
	eventID := enqueueIncidentAlertDeliveryForWorker(t, repository, now)
	sender := &recordingIncidentAlertEmailSender{failuresRemaining: 1}
	worker, err := NewOutboxDeliveryWorker(repository, OutboxDeliveryWorkerConfig{
		BatchSize: 10, PollInterval: time.Second, HandlerTimeout: 5 * time.Second, MaxAttempts: 3,
	}, zaptest.NewLogger(t).Sugar(), testOutboxRegistry(t, sender))
	require.NoError(t, err)
	worker.now = func() time.Time { return now }
	repository.clock = worker.now

	require.NoError(t, worker.DispatchOnce(ctx))
	event, err := client.OutboxEvent.Query().Where(outboxevent.EventIDEQ(eventID)).Only(ctx)
	require.NoError(t, err)
	assert.Equal(t, "pending", event.Status)
	assert.Equal(t, 1, event.AttemptCount)
	assert.Contains(t, event.LastError, emailErrorClassSMTPSend)
	assert.Empty(t, sender.deliveries)

	now = event.NextAttemptAt.Add(time.Millisecond)
	require.NoError(t, worker.DispatchOnce(ctx))
	require.NoError(t, worker.DispatchOnce(ctx))
	event, err = client.OutboxEvent.Query().Where(outboxevent.EventIDEQ(eventID)).Only(ctx)
	require.NoError(t, err)
	assert.Equal(t, "published", event.Status)
	require.Len(t, sender.deliveries, 1, "a published event must not be delivered again")
	assert.Equal(t, 7, sender.deliveries[0].tenantID)
	assert.Equal(t, []string{"operator@example.com"}, sender.deliveries[0].message.To)
	assert.Equal(t, "[ITSM Alert] CPU high", sender.deliveries[0].message.Subject)
	assert.Equal(t, eventID, sender.deliveries[0].message.DeliveryID)
	assert.True(t, sender.deliveries[0].message.DisableProviderFallback)
}

func TestOutboxDeliveryWorkerReclaimsExpiredDeliveryAfterRestart(t *testing.T) {
	repository, client := newOutboxRepository(t)
	ctx := context.Background()
	now := time.Now().UTC().Truncate(time.Millisecond)
	eventID := enqueueIncidentAlertDeliveryForWorker(t, repository, now)
	claimed, err := repository.ClaimDueByEventType(ctx, now, 1, incidentAlertDeliveryEventType, false)
	require.NoError(t, err)
	require.Len(t, claimed, 1)

	sender := &recordingIncidentAlertEmailSender{}
	restartedWorker, err := NewOutboxDeliveryWorker(repository, OutboxDeliveryWorkerConfig{
		BatchSize: 10, PollInterval: time.Second, HandlerTimeout: 5 * time.Second, MaxAttempts: 3,
	}, zaptest.NewLogger(t).Sugar(), testOutboxRegistry(t, sender))
	require.NoError(t, err)
	reclaimedAt := now.Add(outboxEventClaimLeaseDuration + time.Second)
	restartedWorker.now = func() time.Time { return reclaimedAt }
	repository.clock = restartedWorker.now

	require.NoError(t, restartedWorker.DispatchOnce(ctx))
	event, err := client.OutboxEvent.Query().Where(outboxevent.EventIDEQ(eventID)).Only(ctx)
	require.NoError(t, err)
	assert.Equal(t, "published", event.Status)
	require.Len(t, sender.deliveries, 1)
}

func TestOutboxDeliveryWorkerBlocksUnknownTypeButLeavesKafReserved(t *testing.T) {
	repository, client := newOutboxRepository(t)
	ctx := context.Background()
	now := time.Now().UTC().Truncate(time.Millisecond)
	for _, item := range []struct{ id, eventType string }{{"evt-unknown", "unregistered"}, {"evt-kaf-reserved", KafDelegateRequestedEventType}} {
		_, err := repository.Enqueue(ctx, nil, NewOutboxEvent{EventID: item.id, EventType: item.eventType, TenantID: 7, AggregateType: "test", AggregateID: "1", Payload: json.RawMessage(`{}`), NextAttemptAt: now})
		require.NoError(t, err)
	}
	sender := &recordingIncidentAlertEmailSender{}
	worker, err := NewOutboxDeliveryWorker(repository, OutboxDeliveryWorkerConfig{BatchSize: 10, PollInterval: time.Second, HandlerTimeout: time.Second, MaxAttempts: 3}, zaptest.NewLogger(t).Sugar(), testOutboxRegistry(t, sender))
	require.NoError(t, err)
	worker.now = func() time.Time { return now }
	repository.clock = worker.now
	require.NoError(t, worker.DispatchOnce(ctx))

	unknown, err := client.OutboxEvent.Query().Where(outboxevent.EventIDEQ("evt-unknown")).Only(ctx)
	require.NoError(t, err)
	assert.Equal(t, outboxEventStatusBlocked, unknown.Status)
	assert.Contains(t, unknown.LastError, "unknown outbox event type")
	kaf, err := client.OutboxEvent.Query().Where(outboxevent.EventIDEQ("evt-kaf-reserved")).Only(ctx)
	require.NoError(t, err)
	assert.Equal(t, outboxEventStatusPending, kaf.Status)
	audited, err := client.AuditLog.Query().Where(auditlog.RequestIDEQ("evt-unknown"), auditlog.ActionEQ("outbox.unknown_event_type")).Exist(ctx)
	require.NoError(t, err)
	assert.True(t, audited)
}

func TestOutboxDeliveryWorkerDoesNotResendAmbiguousExpiredAttempt(t *testing.T) {
	repository, client := newOutboxRepository(t)
	ctx := context.Background()
	now := time.Now().UTC().Truncate(time.Millisecond)
	eventID := enqueueIncidentAlertDeliveryForWorker(t, repository, now)
	claimed, err := repository.ClaimDueByEventType(ctx, now, 1, incidentAlertDeliveryEventType, false)
	require.NoError(t, err)
	require.Len(t, claimed, 1)
	repository.clock = func() time.Time { return now }
	require.NoError(t, repository.MarkDeliveryAttemptStarted(ctx, claimed[0].ID, claimed[0].ClaimToken, eventID))
	// The process crashes after the transport accepted the message and before
	// MarkPublished. The durable attempt marker is the only surviving evidence.
	sender := &recordingIncidentAlertEmailSender{}
	restartedAt := now.Add(outboxEventClaimLeaseDuration + time.Second)
	worker, err := NewOutboxDeliveryWorker(repository, OutboxDeliveryWorkerConfig{BatchSize: 10, PollInterval: time.Second, HandlerTimeout: time.Second, MaxAttempts: 3}, zaptest.NewLogger(t).Sugar(), testOutboxRegistry(t, sender))
	require.NoError(t, err)
	worker.now = func() time.Time { return restartedAt }
	repository.clock = worker.now
	require.NoError(t, worker.DispatchOnce(ctx))

	event, err := client.OutboxEvent.Query().Where(outboxevent.EventIDEQ(eventID)).Only(ctx)
	require.NoError(t, err)
	assert.Equal(t, outboxEventStatusBlocked, event.Status)
	assert.Contains(t, event.LastError, "delivery_unknown")
	assert.Empty(t, sender.deliveries, "an ambiguous external side effect must not be replayed")
	audited, err := client.AuditLog.Query().Where(auditlog.RequestIDEQ(eventID), auditlog.ActionEQ("outbox.delivery_unknown")).Exist(ctx)
	require.NoError(t, err)
	assert.True(t, audited)
}

func TestOutboxDeliveryWorkerRetriesRealEmailServicePreAcceptanceFailure(t *testing.T) {
	repository, client := newOutboxRepository(t)
	ctx := context.Background()
	now := time.Now().UTC().Truncate(time.Millisecond)
	eventID := enqueueIncidentAlertDeliveryForWorker(t, repository, now)
	email := NewEmailService(EmailConfig{Host: "smtp.example.test", Port: 587, Username: "mailer", From: "mailer@example.test"}, zaptest.NewLogger(t).Sugar())
	calls := 0
	email.smtpSend = func(context.Context, string, smtp.Auth, string, []string, []byte) error {
		calls++
		return newEmailTransportError("smtp", "dial", emailNotAccepted, errors.New("dial unavailable"))
	}
	worker, err := NewOutboxDeliveryWorker(repository, OutboxDeliveryWorkerConfig{BatchSize: 10, PollInterval: time.Second, HandlerTimeout: time.Second, MaxAttempts: 3}, zaptest.NewLogger(t).Sugar(), testOutboxRegistry(t, email))
	require.NoError(t, err)
	worker.now = func() time.Time { return now }
	repository.clock = worker.now
	require.NoError(t, worker.DispatchOnce(ctx))
	event, err := client.OutboxEvent.Query().Where(outboxevent.EventIDEQ(eventID)).Only(ctx)
	require.NoError(t, err)
	assert.Equal(t, outboxEventStatusPending, event.Status)
	assert.Equal(t, 1, calls, "durable SMTP must not retry inside EmailService")
}

func TestOutboxDeliveryWorkerAuditsImmediateRealEmailServiceAmbiguity(t *testing.T) {
	repository, client := newOutboxRepository(t)
	ctx := context.Background()
	now := time.Now().UTC().Truncate(time.Millisecond)
	eventID := enqueueIncidentAlertDeliveryForWorker(t, repository, now)
	email := NewEmailService(EmailConfig{Host: "smtp.example.test", Port: 587, Username: "mailer", From: "mailer@example.test"}, zaptest.NewLogger(t).Sugar())
	calls := 0
	email.smtpSend = func(context.Context, string, smtp.Auth, string, []string, []byte) error {
		calls++
		return newEmailTransportError("smtp", "data_close", emailAcceptanceUnknown, errors.New("acceptance response lost"))
	}
	worker, err := NewOutboxDeliveryWorker(repository, OutboxDeliveryWorkerConfig{BatchSize: 10, PollInterval: time.Second, HandlerTimeout: time.Second, MaxAttempts: 3}, zaptest.NewLogger(t).Sugar(), testOutboxRegistry(t, email))
	require.NoError(t, err)
	worker.now = func() time.Time { return now }
	repository.clock = worker.now
	require.NoError(t, worker.DispatchOnce(ctx))
	event, err := client.OutboxEvent.Query().Where(outboxevent.EventIDEQ(eventID)).Only(ctx)
	require.NoError(t, err)
	assert.Equal(t, outboxEventStatusBlocked, event.Status)
	assert.Contains(t, event.LastError, "delivery_unknown")
	assert.Equal(t, 1, calls)
	audited, err := client.AuditLog.Query().Where(auditlog.RequestIDEQ(eventID), auditlog.ActionEQ("outbox.delivery_unknown")).Exist(ctx)
	require.NoError(t, err)
	assert.True(t, audited)
}

func TestOutboxDeliveryWorkerBlocksUnsupportedIncidentAlertChannel(t *testing.T) {
	repository, client := newOutboxRepository(t)
	ctx := context.Background()
	now := time.Now().UTC().Truncate(time.Millisecond)
	payload := validIncidentAlertDeliveryPayload("evt-unsupported-worker")
	payload.Channel = "sms"
	enqueueIncidentAlertPayload(t, repository, now, payload)
	sender := &recordingIncidentAlertEmailSender{}
	worker, err := NewOutboxDeliveryWorker(repository, OutboxDeliveryWorkerConfig{
		BatchSize: 10, PollInterval: time.Second, HandlerTimeout: 5 * time.Second, MaxAttempts: 3,
	}, zaptest.NewLogger(t).Sugar(), testOutboxRegistry(t, sender))
	require.NoError(t, err)
	worker.now = func() time.Time { return now }
	repository.clock = worker.now

	require.NoError(t, worker.DispatchOnce(ctx))
	event, err := client.OutboxEvent.Query().Where(outboxevent.EventIDEQ(payload.EventID)).Only(ctx)
	require.NoError(t, err)
	assert.Equal(t, "blocked", event.Status)
	assert.Equal(t, 1, event.AttemptCount)
	assert.Contains(t, event.LastError, "unsupported incident alert delivery channel")
	assert.Empty(t, sender.deliveries)
}

func TestOutboxDeliveryWorkerMovesExhaustedDeliveryToDeadLetter(t *testing.T) {
	repository, client := newOutboxRepository(t)
	ctx := context.Background()
	now := time.Now().UTC().Truncate(time.Millisecond)
	eventID := enqueueIncidentAlertDeliveryForWorker(t, repository, now)
	sender := &recordingIncidentAlertEmailSender{failuresRemaining: 2}
	worker, err := NewOutboxDeliveryWorker(repository, OutboxDeliveryWorkerConfig{
		BatchSize: 10, PollInterval: time.Second, HandlerTimeout: 5 * time.Second, MaxAttempts: 1,
	}, zaptest.NewLogger(t).Sugar(), testOutboxRegistry(t, sender))
	require.NoError(t, err)
	worker.now = func() time.Time { return now }
	repository.clock = worker.now

	require.NoError(t, worker.DispatchOnce(ctx))
	event, err := client.OutboxEvent.Query().Where(outboxevent.EventIDEQ(eventID)).Only(ctx)
	require.NoError(t, err)
	assert.Equal(t, "dead_letter", event.Status)
	assert.Equal(t, 1, event.AttemptCount)
	assert.Contains(t, event.LastError, emailErrorClassSMTPSend)
}

func TestOutboxDeliveryWorkerBoundsExternalCallWithHandlerTimeout(t *testing.T) {
	repository, client := newOutboxRepository(t)
	ctx := context.Background()
	now := time.Now().UTC().Truncate(time.Millisecond)
	eventID := enqueueIncidentAlertDeliveryForWorker(t, repository, now)
	worker, err := NewOutboxDeliveryWorker(repository, OutboxDeliveryWorkerConfig{
		BatchSize: 10, PollInterval: time.Second, HandlerTimeout: 20 * time.Millisecond, MaxAttempts: 1,
	}, zaptest.NewLogger(t).Sugar(), testOutboxRegistry(t, blockingIncidentAlertEmailSender{}))
	require.NoError(t, err)
	worker.now = func() time.Time { return now }
	repository.clock = worker.now

	started := time.Now()
	require.NoError(t, worker.DispatchOnce(ctx))
	assert.Less(t, time.Since(started), time.Second)
	event, err := client.OutboxEvent.Query().Where(outboxevent.EventIDEQ(eventID)).Only(ctx)
	require.NoError(t, err)
	assert.Equal(t, "dead_letter", event.Status)
	assert.Contains(t, event.LastError, emailErrorClassSMTPSend)
}

func enqueueIncidentAlertDeliveryForWorker(t *testing.T, repository *OutboxEventRepository, now time.Time) string {
	t.Helper()
	payload := validIncidentAlertDeliveryPayload("evt-incident-alert-worker")
	enqueueIncidentAlertPayload(t, repository, now, payload)
	return payload.EventID
}

func enqueueIncidentAlertPayload(t *testing.T, repository *OutboxEventRepository, now time.Time, payload incidentAlertDeliveryPayload) {
	t.Helper()
	encoded, err := json.Marshal(payload)
	require.NoError(t, err)
	_, err = repository.Enqueue(context.Background(), nil, NewOutboxEvent{
		EventID: payload.EventID, EventType: incidentAlertDeliveryEventType, TenantID: payload.TenantID,
		AggregateType: "incident_alert", AggregateID: "42", Payload: encoded, NextAttemptAt: now,
	})
	require.NoError(t, err)
}

func validIncidentAlertDeliveryPayload(eventID string) incidentAlertDeliveryPayload {
	return incidentAlertDeliveryPayload{
		Version: 1, EventID: eventID, TenantID: 7, AlertID: 42, Channel: "email",
		Recipients: []string{"operator@example.com"}, Subject: "CPU high", Message: "CPU exceeded threshold",
		ActorID: 9, Source: "user", CorrelationID: "request-42",
	}
}

func TestOutboxDeliveryWorkerDisabledPreservesEveryQueueState(t *testing.T) {
	for _, mode := range []string{"standard", "candidate"} {
		t.Run(mode, func(t *testing.T) {
			_, client, db := newOutboxRepositoryWithDriver(t, "sqlite3")
			ctx := context.Background()
			for _, state := range []string{"pending", "unknown", "expired"} {
				kind := incidentAlertDeliveryEventType
				if state == "unknown" {
					kind = "unregistered-private-event"
				}
				row := client.OutboxEvent.Create().SetEventID("disabled-" + state).SetEventType(kind).SetTenantID(1).SetAggregateType("incident_alert").SetAggregateID("1").SetPayload(json.RawMessage(`{"private":"original"}`)).SetNextAttemptAt(time.Now().Add(-time.Hour))
				if state == "expired" {
					row.SetStatus("publishing").SetClaimToken("original-claim").SetClaimExpiresAt(time.Now().Add(-time.Minute)).SetLastError("delivery_attempt_started:disabled-expired")
				}
				row.SaveX(ctx)
			}
			snapshot := func(query string) [][]any {
				rows, err := db.QueryContext(ctx, query)
				require.NoError(t, err)
				defer rows.Close()
				columns, err := rows.Columns()
				require.NoError(t, err)
				var result [][]any
				for rows.Next() {
					values := make([]any, len(columns))
					pointers := make([]any, len(columns))
					for i := range values {
						pointers[i] = &values[i]
					}
					require.NoError(t, rows.Scan(pointers...))
					for i, value := range values {
						if bytes, ok := value.([]byte); ok {
							values[i] = append([]byte(nil), bytes...)
						}
					}
					result = append(result, values)
				}
				require.NoError(t, rows.Err())
				return result
			}
			before, audits := snapshot("SELECT * FROM outbox_events ORDER BY id"), snapshot("SELECT * FROM audit_logs ORDER BY id")
			cfg := config.ExecutionConfig{Mode: mode, DeploymentID: "disabled-worker"}
			if mode == "candidate" {
				cfg.Scopes = []config.ExecutionScopeConfig{{TenantID: 1, ScopeID: "149ff1af-a27c-47c7-827f-103271130bb9"}}
			}
			policy, err := database.NewExecutionPolicy(cfg)
			require.NoError(t, err)
			sender := &recordingIncidentAlertEmailSender{}
			worker, err := NewOutboxDeliveryWorker(NewOutboxEventRepository(client, policy), OutboxDeliveryWorkerConfig{BatchSize: 10, PollInterval: time.Second, HandlerTimeout: time.Second, MaxAttempts: 3}, zaptest.NewLogger(t).Sugar(), testOutboxRegistry(t, sender))
			require.NoError(t, err)
			require.ErrorIs(t, worker.DispatchOnce(ctx), executionscope.ErrDenied)
			//lint:ignore SA1012 Deliberately verifies nil-context rejection.
			require.ErrorIs(t, worker.DispatchOnce(nil), executionscope.ErrDenied)
			cancelled, cancel := context.WithCancel(ctx)
			cancel()
			require.ErrorIs(t, worker.DispatchOnce(cancelled), context.Canceled)
			require.Equal(t, before, snapshot("SELECT * FROM outbox_events ORDER BY id"))
			require.Equal(t, audits, snapshot("SELECT * FROM audit_logs ORDER BY id"))
			require.Empty(t, sender.deliveries)
		})
	}
}
