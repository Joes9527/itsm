package service

import (
	"errors"
	"reflect"
	"testing"
)

// B2 step 1: the pure decision function that gates the Change -> Problem
// verification prompt. Only an explicit successful outcome requests verification;
// every other professional outcome must not be interpreted as a completed repair.
func TestChangeOutcomeRequestsVerification(t *testing.T) {
	if !RequiresProblemVerification("successful") {
		t.Fatal("successful change outcome must request problem verification")
	}
	for _, outcome := range []string{"failed", "rolled_back", "closed", "", "unknown"} {
		if RequiresProblemVerification(outcome) {
			t.Fatalf("outcome %q must not be treated as a verified repair", outcome)
		}
	}
}

// The shared registry must own every relation/outcome event type exactly once, so a
// missing consumer registration is a build-time wiring failure rather than a
// silently undelivered event.
func TestRelationAndOutcomeEventTypesAreRegistered(t *testing.T) {
	registry, err := NewOutboxEventTypeRegistry([]OutboxDeliveryHandler{
		NewWorkItemRelationCreatedDeliveryHandler(nil, nil, nil),
		NewWorkItemRelationRemovedDeliveryHandler(nil, nil, nil),
		NewChangeOutcomeDeliveryHandler(nil, nil, nil),
		NewProblemResolvedDeliveryHandler(nil, nil, nil),
	})
	if err != nil {
		t.Fatalf("register relation delivery handlers: %v", err)
	}
	want := []string{ChangeOutcomeEventType, ProblemResolvedEventType, RelationCreatedEventType, RelationRemovedEventType}
	if got := registry.HandlerTypes(); !reflect.DeepEqual(got, want) {
		t.Fatalf("handler types mismatch: got %v want %v", got, want)
	}
	for _, eventType := range want {
		if handler := registry.Handler(eventType); handler == nil {
			t.Fatalf("event type %s has no registered handler", eventType)
		}
		if _, replaySafe := registry.Handler(eventType).(ReplaySafeOutboxDeliveryHandler); !replaySafe {
			t.Fatalf("event type %s must declare durable replay safety", eventType)
		}
	}
}

// A fan-out that reports a terminally blocked target must still surface a retryable
// target failure, because the worker treats a blocked return as terminal and would
// otherwise never retry the target that could still succeed.
func TestFanOutPrefersRetryableErrorOverBlocked(t *testing.T) {
	blocked := blockOutboxDelivery("counterpart has no eligible recipient")
	retryable := errors.New("notification transport unavailable")

	if got := preferRetryableError(blocked, retryable); got != retryable {
		t.Fatalf("retryable target failure must win over a blocked one, got %v", got)
	}
	if got := preferRetryableError(blocked, nil); got != blocked {
		t.Fatalf("a blocked outcome must be reported when nothing is retryable, got %v", got)
	}
	if got := preferRetryableError(nil, nil); got != nil {
		t.Fatalf("no target failure must stay nil, got %v", got)
	}
}
