package service

import (
	"context"
	"errors"
	"fmt"
	"net/mail"
	"strings"

	"itsm-backend/authorization"

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
	return s.enqueueTicketNotificationTx(ctx, tx, item, eventType, content, deliveryKey, recipientIDs, 0)
}

// enqueueTicketNotificationTx persists channel intent only. The existing delivery
// worker owns external calls, leases and delivery_unknown reconciliation.
func (s *TicketNotificationService) enqueueTicketNotificationTx(ctx context.Context, tx *ent.Tx, item *ent.Ticket, eventType, content, deliveryKey string, recipientIDs []int, verifiedAssignmentRecipient int) error {
	recipients := uniqueTicketNotificationUserIDs(recipientIDs)
	if len(recipients) == 0 {
		return creation.NewDomainValidationFailed("creation notification recipients are required", nil)
	}
	for _, recipientID := range recipients {
		recipient, err := s.currentNotificationRecipient(ctx, tx, recipientID, item.TenantID)
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
		if preferences.EmailEnabled {
			if s.emailService == nil {
				return creation.NewDomainValidationFailed("configured email notification has no delivery owner", nil)
			}
			if _, err := mail.ParseAddress(recipient.Email); err != nil {
				return creation.NewDomainValidationFailed("notification email address is invalid", err)
			}
		}
		if preferences.SmsEnabled {
			if s.connectorManager == nil {
				return creation.NewDomainValidationFailed("configured SMS notification has no delivery owner", nil)
			}
			if strings.TrimSpace(recipient.Phone) == "" {
				return creation.NewDomainValidationFailed("configured SMS notification target is unavailable", nil)
			}
		}
		if preferences.PushEnabled {
			if s.wsService == nil {
				return creation.NewDomainValidationFailed("configured push notification has no delivery owner", nil)
			}
		}

	}
	_, err := s.enqueueNotificationResultTx(ctx, tx, item.ID, item.TenantID, &dto.SendTicketNotificationRequest{UserIDs: recipients, EventType: eventType, Content: content, DeliveryKey: deliveryKey}, nil, verifiedAssignmentRecipient)
	return err
}

func (s *TicketNotificationService) currentNotificationRecipient(ctx context.Context, tx *ent.Tx, recipientID, tenantID int) (*ent.User, error) {
	native, err := tx.User.Query().Where(user.ID(recipientID), user.TenantID(tenantID), user.Active(true)).Only(ctx)
	if err == nil {
		return native, nil
	}
	if !ent.IsNotFound(err) {
		return nil, err
	}
	if s.assignmentDirectory == nil {
		return nil, &ent.NotFoundError{}
	}
	directory, closeDirectory, err := s.assignmentDirectory.Open(ctx, tx, tenantID)
	if err != nil {
		return nil, err
	}
	if directory == nil || closeDirectory == nil {
		return nil, fmt.Errorf("assignment notification directory unavailable")
	}
	recipient, lookupErr := authorization.ResolveCurrentTenantUser(ctx, directory, recipientID, tenantID, s.clock())
	if err := errors.Join(lookupErr, closeDirectory()); err != nil {
		return nil, err
	}
	return recipient, nil
}
