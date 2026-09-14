package database

import (
	"context"
	"database/sql"
	"fmt"

	"itsm-backend/common/executionscope"
	"itsm-backend/common/tenantctx"
	"itsm-backend/config"
)

// ValidateExecutionRuntime is a read-only admission check on the tenant pool.
// The separately restricted transport pool retains its own privilege contract.
func ValidateExecutionRuntime(ctx context.Context, db *sql.DB, cfg config.ExecutionConfig) error {
	if err := cfg.Validate(); err != nil {
		return err
	}
	if db == nil {
		return fmt.Errorf("execution runtime database required")
	}
	var unsafe bool
	err := db.QueryRowContext(ctx, `SELECT current_user<>session_user OR r.rolsuper OR r.rolbypassrls OR r.rolcreaterole OR r.rolcreatedb OR r.rolreplication
OR EXISTS(SELECT 1 FROM pg_auth_members WHERE member=r.oid)
OR EXISTS(SELECT 1 FROM pg_class c JOIN pg_namespace n ON n.oid=c.relnamespace WHERE n.nspname='public' AND c.relowner=r.oid)
OR has_database_privilege(current_database(),'CREATE') OR has_schema_privilege('public','CREATE')
FROM pg_roles r WHERE r.rolname=current_user`).Scan(&unsafe)
	if err != nil {
		return fmt.Errorf("inspect execution runtime role: %w", err)
	}
	if unsafe {
		return fmt.Errorf("execution runtime requires a restricted non-owner tenant identity")
	}
	for _, table := range []string{"public.execution_scopes", "public.execution_scope_members", "public.execution_runtime_bindings", "public.execution_tool_invocations"} {
		var readable bool
		err = db.QueryRowContext(ctx, `SELECT has_table_privilege($1,'SELECT'),
has_table_privilege($1,'INSERT,UPDATE,DELETE,TRUNCATE,REFERENCES,TRIGGER')
OR has_any_column_privilege($1,'INSERT,UPDATE,REFERENCES')`, table).Scan(&readable, &unsafe)
		if err != nil {
			return fmt.Errorf("inspect execution scope permissions: %w", err)
		}
		if !readable || unsafe {
			return fmt.Errorf("execution scope objects require read-only privileges")
		}
	}
	for _, function := range []string{"public.register_new_execution_member()", "public.register_new_execution_tool_invocation()"} {
		if err = db.QueryRowContext(ctx, `SELECT has_function_privilege($1,'EXECUTE')`, function).Scan(&unsafe); err != nil {
			return fmt.Errorf("inspect execution enrollment privileges: %w", err)
		}
		if unsafe {
			return fmt.Errorf("runtime cannot execute enrollment trigger directly")
		}
	}
	if cfg.Mode == "candidate" {
		var available bool
		if err = db.QueryRowContext(ctx, `SELECT has_function_privilege('public.lock_candidate_tool_authorization(uuid,text,bigint,bigint,bigint)','EXECUTE')`).Scan(&available); err != nil {
			return fmt.Errorf("inspect tool authority lock capability: %w", err)
		}
		if !available {
			return fmt.Errorf("candidate runtime requires tool authority lock capability")
		}
	}
	var configured bool
	err = db.QueryRowContext(ctx, `SELECT EXISTS(SELECT 1 FROM public.execution_runtime_bindings WHERE runtime_role=session_user AND deployment_id=$1 AND mode=$2)`, cfg.DeploymentID, cfg.Mode).Scan(&configured)
	if err != nil {
		return err
	}
	if !configured {
		return fmt.Errorf("execution runtime binding does not match configuration")
	}
	for _, s := range cfg.Scopes {
		tenantCtx := tenantctx.WithTenantID(ctx, s.TenantID)
		tx, err := db.BeginTx(tenantCtx, &sql.TxOptions{ReadOnly: true})
		if err != nil {
			return err
		}
		bindErr := BindExecutionScope(tenantCtx, tx, executionscope.Ref{DeploymentID: cfg.DeploymentID, ScopeID: s.ScopeID, TenantID: s.TenantID})
		rollbackErr := tx.Rollback()
		if bindErr != nil {
			return bindErr
		}
		if rollbackErr != nil {
			return rollbackErr
		}
	}
	return nil
}
