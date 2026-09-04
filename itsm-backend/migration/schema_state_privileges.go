package migration

import (
	"context"
	"database/sql"
	"fmt"
	"strings"

	"github.com/lib/pq"
)

const (
	migrationDatabaseUserEnv = "ITSM_MIGRATION_DB_USER"
	runtimeDatabaseUserEnv   = "ITSM_RUNTIME_DB_USER"
)

// SchemaStateRoles names the two deliberately separate database principals.
type SchemaStateRoles struct {
	MigrationRole string
	RuntimeRole   string
}

// LoadSchemaStateRoles reads only the two supported role identifiers.
func LoadSchemaStateRoles(getenv func(string) string) (SchemaStateRoles, error) {
	if getenv == nil {
		return SchemaStateRoles{}, fmt.Errorf("migration role and runtime role configuration is required")
	}
	roles := SchemaStateRoles{
		MigrationRole: strings.TrimSpace(getenv(migrationDatabaseUserEnv)),
		RuntimeRole:   strings.TrimSpace(getenv(runtimeDatabaseUserEnv)),
	}
	if err := validateSchemaStateRoles(roles); err != nil {
		return SchemaStateRoles{}, err
	}
	return roles, nil
}

func validateSchemaStateRoles(roles SchemaStateRoles) error {
	if strings.TrimSpace(roles.MigrationRole) == "" {
		return fmt.Errorf("migration role is required")
	}
	if strings.TrimSpace(roles.RuntimeRole) == "" {
		return fmt.Errorf("runtime role is required")
	}
	if roles.MigrationRole == roles.RuntimeRole {
		return fmt.Errorf("migration role and runtime role must be distinct")
	}
	return nil
}

// ApplySchemaStatePrivileges verifies executor and ownership before granting
// the runtime principal read-only access to the authoritative release marker.
func ApplySchemaStatePrivileges(ctx context.Context, db *sql.DB, roles SchemaStateRoles) error {
	if err := validateSchemaStateRoles(roles); err != nil {
		return err
	}
	if db == nil {
		return fmt.Errorf("schema state privilege database is required")
	}
	return applySchemaStatePrivileges(ctx, db, roles)
}

// ApplySchemaStatePrivilegesOnConnection applies the same contract on the
// dedicated connection that owns the bootstrap advisory lock.
func ApplySchemaStatePrivilegesOnConnection(ctx context.Context, db BootstrapConnection, roles SchemaStateRoles) error {
	return applySchemaStatePrivileges(ctx, db, roles)
}

