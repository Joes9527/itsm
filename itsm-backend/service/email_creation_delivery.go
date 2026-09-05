package service

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"strings"

	"itsm-backend/authorization"
	"itsm-backend/connector/builtin/msgraph"
	"itsm-backend/ent"
	"itsm-backend/ent/intakerequest"
	"itsm-backend/ent/ticket"
	"itsm-backend/ent/user"
	creation "itsm-backend/handlers/common/workitemcreation"
)

// GraphInboundClient is the established provider boundary used by inbound
// creation delivery. It performs external reads/replies after Intake commits.
type GraphInboundClient interface {
	ListAttachments(context.Context, string, string) ([]msgraph.Attachment, error)
	DownloadAttachment(context.Context, string, string, string) ([]byte, error)
	ReplyMessage(context.Context, string, string, string, string) error
}
type GraphInboundProvider func(int) (GraphInboundClient, string, bool)

type EmailAttachmentsDeliveryHandler struct {
	client      *ent.Client
	attachments *TicketAttachmentService
	provider    GraphInboundProvider
}

func NewEmailAttachmentsDeliveryHandler(client *ent.Client, attachments *TicketAttachmentService, provider GraphInboundProvider) *EmailAttachmentsDeliveryHandler {
	return &EmailAttachmentsDeliveryHandler{client, attachments, provider}
}
func (*EmailAttachmentsDeliveryHandler) EventType() string { return EmailAttachmentsRequestedEventType }
func (*EmailAttachmentsDeliveryHandler) ReplaySafe() bool  { return true }
func (h *EmailAttachmentsDeliveryHandler) Deliver(ctx context.Context, event *ent.OutboxEvent) error {
	payload, _, err := loadEmailCreationDelivery(ctx, h.client, event, EmailAttachmentsRequestedEventType)
	if err != nil {
		return err
	}
	if h.attachments == nil || h.provider == nil {
		return blockOutboxDelivery("email attachment delivery is not configured")
	}
	graph, mailbox, ok := h.provider(payload.TenantID)
	if !ok || graph == nil || !strings.EqualFold(mailbox, payload.Mailbox) {
		return blockOutboxDelivery("email delivery mailbox is not configured for this tenant")
	}
	attachments, err := graph.ListAttachments(ctx, payload.Mailbox, payload.GraphMessageID)
	if err != nil {
		return err
	}
	for _, attachment := range attachments {
		if strings.TrimSpace(attachment.ID) == "" {
			return blockOutboxDelivery("email attachment has no stable provider identity")
		}
		data := attachment.Data
		if data == nil {
			data, err = graph.DownloadAttachment(ctx, payload.Mailbox, payload.GraphMessageID, attachment.ID)
			if err != nil {
				return err
			}
		}
		_, err = h.attachments.UploadEmailAttachment(ctx, payload.WorkItemID, &FileHeader{Filename: attachment.Name, ContentType: attachment.ContentType, Size: int64(len(data)), Reader: bytes.NewReader(data)}, payload.ActorID, payload.TenantID, payload.Mailbox, payload.InternetMessageID, attachment.ID)
		if err != nil {
			var failure *creation.IntakeError
			if errors.As(err, &failure) && !failure.Retryable {
				return blockOutboxDelivery(failure.Message)
			}
			return err
		}
	}
	return nil
}

type EmailConfirmationDeliveryHandler struct {
	client   *ent.Client
	provider GraphInboundProvider
}

func NewEmailConfirmationDeliveryHandler(client *ent.Client, provider GraphInboundProvider) *EmailConfirmationDeliveryHandler {
	return &EmailConfirmationDeliveryHandler{client, provider}
}
func (*EmailConfirmationDeliveryHandler) EventType() string {
	return EmailConfirmationRequestedEventType
}
func (h *EmailConfirmationDeliveryHandler) Deliver(ctx context.Context, event *ent.OutboxEvent) error {
	payload, _, err := loadEmailCreationDelivery(ctx, h.client, event, EmailConfirmationRequestedEventType)
	if err != nil {
		return err
	}
	if h.provider == nil {
		return blockOutboxDelivery("email confirmation delivery is not configured")
	}
	graph, mailbox, ok := h.provider(payload.TenantID)
	if !ok || graph == nil || !strings.EqualFold(mailbox, payload.Mailbox) {
		return blockOutboxDelivery("email delivery mailbox is not configured for this tenant")
	}
	// Graph reply has no idempotent acceptance API. An uncertain send is surfaced
	// for reconciliation, and the shared worker fences crashes after attempt start.
	if err := graph.ReplyMessage(ctx, payload.Mailbox, payload.GraphMessageID, fmt.Sprintf("Re: [%s] %s", payload.Number, payload.Title), fmt.Sprintf("工单 %s 已创建：%s", payload.Number, payload.Title)); err != nil {
		return blockOutboxDelivery("delivery_unknown: Graph reply result requires manual reconciliation")
	}
	return nil
}

