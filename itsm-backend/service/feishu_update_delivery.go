package service

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"strconv"
	"time"

	entsql "entgo.io/ent/dialect/sql"

	"itsm-backend/authorization"
	"itsm-backend/common"
	"itsm-backend/common/executionscope"
	"itsm-backend/common/tenantctx"
	feishu "itsm-backend/connector/builtin/feishu"
	"itsm-backend/database"
	"itsm-backend/ent"
	"itsm-backend/ent/auditlog"
	"itsm-backend/ent/feishuticketsync"
	"itsm-backend/ent/outboxevent"
	"itsm-backend/ent/ticket"
	creation "itsm-backend/handlers/common/workitemcreation"
	"itsm-backend/handlers/shared/workitemmutation"
)

const FeishuUpdateRequestedEventType = "feishu.task.update.requested"

type FeishuTaskUpdater interface {
	UpdateTask(context.Context, string, *feishu.FeishuTask) (*feishu.FeishuTask, error)
	TaskDestinationIdentity() string
}
type (
	FeishuUpdateProvider func(int) (FeishuTaskUpdater, bool)
	feishuUpdatePayload  struct {
		TenantID      int               `json:"tenantId"`
		WorkItemID    int               `json:"workItemId"`
		ActorID       int               `json:"actorId"`
		OperationID   string            `json:"operationId"`
		RequestDigest string            `json:"requestDigest"`
		Action        string            `json:"action"`
		ResultStatus  string            `json:"resultStatus"`
		ResultVersion int               `json:"resultVersion"`
		MappingID     int               `json:"mappingId"`
		GUID          string            `json:"guid"`
		Destination   string            `json:"destination"`
		Task          feishu.FeishuTask `json:"task"`
	}
)

type feishuUpdateReceipt struct {
	EventID       string `json:"eventId"`
	PayloadDigest string `json:"payloadDigest"`
}

func feishuUpdateEventID(p feishuUpdatePayload) string {
	digest, _ := workitemmutation.Digest(struct {
		Tenant, Actor, Item int
		Operation           string
	}{p.TenantID, p.ActorID, p.WorkItemID, p.OperationID})
	return "feishu-update:" + digest
}

func feishuUpdateAggregate(p feishuUpdatePayload) string {
	digest, _ := workitemmutation.Digest(struct{ Destination, GUID string }{p.Destination, p.GUID})
	return digest
}

// Called after the owning Ticket CAS, while that row lock remains held. This is
// the insertion-order guarantee required by the shared ordered claim protocol.
func (s *TicketService) enqueueFeishuUpdate(ctx context.Context, tx *ent.Tx, itemID int, m workitemmutation.Meta, requestDigest, action string) (*feishuUpdateReceipt, error) {
	if s.connectorManager == nil {
		return nil, nil
	}
	conn, configured := s.connectorManager.Get(m.TenantID, "feishu")
	if !configured {
		return nil, nil
	}
	target, ok := conn.(FeishuTaskUpdater)
	if !ok || target.TaskDestinationIdentity() == "" {
		//lint:ignore ST1005 Preserve the existing domain term in this public error message.
		return nil, fmt.Errorf("Feishu update capability or destination unavailable")
	}
	item, err := tx.Ticket.Query().Where(ticket.IDEQ(itemID), ticket.TenantIDEQ(m.TenantID), ticket.DeletedAtIsNil()).Only(ctx)
	if err != nil {
		return nil, err
	}
	mapping, err := tx.FeishuTicketSync.Query().Where(feishuticketsync.TenantIDEQ(m.TenantID), feishuticketsync.TicketIDEQ(itemID)).Only(ctx)
	if err != nil {
		//lint:ignore ST1005 Preserve the existing domain term in this public error message.
		return nil, fmt.Errorf("Feishu update requires an existing task mapping: %w", err)
	}
	// The existing unique(tenant_id,feishu_task_id) protects participating targets
	// only when both remote identity fields agree. Never repair legacy identities.
	if mapping.FeishuTaskGUID == "" || mapping.FeishuTaskID != mapping.FeishuTaskGUID {
		//lint:ignore ST1005 Preserve the existing domain term in this public error message.
		return nil, fmt.Errorf("Feishu mapping identity requires reconciliation")
	}
	task, err := prepareFeishuTask(ctx, tx.Client(), item)
	if err != nil {
		return nil, err
	}
	payload := feishuUpdatePayload{TenantID: m.TenantID, WorkItemID: itemID, ActorID: m.ActorID, OperationID: m.OperationID, RequestDigest: requestDigest, Action: action, ResultStatus: item.Status, ResultVersion: item.Version, MappingID: mapping.ID, GUID: mapping.FeishuTaskGUID, Destination: target.TaskDestinationIdentity(), Task: *task}
	data, err := json.Marshal(payload)
	if err != nil {
		return nil, err
	}
	digest, err := workitemmutation.Digest(payload)
	if err != nil {
		return nil, err
	}
	eventID := feishuUpdateEventID(payload)
	_, err = enqueueOutboxEvent(ctx, tx.Client(), tx, NewOutboxEvent{ExecutionWorkItemID: itemID, TenantID: m.TenantID, EventID: eventID, EventType: FeishuUpdateRequestedEventType, AggregateType: "feishu_task", AggregateID: feishuUpdateAggregate(payload), Payload: data})
	if err != nil {
		return nil, err
	}
	return &feishuUpdateReceipt{EventID: eventID, PayloadDigest: digest}, nil
}

