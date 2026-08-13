package service

import (
	"context"
	"fmt"
	"strings"
	"time"

	"itsm-backend/common"
	"itsm-backend/database"
	"itsm-backend/dto"
)

func isValidChangeType(value string) bool {
	return value == string(dto.ChangeTypeNormal) || value == string(dto.ChangeTypeStandard) || value == string(dto.ChangeTypeEmergency)
}

func isValidChangePriority(value string) bool {
	return value == string(dto.ChangePriorityLow) || value == string(dto.ChangePriorityMedium) || value == string(dto.ChangePriorityHigh) || value == string(dto.ChangePriorityCritical)
}

func isValidChangeImpact(value string) bool {
	return value == string(dto.ChangeImpactLow) || value == string(dto.ChangeImpactMedium) || value == string(dto.ChangeImpactHigh)
}

func isValidChangeRisk(value string) bool {
	return value == string(dto.ChangeRiskLow) || value == string(dto.ChangeRiskMedium) || value == string(dto.ChangeRiskHigh)
}

func uniqueNonEmptyStrings(values []string) []string {
	seen := make(map[string]struct{}, len(values))
	result := make([]string, 0, len(values))
	for _, value := range values {
		value = strings.TrimSpace(value)
		if value == "" {
			continue
		}
		if _, ok := seen[value]; ok {
			continue
		}
		seen[value] = struct{}{}
		result = append(result, value)
	}
	return result
}

func optionalTime(value time.Time) *time.Time {
	if value.IsZero() {
		return nil
	}
	return &value
}

func optionalInt(value int) *int {
	if value == 0 {
		return nil
	}
	return &value
}

// API 使用 pending 表达待审批，持久化层和 BPMN 流程统一使用 submitted。
func persistedChangeStatus(status string) string {
	if status == string(dto.ChangeStatusPending) {
		return common.ChangeStatusSubmitted
	}
	return status
}

func apiChangeStatus(status string) dto.ChangeStatus {
	if status == common.ChangeStatusSubmitted {
		return dto.ChangeStatusPending
	}
	return dto.ChangeStatus(status)
}

// isTerminalChangeStatus 判断变更状态是否为终态（不可再转换）
func isTerminalChangeStatus(status string) bool {
	switch status {
	case common.ChangeStatusRejected,
		common.ChangeStatusCompleted,
		common.ChangeStatusCancelled,
		string(dto.ChangeStatusRolledBack):
		return true
	}
	return false
}

// CloseChangeApprovalChains 在变更进入终态时收口残留 pending 审批链节点（对外导出，handlers 包直接复用）。
// 注意：调用方需自行保证已通过状态机校验且已将变更写入终态。
func CloseChangeApprovalChains(ctx context.Context, changeID, tenantID int) error {
	rawDB := database.GetRawDB()
	if rawDB == nil {
		return fmt.Errorf("raw database handle unavailable, skip closing change_approval_chains")
	}
	_, err := rawDB.ExecContext(ctx, `
		UPDATE change_approval_chains
		SET status = 'obsolete', updated_at = CURRENT_TIMESTAMP
		WHERE change_id = $1 AND tenant_id = $2 AND status = 'pending'
	`, changeID, tenantID)
	return err
}

