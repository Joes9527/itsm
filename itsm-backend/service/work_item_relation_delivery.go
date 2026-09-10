package service

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"strconv"
	"strings"

	"itsm-backend/authorization"
	"itsm-backend/dto"
	"itsm-backend/ent"
	"itsm-backend/ent/auditlog"
	"itsm-backend/ent/outboxevent"
	"itsm-backend/ent/ticket"
	"itsm-backend/ent/user"

	"go.uber.org/zap"
)

// ticketNotificationSender is the existing authoritative notification owner. The
// consumer reuses its preference resolution, durable DeliveryKey deduplication and
// applied/idempotent/blocked result instead of writing notifications itself.
type ticketNotificationSender interface {
	SendNotification(ctx context.Context, ticketID int, req *dto.SendTicketNotificationRequest, tenantID int) (*dto.SendTicketNotificationResult, error)
}

// WorkItemRelationDeliveryHandler consumes one directed relation delivery event.
// It validates the durable event and its immutable command receipt, re-checks that
// the acting user can still read both endpoints, then delivers exactly one in-app
// notification to the counterpart endpoint's assignee (falling back to its
// requester). Every unrecognised or unprovable situation blocks visibly instead of
// silently succeeding.
type WorkItemRelationDeliveryHandler struct {
	eventType string
	client    *ent.Client
	sender    ticketNotificationSender
	logger    *zap.SugaredLogger
}

func NewWorkItemRelationCreatedDeliveryHandler(client *ent.Client, sender ticketNotificationSender, logger *zap.SugaredLogger) *WorkItemRelationDeliveryHandler {
	return &WorkItemRelationDeliveryHandler{eventType: RelationCreatedEventType, client: client, sender: sender, logger: logger}
}

func NewWorkItemRelationRemovedDeliveryHandler(client *ent.Client, sender ticketNotificationSender, logger *zap.SugaredLogger) *WorkItemRelationDeliveryHandler {
	return &WorkItemRelationDeliveryHandler{eventType: RelationRemovedEventType, client: client, sender: sender, logger: logger}
}

func (h *WorkItemRelationDeliveryHandler) EventType() string { return h.eventType }

// ReplaySafe is honest here: the delivery writes the durable DeliveryKey that the
// notifications table enforces uniquely per tenant, key and recipient, so a
// repeated delivery cannot duplicate the effect.
func (h *WorkItemRelationDeliveryHandler) ReplaySafe() bool { return true }

func relationReceiptAction(removed bool) string {
	if removed {
		return "work_item.relation_removed"
	}
	return "work_item.relation_added"
}

func relationAction(removed bool) string {
	if removed {
		return "removed"
	}
	return "created"
}

func relationNotificationContent(facts RelationFacts, counterpart *ent.Ticket) string {
	verb := "建立"
	if facts.Removed {
		verb = "解除"
	}
	return fmt.Sprintf("工作项 %s 与当前记录的关联（%s）已%s。", counterpart.TicketNumber, facts.Type, verb)
}

func (h *WorkItemRelationDeliveryHandler) Deliver(ctx context.Context, event *ent.OutboxEvent) error {
	facts, err := h.validate(ctx, event)
	if err != nil {
		return err
	}

	actor, err := h.client.User.Query().Where(user.ID(facts.ActorID), user.TenantID(facts.TenantID), user.Active(true)).Only(ctx)
	if err != nil {
		return blockOutboxDelivery("relation actor unavailable in tenant")
	}

	counterpartID, err := relationCounterpart(facts)
	if err != nil {
		return err
	}

	// Both endpoints must still be readable by the current actor under the existing
	// row scope; a revoked or hidden endpoint blocks visibly rather than notifying
	// about a record the actor can no longer see.
	scope := authorization.WorkItemReadScope(actor.ID, authorization.EffectiveSessionRole(actor))
	for _, endpointID := range []int{facts.SourceID, facts.TargetID} {
		if _, _, err := authorization.ResolveWorkItemIdentity(ctx, h.client, endpointID, facts.TenantID, scope); err != nil {
			return blockOutboxDelivery("relation endpoint is no longer readable")
		}
	}

	counterpart, err := h.client.Ticket.Query().
		Where(ticket.ID(counterpartID), ticket.TenantID(facts.TenantID), ticket.DeletedAtIsNil()).
		Only(ctx)
	if err != nil {
		return blockOutboxDelivery("relation counterpart is unavailable")
	}
	recipient := counterpart.AssigneeID
	if recipient <= 0 {
		recipient = counterpart.RequesterID
	}
	if recipient <= 0 {
		return blockOutboxDelivery("relation counterpart has no eligible recipient")
	}

	result, err := h.sender.SendNotification(ctx, counterpartID, &dto.SendTicketNotificationRequest{
		UserIDs:     []int{recipient},
		EventType:   h.eventType,
		Content:     relationNotificationContent(facts, counterpart),
		DeliveryKey: fmt.Sprintf("%s:%d:%s", event.EventID, counterpartID, relationAction(facts.Removed)),
		InAppOnly:   true,
	}, facts.TenantID)
	if err != nil {
		return fmt.Errorf("relation notification delivery failed: %w", err)
	}
	if result == nil {
		return blockOutboxDelivery("relation notification produced no result")
	}
	if result.Effect == dto.TicketNotificationEffectBlocked {
		return blockOutboxDelivery("relation notification blocked: " + result.BlockCode)
	}
	h.logger.Debugw("relation delivery event consumed",
		"event_id", event.EventID, "relation_type", facts.Type, "effect", result.Effect,
		"counterpart_work_item_id", counterpartID, "recipient_id", recipient)
	return nil
}

