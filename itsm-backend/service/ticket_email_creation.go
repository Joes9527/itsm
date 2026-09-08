package service

import (
	"context"
	"encoding/json"
	"fmt"

	"itsm-backend/ent"
	creation "itsm-backend/handlers/common/workitemcreation"
)

const EmailAttachmentsRequestedEventType = "email.attachments.requested"
const EmailConfirmationRequestedEventType = "email.confirmation.requested"

type emailCreationDelivery struct {
	Number            string `json:"number"`
	Title             string `json:"title"`
	TenantID          int    `json:"tenantId"`
	WorkItemID        int    `json:"workItemId"`
	ActorID           int    `json:"actorId"`
	Mailbox           string `json:"mailbox"`
	GraphMessageID    string `json:"graphMessageId"`
	InternetMessageID string `json:"internetMessageId"`
}

func writeEmailCreationSource(ctx context.Context, tx *ent.Tx, item *ent.Ticket, plan *creation.CreationPlan) error {
	input := plan.Resolved.Command.Email
	if input.TriageComment != "" {
		_, err := tx.TicketComment.Create().SetTenantID(item.TenantID).SetTicketID(item.ID).SetUserID(plan.Resolved.Identity.ActorID).SetContent("[系统 AI 分派] " + input.TriageComment).SetIsInternal(true).Save(ctx)
		if err != nil {
			return creation.NewInfrastructureUnavailable("could not persist email triage evidence", err)
		}
	}
	payload, err := json.Marshal(emailCreationDelivery{Number: item.TicketNumber, Title: item.Title, TenantID: item.TenantID, WorkItemID: item.ID, ActorID: plan.Resolved.Identity.ActorID, Mailbox: input.Mailbox, GraphMessageID: input.GraphMessageID, InternetMessageID: plan.Resolved.Command.SourceReference.EventID})
	if err != nil {
		return creation.NewInternalFailure("could not encode email source delivery", err)
	}
	eventTypes := []string{EmailConfirmationRequestedEventType}
	if input.HasAttachments {
		eventTypes = append(eventTypes, EmailAttachmentsRequestedEventType)
	}
	for _, eventType := range eventTypes {
		_, err := NewOutboxEventRepository(tx.Client()).Enqueue(ctx, tx, NewOutboxEvent{TenantID: item.TenantID, EventID: fmt.Sprintf("%s:%d", eventType, item.ID), EventType: eventType, AggregateType: "work_item", AggregateID: fmt.Sprint(item.ID), Payload: payload})
		if err != nil {
			return creation.NewInfrastructureUnavailable("could not enqueue recoverable email source delivery", err)
		}
	}
	return nil
}
