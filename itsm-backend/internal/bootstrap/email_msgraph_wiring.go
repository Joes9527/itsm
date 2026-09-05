package bootstrap

import (
	"context"
	"crypto/sha256"
	"fmt"
	"strings"

	"go.uber.org/zap"

	"itsm-backend/authorization"
	"itsm-backend/common/tenantctx"
	"itsm-backend/connector/builtin/msgraph"
	"itsm-backend/controller"
	"itsm-backend/ent"
	"itsm-backend/ent/ticket"
	"itsm-backend/ent/user"
	creation "itsm-backend/handlers/common/workitemcreation"
	"itsm-backend/service"
)

// msgraphInboundTicketRequest is a bootstrap-local alias so this package's
// tests can build a fixture without importing connector/builtin/msgraph
// just for a struct literal. It is structurally identical to
// msgraph.InboundTicketRequest by construction (see ticketStoreAdapter.CreateTicket).
type msgraphInboundTicketRequest = msgraph.InboundTicketRequest

// ticketStoreAdapter implements msgraph.TicketStore over the real ent
// client and TicketService, so connector/builtin/msgraph never needs to
// import ent/dto/service directly.
type ticketStoreAdapter struct {
	client      *ent.Client
	creationApp creation.Application
}

func newTicketStoreAdapter(client *ent.Client, app creation.Application) *ticketStoreAdapter {
	if app == nil {
		panic("email creation application is required")
	}
	return &ticketStoreAdapter{client: client, creationApp: app}
}

func (a *ticketStoreAdapter) FindActiveUserByEmail(ctx context.Context, tenantID int, email string) (int, bool, error) {
	ctx = tenantctx.WithTenantID(ctx, tenantID)
	u, err := a.client.User.Query().
		Where(user.EmailEqualFold(email), user.TenantIDEQ(tenantID), user.ActiveEQ(true)).
		Only(ctx)
	if err != nil {
		if ent.IsNotFound(err) {
			return 0, false, nil
		}
		return 0, false, fmt.Errorf("lookup user by email: %w", err)
	}
	return u.ID, true, nil
}

func (a *ticketStoreAdapter) TicketExistsForExternalMessage(ctx context.Context, tenantID int, externalMessageID string) (bool, error) {
	ctx = tenantctx.WithTenantID(ctx, tenantID)
	return a.client.Ticket.Query().
		Where(ticket.TenantIDEQ(tenantID), ticket.ExternalMessageIDEQ(externalMessageID)).
		Exist(ctx)
}

func (a *ticketStoreAdapter) CreateTicket(ctx context.Context, tenantID int, req msgraph.InboundTicketRequest) (int, string, error) {
	ctx = tenantctx.WithTenantID(ctx, tenantID)
	actor, err := a.client.User.Query().Where(user.IDEQ(req.RequesterID), user.TenantIDEQ(tenantID), user.ActiveEQ(true), user.EmailEqualFold(req.CreatorEmail)).Only(ctx)
	if err != nil {
		return 0, "", creation.NewAuthenticationRequired("verified email requester is unavailable", err)
	}
	identity := creation.Identity{TenantID: tenantID, ActorID: actor.ID, RequesterID: actor.ID, Role: actor.Role, Channel: "email", Provider: "msgraph_email"}
	result, err := a.creationApp.Create(ctx, identity, creation.CreateWorkItemCommand{
		RecordClass: creation.RecordClassGeneric, IntakeKind: creation.IntakeKindGeneric, Confirmation: "confirmed",
		IdempotencyKey: fmt.Sprintf("email:%x", sha256.Sum256([]byte(strings.ToLower(strings.TrimSpace(req.Mailbox))+"\x00"+req.ExternalMessageID))),
		Title:          req.Title, Description: req.Description, Priority: req.Priority,
		SourceReference: &creation.SourceReference{Provider: "msgraph_email", EventID: req.ExternalMessageID, ConversationID: req.ConversationID},
		Email:           &creation.EmailInput{Mailbox: req.Mailbox, GraphMessageID: req.GraphMessageID, SenderEmail: req.CreatorEmail, HasAttachments: req.HasAttachments, TriageComment: req.TriageComment},
	})
	if err != nil {
		return 0, "", err
	}
	return result.WorkItemID, result.Number, nil
}

