package migration

import (
	"context"
	"database/sql"
	"fmt"
	"go.uber.org/zap"
	"net/url"
	"os"
)

// Only an independently pinned, non-elevated inspection role may read evidence.
// Application roles remain denied; neither column ACLs nor grant options are allowed.
func validateEvidenceACL(ctx context.Context, q migrationQuery, schema, role string) error {
	if role != "" {
		if err := validateInspectionRole(ctx, q, schema, role); err != nil {
			return err
		}
	}
	rows, err := q.QueryContext(ctx, `SELECT coalesce(r.rolname,'PUBLIC'),a.privilege_type,a.is_grantable FROM pg_class c CROSS JOIN LATERAL aclexplode(c.relacl) a LEFT JOIN pg_roles r ON r.oid=a.grantee WHERE c.oid=$1::regclass AND a.grantee<>c.relowner`, preparationRelation(schema, "work_item_migration_evidence"))
	if err != nil {
		return err
	}
	defer rows.Close()
	found := false
	for rows.Next() {
		var grantee, privilege string
		var grantable bool
		if err = rows.Scan(&grantee, &privilege, &grantable); err != nil {
			return err
		}
		if role == "" || grantee != role || privilege != "SELECT" || grantable {
			return fmt.Errorf("unreviewed evidence attachment access")
		}
		found = true
	}
	if err = rows.Err(); err != nil {
		return err
	}
	if role != "" && !found {
		return fmt.Errorf("configured inspection evidence SELECT grant is missing")
	}
	var columnGrant bool
	if err = q.QueryRowContext(ctx, `SELECT EXISTS(SELECT 1 FROM pg_attribute WHERE attrelid=$1::regclass AND attacl IS NOT NULL)`, preparationRelation(schema, "work_item_migration_evidence")).Scan(&columnGrant); err != nil {
		return err
	}
	if columnGrant {
		return fmt.Errorf("unreviewed evidence column access")
	}
	return nil
}

func validateInspectionRole(ctx context.Context, q migrationQuery, schema, role string) error {
	var unsafe bool
	err := q.QueryRowContext(ctx, `SELECT r.rolsuper OR r.rolbypassrls OR r.rolcreaterole OR r.rolcreatedb OR r.rolreplication
 OR EXISTS(SELECT 1 FROM pg_auth_members WHERE member=r.oid OR roleid=r.oid)
 OR EXISTS(SELECT 1 FROM pg_class WHERE relnamespace=$2::regnamespace AND relowner=r.oid)
 OR EXISTS(SELECT 1 FROM pg_proc WHERE pronamespace=$2::regnamespace AND proowner=r.oid)
 OR has_schema_privilege(r.oid,$2,'CREATE')
 OR EXISTS(SELECT 1 FROM pg_class c CROSS JOIN LATERAL aclexplode(c.relacl) a WHERE c.relnamespace=$2::regnamespace AND a.grantee=r.oid AND a.is_grantable)
 OR EXISTS(SELECT 1 FROM pg_class c WHERE c.relnamespace=$2::regnamespace AND c.relkind IN ('r','p','v','m','f') AND
 (has_table_privilege(r.oid,c.oid,'INSERT,UPDATE,DELETE,TRUNCATE,REFERENCES,TRIGGER') OR has_any_column_privilege(r.oid,c.oid,'INSERT,UPDATE,REFERENCES') OR
 (c.relname NOT IN ('schema_migrations','work_item_migration_evidence') AND has_any_column_privilege(r.oid,c.oid,'SELECT'))))
 OR EXISTS(SELECT 1 FROM pg_class c WHERE c.relnamespace=$2::regnamespace AND CASE WHEN c.relkind='S' THEN has_sequence_privilege(r.oid,c.oid,'USAGE,SELECT,UPDATE') ELSE false END)
 FROM pg_roles r WHERE r.rolname=$1`, role, schema).Scan(&unsafe)
	if err != nil {
		return fmt.Errorf("inspect configured inspection role: %w", err)
	}
	if unsafe {
		return fmt.Errorf("inspection role must have only ledger/evidence SELECT, without ownership, business access, writes or role escalation")
	}
	return nil
}

// InspectRuntimeDatabase opens a distinct, explicitly configured read-only
// connection. It never reuses business credentials or falls back to owner access.
func InspectRuntimeDatabase(ctx context.Context, business *sql.DB, config MigrationControlConfig) error {
	if config.DeploymentID == "" || config.InspectionRole == "" {
		return fmt.Errorf("runtime migration admission requires explicit inspection identity and deployment configuration")
	}
	raw := os.Getenv("ITSM_MIGRATION_INSPECTION_DSN")
	target, err := url.Parse(raw)
	if err != nil || (target.Scheme != "postgres" && target.Scheme != "postgresql") || target.Host == "" {
		return fmt.Errorf("explicit PostgreSQL inspection connection is required")
	}
	if target.User == nil || target.User.Username() != config.InspectionRole {
		return fmt.Errorf("inspection connection role differs from trusted configuration")
	}
	params := target.Query()
	params.Set("default_transaction_read_only", "on")
	target.RawQuery = params.Encode()
	inspector, err := sql.Open("postgres", target.String())
	if err != nil {
		return fmt.Errorf("cannot open inspection connection")
	}
	defer inspector.Close()
	inspector.SetMaxOpenConns(2)
	fingerprint := func(db *sql.DB) (string, error) {
		tx, err := db.BeginTx(ctx, &sql.TxOptions{ReadOnly: true})
		if err != nil {
			return "", fmt.Errorf("cannot inspect database target")
		}
		defer tx.Rollback()
		schema, err := migrationTargetSchema(ctx, tx)
		if err != nil {
			return "", err
		}
		var database, address string
		var port sql.NullInt64
		if err = tx.QueryRowContext(ctx, "SELECT current_database(),coalesce(inet_server_addr()::text,'local'),inet_server_port()").Scan(&database, &address, &port); err != nil {
			return "", fmt.Errorf("cannot identify database target")
		}
		return fmt.Sprintf("%s/%s/%s/%d", database, schema, address, port.Int64), nil
	}
	businessTarget, err := fingerprint(business)
	if err != nil {
		return err
	}
	inspectionTarget, err := fingerprint(inspector)
	if err != nil {
		return err
	}
	if businessTarget != inspectionTarget {
		return fmt.Errorf("inspection connection database/schema/server does not match runtime target")
	}
	var actual string
	if err = inspector.QueryRowContext(ctx, "SELECT current_user").Scan(&actual); err != nil {
		return fmt.Errorf("cannot identify inspection role")
	}
	if actual != config.InspectionRole {
		return fmt.Errorf("actual inspection role differs from trusted configuration")
	}
	schema, err := migrationTargetSchema(ctx, inspector)
	if err != nil {
		return err
	}
	if err = validateInspectionRole(ctx, inspector, schema, config.InspectionRole); err != nil {
		return err
	}
	return NewMigrator(inspector, zap.NewNop().Sugar(), config).InspectRuntimeMigrations(ctx)
}
