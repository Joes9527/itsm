package bpmn_test

import (
	"context"
	"sync/atomic"
	"testing"

	"itsm-backend/ent"
	"itsm-backend/ent/enttest"
	changehandler "itsm-backend/handlers/change"
	. "itsm-backend/service/bpmn"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"go.uber.org/zap/zaptest"

	_ "github.com/mattn/go-sqlite3"
)

func setupChangeHandlerFixture(t *testing.T) (*ent.Client, *ChangeServiceTaskHandler, int, *ent.Change) {
	t.Helper()
	client := enttest.Open(t, "sqlite3", "file:change_handler_test?mode=memory&cache=shared&_fk=1")
	t.Cleanup(func() { client.Close() })
	ctx := context.Background()

	tenant, err := client.Tenant.Create().
		SetName("T").SetCode("ch-1").SetDomain("ch-1.com").SetStatus("active").
		Save(ctx)
	require.NoError(t, err)

	creator, err := client.User.Create().
		SetUsername("creator-ch").SetEmail("creator-ch@test.com").SetPasswordHash("x").
		SetName("发起人").SetTenantID(tenant.ID).SetActive(true).
		Save(ctx)
	require.NoError(t, err)
	workItem, err := client.Ticket.Create().SetTitle("测试变更").SetStatus("draft").SetPriority("medium").SetRecordClass("change_request").SetTicketNumber("TKT-CHANGE-HANDLER").SetRequesterID(creator.ID).SetTenantID(tenant.ID).Save(ctx)
	require.NoError(t, err)

	changeEntity, err := client.Change.Create().
		SetWorkItemID(workItem.ID).
		Save(ctx)
	require.NoError(t, err)

	logger := zaptest.NewLogger(t).Sugar()
	handler := NewChangeServiceTaskHandler(client, logger)
	handler.SetChangeService(changehandler.NewService(nil, client, logger))
	return client, handler, tenant.ID, changeEntity
}

func requireHandlerChangeWorkItem(t *testing.T, client *ent.Client, entity *ent.Change) *ent.Ticket {
	t.Helper()
	workItem, err := client.Ticket.Get(context.Background(), entity.WorkItemID)
	require.NoError(t, err)
	return workItem
}

func TestChangeServiceTaskHandler_CreateChangeRequiresDurableApplication(t *testing.T) {
	for _, missing := range []string{"identity", "application"} {
		t.Run(missing, func(t *testing.T) {
			client, handler, tenantID, _ := setupChangeHandlerFixture(t)
			ctx := context.WithValue(context.Background(), BPMNTenantIDContextKey, tenantID)
			if missing == "application" {
				ctx = WithBPMNCallbackExecutionKey(ctx, "claimed-creation")
			}
			result, err := handler.Execute(ctx, nil, map[string]interface{}{"action": "create_change", "title": "Change"})
			require.NoError(t, err)
			require.Equal(t, CallbackEffectBlocked, result.Status)
			require.Equal(t, CallbackBlockHandlerContract, result.BlockCode)
			require.Nil(t, result.CreationResult)
			require.Empty(t, result.OutputVars)
			if missing == "identity" {
				require.Contains(t, result.Message, "durable callback identity")
			} else {
				require.Contains(t, result.Message, "application or actor directory is unavailable")
			}
			require.Equal(t, 1, client.Ticket.Query().CountX(ctx))
			require.Equal(t, 1, client.Change.Query().CountX(ctx))
			require.Zero(t, client.IntakeRequest.Query().CountX(ctx))
			require.Zero(t, client.OutboxEvent.Query().CountX(ctx))
			require.Zero(t, client.AuditLog.Query().CountX(ctx))
		})
	}
}

func TestChangeServiceTaskHandler_UnchangedUpdateIsIdempotent(t *testing.T) {
	_, handler, tenantID, changeEntity := setupChangeHandlerFixture(t)
	ctx := context.WithValue(context.Background(), BPMNTenantIDContextKey, tenantID)

	effect, err := handler.Execute(ctx, nil, map[string]interface{}{
		"action":    "update_change",
		"change_id": changeEntity.ID,
		"title":     "测试变更",
	})
	require.NoError(t, err)
	require.Equal(t, CallbackEffectIdempotent, effect.Status)
}

func TestChangeServiceTaskHandler_UpdateChangeBlocksStatusBypass(t *testing.T) {
	client, handler, tenantID, changeEntity := setupChangeHandlerFixture(t)
	ctx := context.WithValue(context.Background(), BPMNTenantIDContextKey, tenantID)

	effect, err := handler.Execute(ctx, nil, map[string]interface{}{
		"action":    "update_change",
		"change_id": changeEntity.ID,
		"status":    "completed",
	})
	require.NoError(t, err)
	require.Equal(t, CallbackEffectBlocked, effect.Status)
	require.Equal(t, CallbackBlockHandlerContract, effect.BlockCode)

	after := requireHandlerChangeWorkItem(t, client, changeEntity)
	require.Equal(t, "draft", after.Status)
}

