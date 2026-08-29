package service

import (
	"context"
	"encoding/json"
	"fmt"
	"reflect"
	"strings"
	"sync"
	"testing"
	"time"

	"itsm-backend/ent"
	"itsm-backend/ent/enttest"
	"itsm-backend/ent/outboxevent"

	_ "github.com/mattn/go-sqlite3"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

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
	repo, _ := newOutboxRepository(t)
	ctx := context.Background()
	now := time.Now().UTC().Truncate(time.Millisecond)
	seedPendingEvent(t, repo, "evt-concurrent", now.Add(-time.Second))

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
	assert.Equal(t, 1, claimedCount)
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

func newOutboxRepository(t *testing.T) (*OutboxEventRepository, *ent.Client) {
	t.Helper()
	client := enttest.Open(t, "sqlite3", fmt.Sprintf("file:outbox_repository_%d?mode=memory&cache=shared&_fk=1", time.Now().UnixNano()))
	t.Cleanup(func() { _ = client.Close() })
	return NewOutboxEventRepository(client), client
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
