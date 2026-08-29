package service

import (
	"context"
	"encoding/json"
	"fmt"
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
	assert.Empty(t, second)

	nextAttemptAt := now.Add(time.Minute)
	require.NoError(t, repo.MarkRetry(ctx, first[0].ID, "timeout", nextAttemptAt))
	assertEventState(t, client, "evt-2", outboxEventStatusPending, 1, nextAttemptAt)
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
	require.NoError(t, repo.MarkPublished(ctx, claimed[0].ID, publishedAt))
	event, err := client.OutboxEvent.Get(ctx, claimed[0].ID)
	require.NoError(t, err)
	assert.Equal(t, outboxEventStatusPublished, event.Status)
	assert.False(t, event.PublishedAt.IsZero())
	assert.WithinDuration(t, publishedAt, event.PublishedAt, time.Millisecond)
}

func newOutboxRepository(t *testing.T) (*OutboxEventRepository, *ent.Client) {
	t.Helper()
	client := enttest.Open(t, "sqlite3", fmt.Sprintf("file:outbox_repository_%d?mode=memory&cache=shared&_fk=1", time.Now().UnixNano()))
	t.Cleanup(func() { _ = client.Close() })
	return NewOutboxEventRepository(client), client
}

func seedPendingEvent(t *testing.T, repo *OutboxEventRepository, eventID string, nextAttemptAt time.Time) {
	t.Helper()
	_, err := repo.Enqueue(context.Background(), nil, NewOutboxEvent{
		EventID:       eventID,
		EventType:     "kaf_delegate_requested",
		TenantID:      1,
		AggregateType: "process_task",
		AggregateID:   "TASK-" + eventID,
		Payload:       json.RawMessage(fmt.Sprintf(`{"eventId":%q}`, eventID)),
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
