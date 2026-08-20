package service

import (
	"fmt"
	"testing"

	_ "github.com/mattn/go-sqlite3"

	"itsm-backend/ent/ticketnotification"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
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
