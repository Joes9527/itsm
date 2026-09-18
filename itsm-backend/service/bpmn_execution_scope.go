package service

import (
	"context"

	"itsm-backend/common/executionscope"
	"itsm-backend/common/tenantctx"
	"itsm-backend/database"
	"itsm-backend/ent"
)

// requireBPMNExecution keeps instance management within the original
// mutation transaction; an independent or historical instance is never enrolled.
func requireBPMNExecution(ctx context.Context, tx *ent.Tx, execution *database.ExecutionPolicy, tenantID int, workItemID *int) error {
	if execution == nil || tx == nil || tenantID <= 0 {
		return executionscope.ErrDenied
	}
	if !execution.IsCandidate() {
		return nil
	}
	if workItemID == nil || *workItemID <= 0 {
		return executionscope.ErrDenied
	}
	if tenantctx.IsSystemBypass(ctx) {
		return executionscope.ErrDenied
	}
	if boundTenantID, ok := tenantctx.TenantID(ctx); ok && boundTenantID != tenantID {
		return executionscope.ErrDenied
	}
	ctx = tenantctx.WithTenantID(ctx, tenantID)
	if err := execution.BindEnt(ctx, tx, tenantID); err != nil {
		return err
	}
	return execution.RequireEntMembers(ctx, tx, tenantID, *workItemID)
}
