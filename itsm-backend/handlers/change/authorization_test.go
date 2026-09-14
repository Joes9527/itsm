package change

import (
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

func TestBuildChangeActions(t *testing.T) {
	f := newGovernedChangeFixture(t, "normal")
	_, actions, tasks, err := f.svc.GetChangeActionView(f.ctx, f.record.ID, f.command("read", f.requester).Meta)
	require.NoError(t, err)
	require.Len(t, actions, 13)
	require.True(t, actions["submit"].Allowed)
	require.Empty(t, tasks)
	for _, action := range []string{"approve", "reject", "implement", "record_outcome"} {
		require.False(t, actions[action].Allowed)
	}
}

func TestBuildChangeActionsUsesDistinctSelfApprovalAndRejectionReasons(t *testing.T) {
	f := newGovernedChangeFixture(t, "normal")
	f.submit(t)
	f.assess(t)
	_, actions, _, err := f.svc.GetChangeActionView(f.ctx, f.record.ID, f.command("read", f.requester).Meta)
	require.NoError(t, err)
	require.False(t, actions["approve"].Allowed)
	require.NotEmpty(t, actions["approve"].Reason)
	require.False(t, actions["reject"].Allowed)
	require.NotEmpty(t, actions["reject"].Reason)
	_, actions, _, err = f.svc.GetChangeActionView(f.ctx, f.record.ID, f.command("read", f.approver).Meta)
	require.NoError(t, err)
	require.True(t, actions["approve"].Allowed)
	require.True(t, actions["reject"].Allowed)
}

func TestCanStartImplementationIsTypeAware(t *testing.T) {
	for _, kind := range []string{"normal", "standard", "emergency"} {
		t.Run(kind, func(t *testing.T) {
			f := newGovernedChangeFixture(t, kind)
			_, actions, _, err := f.svc.GetChangeActionView(f.ctx, f.record.ID, f.command("read", f.requester).Meta)
			require.NoError(t, err)
			require.False(t, actions["implement"].Allowed)
			f.submit(t)
			f.assess(t)
			_, err = f.svc.CompleteChangeTask(f.ctx, f.taskCommand(t, "approve", f.approver))
			require.NoError(t, err)
			if kind != "emergency" {
				cmd := f.taskCommand(t, "schedule", f.requester)
				start, end := time.Now().Add(-2*time.Hour), time.Now().Add(-time.Hour)
				cmd.PlannedStart = &start
				cmd.PlannedEnd = &end
				_, err = f.svc.CompleteChangeTask(f.ctx, cmd)
				require.NoError(t, err)
			}
			_, actions, _, err = f.svc.GetChangeActionView(f.ctx, f.record.ID, f.command("read", f.requester).Meta)
			require.NoError(t, err)
			require.Equal(t, kind == "emergency", actions["implement"].Allowed)
		})
	}
}
