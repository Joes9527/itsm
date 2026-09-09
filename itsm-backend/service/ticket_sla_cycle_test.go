package service

import (
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
