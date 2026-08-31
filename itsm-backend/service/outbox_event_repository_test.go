package service

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"reflect"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"itsm-backend/ent"
	"itsm-backend/ent/enttest"
	"itsm-backend/ent/outboxevent"

	"entgo.io/ent/dialect"
	entsql "entgo.io/ent/dialect/sql"
	"github.com/mattn/go-sqlite3"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

var outboxSQLiteDriverID atomic.Uint64

func TestNewWorkflowStartEventIDIsDeterministic(t *testing.T) {
	assert.Equal(t, "workflow-start:501:22", NewWorkflowStartEventID(501, 22))
	assert.Equal(t, NewWorkflowStartEventID(501, 22), NewWorkflowStartEventID(501, 22))
	assert.NotEqual(t, NewWorkflowStartEventID(501, 22), NewWorkflowStartEventID(502, 22))
}

func TestOutboxEventRepository_EnqueueDeduplicatesEventID(t *testing.T) {
	repo, client := newOutboxRepository(t)
	ctx := context.Background()

	tx, err := client.Tx(ctx)
	require.NoError(t, err)

	first, err := repo.Enqueue(ctx, tx, NewOutboxEvent{
		EventID:       "evt-1",
		EventType:     "kaf_delegate_requested",
		TenantID:      1,
		AggregateType: "process_task",
		AggregateID:   "TASK-1",
		Payload:       json.RawMessage(`{"eventId":"evt-1"}`),
	})
	require.NoError(t, err)
	require.NoError(t, tx.Commit())
	assert.Equal(t, outboxEventStatusPending, first.Status)

	_, err = repo.Enqueue(ctx, nil, NewOutboxEvent{
		EventID:       "evt-1",
		EventType:     "kaf_delegate_requested",
		TenantID:      1,
		AggregateType: "process_task",
		AggregateID:   "TASK-1",
		Payload:       json.RawMessage(`{"eventId":"evt-1"}`),
	})
	require.ErrorIs(t, err, ErrDuplicateOutboxEvent)
}

func TestOutboxEventRepository_EnqueueRollsBackWithCallerTransaction(t *testing.T) {
	repo, client := newOutboxRepository(t)
	ctx := context.Background()

	tx, err := client.Tx(ctx)
	require.NoError(t, err)
	_, err = repo.Enqueue(ctx, tx, NewOutboxEvent{
		EventID:       "evt-rollback",
		EventType:     "kaf_delegate_requested",
		TenantID:      1,
		AggregateType: "process_task",
		AggregateID:   "TASK-ROLLBACK",
		Payload:       json.RawMessage(`{"eventId":"evt-rollback"}`),
	})
	require.NoError(t, err)
	require.NoError(t, tx.Rollback())

	exists, err := client.OutboxEvent.Query().
		Where(outboxevent.EventIDEQ("evt-rollback")).
		Exist(ctx)
	require.NoError(t, err)
	assert.False(t, exists)
}

func TestOutboxEventRepository_TenantOwnershipIsImmutableAndScoped(t *testing.T) {
	repo, client := newOutboxRepository(t)
	ctx := context.Background()
	seedPendingEvent(t, repo, "evt-tenant", time.Now().UTC())

	_, canUpdateTenant := reflect.TypeOf(client.OutboxEvent.Update()).MethodByName("SetTenantID")
	assert.False(t, canUpdateTenant, "generated updates must not move events between tenants")

	exists, err := client.OutboxEvent.Query().
		Where(
			outboxevent.EventIDEQ("evt-tenant"),
			outboxevent.TenantIDEQ(2),
		).
		Exist(ctx)
	require.NoError(t, err)
	assert.False(t, exists, "a tenant-scoped read must fail closed for another tenant")
}

func TestOutboxEventRepository_ClaimDueOnlyClaimsOnceAndSchedulesRetry(t *testing.T) {
	repo, client := newOutboxRepository(t)
	ctx := context.Background()
	now := time.Now().UTC().Truncate(time.Millisecond)
	seedPendingEvent(t, repo, "evt-2", now.Add(-time.Second))

	first, err := repo.ClaimDue(ctx, now, 10)
	require.NoError(t, err)
	second, err := repo.ClaimDue(ctx, now, 10)
	require.NoError(t, err)
	require.Len(t, first, 1)
	assert.Equal(t, outboxEventStatusPublishing, first[0].Status)
	require.NotEmpty(t, first[0].ClaimToken)
	assert.WithinDuration(t, now.Add(outboxEventClaimLeaseDuration), first[0].ClaimExpiresAt, time.Millisecond)
	assert.Empty(t, second)

	nextAttemptAt := now.Add(time.Minute)
	require.NoError(t, repo.MarkRetry(ctx, first[0].ID, first[0].ClaimToken, "timeout", nextAttemptAt))
	assertEventState(t, client, "evt-2", outboxEventStatusPending, 1, nextAttemptAt)
}

