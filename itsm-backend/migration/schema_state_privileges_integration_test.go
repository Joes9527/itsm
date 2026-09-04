//go:build integration

package migration

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"net/url"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/lib/pq"
	"github.com/stretchr/testify/require"
)

const schemaStatePrivilegesIntegrationTimeout = 2 * time.Minute

func TestPostgresSchemaStatePrivileges(t *testing.T) {
	adminDSN := strings.TrimSpace(os.Getenv("ITSM_SCHEMA_STATE_PRIVILEGES_TEST_DSN"))
	if adminDSN == "" {
		t.Skip("ITSM_SCHEMA_STATE_PRIVILEGES_TEST_DSN is not configured for a disposable PostgreSQL cluster")
	}

	ctx, cancel := context.WithTimeout(context.Background(), schemaStatePrivilegesIntegrationTimeout)
	defer cancel()
	adminDB, err := sql.Open("postgres", adminDSN)
	require.NoError(t, err)
	registerSchemaStateCleanup(t, "admin database connection", func(context.Context) error {
		return adminDB.Close()
	})
	require.NoError(t, adminDB.PingContext(ctx))

	suffix := strings.ReplaceAll(uuid.NewString(), "-", "")
	migrationRole := "schema_migration_" + suffix
	runtimeRole := "schema_runtime_" + suffix
	inheritedWriterRole := "schema_writer_" + suffix
	databaseName := "schema_privileges_" + suffix
	migrationPassword := "migration_" + suffix
	runtimePassword := "runtime_" + suffix

	_, err = adminDB.ExecContext(ctx, fmt.Sprintf(
		"CREATE ROLE %s LOGIN PASSWORD %s",
		pq.QuoteIdentifier(migrationRole),
		pq.QuoteLiteral(migrationPassword),
	))
	require.NoError(t, err)
	registerSchemaStateCleanup(t, "migration role", func(cleanupCtx context.Context) error {
		_, err := adminDB.ExecContext(cleanupCtx, fmt.Sprintf(
			"DROP ROLE IF EXISTS %s",
			pq.QuoteIdentifier(migrationRole),
		))
		return err
	})
	_, err = adminDB.ExecContext(ctx, fmt.Sprintf(
		"CREATE ROLE %s",
		pq.QuoteIdentifier(inheritedWriterRole),
	))
	require.NoError(t, err)
	registerSchemaStateCleanup(t, "inherited writer role", func(cleanupCtx context.Context) error {
		_, err := adminDB.ExecContext(cleanupCtx, fmt.Sprintf(
			"DROP ROLE IF EXISTS %s",
			pq.QuoteIdentifier(inheritedWriterRole),
		))
		return err
	})
	_, err = adminDB.ExecContext(ctx, fmt.Sprintf(
		"CREATE ROLE %s LOGIN PASSWORD %s",
		pq.QuoteIdentifier(runtimeRole),
		pq.QuoteLiteral(runtimePassword),
	))
	require.NoError(t, err)
	registerSchemaStateCleanup(t, "runtime role", func(cleanupCtx context.Context) error {
		_, err := adminDB.ExecContext(cleanupCtx, fmt.Sprintf(
			"DROP ROLE IF EXISTS %s",
			pq.QuoteIdentifier(runtimeRole),
		))
		return err
	})
	_, err = adminDB.ExecContext(ctx, fmt.Sprintf(
		"CREATE DATABASE %s OWNER %s",
		pq.QuoteIdentifier(databaseName),
		pq.QuoteIdentifier(migrationRole),
	))
	require.NoError(t, err)
	registerSchemaStateCleanup(t, "test database", func(cleanupCtx context.Context) error {
		_, err := adminDB.ExecContext(cleanupCtx, fmt.Sprintf(
			"DROP DATABASE IF EXISTS %s",
			pq.QuoteIdentifier(databaseName),
		))
		return err
	})
	registerSchemaStateCleanup(t, "test database connections", func(cleanupCtx context.Context) error {
		_, err := adminDB.ExecContext(
			cleanupCtx,
			`SELECT pg_terminate_backend(pid) FROM pg_stat_activity WHERE datname = $1`,
			databaseName,
		)
		return err
	})

	migrationDB, err := sql.Open("postgres", schemaStateRoleDSN(t, adminDSN, databaseName, migrationRole, migrationPassword))
	require.NoError(t, err)
	registerSchemaStateCleanup(t, "migration database connection", func(context.Context) error {
		return migrationDB.Close()
	})
	migrationDB.SetMaxOpenConns(1)
	require.NoError(t, migrationDB.PingContext(ctx))
	adminTargetDB, err := sql.Open("postgres", schemaStateDatabaseDSN(t, adminDSN, databaseName))
	require.NoError(t, err)
	registerSchemaStateCleanup(t, "target admin database connection", func(context.Context) error {
		return adminTargetDB.Close()
	})
	adminTargetDB.SetMaxOpenConns(1)
	require.NoError(t, adminTargetDB.PingContext(ctx))

	_, err = migrationDB.ExecContext(ctx, GetMigrationSQL("028_schema_release_state"))
	require.NoError(t, err)
	_, err = migrationDB.ExecContext(ctx, `CREATE TABLE schema_state_child () INHERITS (schema_state)`)
	require.NoError(t, err)
	require.ErrorContains(t, VerifySchemaStateStorage(ctx, migrationDB), "standalone relation")
	_, err = migrationDB.ExecContext(ctx, `DROP TABLE schema_state_child; DROP TABLE schema_state`)
	require.NoError(t, err)

	_, err = migrationDB.ExecContext(ctx, `
		CREATE TABLE schema_state_parent (
			id SMALLINT NOT NULL CHECK (id = 1),
			release_id VARCHAR(128) NOT NULL,
			schema_version VARCHAR(255) NOT NULL,
			baseline_version VARCHAR(64) NOT NULL,
			release_manifest_checksum CHAR(64) NOT NULL,
			updated_at TIMESTAMPTZ NOT NULL DEFAULT CURRENT_TIMESTAMP
		);
		CREATE TABLE schema_state () INHERITS (schema_state_parent);
		ALTER TABLE schema_state ADD PRIMARY KEY (id);
	`)
	require.NoError(t, err)
	require.ErrorContains(t, VerifySchemaStateStorage(ctx, migrationDB), "standalone relation")
	_, err = migrationDB.ExecContext(ctx, `DROP TABLE schema_state; DROP TABLE schema_state_parent`)
	require.NoError(t, err)

	_, err = migrationDB.ExecContext(ctx, `
		CREATE TABLE schema_state (
			id SMALLINT PRIMARY KEY,
			release_id VARCHAR(128) NOT NULL,
			schema_version VARCHAR(255) NOT NULL,
			baseline_version VARCHAR(64) NOT NULL,
			release_manifest_checksum CHAR(64) NOT NULL,
			updated_at TIMESTAMPTZ NOT NULL DEFAULT CURRENT_TIMESTAMP
		);
		INSERT INTO schema_state
			(id, release_id, schema_version, baseline_version, release_manifest_checksum)
		VALUES (2, 'invalid', 'invalid', 'invalid', '`+strings.Repeat("0", 64)+`');
	`)
	require.NoError(t, err)
	_, err = migrationDB.ExecContext(ctx, GetMigrationSQL("028_schema_release_state"))
	require.NoError(t, err)
	require.ErrorContains(t, VerifySchemaStateStorage(ctx, migrationDB), "singleton check")
	_, err = migrationDB.ExecContext(ctx, `DROP TABLE schema_state`)
	require.NoError(t, err)
	_, err = migrationDB.ExecContext(ctx, GetMigrationSQL("028_schema_release_state"))
	require.NoError(t, err)
	require.NoError(t, VerifySchemaStateStorage(ctx, migrationDB))

	roles := SchemaStateRoles{MigrationRole: migrationRole, RuntimeRole: runtimeRole}
	require.NoError(t, ApplySchemaStatePrivileges(ctx, migrationDB, roles))
	require.NoError(t, PromoteSchemaState(ctx, migrationDB, CurrentRelease()))

	runtimeDB, err := sql.Open("postgres", schemaStateRoleDSN(t, adminDSN, databaseName, runtimeRole, runtimePassword))
	require.NoError(t, err)
	registerSchemaStateCleanup(t, "runtime database connection", func(context.Context) error {
		return runtimeDB.Close()
	})
	runtimeDB.SetMaxOpenConns(1)
	require.NoError(t, runtimeDB.PingContext(ctx))
	var releaseID string
	require.NoError(t, runtimeDB.QueryRowContext(ctx, `SELECT release_id FROM schema_state WHERE id = 1`).Scan(&releaseID))
	require.Equal(t, CurrentRelease().ReleaseID, releaseID)

	for name, statement := range map[string]string{
		"insert": `INSERT INTO schema_state (id, release_id, schema_version, baseline_version, release_manifest_checksum) VALUES (1, 'x', 'x', 'x', '` + strings.Repeat("0", 64) + `')`,
		"update": `UPDATE schema_state SET release_id = 'x' WHERE id = 1`,
		"delete": `DELETE FROM schema_state WHERE id = 1`,
	} {
		t.Run("runtime "+name+" denied", func(t *testing.T) {
			_, err := runtimeDB.ExecContext(ctx, statement)
			requirePostgresInsufficientPrivilege(t, err)
		})
	}

	t.Run("invalid role categories fail closed", func(t *testing.T) {
		for _, invalid := range []SchemaStateRoles{
			{RuntimeRole: runtimeRole},
			{MigrationRole: migrationRole},
			{MigrationRole: migrationRole, RuntimeRole: migrationRole},
		} {
			err := ApplySchemaStatePrivileges(ctx, migrationDB, invalid)
			requireSanitizedSchemaStateRoleError(t, err, adminDSN, migrationRole, runtimeRole)
		}
	})

	t.Run("wrong current executor fails closed", func(t *testing.T) {
		err := ApplySchemaStatePrivileges(ctx, runtimeDB, roles)
		requireSanitizedSchemaStateRoleError(t, err, adminDSN, migrationRole, runtimeRole)
		require.ErrorContains(t, err, "migration role")
	})

	t.Run("wrong table owner fails closed", func(t *testing.T) {
		var adminRole string
		require.NoError(t, adminTargetDB.QueryRowContext(ctx, `SELECT current_user`).Scan(&adminRole))
		_, err := adminTargetDB.ExecContext(ctx, fmt.Sprintf(
			"ALTER TABLE public.schema_state OWNER TO %s",
			pq.QuoteIdentifier(adminRole),
		))
		require.NoError(t, err)
		err = ApplySchemaStatePrivileges(ctx, migrationDB, roles)
		requireSanitizedSchemaStateRoleError(t, err, adminDSN, migrationRole, runtimeRole)
		require.ErrorContains(t, err, "migration role")
	})

	t.Run("inherited write privilege fails closed", func(t *testing.T) {
		_, err := adminTargetDB.ExecContext(ctx, fmt.Sprintf(
			"ALTER TABLE public.schema_state OWNER TO %s",
			pq.QuoteIdentifier(migrationRole),
		))
		require.NoError(t, err)
		_, err = migrationDB.ExecContext(ctx, fmt.Sprintf(
			"GRANT UPDATE ON TABLE schema_state TO %s",
			pq.QuoteIdentifier(inheritedWriterRole),
		))
		require.NoError(t, err)
		_, err = adminDB.ExecContext(ctx, fmt.Sprintf(
			"GRANT %s TO %s",
			pq.QuoteIdentifier(inheritedWriterRole),
			pq.QuoteIdentifier(runtimeRole),
		))
		require.NoError(t, err)

		err = ApplySchemaStatePrivileges(ctx, migrationDB, roles)
		requireSanitizedSchemaStateRoleError(t, err, adminDSN, migrationRole, runtimeRole)
		require.ErrorContains(t, err, "runtime role")
	})
}

