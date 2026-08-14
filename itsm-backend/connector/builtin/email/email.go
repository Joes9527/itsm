// Package email — Email Connector for ITSM
// Inbound: IMAP polling → auto-create tickets
// Outbound: SMTP auto-reply on ticket events
package email

import (
	"context"
	"crypto/tls"
	"fmt"
	"io"
	"mime"
	"mime/multipart"
	"mime/quotedprintable"
	"net"
	"net/mail"
	"net/smtp"
	"net/textproto"
	"strings"
	"sync"
	"time"

	"go.uber.org/zap"
	"itsm-backend/connector"
)

func init() {
	connector.MustRegister(func() connector.Connector { return New() })
}

type EmailConnector struct {
	cfg       connector.Config
	logger    *zap.SugaredLogger
	mu        sync.Mutex
	cancel    context.CancelFunc
	lastUID   uint32
	pollCount int
}

func New() *EmailConnector { return &EmailConnector{} }

func (e *EmailConnector) Manifest() connector.Manifest {
	return connector.Manifest{
		Name:         "email",
		Version:      "1.0.0",
		Title:        "邮件连接器",
		Provider:     "itsm",
		Type:         connector.TypeEmail,
		Description:  "IMAP拉取邮件自动创建工单 + SMTP自动回复",
		Capabilities: []connector.Capability{
			connector.CapSendMessage,
			connector.CapReceiveMessage,
			connector.CapCreateTicket,
		},
		Tags:                []string{"email", "imap", "smtp"},
		IsOfficial:          true,
		RequiredPermissions: []string{"connector:write", "ticket:write"},
	}
}

func (e *EmailConnector) Init(_ context.Context, cfg connector.Config) error {
	e.cfg = cfg
	if e.logger == nil {
		e.logger = zap.S().Named("connector.email")
	}
	e.logger.Infow("email connector init", "imap", cfg.Settings["imap_server"])
	return nil
}

// Send sends an auto-reply email via SMTP.
func (e *EmailConnector) Send(_ context.Context, msg *connector.Message) error {
	smtpHost, _ := e.cfg.Settings["smtp_server"].(string)
	smtpPort, _ := e.cfg.Settings["smtp_port"].(string)
	smtpUser, _ := e.cfg.Settings["smtp_user"].(string)
	smtpPass, _ := e.cfg.Settings["smtp_password"].(string)
	from, _ := e.cfg.Settings["from_address"].(string)

	if smtpHost == "" || smtpUser == "" {
		return fmt.Errorf("email: SMTP not configured")
	}

	tmpl, _ := e.cfg.Settings["reply_template"].(string)
	if tmpl == "" {
		tmpl = defaultReplyTemplate
	}

	body := renderTemplate(tmpl, msg)
	addr := net.JoinHostPort(smtpHost, smtpPort)
	if smtpPort == "" {
		addr = net.JoinHostPort(smtpHost, "587")
	}

	auth := smtp.PlainAuth("", smtpUser, smtpPass, smtpHost)
	msgBytes := buildMIME(from, msg.Channel, msg.Title, body)
	if err := smtp.SendMail(addr, auth, from, []string{msg.Channel}, msgBytes); err != nil {
		e.logger.Warnw("email send failed", "error", err, "to", msg.Channel)
		return err
	}

	e.logger.Infow("auto-reply sent", "to", msg.Channel, "title", msg.Title)
	return nil
}

func (e *EmailConnector) HealthCheck(_ context.Context) connector.HealthStatus {
	return connector.HealthStatus{OK: true, Message: "ready"}
}

func (e *EmailConnector) Close() error {
	if e.cancel != nil {
		e.cancel()
	}
	return nil
}

// ── Inbound: IMAP polling ──────────────────────────────────────────

// EmailMessage represents a parsed inbound email.
type EmailMessage struct {
	From    string
	Subject string
	Body    string
	HTML    string
}