func TestOutboxEventRepository_ClaimDueAllowsOnlyOneConcurrentClaimer(t *testing.T) {
	tracker := newSQLiteOutboxUpdateTracker()
	repo, client, db := newOutboxRepositoryWithSQLiteDriver(t, tracker)
	ctx := context.Background()
	now := time.Now().UTC().Truncate(time.Millisecond)
	seedPendingEvent(t, repo, "evt-concurrent", now.Add(-time.Second))

	// Keep SQLite's write lock through four prepared updates. The first two
	// belong to the concurrent callers; reaching four requires at least one
	// retry after the lock error, without serializing through a one-connection
	// pool.
	lockConn, err := db.Conn(ctx)
	require.NoError(t, err)
	t.Cleanup(func() { _ = lockConn.Close() })
	_, err = lockConn.ExecContext(ctx, "BEGIN IMMEDIATE")
	require.NoError(t, err)
	lockHeld := true
	t.Cleanup(func() {
		if lockHeld {
			_, _ = lockConn.ExecContext(ctx, "ROLLBACK")
		}
	})
	tracker.enabled.Store(true)

	start := make(chan struct{})
	results := make(chan []*ent.OutboxEvent, 2)
	errs := make(chan error, 2)
	var wait sync.WaitGroup
	for range 2 {
		wait.Add(1)
		go func() {
			defer wait.Done()
			<-start
			claimed, err := repo.ClaimDue(ctx, now, 1)
			results <- claimed
			errs <- err
		}()
	}
	close(start)
	waitForSQLiteOutboxUpdateAttempts(t, tracker, 4)
	require.NoError(t, func() error {
		_, err := lockConn.ExecContext(ctx, "COMMIT")
		return err
	}())
	lockHeld = false
	wait.Wait()
	close(results)
	close(errs)

	for err := range errs {
		require.NoError(t, err)
	}
	claimedCount := 0
	for claimed := range results {
		claimedCount += len(claimed)
	}
	event, err := client.OutboxEvent.Query().
		Where(outboxevent.EventIDEQ("evt-concurrent")).
		Only(ctx)
	require.NoError(t, err)
	assert.Equalf(t, 1, claimedCount, "final status=%q claim token present=%t", event.Status, event.ClaimToken != "")
	assert.Equal(t, outboxEventStatusPublishing, event.Status)
	assert.NotEmpty(t, event.ClaimToken)
	assert.GreaterOrEqual(t, tracker.attempts.Load(), int32(7), "the callers must retry after the held write lock before exactly one claims")
}

func TestOutboxEventRepository_ClaimDueRecoversExpiredLease(t *testing.T) {
	repo, client := newOutboxRepository(t)
	ctx := context.Background()
	now := time.Now().UTC().Truncate(time.Millisecond)
	seedPendingEvent(t, repo, "evt-expired", now.Add(-time.Second))

	first, err := repo.ClaimDue(ctx, now, 1)
	require.NoError(t, err)
	require.Len(t, first, 1)

	recoveredAt := now.Add(outboxEventClaimLeaseDuration + time.Second)
	second, err := repo.ClaimDue(ctx, recoveredAt, 1)
	require.NoError(t, err)
	require.Len(t, second, 1)
	assert.NotEqual(t, first[0].ClaimToken, second[0].ClaimToken)
	assert.WithinDuration(t, recoveredAt.Add(outboxEventClaimLeaseDuration), second[0].ClaimExpiresAt, time.Millisecond)

	event, err := client.OutboxEvent.Get(ctx, second[0].ID)
	require.NoError(t, err)
	assert.Equal(t, outboxEventStatusPublishing, event.Status)
}

