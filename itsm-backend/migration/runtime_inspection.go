package migration

import (
	"context"
	"crypto/rand"
	"database/sql"
	"encoding/binary"
	"fmt"
	"net/url"
	"os"
	"time"
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
 OR EXISTS(SELECT 1 FROM pg_class c CROSS JOIN LATERAL aclexplode(c.relacl) a WHERE c.relnamespace=$2::regnamespace AND a.grantee IN (0,r.oid) AND a.is_grantable)
 OR EXISTS(SELECT 1 FROM pg_attribute col JOIN pg_class c ON c.oid=col.attrelid CROSS JOIN LATERAL aclexplode(col.attacl) a WHERE c.relnamespace=$2::regnamespace AND a.grantee IN (0,r.oid) AND a.is_grantable)
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
	ctx, cancel := context.WithTimeout(ctx, 10*time.Second)
	defer cancel()
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
	businessTx, err := business.BeginTx(ctx, &sql.TxOptions{ReadOnly: true})
	if err != nil {
		return fmt.Errorf("cannot inspect business target")
	}
	defer businessTx.Rollback()
	inspectionTx, err := inspector.BeginTx(ctx, &sql.TxOptions{ReadOnly: true, Isolation: sql.LevelRepeatableRead})
	if err != nil {
		return fmt.Errorf("cannot inspect inspection target")
	}
	defer inspectionTx.Rollback()
	if err = verifyRuntimeConnectionBinding(ctx, businessTx, inspectionTx); err != nil {
		return err
	}
	var actual string
	if err = inspectionTx.QueryRowContext(ctx, "SELECT current_user").Scan(&actual); err != nil {
		return fmt.Errorf("cannot identify inspection role")
	}
	if actual != config.InspectionRole {
		return fmt.Errorf("actual inspection role differs from trusted configuration")
	}
	schema, err := migrationTargetSchema(ctx, inspectionTx)
	if err != nil {
		return err
	}
	if err = validateInspectionRole(ctx, inspectionTx, schema, config.InspectionRole); err != nil {
		return err
	}
	return inspectRuntimeMigrations(ctx, inspectionTx, config)
}

func runtimeConnectionFingerprint(ctx context.Context, q migrationQuery) (string, error) {
	schema, err := migrationTargetSchema(ctx, q)
	if err != nil {
		return "", err
	}
	var database, address string
	var port sql.NullInt64
	if err = q.QueryRowContext(ctx, "SELECT current_database(),coalesce(inet_server_addr()::text,'local'),inet_server_port()").Scan(&database, &address, &port); err != nil {
		return "", fmt.Errorf("cannot identify database target")
	}
	return fmt.Sprintf("%s/%s/%s/%d", database, schema, address, port.Int64), nil
}

func verifyRuntimeConnectionBinding(ctx context.Context, business, inspection migrationQuery) error {
	businessTarget, err := runtimeConnectionFingerprint(ctx, business)
	if err != nil {
		return err
	}
	inspectionTarget, err := runtimeConnectionFingerprint(ctx, inspection)
	if err != nil {
		return err
	}
	if businessTarget != inspectionTarget {
		return fmt.Errorf("inspection connection database/schema/server does not match runtime target")
	}

	// The tuple is useful for diagnostics but cannot distinguish clusters behind
	// overlapping networks/tunnels. Prove shared live lock state on held read-only
	// transactions, without persistent objects, privileged identity queries or
	// evidence access on the business connection. Never log nonce values.
	var nonce [16]byte
	if _, err = rand.Read(nonce[:]); err != nil {
		return fmt.Errorf("cannot create runtime target proof")
	}
	keys := [2]int64{int64(binary.BigEndian.Uint64(nonce[:8])), int64(binary.BigEndian.Uint64(nonce[8:]))}
	if keys[0] == keys[1] {
		return fmt.Errorf("cannot create distinct runtime target proof")
	}
	var databaseID, pid int64
	if err = business.QueryRowContext(ctx, "SELECT oid::bigint,pg_backend_pid() FROM pg_database WHERE datname=current_database()").Scan(&databaseID, &pid); err != nil {
		return fmt.Errorf("cannot identify business target backend")
	}
	for _, key := range keys {
		var acquired bool
		if err = business.QueryRowContext(ctx, "SELECT pg_try_advisory_xact_lock($1::bigint)", key).Scan(&acquired); err != nil || !acquired {
			return fmt.Errorf("cannot acquire runtime target proof")
		}
	}
	var observed bool
	err = inspection.QueryRowContext(ctx, `SELECT count(*)=2 FROM pg_locks
 WHERE locktype='advisory' AND mode='ExclusiveLock' AND granted
 AND database=$1::oid AND pid=$2 AND objsubid=1
 AND ((classid=$3::oid AND objid=$4::oid) OR (classid=$5::oid AND objid=$6::oid))`,
		databaseID, pid, int64(uint64(keys[0])>>32), int64(uint32(keys[0])), int64(uint64(keys[1])>>32), int64(uint32(keys[1]))).Scan(&observed)
	if err != nil {
		return fmt.Errorf("cannot observe runtime target proof")
	}
	if !observed {
		return fmt.Errorf("inspection connection instance does not match runtime target")
	}
	return nil
}
