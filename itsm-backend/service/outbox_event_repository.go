package service

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	"itsm-backend/ent"
	"itsm-backend/ent/outboxevent"
)

const (
	outboxEventStatusPending    = "pending"
	outboxEventStatusPublishing = "publishing"
	outboxEventStatusPublished  = "published"
)

var (
	// ErrDuplicateOutboxEvent reports an idempotent enqueue attempt for an
	// event ID already persisted by the domain transaction.
	ErrDuplicateOutboxEvent = errors.New("duplicate outbox event")

	errOutboxEventNotPublishing = errors.New("outbox event is not publishing")
)

// NewOutboxEvent contains the immutable delivery data captured with the
// owning domain transaction.
type NewOutboxEvent struct {
	EventID       string
	EventType     string
	TenantID      int
	AggregateType string
	AggregateID   string
	Payload       json.RawMessage
	NextAttemptAt time.Time
}

// OutboxEventRepository persists and atomically claims reliable events.
type OutboxEventRepository struct {
	client *ent.Client
}

func NewOutboxEventRepository(client *ent.Client) *OutboxEventRepository {
	return &OutboxEventRepository{client: client}
}

// Enqueue writes an event through the caller's transaction when supplied, so
// a domain change cannot commit without its matching delivery record.
func (r *OutboxEventRepository) Enqueue(ctx context.Context, tx *ent.Tx, event NewOutboxEvent) (*ent.OutboxEvent, error) {
	creator := r.client.OutboxEvent.Create()
	if tx != nil {
		creator = tx.OutboxEvent.Create()
	}

	creator.SetEventID(event.EventID).
		SetEventType(event.EventType).
		SetTenantID(event.TenantID).
		SetAggregateType(event.AggregateType).
		SetAggregateID(event.AggregateID).
		SetPayload(event.Payload)
	if !event.NextAttemptAt.IsZero() {
		creator.SetNextAttemptAt(event.NextAttemptAt)
	}

	persisted, err := creator.Save(ctx)
	if err != nil {
		if ent.IsConstraintError(err) {
			return nil, fmt.Errorf("%w: %v", ErrDuplicateOutboxEvent, err)
		}
		return nil, err
	}
	return persisted, nil
}

// ClaimDue moves eligible pending events to publishing within one transaction.
// Every candidate is conditionally updated again, so a competing dispatcher
// that claimed it first is never returned to this caller.
func (r *OutboxEventRepository) ClaimDue(ctx context.Context, now time.Time, limit int) ([]*ent.OutboxEvent, error) {
	if limit <= 0 {
		return nil, nil
	}

	tx, err := r.client.Tx(ctx)
	if err != nil {
		return nil, fmt.Errorf("start outbox claim transaction: %w", err)
	}
	defer func() { _ = tx.Rollback() }()

	candidates, err := tx.OutboxEvent.Query().
		Where(
			outboxevent.StatusEQ(outboxEventStatusPending),
			outboxevent.NextAttemptAtLTE(now),
		).
		Order(ent.Asc(outboxevent.FieldNextAttemptAt)).
		Limit(limit).
		All(ctx)
	if err != nil {
		return nil, fmt.Errorf("query due outbox events: %w", err)
	}

	claimedIDs := make([]int, 0, len(candidates))
	for _, candidate := range candidates {
		updated, err := tx.OutboxEvent.Update().
			Where(
				outboxevent.IDEQ(candidate.ID),
				outboxevent.StatusEQ(outboxEventStatusPending),
				outboxevent.NextAttemptAtLTE(now),
			).
			SetStatus(outboxEventStatusPublishing).
			Save(ctx)
		if err != nil {
			return nil, fmt.Errorf("claim outbox event %d: %w", candidate.ID, err)
		}
		if updated == 1 {
			claimedIDs = append(claimedIDs, candidate.ID)
		}
	}

	if err := tx.Commit(); err != nil {
		return nil, fmt.Errorf("commit outbox claims: %w", err)
	}
	if len(claimedIDs) == 0 {
		return nil, nil
	}

	claimed, err := r.client.OutboxEvent.Query().
		Where(outboxevent.IDIn(claimedIDs...)).
		All(ctx)
	if err != nil {
		return nil, fmt.Errorf("load claimed outbox events: %w", err)
	}
	byID := make(map[int]*ent.OutboxEvent, len(claimed))
	for _, event := range claimed {
		byID[event.ID] = event
	}

	ordered := make([]*ent.OutboxEvent, 0, len(claimedIDs))
	for _, id := range claimedIDs {
		if event, ok := byID[id]; ok {
			ordered = append(ordered, event)
		}
	}
	return ordered, nil
}

// MarkRetry makes a failed claim eligible for a later delivery attempt.
func (r *OutboxEventRepository) MarkRetry(ctx context.Context, eventID int, lastError string, nextAttemptAt time.Time) error {
	updated, err := r.client.OutboxEvent.Update().
		Where(
			outboxevent.IDEQ(eventID),
			outboxevent.StatusEQ(outboxEventStatusPublishing),
		).
		SetStatus(outboxEventStatusPending).
		AddAttemptCount(1).
		SetLastError(lastError).
		SetNextAttemptAt(nextAttemptAt).
		Save(ctx)
	if err != nil {
		return fmt.Errorf("schedule outbox retry: %w", err)
	}
	if updated == 0 {
		return errOutboxEventNotPublishing
	}
	return nil
}

// MarkPublished finalizes a successfully delivered publishing lease.
func (r *OutboxEventRepository) MarkPublished(ctx context.Context, eventID int, publishedAt time.Time) error {
	updated, err := r.client.OutboxEvent.Update().
		Where(
			outboxevent.IDEQ(eventID),
			outboxevent.StatusEQ(outboxEventStatusPublishing),
		).
		SetStatus(outboxEventStatusPublished).
		SetPublishedAt(publishedAt).
		ClearLastError().
		Save(ctx)
	if err != nil {
		return fmt.Errorf("mark outbox event published: %w", err)
	}
	if updated == 0 {
		return errOutboxEventNotPublishing
	}
	return nil
}