func TestOutboxEventRepository_RejectsStaleLeaseCompletion(t *testing.T) {
	repo, client := newOutboxRepository(t)
	ctx := context.Background()
	now := time.Now().UTC().Truncate(time.Millisecond)
	seedPendingEvent(t, repo, "evt-stale", now.Add(-time.Second))

	first, err := repo.ClaimDue(ctx, now, 1)
	require.NoError(t, err)
	require.Len(t, first, 1)

	recoveredAt := now.Add(outboxEventClaimLeaseDuration + time.Second)
	second, err := repo.ClaimDue(ctx, recoveredAt, 1)
	require.NoError(t, err)
	require.Len(t, second, 1)

	repo.clock = func() time.Time { return recoveredAt }
	retryErr := repo.MarkRetry(ctx, first[0].ID, first[0].ClaimToken, "stale retry", recoveredAt.Add(time.Minute))
	publishErr := repo.MarkPublished(ctx, first[0].ID, first[0].ClaimToken, recoveredAt)
	require.ErrorIs(t, retryErr, ErrOutboxEventClaimLost)
	require.ErrorIs(t, publishErr, ErrOutboxEventClaimLost)

	event, err := client.OutboxEvent.Get(ctx, second[0].ID)
	require.NoError(t, err)
	assert.Equal(t, outboxEventStatusPublishing, event.Status)
	assert.Equal(t, second[0].ClaimToken, event.ClaimToken)
}

func TestOutboxEventRepository_MarkPublishedFinalizesClaimedEvent(t *testing.T) {
	repo, client := newOutboxRepository(t)
	ctx := context.Background()
	now := time.Now().UTC().Truncate(time.Millisecond)
	seedPendingEvent(t, repo, "evt-3", now.Add(-time.Second))

	claimed, err := repo.ClaimDue(ctx, now, 1)
	require.NoError(t, err)
	require.Len(t, claimed, 1)

	publishedAt := now.Add(time.Second)
	repo.clock = func() time.Time { return publishedAt }
	require.NoError(t, repo.MarkPublished(ctx, claimed[0].ID, claimed[0].ClaimToken, publishedAt))
	event, err := client.OutboxEvent.Get(ctx, claimed[0].ID)
	require.NoError(t, err)
	assert.Equal(t, outboxEventStatusPublished, event.Status)
	assert.False(t, event.PublishedAt.IsZero())
	assert.WithinDuration(t, publishedAt, event.PublishedAt, time.Millisecond)
}

func TestOutboxEventRepository_MarkDeadAndRetryWorkflowStartWithAudit(t *testing.T) {
	repo, client := newOutboxRepository(t)
	ctx := context.Background()
	now := time.Now().UTC().Truncate(time.Millisecond)
	_, err := repo.Enqueue(ctx, nil, NewOutboxEvent{
		EventID: NewWorkflowStartEventID(501, 22), EventType: workflowStartRequestedEventType,
		TenantID: 7, AggregateType: "work_item", AggregateID: "501", Payload: json.RawMessage(`{"workItemId":501}`),
		NextAttemptAt: now.Add(-time.Second),
	})
	require.NoError(t, err)
	claimed, err := repo.ClaimDueByEventType(ctx, now, 1, workflowStartRequestedEventType)
	require.NoError(t, err)
	require.Len(t, claimed, 1)
	repo.clock = func() time.Time { return now }
	require.NoError(t, repo.MarkDead(ctx, claimed[0].ID, claimed[0].ClaimToken, "password=workflow-secret", OutboxRetryAudit{
		TenantID: 7, RequestID: claimed[0].EventID, Resource: "work_item:501",
		Action: "intake.workflow_start.manual_intervention_required", Path: "workflow/start", Method: "DISPATCH", StatusCode: 500,
	}))

	dead, err := client.OutboxEvent.Get(ctx, claimed[0].ID)
	require.NoError(t, err)
	require.Equal(t, outboxEventStatusDead, dead.Status)
	require.Equal(t, 1, dead.AttemptCount)
	require.NotContains(t, dead.LastError, "workflow-secret")
	require.ErrorIs(t, repo.RetryDeadWorkflowStart(ctx, 8, 501), ErrWorkflowStartNotDead)

	retryCtx := context.WithValue(context.WithValue(ctx, "user_id", 31), "request_id", "manual-retry-1")
	require.NoError(t, repo.RetryDeadWorkflowStart(retryCtx, 7, 501))
	retried, err := client.OutboxEvent.Get(ctx, claimed[0].ID)
	require.NoError(t, err)
	require.Equal(t, outboxEventStatusPending, retried.Status)
	require.Zero(t, retried.AttemptCount)
	require.Empty(t, retried.LastError)
	require.Equal(t, claimed[0].EventID, retried.EventID)
	audits, err := client.AuditLog.Query().All(ctx)
	require.NoError(t, err)
	require.Len(t, audits, 2)
	require.Equal(t, "intake.workflow_start.retry_requested", audits[1].Action)
	require.Equal(t, 31, audits[1].UserID)
}

