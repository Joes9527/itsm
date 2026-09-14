package service

import (
	"go.uber.org/zap"
	"itsm-backend/database"
	"itsm-backend/ent"
)

// These producer tests persist an explicit destination; they never start delivery.
func newQueuedNotificationTestService(client *ent.Client, logger *zap.SugaredLogger, policy *database.ExecutionPolicy) *TicketNotificationService {
	notifications := NewTicketNotificationService(client, logger, policy)
	mail := NewEmailService(EmailConfig{DeliveryTransport: "smtp", Host: "smtp.example.invalid", Port: 2525, Username: "fixture", From: "fixture@example.invalid"}, logger)
	mail.SetDeliveryTargetDependencies(nil, policy)
	notifications.SetEmailService(mail)
	return notifications
}
