package msgraph

import (
	"fmt"
	"strings"
)

// cleanEmailBody strips quoted replies and trailing signatures from an
// inbound email body before it becomes a ticket description. Ported from
// connector/builtin/email/service.go's cleanEmailBody (same logic, kept
// package-local per the design's decision not to import the old package).
func cleanEmailBody(body string) string {
	lines := strings.Split(body, "\n")
	var cleaned []string
	for _, line := range lines {
		trimmed := strings.TrimSpace(line)
		if strings.HasPrefix(trimmed, ">") {
			continue
		}
		if strings.HasPrefix(trimmed, "--") || strings.HasPrefix(trimmed, "---") {
			break
		}
		if strings.HasPrefix(trimmed, "发件人:") || strings.HasPrefix(trimmed, "From:") {
			break
		}
		cleaned = append(cleaned, line)
	}
	return strings.TrimSpace(strings.Join(cleaned, "\n"))
}

const replyTemplate = `您好，

您的服务请求已收到，我们会尽快处理。

工单编号：%s
标题：%s
状态：%s

如有疑问，请回复此邮件。

--
KEAS Service Desk (自动回复)
`

// renderReplyTemplate builds the confirmation-reply body sent after a
// ticket is created from an inbound email.
func renderReplyTemplate(ticketNumber, title, status string) string {
	return fmt.Sprintf(replyTemplate, ticketNumber, title, status)
}
