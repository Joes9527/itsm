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
