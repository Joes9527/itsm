package service

import (
	"context"
	"testing"

	_ "github.com/mattn/go-sqlite3"

	"itsm-backend/common/tenantctx"
	"itsm-backend/dto"
	"itsm-backend/ent"
	"itsm-backend/ent/enttest"
	"itsm-backend/ent/ticketnotification"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"go.uber.org/zap/zaptest"
)

// graphSenderSpy 记录 Graph sendMail 调用（用于验证 email 渠道）。
type graphSenderSpy struct {
	calls []string // 收件人列表
}

func (g *graphSenderSpy) SendMail(_ context.Context, _ string, to, _, _, _ string) error {
	g.calls = append(g.calls, to)
	return nil
}

func setupTicketNotificationTest(t *testing.T) (*ent.Client, *TicketNotificationService, context.Context) {
	client := enttest.Open(t, "sqlite3", testDSN())
	logger := zaptest.NewLogger(t).Sugar()
	svc := NewTicketNotificationService(client, logger, standardNotificationPolicy(t))
	return client, svc, context.Background()
}

func createNotifTestData(t *testing.T, client *ent.Client, ctx context.Context) (*ent.Tenant, *ent.User, *ent.Ticket) {
	t.Helper()
	tenant, err := client.Tenant.Create().
		SetName("T").SetCode("t").SetDomain("t.com").SetStatus("active").
		Save(ctx)
	require.NoError(t, err)

	user, err := client.User.Create().
		SetUsername("enduser").SetEmail("enduser@example.com").SetName("End User").
		SetPasswordHash("x").SetRole("end_user").SetActive(true).SetTenantID(tenant.ID).
		Save(ctx)
	require.NoError(t, err)

	ticket, err := client.Ticket.Create().
		SetTitle("Test").SetDescription("d").SetPriority("medium").
		SetStatus("open").SetTicketNumber("TKT-TEST-001").SetTenantID(tenant.ID).SetRequesterID(user.ID).
		Save(ctx)
	require.NoError(t, err)

	return tenant, user, ticket
}

func TestSendNotification_MultiChannelRouting(t *testing.T) {
	client, svc, ctx := setupTicketNotificationTest(t)
	defer client.Close()

	tenant, user, ticket := createNotifTestData(t, client, ctx)
	ctx = tenantctx.WithTenantID(ctx, tenant.ID)

	// 偏好：comment_added 启用 email + in_app，禁用 sms + push
	_, err := client.NotificationPreference.Create().
		SetUserID(user.ID).SetTenantID(tenant.ID).SetEventType("comment_added").
		SetEmailEnabled(true).SetInAppEnabled(true).SetSmsEnabled(false).SetPushEnabled(false).
		Save(ctx)
	require.NoError(t, err)

	// 注入偏好服务 + email 服务（Graph spy）
	svc.SetNotificationPreferenceService(NewNotificationPreferenceService(client, zaptest.NewLogger(t).Sugar()))
	_, calls := configureNotificationSMTPProbe(svc)

	_, err = svc.SendNotification(ctx, ticket.ID, &dto.SendTicketNotificationRequest{
		UserIDs:   []int{user.ID},
		EventType: "comment_added",
		Content:   "new comment",
	}, tenant.ID)
	require.NoError(t, err)

	// Request persists the intent; only the real worker invokes the local spy.
	require.Empty(t, *calls)
	pending := client.TicketNotification.Query().Where(ticketnotification.ChannelEQ("email")).OnlyX(ctx)
	require.Equal(t, "pending", pending.Status)
	require.True(t, pending.SentAt.IsZero())
	svc.SetDeliveryQueueClient(client)
	n, err := svc.ProcessPendingDeliveries(ctx, "direct-notification-test", 10)
	require.NoError(t, err)
	require.Equal(t, 1, n)
	require.Len(t, *calls, 1)
	assert.Equal(t, "enduser@example.com", (*calls)[0])

	// in_app 渠道创建 1 条站内通知记录
	cnt, err := client.TicketNotification.Query().
		Where(ticketnotification.UserID(user.ID), ticketnotification.Channel("in_app")).
		Count(ctx)
	require.NoError(t, err)
	assert.Equal(t, 1, cnt, "应创建 1 条站内通知记录")
}

func TestSendNotification_InAppDisabledNoRecord(t *testing.T) {
	client, svc, ctx := setupTicketNotificationTest(t)
	defer client.Close()

	tenant, user, ticket := createNotifTestData(t, client, ctx)
	ctx = tenantctx.WithTenantID(ctx, tenant.ID)

	// 偏好：全部禁用
	_, err := client.NotificationPreference.Create().
		SetUserID(user.ID).SetTenantID(tenant.ID).SetEventType("comment_added").
		SetEmailEnabled(false).SetInAppEnabled(false).SetSmsEnabled(false).SetPushEnabled(false).
		Save(ctx)
	require.NoError(t, err)

	svc.SetNotificationPreferenceService(NewNotificationPreferenceService(client, zaptest.NewLogger(t).Sugar()))

	_, _ = svc.SendNotification(ctx, ticket.ID, &dto.SendTicketNotificationRequest{
		UserIDs:   []int{user.ID},
		EventType: "comment_added",
		Content:   "x",
	}, tenant.ID)

	cnt, _ := client.TicketNotification.Query().Count(ctx)
	assert.Equal(t, 0, cnt, "偏好全禁用时不应创建站内记录")
}

func TestSendNotification_ZeroRecipientsReturnsBlockedEvidence(t *testing.T) {
	client, svc, ctx := setupTicketNotificationTest(t)
	defer client.Close()
	tenant, _, ticket := createNotifTestData(t, client, ctx)
	ctx = tenantctx.WithTenantID(ctx, tenant.ID)

	result, err := svc.SendNotification(ctx, ticket.ID, &dto.SendTicketNotificationRequest{
		EventType: "ticket_updated",
		Content:   "no recipients",
	}, tenant.ID)
	require.NoError(t, err)
	require.Equal(t, dto.TicketNotificationEffectBlocked, result.Effect)
	require.Equal(t, "recipient_empty", result.BlockCode)
	require.Zero(t, client.TicketNotification.Query().CountX(ctx))
}

func TestSendNotification_DefaultPreferenceWhenNoRecord(t *testing.T) {
	client, svc, ctx := setupTicketNotificationTest(t)
	defer client.Close()

	tenant, user, ticket := createNotifTestData(t, client, ctx)
	ctx = tenantctx.WithTenantID(ctx, tenant.ID)

	// 不创建偏好记录 → 走默认偏好（email+in_app）
	svc.SetNotificationPreferenceService(NewNotificationPreferenceService(client, zaptest.NewLogger(t).Sugar()))
	configureNotificationSMTPProbe(svc)

	result, err := svc.SendNotification(ctx, ticket.ID, &dto.SendTicketNotificationRequest{
		UserIDs:   []int{user.ID},
		EventType: "comment_added",
		Content:   "x",
	}, tenant.ID)
	require.NoError(t, err)
	require.Equal(t, dto.TicketNotificationEffectQueued, result.Effect)
	require.Equal(t, 1, result.QueuedCount)
	require.Equal(t, 1, result.AppliedCount)

	cnt, err := client.TicketNotification.Query().Count(ctx)
	require.NoError(t, err)
	assert.Equal(t, 2, cnt, "默认偏好创建站内记录及邮件意图")
}
