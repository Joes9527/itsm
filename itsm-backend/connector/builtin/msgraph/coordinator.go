package msgraph

import (
	"context"
	"fmt"
	"strings"
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
	ConversationID    string
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
	// FindTicketByConversationID 按邮件对话线程ID查找已关联的工单，用于识别用户回复。
	FindTicketByConversationID(ctx context.Context, tenantID int, conversationID string) (ticketID int, found bool, err error)
	// PostReplyComment 追加一条用户可见的邮件回复评论（IsInternal=false）。
	PostReplyComment(ctx context.Context, tenantID, ticketID, authorUserID int, content string) error
	// SaveAttachment 保存一个邮件附件到工单（复用 TicketAttachmentService）。
	SaveAttachment(ctx context.Context, tenantID, ticketID, uploaderID int, name, contentType string, data []byte) error
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

	// Self-loop guard: never turn a message the shared mailbox sent to
	// itself into a ticket. Without this, a mail loop (e.g. a sender's
	// out-of-office auto-responder bouncing back into the shared mailbox,
	// or any other bounce/auto-reply that lands back in the inbox) would
	// keep creating tickets forever. Note: Graph's delta response doesn't
	// currently get its internetMessageHeaders parsed into Message/deltaMessage,
	// so we can't additionally detect an inbound "Auto-Submitted" header —
	// this address-based guard is the scoped-down protection for now.
	if strings.EqualFold(m.FromAddress, conn.Mailbox()) {
		c.logger.Warnw("msgraph inbound message from the connector's own mailbox, skipping (loop guard)", "tenant_id", tenantID, "from", m.FromAddress)
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

	// 回复识别：邮件 conversationId 匹配已有工单 → 追加评论而非重复建单。
	// 需在 AI 分派之前判断，避免对回复内容做无意义的分派。
	if m.ConversationID != "" {
		ticketID, found, err := c.store.FindTicketByConversationID(ctx, tenantID, m.ConversationID)
		if err != nil {
			c.logger.Errorw("msgraph conversation lookup failed", "tenant_id", tenantID, "conversation_id", m.ConversationID, "error", err)
			return
		}
		if found {
			comment := fmt.Sprintf("[邮件回复] %s", body)
			if err := c.store.PostReplyComment(ctx, tenantID, ticketID, userID, comment); err != nil {
				c.logger.Warnw("msgraph failed to post reply comment", "tenant_id", tenantID, "ticket_id", ticketID, "error", err)
			}
			c.logger.Infow("msgraph reply appended to existing ticket", "tenant_id", tenantID, "ticket_id", ticketID, "from", m.FromAddress)
			return
		}
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
		ConversationID:    m.ConversationID,
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

	// 附件下载（尽力而为，失败不阻断建单）
	if m.HasAttachments {
		c.saveAttachments(ctx, tenantID, userID, ticketID, conn, m)
	}

	replyBody := renderReplyTemplate(ticketNumber, subject, "新建")
	replySubject := fmt.Sprintf("Re: [%s] %s", ticketNumber, subject)
	// 用 reply API 回复原邮件（而非 sendMail），让回复归入同一 conversation 线程，
	// 这样用户后续回复能通过 conversationId 匹配回本工单。
	if err := conn.GraphClient().ReplyMessage(ctx, conn.Mailbox(), m.ID, replySubject, replyBody); err != nil {
		c.logger.Warnw("msgraph failed to send confirmation reply", "tenant_id", tenantID, "ticket_id", ticketID, "error", err)
	}
}

// saveAttachments downloads and saves a message's attachments to the ticket,
// best-effort: failures are logged and summarized into a system comment, but
// never block ticket creation.
func (c *EmailPollingCoordinator) saveAttachments(ctx context.Context, tenantID, userID, ticketID int, conn *GraphConnector, m Message) {
	atts, err := conn.GraphClient().ListAttachments(ctx, conn.Mailbox(), m.ID)
	if err != nil {
		c.logger.Warnw("msgraph list attachments failed", "tenant_id", tenantID, "ticket_id", ticketID, "error", err)
		c.postAttachmentSummary(ctx, tenantID, ticketID, userID, "邮件附件列表获取失败")
		return
	}
	saved, failed := 0, 0
	for _, att := range atts {
		data := att.Data
		if data == nil { // 大附件走 $value 下载
			data, err = conn.GraphClient().DownloadAttachment(ctx, conn.Mailbox(), m.ID, att.ID)
			if err != nil {
				failed++
				c.logger.Warnw("msgraph download attachment failed", "tenant_id", tenantID, "ticket_id", ticketID, "name", att.Name, "error", err)
				continue
			}
		}
		if err := c.store.SaveAttachment(ctx, tenantID, ticketID, userID, att.Name, att.ContentType, data); err != nil {
			failed++
			c.logger.Warnw("msgraph save attachment failed", "tenant_id", tenantID, "ticket_id", ticketID, "name", att.Name, "error", err)
			continue
		}
		saved++
	}
	if failed > 0 {
		c.postAttachmentSummary(ctx, tenantID, ticketID, userID,
			fmt.Sprintf("邮件含 %d 个附件，成功保存 %d 个，%d 个未保存", len(atts), saved, failed))
	}
}

// postAttachmentSummary writes a system comment summarizing attachment results.
func (c *EmailPollingCoordinator) postAttachmentSummary(ctx context.Context, tenantID, ticketID, userID int, content string) {
	if err := c.store.PostSystemComment(ctx, tenantID, ticketID, userID, "[邮件附件] "+content); err != nil {
		c.logger.Warnw("msgraph failed to post attachment summary comment", "tenant_id", tenantID, "ticket_id", ticketID, "error", err)
	}
}
