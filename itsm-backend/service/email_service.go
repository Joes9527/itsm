package service

import (
	"bytes"
	"context"
	"crypto/tls"
	"encoding/base64"
	"errors"
	"fmt"
	"html/template"
	"net"
	"net/mail"
	"net/smtp"
	"strings"
	"sync"
	"time"

	"go.uber.org/zap"
)

// EmailConfig 邮件配置
type EmailConfig struct {
	Host     string // SMTP服务器地址
	Port     int    // SMTP端口
	Username string // 用户名
	Password string // 密码
	From     string // 发件人地址
	FromName string // 发件人名称
}

// GraphMailSender Graph sendMail 发信后端（Exchange Online）。由 msgraph
// 连接器的 Client.SendMail 实现，service 包只依赖该接口，不依赖具体类型。
type GraphMailSender interface {
	SendMail(ctx context.Context, mailbox, to, subject, body, deliveryID string) error
}

// GraphProvider resolves the configured Graph sender for one tenant only.
type GraphProvider func(tenantID int) (GraphMailSender, string, bool)

type smtpSendFunc func(context.Context, string, smtp.Auth, string, []string, []byte) error

const (
	emailErrorClassGraphSend    = "graph_send_failed"
	emailErrorClassSMTPSend     = "smtp_send_failed"
	emailErrorClassDelivery     = "email_delivery_failed"
	emailErrorClassRouteMissing = "email_route_unavailable"
)

var (
	errEmailGraphSend    = errors.New(emailErrorClassGraphSend)
	errEmailSMTPSend     = errors.New(emailErrorClassSMTPSend)
	errEmailRouteMissing = errors.New(emailErrorClassRouteMissing)
)

// EmailService 邮件服务
type EmailService struct {
	config   EmailConfig
	logger   *zap.SugaredLogger
	mu       sync.Mutex
	recent   map[string][]time.Time
	smtpSend smtpSendFunc

	// graphProvider 延迟绑定 Graph 发信后端：返回 sender + 发件邮箱 + 是否可用。
	// connector 运行时 provision，不能启动时注入，故发信时动态查询。
	graphProvider GraphProvider
}

// EmailMessage 邮件消息
type EmailMessage struct {
	To                      []string          // 收件人列表
	CC                      []string          // 抄送人列表
	Subject                 string            // 邮件主题
	Body                    string            // 邮件正文（HTML）
	BodyText                string            // 邮件正文（纯文本）
	Attachments             []EmailAttachment // 附件
	DeliveryID              string            // durable outbox correlation marker
	DisableProviderFallback bool              // prevents ambiguous cross-provider replay
}

// EmailAttachment 邮件附件
type EmailAttachment struct {
	Filename    string // 文件名
	ContentType string // 内容类型
	Data        []byte // 文件内容
}

// NewEmailService 创建邮件服务
func NewEmailService(config EmailConfig, logger *zap.SugaredLogger) *EmailService {
	return &EmailService{
		config:   config,
		logger:   logger,
		recent:   make(map[string][]time.Time),
		smtpSend: sendSMTPWithContext,
	}
}

// SetGraphProvider 注入 Graph 发信后端（延迟绑定：发信时动态查 connector）。
func (s *EmailService) SetGraphProvider(provider GraphProvider) {
	s.graphProvider = provider
}

// Send sends through explicitly configured SMTP for legacy callers that do not
// carry tenant identity. Tenant-owned delivery must use SendForTenant.
func (s *EmailService) Send(ctx context.Context, msg *EmailMessage) error {
	if err := s.prepareMessage(msg); err != nil {
		return err
	}
	if !s.smtpConfigured() {
		return emailDeliveryError(errEmailRouteMissing)
	}
	if err := s.sendViaSMTP(ctx, msg); err != nil {
		return emailDeliveryError(err)
	}
	return nil
}

// SendForTenant sends through only the requested tenant's Graph connector and
// falls back to configured SMTP when Graph is unavailable at runtime.
func (s *EmailService) SendForTenant(ctx context.Context, tenantID int, msg *EmailMessage) error {
	if tenantID <= 0 {
		return fmt.Errorf("email tenant is required")
	}
	if err := s.prepareMessage(msg); err != nil {
		return err
	}

	var routeErrors []error
	if s.graphProvider != nil {
		if sender, mailbox, ok := s.graphProvider(tenantID); ok && sender != nil {
			if err := s.sendViaGraph(ctx, sender, mailbox, msg); err == nil {
				return nil
			} else {
				routeErrors = append(routeErrors, err)
				if msg.DisableProviderFallback {
					return emailDeliveryError(routeErrors...)
				}
			}
		}
	}
	if s.smtpConfigured() {
		if err := s.sendViaSMTP(ctx, msg); err == nil {
			return nil
		} else {
			routeErrors = append(routeErrors, err)
		}
	}
	if len(routeErrors) == 0 {
		routeErrors = append(routeErrors, errEmailRouteMissing)
	}
	return emailDeliveryError(routeErrors...)
}

