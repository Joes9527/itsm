package service

import (
	"context"
	"fmt"
	"testing"
	"time"

	_ "github.com/mattn/go-sqlite3"

	"itsm-backend/dto"
	"itsm-backend/ent/enttest"
	"itsm-backend/ent/ticketnotification"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"go.uber.org/zap/zaptest"
)

// TestNotifyTicketCreated_NoAssignee_OnlyNotifiesRequester 覆盖工单创建通知去广播兜底后的行为：
// 工单创建时还没有 assignee（现在是正常状态——分配改到审批通过后由 BPMN fulfillment 节点触发），
// 这种情况下只应该通知申请人本人，不应该把租户内其他用户也拉进收件人列表。
//
// 沿用 ticket_notification_multi_channel_test.go 里已有的测试风格：不 mock SendNotification，
// 直接用真实 DB 断言最终落到 ticket_notifications 表里的收件人行。
func TestNotifyTicketCreated_NoAssignee_OnlyNotifiesRequester(t *testing.T) {
	client, svc, ctx := setupTicketNotificationTest(t)
	defer client.Close()

	tenant, requester, ticket := createNotifTestData(t, client, ctx)

	// 另建 3 个跟这张工单毫无关系的用户，验证他们不会被拉进收件人列表——
	// 这是本次要修的 bug 本身：旧代码会把除申请人外的全部用户都拉进来广播。
	for i := 0; i < 3; i++ {
		_, err := client.User.Create().
			SetUsername(fmt.Sprintf("bystander_%d", i)).
			SetEmail(fmt.Sprintf("bystander_%d@example.com", i)).
			SetName(fmt.Sprintf("bystander_%d", i)).
			SetPasswordHash("hash").SetActive(true).SetTenantID(tenant.ID).
			Save(ctx)
		require.NoError(t, err)
	}

	// createNotifTestData 创建的 ticket 没有设置 AssigneeID，符合当前"创建时没有处理人"的正常状态。
	require.Zero(t, ticket.AssigneeID, "测试前提：工单创建时不应有 assignee")

	err := svc.NotifyTicketCreated(ctx, ticket)
	require.NoError(t, err)

	notifs, err := client.TicketNotification.Query().
		Where(ticketnotification.TicketID(ticket.ID)).
		All(ctx)
	require.NoError(t, err)
	require.Len(t, notifs, 1, "没有 assignee 时只应该通知申请人本人，不广播")
	assert.Equal(t, requester.ID, notifs[0].UserID)
}

// TestNotifyTicketCreated_WithAssignee_NotifiesAssigneeAndRequester 覆盖有 assignee 时的既有行为：
// 处理人和申请人都应收到通知（去重，不重复通知同一人）。
func TestNotifyTicketCreated_WithAssignee_NotifiesAssigneeAndRequester(t *testing.T) {
	client, svc, ctx := setupTicketNotificationTest(t)
	defer client.Close()

	_, requester, ticket := createNotifTestData(t, client, ctx)

	assignee, err := client.User.Create().
		SetUsername("assignee_u").SetEmail("assignee_u@example.com").SetName("Assignee").
		SetPasswordHash("hash").SetActive(true).SetTenantID(ticket.TenantID).
		Save(ctx)
	require.NoError(t, err)

	ticket, err = ticket.Update().SetAssigneeID(assignee.ID).Save(ctx)
	require.NoError(t, err)

	err = svc.NotifyTicketCreated(ctx, ticket)
	require.NoError(t, err)

	notifs, err := client.TicketNotification.Query().
		Where(ticketnotification.TicketID(ticket.ID)).
		All(ctx)
	require.NoError(t, err)
	require.Len(t, notifs, 2, "有 assignee 时应该同时通知处理人和申请人")

	gotUserIDs := []int{notifs[0].UserID, notifs[1].UserID}
	assert.ElementsMatch(t, []int{assignee.ID, requester.ID}, gotUserIDs)
}

// TestCreateTicket_NoAssignee_NotifiesRequesterAsync 回归覆盖 Task 5 的网关条件 bug：
// ticket_service.go 里 CreateTicket 触发异步通知的 if 条件曾经是
// `s.notificationSvc != nil && tkt.AssigneeID != nil`，导致创建时没有 assignee 的工单
// （BPMN 履约节点分配处理人前创建——现在是常态）永远不会调用 NotifyTicketCreated，
// 申请人收不到"工单已创建"通知，跟 NotifyTicketCreated 内部已经实现的
// "没有 assignee 时只通知申请人"兜底逻辑互相矛盾。
//
// 本测试特意走真实的 CreateTicket 路径（而不是像上面两个测试那样直接调用
// NotifyTicketCreated），这样才能覆盖到调用方的网关条件本身。CreateTicket 内部用
// go func() + context.Background() 异步派发通知，属于测试里天然的时序不确定性来源，
// 所以用 require.Eventually 轮询等待落库，而不是同步断言。
func TestCreateTicket_NoAssignee_NotifiesRequesterAsync(t *testing.T) {
	client := enttest.Open(t, "sqlite3", testDSN())
	defer client.Close()

	logger := zaptest.NewLogger(t).Sugar()
	ctx := context.Background()

	tenant, err := client.Tenant.Create().
		SetName("T2").SetCode("t2").SetDomain("t2.com").SetStatus("active").
		Save(ctx)
	require.NoError(t, err)

	requester, err := client.User.Create().
		SetUsername("requester2").SetEmail("requester2@example.com").SetName("Requester Two").
		SetPasswordHash("hash").SetRole("end_user").SetActive(true).SetTenantID(tenant.ID).
		Save(ctx)
	require.NoError(t, err)

	svc := NewTicketServiceForTest(client, logger)
	svc.SetNotificationService(NewTicketNotificationService(client, logger))

	created, err := svc.CreateTicket(ctx, &dto.CreateTicketRequest{
		Title:       "无处理人工单",
		Description: "创建时未分配处理人，走 BPMN 履约节点后续分配",
		Priority:    "medium",
		Category:    "incident",
		RequesterID: requester.ID,
	}, tenant.ID)
	require.NoError(t, err)
	require.Nil(t, created.AssigneeID, "测试前提：创建时不应有 assignee")

	require.Eventually(t, func() bool {
		notifs, qErr := client.TicketNotification.Query().
			Where(ticketnotification.TicketID(created.ID)).
			All(ctx)
		if qErr != nil || len(notifs) != 1 {
			return false
		}
		return notifs[0].UserID == requester.ID
	}, 5*time.Second, 50*time.Millisecond,
		"没有 assignee 时，CreateTicket 也应该异步通知申请人本人（回归：调用方不应再以 AssigneeID 网关调用 NotifyTicketCreated）")
}