type FeishuUpdateDeliveryHandler struct {
	client    *ent.Client
	execution *database.ExecutionPolicy
	directory database.DirectorySnapshot
	provider  FeishuUpdateProvider
}

func NewFeishuUpdateDeliveryHandler(client *ent.Client, execution *database.ExecutionPolicy, directory database.DirectorySnapshot, provider FeishuUpdateProvider) *FeishuUpdateDeliveryHandler {
	return &FeishuUpdateDeliveryHandler{client, execution, directory, provider}
}

func (*FeishuUpdateDeliveryHandler) EventType() string       { return FeishuUpdateRequestedEventType }
func (*FeishuUpdateDeliveryHandler) SerialByAggregate() bool { return true }

func (h *FeishuUpdateDeliveryHandler) Deliver(ctx context.Context, event *ent.OutboxEvent) error {
	if h == nil || h.client == nil || h.execution == nil || h.provider == nil {
		return blockOutboxDelivery("Feishu update dependencies unavailable")
	}
	if event == nil || event.ID <= 0 || event.TenantID <= 0 || event.EventType != FeishuUpdateRequestedEventType || event.ClaimToken == "" {
		return blockOutboxDelivery("invalid Feishu update delivery identity")
	}
	if id, ok := tenantctx.TenantID(ctx); ok && id != event.TenantID {
		return blockOutboxDelivery("Feishu update tenant mismatch")
	}
	ctx = tenantctx.WithTenantID(ctx, event.TenantID)
	tx, err := h.client.Tx(ctx)
	if err != nil {
		return err
	}
	payload, err := h.validateUpdateTx(ctx, tx, event)
	if err != nil {
		_ = tx.Rollback()
		return feishuUpdatePreflightError(err)
	}
	if err = tx.Commit(); err != nil {
		return err
	}
	target, ok := h.provider(payload.TenantID)
	if !ok || target == nil || target.TaskDestinationIdentity() != payload.Destination {
		return blockOutboxDelivery("Feishu update destination changed or unavailable")
	}
	response, err := target.UpdateTask(ctx, payload.GUID, &payload.Task)
	if err != nil || response == nil || response.GUID != payload.GUID {
		return blockOutboxDelivery("delivery_unknown: Feishu update requires reconciliation")
	}
	// The provider must never redirect the mapping. Success after lost scope,
	// permission, lease, or durable receipt is ambiguous and cannot be auto-retried.
	tx, err = h.client.Tx(ctx)
	if err != nil {
		return blockOutboxDelivery("delivery_unknown: Feishu update receipt unavailable")
	}
	defer tx.Rollback()
	if _, err = h.validateUpdateTx(ctx, tx, event); err != nil {
		return blockOutboxDelivery("delivery_unknown: Feishu update receipt authorization changed")
	}
	current, ok := h.provider(payload.TenantID)
	if !ok || current == nil || current.TaskDestinationIdentity() != payload.Destination {
		return blockOutboxDelivery("delivery_unknown: Feishu destination changed before receipt")
	}
	member, err := h.execution.TenantPredicate(ctx, tx, payload.TenantID, feishuticketsync.FieldTenantID, feishuticketsync.FieldTicketID)
	if err != nil {
		return blockOutboxDelivery("delivery_unknown: Feishu update receipt scope unavailable")
	}
	count, err := tx.FeishuTicketSync.Update().Where(feishuticketsync.IDEQ(payload.MappingID), feishuticketsync.TenantIDEQ(payload.TenantID), feishuticketsync.TicketIDEQ(payload.WorkItemID), feishuticketsync.FeishuTaskIDEQ(payload.GUID), feishuticketsync.FeishuTaskGUIDEQ(payload.GUID), member).SetSyncStatus("synced").SetLastSyncDirection("itsm_to_feishu").SetLastSyncedAt(time.Now()).ClearErrorMessage().Save(ctx)
	if err != nil || count != 1 {
		return blockOutboxDelivery("delivery_unknown: Feishu mapping receipt could not be persisted")
	}
	if err = tx.Commit(); err != nil {
		return blockOutboxDelivery("delivery_unknown: Feishu mapping receipt could not commit")
	}
	return nil
}