func applySchemaStatePrivileges(ctx context.Context, db BootstrapConnection, roles SchemaStateRoles) error {
	if err := validateSchemaStateRoles(roles); err != nil {
		return err
	}
	if db == nil {
		return fmt.Errorf("schema state privilege database is required")
	}

	tx, err := db.BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("begin schema state privilege provisioning: database operation failed")
	}
	defer tx.Rollback()

	var currentUser string
	if err := tx.QueryRowContext(ctx, `SELECT current_user`).Scan(&currentUser); err != nil {
		return fmt.Errorf("verify migration role executor: database operation failed")
	}
	if currentUser != roles.MigrationRole {
		return fmt.Errorf("schema state privilege executor is not the migration role")
	}

	var owner string
	if err := tx.QueryRowContext(ctx, `
		SELECT pg_get_userbyid(relation.relowner)
		FROM pg_class relation
		JOIN pg_namespace namespace ON namespace.oid = relation.relnamespace
		WHERE namespace.nspname = current_schema()
		  AND relation.relname = 'schema_state'
		  AND relation.relkind IN ('r', 'p')
	`).Scan(&owner); err != nil {
		return fmt.Errorf("verify schema state migration role ownership: database operation failed")
	}
	if owner != roles.MigrationRole {
		return fmt.Errorf("schema state owner is not the migration role")
	}

	var runtimeRoleExists bool
	if err := tx.QueryRowContext(ctx, `SELECT EXISTS (SELECT 1 FROM pg_roles WHERE rolname = $1)`, roles.RuntimeRole).Scan(&runtimeRoleExists); err != nil {
		return fmt.Errorf("verify runtime role: database operation failed")
	}
	if !runtimeRoleExists {
		return fmt.Errorf("runtime role is unavailable")
	}

	runtimeIdentifier := pq.QuoteIdentifier(roles.RuntimeRole)
	migrationIdentifier := pq.QuoteIdentifier(roles.MigrationRole)
	var schemaName string
	if err := tx.QueryRowContext(ctx, `SELECT current_schema()::text`).Scan(&schemaName); err != nil {
		return fmt.Errorf("inspect runtime managed schema: database operation failed")
	}
	schemaIdentifier := pq.QuoteIdentifier(schemaName)
	statements := []string{
		`GRANT USAGE ON SCHEMA ` + schemaIdentifier + ` TO ` + runtimeIdentifier,
		`GRANT SELECT, INSERT, UPDATE, DELETE ON ALL TABLES IN SCHEMA ` + schemaIdentifier + ` TO ` + runtimeIdentifier,
		`GRANT USAGE, SELECT ON ALL SEQUENCES IN SCHEMA ` + schemaIdentifier + ` TO ` + runtimeIdentifier,
		`ALTER DEFAULT PRIVILEGES FOR ROLE ` + migrationIdentifier + ` IN SCHEMA ` + schemaIdentifier +
			` GRANT SELECT, INSERT, UPDATE, DELETE ON TABLES TO ` + runtimeIdentifier,
		`ALTER DEFAULT PRIVILEGES FOR ROLE ` + migrationIdentifier + ` IN SCHEMA ` + schemaIdentifier +
			` GRANT USAGE, SELECT ON SEQUENCES TO ` + runtimeIdentifier,
		`REVOKE INSERT, UPDATE, DELETE, TRUNCATE ON TABLE schema_state FROM PUBLIC`,
		`REVOKE INSERT, UPDATE, DELETE, TRUNCATE ON TABLE schema_state FROM ` + runtimeIdentifier,
		`GRANT SELECT ON TABLE schema_state TO ` + runtimeIdentifier,
	}
	for _, statement := range statements {
		if _, err := tx.ExecContext(ctx, statement); err != nil {
			return fmt.Errorf("provision schema state runtime role privileges: database operation failed")
		}
	}
	var canSelect, canInsert, canUpdate, canDelete, canTruncate, bypassRLS bool
	if err := tx.QueryRowContext(ctx, `
		SELECT has_table_privilege($1, 'schema_state', 'SELECT'),
		       has_table_privilege($1, 'schema_state', 'INSERT'),
		       has_table_privilege($1, 'schema_state', 'UPDATE'),
		       has_table_privilege($1, 'schema_state', 'DELETE'),
		       has_table_privilege($1, 'schema_state', 'TRUNCATE'),
		       (SELECT rolbypassrls FROM pg_roles WHERE rolname = $1)
	`, roles.RuntimeRole).Scan(&canSelect, &canInsert, &canUpdate, &canDelete, &canTruncate, &bypassRLS); err != nil {
		return fmt.Errorf("verify runtime role schema state privileges: database operation failed")
	}
	if !canSelect || canInsert || canUpdate || canDelete || canTruncate || bypassRLS {
		return fmt.Errorf("runtime role schema state privileges are not read-only")
	}
	var inaccessibleRelations, inaccessibleSequences int64
	var canUseSchema, canCreateInSchema bool
	if err := tx.QueryRowContext(ctx, `
		SELECT
		    count(*) FILTER (
		        WHERE relation.relkind IN ('r', 'p')
		          AND relation.relname <> 'schema_state'
		          AND NOT (
		              has_table_privilege($1, relation.oid, 'SELECT')
		              AND has_table_privilege($1, relation.oid, 'INSERT')
		              AND has_table_privilege($1, relation.oid, 'UPDATE')
		              AND has_table_privilege($1, relation.oid, 'DELETE')
		          )
		    ),
		    count(*) FILTER (
		        WHERE relation.relkind = 'S'
		          AND NOT (
		              has_sequence_privilege($1, relation.oid, 'USAGE')
		              AND has_sequence_privilege($1, relation.oid, 'SELECT')
		          )
		    ),
		    has_schema_privilege($1, current_schema(), 'USAGE'),
		    has_schema_privilege($1, current_schema(), 'CREATE')
		FROM pg_class relation
		JOIN pg_namespace namespace ON namespace.oid = relation.relnamespace
		WHERE namespace.nspname = current_schema()
		  AND relation.relkind IN ('r', 'p', 'S')
	`, roles.RuntimeRole).Scan(
		&inaccessibleRelations, &inaccessibleSequences, &canUseSchema, &canCreateInSchema,
	); err != nil {
		return fmt.Errorf("verify runtime managed schema privileges: database operation failed")
	}
	if inaccessibleRelations != 0 || inaccessibleSequences != 0 || !canUseSchema || canCreateInSchema {
		return fmt.Errorf("runtime managed schema privileges are invalid")
	}
	var unauthorizedWriters int64
	if err := tx.QueryRowContext(ctx, `
		SELECT count(*)
		FROM pg_roles role_record
		WHERE role_record.rolname <> current_user
		  AND NOT role_record.rolsuper
		  AND role_record.rolname !~ '^pg_'
		  AND (
		      has_table_privilege(role_record.oid, 'schema_state', 'INSERT')
		      OR has_table_privilege(role_record.oid, 'schema_state', 'UPDATE')
		      OR has_table_privilege(role_record.oid, 'schema_state', 'DELETE')
		      OR has_table_privilege(role_record.oid, 'schema_state', 'TRUNCATE')
		  )
	`).Scan(&unauthorizedWriters); err != nil {
		return fmt.Errorf("verify effective schema state writer boundary: database operation failed")
	}
	if unauthorizedWriters != 0 {
		return fmt.Errorf("schema state effective writer boundary is not exclusive")
	}
	if err := tx.Commit(); err != nil {
		return fmt.Errorf("commit schema state runtime role privileges: database operation failed")
	}
	return nil
}
