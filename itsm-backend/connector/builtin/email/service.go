package email

import (
	"context"
	"fmt"
	"strings"
	"time"

	"itsm-backend/connector"
	"itsm-backend/ent"
	"itsm-backend/ent/tickettemplate"
	"go.uber.org/zap"
)

// TicketCreator abstracts ticket creation for the email connector.
type TicketCreator interface {
	CreateTicketFromEmail(ctx context.Context, subject, body, fromEmail string, tenantID int) (*ent.Ticket, error)
}

// Service wires the email connector to the ITSM ticket system.
type Service struct {
	connector *EmailConnector
	client    *ent.Client
	creator   TicketCreator
	logger    *zap.SugaredLogger
}

func NewService(client *ent.Client, creator TicketCreator, logger *zap.SugaredLogger) *Service {
	return &Service{
		connector: New(),
		client:    client,
		creator:   creator,
		logger:    logger,
	}
}

// GetConnector returns the underlying EmailConnector.
func (s *Service) GetConnector() *EmailConnector { return s.connector }

// Start begins IMAP polling and ticket creation.
func (s *Service) Start(ctx context.Context, cfg connector.Config, tenantID int) error {
	if err := s.connector.Init(ctx, cfg); err != nil {
		return err
	}

	s.connector.StartPolling(ctx, func(email EmailMessage) error {
		return s.handleInbound(ctx, email, tenantID)
	})

	return nil
}

func (s *Service) handleInbound(ctx context.Context, email EmailMessage, tenantID int) error {
	subject := email.Subject
	if subject == "" {
		subject = fmt.Sprintf("邮件工单 - %s - %s", email.From, time.Now().Format("2006-01-02 15:04"))
	}

	body := email.Body
	if body == "" {
		body = email.HTML
	}
	if body == "" {
		body = "(无正文)"
	}

	// Clean body - remove common email signatures and quoted replies
	body = cleanEmailBody(body)

	tkt, err := s.creator.CreateTicketFromEmail(ctx, subject, body, email.From, tenantID)
	if err != nil {
		s.logger.Errorw("failed to create ticket from email", "error", err, "from", email.From)
		return err
	}

	s.logger.Infow("ticket created from email", "ticket_id", tkt.ID, "from", email.From, "subject", subject)
	return nil
}

// SendAutoReply sends an auto-reply email for a ticket event.
func (s *Service) SendAutoReply(to, ticketNumber, title, status, tmpl string) error {
	msg := &connector.Message{
		Channel: to,
		Title:   fmt.Sprintf("Re: [%s] %s", ticketNumber, title),
		Type:    "text",
		Metadata: map[string]interface{}{
			"ticket_number": ticketNumber,
			"status":        status,
		},
	}

	if tmpl != "" {
		s.connector.cfg.Settings["reply_template"] = tmpl
	}

	return s.connector.Send(context.Background(), msg)
}

// MatchTemplate tries to find a matching ticket template based on email content.
func (s *Service) MatchTemplate(ctx context.Context, subject, body string, tenantID int) (*ent.TicketTemplate, error) {
	templates, err := s.client.TicketTemplate.Query().
		Where(tickettemplate.TenantIDEQ(tenantID), tickettemplate.IsActiveEQ(true)).
		All(ctx)
	if err != nil || len(templates) == 0 {
		return nil, err
	}

	text := strings.ToLower(subject + " " + body)

	// Priority matching: keywords → template category
	// Ordered by specificity: more specific keywords first
	type kwEntry struct{ keyword, category string }
	keywordList := []kwEntry{
		{"钓鱼", "SEC"}, {"病毒", "SEC"}, {"安全事件", "SEC"},
		{"打印机", "EUC"}, {"电脑", "EUC"}, {"开机", "EUC"}, {"软件安装", "EUC"},
		{"故障", "EUC"}, {"报修", "EUC"},
		{"服务器", "INF"}, {"虚拟机", "INF"}, {"存储", "INF"},
		{"VPN", "ACC"}, {"AD", "ACC"}, {"密码", "ACC"}, {"重置", "ACC"},
		{"账号", "ACC"},
		{"邮箱", "COL"}, {"邮件", "COL"}, {"Teams", "COL"},
		{"网络", "NET"}, {"上网", "NET"},
	}

	for _, kw := range keywordList {
		if strings.Contains(text, kw.keyword) {
			for _, t := range templates {
				if strings.EqualFold(t.Category, kw.category) {
					return t, nil
				}
			}
		}
	}

	// Default: first matching generic template
	for _, t := range templates {
		if t.Category == "GEN" {
			return t, nil
		}
	}

	return nil, nil
}

func cleanEmailBody(body string) string {
	// Remove common quoted reply markers
	lines := strings.Split(body, "\n")
	var cleaned []string
	for _, line := range lines {
		trimmed := strings.TrimSpace(line)
		// Skip quoted replies
		if strings.HasPrefix(trimmed, ">") {
			continue
		}
		// Stop at common signature markers
		if strings.HasPrefix(trimmed, "--") || strings.HasPrefix(trimmed, "---") {
			break
		}
		// Stop at "发件人:" or "From:" in body (forwarded email)
		if strings.HasPrefix(trimmed, "发件人:") || strings.HasPrefix(trimmed, "From:") {
			break
		}
		cleaned = append(cleaned, line)
	}
	return strings.TrimSpace(strings.Join(cleaned, "\n"))
}