func TestOutboxEventRepository_RedactsSensitiveEntityFieldsAndSanitizesRetryError(t *testing.T) {
	repo, client := newOutboxRepository(t)
	ctx := context.Background()
	now := time.Now().UTC().Truncate(time.Millisecond)
	secretPayload := "payload-secret"
	secretError := "Authorization: Bearer error-secret\n" + strings.Repeat("x", outboxEventLastErrorMaxLength)
	seedPendingEventWithPayload(t, repo, "evt-sensitive", now.Add(-time.Second), json.RawMessage(fmt.Sprintf(`{"token":%q}`, secretPayload)))

	claimed, err := repo.ClaimDue(ctx, now, 1)
	require.NoError(t, err)
	require.Len(t, claimed, 1)
	repo.clock = func() time.Time { return now }
	require.NoError(t, repo.MarkRetry(ctx, claimed[0].ID, claimed[0].ClaimToken, secretError, now.Add(time.Minute)))

	event, err := client.OutboxEvent.Get(ctx, claimed[0].ID)
	require.NoError(t, err)
	assert.NotContains(t, event.String(), secretPayload)
	assert.NotContains(t, event.String(), "error-secret")
	assert.NotContains(t, event.LastError, "error-secret")
	assert.NotContains(t, event.LastError, "\n")
	assert.LessOrEqual(t, len(event.LastError), outboxEventLastErrorMaxLength)
}

func TestSummarizeOutboxError_RedactsCommonCredentialSpellings(t *testing.T) {
	tests := []struct {
		name  string
		input string
		want  string
	}{
		{
			name:  "access token with underscore",
			input: "delivery rejected: access_token=live-access-token",
			want:  "delivery rejected: access_token=[redacted]",
		},
		{
			name:  "access token with hyphen and mixed case",
			input: "delivery rejected: ACCESS-TOKEN: mixed-case-access-token",
			want:  "delivery rejected: ACCESS-TOKEN=[redacted]",
		},
		{
			name:  "client secret with underscore",
			input: "delivery rejected: client_secret=client-secret-value",
			want:  "delivery rejected: client_secret=[redacted]",
		},
		{
			name:  "client secret with hyphen and mixed case",
			input: "delivery rejected: Client-Secret: mixed-case-client-secret",
			want:  "delivery rejected: Client-Secret=[redacted]",
		},
		{
			name:  "URL userinfo",
			input: "delivery rejected: POST https://webhook-user:webhook-password@api.example.test/events",
			want:  "delivery rejected: POST https://[redacted]@api.example.test/events",
		},
		{
			name:  "URL userinfo with empty username",
			input: "delivery rejected: POST https://:webhook-password@api.example.test/events",
			want:  "delivery rejected: POST https://[redacted]@api.example.test/events",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			assert.Equal(t, tt.want, summarizeOutboxError(tt.input))
		})
	}
}

func TestSummarizeOutboxError_RedactsQuotedJSONCredentialValues(t *testing.T) {
	tests := []struct {
		name       string
		input      string
		secret     string
		diagnostic string
	}{
		{
			name:       "JSON token",
			input:      `delivery rejected: {"detail":"invalid signature","token":"json-token-secret"}`,
			secret:     "json-token-secret",
			diagnostic: "invalid signature",
		},
		{
			name:       "JSON access token with underscore",
			input:      `delivery rejected: {"access_token":"json-access-token-secret"}`,
			secret:     "json-access-token-secret",
			diagnostic: "access_token",
		},
		{
			name:       "JSON client secret with hyphen",
			input:      `delivery rejected: {"client-secret":"json-client-secret"}`,
			secret:     "json-client-secret",
			diagnostic: "client-secret",
		},
		{
			name:       "escaped JSON API key",
			input:      `delivery rejected: {\"api-key\":\"escaped-api-key-secret\"}`,
			secret:     "escaped-api-key-secret",
			diagnostic: "api-key",
		},
		{
			name:       "escaped JSON password",
			input:      `delivery rejected: {\"password\":\"escaped-password-secret\"}`,
			secret:     "escaped-password-secret",
			diagnostic: "password",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			summary := summarizeOutboxError(tt.input)
			assert.NotContains(t, summary, tt.secret)
			assert.Contains(t, summary, tt.diagnostic)
		})
	}
}

func newOutboxRepository(t *testing.T) (*OutboxEventRepository, *ent.Client) {
	t.Helper()
	repo, client, _ := newOutboxRepositoryWithDriver(t, "sqlite3")
	return repo, client
}

