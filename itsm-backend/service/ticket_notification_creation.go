package service

import (
	"context"
	"fmt"
	"net/mail"
	"strings"

	"itsm-backend/dto"
	"itsm-backend/ent"
	"itsm-backend/ent/ticket"
	"itsm-backend/ent/user"
	creation "itsm-backend/handlers/common/workitemcreation"
)

// EnqueueCreationTx writes notification facts and external delivery intent in
// the caller's Intake transaction. It never performs external delivery.
func (s *TicketNotificationService) EnqueueCreationTx(ctx context.Context, tx *ent.Tx, item *ent.Ticket, actorID int, eventType, content, deliveryKey string, recipientIDs []int) error {
	if s == nil || tx == nil || item == nil || item.TenantID <= 0 || item.ID <= 0 || actorID <= 0 || strings.TrimSpace(deliveryKey) == "" || strings.TrimSpace(eventType) == "" || strings.TrimSpace(content) == "" {
		return creation.NewDomainValidationFailed("creation notification identity and content are required", nil)
	}
	target, err := tx.Ticket.Query().Where(ticket.IDEQ(item.ID), ticket.TenantIDEQ(item.TenantID), ticket.DeletedAtIsNil()).Exist(ctx)
	if err != nil {
		return creation.NewInfrastructureUnavailable("could not verify notification target", err)
	}
	if !target {
		return creation.NewReferenceNotFound("notification target is unavailable", nil)
	}
	actor, err := tx.User.Query().Where(user.IDEQ(actorID), user.TenantIDEQ(item.TenantID), user.ActiveEQ(true)).Exist(ctx)
	if err != nil {
		return creation.NewInfrastructureUnavailable("could not verify notification actor", err)
	}
	if !actor {
		return creation.NewPermissionDenied("notification actor is unavailable", nil)
	}
	recipients := uniqueTicketNotificationUserIDs(recipientIDs)
	if len(recipients) == 0 {
		return creation.NewDomainValidationFailed("creation notification recipients are required", nil)
	}
	for _, recipientID := range recipients {
		recipient, err := tx.User.Query().Where(user.IDEQ(recipientID), user.TenantIDEQ(item.TenantID), user.ActiveEQ(true)).Only(ctx)
		if ent.IsNotFound(err) {
			return creation.NewReferenceNotFound("notification recipient is unavailable", err)
		}
		if err != nil {
			return creation.NewInfrastructureUnavailable("could not load notification recipient", err)
		}
		preferences, err := NewNotificationPreferenceService(tx.Client(), s.logger).GetUserPreferenceByEventType(ctx, recipientID, item.TenantID, eventType)
		if err != nil {
			return creation.NewInfrastructureUnavailable("could not resolve notification preferences", err)
		}
		req := &dto.SendTicketNotificationRequest{UserIDs: []int{recipientID}, EventType: eventType, Content: content, DeliveryKey: deliveryKey}
		if preferences.InAppEnabled {
			if err := createInAppNotificationPair(ctx, tx.Client(), item.ID, recipientID, req, item.TenantID, s.clock()); err != nil {
				return creation.NewInfrastructureUnavailable("could not persist creation notification", err)
			}
		}
		channels := []string{}
		if preferences.EmailEnabled {
			if s.emailService == nil {
				return creation.NewDomainValidationFailed("configured email notification has no delivery owner", nil)
			}
			if _, err := mail.ParseAddress(recipient.Email); err != nil {
				return creation.NewDomainValidationFailed("notification email address is invalid", err)
			}
			channels = append(channels, "email")
		}
		if preferences.SmsEnabled {
			if s.connectorManager == nil {
				return creation.NewDomainValidationFailed("configured SMS notification has no delivery owner", nil)
			}
			if _, ok := s.connectorManager.Get(item.TenantID, "sms"); !ok || strings.TrimSpace(recipient.Phone) == "" {
				return creation.NewDomainValidationFailed("configured SMS notification target is unavailable", nil)
			}
			channels = append(channels, "sms")
		}
		if preferences.PushEnabled {
			if s.wsService == nil {
				return creation.NewDomainValidationFailed("configured push notification has no delivery owner", nil)
			}
			channels = append(channels, "push")
		}
		for _, channel := range channels {
			if err := tx.TicketNotification.Create().SetTenantID(item.TenantID).SetTicketID(item.ID).SetUserID(recipient.ID).SetType(eventType).SetChannel(channel).SetContent(content).SetDeliveryKey(deliveryKey).SetStatus(ticketNotificationStatusPending).SetNextAttemptAt(s.clock()).Exec(ctx); err != nil {
				return creation.NewInfrastructureUnavailable(fmt.Sprintf("could not persist %s creation notification", channel), err)
			}
		}
	}
	return nil
}
