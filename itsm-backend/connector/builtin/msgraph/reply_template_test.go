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

// TestRenderReplyTemplate_DoesNotInviteAReply is a regression test: the
// original wording ("如有疑问，请回复此邮件" — "if you have questions, reply
// to this email") invited users to reply, but handleMessage in
// coordinator.go treats every inbound message as "create a new ticket"
// (keyed only on internetMessageId) with no reply-to-existing-ticket
// detection — so a user replying to the confirmation would create a SECOND,
// unrelated ticket. The template must no longer invite a reply.
func TestRenderReplyTemplate_DoesNotInviteAReply(t *testing.T) {
	got := renderReplyTemplate("TCK-0001", "打印机无法使用", "新建")
	assert.NotContains(t, got, "请回复", "reply template must not invite the recipient to reply to this email")
}
