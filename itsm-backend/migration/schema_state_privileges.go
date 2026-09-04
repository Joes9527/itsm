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
	statements := []string{
		`REVOKE INSERT, UPDATE, DELETE ON TABLE schema_state FROM PUBLIC`,
		`REVOKE INSERT, UPDATE, DELETE ON TABLE schema_state FROM ` + runtimeIdentifier,
		`GRANT SELECT ON TABLE schema_state TO ` + runtimeIdentifier,
	}
	for _, statement := range statements {
		if _, err := tx.ExecContext(ctx, statement); err != nil {
			return fmt.Errorf("provision schema state runtime role privileges: database operation failed")
		}
	}
	var canSelect, canInsert, canUpdate, canDelete bool
	if err := tx.QueryRowContext(ctx, `
		SELECT has_table_privilege($1, 'schema_state', 'SELECT'),
		       has_table_privilege($1, 'schema_state', 'INSERT'),
		       has_table_privilege($1, 'schema_state', 'UPDATE'),
		       has_table_privilege($1, 'schema_state', 'DELETE')
	`, roles.RuntimeRole).Scan(&canSelect, &canInsert, &canUpdate, &canDelete); err != nil {
		return fmt.Errorf("verify runtime role schema state privileges: database operation failed")
	}
	if !canSelect || canInsert || canUpdate || canDelete {
		return fmt.Errorf("runtime role schema state privileges are not read-only")
	}
	if err := tx.Commit(); err != nil {
		return fmt.Errorf("commit schema state runtime role privileges: database operation failed")
	}
	return nil
}
