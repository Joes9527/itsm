package service

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"time"

	entsql "entgo.io/ent/dialect/sql"
	"itsm-backend/common/executionscope"
	"itsm-backend/common/tenantctx"
	"itsm-backend/connector"
	"itsm-backend/database"
	"itsm-backend/ent"
	"itsm-backend/ent/auditlog"
	"itsm-backend/ent/outboxevent"
)

type webhookSender interface {
	Send(context.Context, *connector.Message) error
	connector.DeliveryDestination
}

type boundWebhookTarget struct {
	sender     webhookSender
	generation uint64
}

type WebhookDeliveryHandler struct {
	client  *ent.Client
	policy  *database.ExecutionPolicy
	manager *connector.Manager
}

func NewWebhookDeliveryHandler(client *ent.Client, policy *database.ExecutionPolicy, manager *connector.Manager) *WebhookDeliveryHandler {
	return &WebhookDeliveryHandler{client, policy, manager}
}
func (*WebhookDeliveryHandler) EventType() string { return WebhookDeliveryRequestedEventType }

func (h *WebhookDeliveryHandler) target(tenantID int, p webhookDeliveryPayload) (boundWebhookTarget, error) {
	conn, generation, ok := h.manager.GetInstance(tenantID, "webhook", p.Target.Provider)
	if !ok {
		return boundWebhookTarget{}, fmt.Errorf("webhook target unavailable")
	}
	target, ok := conn.(webhookSender)
	if !ok || target.DeliveryDestinationIdentity() == "" || target.DeliveryDestinationIdentity() != p.Target.DestinationDigest {
		return boundWebhookTarget{}, fmt.Errorf("webhook destination changed")
	}
	return boundWebhookTarget{target, generation}, nil
}

func (h *WebhookDeliveryHandler) Deliver(ctx context.Context, event *ent.OutboxEvent) error {
	if h == nil || h.client == nil || h.policy == nil || h.manager == nil || ctx == nil || event == nil || event.ID <= 0 || event.ClaimToken == "" || event.EventType != WebhookDeliveryRequestedEventType {
		return blockOutboxDelivery("invalid webhook delivery dependencies or identity")
	}
	if id, ok := tenantctx.TenantID(ctx); ok && id != event.TenantID {
		return blockOutboxDelivery("webhook tenant mismatch")
	}
	ctx = tenantctx.WithTenantID(ctx, event.TenantID)
	tx, err := h.client.Tx(ctx)
	if err != nil {
		return err
	}
	payload, err := h.validateTx(ctx, tx, event)
	if err != nil {
		_ = tx.Rollback()
		return webhookPreflightError(err)
	}
	if err = tx.Commit(); err != nil {
		return err
	}
	target, err := h.target(event.TenantID, payload)
	if err != nil {
		return blockOutboxDelivery("webhook destination changed or unavailable")
	}
	body, err := json.Marshal(payload.Source)
	if err != nil {
		return blockOutboxDelivery("invalid webhook message")
	}
	// This exact provisioned object was validated above. Do not resolve the key
	// again to send, since a concurrent provision can bind it to another endpoint.
	if err = target.sender.Send(ctx, &connector.Message{ID: event.EventID, Type: "text", Title: "ITSM 事件: " + payload.Source.EventType, Content: string(body), Metadata: map[string]interface{}{"eventType": payload.Source.EventType}}); err != nil {
		return blockOutboxDelivery("delivery_unknown: webhook send requires reconciliation")
	}
	tx, err = h.client.Tx(ctx)
	if err != nil {
		return blockOutboxDelivery("delivery_unknown: webhook receipt unavailable")
	}
	defer tx.Rollback()
	if _, err = h.validateTx(ctx, tx, event); err != nil {
		return blockOutboxDelivery("delivery_unknown: webhook authority changed after send")
	}
	current, err := h.target(event.TenantID, payload)
	if err != nil || current.generation != target.generation {
		return blockOutboxDelivery("delivery_unknown: webhook instance changed after send")
	}
	digest, err := webhookJSONDigest(payload)
	if err != nil {
		return blockOutboxDelivery("delivery_unknown: webhook receipt digest unavailable")
	}
	_, err = tx.AuditLog.Create().SetTenantID(event.TenantID).SetUserID(0).SetOperationID("webhook_deliver:" + event.EventID).SetResource("event").SetAction("webhook.delivered").SetPath("outbox://" + event.EventID).SetMethod("POST").SetStatusCode(200).SetRequestDigest(digest).SetResultStatus("delivered").Save(ctx)
	if err != nil {
		return blockOutboxDelivery("delivery_unknown: webhook receipt could not be persisted")
	}
	if err = tx.Commit(); err != nil {
		return blockOutboxDelivery("delivery_unknown: webhook receipt could not commit")
	}
	return nil
}

