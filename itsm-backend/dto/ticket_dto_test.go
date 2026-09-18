package dto

import (
	"encoding/json"
	"testing"

	"github.com/stretchr/testify/require"
)

// 操作回执摘要 = sha256(json.Marshal(TicketEditFields))（见 handlers/shared/workitemmutation）。
// 因此新增字段必须用 omitempty：否则所有既有 work_item.edit 载荷的摘要都会改变，
// 在途重试会被当成新命令而不是重放。该用例把"退役前的规范形态"显式固定下来。
func TestTicketEditFieldsKeepsLegacyCanonicalJSON(t *testing.T) {
	retiredShape := struct {
		Title       string                 `json:"title"`
		Description string                 `json:"description"`
		Priority    string                 `json:"priority"`
		Status      string                 `json:"status"`
		Type        string                 `json:"type"`
		CategoryID  *int                   `json:"categoryId,omitempty"`
		AssigneeID  *int                   `json:"assigneeId"`
		RequesterID int                    `json:"requesterId"`
		Tags        []string               `json:"tags"`
		Resolution  string                 `json:"resolution"`
		FormFields  map[string]interface{} `json:"formFields"`
	}{Title: "标题", Status: "closed", Priority: "medium"}
	legacy, err := json.Marshal(retiredShape)
	require.NoError(t, err)

	current, err := json.Marshal(TicketEditFields{Title: "标题", Status: "closed", Priority: "medium"})
	require.NoError(t, err)

	require.JSONEq(t, string(legacy), string(current))
	require.Equal(t, string(legacy), string(current), "未使用分类纠正原因时载荷必须逐字节不变（摘要稳定）")
	require.NotContains(t, string(current), "classificationReason")

	withReason, err := json.Marshal(TicketEditFields{Title: "标题", ClassificationReason: "现场归类有误"})
	require.NoError(t, err)
	require.Contains(t, string(withReason), `"classificationReason":"现场归类有误"`)

	// 分类目标只保留最深节点 ID 字段：不再存在按显示名称解析的载荷字段。
	require.NotContains(t, string(current), `"category"`)
	require.NotContains(t, string(withReason), `"category"`)
}
