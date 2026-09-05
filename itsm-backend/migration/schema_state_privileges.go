package migration

import (
	"context"
	"database/sql"
	"fmt"
	"regexp"
	"strings"

	"github.com/lib/pq"
)

const (
	migrationDatabaseUserEnv = "ITSM_MIGRATION_DB_USER"
	runtimeDatabaseUserEnv   = "ITSM_RUNTIME_DB_USER"
	bootstrapDatabaseUserEnv = "ITSM_BOOTSTRAP_DB_USER"
)

var canonicalDatabaseRolePattern = regexp.MustCompile(`^[a-z_][a-z0-9_]{0,62}$`)

// SchemaStateRoles names the stable database security categories used by the
// catalog verifier. BootstrapRole may equal MigrationRole for a local cluster
// whose migration principal is also its sole bootstrap DBA.
type SchemaStateRoles struct {
	MigrationRole string
	RuntimeRole   string
	BootstrapRole string
}

type schemaStateRolesContextKey struct{}

// WithSchemaStateRoles binds validated role categories to catalog verification
// without placing physical role names in immutable release assets.
func WithSchemaStateRoles(ctx context.Context, roles SchemaStateRoles) (context.Context, error) {
	if ctx == nil {
		return nil, fmt.Errorf("schema state role context is required")
	}
	if err := validateSchemaStateRoles(roles); err != nil {
		return nil, err
	}
	return context.WithValue(ctx, schemaStateRolesContextKey{}, roles), nil
}

func schemaStateRolesFromContext(ctx context.Context) (SchemaStateRoles, error) {
	if ctx == nil {
		return SchemaStateRoles{}, fmt.Errorf("schema state role context is required")
	}
	roles, ok := ctx.Value(schemaStateRolesContextKey{}).(SchemaStateRoles)
	if !ok {
		return SchemaStateRoles{}, fmt.Errorf("schema state role context is required")
	}
	if err := validateSchemaStateRoles(roles); err != nil {
		return SchemaStateRoles{}, err
	}
	return roles, nil
}

// LoadSchemaStateRoles reads only the three supported role identifiers.
func LoadSchemaStateRoles(getenv func(string) string) (SchemaStateRoles, error) {
	if getenv == nil {
		return SchemaStateRoles{}, fmt.Errorf("migration, runtime, and bootstrap role configuration is required")
	}
	roles := SchemaStateRoles{
		MigrationRole: strings.TrimSpace(getenv(migrationDatabaseUserEnv)),
		RuntimeRole:   strings.TrimSpace(getenv(runtimeDatabaseUserEnv)),
		BootstrapRole: strings.TrimSpace(getenv(bootstrapDatabaseUserEnv)),
	}
	if err := validateSchemaStateRoles(roles); err != nil {
		return SchemaStateRoles{}, err
	}
	return roles, nil
}

