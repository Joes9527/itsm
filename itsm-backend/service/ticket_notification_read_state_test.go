package service

import (
	"context"
	"testing"
	"time"

	"itsm-backend/ent/ticketnotification"

	"github.com/stretchr/testify/require"
	"go.uber.org/zap/zaptest"
)

func TestTicketNotificationReadStateDoesNotChangeDurableDeliveryState(t *testing.T) {
	fixture := newDurableNotificationFixture(t, "notification-read-state")
	pending := fixture.enqueueExternalCC(t)
	now := time.Now().Add(time.Hour)
	fixture.notifications.now = func() time.Time { return now }

	processing := fixture.client.TicketNotification.Create().
		SetTicketID(fixture.ticket.ID).
		SetUserID(fixture.recipient.ID).
		SetType("cc").
		SetChannel("email").
		SetContent("processing delivery").
		SetStatus(ticketNotificationStatusProcessing).
		SetDeliveryKey("notification-read-state-processing").
		SetAttemptCount(1).
		SetNextAttemptAt(now.Add(-2 * time.Hour)).
		SetLeaseOwner("expired-read-state-worker").
		SetLeaseExpiresAt(now.Add(-time.Hour)).
		SetTenantID(fixture.tenant.ID).
		SaveX(fixture.ctx)
	sent := fixture.client.TicketNotification.Create().
		SetTicketID(fixture.ticket.ID).
		SetUserID(fixture.recipient.ID).
		SetType("cc").
		SetChannel("in_app").
		SetContent("in-app delivery").
		SetStatus(ticketNotificationStatusSent).
		SetSentAt(now.Add(-time.Minute)).
		SetTenantID(fixture.tenant.ID).
		SaveX(fixture.ctx)

	require.NoError(t, fixture.notifications.MarkNotificationRead(
		fixture.ctx, pending.ID, fixture.recipient.ID, fixture.tenant.ID,
	))
	require.NoError(t, fixture.notifications.MarkAllNotificationsRead(
		fixture.ctx, fixture.recipient.ID, fixture.tenant.ID,
	))

	pending = fixture.client.TicketNotification.GetX(fixture.ctx, pending.ID)
	processing = fixture.client.TicketNotification.GetX(fixture.ctx, processing.ID)
	sent = fixture.client.TicketNotification.GetX(fixture.ctx, sent.ID)
	require.Equal(t, ticketNotificationStatusPending, pending.Status)
	require.Equal(t, ticketNotificationStatusProcessing, processing.Status)
	require.Equal(t, ticketNotificationStatusSent, sent.Status)
	require.False(t, pending.ReadAt.IsZero())
	require.False(t, processing.ReadAt.IsZero())
	require.False(t, sent.ReadAt.IsZero())

	read := true
	responses, total, err := fixture.notifications.ListUserNotifications(
		fixture.ctx, fixture.recipient.ID, fixture.tenant.ID, 1, 10, &read,
	)
	require.NoError(t, err)
	require.Equal(t, 3, total)
	require.Len(t, responses, 3)

	connectorSink := &durableNotificationConnector{}
	configureDurableNotificationConnector(t, fixture.notifications, fixture.tenant.ID, connectorSink)
	graphSink := &graphSenderSpy{}
	emailService := NewEmailService(EmailConfig{}, zaptest.NewLogger(t).Sugar())
	emailService.SetGraphProvider(func(_ int) (GraphMailSender, string, bool) {
		return graphSink, "sender@example.test", true
	})
	fixture.notifications.SetEmailService(emailService)

	completed, err := fixture.notifications.ProcessPendingDeliveries(
		context.Background(), "notification-read-state-worker", 10,
	)
	require.NoError(t, err)
	require.Equal(t, 2, completed)
	require.Equal(t, ticketNotificationStatusSent, fixture.client.TicketNotification.GetX(fixture.ctx, pending.ID).Status)
	require.Equal(t, ticketNotificationStatusSent, fixture.client.TicketNotification.GetX(fixture.ctx, processing.ID).Status)
	require.Equal(t, 3, fixture.client.TicketNotification.Query().Where(
		ticketnotification.ReadAtNotNil(),
	).CountX(fixture.ctx))
}
