package service

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strconv"
	"strings"

	"itsm-backend/database"
	"itsm-backend/dto"
	"itsm-backend/ent"
	"itsm-backend/ent/auditlog"
	"itsm-backend/ent/outboxevent"
	"itsm-backend/ent/workitemrelation"

	"go.uber.org/zap"
)

// ChangeOutcomeDeliveryHandler consumes a recorded Change outcome. A successful
// outcome prompts verification from every live resolved_by_change source; every
// other outcome is a declared "no verification required" case that must still stay
// observable. Both paths are idempotent through the notification DeliveryKey or the
// decision audit key.
type ChangeOutcomeDeliveryHandler struct {
	client    *ent.Client
	directory database.DirectorySnapshot
	sender    ticketNotificationSender
	logger    *zap.SugaredLogger
}

func NewChangeOutcomeDeliveryHandler(client *ent.Client, directory database.DirectorySnapshot, sender ticketNotificationSender, logger *zap.SugaredLogger) *ChangeOutcomeDeliveryHandler {
	return &ChangeOutcomeDeliveryHandler{client: client, directory: directory, sender: sender, logger: logger}
}

func (*ChangeOutcomeDeliveryHandler) EventType() string { return ChangeOutcomeEventType }

// ReplaySafe is honest: the prompt uses the notifications DeliveryKey and the
// declared skip uses the audit selection key, so a repeated delivery cannot
// duplicate either effect.
func (*ChangeOutcomeDeliveryHandler) ReplaySafe() bool { return true }

func (h *ChangeOutcomeDeliveryHandler) Deliver(ctx context.Context, event *ent.OutboxEvent) error {
	facts, err := h.validate(ctx, event)
	if err != nil {
		return err
	}
	if err := authorizeWorkItemDeliveryActor(ctx, h.client, h.directory, facts.ActorID, facts.TenantID, facts.WorkItemID); err != nil {
		return err
	}

	if !RequiresProblemVerification(facts.Outcome) {
		return h.recordVerificationDecision(ctx, facts, "outcome "+facts.Outcome+" is not a verified repair")
	}

	sources, err := h.client.WorkItemRelation.Query().
		Where(
			workitemrelation.TenantID(facts.TenantID),
			workitemrelation.TargetWorkItemID(facts.WorkItemID),
			workitemrelation.RelationType("resolved_by_change"),
			workitemrelation.DeletedAtIsNil(),
		).
		All(ctx)
	if err != nil {
		return fmt.Errorf("read resolved_by_change sources: %w", err)
	}
	if len(sources) == 0 {
		return h.recordVerificationDecision(ctx, facts, "no live resolved_by_change source")
	}

	// Each source is an independent target: a failure on one must not silently drop
	// the others, a retry must not duplicate the ones that already succeeded, and a
	// terminally blocked target must not mask a retryable one.
	var blockedErr, retryableErr error
	for _, source := range sources {
		err := h.promptSource(ctx, event, facts, source.SourceWorkItemID)
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
		h.logger.Warnw("change outcome verification prompt failed", "source_work_item_id", source.SourceWorkItemID, "error_summary", err.Error())
	}
	return preferRetryableError(blockedErr, retryableErr)
}

// promptSource asks one resolved_by_change source to verify the successful repair.
func (h *ChangeOutcomeDeliveryHandler) promptSource(ctx context.Context, event *ent.OutboxEvent, facts ChangeOutcomeFacts, sourceID int) error {
	changeItem, sourceItem, recipient, err := loadWorkItemDeliveryTarget(ctx, h.client, h.directory, facts.ActorID, facts.TenantID, facts.WorkItemID, sourceID)
	if err != nil {
		return err
	}

	result, err := h.sender.SendNotification(ctx, sourceID, &dto.SendTicketNotificationRequest{
		UserIDs:     []int{recipient},
		EventType:   ChangeOutcomeEventType,
		Content:     changeOutcomeNotificationContent(changeItem, sourceItem),
		DeliveryKey: fmt.Sprintf("%s:%d:verify", event.EventID, sourceID),
		InAppOnly:   true,
	}, facts.TenantID)
	if err != nil {
		return fmt.Errorf("change outcome notification delivery failed: %w", err)
	}
	if result == nil {
		return blockOutboxDelivery("change outcome notification produced no result")
	}
	if result.Effect == dto.TicketNotificationEffectBlocked {
		return blockOutboxDelivery("change outcome notification blocked: " + result.BlockCode)
	}
	return nil
}