func (s *EmailService) prepareMessage(msg *EmailMessage) error {
	if err := s.validateMessage(msg); err != nil {
		return err
	}
	if err := s.checkRateLimit(msg.To, 20, time.Minute); err != nil {
		return err
	}
	return nil
}

// sendViaGraph 通过 Graph sendMail 发送（纯文本 body）。
func (s *EmailService) sendViaGraph(ctx context.Context, sender GraphMailSender, mailbox string, msg *EmailMessage) error {
	body := msg.BodyText
	if body == "" {
		body = msg.Body
	}
	for _, to := range msg.To {
		if err := sender.SendMail(ctx, mailbox, to, msg.Subject, body, msg.DeliveryID); err != nil {
			s.logger.Errorw("email Graph delivery failed", "error_class", emailErrorClassGraphSend)
			return errEmailGraphSend
		}
	}
	s.logger.Infow("email delivered via Graph")
	return nil
}

// sendViaSMTP 通过 SMTP 发送（现有逻辑）。
func (s *EmailService) sendViaSMTP(ctx context.Context, msg *EmailMessage) error {
	s.logger.Infow("sending email via SMTP")

	// 构建邮件内容
	var emailBody strings.Builder

	// MIME边界
	boundary := "Mixed_123456789"

	// 邮件头
	emailBody.WriteString(fmt.Sprintf("From: %s <%s>\r\n", s.config.FromName, s.config.From))
	emailBody.WriteString(fmt.Sprintf("To: %s\r\n", strings.Join(msg.To, ",")))
	if len(msg.CC) > 0 {
		emailBody.WriteString(fmt.Sprintf("Cc: %s\r\n", strings.Join(msg.CC, ",")))
	}
	emailBody.WriteString(fmt.Sprintf("Subject: %s\r\n", msg.Subject))
	if msg.DeliveryID != "" {
		emailBody.WriteString(fmt.Sprintf("Message-ID: <%s@itsm.local>\r\n", msg.DeliveryID))
		emailBody.WriteString(fmt.Sprintf("X-ITSM-Delivery-ID: %s\r\n", msg.DeliveryID))
	}
	emailBody.WriteString("MIME-Version: 1.0\r\n")
	emailBody.WriteString(fmt.Sprintf("Content-Type: multipart/alternative; boundary=\"%s\"\r\n", boundary))
	emailBody.WriteString("\r\n")

	// 纯文本版本
	if msg.BodyText != "" {
		emailBody.WriteString(fmt.Sprintf("--%s\r\n", boundary))
		emailBody.WriteString("Content-Type: text/plain; charset=UTF-8\r\n")
		emailBody.WriteString("Content-Transfer-Encoding: base64\r\n")
		emailBody.WriteString("\r\n")
		emailBody.WriteString(base64.StdEncoding.EncodeToString([]byte(msg.BodyText)))
		emailBody.WriteString("\r\n")
	}

	// HTML版本
	if msg.Body != "" {
		emailBody.WriteString(fmt.Sprintf("--%s\r\n", boundary))
		emailBody.WriteString("Content-Type: text/html; charset=UTF-8\r\n")
		emailBody.WriteString("Content-Transfer-Encoding: base64\r\n")
		emailBody.WriteString("\r\n")
		emailBody.WriteString(base64.StdEncoding.EncodeToString([]byte(msg.Body)))
		emailBody.WriteString("\r\n")
	}

	// 添加附件
	for _, att := range msg.Attachments {
		emailBody.WriteString(fmt.Sprintf("--%s\r\n", boundary))
		emailBody.WriteString(fmt.Sprintf("Content-Type: %s; name=\"%s\"\r\n", att.ContentType, att.Filename))
		emailBody.WriteString("Content-Transfer-Encoding: base64\r\n")
		emailBody.WriteString(fmt.Sprintf("Content-Disposition: attachment; filename=\"%s\"\r\n", att.Filename))
		emailBody.WriteString("\r\n")
		emailBody.WriteString(base64.StdEncoding.EncodeToString(att.Data))
		emailBody.WriteString("\r\n")
	}

	emailBody.WriteString(fmt.Sprintf("--%s--\r\n", boundary))

	// 发送邮件
	auth := smtp.PlainAuth("", s.config.Username, s.config.Password, s.config.Host)
	addr := fmt.Sprintf("%s:%d", s.config.Host, s.config.Port)

	var err error
	for attempt := 0; attempt < 3; attempt++ {
		err = s.smtpSend(ctx, addr, auth, s.config.From, append(append([]string{}, msg.To...), msg.CC...), []byte(emailBody.String()))
		if err == nil {
			break
		}
		if attempt < 2 {
			select {
			case <-ctx.Done():
				s.logger.Errorw("email SMTP delivery failed", "error_class", emailErrorClassSMTPSend)
				return errEmailSMTPSend
			case <-time.After(time.Duration(attempt+1) * 200 * time.Millisecond):
			}
		}
	}
	if err != nil {
		s.logger.Errorw("email SMTP delivery failed", "error_class", emailErrorClassSMTPSend)
		return errEmailSMTPSend
	}

	s.logger.Infow("email delivered via SMTP")
	return nil
}

