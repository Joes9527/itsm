//go:build integration_postgres

package integration

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
	"go.uber.org/zap"
	"itsm-backend/common/tenantctx"
	"itsm-backend/ent"
	"itsm-backend/service"
)

type notificationRuntimeGraph struct {
	t        *testing.T
	tenantID int
	calls    int
	err      error
	body     string
}

func (g *notificationRuntimeGraph) SendMail(ctx context.Context, _ string, _ string, _ string, body string, deliveryID string) error {
	g.calls++
	tenant, ok := tenantctx.TenantID(ctx)
	require.True(g.t, ok)
	require.Equal(g.t, g.tenantID, tenant)
	require.False(g.t, tenantctx.IsSystemBypass(ctx))
	require.NotEmpty(g.t, deliveryID)
	g.body = body
	return g.err
}
func TestPostgresTicketNotificationRuntimeUsesQueueAndTenantCapabilities(t *testing.T) {
	f := newIncidentEffectsFixture(t)
	runtime, _ := runtimeClients(t, f)
	graph := &notificationRuntimeGraph{t: t, tenantID: f.tenant.ID}
	email := service.NewEmailService(service.EmailConfig{}, zap.NewNop().Sugar())
	email.SetGraphProvider(func(tenantID int) (service.GraphMailSender, string, bool) {
		require.Equal(t, f.tenant.ID, tenantID)
		return graph, "support@example.test", true
	})
	owner := service.NewTicketNotificationService(runtime.Tenant, zap.NewNop().Sugar())
	owner.SetDeliveryQueueClient(runtime.System)
	owner.SetEmailService(email)
	enqueue := func(key string) *ent.TicketNotification {
		return f.client.TicketNotification.Create().SetTenantID(f.tenant.ID).SetTicketID(f.inc.WorkItemID).SetUserID(f.actor.ID).SetType("ticket_created").SetChannel("email").SetContent("Original frozen notification").SetDeliveryKey(key).SaveX(f.ctx)
	}
	first := enqueue("creation-one")
	count, err := owner.ProcessPendingDeliveries(tenantctx.WithSystemBypass(f.ctx), "notification-pg", 10)
	require.NoError(t, err)
	require.Equal(t, 1, count)
	require.Equal(t, 1, graph.calls)
	require.Equal(t, "sent", f.client.TicketNotification.GetX(f.ctx, first.ID).Status)
	require.Equal(t, "Original frozen notification", graph.body)
	unknown := enqueue("creation-unknown")
	graph.err = errors.New("provider accepted but disconnected")
	count, err = owner.ProcessPendingDeliveries(f.ctx, "notification-pg", 10)
	require.Error(t, err)
	require.Zero(t, count)
	row := f.client.TicketNotification.GetX(f.ctx, unknown.ID)
	require.Equal(t, "failed", row.Status)
	require.Equal(t, "delivery_unknown", row.LastErrorClass)
	stale := enqueue("creation-crashed")
	f.client.TicketNotification.UpdateOneID(stale.ID).SetStatus("processing").SetAttemptCount(1).SetLeaseOwner("lost-worker").SetLeaseExpiresAt(time.Now().Add(-time.Minute)).ExecX(f.ctx)
	count, err = owner.ProcessPendingDeliveries(f.ctx, "notification-pg", 10)
	require.Error(t, err)
	require.Zero(t, count)
	require.Equal(t, 2, graph.calls)
	_, err = runtime.System.TicketNotification.Create().SetTenantID(f.tenant.ID).SetTicketID(f.inc.WorkItemID).SetUserID(f.actor.ID).SetType("forbidden").SetContent("forbidden").Save(f.ctx)
	require.ErrorContains(t, err, "permission denied")
	_, err = runtime.System.Ticket.UpdateOneID(f.inc.WorkItemID).SetTitle("forbidden").Save(f.ctx)
	require.ErrorContains(t, err, "permission denied")
	_, err = runtime.System.User.UpdateOneID(f.actor.ID).SetName("forbidden").Save(f.ctx)
	require.ErrorContains(t, err, "permission denied")
}
