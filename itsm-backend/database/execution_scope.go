package database

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"strconv"

	"itsm-backend/common/executionscope"
	"itsm-backend/common/tenantctx"
	"itsm-backend/ent"
)

func validateExecutionContext(ctx context.Context, ref executionscope.Ref) error {
	if ctx == nil {
		return executionscope.ErrDenied
	}
	if err := executionscope.ValidateRef(ref); err != nil {
		return err
	}
	tenantID, ok := tenantctx.TenantID(ctx)
	if !ok || tenantID != ref.TenantID || tenantctx.IsSystemBypass(ctx) {
		return fmt.Errorf("%w: explicit tenant transaction required", executionscope.ErrDenied)
	}
	return nil
}

// BindExecutionScope binds an authorized tenant transaction to its configured deployment.
// The caller still owns all business authorization and the transaction lifetime.
func BindExecutionScope(ctx context.Context, tx *sql.Tx, ref executionscope.Ref) error {
	if tx == nil {
		return executionscope.ErrDenied
	}
	return bindExecutionScope(ctx, tx, ref)
}

// BindEntExecutionScope uses the caller's Ent transaction driver, never a new connection.
func BindEntExecutionScope(ctx context.Context, tx *ent.Tx, ref executionscope.Ref) error {
	if tx == nil {
		return executionscope.ErrDenied
	}
	return bindExecutionScope(ctx, tx.Client(), ref)
}

func bindExecutionScope(ctx context.Context, tx executionTransaction, ref executionscope.Ref) error {
	if err := validateExecutionContext(ctx, ref); err != nil {
		return err
	}
	if _, err := tx.ExecContext(ctx, `SELECT set_config('app.current_tenant',$1,true)`, strconv.Itoa(ref.TenantID)); err != nil {
		return err
	}
	var id string
	err := scanExecutionRow(ctx, tx, &id, `SELECT s.id::text FROM public.execution_scopes s
JOIN public.execution_runtime_bindings b ON b.deployment_id=s.deployment_id
WHERE b.runtime_role=session_user AND b.mode='candidate'
AND s.id=$1 AND s.deployment_id=$2 AND s.tenant_id=$3 AND s.status='active'`, ref.ScopeID, ref.DeploymentID, ref.TenantID)
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
	if tx == nil {
		return executionscope.ErrDenied
	}
	return requireExecutionMember(ctx, tx, ref, workItemID)
}

// RequireEntExecutionMember observes uncommitted membership in the original transaction.
func RequireEntExecutionMember(ctx context.Context, tx *ent.Tx, ref executionscope.Ref, workItemID int) error {
	if tx == nil {
		return executionscope.ErrDenied
	}
	return requireExecutionMember(ctx, tx.Client(), ref, workItemID)
}

func requireExecutionMember(ctx context.Context, tx executionTransaction, ref executionscope.Ref, workItemID int) error {
	if err := validateExecutionContext(ctx, ref); err != nil {
		return err
	}
	if workItemID <= 0 {
		return executionscope.ErrDenied
	}
	var id int
	err := scanExecutionRow(ctx, tx, &id, `SELECT m.work_item_id FROM public.execution_scope_members m
JOIN public.execution_scopes s ON s.id=m.scope_id
JOIN public.execution_runtime_bindings b ON b.deployment_id=s.deployment_id
WHERE b.runtime_role=session_user AND b.mode='candidate'
AND s.id=$1 AND s.deployment_id=$2 AND s.tenant_id=$3 AND s.status='active'
AND m.work_item_id=$4 AND current_setting('app.execution_scope_id',true)=$1::text
AND current_setting('app.current_tenant',true)=$5`, ref.ScopeID, ref.DeploymentID, ref.TenantID, workItemID, strconv.Itoa(ref.TenantID))
	if err == sql.ErrNoRows {
		return executionscope.ErrDenied
	}
	if err != nil {
		return fmt.Errorf("verify execution member: %w", err)
	}
	return nil
}

// Both database/sql.Tx and Ent's transaction-bound Client expose these methods.
type executionTransaction interface {
	ExecContext(context.Context, string, ...any) (sql.Result, error)
	QueryContext(context.Context, string, ...any) (*sql.Rows, error)
}

func scanExecutionRow(ctx context.Context, tx executionTransaction, destination any, query string, args ...any) error {
	rows, err := tx.QueryContext(ctx, query, args...)
	if err != nil {
		return err
	}
	if !rows.Next() {
		err = rows.Err()
		closeErr := rows.Close()
		if err != nil || closeErr != nil {
			return errors.Join(err, closeErr)
		}
		return sql.ErrNoRows
	}
	err = rows.Scan(destination)
	return errors.Join(err, rows.Err(), rows.Close())
}
