//go:build integration_postgres

package integration

import (
	"context"
	"errors"
	"github.com/stretchr/testify/require"
	"itsm-backend/dto"
	"itsm-backend/ent"
	"sync"
	"testing"
)

func TestWorkItemAssignmentIncidentConcurrentAndReplay(t *testing.T) {
	for _, sameKey := range []bool{false, true} {
		t.Run(map[bool]string{false: "competing", true: "same-key"}[sameKey], func(t *testing.T) {
			f := incidentLifecycleFixture(t)
			f.client.Ticket.UpdateOneID(f.inc.WorkItemID).SetAssigneeID(f.actor.ID).ExecX(f.ctx)
			next := f.client.User.Create().SetTenantID(f.tenant.ID).SetUsername("next").SetName("next").SetRole("agent").SetActive(true).SetEmail("next@example.test").SetPasswordHash("test").SaveX(f.ctx)
			cmd := incidentPGCommand(f, "assign-a")
			cmd.Action, cmd.Reason, cmd.Resolution, cmd.AssigneeID = "assign", "handover", "", next.ID
			other := cmd
			if !sameKey {
				other.Meta.OperationID = "assign-b"
			}
			start := make(chan struct{})
			errs := make(chan error, 2)
			var wg sync.WaitGroup
			for _, command := range []dto.IncidentCommand{cmd, other} {
				wg.Add(1)
				go func(c dto.IncidentCommand) {
					defer wg.Done()
					<-start
					_, err := f.svc.ApplyIncidentCommand(f.ctx, c)
					errs <- err
				}(command)
			}
			close(start)
			wg.Wait()
			close(errs)
			successes := 0
			for err := range errs {
				if err == nil {
					successes++
				}
			}
			if sameKey {
				require.Equal(t, 2, successes)
			} else {
				require.Equal(t, 1, successes)
			}
			item := f.client.Ticket.GetX(f.ctx, f.inc.WorkItemID)
			require.Equal(t, cmd.Meta.ExpectedVersion+1, item.Version)
			require.Equal(t, "in_progress", item.Status)
			require.Equal(t, next.ID, item.AssigneeID)
			require.Equal(t, 1, f.client.AuditLog.Query().CountX(f.ctx))
			require.Equal(t, 1, f.client.IncidentEvent.Query().CountX(f.ctx))
			require.Equal(t, 1, f.client.OutboxEvent.Query().CountX(f.ctx), "no status event for reassignment")
			f.actor.Update().SetRole("unknown-no-permissions").ExecX(f.ctx)
			_, err := f.svc.ApplyIncidentCommand(f.ctx, cmd)
			require.Error(t, err, "current authorization required for replay")
		})
	}
}

func TestWorkItemAssignmentIncidentAuditFailureRollsBack(t *testing.T) {
	f := incidentLifecycleFixture(t)
	cmd := incidentPGCommand(f, "assign-fault")
	cmd.Action, cmd.AssigneeID, cmd.Reason = "assign", f.actor.ID, "handover"
	before := f.client.Ticket.GetX(f.ctx, f.inc.WorkItemID)
	f.client.AuditLog.Use(func(next ent.Mutator) ent.Mutator {
		return ent.MutateFunc(func(ctx context.Context, m ent.Mutation) (ent.Value, error) { return nil, errors.New("audit failure") })
	})
	_, err := f.svc.ApplyIncidentCommand(f.ctx, cmd)
	require.ErrorContains(t, err, "audit failure")
	after := f.client.Ticket.GetX(f.ctx, before.ID)
	require.Equal(t, before.Version, after.Version)
	require.Equal(t, before.AssigneeID, after.AssigneeID)
	require.Zero(t, f.client.IncidentEvent.Query().CountX(f.ctx))
	require.Zero(t, f.client.AuditLog.Query().CountX(f.ctx))
}