// StartPolling begins IMAP polling loop. onTicket is called for each new email.
func (e *EmailConnector) StartPolling(ctx context.Context, onTicket func(EmailMessage) error) {
	ctx, e.cancel = context.WithCancel(ctx)
	interval := 60
	if v, ok := e.cfg.Settings["poll_interval"].(float64); ok && v > 0 {
		interval = int(v)
	}

	go func() {
		// Run immediately, then on interval
		e.pollOnce(ctx, onTicket)
		ticker := time.NewTicker(time.Duration(interval) * time.Second)
		defer ticker.Stop()
		for {
			select {
			case <-ctx.Done():
				return
			case <-ticker.C:
				e.pollOnce(ctx, onTicket)
			}
		}
	}()

	e.logger.Infow("email polling started", "interval_s", interval)
}

func (e *EmailConnector) pollOnce(ctx context.Context, onTicket func(EmailMessage) error) {
	server, _ := e.cfg.Settings["imap_server"].(string)
	port, _ := e.cfg.Settings["imap_port"].(string)
	user, _ := e.cfg.Settings["imap_user"].(string)
	pass, _ := e.cfg.Settings["imap_password"].(string)
	useTLS, _ := e.cfg.Settings["imap_tls"].(bool)

	if server == "" || user == "" {
		return
	}
	if port == "" {
		port = "993"
	}

	conn, err := dialIMAP(server, port, useTLS)
	if err != nil {
		e.logger.Warnw("imap dial failed", "error", err)
		return
	}
	defer conn.Close()

	tc := textproto.NewConn(conn)
	if _, _, err := tc.ReadResponse(0); err != nil {
		return
	}

	// LOGIN
	id := fmt.Sprintf("A%03d", e.pollCount)
	e.pollCount++
	if err := tc.PrintfLine("%s LOGIN %s %s", id, user, pass); err != nil {
		return
	}
	if _, _, err := tc.ReadResponse(0); err != nil {
		e.logger.Warnw("imap login failed", "error", err)
		return
	}

	// SELECT INBOX
	id2 := fmt.Sprintf("A%03d", e.pollCount)
	e.pollCount++
	if err := tc.PrintfLine("%s SELECT INBOX", id2); err != nil {
		return
	}
	if _, _, err := tc.ReadResponse(0); err != nil {
		return
	}

	// SEARCH UNSEEN
	id3 := fmt.Sprintf("A%03d", e.pollCount)
	e.pollCount++
	if err := tc.PrintfLine("%s UID SEARCH UNSEEN", id3); err != nil {
		return
	}
	_, imapResp, err := tc.ReadResponse(0)
	if err != nil {
		return
	}

	uids := parseSearchResponse(imapResp)
	if len(uids) == 0 {
		return
	}

	e.logger.Infow("imap unread messages", "count", len(uids))

	// Fetch each unseen message
	for _, uid := range uids {
		if uid <= e.lastUID {
			continue
		}
		email, err := e.fetchMessage(tc, uid)
		if err != nil {
			e.logger.Warnw("fetch message failed", "uid", uid, "error", err)
			continue
		}
		if err := onTicket(email); err != nil {
			e.logger.Warnw("process email failed", "uid", uid, "error", err)
		}
		if uid > e.lastUID {
			e.lastUID = uid
		}
	}
}

func (e *EmailConnector) fetchMessage(tc *textproto.Conn, uid uint32) (EmailMessage, error) {
	id := fmt.Sprintf("A%03d", e.pollCount)
	e.pollCount++
	if err := tc.PrintfLine("%s UID FETCH %d (BODY[])", id, uid); err != nil {
		return EmailMessage{}, err
	}

	// Read the multi-line FETCH response
	var sb strings.Builder
	for {
		line, err := tc.ReadLine()
		if err != nil {
			break
		}
		if strings.HasPrefix(line, fmt.Sprintf("%s ", id[:3])) && strings.Contains(line, "OK") {
			break
		}
		sb.WriteString(line)
		sb.WriteString("\r\n")
	}

	return parseEmail(strings.NewReader(sb.String()))
}

