package service

import (
	"context"
	"fmt"
	"strings"

	"itsm-backend/dto"
	"itsm-backend/ent"
	"itsm-backend/ent/slaalerthistory"
	"itsm-backend/ent/ticket"
	"itsm-backend/ent/ticketnotification"
	"itsm-backend/ent/user"
)

// EnqueueNotificationTx contributes durable notification intents to an owning
// business transaction. It never commits or invokes a provider. Any error
// requires the caller to roll back, including intents already written here.
func (s *TicketNotificationService) EnqueueNotificationTx(ctx context.Context, tx *ent.Tx, ticketID, tenantID int, req *dto.SendTicketNotificationRequest) error {
	return s.enqueueNotificationTx(ctx, tx, ticketID, tenantID, req, nil)
}

// selectedChannels is a server-owned restriction on the recipient preferences.
// nil retains all eligible preference channels; an empty set explicitly disables all.
func (s *TicketNotificationService) enqueueNotificationTx(ctx context.Context, tx *ent.Tx, ticketID, tenantID int, req *dto.SendTicketNotificationRequest, selectedChannels map[string]bool) error {
	if s == nil || tx == nil || req == nil || strings.TrimSpace(req.DeliveryKey) == "" || strings.TrimSpace(req.EventType) == "" || strings.TrimSpace(req.Content) == "" || len(req.UserIDs) == 0 {
		return fmt.Errorf("notification intent requires transaction, target, recipients and stable delivery identity")
	}
	if err := s.execution.BindEnt(ctx, tx, tenantID); err != nil {
		return err
	}
	if err := s.execution.RequireEntMembers(ctx, tx, tenantID, ticketID); err != nil {
		return err
	}
	if _, err := tx.Ticket.Query().Where(ticket.IDEQ(ticketID), ticket.TenantIDEQ(tenantID), ticket.DeletedAtIsNil()).Only(ctx); err != nil {
		return fmt.Errorf("notification intent target: %w", err)
	}
	if req.SLAAlertHistoryID != nil {
		if _, err := tx.SLAAlertHistory.Query().Where(slaalerthistory.IDEQ(*req.SLAAlertHistoryID), slaalerthistory.TenantIDEQ(tenantID), slaalerthistory.TicketIDEQ(ticketID), slaalerthistory.NotificationTrackingVersionEQ(1)).Only(ctx); err != nil {
			return fmt.Errorf("notification SLA owner: %w", err)
		}
	}
	// Preference reads share the caller's snapshot, including its uncommitted
	// configuration edits; never mutate the service used by other goroutines.
	reader := *s
	if s.prefService != nil {
		preferences := *s.prefService
		preferences.client = tx.Client()
		reader.prefService = &preferences
	}
	seen := map[int]bool{}
	for _, userID := range req.UserIDs {
		if seen[userID] {
			continue
		}
		seen[userID] = true
		if _, err := tx.User.Query().Where(user.IDEQ(userID), user.TenantIDEQ(tenantID), user.ActiveEQ(true)).Only(ctx); err != nil {
			return fmt.Errorf("notification intent recipient: %w", err)
		}
		persisted, err := tx.TicketNotification.Query().Where(ticketnotification.TenantIDEQ(tenantID), ticketnotification.TicketIDEQ(ticketID), ticketnotification.UserIDEQ(userID), ticketnotification.DeliveryKeyEQ(req.DeliveryKey)).All(ctx)
		if err != nil {
			return fmt.Errorf("notification intent replay lookup: %w", err)
		}
		for _, row := range persisted {
			if err := validateNotificationConnectorTarget(row); err != nil {
				return err
			}
			if row.Type != req.EventType || row.Content != req.Content || (row.SLAAlertHistoryID == nil) != (req.SLAAlertHistoryID == nil) || (row.SLAAlertHistoryID != nil && req.SLAAlertHistoryID != nil && *row.SLAAlertHistoryID != *req.SLAAlertHistoryID) {
				return fmt.Errorf("notification delivery identity conflicts with persisted intent")
			}
		}
		// Once materialized, this recipient's channels are frozen. Check identity
		// across channels before current preferences can hide an earlier delivery.
		if len(persisted) > 0 {
			continue
		}
		prefs, err := reader.resolvePreferences(ctx, userID, tenantID, req.EventType)
		if err != nil {
			return err
		}
		channels := []struct {
			name    string
			enabled bool
		}{{"in_app", prefs.InAppEnabled}, {"email", prefs.EmailEnabled && !req.InAppOnly}, {"sms", prefs.SmsEnabled && !req.InAppOnly}, {"push", prefs.PushEnabled && !req.InAppOnly}}
		// Explicitly disabled preferences produce no intent. There is no durable
		// suppression receipt; callers must not treat this as a sent notification.
		for _, channel := range channels {
			if !channel.enabled || (selectedChannels != nil && !selectedChannels[channel.name]) {
				continue
			}
			if channel.name == "in_app" {
				if err := createInAppNotificationPair(ctx, tx.Client(), ticketID, userID, req, tenantID, s.clock()); err != nil {
					return err
				}
				continue
			}
			create := tx.TicketNotification.Create().SetNillableSLAAlertHistoryID(req.SLAAlertHistoryID).SetTenantID(tenantID).SetTicketID(ticketID).SetUserID(userID).SetType(req.EventType).SetChannel(channel.name).SetContent(req.Content).SetDeliveryKey(req.DeliveryKey).SetStatus(ticketNotificationStatusPending).SetNextAttemptAt(s.clock())
			if err := s.BindNotificationConnectorTarget(ctx, tenantID, channel.name, create); err != nil {
				return err
			}
			if _, err := create.Save(ctx); err != nil {
				return fmt.Errorf("notification intent write: %w", err)
			}
		}
	}
	return nil
}
