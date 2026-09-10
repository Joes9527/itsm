package service

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strconv"
	"strings"

	"itsm-backend/authorization"
	"itsm-backend/dto"
	"itsm-backend/ent"
	"itsm-backend/ent/auditlog"
	"itsm-backend/ent/outboxevent"
	"itsm-backend/ent/predicate"
	"itsm-backend/ent/ticket"
	"itsm-backend/ent/user"
	"itsm-backend/ent/workitemrelation"

	"go.uber.org/zap"
)

// ProblemResolvedDeliveryHandler informs the handler of every Incident that
// investigated a resolved Problem. It never transitions Incident lifecycle state:
// restoring or closing Incidents stays with the Incident domain, so a resolution
// can only notify.
type ProblemResolvedDeliveryHandler struct {
	client *ent.Client
	sender ticketNotificationSender
	logger *zap.SugaredLogger
}

func NewProblemResolvedDeliveryHandler(client *ent.Client, sender ticketNotificationSender, logger *zap.SugaredLogger) *ProblemResolvedDeliveryHandler {
	return &ProblemResolvedDeliveryHandler{client: client, sender: sender, logger: logger}
}

func (*ProblemResolvedDeliveryHandler) EventType() string { return ProblemResolvedEventType }

// ReplaySafe is honest: the notification uses the notifications DeliveryKey and the
// declared skip uses the audit selection key.
func (*ProblemResolvedDeliveryHandler) ReplaySafe() bool { return true }

func (h *ProblemResolvedDeliveryHandler) Deliver(ctx context.Context, event *ent.OutboxEvent) error {
	facts, err := h.validate(ctx, event)
	if err != nil {
		return err
	}
	sources, err := h.client.WorkItemRelation.Query().
		Where(
			workitemrelation.TenantID(facts.TenantID),
			workitemrelation.TargetWorkItemID(facts.WorkItemID),
			workitemrelation.RelationType("investigated_by"),
			workitemrelation.DeletedAtIsNil(),
		).
		All(ctx)
	if err != nil {
		return fmt.Errorf("read investigated_by incidents: %w", err)
	}
	if len(sources) == 0 {
		return h.recordNoIncidentDecision(ctx, facts)
	}

	actor, err := h.client.User.Query().Where(user.ID(facts.ActorID), user.TenantID(facts.TenantID), user.Active(true)).Only(ctx)
	if err != nil {
		return blockOutboxDelivery("problem resolved actor unavailable in tenant")
	}
	scope := authorization.WorkItemReadScope(actor.ID, authorization.EffectiveSessionRole(actor))
	problemItem, err := h.client.Ticket.Query().Where(ticket.ID(facts.WorkItemID), ticket.TenantID(facts.TenantID), ticket.DeletedAtIsNil()).Only(ctx)
	if err != nil {
		return blockOutboxDelivery("problem resolved work item is unavailable")
	}

	// A blocked target must not mask a retryable one, and a retry must not duplicate
	// the Incidents that were already notified.
	var blockedErr, retryableErr error
	for _, source := range sources {
		err := h.notifyIncidentHandler(ctx, event, facts, source.SourceWorkItemID, problemItem, scope)
		if err == nil {
			continue
		}
		var blocked *outboxDeliveryBlockedError
		if errors.As(err, &blocked) {
			if blockedErr == nil {
				blockedErr = err
			}
		} else if retryableErr == nil {
			retryableErr = err
		}
		h.logger.Warnw("problem resolution notification failed", "incident_work_item_id", source.SourceWorkItemID, "error_summary", err.Error())
	}
	return preferRetryableError(blockedErr, retryableErr)
}

func (h *ProblemResolvedDeliveryHandler) notifyIncidentHandler(ctx context.Context, event *ent.OutboxEvent, facts ProblemResolvedFacts, incidentID int, problemItem *ent.Ticket, scope predicate.Ticket) error {
	incidentItem, _, err := authorization.ResolveWorkItemIdentity(ctx, h.client, incidentID, facts.TenantID, scope)
	if err != nil {
		return blockOutboxDelivery("investigating incident is not readable")
	}
	recipient := incidentItem.AssigneeID
	if recipient <= 0 {
		recipient = incidentItem.RequesterID
	}
	if recipient <= 0 {
		return blockOutboxDelivery("investigating incident has no eligible recipient")
	}
	result, err := h.sender.SendNotification(ctx, incidentID, &dto.SendTicketNotificationRequest{
		UserIDs:     []int{recipient},
		EventType:   ProblemResolvedEventType,
		Content:     fmt.Sprintf("关联问题 %s 已解决，请确认事件 %s 的恢复结果。", problemItem.TicketNumber, incidentItem.TicketNumber),
		DeliveryKey: fmt.Sprintf("%s:%d:resolved", event.EventID, incidentID),
		InAppOnly:   true,
	}, facts.TenantID)
	if err != nil {
		return fmt.Errorf("problem resolution notification delivery failed: %w", err)
	}
	if result == nil {
		return blockOutboxDelivery("problem resolution notification produced no result")
	}
	if result.Effect == dto.TicketNotificationEffectBlocked {
		return blockOutboxDelivery("problem resolution notification blocked: " + result.BlockCode)
	}
	return nil
}