// isValidChangeStatusTransition 检查变更状态转换是否合法
// Change状态转换规则:
// draft -> submitted, cancelled
// submitted -> approved, rejected, cancelled
// approved -> scheduled, cancelled
// rejected -> (不允许转换到其他状态)
// scheduled -> in_progress, cancelled
// in_progress -> completed, failed, cancelled
// completed -> (不允许转换到其他状态)
// failed -> scheduled, cancelled
// cancelled -> (不允许转换到其他状态)
// IsValidChangeStatusTransition 检查变更状态转换是否合法
// 根据ITIL标准，不同类型的变更有不同的状态转换规则
func IsValidChangeStatusTransition(currentStatus, newStatus, changeType string) bool {
	// 历史兼容：handlers/change 模块使用 "pending"，而 common 常量用 "submitted"，
	// 两者是等价的状态（变更已提交等待CAB审批）。在此处做归一化，避免状态机误判。
	if currentStatus == "pending" {
		currentStatus = common.ChangeStatusSubmitted
	}

	// 基础转换规则（适用于所有变更类型）
	baseTransitions := map[string][]string{
		common.ChangeStatusRejected:        {}, // 被拒绝后不允许转换
		common.ChangeStatusCompleted:       {}, // 已完成不允许转换
		common.ChangeStatusCancelled:       {}, // 已取消不允许转换
		string(dto.ChangeStatusRolledBack): {},
	}

	// 不同变更类型的特殊转换规则
	var typeSpecificTransitions map[string][]string
	switch changeType {
	case string(dto.ChangeTypeStandard):
		// 标准变更：预授权，可以跳过审批步骤
		typeSpecificTransitions = map[string][]string{
			common.ChangeStatusDraft:      {common.ChangeStatusSubmitted, common.ChangeStatusApproved, common.ChangeStatusScheduled, common.ChangeStatusInProgress, common.ChangeStatusCancelled},
			common.ChangeStatusSubmitted:  {common.ChangeStatusApproved, common.ChangeStatusRejected, common.ChangeStatusCancelled},
			common.ChangeStatusApproved:   {common.ChangeStatusScheduled, common.ChangeStatusInProgress, common.ChangeStatusCancelled},
			common.ChangeStatusScheduled:  {common.ChangeStatusInProgress, common.ChangeStatusCancelled},
			common.ChangeStatusInProgress: {common.ChangeStatusCompleted, common.ChangeStatusFailed, string(dto.ChangeStatusRolledBack), common.ChangeStatusCancelled},
			common.ChangeStatusFailed:     {common.ChangeStatusScheduled, string(dto.ChangeStatusRolledBack), common.ChangeStatusCancelled},
		}
	case string(dto.ChangeTypeEmergency):
		// 紧急变更：可以跳过多个步骤，快速实施
		typeSpecificTransitions = map[string][]string{
			common.ChangeStatusDraft:      {common.ChangeStatusSubmitted, common.ChangeStatusApproved, common.ChangeStatusInProgress, common.ChangeStatusCancelled},
			common.ChangeStatusSubmitted:  {common.ChangeStatusApproved, common.ChangeStatusRejected, common.ChangeStatusCancelled},
			common.ChangeStatusApproved:   {common.ChangeStatusInProgress, common.ChangeStatusCancelled},
			common.ChangeStatusInProgress: {common.ChangeStatusCompleted, common.ChangeStatusFailed, string(dto.ChangeStatusRolledBack), common.ChangeStatusCancelled},
			common.ChangeStatusFailed:     {common.ChangeStatusScheduled, string(dto.ChangeStatusRolledBack), common.ChangeStatusCancelled},
		}
	default: // 普通变更：严格的ITIL流程
		typeSpecificTransitions = map[string][]string{
			common.ChangeStatusDraft:      {common.ChangeStatusSubmitted, common.ChangeStatusCancelled},
			common.ChangeStatusSubmitted:  {common.ChangeStatusApproved, common.ChangeStatusRejected, common.ChangeStatusCancelled},
			common.ChangeStatusApproved:   {common.ChangeStatusScheduled, common.ChangeStatusCancelled},
			common.ChangeStatusScheduled:  {common.ChangeStatusInProgress, common.ChangeStatusCancelled},
			common.ChangeStatusInProgress: {common.ChangeStatusCompleted, common.ChangeStatusFailed, string(dto.ChangeStatusRolledBack), common.ChangeStatusCancelled},
			common.ChangeStatusFailed:     {common.ChangeStatusScheduled, string(dto.ChangeStatusRolledBack), common.ChangeStatusCancelled},
		}
	}

	// 合并基础规则和类型特定规则
	validTransitions := make(map[string][]string)
	for k, v := range baseTransitions {
		validTransitions[k] = v
	}
	for k, v := range typeSpecificTransitions {
		validTransitions[k] = v
	}

	allowed, ok := validTransitions[currentStatus]
	if !ok {
		// 未知状态必须失败关闭，避免绕过变更生命周期约束。
		return false
	}

	for _, status := range allowed {
		if status == newStatus {
			return true
		}
	}
	return false
}
