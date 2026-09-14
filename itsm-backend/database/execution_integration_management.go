package database

import (
	"context"

	"itsm-backend/common/executionscope"
	"itsm-backend/common/tenantctx"
)

// RequireIntegrationManagement restricts configuration mutations by deployment.
// Standard callers still require the existing RBAC and domain authorization.
// Candidate target declarations never grant request-time configuration rights.
func (p *ExecutionPolicy) RequireIntegrationManagement(ctx context.Context, tenantID int) error {
	if p == nil || p.mode != "standard" || ctx == nil || tenantID <= 0 || tenantctx.IsSystemBypass(ctx) {
		return executionscope.ErrDenied
	}
	if err := ctx.Err(); err != nil {
		return err
	}
	actual, ok := tenantctx.TenantID(ctx)
	if !ok || actual != tenantID {
		return executionscope.ErrDenied
	}
	return nil
}