func parseEmail(r io.Reader) (EmailMessage, error) {
	msg, err := mail.ReadMessage(r)
	if err != nil {
		return EmailMessage{}, err
	}

	em := EmailMessage{
		From:    cleanAddress(msg.Header.Get("From")),
		Subject: decodeHeader(msg.Header.Get("Subject")),
	}

	mediaType, params, _ := mime.ParseMediaType(msg.Header.Get("Content-Type"))

	if strings.HasPrefix(mediaType, "multipart/") {
		mr := multipart.NewReader(msg.Body, params["boundary"])
		for {
			part, err := mr.NextPart()
			if err != nil {
				break
			}
			partType, _, _ := mime.ParseMediaType(part.Header.Get("Content-Type"))
			switch partType {
			case "text/plain":
				b, _ := io.ReadAll(part)
				em.Body = decodeContent(string(b), part.Header.Get("Content-Transfer-Encoding"))
			case "text/html":
				b, _ := io.ReadAll(part)
				em.HTML = decodeContent(string(b), part.Header.Get("Content-Transfer-Encoding"))
			}
		}
	} else if mediaType == "text/plain" || mediaType == "" {
		b, _ := io.ReadAll(msg.Body)
		em.Body = decodeContent(string(b), msg.Header.Get("Content-Transfer-Encoding"))
	}

	return em, nil
}

// ── Helpers ─────────────────────────────────────────────────────────

func dialIMAP(server, port string, useTLS bool) (net.Conn, error) {
	addr := net.JoinHostPort(server, port)
	if useTLS {
		return tls.Dial("tcp", addr, &tls.Config{ServerName: server})
	}
	return net.DialTimeout("tcp", addr, 15*time.Second)
}

func parseSearchResponse(resp string) []uint32 {
	var uids []uint32
	for _, line := range strings.Split(resp, "\r\n") {
		if strings.Contains(line, "SEARCH") {
			parts := strings.Fields(line)
			for _, p := range parts {
				var uid uint32
				if _, err := fmt.Sscanf(p, "%d", &uid); err == nil && uid > 0 {
					uids = append(uids, uid)
				}
			}
		}
	}
	return uids
}

func decodeHeader(s string) string {
	dec := new(mime.WordDecoder)
	result, _ := dec.DecodeHeader(s)
	return result
}

func decodeContent(s, encoding string) string {
	encoding = strings.ToLower(encoding)
	switch encoding {
	case "quoted-printable":
		r := quotedprintable.NewReader(strings.NewReader(s))
		b, _ := io.ReadAll(r)
		return string(b)
	case "base64":
		// simplified — mime package handles this in multipart parts
		return s
	default:
		return s
	}
}

func cleanAddress(addr string) string {
	addr = strings.TrimSpace(addr)
	if a, err := mail.ParseAddress(addr); err == nil {
		return a.Address
	}
	return addr
}

func buildMIME(from, to, subject, body string) []byte {
	encoded := mime.QEncoding.Encode("utf-8", subject)
	return []byte(fmt.Sprintf(
		"From: %s\r\nTo: %s\r\nSubject: %s\r\n"+
			"MIME-Version: 1.0\r\nContent-Type: text/plain; charset=UTF-8\r\n\r\n%s",
		from, to, encoded, body))
}

func renderTemplate(tmpl string, msg *connector.Message) string {
	r := strings.NewReplacer(
		"{{.TicketNumber}}", msg.Metadata["ticket_number"].(string),
		"{{.Title}}", msg.Title,
		"{{.Status}}", msg.Metadata["status"].(string),
	)
	return r.Replace(tmpl)
}

const defaultReplyTemplate = `您好，

您的服务请求已收到，我们会尽快处理。

工单编号：{{.TicketNumber}}
标题：{{.Title}}
状态：{{.Status}}

如有疑问，请回复此邮件。

--
KEAS Service Desk (自动回复)
`