func registerSchemaStateCleanup(t *testing.T, category string, cleanup func(context.Context) error) {
	t.Helper()
	t.Cleanup(func() {
		cleanupCtx, cleanupCancel := context.WithTimeout(context.Background(), schemaStatePrivilegesIntegrationTimeout)
		defer cleanupCancel()
		if err := cleanup(cleanupCtx); err != nil {
			var pqErr *pq.Error
			if errors.As(err, &pqErr) {
				t.Errorf("%s cleanup failed (SQLSTATE %s)", category, pqErr.Code)
				return
			}
			t.Errorf("%s cleanup failed", category)
		}
	})
}

func schemaStateDatabaseDSN(t *testing.T, base, databaseName string) string {
	t.Helper()
	if strings.HasPrefix(base, "postgres://") || strings.HasPrefix(base, "postgresql://") {
		parsed, err := url.Parse(base)
		require.NoError(t, err)
		parsed.Path = "/" + databaseName
		return parsed.String()
	}
	return strings.TrimSpace(base) + " dbname=" + databaseName
}

func schemaStateRoleDSN(t *testing.T, base, databaseName, role, password string) string {
	t.Helper()
	if strings.HasPrefix(base, "postgres://") || strings.HasPrefix(base, "postgresql://") {
		parsed, err := url.Parse(base)
		require.NoError(t, err)
		parsed.Path = "/" + databaseName
		parsed.User = url.UserPassword(role, password)
		return parsed.String()
	}
	return strings.TrimSpace(base) + " dbname=" + databaseName + " user=" + role + " password=" + password
}

func requirePostgresInsufficientPrivilege(t *testing.T, err error) {
	t.Helper()
	require.Error(t, err)
	var pqErr *pq.Error
	require.ErrorAs(t, err, &pqErr)
	require.Equal(t, pq.ErrorCode("42501"), pqErr.Code)
}

func requireSanitizedSchemaStateRoleError(t *testing.T, err error, secrets ...string) {
	t.Helper()
	require.Error(t, err)
	for _, secret := range secrets {
		require.NotContains(t, err.Error(), secret)
	}
}