func validateSchemaStateRoles(roles SchemaStateRoles) error {
	if !canonicalDatabaseRolePattern.MatchString(roles.MigrationRole) {
		return fmt.Errorf("migration role identifier is invalid")
	}
	if !canonicalDatabaseRolePattern.MatchString(roles.RuntimeRole) {
		return fmt.Errorf("runtime role identifier is invalid")
	}
	if !canonicalDatabaseRolePattern.MatchString(roles.BootstrapRole) {
		return fmt.Errorf("bootstrap role identifier is invalid")
	}
	if roles.MigrationRole == roles.RuntimeRole {
		return fmt.Errorf("migration role and runtime role must be distinct")
	}
	if roles.BootstrapRole == roles.RuntimeRole {
		return fmt.Errorf("bootstrap role and runtime role must be distinct")
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

	if err := verifySchemaStateAuthority(ctx, tx, roles); err != nil {
		return err
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
	if err := verifySchemaStateAuthority(ctx, tx, roles); err != nil {
		return err
	}
	if err := tx.Commit(); err != nil {
		return fmt.Errorf("commit schema state runtime role privileges: database operation failed")
	}
	return nil
}

func verifySchemaStateAuthority(ctx context.Context, db DBTX, roles SchemaStateRoles) error {
	if err := verifyCatalogRoleBoundary(ctx, db, roles); err != nil {
		return err
	}
	var ownerMatches bool
	if err := db.QueryRowContext(ctx, `
		SELECT relation.relowner = (SELECT oid FROM pg_roles WHERE rolname = $1)
		FROM pg_class relation
		JOIN pg_namespace namespace ON namespace.oid = relation.relnamespace
		WHERE namespace.nspname = current_schema()
		  AND relation.relname = 'schema_state'
		  AND relation.relkind IN ('r', 'p')
	`, roles.MigrationRole).Scan(&ownerMatches); err != nil {
		return fmt.Errorf("verify schema state migration role ownership: database operation failed")
	}
	if !ownerMatches {
		return fmt.Errorf("schema state owner is not the migration role")
	}

	var unauthorizedWriters, publicWriteGrants int64
	if err := db.QueryRowContext(ctx, `
		WITH RECURSIVE role_identity AS (
		    SELECT (SELECT oid FROM pg_roles WHERE rolname = $1) AS migration_oid,
		           (SELECT oid FROM pg_roles WHERE rolname = $2) AS bootstrap_oid
		), schema_state_relation AS (
		    SELECT relation.oid, relation.relowner, relation.relacl
		    FROM pg_class relation
		    JOIN pg_namespace namespace ON namespace.oid = relation.relnamespace
		    WHERE namespace.nspname = current_schema()
		      AND relation.relname = 'schema_state'
		      AND relation.relkind IN ('r', 'p')
		), direct_writer_roles AS (
		    SELECT migration_oid AS oid FROM role_identity
		    UNION
		    SELECT bootstrap_oid FROM role_identity
		    UNION
		    SELECT oid FROM pg_roles WHERE rolname = 'pg_write_all_data'
		    UNION
		    SELECT acl.grantee
		    FROM schema_state_relation relation
		    CROSS JOIN LATERAL aclexplode(COALESCE(relation.relacl, acldefault('r', relation.relowner))) acl
		    WHERE acl.grantee <> 0
		      AND acl.privilege_type IN ('INSERT', 'UPDATE', 'DELETE', 'TRUNCATE')
		    UNION
		    SELECT acl.grantee
		    FROM pg_attribute attribute
		    JOIN schema_state_relation relation ON relation.oid = attribute.attrelid
		    CROSS JOIN LATERAL aclexplode(attribute.attacl) acl
		    WHERE attribute.attnum > 0
		      AND NOT attribute.attisdropped
		      AND acl.grantee <> 0
		      AND acl.privilege_type IN ('INSERT', 'UPDATE')
		), role_reachability AS (
		    SELECT membership.member, membership.roleid AS reachable
		    FROM pg_auth_members membership
		    WHERE membership.inherit_option OR membership.set_option
		    UNION
		    SELECT reachability.member, membership.roleid
		    FROM role_reachability reachability
		    JOIN pg_auth_members membership ON membership.member = reachability.reachable
		    WHERE membership.inherit_option OR membership.set_option
		), unauthorized AS (
		    SELECT DISTINCT role_record.oid
		    FROM pg_roles role_record
		    CROSS JOIN role_identity identity
		    WHERE role_record.oid NOT IN (identity.migration_oid, identity.bootstrap_oid)
		      AND role_record.rolname !~ '^pg_'
		      AND (
		          role_record.rolsuper
		          OR role_record.oid IN (SELECT oid FROM direct_writer_roles)
		          OR EXISTS (
		              SELECT 1
		              FROM role_reachability reachability
		              WHERE reachability.member = role_record.oid
		                AND reachability.reachable IN (SELECT oid FROM direct_writer_roles)
		          )
		      )
		), public_writes AS (
		    SELECT acl.privilege_type
		    FROM schema_state_relation relation
		    CROSS JOIN LATERAL aclexplode(COALESCE(relation.relacl, acldefault('r', relation.relowner))) acl
		    WHERE acl.grantee = 0
		      AND acl.privilege_type IN ('INSERT', 'UPDATE', 'DELETE', 'TRUNCATE')
		    UNION ALL
		    SELECT acl.privilege_type
		    FROM pg_attribute attribute
		    JOIN schema_state_relation relation ON relation.oid = attribute.attrelid
		    CROSS JOIN LATERAL aclexplode(attribute.attacl) acl
		    WHERE attribute.attnum > 0
		      AND NOT attribute.attisdropped
		      AND acl.grantee = 0
		      AND acl.privilege_type IN ('INSERT', 'UPDATE')
		)
		SELECT (SELECT count(*) FROM unauthorized),
		       (SELECT count(*) FROM public_writes)
	`, roles.MigrationRole, roles.BootstrapRole).Scan(&unauthorizedWriters, &publicWriteGrants); err != nil {
		return fmt.Errorf("verify effective schema state writer boundary: database operation failed")
	}
	if unauthorizedWriters != 0 || publicWriteGrants != 0 {
		return fmt.Errorf("schema state effective writer boundary is not exclusive")
	}
	return nil
}
