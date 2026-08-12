package msgraph

import (
	"context"
	"fmt"
	"sync"
	"time"

	"go.uber.org/zap"
)

// InboundTicketRequest is the minimal ticket-creation request the
// coordinator issues. Kept local (not itsm-backend/dto) so this package
// has zero dependency on ent/dto/service — see the design doc's decision
// to keep the connector package testable without a database.
type InboundTicketRequest struct {
	Title             string
	Description       string
	Priority          string // one of: low, medium, high, critical
	RequesterID       int
	CreatorEmail      string
	Source            string
	ExternalMessageID string
}

// TriageSuggestion is the AI classification result the coordinator applies
// (Priority) or records as advisory (Category) — see design decision 6.
type TriageSuggestion struct {
	Category    string
	Priority    string
	Confidence  float64
	Explanation string
}

// Triager produces a triage suggestion for a new inbound ticket. Backed by
// service.TriageService in production (adapter in
// internal/bootstrap/email_msgraph_wiring.go), stubbed in tests.
type Triager interface {
	Suggest(ctx context.Context, tenantID int, title, description string) TriageSuggestion
}

// TicketStore is everything the coordinator needs from the ticket system.
// Backed by *ent.Client + *service.TicketService in production (adapter in
// internal/bootstrap/email_msgraph_wiring.go), stubbed in tests.
type TicketStore interface {
	FindActiveUserByEmail(ctx context.Context, tenantID int, email string) (userID int, found bool, err error)
	TicketExistsForExternalMessage(ctx context.Context, tenantID int, externalMessageID string) (bool, error)
	CreateTicket(ctx context.Context, tenantID int, req InboundTicketRequest) (ticketID int, ticketNumber string, err error)
	PostSystemComment(ctx context.Context, tenantID, ticketID, authorUserID int, content string) error
}

// EmailPollingCoordinator polls one MS Graph mailbox per tenant (one
// goroutine per tenant, keyed by tenantID) and turns new inbound mail into
// tickets.
type EmailPollingCoordinator struct {
	store  TicketStore
	triage Triager
	logger *zap.SugaredLogger

	mu      sync.Mutex
	cancels map[int]context.CancelFunc // key: tenantID
}

func NewEmailPollingCoordinator(store TicketStore, triage Triager, logger *zap.SugaredLogger) *EmailPollingCoordinator {
	return &EmailPollingCoordinator{
		store:   store,
		triage:  triage,
		logger:  logger,
		cancels: make(map[int]context.CancelFunc),
	}
}

// Start begins polling for the given tenant using the given connector
// instance. If polling is already running for this tenant, the old poller
// is cancelled first (handles "admin changed the config" without leaking
// goroutines). The stop-old/install-new step happens under a single lock
// acquisition so concurrent Start calls for the same tenantID can never
// leave an old CancelFunc unreachable in c.cancels.
func (c *EmailPollingCoordinator) Start(ctx context.Context, tenantID int, conn *GraphConnector) {
	pollCtx, cancel := context.WithCancel(ctx)
	c.mu.Lock()
	if oldCancel, ok := c.cancels[tenantID]; ok {
		oldCancel()
	}
	c.cancels[tenantID] = cancel
	c.mu.Unlock()

	interval := time.Duration(conn.PollIntervalSeconds()) * time.Second

	go func() {
		deltaLink := ""
		c.pollOnce(pollCtx, tenantID, conn, &deltaLink)
		ticker := time.NewTicker(interval)
		defer ticker.Stop()
		for {
			select {
			case <-pollCtx.Done():
				return
			case <-ticker.C:
				c.pollOnce(pollCtx, tenantID, conn, &deltaLink)
			}
		}
	}()
}

// Stop cancels polling for a tenant, if running. Safe to call when nothing
// is running for that tenant.
func (c *EmailPollingCoordinator) Stop(tenantID int) {
	c.mu.Lock()
	cancel, ok := c.cancels[tenantID]
	delete(c.cancels, tenantID)
	c.mu.Unlock()
	if ok {
		cancel()
	}
}