// recordNoIncidentDecision keeps a resolution with no investigating Incident visible
// instead of silently skipping the notification step.
func (h *ProblemResolvedDeliveryHandler) recordNoIncidentDecision(ctx context.Context, facts ProblemResolvedFacts) error {
	eventID := problemResolvedEventID(facts.WorkItemID, facts.Version)
	exists, err := h.client.AuditLog.Query().
		Where(auditlog.TenantID(facts.TenantID), auditlog.UserID(facts.ActorID), auditlog.OperationID(eventID)).
		Exist(ctx)
	if err != nil {
		return fmt.Errorf("look up problem resolution notification decision: %w", err)
	}
	if exists {
		return nil
	}
	body, err := json.Marshal(map[string]any{"eventId": eventID, "workItemId": facts.WorkItemID, "reason": "no live investigated_by incident"})
	if err != nil {
		return err
	}
	if _, err := h.client.AuditLog.Create().
		SetTenantID(facts.TenantID).
		SetUserID(facts.ActorID).
		SetOperationID(eventID).
		SetResource("problem_resolution").
		SetAction("notification_not_required").
		SetPath(strconv.Itoa(facts.WorkItemID)).
		SetMethod("outbox").
		SetStatusCode(200).
		SetRequestBody(string(body)).
		Save(ctx); err != nil {
		return fmt.Errorf("record problem resolution notification decision: %w", err)
	}
	h.logger.Warnw("problem resolution notification not required", "work_item_id", facts.WorkItemID, "reason", "no live investigated_by incident")
	return nil
}

func (h *ProblemResolvedDeliveryHandler) validate(ctx context.Context, event *ent.OutboxEvent) (ProblemResolvedFacts, error) {
	var facts ProblemResolvedFacts
	if event == nil || event.EventType != ProblemResolvedEventType {
		return facts, blockOutboxDelivery("unknown problem resolved event")
	}
	decoder := json.NewDecoder(bytes.NewReader(event.Payload))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&facts); err != nil {
		return facts, blockOutboxDelivery("invalid problem resolved payload")
	}
	if facts.TenantID <= 0 || facts.ActorID <= 0 || facts.ProblemID <= 0 || facts.WorkItemID <= 0 || facts.Version <= 1 ||
		strings.TrimSpace(facts.Source) == "" || strings.TrimSpace(facts.OperationID) == "" ||
		event.TenantID != facts.TenantID || event.AggregateType != "work_item" ||
		event.AggregateID != strconv.Itoa(facts.WorkItemID) ||
		event.EventID != problemResolvedEventID(facts.WorkItemID, facts.Version) {
		return facts, blockOutboxDelivery("problem resolved identity mismatch")
	}
	stored, err := h.client.OutboxEvent.Query().
		Where(outboxevent.ID(event.ID), outboxevent.TenantID(facts.TenantID), outboxevent.EventID(event.EventID), outboxevent.EventType(event.EventType)).
		Only(ctx)
	if err != nil {
		return facts, err
	}
	var durable ProblemResolvedFacts
	if json.Unmarshal(stored.Payload, &durable) != nil || durable != facts {
		return facts, blockOutboxDelivery("problem resolved payload differs from durable event")
	}
	receipt, err := h.client.AuditLog.Query().
		Where(
			auditlog.TenantID(facts.TenantID),
			auditlog.UserID(facts.ActorID),
			auditlog.OperationID(facts.OperationID),
			auditlog.ResultVersion(facts.Version),
			auditlog.Resource("work_item"),
			auditlog.Action("problem.resolve"),
			auditlog.Path(strconv.Itoa(facts.WorkItemID)),
		).Only(ctx)
	if err != nil {
		return facts, err
	}
	var receiptFacts struct {
		ProblemID int `json:"problemId"`
	}
	if receipt.RequestBody == nil || json.Unmarshal([]byte(*receipt.RequestBody), &receiptFacts) != nil || receiptFacts.ProblemID != facts.ProblemID {
		return facts, blockOutboxDelivery("problem resolved event lacks immutable command provenance")
	}
	return facts, nil
}
