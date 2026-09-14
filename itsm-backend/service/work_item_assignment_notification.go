package service

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"strconv"

	"entgo.io/ent/dialect/sql"
	"itsm-backend/authorization"
	"itsm-backend/common/tenantctx"
	"itsm-backend/ent"
	"itsm-backend/ent/ticket"
	"itsm-backend/ent/ticketworkflowrecord"
	assignment "itsm-backend/handlers/common/workitemassignment"
)

// NewWorkItemAssignmentWriter must be used inside SessionReader.Write. The
// session owns verified directory provenance and the target transaction; callers
// still enforce professional permission and row visibility before Apply.
// Workflow/background callers inject their existing verified actor boundary
// directly into assignment.NewWriter; they must not fabricate a SessionSnapshot.
func NewWorkItemAssignmentWriter(session *authorization.SessionSnapshot) *assignment.Writer {
	return assignment.NewWriter(EnqueueWorkItemAssignment, func(ctx context.Context, client *ent.Client, cmd assignment.Command) error {
		return session.ValidateAssignmentIdentities(ctx, client, cmd.ActorID, cmd.ActorTenantID, cmd.TenantID, cmd.AssigneeID)
	})
}

func EnqueueWorkItemAssignment(ctx context.Context, client *ent.Client, event assignment.Event) error {
	payload, err := json.Marshal(event)
	if err != nil {
		return err
	}
	_, err = NewOutboxEventRepository(client).Enqueue(ctx, nil, NewOutboxEvent{EventID: event.EventID, EventType: assignment.EventType, TenantID: event.Command.TenantID, AggregateType: "work_item", AggregateID: strconv.Itoa(event.Command.WorkItemID), Payload: payload})
	return err
}

type WorkItemAssignmentNotificationHandler struct {
	client        *ent.Client
	notifications *TicketNotificationService
}

func NewWorkItemAssignmentNotificationHandler(client *ent.Client, notifications *TicketNotificationService) *WorkItemAssignmentNotificationHandler {
	return &WorkItemAssignmentNotificationHandler{client: client, notifications: notifications}
}
func (*WorkItemAssignmentNotificationHandler) EventType() string { return assignment.EventType }

// ReplaySafe covers only the atomic deterministic notification intent below.
// It does NOT declare any external notification transport replay-safe.
func (*WorkItemAssignmentNotificationHandler) ReplaySafe() bool { return true }

func decodeWorkItemAssignment(event *ent.OutboxEvent) (assignment.Event, error) {
	var p assignment.Event
	if event == nil || event.EventType != assignment.EventType {
		return p, blockOutboxDelivery("invalid assignment event")
	}
	decoder := json.NewDecoder(bytes.NewReader(event.Payload))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&p); err != nil {
		return p, blockOutboxDelivery("invalid assignment payload")
	}
	var extra any
	if decoder.Decode(&extra) != io.EOF || p.Command.Validate() != nil || p.PreviousAssigneeID < 0 || p.PreviousAssigneeID == p.Command.AssigneeID || p.Version != p.Command.ExpectedVersion+1 ||
		p.EventID != assignment.EventID(p.Command.TenantID, p.Command.WorkItemID, p.Version) || p.EventID != event.EventID || p.Command.TenantID != event.TenantID ||
		event.AggregateType != "work_item" || event.AggregateID != strconv.Itoa(p.Command.WorkItemID) {
		return p, blockOutboxDelivery("assignment event identity mismatch")
	}
	return p, nil
}
func (h *WorkItemAssignmentNotificationHandler) Deliver(ctx context.Context, event *ent.OutboxEvent) error {
	p, err := decodeWorkItemAssignment(event)
	if err != nil {
		return err
	}
	if tenantID, ok := tenantctx.TenantID(ctx); ok && tenantID != p.Command.TenantID {
		return blockOutboxDelivery("assignment delivery tenant context mismatch")
	}
	if h == nil || h.client == nil || h.notifications == nil {
		return fmt.Errorf("assignment notification dependencies unavailable")
	}
	tx, err := h.client.Tx(ctx)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	// Serialize duplicate materialization and compare immutable audit evidence,
	// never reinterpret a historical event using the current owner.
	item, err := tx.Ticket.Query().Where(ticket.ID(p.Command.WorkItemID), ticket.TenantID(p.Command.TenantID), func(s *sql.Selector) {
		if s.Dialect() != "sqlite3" {
			s.ForUpdate()
		}
	}).Only(ctx)
	if err != nil {
		return workflowStartReferenceError(err, "assignment WorkItem")
	}
	records, err := tx.TicketWorkflowRecord.Query().Where(ticketworkflowrecord.TicketID(item.ID), ticketworkflowrecord.TenantID(item.TenantID), ticketworkflowrecord.Action("assign"), ticketworkflowrecord.OperatorID(p.Command.ActorID)).All(ctx)
	if err != nil {
		return err
	}
	matched := 0
	for _, record := range records {
		if record.Metadata["eventId"] != p.EventID {
			continue
		}
		version, vok := numericInt(record.Metadata["version"])
		actorTenant, aok := numericInt(record.Metadata["actorTenantId"])
		if !vok || !aok || version != p.Version || actorTenant != p.Command.ActorTenantID || record.FromUserID != p.PreviousAssigneeID || record.ToUserID != p.Command.AssigneeID || record.Metadata["source"] != p.Command.Source || record.Reason != p.Command.Reason {
			return blockOutboxDelivery("assignment audit mismatch")
		}
		matched++
	}
	if matched != 1 {
		return blockOutboxDelivery("assignment audit unavailable or ambiguous")
	}
	repository := NewOutboxEventRepository(tx.Client())
	completed, err := repository.DeliveryCompleted(ctx, event)
	if err != nil {
		return err
	}
	if completed {
		return tx.Commit()
	}
	if p.Command.AssigneeID == 0 {
		if err = repository.RecordDeliveryCompleted(ctx, event); err != nil {
			return err
		}
		return tx.Commit()
	}
	content := fmt.Sprintf("工单 #%s 已分配给您", item.TicketNumber)
	if err = h.notifications.enqueueTicketNotificationTx(ctx, tx, item, "ticket_assigned", content, p.EventID, []int{p.Command.AssigneeID}); err != nil {
		return err
	}
	if err = repository.RecordDeliveryCompleted(ctx, event); err != nil {
		return err
	}
	return tx.Commit()
}
