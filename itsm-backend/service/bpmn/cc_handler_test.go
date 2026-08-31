package bpmn

import (
	"context"
	"strconv"
	"testing"

	"itsm-backend/ent/enttest"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"go.uber.org/zap"
)

func TestCCTaskHandler_UsesContextTenantAndRejectsCrossTenantRecipients(t *testing.T) {
	client := enttest.Open(t, "sqlite3", "file:cc_handler_tenant?mode=memory&cache=shared&_fk=1")
	t.Cleanup(func() { _ = client.Close() })
	ctx := context.Background()
	tenantA := client.Tenant.Create().SetName("A").SetCode("cc-a").SetDomain("cc-a.test").SetStatus("active").SaveX(ctx)
	tenantB := client.Tenant.Create().SetName("B").SetCode("cc-b").SetDomain("cc-b.test").SetStatus("active").SaveX(ctx)
	requester := client.User.Create().SetUsername("cc-requester").SetEmail("cc-requester@test.invalid").SetPasswordHash("x").SetName("Requester").SetTenantID(tenantA.ID).SetActive(true).SaveX(ctx)
	crossTenantUser := client.User.Create().SetUsername("cc-cross").SetEmail("cc-cross@test.invalid").SetPasswordHash("x").SetName("Cross").SetTenantID(tenantB.ID).SetActive(true).SaveX(ctx)
	ticket := client.Ticket.Create().SetTitle("CC tenant test").SetTicketNumber("CC-1").SetStatus("open").SetRequesterID(requester.ID).SetTenantID(tenantA.ID).SaveX(ctx)
	handler := NewCCTaskHandler(client, zap.NewNop().Sugar())
	callbackCtx := context.WithValue(ctx, BPMNTenantIDContextKey, tenantA.ID)
	callbackCtx = WithBPMNCallbackExecutionKey(callbackCtx, "cc-cross-tenant-key")

	_, err := handler.Execute(callbackCtx, nil, map[string]interface{}{
		"ticket_id": ticket.ID,
		"tenant_id": tenantB.ID,
		"ccType":    "user",
		"ccUserIds": strconv.Itoa(crossTenantUser.ID),
		"ccNotify":  true,
	})

	require.Error(t, err)
	assert.Zero(t, client.TicketCC.Query().CountX(ctx))
	assert.Zero(t, client.TicketNotification.Query().CountX(ctx))
	assert.Zero(t, client.Notification.Query().CountX(ctx))
}

func TestCCTaskHandler_RetryIsAtomicAndIdempotent(t *testing.T) {
	client := enttest.Open(t, "sqlite3", "file:cc_handler_retry?mode=memory&cache=shared&_fk=1")
	t.Cleanup(func() { _ = client.Close() })
	ctx := context.Background()
	tenant := client.Tenant.Create().SetName("T").SetCode("cc-retry").SetDomain("cc-retry.test").SetStatus("active").SaveX(ctx)
	requester := client.User.Create().SetUsername("cc-owner").SetEmail("cc-owner@test.invalid").SetPasswordHash("x").SetName("Owner").SetTenantID(tenant.ID).SetActive(true).SaveX(ctx)
	recipient := client.User.Create().SetUsername("cc-recipient").SetEmail("cc-recipient@test.invalid").SetPasswordHash("x").SetName("Recipient").SetTenantID(tenant.ID).SetActive(true).SaveX(ctx)
	ticket := client.Ticket.Create().SetTitle("CC retry test").SetTicketNumber("CC-2").SetStatus("open").SetRequesterID(requester.ID).SetTenantID(tenant.ID).SaveX(ctx)
	handler := NewCCTaskHandler(client, zap.NewNop().Sugar())
	callbackCtx := context.WithValue(ctx, BPMNTenantIDContextKey, tenant.ID)
	callbackCtx = WithBPMNCallbackExecutionKey(callbackCtx, "cc-retry-key")
	variables := map[string]interface{}{
		"ticket_id": ticket.ID,
		"ccType":    "user",
		"ccUserIds": strconv.Itoa(recipient.ID),
		"ccNotify":  true,
	}

	_, err := handler.Execute(callbackCtx, nil, variables)
	require.NoError(t, err)
	_, err = handler.Execute(callbackCtx, nil, variables)
	require.NoError(t, err)

	assert.Equal(t, 1, client.TicketCC.Query().CountX(ctx))
	assert.Equal(t, 1, client.TicketNotification.Query().CountX(ctx))
	assert.Equal(t, 1, client.Notification.Query().CountX(ctx))
}