func newOutboxRepositoryWithSQLiteDriver(t *testing.T, tracker *sqliteOutboxUpdateTracker) (*OutboxEventRepository, *ent.Client, *sql.DB) {
	t.Helper()
	driverName := fmt.Sprintf("sqlite3_outbox_repository_%d", outboxSQLiteDriverID.Add(1))
	sql.Register(driverName, &sqlite3.SQLiteDriver{
		ConnectHook: func(conn *sqlite3.SQLiteConn) error {
			if tracker == nil {
				return nil
			}
			connectionID := tracker.connectionIDs.Add(1)
			conn.RegisterAuthorizer(func(actionCode int, arg1, _, _ string) int {
				if tracker.enabled.Load() && actionCode == sqlite3.SQLITE_UPDATE && arg1 == "outbox_events" {
					tracker.attempts.Add(1)
					select {
					case tracker.updateAttempts <- connectionID:
					default:
					}
				}
				return sqlite3.SQLITE_OK
			})
			return nil
		},
	})
	return newOutboxRepositoryWithDriver(t, driverName)
}

func newOutboxRepositoryWithDriver(t *testing.T, driverName string) (*OutboxEventRepository, *ent.Client, *sql.DB) {
	t.Helper()
	db, err := sql.Open(driverName, fmt.Sprintf("file:outbox_repository_%d?mode=memory&cache=shared&_fk=1&_busy_timeout=1", time.Now().UnixNano()))
	require.NoError(t, err)
	db.SetMaxOpenConns(4)
	db.SetMaxIdleConns(4)
	client := enttest.NewClient(t, enttest.WithOptions(ent.Driver(entsql.OpenDB(dialect.SQLite, db))))
	t.Cleanup(func() { _ = client.Close() })
	return NewOutboxEventRepository(client), client, db
}

type sqliteOutboxUpdateTracker struct {
	connectionIDs  atomic.Uint64
	enabled        atomic.Bool
	attempts       atomic.Int32
	updateAttempts chan uint64
}

func newSQLiteOutboxUpdateTracker() *sqliteOutboxUpdateTracker {
	return &sqliteOutboxUpdateTracker{updateAttempts: make(chan uint64, 16)}
}

func waitForSQLiteOutboxUpdateAttempts(t *testing.T, tracker *sqliteOutboxUpdateTracker, want int) {
	t.Helper()
	timeout := time.NewTimer(time.Second)
	defer timeout.Stop()
	observed := 0
	connections := make(map[uint64]struct{}, 2)
	for observed < want || len(connections) < 2 {
		select {
		case connectionID := <-tracker.updateAttempts:
			observed++
			connections[connectionID] = struct{}{}
		case <-timeout.C:
			t.Fatalf("timed out waiting for %d SQLite outbox update attempts across two connections; observed %d attempts on %d connections", want, tracker.attempts.Load(), len(connections))
		}
	}
}

func seedPendingEvent(t *testing.T, repo *OutboxEventRepository, eventID string, nextAttemptAt time.Time) {
	t.Helper()
	seedPendingEventWithPayload(t, repo, eventID, nextAttemptAt, json.RawMessage(fmt.Sprintf(`{"eventId":%q}`, eventID)))
}

func seedPendingEventWithPayload(t *testing.T, repo *OutboxEventRepository, eventID string, nextAttemptAt time.Time, payload json.RawMessage) {
	t.Helper()
	_, err := repo.Enqueue(context.Background(), nil, NewOutboxEvent{
		EventID:       eventID,
		EventType:     "kaf_delegate_requested",
		TenantID:      1,
		AggregateType: "process_task",
		AggregateID:   "TASK-" + eventID,
		Payload:       payload,
		NextAttemptAt: nextAttemptAt,
	})
	require.NoError(t, err)
}

func assertEventState(t *testing.T, client *ent.Client, eventID, wantStatus string, wantAttempts int, wantNextAttemptAt time.Time) {
	t.Helper()
	event, err := client.OutboxEvent.Query().
		Where(outboxevent.EventIDEQ(eventID)).
		First(context.Background())
	require.NoError(t, err)
	assert.Equal(t, eventID, event.EventID)
	assert.Equal(t, wantStatus, event.Status)
	assert.Equal(t, wantAttempts, event.AttemptCount)
	assert.WithinDuration(t, wantNextAttemptAt, event.NextAttemptAt, time.Millisecond)
}