func (h *WebhookDeliveryHandler) validateTx(ctx context.Context, tx *ent.Tx, event *ent.OutboxEvent) (webhookDeliveryPayload, error) {
	var payload webhookDeliveryPayload
	if err := h.policy.BindEnt(ctx, tx, event.TenantID); err != nil {
		return payload, err
	}
	stored, err := tx.OutboxEvent.Query().Where(outboxevent.IDEQ(event.ID), outboxevent.TenantIDEQ(event.TenantID), outboxevent.EventTypeEQ(WebhookDeliveryRequestedEventType), outboxevent.EventIDEQ(event.EventID), outboxevent.StatusEQ(outboxEventStatusPublishing), outboxevent.ClaimTokenEQ(event.ClaimToken), outboxevent.ClaimExpiresAtGT(time.Now()), outboxevent.LastErrorEQ(outboxDeliveryAttemptPrefix+summarizeOutboxError(event.EventID)), func(s *entsql.Selector) { s.ForUpdate() }).Only(ctx)
	if err != nil {
		return payload, err
	}
	decoder := json.NewDecoder(bytes.NewReader(stored.Payload))
	decoder.DisallowUnknownFields()
	if err = decoder.Decode(&payload); err != nil {
		return payload, blockOutboxDelivery("invalid webhook payload")
	}
	var trailing interface{}
	if decoder.Decode(&trailing) != io.EOF {
		return payload, blockOutboxDelivery("invalid webhook payload")
	}
	digest, err := webhookJSONDigest(stored.Payload)
	if err != nil || stored.EventID != "webhook-delivery:"+digest || payload.Source.Execution == nil || payload.Source.TenantID != fmt.Sprint(event.TenantID) || stored.ExecutionWorkItemID == nil || *stored.ExecutionWorkItemID != payload.Source.Execution.WorkItemID || stored.AggregateType != "work_item" || stored.AggregateID != fmt.Sprint(payload.Source.Execution.WorkItemID) {
		return payload, blockOutboxDelivery("webhook intent identity mismatch")
	}
	receipt, err := tx.AuditLog.Query().Where(auditlog.TenantIDEQ(event.TenantID), auditlog.UserIDEQ(0), auditlog.OperationIDEQ("webhook_consume:"+payload.Source.EventID)).Only(ctx)
	if err != nil {
		return payload, err
	}
	if receipt.Resource != "event" || receipt.Action != "webhook.enqueued" || receipt.Path != "eventbus://"+payload.Source.EventType || receipt.Method != "CONSUME" || receipt.StatusCode != 202 || receipt.ResultStatus == nil || *receipt.ResultStatus != "enqueued" || receipt.ResultVersion != nil || receipt.RequestDigest == nil || receipt.RequestBody == nil {
		return payload, blockOutboxDelivery("invalid webhook consumption receipt")
	}
	var saved webhookConsumptionReceipt
	if err = json.Unmarshal([]byte(*receipt.RequestBody), &saved); err != nil {
		return payload, blockOutboxDelivery("invalid webhook consumption receipt")
	}
	savedDigest, err := webhookJSONDigest(saved.Source)
	if err != nil {
		return payload, err
	}
	sourceDigest, err := webhookJSONDigest(payload.Source)
	if err != nil || savedDigest != sourceDigest || sourceDigest != *receipt.RequestDigest {
		return payload, blockOutboxDelivery("webhook source digest mismatch")
	}
	matches := 0
	for _, intent := range saved.Intents {
		if intent.EventID == stored.EventID && intent.PayloadDigest == digest {
			matches++
		}
	}
	if matches != 1 {
		return payload, blockOutboxDelivery("webhook intent not in receipt")
	}
	// Audit stores the original JSON string. Recheck that original envelope, not
	// the JSONB copy whose key order may have changed during persistence.
	payload.Source = saved.Source
	if payload.Source.Execution == nil {
		return payload, executionscope.ErrDenied
	}
	ref := executionscope.Ref{DeploymentID: payload.Source.Execution.DeploymentID, ScopeID: payload.Source.Execution.ScopeID, TenantID: event.TenantID}
	if err = NewExecutionEventAuthority(h.client, h.policy).ValidateEventTx(ctx, tx, ref, payload.Source); err != nil {
		return payload, err
	}
	return payload, nil
}

func webhookPreflightError(err error) error {
	if ent.IsNotFound(err) || errors.Is(err, executionscope.ErrDenied) {
		return blockOutboxDelivery("webhook source, receipt or claim unavailable")
	}
	return err
}
