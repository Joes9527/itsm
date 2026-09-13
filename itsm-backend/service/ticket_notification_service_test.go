package service

import (
	"context"
	"testing"

	"go.uber.org/zap"
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

func TestTicketNotificationQueueReplayAndConflict(t *testing.T) {
	client, svc, ctx := setupTicketNotificationTest(t)
	defer client.Close()
	tenant, recipient, item := createNotifTestData(t, client, ctx)
	svc.SetNotificationPreferenceService(NewNotificationPreferenceService(client, zap.NewNop().Sugar()))
	pref := client.NotificationPreference.Create().SetUserID(recipient.ID).SetTenantID(tenant.ID).SetEventType("ticket_updated").SetEmailEnabled(true).SetInAppEnabled(true).SetSmsEnabled(false).SetPushEnabled(false).SaveX(ctx)
	req := &dto.SendTicketNotificationRequest{UserIDs: []int{recipient.ID, recipient.ID}, EventType: "ticket_updated", Content: "once", DeliveryKey: "mixed-once"}
	first, err := svc.SendNotification(ctx, item.ID, req, tenant.ID)
	require.NoError(t, err)
	require.Equal(t, dto.TicketNotificationEffectQueued, first.Effect)
	require.Equal(t, 1, first.RecipientCount)
	require.Equal(t, 1, first.AppliedCount)
	require.Equal(t, 1, first.QueuedCount)
	require.Equal(t, 2, first.DeliveryCount)
	pref.Update().SetEmailEnabled(false).SetSmsEnabled(true).SaveX(ctx)
	replay, err := svc.SendNotification(ctx, item.ID, req, tenant.ID)
	require.NoError(t, err)
	require.Equal(t, dto.TicketNotificationEffectIdempotent, replay.Effect)
	require.Equal(t, 1, replay.IdempotentCount)
	require.Equal(t, 1, replay.ExternalIntentCount)
	require.Equal(t, 2, replay.DeliveryCount)
	require.Zero(t, replay.QueuedCount)
	req.InAppOnly = true
	_, err = svc.SendNotification(ctx, item.ID, req, tenant.ID)
	require.ErrorContains(t, err, "conflicts")
	req.InAppOnly = false
	req.Content = "changed"
	_, err = svc.SendNotification(ctx, item.ID, req, tenant.ID)
	require.ErrorContains(t, err, "conflicts")
	require.Equal(t, 2, client.TicketNotification.Query().CountX(ctx))
	require.Equal(t, 1, client.Notification.Query().CountX(ctx))
}

func TestTicketNotificationMixedTargetFailureRollsBack(t *testing.T) {
	client, svc, ctx := setupTicketNotificationTest(t)
	defer client.Close()
	tenant, recipient, item := createNotifTestData(t, client, ctx)
	svc.SetNotificationPreferenceService(NewNotificationPreferenceService(client, zap.NewNop().Sugar()))
	client.NotificationPreference.Create().SetUserID(recipient.ID).SetTenantID(tenant.ID).SetEventType("ticket_updated").SetEmailEnabled(true).SetInAppEnabled(true).SetSmsEnabled(true).SetPushEnabled(false).SaveX(ctx)
	result, err := svc.SendNotification(ctx, item.ID, &dto.SendTicketNotificationRequest{UserIDs: []int{recipient.ID}, EventType: "ticket_updated", Content: "rollback"}, tenant.ID)
	require.Error(t, err)
	require.Nil(t, result)
	require.Zero(t, client.TicketNotification.Query().CountX(ctx))
	require.Zero(t, client.Notification.Query().CountX(ctx))
}

func TestTicketNotificationMixedReplayKeepsExternalEvidence(t *testing.T) {
	client, svc, ctx := setupTicketNotificationTest(t)
	defer client.Close()
	tenant, recipient, item := createNotifTestData(t, client, ctx)
	svc.SetNotificationPreferenceService(NewNotificationPreferenceService(client, zap.NewNop().Sugar()))
	client.NotificationPreference.Create().SetUserID(recipient.ID).SetTenantID(tenant.ID).SetEventType("ticket_updated").SetEmailEnabled(true).SetInAppEnabled(false).SetSmsEnabled(false).SetPushEnabled(false).SaveX(ctx)
	req := &dto.SendTicketNotificationRequest{UserIDs: []int{recipient.ID}, EventType: "ticket_updated", Content: "mixed replay", DeliveryKey: "mixed-replay"}
	_, err := svc.SendNotification(ctx, item.ID, req, tenant.ID)
	require.NoError(t, err)
	other := client.User.Create().SetTenantID(tenant.ID).SetUsername("other").SetName("Other").SetEmail("other@example.invalid").SetPasswordHash("x").SetActive(true).SaveX(ctx)
	client.NotificationPreference.Create().SetUserID(other.ID).SetTenantID(tenant.ID).SetEventType("ticket_updated").SetEmailEnabled(false).SetInAppEnabled(true).SetSmsEnabled(false).SetPushEnabled(false).SaveX(ctx)
	req.UserIDs = append(req.UserIDs, other.ID)
	result, err := svc.SendNotification(ctx, item.ID, req, tenant.ID)
	require.NoError(t, err)
	require.Equal(t, dto.TicketNotificationEffectApplied, result.Effect)
	require.Equal(t, 1, result.AppliedCount)
	require.Equal(t, 1, result.IdempotentCount)
	require.Equal(t, 1, result.ExternalIntentCount)
	require.Equal(t, 2, result.DeliveryCount)
	require.Zero(t, result.QueuedCount)
}
