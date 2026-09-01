package service

import (
	"context"
	"encoding/json"
	"errors"
	"net/mail"
	"strconv"
	"strings"

	"itsm-backend/ent"
)

type incidentAlertEmailSender interface {
	SendForTenant(context.Context, int, *EmailMessage) error
}

type IncidentAlertDeliveryHandler struct {
	emailSender incidentAlertEmailSender
}

func NewIncidentAlertDeliveryHandler(emailSender incidentAlertEmailSender) *IncidentAlertDeliveryHandler {
	return &IncidentAlertDeliveryHandler{emailSender: emailSender}
}

func (h *IncidentAlertDeliveryHandler) EventType() string {
	return incidentAlertDeliveryEventType
}

func (h *IncidentAlertDeliveryHandler) Deliver(ctx context.Context, event *ent.OutboxEvent) error {
	if event == nil {
		return blockOutboxDelivery("incident alert delivery event is required")
	}
	var payload incidentAlertDeliveryPayload
	if err := json.Unmarshal(event.Payload, &payload); err != nil {
		return blockOutboxDelivery("invalid incident alert delivery payload")
	}
	if reason := validateIncidentAlertDelivery(event, payload); reason != "" {
		return blockOutboxDelivery(reason)
	}
	if payload.Channel != "email" {
		return blockOutboxDelivery("unsupported incident alert delivery channel: " + payload.Channel)
	}
	if h.emailSender == nil {
		return blockOutboxDelivery("incident alert email delivery is not configured")
	}
	err := h.emailSender.SendForTenant(ctx, payload.TenantID, &EmailMessage{
		To:                      append([]string(nil), payload.Recipients...),
		Subject:                 "[ITSM Alert] " + payload.Subject,
		BodyText:                payload.Message,
		DeliveryID:              event.EventID,
		DisableProviderFallback: true,
	})
	if err != nil && (errors.Is(err, errEmailGraphSend) || errors.Is(err, errEmailSMTPSend)) {
		return blockOutboxDelivery("delivery_unknown: email transport result is ambiguous; manual reconciliation required")
	}
	return err
}

func validateIncidentAlertDelivery(event *ent.OutboxEvent, payload incidentAlertDeliveryPayload) string {
	if payload.Version != 1 {
		return "unsupported incident alert delivery payload version"
	}
	if payload.EventID == "" || payload.EventID != event.EventID {
		return "incident alert delivery event identity mismatch"
	}
	if payload.TenantID <= 0 || payload.TenantID != event.TenantID {
		return "incident alert delivery tenant mismatch"
	}
	if event.AggregateType != "incident_alert" || payload.AlertID <= 0 || event.AggregateID != strconv.Itoa(payload.AlertID) {
		return "incident alert delivery aggregate mismatch"
	}
	if strings.TrimSpace(payload.Source) == "" || strings.TrimSpace(payload.CorrelationID) == "" {
		return "incident alert delivery actor metadata is incomplete"
	}
	if strings.TrimSpace(payload.Subject) == "" || strings.TrimSpace(payload.Message) == "" {
		return "incident alert delivery content is incomplete"
	}
	if len(payload.Recipients) == 0 {
		return "incident alert delivery recipient is required"
	}
	for _, recipient := range payload.Recipients {
		if _, err := mail.ParseAddress(recipient); err != nil {
			return "invalid incident alert delivery recipient"
		}
	}
	return ""
}

var _ OutboxDeliveryHandler = (*IncidentAlertDeliveryHandler)(nil)
