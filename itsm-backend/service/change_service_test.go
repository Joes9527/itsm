package service

import (
	"context"
	"testing"

	"itsm-backend/common"
	"itsm-backend/database"
	"itsm-backend/dto"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// TestIsValidChangeStatusTransition 覆盖 IsValidChangeStatusTransition 的 ITIL 状态机语义：
// 合法转换、非法转换（含跳级）、终态锁定（cancelled/rejected/completed/rolled_back 不可再转换）、
// "pending" 历史别名归一化（handlers/change 使用 pending，内部状态机用 submitted），
// 以及 normal/standard/emergency 三种变更类型的差异化转换规则。
//
// 背景：change_service.go 里的 ChangeService 结构体（Track 6，历史遗留、零调用方）已删除，
// 原 change_service_test.go 随之删除；但 IsValidChangeStatusTransition 是仍被
// handlers/change/service.go 实际调用的独立函数，删除前该文件对它有充分的间接覆盖
// （通过 ChangeService.UpdateChangeStatus）。本测试直接调用该纯函数，恢复并强化那部分覆盖，
// 后续任务（8/9/12）会依赖这里锁定的精确语义。
func TestIsValidChangeStatusTransition(t *testing.T) {
	tests := []struct {
		name          string
		currentStatus string
		newStatus     string
		changeType    string
		want          bool
	}{
		// ---- normal：严格 ITIL 流程 ----
		{"normal: draft -> submitted 合法", common.ChangeStatusDraft, common.ChangeStatusSubmitted, "normal", true},
		{"normal: draft -> cancelled 合法", common.ChangeStatusDraft, common.ChangeStatusCancelled, "normal", true},
		{"normal: draft -> approved 非法（跳过 submitted）", common.ChangeStatusDraft, common.ChangeStatusApproved, "normal", false},
		{"normal: submitted -> approved 合法", common.ChangeStatusSubmitted, common.ChangeStatusApproved, "normal", true},
		{"normal: submitted -> rejected 合法", common.ChangeStatusSubmitted, common.ChangeStatusRejected, "normal", true},
		{"normal: submitted -> in_progress 非法（跳过 approved/scheduled）", common.ChangeStatusSubmitted, common.ChangeStatusInProgress, "normal", false},
		{"normal: approved -> scheduled 合法", common.ChangeStatusApproved, common.ChangeStatusScheduled, "normal", true},
		{"normal: approved -> in_progress 非法（跳过 scheduled）", common.ChangeStatusApproved, common.ChangeStatusInProgress, "normal", false},
		{"normal: scheduled -> in_progress 合法", common.ChangeStatusScheduled, common.ChangeStatusInProgress, "normal", true},
		{"normal: in_progress -> completed 合法", common.ChangeStatusInProgress, common.ChangeStatusCompleted, "normal", true},
		{"normal: in_progress -> failed 合法", common.ChangeStatusInProgress, common.ChangeStatusFailed, "normal", true},
		{"normal: in_progress -> rolled_back 合法", common.ChangeStatusInProgress, string(dto.ChangeStatusRolledBack), "normal", true},
		{"normal: failed -> scheduled 合法（重新排期）", common.ChangeStatusFailed, common.ChangeStatusScheduled, "normal", true},

		// ---- 终态锁定：不论变更类型，进入终态后不可再转换 ----
		{"终态: cancelled -> draft 非法", common.ChangeStatusCancelled, common.ChangeStatusDraft, "normal", false},
		{"终态: cancelled -> approved 非法（standard 类型下同样锁定）", common.ChangeStatusCancelled, common.ChangeStatusApproved, "standard", false},
		{"终态: rejected -> submitted 非法", common.ChangeStatusRejected, common.ChangeStatusSubmitted, "normal", false},
		{"终态: completed -> in_progress 非法", common.ChangeStatusCompleted, common.ChangeStatusInProgress, "normal", false},
		{"终态: rolled_back -> draft 非法", string(dto.ChangeStatusRolledBack), common.ChangeStatusDraft, "normal", false},

		// ---- "pending" 历史别名归一化（仅 currentStatus 侧生效）----
		{"pending 别名等价 submitted -> approved 合法", "pending", common.ChangeStatusApproved, "normal", true},
		{"pending 别名等价 submitted -> rejected 合法", "pending", common.ChangeStatusRejected, "normal", true},

		// ---- standard：预授权，可跳过审批/排期 ----
		{"standard: draft -> approved 合法（预授权跳过 submitted）", common.ChangeStatusDraft, common.ChangeStatusApproved, "standard", true},
		{"standard: draft -> scheduled 合法（预授权）", common.ChangeStatusDraft, common.ChangeStatusScheduled, "standard", true},
		{"standard: draft -> in_progress 合法（预授权）", common.ChangeStatusDraft, common.ChangeStatusInProgress, "standard", true},
		{"standard: approved -> in_progress 合法（跳过 scheduled）", common.ChangeStatusApproved, common.ChangeStatusInProgress, "standard", true},

		// ---- emergency：快速通道，可跳过排期 ----
		{"emergency: draft -> in_progress 合法（快速通道）", common.ChangeStatusDraft, common.ChangeStatusInProgress, "emergency", true},
		{"emergency: approved -> in_progress 合法（跳过 scheduled）", common.ChangeStatusApproved, common.ChangeStatusInProgress, "emergency", true},
		{"emergency: approved -> scheduled 非法（紧急流程无排期步骤）", common.ChangeStatusApproved, common.ChangeStatusScheduled, "emergency", false},
		{"emergency: scheduled 状态未定义于紧急流程转换表，任何转换均失败关闭", common.ChangeStatusScheduled, common.ChangeStatusInProgress, "emergency", false},

		// ---- 未知状态必须失败关闭 ----
		{"未知当前状态失败关闭", "some_unknown_status", common.ChangeStatusDraft, "normal", false},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := IsValidChangeStatusTransition(tt.currentStatus, tt.newStatus, tt.changeType)
			assert.Equal(t, tt.want, got,
				"IsValidChangeStatusTransition(%q, %q, %q)", tt.currentStatus, tt.newStatus, tt.changeType)
		})
	}
}

// TestCloseChangeApprovalChains_NoRawDBConfigured 覆盖 CloseChangeApprovalChains 在
// database.GetRawDB() 返回 nil（原始 DB 连接未初始化）时必须 fail-closed 返回 error，
// 而不是静默跳过或 panic。核心的“真正收口 pending 行”行为需要真实 Postgres 连接
// （原始 SQL 使用 change_approval_chains 裸表，非 Ent 管理），见
// change_approval_chains_integration_test.go（build tag: integration）。
func TestCloseChangeApprovalChains_NoRawDBConfigured(t *testing.T) {
	prev := database.GetRawDB()
	database.SetRawDBForTest(nil)
	t.Cleanup(func() { database.SetRawDBForTest(prev) })

	err := CloseChangeApprovalChains(context.Background(), 1, 1)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "raw database handle unavailable")
}