// FindTicketByConversationID 按邮件对话线程ID查找已关联的工单。
func (a *ticketStoreAdapter) FindTicketByConversationID(ctx context.Context, tenantID int, conversationID string) (int, bool, error) {
	ctx = tenantctx.WithTenantID(ctx, tenantID)
	t, err := a.client.Ticket.Query().
		Where(ticket.TenantIDEQ(tenantID), ticket.ConversationIDEQ(conversationID)).
		Only(ctx)
	if err != nil {
		if ent.IsNotFound(err) {
			return 0, false, nil
		}
		return 0, false, fmt.Errorf("lookup ticket by conversation id: %w", err)
	}
	return t.ID, true, nil
}

// PostReplyComment 追加一条用户可见的邮件回复评论（IsInternal=false）。
func (a *ticketStoreAdapter) PostReplyComment(ctx context.Context, tenantID, ticketID, authorUserID int, content string) error {
	ctx = tenantctx.WithTenantID(ctx, tenantID)
	tx, err := a.client.Tx(ctx)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	actor, err := tx.User.Query().Where(user.IDEQ(authorUserID), user.TenantIDEQ(tenantID), user.ActiveEQ(true)).Only(ctx)
	if err != nil {
		return err
	}
	identity := creation.Identity{TenantID: tenantID, ActorID: actor.ID, RequesterID: actor.ID, Role: actor.Role, Channel: "email", Provider: "msgraph_email"}
	for _, action := range []string{"read", "write"} {
		if err := authorization.RequireCurrentPermission(ctx, tx, identity, "ticket", action); err != nil {
			return err
		}
	}
	item, err := tx.Ticket.Query().Where(ticket.IDEQ(ticketID), ticket.TenantIDEQ(tenantID), ticket.RequesterIDEQ(actor.ID), ticket.SourceEQ("email"), ticket.DeletedAtIsNil()).Only(ctx)
	if err != nil {
		return creation.NewPermissionDenied("email reply target is unavailable", err)
	}
	if err := tx.TicketComment.Create().SetTicketID(item.ID).SetUserID(actor.ID).SetContent(content).SetIsInternal(false).SetTenantID(tenantID).Exec(ctx); err != nil {
		return err
	}
	return tx.Commit()
}

// triagerAdapter implements msgraph.Triager over the real TriageService.
type triagerAdapter struct {
	triageService *service.TriageService
}

func newTriagerAdapter(triageService *service.TriageService) *triagerAdapter {
	return &triagerAdapter{triageService: triageService}
}

func (a *triagerAdapter) Suggest(ctx context.Context, tenantID int, title, description string) msgraph.TriageSuggestion {
	result := a.triageService.SuggestForTenant(ctx, title, description, tenantID)
	return msgraph.TriageSuggestion{
		Category:    result.Category,
		Priority:    strings.ToLower(result.Priority),
		Confidence:  result.Confidence,
		Explanation: result.Explanation,
	}
}

// wireEmailMsgraphConnector constructs the MS Graph email polling
// coordinator and registers it with the connector controller. Call once
// during bootstrap, after ticketService and triageService are constructed.
// Safe to call even if no msgraph-email connector is ever provisioned by
// any tenant — SetEmailCoordinator just makes the capability available.
func wireEmailMsgraphConnector(
	client *ent.Client,
	creationApp creation.Application,
	triageService *service.TriageService,
	connectorController *controller.ConnectorController,
	logger *zap.SugaredLogger,
) {
	store := newTicketStoreAdapter(client, creationApp)
	triager := newTriagerAdapter(triageService)
	coordinator := msgraph.NewEmailPollingCoordinator(store, triager, logger.Named("connector.msgraph.coordinator"))
	connectorController.SetEmailCoordinator(coordinator)
}
