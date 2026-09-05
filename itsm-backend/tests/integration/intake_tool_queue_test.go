package integration

import (
	"context"
	"encoding/json"
	"errors"
	"github.com/stretchr/testify/require"
	"go.uber.org/zap"
	"itsm-backend/ent"
	"itsm-backend/service"
	"testing"
	"time"
)

func TestIntakeApprovedToolCreationRecoversAcknowledgement(t *testing.T) {
	f := newUnifiedIntakeFixture(t)
	ctx := context.Background()
	q := service.NewToolQueue(f.client, nil, f.app, nil, 1, zap.NewNop().Sugar())
	defer q.Close()
	inv := f.client.ToolInvocation.Create().SetTenantID(f.identity.TenantID).SetUserID(f.identity.ActorID).SetToolName("create_ticket").SetArguments(`{"title":"Approved AI request","description":"Verified requested work","priority":"high"}`).SetNeedsApproval(true).SetApprovalState("approved").SetApprovedBy(f.identity.ActorID).SetApprovedAt(time.Now()).SetStatus("pending").SaveX(ctx)
	failed := false
	f.client.ToolInvocation.Use(func(next ent.Mutator) ent.Mutator {
		return ent.MutateFunc(func(ctx context.Context, m ent.Mutation) (ent.Value, error) {
			if typed, ok := m.(*ent.ToolInvocationMutation); ok && !failed {
				if status, ok := typed.Status(); ok && status == "done" {
					failed = true
					return nil, errors.New("injected tool acknowledgement failure")
				}
			}
			return next.Mutate(ctx, m)
		})
	})
	job := service.ToolJob{InvocationID: inv.ID, TenantID: f.identity.TenantID}
	require.Error(t, q.ProcessJob(ctx, job))
	require.True(t, failed)
	require.Equal(t, 1, f.client.Ticket.Query().CountX(ctx))
	require.NoError(t, q.ProcessJob(ctx, job))
	require.Equal(t, 1, f.client.Ticket.Query().CountX(ctx))
	recorded := f.client.ToolInvocation.GetX(ctx, inv.ID)
	require.Equal(t, "done", recorded.Status)
	var result map[string]any
	require.NoError(t, json.Unmarshal([]byte(*recorded.Result), &result))
	require.Contains(t, result, "workItemId")
	require.NotContains(t, result, "id")
	require.True(t, result["replayed"].(bool))
	f.client.User.UpdateOneID(f.identity.ActorID).SetActive(false).SaveX(ctx)
	require.Error(t, q.ProcessJob(ctx, job))
	require.Equal(t, 1, f.client.Ticket.Query().CountX(ctx))
}
func TestIntakeToolCreationRequiresApprovedTenantInvocation(t *testing.T) {
	for _, state := range []string{"pending", "rejected", "wrong_tenant", "missing_actor", "malformed"} {
		t.Run(state, func(t *testing.T) {
			f := newUnifiedIntakeFixture(t)
			ctx := context.Background()
			q := service.NewToolQueue(f.client, nil, f.app, nil, 1, zap.NewNop().Sugar())
			defer q.Close()
			builder := f.client.ToolInvocation.Create().SetTenantID(f.identity.TenantID).SetToolName("create_ticket").SetArguments(`{"title":"Approved AI request","description":"Request","priority":"high"}`).SetNeedsApproval(true).SetApprovalState("approved").SetApprovedBy(f.identity.ActorID).SetApprovedAt(time.Now()).SetStatus("pending")
			if state != "missing_actor" {
				builder.SetUserID(f.identity.ActorID)
			}
			if state == "pending" || state == "rejected" {
				builder.SetApprovalState(state)
			}
			if state == "malformed" {
				builder.SetArguments(`{"title":"one","title":"two"}`)
			}
			inv := builder.SaveX(ctx)
			tenantID := f.identity.TenantID
			if state == "wrong_tenant" {
				tenantID += 100
			}
			require.Error(t, q.ProcessJob(ctx, service.ToolJob{InvocationID: inv.ID, TenantID: tenantID}))
			assertNoEntryGraph(t, f.client)
		})
	}
}