func sendSMTPWithContext(ctx context.Context, addr string, auth smtp.Auth, from string, recipients []string, msg []byte) error {
	host, _, err := net.SplitHostPort(addr)
	if err != nil {
		return err
	}
	connection, err := (&net.Dialer{}).DialContext(ctx, "tcp", addr)
	if err != nil {
		return err
	}
	defer connection.Close()
	if deadline, ok := ctx.Deadline(); ok {
		if err := connection.SetDeadline(deadline); err != nil {
			return err
		}
	}
	client, err := smtp.NewClient(connection, host)
	if err != nil {
		return err
	}
	defer client.Close()
	if ok, _ := client.Extension("STARTTLS"); ok {
		if err := client.StartTLS(&tls.Config{ServerName: host, MinVersion: tls.VersionTLS12}); err != nil {
			return err
		}
	}
	if auth != nil {
		if err := client.Auth(auth); err != nil {
			return err
		}
	}
	if err := client.Mail(from); err != nil {
		return err
	}
	for _, recipient := range recipients {
		if err := client.Rcpt(recipient); err != nil {
			return err
		}
	}
	writer, err := client.Data()
	if err != nil {
		return err
	}
	if _, err := writer.Write(msg); err != nil {
		_ = writer.Close()
		return err
	}
	if err := writer.Close(); err != nil {
		return err
	}
	return client.Quit()
}

func (s *EmailService) smtpConfigured() bool {
	return s.ValidateConfig() == nil && s.smtpSend != nil
}

func emailDeliveryError(routeErrors ...error) error {
	return fmt.Errorf("%s: %w", emailErrorClassDelivery, errors.Join(routeErrors...))
}

func (s *EmailService) validateMessage(msg *EmailMessage) error {
	if msg == nil || len(msg.To) == 0 {
		return fmt.Errorf("email recipient is required")
	}
	if strings.ContainsAny(msg.Subject, "\r\n") {
		return fmt.Errorf("email subject contains invalid characters")
	}
	if strings.ContainsAny(msg.DeliveryID, "\r\n") {
		return fmt.Errorf("email delivery id contains invalid characters")
	}
	for _, address := range append(append([]string{}, msg.To...), msg.CC...) {
		if _, err := mail.ParseAddress(address); err != nil {
			return fmt.Errorf("invalid email recipient")
		}
	}
	return nil
}

func (s *EmailService) checkRateLimit(keys []string, limit int, window time.Duration) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	now := time.Now()
	for _, key := range keys {
		kept := s.recent[key][:0]
		for _, sent := range s.recent[key] {
			if now.Sub(sent) < window {
				kept = append(kept, sent)
			}
		}
		if len(kept) >= limit {
			s.recent[key] = kept
			return fmt.Errorf("email rate limit exceeded")
		}
		s.recent[key] = append(kept, now)
	}
	return nil
}

// SendTemplate 发送模板邮件
func (s *EmailService) SendTemplate(ctx context.Context, msg *EmailMessage, templateName string, data interface{}) error {
	s.logger.Infow("sending template email", "template", templateName)

	// 解析模板
	tmpl, err := template.New(templateName).Option("missingkey=error").Parse(msg.Body)
	if err != nil {
		return fmt.Errorf("failed to parse template: %w", err)
	}

	// 执行模板
	var buf bytes.Buffer
	err = tmpl.Execute(&buf, data)
	if err != nil {
		return fmt.Errorf("failed to execute template: %w", err)
	}

	msg.Body = buf.String()
	if msg.BodyText != "" {
		textTmpl, parseErr := template.New(templateName + "_text").Option("missingkey=error").Parse(msg.BodyText)
		if parseErr != nil {
			return fmt.Errorf("failed to parse text template: %w", parseErr)
		}
		buf.Reset()
		if executeErr := textTmpl.Execute(&buf, data); executeErr != nil {
			return fmt.Errorf("failed to execute text template: %w", executeErr)
		}
		msg.BodyText = buf.String()
	}

	return s.Send(ctx, msg)
}

