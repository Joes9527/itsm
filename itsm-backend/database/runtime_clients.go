package database

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"strings"
	"time"

	entsql "entgo.io/ent/dialect/sql"
	"go.uber.org/zap"
	"itsm-backend/config"
	"itsm-backend/ent"
)

// RuntimeClients keeps the tenant execution pool separate from the restricted
// cross-tenant directory/transport capability. It never opens migration credentials.
type RuntimeClients struct {
	Tenant   *ent.Client
	System   *ent.Client
	SystemDB *sql.DB
}

func (c *RuntimeClients) Close() error { return errors.Join(c.Tenant.Close(), c.System.Close()) }

func InitRuntimeDatabases(cfg *config.DatabaseConfig, rlsCfg *config.RLSConfig, logger *zap.SugaredLogger) (*RuntimeClients, error) {
	if cfg.SystemRoleUser == "" || cfg.SystemRoleUser == cfg.User {
		return nil, fmt.Errorf("DB_SYSTEM_ROLE_USER must name a separate restricted system role; migration/runtime credential fallback is not supported")
	}
	systemCfg := *cfg
	systemCfg.User, systemCfg.Password = cfg.SystemRoleUser, cfg.SystemRolePassword
	systemDB, err := InitDB(&systemCfg)
	if err != nil {
		return nil, fmt.Errorf("connect restricted system database: %w", err)
	}
	keep := false
	defer func() {
		if !keep {
			_ = systemDB.Close()
		}
	}()
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	if err := validateSystemPrivileges(ctx, systemDB); err != nil {
		return nil, err
	}
	tenant, err := InitDatabaseWithRLS(cfg, rlsCfg, logger)
	if err != nil {
		return nil, err
	}
	// A tenant role owning a policy table bypasses non-FORCE RLS even without
	// BYPASSRLS. Validate ownership at construction as well as per-query role checks.
	if rlsCfg != nil && rlsCfg.Mode == "enforce" {
		var unsafe bool
		err = rawDB.QueryRowContext(ctx, `SELECT r.rolsuper OR r.rolbypassrls OR r.rolcreaterole OR EXISTS(SELECT 1 FROM pg_class c JOIN pg_namespace n ON n.oid=c.relnamespace WHERE n.nspname=current_schema() AND c.relowner=r.oid) OR EXISTS(SELECT 1 FROM pg_auth_members WHERE member=r.oid) FROM pg_roles r WHERE rolname=current_user`).Scan(&unsafe)
		if err != nil {
			_ = tenant.Close()
			return nil, fmt.Errorf("verify runtime database role: %w", err)
		}
		if unsafe {
			_ = tenant.Close()
			return nil, fmt.Errorf("runtime database role must be non-superuser/non-bypass, non-owner and have no role memberships")
		}

	}
	system := ent.NewClient(ent.Driver(entsql.OpenDB("postgres", systemDB)))
	RegisterSoftDeleteInterceptors(system)
	keep = true
	return &RuntimeClients{Tenant: tenant, System: system, SystemDB: systemDB}, nil
}

// These grants belong to session authentication/directory lookup, connector
// restoration and durable outbox transport. Business producers use Tenant.
// Audit INSERT plus SELECT(id) permits Ent RETURNING without reading audit content.
var systemTablePrivileges = map[string]string{
	"users": "SELECT", "tenants": "SELECT", "msp_allocations": "SELECT",
	"connector_configs": "SELECT", "outbox_events": "SELECT,UPDATE", "audit_logs": "INSERT",
}