func (h *FeishuUpdateDeliveryHandler) validateUpdateTx(ctx context.Context, tx *ent.Tx, event *ent.OutboxEvent) (feishuUpdatePayload, error) {
	var empty feishuUpdatePayload
	if err := h.execution.BindEnt(ctx, tx, event.TenantID); err != nil {
		return empty, err
	}
	stored, err := tx.OutboxEvent.Query().Where(outboxevent.IDEQ(event.ID), outboxevent.TenantIDEQ(event.TenantID), outboxevent.EventTypeEQ(FeishuUpdateRequestedEventType), outboxevent.EventIDEQ(event.EventID), outboxevent.StatusEQ(outboxEventStatusPublishing), outboxevent.ClaimTokenEQ(event.ClaimToken), outboxevent.ClaimExpiresAtGT(time.Now()), outboxevent.LastErrorEQ(outboxDeliveryAttemptPrefix+summarizeOutboxError(event.EventID)), orderedOutboxHead, func(s *entsql.Selector) { s.ForUpdate() }).Only(ctx)
	if err != nil {
		return empty, err
	}
	var payload feishuUpdatePayload
	decoder := json.NewDecoder(bytes.NewReader(stored.Payload))
	decoder.DisallowUnknownFields()
	if err = decoder.Decode(&payload); err != nil {
		return empty, blockOutboxDelivery("invalid Feishu update payload")
	}
	var trailing any
	if decoder.Decode(&trailing) != io.EOF {
		return empty, blockOutboxDelivery("invalid Feishu update payload")
	}
	if payload.TenantID != event.TenantID || payload.WorkItemID <= 0 || payload.ActorID <= 0 || payload.OperationID == "" || payload.RequestDigest == "" || payload.ResultVersion <= 0 || payload.MappingID <= 0 || payload.GUID == "" || payload.Destination == "" || payload.Task.GUID != "" || payload.Task.Name == "" || stored.ExecutionWorkItemID == nil || *stored.ExecutionWorkItemID != payload.WorkItemID || stored.AggregateType != "feishu_task" || stored.AggregateID != feishuUpdateAggregate(payload) || stored.EventID != feishuUpdateEventID(payload) {
		return empty, blockOutboxDelivery("Feishu update identity mismatch")
	}
	if err = h.execution.RequireEntMembers(ctx, tx, payload.TenantID, payload.WorkItemID); err != nil {
		return empty, err
	}
	item, err := tx.Ticket.Query().Where(ticket.IDEQ(payload.WorkItemID), ticket.TenantIDEQ(payload.TenantID), ticket.DeletedAtIsNil()).Only(ctx)
	if err != nil {
		return empty, err
	}
	if item.Version < payload.ResultVersion {
		return empty, blockOutboxDelivery("Feishu update WorkItem version or class mismatch")
	}
	actor, err := authorization.ResolveLifecycleActor(ctx, tx, h.directory, payload.ActorID, payload.TenantID)
	if err != nil {
		return empty, err
	}
	if _, _, err = authorization.AuthorizeWorkItem(ctx, tx.Client(), item.ID, payload.TenantID, authorization.EffectiveSessionRole(actor), "update"); err != nil {
		return empty, err
	}
	identity := creation.Identity{TenantID: payload.TenantID, ActorID: actor.ID, Role: authorization.EffectiveSessionRole(actor)}
	switch payload.Action {
	case "work_item.escalation.manual":
		if item.RecordClass != "generic" || payload.ResultStatus != "in_progress" {
			return empty, blockOutboxDelivery("invalid manual escalation update contract")
		}
		if err = authorization.RequireCurrentPermission(ctx, tx, identity, "ticket", "escalate"); err != nil {
			return empty, err
		}
	case "work_item.edit":
		if payload.ResultStatus == "" {
			return empty, blockOutboxDelivery("missing edit result status")
		}
		if err = authorization.RequireCurrentPermission(ctx, tx, identity, "ticket", "update"); err != nil {
			return empty, err
		}
		policy, policyErr := authorization.ResolveWorkItemPolicy(item.RecordClass)
		if policyErr != nil {
			return empty, blockOutboxDelivery("unsupported edit WorkItem class")
		}
		if err = authorization.RequireCurrentPermission(ctx, tx, identity, policy.Resource, policy.ResolveAction("update")); err != nil {
			return empty, err
		}
	default:
		return empty, blockOutboxDelivery("unsupported Feishu update command")
	}
	receipt, err := tx.AuditLog.Query().Where(auditlog.TenantIDEQ(payload.TenantID), auditlog.UserIDEQ(payload.ActorID), auditlog.OperationIDEQ(payload.OperationID), auditlog.ResourceEQ("work_item"), auditlog.ActionEQ(payload.Action), auditlog.PathEQ(strconv.Itoa(item.ID))).Only(ctx)
	if err != nil {
		return empty, err
	}
	if receipt.RequestDigest == nil || *receipt.RequestDigest != payload.RequestDigest || receipt.ResultVersion == nil || *receipt.ResultVersion != payload.ResultVersion || receipt.ResultStatus == nil || *receipt.ResultStatus != payload.ResultStatus {
		return empty, blockOutboxDelivery("Feishu update operation receipt mismatch")
	}
	var facts struct {
		FeishuUpdate *feishuUpdateReceipt `json:"feishuUpdate"`
	}
	if receipt.RequestBody == nil {
		return empty, blockOutboxDelivery("Feishu update receipt facts unavailable")
	}
	if err = json.Unmarshal([]byte(*receipt.RequestBody), &facts); err != nil {
		return empty, blockOutboxDelivery("Feishu update receipt facts unavailable")
	}
	digest, err := workitemmutation.Digest(payload)
	if err != nil {
		return empty, err
	}
	if facts.FeishuUpdate == nil || facts.FeishuUpdate.EventID != stored.EventID || facts.FeishuUpdate.PayloadDigest != digest {
		return empty, blockOutboxDelivery("Feishu update snapshot differs from operation receipt")
	}
	_, err = tx.FeishuTicketSync.Query().Where(feishuticketsync.IDEQ(payload.MappingID), feishuticketsync.TenantIDEQ(payload.TenantID), feishuticketsync.TicketIDEQ(item.ID), feishuticketsync.FeishuTaskIDEQ(payload.GUID), feishuticketsync.FeishuTaskGUIDEQ(payload.GUID)).Only(ctx)
	if err != nil {
		return empty, err
	}
	return payload, nil
}

func feishuUpdatePreflightError(err error) error {
	if ent.IsNotFound(err) || errors.Is(err, executionscope.ErrDenied) || errors.Is(err, creation.ErrPermissionDenied) || errors.Is(err, creation.ErrAuthenticationRequired) {
		return blockOutboxDelivery("Feishu update authority, lease, or target is unavailable")
	}
	if app, ok := common.AsAppError(err); ok && app.Code == common.ErrCodeForbidden {
		return blockOutboxDelivery("Feishu update actor is unavailable")
	}
	return err
}