// relationCounterpart returns the stored endpoint that the mutation did not act on.
func relationCounterpart(facts RelationFacts) (int, error) {
	switch facts.MutationWorkItemID {
	case facts.SourceID:
		return facts.TargetID, nil
	case facts.TargetID:
		return facts.SourceID, nil
	default:
		return 0, blockOutboxDelivery("relation mutation endpoint is not a stored endpoint")
	}
}

// validate proves the event is the durable, receipt-backed fact this consumer owns
// before it performs any effect.
func (h *WorkItemRelationDeliveryHandler) validate(ctx context.Context, event *ent.OutboxEvent) (RelationFacts, error) {
	var facts RelationFacts
	if event == nil || event.EventType != h.eventType {
		return facts, blockOutboxDelivery("unknown relation delivery event")
	}
	decoder := json.NewDecoder(bytes.NewReader(event.Payload))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&facts); err != nil {
		return facts, blockOutboxDelivery("invalid relation delivery payload")
	}
	if facts.TenantID <= 0 || facts.ActorID <= 0 || facts.RelationID <= 0 || facts.MutationWorkItemID <= 0 ||
		facts.SourceID <= 0 || facts.TargetID <= 0 || facts.Version <= 1 ||
		strings.TrimSpace(facts.Source) == "" || strings.TrimSpace(facts.OperationID) == "" ||
		!IsDirectedRelation(facts.Type) || facts.Removed != (h.eventType == RelationRemovedEventType) ||
		event.TenantID != facts.TenantID || event.AggregateType != "work_item" ||
		event.AggregateID != strconv.Itoa(facts.MutationWorkItemID) ||
		event.EventID != relationEventID(facts.RelationID, facts.Version, facts.Removed) {
		return facts, blockOutboxDelivery("relation delivery identity mismatch")
	}

	stored, err := h.client.OutboxEvent.Query().
		Where(outboxevent.ID(event.ID), outboxevent.TenantID(facts.TenantID), outboxevent.EventID(event.EventID), outboxevent.EventType(event.EventType)).
		Only(ctx)
	if err != nil {
		return facts, err
	}
	var durable RelationFacts
	if json.Unmarshal(stored.Payload, &durable) != nil || durable != facts {
		return facts, blockOutboxDelivery("relation payload differs from durable event")
	}

	receipt, err := h.client.AuditLog.Query().
		Where(
			auditlog.TenantID(facts.TenantID),
			auditlog.UserID(facts.ActorID),
			auditlog.OperationID(facts.OperationID),
			auditlog.ResultVersion(facts.Version),
			auditlog.Resource("work_item"),
			auditlog.Action(relationReceiptAction(facts.Removed)),
			auditlog.Path(strconv.Itoa(facts.MutationWorkItemID)),
		).Only(ctx)
	if err != nil {
		return facts, err
	}
	var receiptFacts RelationFacts
	if receipt.RequestBody == nil || json.Unmarshal([]byte(*receipt.RequestBody), &receiptFacts) != nil || receiptFacts != facts {
		return facts, blockOutboxDelivery("relation event lacks immutable command provenance")
	}
	return facts, nil
}