// recordVerificationDecision makes a declared "no verification required" outcome
// visible instead of silently skipping the step.
func (h *ChangeOutcomeDeliveryHandler) recordVerificationDecision(ctx context.Context, facts ChangeOutcomeFacts, reason string) error {
	eventID := changeOutcomeEventID(facts.WorkItemID, facts.Version)
	exists, err := h.client.AuditLog.Query().
		Where(auditlog.TenantID(facts.TenantID), auditlog.UserID(facts.ActorID), auditlog.OperationID(eventID)).
		Exist(ctx)
	if err != nil {
		return fmt.Errorf("look up change outcome verification decision: %w", err)
	}
	if exists {
		return nil
	}
	body, err := json.Marshal(map[string]any{"eventId": eventID, "workItemId": facts.WorkItemID, "outcome": facts.Outcome, "reason": reason})
	if err != nil {
		return err
	}
	if _, err := h.client.AuditLog.Create().
		SetTenantID(facts.TenantID).
		SetUserID(facts.ActorID).
		SetOperationID(eventID).
		SetResource("change_outcome").
		SetAction("verification_not_required").
		SetPath(strconv.Itoa(facts.WorkItemID)).
		SetMethod("outbox").
		SetStatusCode(200).
		SetRequestBody(string(body)).
		Save(ctx); err != nil {
		return fmt.Errorf("record change outcome verification decision: %w", err)
	}
	h.logger.Warnw("change outcome verification not required", "work_item_id", facts.WorkItemID, "outcome", facts.Outcome, "reason", reason)
	return nil
}

func (h *ChangeOutcomeDeliveryHandler) validate(ctx context.Context, event *ent.OutboxEvent) (ChangeOutcomeFacts, error) {
	var facts ChangeOutcomeFacts
	if event == nil || event.EventType != ChangeOutcomeEventType {
		return facts, blockOutboxDelivery("unknown change outcome delivery event")
	}
	decoder := json.NewDecoder(bytes.NewReader(event.Payload))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&facts); err != nil {
		return facts, blockOutboxDelivery("invalid change outcome payload")
	}
	if facts.TenantID <= 0 || facts.ActorID <= 0 || facts.ActorTenantID <= 0 || facts.ChangeID <= 0 || facts.WorkItemID <= 0 || facts.Version <= 1 ||
		strings.TrimSpace(facts.Outcome) == "" || strings.TrimSpace(facts.Source) == "" || strings.TrimSpace(facts.OperationID) == "" ||
		event.TenantID != facts.TenantID || event.AggregateType != "work_item" ||
		event.AggregateID != strconv.Itoa(facts.WorkItemID) ||
		event.EventID != changeOutcomeEventID(facts.WorkItemID, facts.Version) {
		return facts, blockOutboxDelivery("change outcome identity mismatch")
	}
	stored, err := h.client.OutboxEvent.Query().
		Where(outboxevent.ID(event.ID), outboxevent.TenantID(facts.TenantID), outboxevent.EventID(event.EventID), outboxevent.EventType(event.EventType)).
		Only(ctx)
	if err != nil {
		return facts, classifyWorkItemDeliveryError(err)
	}
	var durable ChangeOutcomeFacts
	if json.Unmarshal(stored.Payload, &durable) != nil || durable != facts {
		return facts, blockOutboxDelivery("change outcome payload differs from durable event")
	}
	receipt, err := h.client.AuditLog.Query().
		Where(
			auditlog.TenantID(facts.TenantID),
			auditlog.UserID(facts.ActorID),
			auditlog.OperationID(facts.OperationID),
			auditlog.ResultVersion(facts.Version),
			auditlog.Resource("work_item"),
			auditlog.Action("change.record_outcome"),
			auditlog.Path(strconv.Itoa(facts.WorkItemID)),
		).Only(ctx)
	if err != nil {
		return facts, classifyWorkItemDeliveryError(err)
	}
	var receiptFacts struct {
		ChangeID int    `json:"changeId"`
		Outcome  string `json:"outcome"`
	}
	if receipt.RequestBody == nil || json.Unmarshal([]byte(*receipt.RequestBody), &receiptFacts) != nil ||
		receiptFacts.ChangeID != facts.ChangeID || receiptFacts.Outcome != facts.Outcome {
		return facts, blockOutboxDelivery("change outcome event lacks immutable command provenance")
	}
	return facts, nil
}

func changeOutcomeNotificationContent(changeItem, sourceItem *ent.Ticket) string {
	return fmt.Sprintf("变更 %s 已成功实施，请验证关联问题 %s 的修复结果。", changeItem.TicketNumber, sourceItem.TicketNumber)
}
