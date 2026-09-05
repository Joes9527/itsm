package service

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"time"

	"itsm-backend/authorization"
	feishu "itsm-backend/connector/builtin/feishu"
	"itsm-backend/ent"
	"itsm-backend/ent/feishuticketsync"
	"itsm-backend/ent/intakerequest"
	"itsm-backend/ent/ticket"
	"itsm-backend/ent/user"
	creation "itsm-backend/handlers/common/workitemcreation"
)

const FeishuCreationRequestedEventType = "feishu.task.creation.requested"

type FeishuTaskCreator interface {
	CreateTask(context.Context, *feishu.FeishuTask) (*feishu.FeishuTask, error)
	TaskDestinationIdentity() string
}
type FeishuTaskProvider func(int) (FeishuTaskCreator, bool)
type feishuCreationPayload struct {
	TenantID    int               `json:"tenantId"`
	WorkItemID  int               `json:"workItemId"`
	ActorID     int               `json:"actorId"`
	Destination string            `json:"destination"`
	Task        feishu.FeishuTask `json:"task"`
}

func enqueueFeishuCreation(ctx context.Context, tx *ent.Tx, item *ent.Ticket, actorID int, destination string) error {
	task, err := prepareFeishuTask(ctx, tx.Client(), item)
	if err != nil {
		return creation.NewInfrastructureUnavailable("could not freeze Feishu task", err)
	}
	payload, err := json.Marshal(feishuCreationPayload{item.TenantID, item.ID, actorID, destination, *task})
	if err != nil {
		return creation.NewInternalFailure("could not encode Feishu creation", err)
	}
	_, err = NewOutboxEventRepository(tx.Client()).Enqueue(ctx, tx, NewOutboxEvent{TenantID: item.TenantID, EventID: fmt.Sprintf("feishu-create:%d", item.ID), EventType: FeishuCreationRequestedEventType, AggregateType: "work_item", AggregateID: fmt.Sprint(item.ID), Payload: payload})
	if err != nil {
		return creation.NewInfrastructureUnavailable("could not persist Feishu creation intent", err)
	}
	return nil
}

type FeishuCreationDeliveryHandler struct {
	owner    *FeishuSyncService
	provider FeishuTaskProvider
}

func NewFeishuCreationDeliveryHandler(owner *FeishuSyncService, provider FeishuTaskProvider) *FeishuCreationDeliveryHandler {
	return &FeishuCreationDeliveryHandler{owner, provider}
}
func (*FeishuCreationDeliveryHandler) EventType() string { return FeishuCreationRequestedEventType }

// Feishu creation has no provider idempotency contract: the shared worker fences
// crashes after attempt start. A committed mapping permits explicit safe replay.
func (h *FeishuCreationDeliveryHandler) Deliver(ctx context.Context, event *ent.OutboxEvent) error {
	if h == nil || h.owner == nil || h.owner.client == nil || h.provider == nil {
		return blockOutboxDelivery("Feishu creation owner is unavailable")
	}
	var payload feishuCreationPayload
	if event == nil || event.EventType != FeishuCreationRequestedEventType {
		return blockOutboxDelivery("invalid Feishu creation event")
	}
	decoder := json.NewDecoder(bytes.NewReader(event.Payload))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&payload); err != nil {
		return blockOutboxDelivery("invalid Feishu creation payload")
	}
	var trailing any
	if decoder.Decode(&trailing) != io.EOF {
		return blockOutboxDelivery("invalid Feishu creation payload")
	}
	if payload.TenantID <= 0 || payload.ActorID <= 0 || payload.WorkItemID <= 0 || event.TenantID != payload.TenantID || event.AggregateType != "work_item" || event.AggregateID != fmt.Sprint(payload.WorkItemID) || event.EventID != fmt.Sprintf("feishu-create:%d", payload.WorkItemID) || payload.Destination == "" || payload.Task.Name == "" || payload.Task.GUID != "" {
		return blockOutboxDelivery("Feishu creation event identity mismatch")
	}
	tx, err := h.owner.client.Tx(ctx)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	actor, err := tx.User.Query().Where(user.IDEQ(payload.ActorID), user.TenantIDEQ(payload.TenantID), user.ActiveEQ(true)).Only(ctx)
	if ent.IsNotFound(err) {
		return blockOutboxDelivery("Feishu creation actor is unavailable")
	}
	if err != nil {
		return err
	}
	item, err := tx.Ticket.Query().Where(ticket.IDEQ(payload.WorkItemID), ticket.TenantIDEQ(payload.TenantID), ticket.DeletedAtIsNil(), ticket.RecordClassEQ("generic")).Only(ctx)
	if ent.IsNotFound(err) {
		return blockOutboxDelivery("Feishu creation target is unavailable")
	}
	if err != nil {
		return err
	}
	identity := creation.Identity{TenantID: payload.TenantID, ActorID: actor.ID, RequesterID: item.RequesterID, Role: actor.Role, Channel: "internal"}
	if err := authorization.AuthorizeWorkItemCreation(ctx, tx, identity, creation.CreateWorkItemCommand{RecordClass: "generic"}); err != nil {
		return blockOutboxDelivery("Feishu creation permission is no longer valid")
	}
	complete, err := tx.IntakeRequest.Query().Where(intakerequest.TenantIDEQ(payload.TenantID), intakerequest.ActorIDEQ(payload.ActorID), intakerequest.WorkItemIDEQ(item.ID), intakerequest.StatusEQ("completed")).Exist(ctx)
	if err != nil {
		return err
	}
	if !complete {
		return blockOutboxDelivery("Feishu target has no completed Intake receipt")
	}
	mapped, err := tx.FeishuTicketSync.Query().Where(feishuticketsync.TenantIDEQ(payload.TenantID), feishuticketsync.TicketIDEQ(item.ID)).Only(ctx)
	if err != nil && !ent.IsNotFound(err) {
		return err
	}
	if mapped != nil {
		if mapped.FeishuTaskGUID == "" {
			return blockOutboxDelivery("Feishu mapping is incomplete")
		}
		return tx.Commit()
	}
	if err := tx.Commit(); err != nil {
		return err
	}
	provider, ok := h.provider(payload.TenantID)
	if !ok || provider == nil || provider.TaskDestinationIdentity() != payload.Destination {
		return blockOutboxDelivery("Feishu destination changed or is unavailable")
	}
	task, err := provider.CreateTask(ctx, &payload.Task)
	if err != nil || task == nil || task.GUID == "" {
		return blockOutboxDelivery("delivery_unknown: Feishu task creation requires reconciliation")
	}
	// Persist the provider receipt. Failure after external success is fenced by
	// the worker, so it cannot silently create a second remote task.
	_, err = h.owner.client.FeishuTicketSync.Create().SetTenantID(payload.TenantID).SetTicketID(item.ID).SetFeishuTaskID(task.GUID).SetFeishuTaskGUID(task.GUID).SetSyncStatus("synced").SetLastSyncDirection("itsm_to_feishu").SetLastSyncedAt(time.Now()).Save(ctx)
	if err != nil {
		return blockOutboxDelivery("delivery_unknown: Feishu task mapping requires reconciliation")
	}
	return nil
}