// SendTicketNotification 发送工单通知邮件
func (s *EmailService) SendTicketNotification(ctx context.Context, to []string, ticketNumber, ticketTitle, action, content string) error {
	return s.Send(ctx, buildTicketNotificationEmail(to, ticketNumber, ticketTitle, action, content))
}

// SendTicketNotificationForTenant sends a ticket email through tenant-scoped routing.
func (s *EmailService) SendTicketNotificationForTenant(
	ctx context.Context,
	tenantID int,
	to []string,
	ticketNumber, ticketTitle, action, content string,
) error {
	return s.SendForTenant(ctx, tenantID, buildTicketNotificationEmail(to, ticketNumber, ticketTitle, action, content))
}

func buildTicketNotificationEmail(to []string, ticketNumber, ticketTitle, action, content string) *EmailMessage {
	subject := fmt.Sprintf("[ITSM] 工单 %s - %s", ticketNumber, action)

	bodyText := fmt.Sprintf(`
工单通知
========

工单编号: %s
工单标题: %s
操作: %s

%s

---
此邮件由ITSM系统自动发送
发送时间: %s
`, ticketNumber, ticketTitle, action, content, time.Now().Format("2006-01-02 15:04:05"))

	bodyHTML := fmt.Sprintf(`
<!DOCTYPE html>
<html>
<head>
    <meta charset="UTF-8">
</head>
<body style="font-family: Arial, sans-serif; line-height: 1.6;">
    <div style="max-width: 600px; margin: 0 auto; padding: 20px;">
        <h2 style="color: #333;">工单通知</h2>
        <table style="width: 100%%; border-collapse: collapse; margin: 20px 0;">
            <tr>
                <td style="padding: 10px; border-bottom: 1px solid #ddd; font-weight: bold;">工单编号</td>
                <td style="padding: 10px; border-bottom: 1px solid #ddd;">%s</td>
            </tr>
            <tr>
                <td style="padding: 10px; border-bottom: 1px solid #ddd; font-weight: bold;">工单标题</td>
                <td style="padding: 10px; border-bottom: 1px solid #ddd;">%s</td>
            </tr>
            <tr>
                <td style="padding: 10px; border-bottom: 1px solid #ddd; font-weight: bold;">操作</td>
                <td style="padding: 10px; border-bottom: 1px solid #ddd;">%s</td>
            </tr>
        </table>
        <div style="background-color: #f5f5f5; padding: 15px; border-radius: 5px;">
            <p style="margin: 0;">%s</p>
        </div>
        <hr style="border: none; border-top: 1px solid #ddd; margin: 20px 0;">
        <p style="color: #888; font-size: 12px;">此邮件由ITSM系统自动发送</p>
    </div>
</body>
</html>
`, ticketNumber, ticketTitle, action, content)

	return &EmailMessage{
		To:       to,
		Subject:  subject,
		Body:     bodyHTML,
		BodyText: bodyText,
	}
}

