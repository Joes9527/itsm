package service

import (
	"context"
	"encoding/json"
	"errors"
	"testing"

	"github.com/stretchr/testify/require"
	"go.uber.org/zap"
	"itsm-backend/connector/builtin/msgraph"
	"itsm-backend/ent/enttest"
)

type recoveryGraph struct {
	subject   string
	body      string
	replies   int
	downloads int
	fail      bool
}

func (g *recoveryGraph) ListAttachments(context.Context, string, string) ([]msgraph.Attachment, error) {
	return []msgraph.Attachment{{ID: "attachment-a", Name: "evidence.txt", ContentType: "text/plain", Size: 8}}, nil
}
func (g *recoveryGraph) DownloadAttachment(context.Context, string, string, string) ([]byte, error) {
	g.downloads++
	if g.fail {
		return nil, errors.New("injected Graph failure")
	}
	return []byte("evidence"), nil
}
func (g *recoveryGraph) ReplyMessage(_ context.Context, _ string, _ string, subject string, body string) error {
	g.subject = subject
	g.body = body
	g.replies++
	if g.fail {
		return errors.New("unknown acceptance")
	}
	return nil
}

func TestEmailAttachmentDeliveryRetriesAndReplaysActualPersistence(t *testing.T) {
	ctx := context.Background()
	client := enttest.Open(t, "sqlite3", "file:"+t.Name()+"?mode=memory&cache=shared&_fk=1")
	defer client.Close()
	tenant := client.Tenant.Create().SetCode("email").SetName("email").SaveX(ctx)
	actor := client.User.Create().SetTenantID(tenant.ID).SetUsername("mail").SetEmail("mail@example.test").SetName("Mail").SetRole("super_admin").SetPasswordHash("unused").SaveX(ctx)
	item := client.Ticket.Create().SetTenantID(tenant.ID).SetRequesterID(actor.ID).SetTitle("Email").SetTicketNumber("MAIL-1").SetSource("email").SetExternalMessageID("message").SaveX(ctx)
	client.IntakeRequest.Create().SetTenantID(tenant.ID).SetActorTenantID(tenant.ID).SetActorID(actor.ID).SetRequesterID(actor.ID).SetChannel("email").SetOperation("create").SetIdempotencyKey("message").SetRequestDigest("digest").SetDigestVersion("intake-v3").SetStatus("completed").SetWorkItemID(item.ID).SaveX(ctx)
	payload, _ := json.Marshal(emailCreationDelivery{Number: item.TicketNumber, Title: item.Title, TenantID: tenant.ID, WorkItemID: item.ID, ActorID: actor.ID, Mailbox: "support@example.test", GraphMessageID: "graph-message", InternetMessageID: "message"})
	event := client.OutboxEvent.Create().SetTenantID(tenant.ID).SetEventID("email.attachments.requested:1").SetEventType(EmailAttachmentsRequestedEventType).SetAggregateType("work_item").SetAggregateID("1").SetPayload(payload).SaveX(ctx)
	storage := &recoveryStorage{objects: map[string][]byte{}}
	attachments := NewTicketAttachmentService(client, zap.NewNop().Sugar())
	attachments.SetStorage(storage)
	graph := &recoveryGraph{fail: true}
	handler := NewEmailAttachmentsDeliveryHandler(client, attachments, func(int) (GraphInboundClient, string, bool) { return graph, "support@example.test", true })
	require.ErrorContains(t, handler.Deliver(ctx, event), "injected Graph failure")
	require.Zero(t, client.TicketAttachment.Query().CountX(ctx))
	graph.fail = false
	require.NoError(t, handler.Deliver(ctx, event))
	require.NoError(t, handler.Deliver(ctx, event))
	require.Equal(t, 1, client.TicketAttachment.Query().CountX(ctx))
	require.Len(t, storage.objects, 1)

	confirmation := *event
	confirmation.EventType = EmailConfirmationRequestedEventType
	confirmation.EventID = "email.confirmation.requested:1"
	client.Ticket.UpdateOneID(item.ID).SetTitle("edited later").ExecX(ctx)
	confirmationHandler := NewEmailConfirmationDeliveryHandler(client, func(int) (GraphInboundClient, string, bool) { return graph, "support@example.test", true })
	require.NoError(t, confirmationHandler.Deliver(ctx, &confirmation))
	require.Contains(t, graph.body, "Email")
	require.NotContains(t, graph.body, "edited later")
	graph.fail = true
	require.ErrorContains(t, confirmationHandler.Deliver(ctx, &confirmation), "delivery_unknown")
	graph.fail = false
	confirmation.Payload = append([]byte(`{"tenantId":999,`), payload[1:]...)
	require.Error(t, confirmationHandler.Deliver(ctx, &confirmation))
	require.Equal(t, 2, graph.replies)
	event.TenantID++
	require.Error(t, handler.Deliver(ctx, event))
	require.Equal(t, 3, graph.downloads, "foreign event rejected before provider calls")
}
