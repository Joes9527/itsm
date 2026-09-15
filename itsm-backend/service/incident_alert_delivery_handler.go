package service

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/mail"
	"strconv"
	"strings"
	"time"

	entsql "entgo.io/ent/dialect/sql"

	"itsm-backend/common/executionscope"
	"itsm-backend/common/tenantctx"
	"itsm-backend/database"
	"itsm-backend/ent/auditlog"
	"itsm-backend/ent/incident"
	"itsm-backend/ent/incidentalert"
	"itsm-backend/ent/outboxevent"
	"itsm-backend/ent/ticket"

	"itsm-backend/ent"
)

type incidentAlertEmailSender interface {
	SendToTarget(context.Context, int, string, EmailTarget, *EmailMessage) error
}

type IncidentAlertDeliveryHandler struct {
	emailSender incidentAlertEmailSender
	client      *ent.Client
	policy      *database.ExecutionPolicy
}

func NewIncidentAlertDeliveryHandler(client *ent.Client, policy *database.ExecutionPolicy, emailSender incidentAlertEmailSender) *IncidentAlertDeliveryHandler {
	return &IncidentAlertDeliveryHandler{emailSender: emailSender, client: client, policy: policy}
}

func (h *IncidentAlertDeliveryHandler) EventType() string {
	return incidentAlertDeliveryEventType
}

func (h *IncidentAlertDeliveryHandler) Deliver(ctx context.Context, event *ent.OutboxEvent) error {
	if event == nil {
		return blockOutboxDelivery("incident alert delivery event is required")
	}
	if h == nil || h.client == nil || h.policy == nil || h.emailSender == nil || event.ID <= 0 || event.ClaimToken == "" || event.EventType != incidentAlertDeliveryEventType {
		return blockOutboxDelivery("incident alert delivery dependencies or identity are incomplete")
	}
	if tenantID, ok := tenantctx.TenantID(ctx); !ok || tenantID != event.TenantID {
		return blockOutboxDelivery("incident alert delivery tenant mismatch")
	}
	tx, err := h.client.Tx(ctx)
	if err != nil {
		return err
	}
	payload, err := h.validateTx(ctx, tx, event)
	if err != nil {
		_ = tx.Rollback()
		return incidentAlertPreflightError(err)
	}
	if err = tx.Commit(); err != nil {
		return err
	}
	err = h.emailSender.SendToTarget(ctx, payload.TenantID, "outbox", payload.Target, &EmailMessage{
		To:                      append([]string(nil), payload.Recipients...),
		Subject:                 "[ITSM Alert] " + payload.Subject,
		BodyText:                payload.Message,
		DeliveryID:              event.EventID,
		DisableProviderFallback: true,
	})
	if err != nil && emailTransportOutcomeOf(err) == emailAcceptanceUnknown {
		return blockOutboxDelivery("delivery_unknown: email " + emailTransportStageOf(err, "transport") + " result is ambiguous; manual reconciliation required")
	}
	if errors.Is(err, executionscope.ErrDenied) {
		return blockOutboxDelivery("incident alert delivery target is not permitted")
	}
	if err != nil {
		return err
	}
	tx, err = h.client.Tx(ctx)
	if err != nil {
		return blockOutboxDelivery("delivery_unknown: incident alert receipt unavailable")
	}
	defer tx.Rollback()
	current, err := h.validateTx(ctx, tx, event)
	if err != nil {
		return blockOutboxDelivery("delivery_unknown: incident alert authority changed after send")
	}
	beforeDigest, err := incidentAlertJSONDigest(payload)
	if err != nil {
		return blockOutboxDelivery("delivery_unknown: incident alert digest unavailable")
	}
	afterDigest, err := incidentAlertJSONDigest(current)
	if err != nil || beforeDigest != afterDigest {
		return blockOutboxDelivery("delivery_unknown: incident alert payload changed after send")
	}
	_, err = tx.AuditLog.Create().SetTenantID(event.TenantID).SetUserID(payload.ActorID).SetOperationID("incident_alert_deliver:" + event.EventID).
		SetRequestID(payload.CorrelationID).SetResource("incident_alert").SetAction("incident_alert.delivered").SetPath("outbox://" + event.EventID).SetMethod("POST").SetStatusCode(200).SetRequestDigest(beforeDigest).SetResultStatus("delivered").Save(ctx)
	if err != nil {
		return blockOutboxDelivery("delivery_unknown: incident alert receipt could not be persisted")
	}
	if err = tx.Commit(); err != nil {
		return blockOutboxDelivery("delivery_unknown: incident alert receipt could not commit")
	}
	return nil
}