// SendSLANotification 发送SLA告警邮件
func (s *EmailService) SendSLANotification(ctx context.Context, to []string, ticketNumber, ticketTitle, slaType, deadline string) error {
	subject := fmt.Sprintf("[ITSM SLA告警] 工单 %s - %s 即将到期", ticketNumber, slaType)

	bodyText := fmt.Sprintf(`
SLA告警通知
===========

工单编号: %s
工单标题: %s
SLA类型: %s
截止时间: %s

请及时处理，避免SLA违规。

---
此邮件由ITSM系统自动发送
发送时间: %s
`, ticketNumber, ticketTitle, slaType, deadline, time.Now().Format("2006-01-02 15:04:05"))

	bodyHTML := fmt.Sprintf(`
<!DOCTYPE html>
<html>
<head>
    <meta charset="UTF-8">
</head>
<body style="font-family: Arial, sans-serif; line-height: 1.6;">
    <div style="max-width: 600px; margin: 0 auto; padding: 20px;">
        <h2 style="color: #f59e0b;">⚠️ SLA告警通知</h2>
        <table style="width: 100%%; border-collapse: collapse; margin: 20px 0;">
            <tr>
                <td style="padding: 10px; border-bottom: 1px solid #ddd; font-weight: bold;">工单编号</td>
                <td style="padding: 10px; border-bottom: 1px solid #ddd;">%s</td>
            </tr>
            <tr>
                <td style="padding: 10px; border-bottom: 1px solid #ddd; font-weight: bold;">工单标题</td>
                <td style="padding: 10px; border-bottom: 1px solid #ddd;">%s</td>
            </tr>
            <tr>
                <td style="padding: 10px; border-bottom: 1px solid #ddd; font-weight: bold;">SLA类型</td>
                <td style="padding: 10px; border-bottom: 1px solid #ddd;">%s</td>
            </tr>
            <tr>
                <td style="padding: 10px; border-bottom: 1px solid #ddd; font-weight: bold;">截止时间</td>
                <td style="padding: 10px; border-bottom: 1px solid #ddd; color: #ef4444;">%s</td>
            </tr>
        </table>
        <div style="background-color: #fef3c7; padding: 15px; border-radius: 5px; border-left: 4px solid #f59e0b;">
            <p style="margin: 0; color: #92400e;">请及时处理，避免SLA违规！</p>
        </div>
        <hr style="border: none; border-top: 1px solid #ddd; margin: 20px 0;">
        <p style="color: #888; font-size: 12px;">此邮件由ITSM系统自动发送</p>
    </div>
</body>
</html>
`, ticketNumber, ticketTitle, slaType, deadline)

	msg := &EmailMessage{
		To:       to,
		Subject:  subject,
		Body:     bodyHTML,
		BodyText: bodyText,
	}

	return s.Send(ctx, msg)
}

// ValidateConfig 验证邮件配置
func (s *EmailService) ValidateConfig() error {
	if s.config.Host == "" {
		return fmt.Errorf("email host is required")
	}
	if s.config.Port == 0 {
		return fmt.Errorf("email port is required")
	}
	if s.config.Username == "" {
		return fmt.Errorf("email username is required")
	}
	if s.config.From == "" {
		return fmt.Errorf("email from address is required")
	}
	return nil
}

// SendPasswordResetEmail 发送密码重置邮件
func (s *EmailService) SendPasswordResetEmail(ctx context.Context, to []string, resetToken, baseURL string) error {
	// 构建重置链接
	resetURL := fmt.Sprintf("%s/reset-password?token=%s", baseURL, resetToken)

	subject := "[ITSM] 密码重置"

	bodyText := fmt.Sprintf(`
密码重置
========

您收到此邮件是因为您请求重置ITSM系统的密码。

请点击以下链接重置密码：
%s

此链接将在1小时后失效。

如果您没有请求重置密码，请忽略此邮件。

---
此邮件由ITSM系统自动发送
发送时间: %s
`, resetURL, time.Now().Format("2006-01-02 15:04:05"))

	bodyHTML := fmt.Sprintf(`
<!DOCTYPE html>
<html>
<head>
    <meta charset="UTF-8">
</head>
<body style="font-family: Arial, sans-serif; line-height: 1.6;">
    <div style="max-width: 600px; margin: 0 auto; padding: 20px;">
        <h2 style="color: #333;">密码重置</h2>
        <div style="background-color: #f5f5f5; padding: 20px; border-radius: 5px; margin: 20px 0;">
            <p style="margin: 0 0 15px 0;">您收到此邮件是因为您请求重置ITSM系统的密码。</p>
            <p style="margin: 0 0 15px 0;">请点击下方按钮重置密码：</p>
            <p style="text-align: center; margin: 30px 0;">
                <a href="%s" style="background-color: #1890ff; color: white; padding: 12px 30px; text-decoration: none; border-radius: 5px; display: inline-block;">
                    重置密码
                </a>
            </p>
            <p style="margin: 0; color: #888; font-size: 12px;">此链接将在1小时后失效</p>
        </div>
        <div style="background-color: #fff7e6; padding: 15px; border-radius: 5px; border-left: 4px solid #faad14;">
            <p style="margin: 0; color: #d48806;">如果您没有请求重置密码，请忽略此邮件。</p>
        </div>
        <hr style="border: none; border-top: 1px solid #ddd; margin: 20px 0;">
        <p style="color: #888; font-size: 12px;">此邮件由ITSM系统自动发送</p>
    </div>
</body>
</html>
`, resetURL)

	msg := &EmailMessage{
		To:       to,
		Subject:  subject,
		Body:     bodyHTML,
		BodyText: bodyText,
	}

	return s.Send(ctx, msg)
}
