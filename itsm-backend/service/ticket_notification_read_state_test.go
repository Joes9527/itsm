package service

import (
	"context"
	"fmt"
	"testing"
	"time"

	"itsm-backend/ent/enttest"
	"itsm-backend/ent/ticketnotification"

	_ "github.com/mattn/go-sqlite3"
	"github.com/stretchr/testify/require"
	"go.uber.org/zap"
	"go.uber.org/zap/zaptest"
	"go.uber.org/zap/zaptest/observer"
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
	require.Error(t, err)
	require.Equal(t, 1, completed)
	require.Equal(t, ticketNotificationStatusSent, fixture.client.TicketNotification.GetX(fixture.ctx, pending.ID).Status)
	require.Equal(t, ticketNotificationStatusFailed, fixture.client.TicketNotification.GetX(fixture.ctx, processing.ID).Status)
	require.Equal(t, 3, fixture.client.TicketNotification.Query().Where(
		ticketnotification.ReadAtNotNil(),
	).CountX(fixture.ctx))
}

func TestTicketNotificationMarkReadReturnsOwnershipNotFoundSentinel(t *testing.T) {
	fixture := newDurableNotificationFixture(t, "notification-read-ownership")
	notification := fixture.enqueueExternalCC(t)

	for _, test := range []struct {
		name           string
		notificationID int
		userID         int
		tenantID       int
	}{
		{name: "missing", notificationID: notification.ID + 100000, userID: fixture.recipient.ID, tenantID: fixture.tenant.ID},
		{name: "foreign user", notificationID: notification.ID, userID: fixture.recipient.ID + 100000, tenantID: fixture.tenant.ID},
		{name: "foreign tenant", notificationID: notification.ID, userID: fixture.recipient.ID, tenantID: fixture.tenant.ID + 100000},
	} {
		t.Run(test.name, func(t *testing.T) {
			err := fixture.notifications.MarkNotificationRead(fixture.ctx, test.notificationID, test.userID, test.tenantID)
			require.ErrorIs(t, err, ErrTicketNotificationNotFound)
			require.Equal(t, ErrTicketNotificationNotFound.Error(), err.Error())
		})
	}
}

func TestTicketNotificationReadStorageFailuresAreSanitized(t *testing.T) {
	client := enttest.Open(t, "sqlite3", "file:ticket_notification_read_storage?mode=memory&cache=shared&_fk=1")
	core, logs := observer.New(zap.ErrorLevel)
	notifications := NewTicketNotificationService(client, zap.New(core).Sugar())
	require.NoError(t, client.Close())

	for _, operation := range []struct {
		name string
		call func() error
	}{
		{name: "mark one", call: func() error { return notifications.MarkNotificationRead(context.Background(), 1, 2, 3) }},
		{name: "mark all", call: func() error { return notifications.MarkAllNotificationsRead(context.Background(), 2, 3) }},
	} {
		t.Run(operation.name, func(t *testing.T) {
			err := operation.call()
			require.ErrorIs(t, err, ErrTicketNotificationStorage)
			require.Equal(t, ErrTicketNotificationStorage.Error(), err.Error())
		})
	}

	entries := logs.All()
	require.Len(t, entries, 2)
	for _, entry := range entries {
		fields := entry.ContextMap()
		require.Equal(t, "ticket_notification_read_storage", fields["error_class"])
		require.NotContains(t, fields, "error")
		require.NotContains(t, fmt.Sprint(entry), "sql: database is closed")
	}
}