func loadEmailCreationDelivery(ctx context.Context, client *ent.Client, event *ent.OutboxEvent, eventType string) (emailCreationDelivery, *ent.Ticket, error) {
	var payload emailCreationDelivery
	if event == nil || event.EventType != eventType {
		return payload, nil, blockOutboxDelivery("invalid email source event type")
	}
	shape := json.NewDecoder(bytes.NewReader(event.Payload))
	opening, err := shape.Token()
	if err != nil || opening != json.Delim('{') {
		return payload, nil, blockOutboxDelivery("invalid email source event payload")
	}
	seen := map[string]bool{}
	for shape.More() {
		key, err := shape.Token()
		if err != nil {
			return payload, nil, blockOutboxDelivery("invalid email source event payload")
		}
		name, ok := key.(string)
		if !ok || seen[name] {
			return payload, nil, blockOutboxDelivery("duplicate email source event field")
		}
		seen[name] = true
		var value json.RawMessage
		if err := shape.Decode(&value); err != nil || bytes.Equal(bytes.TrimSpace(value), []byte("null")) {
			return payload, nil, blockOutboxDelivery("invalid email source event value")
		}
	}
	decoder := json.NewDecoder(bytes.NewReader(event.Payload))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&payload); err != nil {
		return payload, nil, blockOutboxDelivery("invalid email source event payload")
	}
	var trailing any
	if err := decoder.Decode(&trailing); err != io.EOF {
		return payload, nil, blockOutboxDelivery("invalid email source event payload")
	}
	if payload.Number == "" || payload.Title == "" || payload.TenantID <= 0 || payload.WorkItemID <= 0 || payload.ActorID <= 0 || event.TenantID != payload.TenantID || event.AggregateType != "work_item" || event.AggregateID != fmt.Sprint(payload.WorkItemID) || event.EventID != fmt.Sprintf("%s:%d", eventType, payload.WorkItemID) || strings.TrimSpace(payload.Mailbox) == "" || strings.TrimSpace(payload.GraphMessageID) == "" || strings.TrimSpace(payload.InternetMessageID) == "" {
		return payload, nil, blockOutboxDelivery("email source event identity mismatch")
	}
	if client == nil {
		return payload, nil, blockOutboxDelivery("email source reader is not configured")
	}
	tx, err := client.Tx(ctx)
	if err != nil {
		return payload, nil, err
	}
	defer tx.Rollback()
	actor, err := tx.User.Query().Where(user.IDEQ(payload.ActorID), user.TenantIDEQ(payload.TenantID), user.ActiveEQ(true)).Only(ctx)
	if ent.IsNotFound(err) {
		return payload, nil, blockOutboxDelivery("email source actor is unavailable")
	}
	if err != nil {
		return payload, nil, err
	}
	identity := creation.Identity{TenantID: payload.TenantID, ActorID: payload.ActorID, RequesterID: payload.ActorID, Role: actor.Role, Channel: "email", Provider: "msgraph_email"}
	for _, action := range []string{"read", "write"} {
		if err := authorization.RequireCurrentPermission(ctx, tx, identity, "ticket", action); err != nil {
			var failure *creation.IntakeError
			if errors.As(err, &failure) && !failure.Retryable {
				return payload, nil, blockOutboxDelivery(failure.Message)
			}
			return payload, nil, err
		}
	}
	item, err := tx.Ticket.Query().Where(ticket.IDEQ(payload.WorkItemID), ticket.TenantIDEQ(payload.TenantID), ticket.RequesterIDEQ(payload.ActorID), ticket.SourceEQ("email"), ticket.ExternalMessageIDEQ(payload.InternetMessageID), ticket.DeletedAtIsNil()).Only(ctx)
	if ent.IsNotFound(err) {
		return payload, nil, blockOutboxDelivery("email source work item is unavailable")
	}
	if err != nil {
		return payload, nil, err
	}
	complete, err := tx.IntakeRequest.Query().Where(intakerequest.TenantIDEQ(payload.TenantID), intakerequest.ActorIDEQ(payload.ActorID), intakerequest.RequesterIDEQ(payload.ActorID), intakerequest.ChannelEQ("email"), intakerequest.StatusEQ("completed"), intakerequest.WorkItemIDEQ(item.ID)).Exist(ctx)
	if err != nil {
		return payload, nil, err
	}
	if !complete {
		return payload, nil, blockOutboxDelivery("email source has no completed creation receipt")
	}
	if err := tx.Commit(); err != nil {
		return payload, nil, err
	}
	return payload, item, nil
}
