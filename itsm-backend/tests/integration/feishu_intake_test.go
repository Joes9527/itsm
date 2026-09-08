package integration

import (
	"context"
	"errors"
	"testing"

	"github.com/stretchr/testify/require"
	"go.uber.org/zap"
	feishu "itsm-backend/connector/builtin/feishu"
	"itsm-backend/ent"

	"itsm-backend/service"
)

func TestFeishuCreationUsesIntakeAndRejectsUnmappedActor(t *testing.T) {
	f := newUnifiedIntakeFixture(t)
	ctx := context.Background()
	f.client.User.UpdateOneID(f.identity.ActorID).SetFeishuOpenID("trusted-open-id").ExecX(ctx)
	svc := service.NewFeishuSyncService(f.client, zap.NewNop().Sugar(), f.app)
	_, _, err := svc.SyncFeishuTaskToTicket(ctx, f.identity.TenantID, &feishu.FeishuTask{GUID: "unmapped", Name: "Unmapped task", CreatorID: "unknown"})
	require.Error(t, err, "unknown creator cannot become the first active tenant user")
	require.Zero(t, f.client.Ticket.Query().CountX(ctx))
	first, action, err := svc.SyncFeishuTaskToTicket(ctx, f.identity.TenantID, &feishu.FeishuTask{GUID: "task-one", Name: "First task", CreatorID: "trusted-open-id", Priority: "high"})
	require.NoError(t, err)
	require.Equal(t, "created", action)
	require.Equal(t, 1, f.client.IntakeRequest.Query().CountX(ctx))
	require.Equal(t, 1, f.client.IntakeResolutionSnapshot.Query().CountX(ctx))
	second, action, err := svc.SyncFeishuTaskToTicket(ctx, f.identity.TenantID, &feishu.FeishuTask{GUID: "task-one", Name: "Updated task", CreatorID: "trusted-open-id", Priority: "low"})
	require.NoError(t, err)
	require.Equal(t, "updated", action)
	require.Equal(t, first.TicketID, second.TicketID)
	require.Equal(t, 1, f.client.IntakeRequest.Query().CountX(ctx))
	require.Equal(t, 1, f.client.FeishuTicketSync.Query().CountX(ctx))
	require.Equal(t, "Updated task", f.client.Ticket.GetX(ctx, first.TicketID).Title)
}
func TestFeishuMappingFailureRollsBackIntakeNumberAndGraph(t *testing.T) {
	f := newUnifiedIntakeFixture(t)
	ctx := context.Background()
	f.client.User.UpdateOneID(f.identity.ActorID).SetFeishuOpenID("trusted").ExecX(ctx)
	svc := service.NewFeishuSyncService(f.client, zap.NewNop().Sugar(), f.app)
	fail := true
	injected := errors.New("injected sync mapping failure")
	f.client.FeishuTicketSync.Use(func(next ent.Mutator) ent.Mutator {
		return ent.MutateFunc(func(ctx context.Context, m ent.Mutation) (ent.Value, error) {
			if fail {
				return nil, injected
			}
			return next.Mutate(ctx, m)
		})
	})
	task := &feishu.FeishuTask{GUID: "task-failure", CreatorID: "trusted", Name: "Task"}
	_, _, err := svc.SyncFeishuTaskToTicket(ctx, f.identity.TenantID, task)
	require.ErrorIs(t, err, injected)
	assertNoEntryGraph(t, f.client)
	fail = false
	created, _, err := svc.SyncFeishuTaskToTicket(ctx, f.identity.TenantID, task)
	require.NoError(t, err)
	require.Regexp(t, `^TKT-[0-9]{6}-000001$`, created.TicketNumber)
	require.Equal(t, 1, f.client.FeishuTicketSync.Query().CountX(ctx))
	require.Equal(t, 1, f.client.IntakeRequest.Query().CountX(ctx))
}

func TestFeishuNativeMSPCreatorUsesCanonicalRole(t *testing.T) {
	f := newUnifiedIntakeFixture(t)
	ctx := context.Background()
	f.client.Tenant.UpdateOneID(f.identity.TenantID).SetType("msp_provider").ExecX(ctx)
	f.client.User.UpdateOneID(f.identity.ActorID).SetRole("admin").SetMspRole("provider_agent").SetFeishuOpenID("native-msp-open").ExecX(ctx)
	f.client.Role.Update().SetCode("msp_tech").ExecX(ctx)
	svc := service.NewFeishuSyncService(f.client, zap.NewNop().Sugar(), f.app)
	result, action, err := svc.SyncFeishuTaskToTicket(ctx, f.identity.TenantID, &feishu.FeishuTask{GUID: "native-msp", Name: "Native MSP request", CreatorID: "native-msp-open", Priority: "high"})
	require.NoError(t, err)
	require.Equal(t, "created", action)
	item := f.client.Ticket.GetX(ctx, result.TicketID)
	require.Equal(t, f.identity.ActorID, item.OpenedByID)
	require.Equal(t, f.identity.ActorID, item.RequesterID)
	require.Equal(t, f.identity.TenantID, f.client.IntakeRequest.Query().OnlyX(ctx).ActorTenantID)
}
