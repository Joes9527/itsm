package service

import (
	"context"
	"errors"
	"fmt"
	"sort"
	"strings"
	"time"

	"itsm-backend/ent"

	"go.uber.org/zap"
)

// OutboxDeliveryHandler owns one event type and performs only its external
// side effect. Claim, retry, terminal state, and restart recovery stay in the
// shared outbox worker.
type OutboxDeliveryHandler interface {
	EventType() string
	Deliver(context.Context, *ent.OutboxEvent) error
}

type OutboxDeliveryWorkerConfig struct {
	BatchSize      int
	PollInterval   time.Duration
	HandlerTimeout time.Duration
	MaxAttempts    int
}

type outboxDeliveryBlockedError struct {
	reason string
}

func (e *outboxDeliveryBlockedError) Error() string { return e.reason }

func blockOutboxDelivery(reason string) error {
	return &outboxDeliveryBlockedError{reason: strings.TrimSpace(reason)}
}

type OutboxDeliveryWorker struct {
	repository *OutboxEventRepository
	config     OutboxDeliveryWorkerConfig
	handlers   map[string]OutboxDeliveryHandler
	eventTypes []string
	logger     *zap.SugaredLogger
	now        func() time.Time
}

func NewOutboxDeliveryWorker(
	repository *OutboxEventRepository,
	config OutboxDeliveryWorkerConfig,
	logger *zap.SugaredLogger,
	handlers ...OutboxDeliveryHandler,
) (*OutboxDeliveryWorker, error) {
	if repository == nil {
		return nil, fmt.Errorf("outbox repository is required")
	}
	if config.BatchSize <= 0 || config.PollInterval <= 0 || config.HandlerTimeout <= 0 || config.MaxAttempts <= 0 {
		return nil, fmt.Errorf("outbox worker batch size, poll interval, handler timeout, and max attempts must be positive")
	}
	if config.HandlerTimeout >= outboxEventClaimLeaseDuration {
		return nil, fmt.Errorf("outbox worker handler timeout must be shorter than the delivery lease")
	}
	if logger == nil {
		return nil, fmt.Errorf("outbox worker logger is required")
	}
	registry := make(map[string]OutboxDeliveryHandler, len(handlers))
	eventTypes := make([]string, 0, len(handlers))
	for _, handler := range handlers {
		if handler == nil {
			return nil, fmt.Errorf("outbox delivery handler is required")
		}
		eventType := strings.TrimSpace(handler.EventType())
		if eventType == "" {
			return nil, fmt.Errorf("outbox delivery handler event type is required")
		}
		if _, exists := registry[eventType]; exists {
			return nil, fmt.Errorf("duplicate outbox delivery handler: %s", eventType)
		}
		registry[eventType] = handler
		eventTypes = append(eventTypes, eventType)
	}
	if len(registry) == 0 {
		return nil, fmt.Errorf("at least one outbox delivery handler is required")
	}
	sort.Strings(eventTypes)
	return &OutboxDeliveryWorker{
		repository: repository,
		config:     config,
		handlers:   registry,
		eventTypes: eventTypes,
		logger:     logger,
		now:        time.Now,
	}, nil
}

func (w *OutboxDeliveryWorker) DispatchOnce(ctx context.Context) error {
	for _, eventType := range w.eventTypes {
		events, err := w.repository.ClaimDueByEventType(ctx, w.now().UTC(), w.config.BatchSize, eventType)
		if err != nil {
			return fmt.Errorf("claim %s outbox deliveries: %w", eventType, err)
		}
		for _, event := range events {
			if err := w.dispatch(ctx, w.handlers[eventType], event); err != nil {
				return err
			}
		}
	}
	return nil
}

func (w *OutboxDeliveryWorker) dispatch(ctx context.Context, handler OutboxDeliveryHandler, event *ent.OutboxEvent) error {
	handlerCtx, cancel := context.WithTimeout(ctx, w.config.HandlerTimeout)
	err := handler.Deliver(handlerCtx, event)
	cancel()
	if err == nil {
		if err := w.repository.MarkPublished(ctx, event.ID, event.ClaimToken, w.now().UTC()); err != nil {
			return fmt.Errorf("publish outbox delivery %s: %w", event.EventID, err)
		}
		return nil
	}

	var blocked *outboxDeliveryBlockedError
	if errors.As(err, &blocked) {
		if markErr := w.repository.MarkBlocked(ctx, event.ID, event.ClaimToken, blocked.Error()); markErr != nil {
			return fmt.Errorf("block outbox delivery %s: %w", event.EventID, markErr)
		}
		w.logger.Warnw("outbox delivery blocked", "event_id", event.EventID, "event_type", event.EventType)
		return nil
	}
	if event.AttemptCount+1 >= w.config.MaxAttempts {
		if markErr := w.repository.MarkDeadLetter(ctx, event.ID, event.ClaimToken, err.Error()); markErr != nil {
			return fmt.Errorf("dead-letter outbox delivery %s: %w", event.EventID, markErr)
		}
		w.logger.Errorw("outbox delivery moved to dead letter", "event_id", event.EventID, "event_type", event.EventType)
		return nil
	}
	nextAttemptAt := w.now().UTC().Add(outboxRetryDelay(event.AttemptCount + 1))
	if markErr := w.repository.MarkRetry(ctx, event.ID, event.ClaimToken, err.Error(), nextAttemptAt); markErr != nil {
		return fmt.Errorf("retry outbox delivery %s: %w", event.EventID, markErr)
	}
	return nil
}

func (w *OutboxDeliveryWorker) Run(ctx context.Context) {
	if err := w.DispatchOnce(ctx); err != nil && ctx.Err() == nil {
		w.logger.Errorw("outbox delivery dispatch failed", "error_summary", summarizeOutboxError(err.Error()))
	}
	ticker := time.NewTicker(w.config.PollInterval)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			if err := w.DispatchOnce(ctx); err != nil && ctx.Err() == nil {
				w.logger.Errorw("outbox delivery dispatch failed", "error_summary", summarizeOutboxError(err.Error()))
			}
		}
	}
}

func outboxRetryDelay(attemptCount int) time.Duration {
	const maxDelay = 5 * time.Minute
	delay := time.Second
	for attempt := 0; attempt < attemptCount && delay < maxDelay; attempt++ {
		if delay > maxDelay/2 {
			return maxDelay
		}
		delay *= 2
	}
	if delay > maxDelay {
		return maxDelay
	}
	return delay
}