func validateIncidentAlertDelivery(event *ent.OutboxEvent, payload incidentAlertDeliveryPayload) string {
	if payload.Version != 2 {
		return "unsupported incident alert delivery payload version"
	}
	if payload.Target.Validate() != nil {
		return "invalid incident alert email target"
	}
	if payload.WorkItemID <= 0 || payload.IncidentID <= 0 {
		return "incident alert source identity is incomplete"
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
	if len(payload.Recipients) != 1 {
		return "incident alert delivery requires exactly one recipient"
	}
	for _, recipient := range payload.Recipients {
		if _, err := mail.ParseAddress(recipient); err != nil {
			return "invalid incident alert delivery recipient"
		}
	}
	return ""
}

var _ OutboxDeliveryHandler = (*IncidentAlertDeliveryHandler)(nil)

func decodeIncidentAlertJSON(data []byte, destination interface{}) error {
	decoder := json.NewDecoder(bytes.NewReader(data))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(destination); err != nil {
		return err
	}
	var extra interface{}
	if decoder.Decode(&extra) != io.EOF {
		return fmt.Errorf("trailing incident alert JSON")
	}
	return nil
}

func incidentAlertPreflightError(err error) error {
	if ent.IsNotFound(err) || errors.Is(err, executionscope.ErrDenied) {
		return blockOutboxDelivery("incident alert source, receipt or claim unavailable")
	}
	return err
}

func (h *IncidentAlertDeliveryHandler) validateTx(ctx context.Context, tx *ent.Tx, event *ent.OutboxEvent) (incidentAlertDeliveryPayload, error) {
	var payload incidentAlertDeliveryPayload
	if err := h.policy.BindEnt(ctx, tx, event.TenantID); err != nil {
		return payload, err
	}
	if err := h.policy.RequireCapability(ctx, event.TenantID, "outbox"); err != nil {
		return payload, err
	}
	stored, err := tx.OutboxEvent.Query().Where(outboxevent.IDEQ(event.ID), outboxevent.TenantIDEQ(event.TenantID), outboxevent.EventTypeEQ(incidentAlertDeliveryEventType), outboxevent.EventIDEQ(event.EventID), outboxevent.StatusEQ(outboxEventStatusPublishing), outboxevent.ClaimTokenEQ(event.ClaimToken), outboxevent.ClaimExpiresAtGT(time.Now().UTC()), outboxevent.LastErrorEQ(outboxDeliveryAttemptPrefix+summarizeOutboxError(event.EventID)), func(s *entsql.Selector) { s.ForUpdate() }).Only(ctx)
	if err != nil {
		return payload, err
	}
	if err = decodeIncidentAlertJSON(stored.Payload, &payload); err != nil {
		return payload, blockOutboxDelivery("invalid incident alert delivery payload")
	}
	if reason := validateIncidentAlertDelivery(stored, payload); reason != "" {
		return payload, blockOutboxDelivery(reason)
	}
	if payload.Channel != "email" || payload.ActorID < 0 || stored.ExecutionWorkItemID == nil || *stored.ExecutionWorkItemID != payload.WorkItemID {
		return payload, blockOutboxDelivery("incident alert delivery source mismatch")
	}
	source, err := tx.IncidentAlert.Query().Where(incidentalert.IDEQ(payload.AlertID), incidentalert.TenantIDEQ(event.TenantID), incidentalert.IncidentIDEQ(payload.IncidentID)).Only(ctx)
	if err != nil {
		return payload, err
	}
	item, err := tx.Incident.Query().Where(incident.IDEQ(source.IncidentID), incident.HasWorkItemWith(ticket.TenantIDEQ(event.TenantID), ticket.IDEQ(payload.WorkItemID))).Only(ctx)
	if err != nil {
		return payload, err
	}
	if err = requireIncidentExecutionTx(ctx, tx, h.policy, item.ID, event.TenantID); err != nil {
		return payload, err
	}
	receipt, err := tx.AuditLog.Query().Where(auditlog.TenantIDEQ(event.TenantID), auditlog.UserIDEQ(payload.ActorID), auditlog.OperationIDEQ("incident_alert_accept:"+fmt.Sprint(payload.AlertID))).Only(ctx)
	if err != nil {
		return payload, err
	}
	if receipt.Resource != "incident_alert" || receipt.Action != "incident_alert.delivery_accepted" || receipt.Path != "incident_alerts/delivery" || receipt.Method != "OUTBOX" || receipt.StatusCode != 202 || receipt.RequestID != payload.CorrelationID || receipt.RequestBody == nil || receipt.RequestDigest == nil || receipt.ResultStatus == nil || *receipt.ResultStatus != "accepted" || receipt.ResultVersion != nil {
		return payload, blockOutboxDelivery("invalid incident alert acceptance receipt")
	}
	var accepted incidentAlertAcceptanceReceipt
	if err = decodeIncidentAlertJSON([]byte(*receipt.RequestBody), &accepted); err != nil {
		return payload, blockOutboxDelivery("invalid incident alert acceptance manifest")
	}
	digest, err := incidentAlertJSONDigest(accepted)
	if err != nil || digest != *receipt.RequestDigest || accepted.Version != 2 || accepted.TenantID != payload.TenantID || accepted.AlertID != payload.AlertID || accepted.IncidentID != payload.IncidentID || accepted.WorkItemID != payload.WorkItemID || accepted.ActorID != payload.ActorID || accepted.Source != payload.Source || accepted.CorrelationID != payload.CorrelationID {
		return payload, blockOutboxDelivery("incident alert acceptance identity mismatch")
	}
	digest, err = incidentAlertJSONDigest(payload)
	if err != nil {
		return payload, err
	}
	matches := 0
	for _, intent := range accepted.Intents {
		if intent.EventID == stored.EventID {
			matches++
			if intent.PayloadDigest != digest {
				return payload, blockOutboxDelivery("incident alert acceptance payload digest mismatch")
			}
		}
	}
	if matches != 1 {
		return payload, blockOutboxDelivery("incident alert payload not in acceptance receipt")
	}
	delivered, err := tx.AuditLog.Query().Where(auditlog.TenantIDEQ(event.TenantID), auditlog.UserIDEQ(payload.ActorID), auditlog.OperationIDEQ("incident_alert_deliver:"+event.EventID)).Only(ctx)
	if err != nil && !ent.IsNotFound(err) {
		return payload, err
	}
	if err == nil {
		if delivered.Resource != "incident_alert" || delivered.Action != "incident_alert.delivered" || delivered.Path != "outbox://"+event.EventID || delivered.Method != "POST" || delivered.StatusCode != 200 || delivered.RequestID != payload.CorrelationID || delivered.RequestDigest == nil || *delivered.RequestDigest != digest || delivered.ResultStatus == nil || *delivered.ResultStatus != "delivered" || delivered.ResultVersion != nil {
			return payload, blockOutboxDelivery("incident alert delivery receipt conflicts with intent")
		}
		return payload, blockOutboxDelivery("delivery_unknown: incident alert delivery receipt already exists")
	}
	return payload, nil
}
