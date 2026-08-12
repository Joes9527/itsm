package bootstrap

import (
	"context"
	"fmt"
	"strings"
	"time"

	"go.uber.org/zap"

	"itsm-backend/connector/builtin/msgraph"
	"itsm-backend/controller"
	"itsm-backend/dto"
	"itsm-backend/ent"
	"itsm-backend/ent/ticket"
	"itsm-backend/ent/user"
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
	client        *ent.Client
	ticketService *service.TicketService
}

func newTicketStoreAdapter(client *ent.Client, ticketService *service.TicketService) *ticketStoreAdapter {
	return &ticketStoreAdapter{client: client, ticketService: ticketService}
}

func (a *ticketStoreAdapter) FindActiveUserByEmail(ctx context.Context, tenantID int, email string) (int, bool, error) {
	u, err := a.client.User.Query().
		Where(user.EmailEqualFold(email), user.TenantIDEQ(tenantID), user.ActiveEQ(true)).
		First(ctx)
	if err != nil {
		if ent.IsNotFound(err) {
			return 0, false, nil
		}
		return 0, false, fmt.Errorf("lookup user by email: %w", err)
	}
	return u.ID, true, nil
}

func (a *ticketStoreAdapter) TicketExistsForExternalMessage(ctx context.Context, tenantID int, externalMessageID string) (bool, error) {
	return a.client.Ticket.Query().
		Where(ticket.TenantIDEQ(tenantID), ticket.ExternalMessageIDEQ(externalMessageID)).
		Exist(ctx)
}

func (a *ticketStoreAdapter) CreateTicket(ctx context.Context, tenantID int, req msgraph.InboundTicketRequest) (int, string, error) {
	tkt, err := a.ticketService.CreateTicket(ctx, &dto.CreateTicketRequest{
		Title:             req.Title,
		Description:       req.Description,
		Priority:          req.Priority,
		RequesterID:       req.RequesterID,
		CreatorEmail:      req.CreatorEmail,
		Source:            req.Source,
		ExternalMessageID: req.ExternalMessageID,
	}, tenantID)
	if err != nil {
		return 0, "", err
	}

	// Tickets created via the HTTP API get an audit_log row through
	// middleware.AuditMiddleware. This path bypasses HTTP entirely (it's a
	// background polling goroutine), so without this it would be LESS
	// auditable than manual ticket creation — backwards for a
	// connector-triggered, high-risk automated action. Mirror the shape
	// middleware/audit.go writes, using placeholder path/method values that
	// make the non-HTTP origin of this record clear.
	a.recordCreateTicketAudit(ctx, tenantID, req.RequesterID, tkt.ID)

	return tkt.ID, tkt.TicketNumber, nil
}

// recordCreateTicketAudit writes one audit_log row for a ticket created by
// the msgraph-email polling coordinator. Best-effort: a failure to write
// the audit record must not fail (or roll back) the ticket creation that
// already succeeded, but it is logged loudly so it doesn't go unnoticed.
func (a *ticketStoreAdapter) recordCreateTicketAudit(ctx context.Context, tenantID, userID, ticketID int) {
	err := a.client.AuditLog.Create().
		SetCreatedAt(time.Now()).
		SetTenantID(tenantID).
		SetUserID(userID).
		SetResource("ticket").
		SetAction("create").
		SetPath("connector:msgraph-email").
		SetMethod("CONNECTOR").
		SetStatusCode(201).
		SetRequestBody(fmt.Sprintf(`{"ticketId":%d,"source":"email"}`, ticketID)).
		Exec(ctx)
	if err != nil {
		zap.S().Errorw("msgraph: failed to write audit log for connector-created ticket", "tenant_id", tenantID, "ticket_id", ticketID, "error", err)
	}
}

// PostSystemComment writes an internal ticket comment directly via ent,
// bypassing TicketCommentService's permission gate (which requires the
// authoring user to hold an admin/agent/manager/technician/security role).
// This is a backend-automation write, not a simulated user action — same
// reasoning the BPMN engine and escalation service already use elsewhere
// for system-generated ticket state changes. authorUserID must be an
// existing user in the tenant (the ticket's requester, satisfying the
// comment's required FK) — the content text itself makes clear it's
// system-generated.
func (a *ticketStoreAdapter) PostSystemComment(ctx context.Context, tenantID, ticketID, authorUserID int, content string) error {
	_, err := a.client.TicketComment.Create().
		SetTicketID(ticketID).
		SetUserID(authorUserID).
		SetContent("[系统 AI 分派] " + content).
		SetIsInternal(true).
		SetTenantID(tenantID).
		Save(ctx)
	return err
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
	ticketService *service.TicketService,
	triageService *service.TriageService,
	connectorController *controller.ConnectorController,
	logger *zap.SugaredLogger,
) {
	store := newTicketStoreAdapter(client, ticketService)
	triager := newTriagerAdapter(triageService)
	coordinator := msgraph.NewEmailPollingCoordinator(store, triager, logger.Named("connector.msgraph.coordinator"))
	connectorController.SetEmailCoordinator(coordinator)
}
