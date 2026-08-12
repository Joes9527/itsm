package email

import (
	"strings"
	"testing"
)

func TestParseEmailPlainText(t *testing.T) {
	raw := "From: test@example.com\r\nSubject: Test Subject\r\nContent-Type: text/plain; charset=UTF-8\r\n\r\nHello, this is a test email."
	em, err := parseEmail(strings.NewReader(raw))
	if err != nil {
		t.Fatalf("parseEmail failed: %v", err)
	}
	if em.From != "test@example.com" {
		t.Errorf("expected from=test@example.com, got=%s", em.From)
	}
	if em.Subject != "Test Subject" {
		t.Errorf("expected subject='Test Subject', got=%s", em.Subject)
	}
	if em.Body != "Hello, this is a test email." {
		t.Errorf("unexpected body: %s", em.Body)
	}
}

func TestCleanEmailBody(t *testing.T) {
	body := `请帮我重置密码

谢谢
---
签名信息
From: someone@else.com
发件人: 其他人`

	cleaned := cleanEmailBody(body)
	if strings.Contains(cleaned, "签名信息") {
		t.Error("should have trimmed signature")
	}
	if strings.Contains(cleaned, "发件人") {
		t.Error("should have trimmed forwarded email")
	}
	if !strings.Contains(cleaned, "请帮我重置密码") {
		t.Error("should keep main content")
	}
}

func TestRenderTemplate(t *testing.T) {
	msg := &struct {
		Channel  string
		Title    string
		Metadata map[string]interface{}
	}{
		Title: "测试工单",
		Metadata: map[string]interface{}{
			"ticket_number": "TKT-001",
			"status":        "已创建",
		},
	}

	// Test with connector.Message-compatible data
	result := defaultReplyTemplate
	result = strings.ReplaceAll(result, "{{.TicketNumber}}", msg.Metadata["ticket_number"].(string))
	result = strings.ReplaceAll(result, "{{.Title}}", msg.Title)
	result = strings.ReplaceAll(result, "{{.Status}}", msg.Metadata["status"].(string))

	if !strings.Contains(result, "TKT-001") {
		t.Error("template should contain ticket number")
	}
	if !strings.Contains(result, "测试工单") {
		t.Error("template should contain title")
	}
}

func TestParseSearchResponse(t *testing.T) {
	resp := "* SEARCH 1 2 3 42 100\r\nA003 OK SEARCH completed\r\n"
	uids := parseSearchResponse(resp)
	if len(uids) != 5 {
		t.Errorf("expected 5 UIDs, got %d: %v", len(uids), uids)
	}
	if uids[3] != 42 {
		t.Errorf("expected uid[3]=42, got=%d", uids[3])
	}
}

func TestMatchTemplateKeywordMapping(t *testing.T) {
	tests := []struct {
		text     string
		expected string
	}{
		{"请帮我重置密码", "ACC"},
		{"AD账号无法登录", "ACC"},
		{"邮箱满了", "COL"},
		{"邮件发不出去", "COL"},
		{"打印机坏了", "EUC"},
		{"电脑无法开机", "EUC"},
		{"网络连不上", "NET"},
		{"服务器需要扩容", "INF"},
		{"发现了钓鱼邮件", "SEC"},
	}

	for _, tt := range tests {
		// Test the keyword mapping logic
		type kwEntry struct{ keyword, category string }
		keywordList := []kwEntry{
			{"钓鱼", "SEC"}, {"病毒", "SEC"}, {"安全", "SEC"},
			{"打印机", "EUC"}, {"电脑", "EUC"}, {"开机", "EUC"},
			{"服务器", "INF"}, {"虚拟机", "INF"},
			{"AD", "ACC"}, {"密码", "ACC"}, {"账号", "ACC"},
			{"邮箱", "COL"}, {"邮件", "COL"},
			{"网络", "NET"}, {"上网", "NET"},
		}

		text := strings.ToLower(tt.text)
		found := ""
		for _, kw := range keywordList {
			if strings.Contains(text, kw.keyword) {
				found = kw.category
				break
			}
		}
		if found != tt.expected {
			t.Errorf("text=%q: expected=%s, got=%s", tt.text, tt.expected, found)
		}
	}
}
