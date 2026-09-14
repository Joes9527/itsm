package service

import (
	"context"
	"errors"
	"time"
)

// Only voteOnce can establish that the complete owning attempt rolled back.
// Caller-owned task transactions and uncertain commits never enter this retry.
type bpmnVoteRolledBack struct{ error }

func (e *bpmnVoteRolledBack) Unwrap() error { return e.error }

func (s *bpmnTaskService) Vote(ctx context.Context, taskID string, req *VoteRequest) error {
	for attempt := 0; ; attempt++ {
		err := s.voteOnce(ctx, taskID, req)
		var rolledBack *bpmnVoteRolledBack
		var state interface{ SQLState() string }
		if !errors.As(err, &rolledBack) || !errors.As(rolledBack.error, &state) || (state.SQLState() != "40001" && state.SQLState() != "40P01") || attempt >= outboxEventClaimRetryAttempts-1 {
			return err
		}
		delay := time.Duration(attempt+1) * outboxEventClaimRetryDelay
		if delay > outboxEventClaimRetryMaxDelay {
			delay = outboxEventClaimRetryMaxDelay
		}
		timer := time.NewTimer(delay)
		select {
		case <-ctx.Done():
			timer.Stop()
			return ctx.Err()
		case <-timer.C:
		}
	}
}
