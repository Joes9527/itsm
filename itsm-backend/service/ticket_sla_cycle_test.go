package service

import (
	"github.com/stretchr/testify/require"
	"itsm-backend/ent"
	"testing"
	"time"
)

func TestResetSLACycle(t *testing.T) {
	oldAt := time.Date(2026, 9, 9, 8, 0, 0, 0, time.UTC)
	reopened := oldAt.Add(4 * time.Hour)
	old := SLACycleClock{Number: 1, StartedAt: oldAt, ResponseAt: &oldAt, ResolvedAt: &oldAt, PausedMinutes: 30}
	got := ResetSLACycle(old, reopened)
	if got.Number != 2 || !got.StartedAt.Equal(reopened) || got.ResponseAt != nil || got.ResolvedAt != nil || got.PausedMinutes != 0 {
		t.Fatalf("cycle not reset: %+v", got)
	}
	if old.Number != 1 || old.ResolvedAt == nil {
		t.Fatal("old result overwritten")
	}
}

func TestClosedSLACycleStopsPendingMeasurements(t *testing.T) {
	start := time.Date(2026, 9, 9, 8, 0, 0, 0, time.UTC)
	for _, closeAfter := range []time.Duration{10 * time.Minute, 90 * time.Minute} {
		closed := start.Add(closeAfter)
		item := &ent.Ticket{SLACycleStartedAt: start, ClosedAt: &closed, SLAResponseDeadline: start.Add(30 * time.Minute), SLAResolutionDeadline: start.Add(time.Hour), SLAPausedMinutes: 5}
		atClose := projectSLACycle(item, *item.ClosedAt)
		later := projectSLACycle(item, item.ClosedAt.Add(48*time.Hour))
		require.Equal(t, atClose, later, "pending clocks and breach facts must freeze at cancellation")
		require.Equal(t, closeAfter > time.Hour, later.ResolutionBreached)
		require.True(t, item.FirstResponseAt.IsZero())
		require.True(t, item.ResolvedAt.IsZero())
	}
}