func (c *EmailPollingCoordinator) pollOnce(ctx context.Context, tenantID int, conn *GraphConnector, deltaLink *string) {
	messages, next, err := conn.GraphClient().PollDelta(ctx, conn.Mailbox(), *deltaLink)
	if err != nil {
		c.logger.Warnw("msgraph poll failed", "tenant_id", tenantID, "error", err)
		return
	}
	if next != "" {
		*deltaLink = next
	}
	for _, m := range messages {
		c.handleMessage(ctx, tenantID, conn, m)
	}
}

func (c *EmailPollingCoordinator) handleMessage(ctx context.Context, tenantID int, conn *GraphConnector, m Message) {
	if m.InternetMessageID == "" {
		c.logger.Warnw("msgraph message missing internetMessageId, skipping", "tenant_id", tenantID, "graph_id", m.ID)
		return
	}

	exists, err := c.store.TicketExistsForExternalMessage(ctx, tenantID, m.InternetMessageID)
	if err != nil {
		c.logger.Errorw("msgraph dedup check failed", "tenant_id", tenantID, "error", err)
		return
	}
	if exists {
		return
	}

	userID, found, err := c.store.FindActiveUserByEmail(ctx, tenantID, m.FromAddress)
	if err != nil {
		c.logger.Errorw("msgraph requester lookup failed", "tenant_id", tenantID, "from", m.FromAddress, "error", err)
		return
	}
	if !found {
		c.logger.Warnw("msgraph inbound email from unregistered sender, skipping", "tenant_id", tenantID, "from", m.FromAddress)
		return
	}

	subject := m.Subject
	if subject == "" {
		subject = fmt.Sprintf("邮件工单 - %s - %s", m.FromAddress, m.ReceivedDateTime.Format("2006-01-02 15:04"))
	}
	body := cleanEmailBody(m.BodyContent)
	if body == "" {
		body = "(无正文)"
	}

	suggestion := c.triage.Suggest(ctx, tenantID, subject, body)
	priority := suggestion.Priority
	if priority == "" {
		priority = "medium"
	}

	ticketID, ticketNumber, err := c.store.CreateTicket(ctx, tenantID, InboundTicketRequest{
		Title:             subject,
		Description:       body,
		Priority:          priority,
		RequesterID:       userID,
		CreatorEmail:      m.FromAddress,
		Source:            "email",
		ExternalMessageID: m.InternetMessageID,
	})
	if err != nil {
		c.logger.Errorw("msgraph create ticket failed", "tenant_id", tenantID, "from", m.FromAddress, "error", err)
		return
	}
	c.logger.Infow("msgraph ticket created", "tenant_id", tenantID, "ticket_id", ticketID, "ticket_number", ticketNumber, "from", m.FromAddress)

	comment := fmt.Sprintf(
		"AI 分派参考：建议分类=%s（未自动应用，请人工确认），优先级=%s（已应用），置信度=%.0f%%，理由=%s",
		suggestion.Category, priority, suggestion.Confidence*100, suggestion.Explanation,
	)
	if err := c.store.PostSystemComment(ctx, tenantID, ticketID, userID, comment); err != nil {
		c.logger.Warnw("msgraph failed to post triage comment", "tenant_id", tenantID, "ticket_id", ticketID, "error", err)
	}

	replyBody := renderReplyTemplate(ticketNumber, subject, "新建")
	replySubject := fmt.Sprintf("Re: [%s] %s", ticketNumber, subject)
	if err := conn.GraphClient().SendMail(ctx, conn.Mailbox(), m.FromAddress, replySubject, replyBody); err != nil {
		c.logger.Warnw("msgraph failed to send confirmation reply", "tenant_id", tenantID, "ticket_id", ticketID, "error", err)
	}
}
