package service

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"regexp"
	"strings"
	"time"

	"itsm-backend/ent"
	"itsm-backend/ent/outboxevent"

	"github.com/google/uuid"
)

const (
	outboxEventStatusPending    = "pending"
	outboxEventStatusPublishing = "publishing"
	outboxEventStatusPublished  = "published"
	outboxEventStatusBlocked    = "blocked"
	outboxEventStatusDeadLetter = "dead_letter"

	outboxEventClaimLeaseDuration = 5 * time.Minute
	outboxEventLastErrorMaxLength = 512
	outboxEventClaimRetryAttempts = 50
	outboxEventClaimRetryDelay    = 10 * time.Millisecond
	outboxEventClaimRetryMaxDelay = 100 * time.Millisecond
	outboxDeliveryAttemptPrefix   = "delivery_attempt_started:"
	outboxDeliveryUnknownError    = "delivery_unknown: external side effect may have completed; manual reconciliation required"
)

var (
	// ErrDuplicateOutboxEvent reports an idempotent enqueue attempt for an
	// event ID already persisted by the domain transaction.
	ErrDuplicateOutboxEvent = errors.New("duplicate outbox event")

	// ErrOutboxEventClaimLost reports a completion attempt for an expired or
	// superseded publishing lease.
	ErrOutboxEventClaimLost = errors.New("outbox event claim is no longer active")

	outboxSensitiveErrorJSONValuePattern   = regexp.MustCompile(`(?i)("(?:authorization|access[_ -]?token|client[_ -]?secret|token|secret|password|api[_ -]?key)"\s*:\s*")(?:\\.|[^"\\])*(")`)
	outboxSensitiveErrorValuePattern       = regexp.MustCompile(`(?i)(^|[^[:alnum:]_])(authorization|access[_ -]?token|client[_ -]?secret|token|secret|password|api[_ -]?key)\s*[:=]\s*(?:bearer\s+)?[^\s,;]+`)
	outboxSensitiveErrorURLUserinfoPattern = regexp.MustCompile(`(?i)([a-z][a-z0-9+.-]*://)[^/\s:@]*:[^/\s@]+@`)
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

// OutboxRetryAudit is the payload-free audit evidence recorded with a failed
// delivery retry transition.
type OutboxRetryAudit struct {
	TenantID   int
	RequestID  string
	Resource   string
	Action     string
	Path       string
	Method     string
	StatusCode int
}

// OutboxEventRepository persists and atomically claims reliable events.
type OutboxEventRepository struct {
	client *ent.Client
	clock  func() time.Time
}

func NewOutboxEventRepository(client *ent.Client) *OutboxEventRepository {
	return &OutboxEventRepository{client: client, clock: time.Now}
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

// ClaimDue first returns expired publishing leases to pending, then moves
// eligible events to publishing within the same transaction. Every candidate
// is conditionally updated again, so a competing dispatcher that claimed it
// first is never returned to this caller.
func (r *OutboxEventRepository) ClaimDue(ctx context.Context, now time.Time, limit int) ([]*ent.OutboxEvent, error) {
	return r.ClaimDueByEventType(ctx, now, limit, "")
}

// ClaimDueByEventType applies the same lease protocol as ClaimDue while
// preventing one delivery integration from claiming another event type.
func (r *OutboxEventRepository) ClaimDueByEventType(ctx context.Context, now time.Time, limit int, eventType string) ([]*ent.OutboxEvent, error) {
	for attempt := 0; attempt < outboxEventClaimRetryAttempts; attempt++ {
		claimed, err := r.claimDue(ctx, now, limit, eventType)
		if err == nil || !isRetryableOutboxClaimError(err) || attempt == outboxEventClaimRetryAttempts-1 {
			return claimed, err
		}

		delay := time.Duration(attempt+1) * outboxEventClaimRetryDelay
		if delay > outboxEventClaimRetryMaxDelay {
			delay = outboxEventClaimRetryMaxDelay
		}
		timer := time.NewTimer(delay)
		select {
		case <-ctx.Done():
			timer.Stop()
			return nil, ctx.Err()
		case <-timer.C:
		}
	}
	return nil, nil
}

func (r *OutboxEventRepository) claimDue(ctx context.Context, now time.Time, limit int, eventType string) ([]*ent.OutboxEvent, error) {
	if limit <= 0 {
		return nil, nil
	}
	now = now.UTC()
	claimExpiresAt := now.Add(outboxEventClaimLeaseDuration)

	tx, err := r.client.Tx(ctx)
	if err != nil {
		return nil, fmt.Errorf("start outbox claim transaction: %w", err)
	}
	defer func() { _ = tx.Rollback() }()

	ambiguousQuery := tx.OutboxEvent.Query().Where(
		outboxevent.StatusEQ(outboxEventStatusPublishing),
		outboxevent.LastErrorHasPrefix(outboxDeliveryAttemptPrefix),
		outboxevent.Or(outboxevent.ClaimExpiresAtLTE(now), outboxevent.ClaimExpiresAtIsNil()),
	)
	if eventType != "" {
		ambiguousQuery = ambiguousQuery.Where(outboxevent.EventTypeEQ(eventType))
	}
	ambiguous, err := ambiguousQuery.All(ctx)
	if err != nil {
		return nil, fmt.Errorf("query ambiguous expired outbox claims: %w", err)
	}
	for _, event := range ambiguous {
		updated, err := tx.OutboxEvent.Update().Where(
			outboxevent.IDEQ(event.ID), outboxevent.StatusEQ(outboxEventStatusPublishing),
			outboxevent.LastErrorHasPrefix(outboxDeliveryAttemptPrefix),
		).SetStatus(outboxEventStatusBlocked).AddAttemptCount(1).SetLastError(outboxDeliveryUnknownError).
			ClearClaimToken().ClearClaimExpiresAt().Save(ctx)
		if err != nil {
			return nil, fmt.Errorf("block ambiguous outbox claim: %w", err)
		}
		if updated == 1 {
			if err := tx.AuditLog.Create().SetTenantID(event.TenantID).SetRequestID(event.EventID).
				SetResource("outbox_event").SetAction("outbox.delivery_unknown").SetPath("outbox/events").
				SetMethod("WORKER").SetStatusCode(409).Exec(ctx); err != nil {
				return nil, fmt.Errorf("audit ambiguous outbox claim: %w", err)
			}
		}
	}

	expiredClaims := tx.OutboxEvent.Update().
		Where(
			outboxevent.StatusEQ(outboxEventStatusPublishing),
			outboxevent.Or(
				outboxevent.LastErrorIsNil(),
				outboxevent.Not(outboxevent.LastErrorHasPrefix(outboxDeliveryAttemptPrefix)),
			),
			outboxevent.Or(
				outboxevent.ClaimExpiresAtLTE(now),
				outboxevent.ClaimExpiresAtIsNil(),
			),
		)
	if eventType != "" {
		expiredClaims = expiredClaims.Where(outboxevent.EventTypeEQ(eventType))
	}
	_, err = expiredClaims.
		SetStatus(outboxEventStatusPending).
		ClearClaimToken().
		ClearClaimExpiresAt().
		Save(ctx)
	if err != nil {
		return nil, fmt.Errorf("recover expired outbox claims: %w", err)
	}

	candidateQuery := tx.OutboxEvent.Query().
		Where(
			outboxevent.StatusEQ(outboxEventStatusPending),
			outboxevent.NextAttemptAtLTE(now),
		)
	if eventType != "" {
		candidateQuery = candidateQuery.Where(outboxevent.EventTypeEQ(eventType))
	}
	candidates, err := candidateQuery.
		Order(ent.Asc(outboxevent.FieldNextAttemptAt)).
		Limit(limit).
		All(ctx)
	if err != nil {
		return nil, fmt.Errorf("query due outbox events: %w", err)
	}

	claimed := make([]*ent.OutboxEvent, 0, len(candidates))
	for _, candidate := range candidates {
		claimToken := uuid.NewString()
		claim := tx.OutboxEvent.Update().
			Where(
				outboxevent.IDEQ(candidate.ID),
				outboxevent.StatusEQ(outboxEventStatusPending),
				outboxevent.NextAttemptAtLTE(now),
			)
		if eventType != "" {
			claim = claim.Where(outboxevent.EventTypeEQ(eventType))
		}
		updated, err := claim.
			SetStatus(outboxEventStatusPublishing).
			SetClaimToken(claimToken).
			SetClaimExpiresAt(claimExpiresAt).
			Save(ctx)
		if err != nil {
			return nil, fmt.Errorf("claim outbox event %d: %w", candidate.ID, err)
		}
		if updated == 1 {
			// The conditional update is the authoritative claim result. Returning
			// this snapshot avoids a separate post-commit read racing another
			// SQLite connection while retaining the exact lease the caller owns.
			candidate.Status = outboxEventStatusPublishing
			candidate.ClaimToken = claimToken
			candidate.ClaimExpiresAt = claimExpiresAt
			claimed = append(claimed, candidate)
		}
	}

	if err := tx.Commit(); err != nil {
		return nil, fmt.Errorf("commit outbox claims: %w", err)
	}
	if len(claimed) == 0 {
		return nil, nil
	}
	return claimed, nil
}

// MarkDeliveryAttemptStarted durably records the boundary immediately before
// an external side effect. Expiry after this marker is delivery-ambiguous.
func (r *OutboxEventRepository) MarkDeliveryAttemptStarted(ctx context.Context, eventID int, claimToken, deliveryID string) error {
	updated, err := r.client.OutboxEvent.Update().Where(
		outboxevent.IDEQ(eventID), outboxevent.StatusEQ(outboxEventStatusPublishing),
		outboxevent.ClaimTokenEQ(claimToken), outboxevent.ClaimExpiresAtGT(r.currentTime()),
	).SetLastError(outboxDeliveryAttemptPrefix + summarizeOutboxError(deliveryID)).Save(ctx)
	if err != nil {
		return fmt.Errorf("mark delivery attempt started: %w", err)
	}
	if updated == 0 {
		return ErrOutboxEventClaimLost
	}
	return nil
}

// BlockUnknownPendingEventTypes prevents unregistered events from remaining
// pending forever. Status and audit evidence commit atomically.
func (r *OutboxEventRepository) BlockUnknownPendingEventTypes(ctx context.Context, now time.Time, limit int, knownTypes []string) (int, error) {
	if limit <= 0 || len(knownTypes) == 0 {
		return 0, nil
	}
	tx, err := r.client.Tx(ctx)
	if err != nil {
		return 0, fmt.Errorf("start unknown outbox transaction: %w", err)
	}
	defer func() { _ = tx.Rollback() }()
	events, err := tx.OutboxEvent.Query().Where(
		outboxevent.StatusEQ(outboxEventStatusPending), outboxevent.NextAttemptAtLTE(now.UTC()),
		outboxevent.EventTypeNotIn(knownTypes...),
	).Order(ent.Asc(outboxevent.FieldNextAttemptAt)).Limit(limit).All(ctx)
	if err != nil {
		return 0, fmt.Errorf("query unknown outbox events: %w", err)
	}
	blocked := 0
	for _, event := range events {
		updated, err := tx.OutboxEvent.Update().Where(outboxevent.IDEQ(event.ID), outboxevent.StatusEQ(outboxEventStatusPending)).
			SetStatus(outboxEventStatusBlocked).AddAttemptCount(1).SetLastError("unknown outbox event type: " + event.EventType).Save(ctx)
		if err != nil {
			return 0, fmt.Errorf("block unknown outbox event: %w", err)
		}
		if updated == 0 {
			continue
		}
		if err := tx.AuditLog.Create().SetTenantID(event.TenantID).SetRequestID(event.EventID).
			SetResource("outbox_event").SetAction("outbox.unknown_event_type").SetPath("outbox/events").
			SetMethod("WORKER").SetStatusCode(422).Exec(ctx); err != nil {
			return 0, fmt.Errorf("audit unknown outbox event: %w", err)
		}
		blocked++
	}
	if err := tx.Commit(); err != nil {
		return 0, fmt.Errorf("commit unknown outbox transaction: %w", err)
	}
	return blocked, nil
}

// MarkRetry makes an active failed claim eligible for a later delivery attempt.
func (r *OutboxEventRepository) MarkRetry(ctx context.Context, eventID int, claimToken, lastError string, nextAttemptAt time.Time) error {
	updated, err := r.client.OutboxEvent.Update().
		Where(
			outboxevent.IDEQ(eventID),
			outboxevent.StatusEQ(outboxEventStatusPublishing),
			outboxevent.ClaimTokenEQ(claimToken),
			outboxevent.ClaimExpiresAtGT(r.currentTime()),
		).
		SetStatus(outboxEventStatusPending).
		AddAttemptCount(1).
		SetLastError(summarizeOutboxError(lastError)).
		SetNextAttemptAt(nextAttemptAt).
		ClearClaimToken().
		ClearClaimExpiresAt().
		Save(ctx)
	if err != nil {
		return fmt.Errorf("schedule outbox retry: %w", err)
	}
	if updated == 0 {
		return ErrOutboxEventClaimLost
	}
	return nil
}

// MarkRetryWithAudit atomically returns an active claim to pending and records
// audit evidence for the failed delivery. A rejection can never become
// retryable unless its audit record commits with the same transition.
func (r *OutboxEventRepository) MarkRetryWithAudit(ctx context.Context, eventID int, claimToken, lastError string, nextAttemptAt time.Time, audit OutboxRetryAudit) error {
	tx, err := r.client.Tx(ctx)
	if err != nil {
		return fmt.Errorf("start outbox retry audit transaction: %w", err)
	}
	defer func() { _ = tx.Rollback() }()

	updated, err := tx.OutboxEvent.Update().
		Where(
			outboxevent.IDEQ(eventID),
			outboxevent.StatusEQ(outboxEventStatusPublishing),
			outboxevent.ClaimTokenEQ(claimToken),
			outboxevent.ClaimExpiresAtGT(r.currentTime()),
		).
		SetStatus(outboxEventStatusPending).
		AddAttemptCount(1).
		SetLastError(summarizeOutboxError(lastError)).
		SetNextAttemptAt(nextAttemptAt).
		ClearClaimToken().
		ClearClaimExpiresAt().
		Save(ctx)
	if err != nil {
		return fmt.Errorf("schedule outbox retry: %w", err)
	}
	if updated == 0 {
		return ErrOutboxEventClaimLost
	}

	if err := tx.AuditLog.Create().
		SetTenantID(audit.TenantID).
		SetRequestID(audit.RequestID).
		SetResource(audit.Resource).
		SetAction(audit.Action).
		SetPath(audit.Path).
		SetMethod(audit.Method).
		SetStatusCode(audit.StatusCode).
		Exec(ctx); err != nil {
		return fmt.Errorf("record outbox retry audit: %w", err)
	}
	if err := tx.Commit(); err != nil {
		return fmt.Errorf("commit outbox retry audit transaction: %w", err)
	}
	return nil
}

// MarkPublished finalizes an active, successfully delivered publishing lease.
func (r *OutboxEventRepository) MarkPublished(ctx context.Context, eventID int, claimToken string, publishedAt time.Time) error {
	updated, err := r.client.OutboxEvent.Update().
		Where(
			outboxevent.IDEQ(eventID),
			outboxevent.StatusEQ(outboxEventStatusPublishing),
			outboxevent.ClaimTokenEQ(claimToken),
			outboxevent.ClaimExpiresAtGT(r.currentTime()),
		).
		SetStatus(outboxEventStatusPublished).
		SetPublishedAt(publishedAt).
		ClearLastError().
		ClearClaimToken().
		ClearClaimExpiresAt().
		Save(ctx)
	if err != nil {
		return fmt.Errorf("mark outbox event published: %w", err)
	}
	if updated == 0 {
		return ErrOutboxEventClaimLost
	}
	return nil
}

// MarkBlocked records a non-retryable configuration or payload failure while
// retaining the delivery for operator inspection.
func (r *OutboxEventRepository) MarkBlocked(ctx context.Context, eventID int, claimToken, lastError string) error {
	return r.markTerminal(ctx, eventID, claimToken, outboxEventStatusBlocked, lastError)
}

// MarkDeliveryUnknown atomically records the ambiguous terminal state and the
// audit evidence required for manual reconciliation.
func (r *OutboxEventRepository) MarkDeliveryUnknown(ctx context.Context, event *ent.OutboxEvent, claimToken, lastError string) error {
	if event == nil {
		return fmt.Errorf("outbox event is required")
	}
	tx, err := r.client.Tx(ctx)
	if err != nil {
		return fmt.Errorf("start delivery unknown transaction: %w", err)
	}
	defer func() { _ = tx.Rollback() }()
	updated, err := tx.OutboxEvent.Update().Where(
		outboxevent.IDEQ(event.ID), outboxevent.StatusEQ(outboxEventStatusPublishing),
		outboxevent.ClaimTokenEQ(claimToken), outboxevent.ClaimExpiresAtGT(r.currentTime()),
	).SetStatus(outboxEventStatusBlocked).AddAttemptCount(1).SetLastError(summarizeOutboxError(lastError)).
		ClearClaimToken().ClearClaimExpiresAt().Save(ctx)
	if err != nil {
		return fmt.Errorf("mark delivery unknown: %w", err)
	}
	if updated == 0 {
		return ErrOutboxEventClaimLost
	}
	if err := tx.AuditLog.Create().SetTenantID(event.TenantID).SetRequestID(event.EventID).
		SetResource("outbox_event").SetAction("outbox.delivery_unknown").SetPath("outbox/events").
		SetMethod("WORKER").SetStatusCode(409).Exec(ctx); err != nil {
		return fmt.Errorf("audit delivery unknown: %w", err)
	}
	if err := tx.Commit(); err != nil {
		return fmt.Errorf("commit delivery unknown transaction: %w", err)
	}
	return nil
}

// MarkDeadLetter records a transient delivery that exhausted its configured
// retry budget. It remains observable and can be replayed by an explicit
// operator action in the unified outbox boundary.
func (r *OutboxEventRepository) MarkDeadLetter(ctx context.Context, eventID int, claimToken, lastError string) error {
	return r.markTerminal(ctx, eventID, claimToken, outboxEventStatusDeadLetter, lastError)
}

func (r *OutboxEventRepository) markTerminal(ctx context.Context, eventID int, claimToken, status, lastError string) error {
	updated, err := r.client.OutboxEvent.Update().
		Where(
			outboxevent.IDEQ(eventID),
			outboxevent.StatusEQ(outboxEventStatusPublishing),
			outboxevent.ClaimTokenEQ(claimToken),
			outboxevent.ClaimExpiresAtGT(r.currentTime()),
		).
		SetStatus(status).
		AddAttemptCount(1).
		SetLastError(summarizeOutboxError(lastError)).
		ClearClaimToken().
		ClearClaimExpiresAt().
		Save(ctx)
	if err != nil {
		return fmt.Errorf("mark outbox event %s: %w", status, err)
	}
	if updated == 0 {
		return ErrOutboxEventClaimLost
	}
	return nil
}

func (r *OutboxEventRepository) currentTime() time.Time {
	return r.clock().UTC()
}

func summarizeOutboxError(lastError string) string {
	summary := strings.Join(strings.Fields(lastError), " ")
	summary = outboxSensitiveErrorURLUserinfoPattern.ReplaceAllString(summary, "$1[redacted]@")
	// Error payloads can be embedded JSON; normalize escaped quotes so the same
	// redactor covers both direct and string-escaped response bodies.
	summary = strings.ReplaceAll(summary, `\"`, `"`)
	summary = outboxSensitiveErrorJSONValuePattern.ReplaceAllString(summary, "$1[redacted]$2")
	summary = outboxSensitiveErrorValuePattern.ReplaceAllString(summary, "$1$2=[redacted]")
	if len(summary) <= outboxEventLastErrorMaxLength {
		return summary
	}
	return summary[:outboxEventLastErrorMaxLength]
}

func isRetryableOutboxClaimError(err error) bool {
	message := strings.ToLower(err.Error())
	return strings.Contains(message, "database is locked") ||
		strings.Contains(message, "database table is locked") ||
		strings.Contains(message, "could not serialize access") ||
		strings.Contains(message, "deadlock detected")
}