func validateSystemPrivileges(ctx context.Context, db *sql.DB) error {
	var unsafe bool
	if err := db.QueryRowContext(ctx, `SELECT rolsuper OR rolinherit OR NOT rolbypassrls OR rolcreaterole OR rolcreatedb OR rolreplication OR EXISTS(SELECT 1 FROM pg_auth_members WHERE member=pg_roles.oid) FROM pg_roles WHERE rolname=current_user`).Scan(&unsafe); err != nil {
		return fmt.Errorf("verify system role: %w", err)
	}
	if unsafe {
		return fmt.Errorf("system database role requires NOSUPERUSER BYPASSRLS NOCREATEROLE NOCREATEDB NOREPLICATION NOINHERIT and no role memberships")
	}
	var schema string
	if err := db.QueryRowContext(ctx, `SELECT current_schema()`).Scan(&schema); err != nil {
		return err
	}
	// Include inherited/PUBLIC/column privileges and ownership. No table outside
	// the exact selected-schema allowlist may be accessible through this role.
	rows, err := db.QueryContext(ctx, `SELECT n.nspname,c.relname,p.name,has_table_privilege(c.oid,p.name),CASE WHEN p.name IN ('SELECT','INSERT','UPDATE','REFERENCES') THEN has_any_column_privilege(c.oid,p.name) ELSE false END,c.relowner=(SELECT oid FROM pg_roles WHERE rolname=current_user) FROM pg_class c JOIN pg_namespace n ON n.oid=c.relnamespace CROSS JOIN (VALUES('SELECT'),('INSERT'),('UPDATE'),('DELETE'),('TRUNCATE'),('REFERENCES'),('TRIGGER')) p(name) WHERE n.nspname NOT IN ('pg_catalog','information_schema') AND n.nspname NOT LIKE 'pg_toast%' AND c.relkind IN ('r','p','v','m','f')`)
	if err != nil {
		return err
	}
	var mismatch string
	for rows.Next() {
		var ns, table, priv string
		var tableAccess, columnAccess, owner bool
		if err := rows.Scan(&ns, &table, &priv, &tableAccess, &columnAccess, &owner); err != nil {
			_ = rows.Close()
			return err
		}
		allowed := ns == schema && strings.Contains(","+systemTablePrivileges[table]+",", ","+priv+",")
		auditID := ns == schema && table == "audit_logs" && priv == "SELECT" && !tableAccess
		if owner || (allowed && !tableAccess) || (!allowed && (tableAccess || columnAccess) && !auditID) {
			mismatch = fmt.Sprintf("%s.%s %s", ns, table, priv)
			break
		}
	}
	err = errors.Join(rows.Err(), rows.Close())
	if err != nil {
		return err
	}
	if mismatch != "" {
		return fmt.Errorf("system database privileges differ from restricted contract: %s", mismatch)
	}
	var auditOK bool
	if err := db.QueryRowContext(ctx, `SELECT bool_and(has_column_privilege('audit_logs',attname,'SELECT')=(attname='id')) FROM pg_attribute WHERE attrelid='audit_logs'::regclass AND attnum>0 AND NOT attisdropped`).Scan(&auditOK); err != nil {
		return err
	}
	if !auditOK {
		return fmt.Errorf("system audit read capability must be SELECT(id) only")
	}
	if err := db.QueryRowContext(ctx, `SELECT EXISTS(SELECT 1 FROM pg_class c JOIN pg_namespace n ON n.oid=c.relnamespace WHERE c.relkind='S' AND n.nspname NOT LIKE 'pg_%' AND (has_sequence_privilege(c.oid,'SELECT') OR has_sequence_privilege(c.oid,'UPDATE') OR (has_sequence_privilege(c.oid,'USAGE') AND c.oid<>pg_get_serial_sequence('audit_logs','id')::regclass))) OR NOT has_sequence_privilege(pg_get_serial_sequence('audit_logs','id'),'USAGE')`).Scan(&unsafe); err != nil {
		return err
	}
	if unsafe {
		return fmt.Errorf("system sequence privileges must be audit_logs id USAGE only")
	}
	// Fail missing tables and schema/database DDL rights, including PUBLIC grants.
	for table, privileges := range systemTablePrivileges {
		for _, privilege := range strings.Split(privileges, ",") {
			var granted bool
			if err := db.QueryRowContext(ctx, `SELECT has_table_privilege($1,$2)`, table, privilege).Scan(&granted); err != nil {
				return fmt.Errorf("system capability %s: %w", table, err)
			}
			if !granted {
				return fmt.Errorf("system capability missing: %s %s", table, privilege)
			}
		}
	}
	if err := db.QueryRowContext(ctx, `SELECT has_database_privilege(current_database(),'CREATE') OR EXISTS(SELECT 1 FROM pg_namespace WHERE nspname NOT LIKE 'pg_%' AND nspname <> 'information_schema' AND has_schema_privilege(oid,'CREATE')) OR EXISTS(SELECT 1 FROM pg_proc p JOIN pg_namespace n ON n.oid=p.pronamespace WHERE p.prosecdef AND n.nspname NOT IN ('pg_catalog','information_schema') AND has_function_privilege(p.oid,'EXECUTE'))`).Scan(&unsafe); err != nil {
		return err
	}
	if unsafe {
		return fmt.Errorf("system role must not have database/schema CREATE or application SECURITY DEFINER execution privileges")
	}
	return nil
}
