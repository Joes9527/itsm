package service

import (
	"context"
	"testing"

	"itsm-backend/dto"

	"github.com/stretchr/testify/require"
)

func TestTicketNotificationMissingRecipientBlocksWholeDelivery(t *testing.T) {
	client, svc, ctx := setupTicketNotificationTest(t)
	t.Cleanup(func() { require.NoError(t, client.Close()) })
	tenant, recipient, ticketEntity := createNotifTestData(t, client, ctx)

	result, err := svc.SendNotification(ctx, ticketEntity.ID, &dto.SendTicketNotificationRequest{
		UserIDs:   []int{recipient.ID, recipient.ID + 100000},
		EventType: "ticket_updated",
		Content:   "all recipients must resolve",
	}, tenant.ID)
	require.NoError(t, err)
	require.Equal(t, dto.TicketNotificationEffectBlocked, result.Effect)
	require.Equal(t, "recipient_missing", result.BlockCode)
	require.Zero(t, client.TicketNotification.Query().CountX(context.Background()))
	require.Zero(t, client.Notification.Query().CountX(context.Background()))
}

func TestTicketNotificationStableDeliveryKeyIsIdempotent(t *testing.T) {
	client, svc, ctx := setupTicketNotificationTest(t)
	t.Cleanup(func() { require.NoError(t, client.Close()) })
	tenant, recipient, ticketEntity := createNotifTestData(t, client, ctx)
	req := &dto.SendTicketNotificationRequest{
		UserIDs: []int{recipient.ID}, EventType: "ticket_updated", Content: "once",
		DeliveryKey: "ticket-notification-idempotent", InAppOnly: true,
	}
	first, err := svc.SendNotification(ctx, ticketEntity.ID, req, tenant.ID)
	require.NoError(t, err)
	require.Equal(t, dto.TicketNotificationEffectApplied, first.Effect)
	second, err := svc.SendNotification(ctx, ticketEntity.ID, req, tenant.ID)
	require.NoError(t, err)
	require.Equal(t, dto.TicketNotificationEffectIdempotent, second.Effect)
	require.Equal(t, 1, second.IdempotentCount)
	require.Equal(t, 1, client.TicketNotification.Query().CountX(ctx))
	require.Equal(t, 1, client.Notification.Query().CountX(ctx))
}