func TestChangeServiceTaskHandler_UpdateChangeCASLoserClassifiesExactEffect(t *testing.T) {
	tests := []struct {
		name        string
		winnerTitle string
		wantStatus  CallbackEffectStatus
	}{
		{name: "same payload is idempotent", winnerTitle: "CAS title", wantStatus: CallbackEffectIdempotent},
		{name: "different payload is blocked", winnerTitle: "conflicting title", wantStatus: CallbackEffectBlocked},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			client, handler, tenantID, changeEntity := setupChangeHandlerFixture(t)
			ctx := context.WithValue(context.Background(), BPMNTenantIDContextKey, tenantID)
			var injected atomic.Bool
			client.Use(func(next ent.Mutator) ent.Mutator {
				return ent.MutateFunc(func(mutationCtx context.Context, mutation ent.Mutation) (ent.Value, error) {
					if ticketMutation, ok := mutation.(*ent.TicketMutation); ok {
						if _, exists := ticketMutation.Title(); exists && injected.CompareAndSwap(false, true) {
							_, injectErr := client.Ticket.UpdateOneID(changeEntity.WorkItemID).SetTitle(tc.winnerTitle).AddVersion(1).Save(mutationCtx)
							require.NoError(t, injectErr)
						}
					}
					return next.Mutate(mutationCtx, mutation)
				})
			})

			effect, err := handler.Execute(ctx, nil, map[string]interface{}{
				"action": "update_change", "change_id": changeEntity.ID, "title": "CAS title",
			})
			require.NoError(t, err)
			require.Equal(t, tc.wantStatus, effect.Status)
		})
	}
}

func TestChangeServiceTaskHandler_UnknownActionBlocks(t *testing.T) {
	_, handler, tenantID, _ := setupChangeHandlerFixture(t)
	ctx := context.WithValue(context.Background(), BPMNTenantIDContextKey, tenantID)

	effect, err := handler.Execute(ctx, nil, map[string]interface{}{"action": "invented_action"})
	require.NoError(t, err)
	require.Equal(t, CallbackEffectBlocked, effect.Status)
	require.Equal(t, CallbackBlockHandlerContract, effect.BlockCode)
}

func TestChangeServiceTaskHandler_NotifyStakeholdersWithoutDurableDeliveryBlocks(t *testing.T) {
	_, handler, tenantID, changeEntity := setupChangeHandlerFixture(t)
	ctx := context.WithValue(context.Background(), BPMNTenantIDContextKey, tenantID)

	result, err := handler.Execute(ctx, nil, map[string]interface{}{
		"action": "notify_stakeholders", "change_id": changeEntity.ID,
	})
	require.NoError(t, err)
	assert.Equal(t, CallbackEffectBlocked, result.Status)
	assert.Equal(t, CallbackBlockHandlerContract, result.BlockCode)
}

// Lifecycle success is exercised through the actual restricted PostgreSQL
// worker and default definitions in tests/integration/workitem_change_callback_postgres_test.go.
func TestChangeLifecycleRequiresDurableIdentity(t *testing.T) {
	for _, action := range []string{"assess_risk", "approve_change", "authorize_change", "reject_change", "schedule_change", "implement_change", "verify_change", "review_change", "close_change", "cancel_change"} {
		t.Run(action, func(t *testing.T) {
			client, handler, tenantID, c := setupChangeHandlerFixture(t)
			ctx := context.WithValue(context.Background(), BPMNTenantIDContextKey, tenantID)
			before := requireHandlerChangeWorkItem(t, client, c)
			effect, err := handler.Execute(ctx, nil, map[string]interface{}{"action": action, "change_id": c.ID, "actor_id": 999, "version": before.Version})
			require.NoError(t, err)
			require.Equal(t, CallbackEffectBlocked, effect.Status)
			require.Contains(t, effect.Message, "durable callback identity")
			after := requireHandlerChangeWorkItem(t, client, c)
			require.Equal(t, before.Version, after.Version)
			require.Equal(t, before.Status, after.Status)
			require.Zero(t, client.AuditLog.Query().CountX(ctx))
		})
	}
}

func TestChangeMetadataCallbackPreservesTenantBoundary(t *testing.T) {
	for _, scope := range []string{"own", "foreign", "missing"} {
		t.Run(scope, func(t *testing.T) {
			client, handler, tenantID, c := setupChangeHandlerFixture(t)
			ctx := context.Background()
			if scope == "own" {
				ctx = context.WithValue(ctx, BPMNTenantIDContextKey, tenantID)
			}
			if scope == "foreign" {
				ctx = context.WithValue(ctx, BPMNTenantIDContextKey, tenantID+999)
			}
			_, err := handler.Execute(ctx, nil, map[string]interface{}{"action": "update_change", "change_id": c.ID, "title": "Scoped metadata"})
			if scope == "own" {
				require.NoError(t, err)
				require.Equal(t, "Scoped metadata", requireHandlerChangeWorkItem(t, client, c).Title)
			} else {
				require.Error(t, err)
				require.NotEqual(t, "Scoped metadata", requireHandlerChangeWorkItem(t, client, c).Title)
			}
		})
	}
}
