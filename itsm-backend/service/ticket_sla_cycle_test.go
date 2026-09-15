package service

import (
	"testing"
	"time"

	"github.com/stretchr/testify/require"
	"go.uber.org/zap"
	"itsm-backend/ent"
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

func TestSLACycleContractStatus(t *testing.T) {
	for _, tc := range []struct {
		name string
		item ent.Ticket
		want string
	}{
		{"not_required", ent.Ticket{}, "not_required"},
		{"missing_policy", ent.Ticket{SLADefinitionID: 12}, "configuration_missing"},
		{"legacy_deadline_without_policy", ent.Ticket{SLAResponseDeadline: time.Now().Add(time.Hour)}, "configuration_missing"},
	} {
		t.Run(tc.name, func(t *testing.T) { require.Equal(t, tc.want, projectSLACycle(&tc.item, time.Now()).SLAStatus) })
	}
}

func TestTicketSLADetailClosedRemainingFrozen(t *testing.T) {
	client, _, ctx := setupIncidentTest(t)
	defer client.Close()
	tenant, err := createIncidentTestTenant(ctx, client, "closed-sla")
	require.NoError(t, err)
	actor, err := createIncidentTestUser(ctx, client, tenant.ID, "closed-actor")
	require.NoError(t, err)
	at := time.Now().Add(-48 * time.Hour)
	closed := at.Add(10 * time.Minute)
	item := client.Ticket.Create().SetTenantID(tenant.ID).SetRequesterID(actor.ID).SetTitle("closed").SetTicketNumber("CLOSED-SLA").SetCreatedAt(at).SetClosedAt(closed).SetSLAResponseDeadline(at.Add(30 * time.Minute)).SaveX(ctx)
	got, err := NewTicketServiceForTest(client, zap.NewNop().Sugar()).GetTicketSLAInfo(ctx, item.ID, tenant.ID)
	require.NoError(t, err)
	require.NotNil(t, got.ResponseTimeRemaining)
	require.Equal(t, 20, *got.ResponseTimeRemaining)
}
