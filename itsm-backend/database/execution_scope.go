package database

import (
	"context"
	"database/sql"
	"fmt"
	"strconv"

	"itsm-backend/common/executionscope"
	"itsm-backend/common/tenantctx"
)

func validateExecutionContext(ctx context.Context, tx *sql.Tx, ref executionscope.Ref) error {
	if err := executionscope.ValidateRef(ref); err != nil {
		return err
	}
	tenantID, ok := tenantctx.TenantID(ctx)
	if tx == nil || !ok || tenantID != ref.TenantID || tenantctx.IsSystemBypass(ctx) {
		return fmt.Errorf("%w: explicit tenant transaction required", executionscope.ErrDenied)
	}
	return nil
}

// BindExecutionScope binds an authorized tenant transaction to its configured deployment.
// The caller still owns all business authorization and the transaction lifetime.
func BindExecutionScope(ctx context.Context, tx *sql.Tx, ref executionscope.Ref) error {
	if err := validateExecutionContext(ctx, tx, ref); err != nil {
		return err
	}
	if _, err := tx.ExecContext(ctx, `SELECT set_config('app.current_tenant',$1,true)`, strconv.Itoa(ref.TenantID)); err != nil {
		return err
	}
	var id string
	err := tx.QueryRowContext(ctx, `SELECT s.id::text FROM public.execution_scopes s
JOIN public.execution_runtime_bindings b ON b.deployment_id=s.deployment_id
WHERE b.runtime_role=session_user AND b.mode='candidate'
AND s.id=$1 AND s.deployment_id=$2 AND s.tenant_id=$3 AND s.status='active'`, ref.ScopeID, ref.DeploymentID, ref.TenantID).Scan(&id)
	if err == sql.ErrNoRows {
		return executionscope.ErrDenied
	}
	if err != nil {
		return fmt.Errorf("validate execution scope: %w", err)
	}
	_, err = tx.ExecContext(ctx, `SELECT set_config('app.execution_scope_id',$1,true)`, id)
	return err
}

// RequireExecutionMember never registers existing records or substitutes a second transaction.
func RequireExecutionMember(ctx context.Context, tx *sql.Tx, ref executionscope.Ref, workItemID int) error {
	if err := validateExecutionContext(ctx, tx, ref); err != nil {
		return err
	}
	if workItemID <= 0 {
		return executionscope.ErrDenied
	}
	var id int
	err := tx.QueryRowContext(ctx, `SELECT m.work_item_id FROM public.execution_scope_members m
JOIN public.execution_scopes s ON s.id=m.scope_id
JOIN public.execution_runtime_bindings b ON b.deployment_id=s.deployment_id
WHERE b.runtime_role=session_user AND b.mode='candidate'
AND s.id=$1 AND s.deployment_id=$2 AND s.tenant_id=$3 AND s.status='active'
AND m.work_item_id=$4 AND current_setting('app.execution_scope_id',true)=$1::text
AND current_setting('app.current_tenant',true)=$5`, ref.ScopeID, ref.DeploymentID, ref.TenantID, workItemID, strconv.Itoa(ref.TenantID)).Scan(&id)
	if err == sql.ErrNoRows {
		return executionscope.ErrDenied
	}
	if err != nil {
		return fmt.Errorf("verify execution member: %w", err)
	}
	return nil
}
