package msgraph

import (
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
)

func TestCleanEmailBody_StripsQuotedRepliesAndSignature(t *testing.T) {
	body := "Please help, my laptop won't boot.\n" +
		"> On Mon, someone wrote:\n" +
		"> previous message\n" +
		"--\n" +
		"Sent from my iPhone"
	got := cleanEmailBody(body)
	assert.Equal(t, "Please help, my laptop won't boot.", got)
}

func TestCleanEmailBody_StripsForwardedHeader(t *testing.T) {
	body := "See below.\n发件人: someone@example.com\n原始邮件内容"
	got := cleanEmailBody(body)
	assert.Equal(t, "See below.", got)
}

func TestRenderReplyTemplate_IncludesTicketNumberAndTitle(t *testing.T) {
	got := renderReplyTemplate("TCK-0001", "打印机无法使用", "新建")
	assert.True(t, strings.Contains(got, "TCK-0001"))
	assert.True(t, strings.Contains(got, "打印机无法使用"))
	assert.True(t, strings.Contains(got, "新建"))
}
