package service

import (
	"context"
	"fmt"

	"itsm-backend/database"
)

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
