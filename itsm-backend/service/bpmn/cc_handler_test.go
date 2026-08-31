package bpmn

import (
	"context"
	"database/sql"
	"encoding/json"
	"strconv"
	"testing"
	"time"

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
	const dsn = "file:cc_handler_retry?mode=memory&cache=shared&_fk=1"
	client := enttest.Open(t, "sqlite3", dsn)
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

	first, err := handler.Execute(callbackCtx, nil, variables)
	require.NoError(t, err)
	second, err := handler.Execute(callbackCtx, nil, variables)
	require.NoError(t, err)

	assert.Equal(t, 1, client.TicketCC.Query().CountX(ctx))
	assert.Equal(t, 1, client.TicketNotification.Query().CountX(ctx))
	assert.Equal(t, 1, client.Notification.Query().CountX(ctx))
	assert.Equal(t, []int{recipient.ID}, first.OutputVars["added_cc_users"])
	assert.Empty(t, second.OutputVars["added_cc_users"])

	db, err := sql.Open("sqlite3", dsn)
	require.NoError(t, err)
	t.Cleanup(func() { _ = db.Close() })
	var storedKey sql.NullString
	require.NoError(t, db.QueryRowContext(ctx, "SELECT delivery_key FROM ticket_ccs").Scan(&storedKey))
	require.True(t, storedKey.Valid)
	assert.Equal(t, "cc-retry-key", storedKey.String)

	row := client.TicketCC.Query().OnlyX(ctx)
	encoded, err := json.Marshal(row)
	require.NoError(t, err)
	assert.NotContains(t, string(encoded), "deliveryKey")
	assert.NotContains(t, string(encoded), "delivery_key")
}

func TestCCTaskHandler_RequiresCallbackExecutionKey(t *testing.T) {
	client := enttest.Open(t, "sqlite3", "file:cc_handler_requires_key?mode=memory&cache=shared&_fk=1")
	t.Cleanup(func() { _ = client.Close() })
	ctx := context.Background()
	tenant := client.Tenant.Create().SetName("T").SetCode("cc-key").SetDomain("cc-key.test").SetStatus("active").SaveX(ctx)
	requester := client.User.Create().SetUsername("cc-key-owner").SetEmail("cc-key-owner@test.invalid").SetPasswordHash("x").SetName("Owner").SetTenantID(tenant.ID).SetActive(true).SaveX(ctx)
	recipient := client.User.Create().SetUsername("cc-key-recipient").SetEmail("cc-key-recipient@test.invalid").SetPasswordHash("x").SetName("Recipient").SetTenantID(tenant.ID).SetActive(true).SaveX(ctx)
	ticket := client.Ticket.Create().SetTitle("CC key test").SetTicketNumber("CC-KEY").SetStatus("open").SetRequesterID(requester.ID).SetTenantID(tenant.ID).SaveX(ctx)
	handler := NewCCTaskHandler(client, zap.NewNop().Sugar())
	callbackCtx := context.WithValue(ctx, BPMNTenantIDContextKey, tenant.ID)

	_, err := handler.Execute(callbackCtx, nil, map[string]interface{}{
		"ticket_id": ticket.ID,
		"ccType":    "user",
		"ccUserIds": strconv.Itoa(recipient.ID),
		"ccNotify":  true,
	})

	require.EqualError(t, err, "抄送回调执行键不能为空")
	assert.Zero(t, client.TicketCC.Query().CountX(ctx))
	assert.Zero(t, client.TicketNotification.Query().CountX(ctx))
	assert.Zero(t, client.Notification.Query().CountX(ctx))
}

func TestCCTaskHandler_DifferentDeliveryReusesActiveOrdinaryRelation(t *testing.T) {
	const dsn = "file:cc_handler_active_ordinary?mode=memory&cache=shared&_fk=1"
	client := enttest.Open(t, "sqlite3", dsn)
	t.Cleanup(func() { _ = client.Close() })
	ctx := context.Background()
	tenant := client.Tenant.Create().SetName("T").SetCode("cc-ordinary").SetDomain("cc-ordinary.test").SetStatus("active").SaveX(ctx)
	requester := client.User.Create().SetUsername("cc-ordinary-owner").SetEmail("cc-ordinary-owner@test.invalid").SetPasswordHash("x").SetName("Owner").SetTenantID(tenant.ID).SetActive(true).SaveX(ctx)
	recipient := client.User.Create().SetUsername("cc-ordinary-recipient").SetEmail("cc-ordinary-recipient@test.invalid").SetPasswordHash("x").SetName("Recipient").SetTenantID(tenant.ID).SetActive(true).SaveX(ctx)
	ticket := client.Ticket.Create().SetTitle("CC ordinary test").SetTicketNumber("CC-ORDINARY").SetStatus("open").SetRequesterID(requester.ID).SetTenantID(tenant.ID).SaveX(ctx)
	ordinary := client.TicketCC.Create().
		SetTicketID(ticket.ID).
		SetUserID(recipient.ID).
		SetAddedBy(requester.ID).
		SetTenantID(tenant.ID).
		SetAddedAt(time.Now()).
		SetIsActive(true).
		SaveX(ctx)
	handler := NewCCTaskHandler(client, zap.NewNop().Sugar())
	callbackCtx := context.WithValue(ctx, BPMNTenantIDContextKey, tenant.ID)
	callbackCtx = WithBPMNCallbackExecutionKey(callbackCtx, "cc-different-delivery-key")

	result, err := handler.Execute(callbackCtx, nil, map[string]interface{}{
		"ticket_id": ticket.ID,
		"ccType":    "user",
		"ccUserIds": strconv.Itoa(recipient.ID),
		"ccNotify":  true,
	})

	require.NoError(t, err)
	assert.Empty(t, result.OutputVars["added_cc_users"])
	assert.Equal(t, 1, client.TicketCC.Query().CountX(ctx))
	assert.Zero(t, client.TicketNotification.Query().CountX(ctx))
	assert.Zero(t, client.Notification.Query().CountX(ctx))

	db, err := sql.Open("sqlite3", dsn)
	require.NoError(t, err)
	t.Cleanup(func() { _ = db.Close() })
	var storedKey sql.NullString
	require.NoError(t, db.QueryRowContext(ctx, "SELECT delivery_key FROM ticket_ccs WHERE id = ?", ordinary.ID).Scan(&storedKey))
	assert.False(t, storedKey.Valid)
}
