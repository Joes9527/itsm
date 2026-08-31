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

func TestGenericServiceTaskHandler_RetryUsesStableExecutionKey(t *testing.T) {
	client, handler, tenantID, tkt := setupGenericHandlerFixture(t)
	ctx := context.WithValue(context.Background(), BPMNTenantIDContextKey, tenantID)
	ctx = WithBPMNCallbackExecutionKey(ctx, "generic-notification-retry-key")
	variables := map[string]interface{}{
		"action": "notify_rejection", "business_id": tkt.ID, "reject_reason": "not approved",
	}

	_, err := handler.Execute(ctx, nil, variables)
	require.NoError(t, err)
	_, err = handler.Execute(ctx, nil, variables)
	require.NoError(t, err)

	count, err := client.TicketNotification.Query().
		Where(ticketnotification.TicketID(tkt.ID), ticketnotification.UserID(tkt.RequesterID)).
		Count(ctx)
	require.NoError(t, err)
	assert.Equal(t, 1, count)
}

func TestGenericServiceTaskHandler_UnknownActionFailsClosed(t *testing.T) {
	_, handler, tenantID, _ := setupGenericHandlerFixture(t)
	ctx := context.WithValue(context.Background(), BPMNTenantIDContextKey, tenantID)

	result, err := handler.Execute(ctx, nil, map[string]interface{}{
		"action": "some_future_custom_action",
		"foo":    "bar",
	})
	require.Error(t, err)
	assert.Nil(t, result)
}

func TestGenericServiceTaskHandler_MissingBusinessID_ReturnsError(t *testing.T) {
	_, handler, tenantID, _ := setupGenericHandlerFixture(t)
	ctx := context.WithValue(context.Background(), BPMNTenantIDContextKey, tenantID)

	_, err := handler.Execute(ctx, nil, map[string]interface{}{"action": "complete_service"})
	assert.Error(t, err)
}

// TestGenericServiceTaskHandler_Notify_CrossTenantTicket_DoesNotLeak 是 Finding 2 的核心回归：
// notifyRequester 以前用不带租户过滤的 Ticket.Get 取工单，另一个租户的工单会被读出来
// 并把标题/编号写进一条持久化通知里。现在必须查不到 → 干净跳过，绝不落库。
func TestGenericServiceTaskHandler_Notify_CrossTenantTicket_DoesNotLeak(t *testing.T) {
	client, handler, tenantID, tkt := setupGenericHandlerFixture(t)
	ctx := context.Background()

	otherTenant, err := client.Tenant.Create().
		SetName("T2").SetCode("gh-2").SetDomain("gh-2.com").SetStatus("active").
		Save(ctx)
	require.NoError(t, err)
	require.NotEqual(t, tenantID, otherTenant.ID)

	// 以"另一个租户"的身份去通知租户 1 的工单
	otherCtx := context.WithValue(ctx, BPMNTenantIDContextKey, otherTenant.ID)
	result, err := handler.Execute(otherCtx, nil, map[string]interface{}{
		"action":        "notify",
		"business_type": "ticket",
		"business_id":   float64(tkt.ID),
	})
	require.NoError(t, err, "跨租户查不到工单属于空态，应该干净跳过而不是让流程卡死")
	require.NotNil(t, result)
	assert.True(t, result.Success)

	count, err := client.TicketNotification.Query().
		Where(ticketnotification.TicketID(tkt.ID)).
		Count(ctx)
	require.NoError(t, err)
	assert.Zero(t, count, "绝不能把别的租户的工单内容写进通知")

	notifCount, err := client.Notification.Query().Count(ctx)
	require.NoError(t, err)
	assert.Zero(t, notifCount, "统一通知同样不得跨租户落库")
}

// TestGenericServiceTaskHandler_Notify_NonTicketBusinessType_IsNoOp：
// incident_emergency_flow 的 Activity_Notify 同样声明 generic_task/notify，
// 但它是以 business_type=incident、business_id=<事件ID> 触发的——事件 ID 和工单 ID
// 是两个完全不同的 ID 空间，绝不能拿事件 ID 去查工单。
func TestGenericServiceTaskHandler_Notify_NonTicketBusinessType_IsNoOp(t *testing.T) {
	client, handler, tenantID, tkt := setupGenericHandlerFixture(t)
	ctx := context.WithValue(context.Background(), BPMNTenantIDContextKey, tenantID)

	result, err := handler.Execute(ctx, nil, map[string]interface{}{
		"action":        "notify",
		"business_type": "incident",
		"business_id":   float64(tkt.ID), // 故意撞上一个真实存在的工单 ID
	})
	require.NoError(t, err, "非工单业务类型应该跳过，不能让 incident 流程卡在通知节点上")
	assert.True(t, result.Success)

	count, err := client.TicketNotification.Query().
		Where(ticketnotification.TicketID(tkt.ID)).
		Count(ctx)
	require.NoError(t, err)
	assert.Zero(t, count, "business_type=incident 时不得给同号工单发通知")
}
