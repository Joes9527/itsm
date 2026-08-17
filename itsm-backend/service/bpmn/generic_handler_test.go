package bpmn

import (
	"context"
	"testing"

	"itsm-backend/ent"
	"itsm-backend/ent/enttest"
	"itsm-backend/ent/ticketnotification"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"go.uber.org/zap/zaptest"
)

func setupGenericHandlerFixture(t *testing.T) (*ent.Client, *GenericServiceTaskHandler, int, *ent.Ticket) {
	client := enttest.Open(t, "sqlite3", "file:generic_handler_test?mode=memory&cache=shared&_fk=1")
	t.Cleanup(func() { client.Close() })
	ctx := context.Background()

	tenant, err := client.Tenant.Create().
		SetName("T").SetCode("gh-1").SetDomain("gh-1.com").SetStatus("active").
		Save(ctx)
	require.NoError(t, err)

	requester, err := client.User.Create().
		SetUsername("requester-gh").SetEmail("requester-gh@test.com").SetPasswordHash("x").
		SetName("申请人").SetTenantID(tenant.ID).SetActive(true).
		Save(ctx)
	require.NoError(t, err)

	tkt, err := client.Ticket.Create().
		SetTitle("生成通用handler测试工单").SetTicketNumber("T-GH-1").SetStatus("open").
		SetRequesterID(requester.ID).SetTenantID(tenant.ID).
		Save(ctx)
	require.NoError(t, err)

	handler := NewGenericServiceTaskHandler(client, zaptest.NewLogger(t).Sugar())
	return client, handler, tenant.ID, tkt
}

func TestGenericServiceTaskHandler_CompleteService_ResolvesTicket(t *testing.T) {
	client, handler, tenantID, tkt := setupGenericHandlerFixture(t)
	ctx := context.WithValue(context.Background(), BPMNTenantIDContextKey, tenantID)

	result, err := handler.Execute(ctx, nil, map[string]interface{}{
		"action":      "complete_service",
		"business_id": float64(tkt.ID),
	})
	require.NoError(t, err)
	assert.True(t, result.Success)

	updated, err := client.Ticket.Get(ctx, tkt.ID)
	require.NoError(t, err)
	assert.Equal(t, "resolved", updated.Status)
	assert.NotNil(t, updated.ResolvedAt)
}

func TestGenericServiceTaskHandler_NotifyRejection_CreatesNotificationForRequester(t *testing.T) {
	client, handler, tenantID, tkt := setupGenericHandlerFixture(t)
	ctx := context.WithValue(context.Background(), BPMNTenantIDContextKey, tenantID)

	result, err := handler.Execute(ctx, nil, map[string]interface{}{
		"action":      "notify_rejection",
		"business_id": float64(tkt.ID),
	})
	require.NoError(t, err)
	assert.True(t, result.Success)

	count, err := client.TicketNotification.Query().
		Where(ticketnotification.TicketID(tkt.ID), ticketnotification.UserID(tkt.RequesterID)).
		Count(ctx)
	require.NoError(t, err)
	assert.Equal(t, 1, count, "应该给申请人真实创建一条驳回通知，不是只打日志")
}

func TestGenericServiceTaskHandler_Notify_CreatesNotificationForRequester(t *testing.T) {
	client, handler, tenantID, tkt := setupGenericHandlerFixture(t)
	ctx := context.WithValue(context.Background(), BPMNTenantIDContextKey, tenantID)

	_, err := handler.Execute(ctx, nil, map[string]interface{}{
		"action":      "notify",
		"business_id": float64(tkt.ID),
	})
	require.NoError(t, err)

	count, err := client.TicketNotification.Query().
		Where(ticketnotification.TicketID(tkt.ID)).
		Count(ctx)
	require.NoError(t, err)
	assert.Equal(t, 1, count)
}

func TestGenericServiceTaskHandler_UnknownAction_KeepsPassthroughBehavior(t *testing.T) {
	_, handler, tenantID, _ := setupGenericHandlerFixture(t)
	ctx := context.WithValue(context.Background(), BPMNTenantIDContextKey, tenantID)

	result, err := handler.Execute(ctx, nil, map[string]interface{}{
		"action": "some_future_custom_action",
		"foo":    "bar",
	})
	require.NoError(t, err)
	assert.True(t, result.Success)
	assert.Equal(t, "bar", result.OutputVars["foo"], "未识别的 action 应该保留原有透传行为，不破坏自定义模板")
}

func TestGenericServiceTaskHandler_MissingBusinessID_ReturnsError(t *testing.T) {
	_, handler, tenantID, _ := setupGenericHandlerFixture(t)
	ctx := context.WithValue(context.Background(), BPMNTenantIDContextKey, tenantID)

	_, err := handler.Execute(ctx, nil, map[string]interface{}{"action": "complete_service"})
	assert.Error(t, err)
}
