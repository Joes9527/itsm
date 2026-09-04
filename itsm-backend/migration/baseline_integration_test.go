//go:build integration

package migration

import (
	"context"
	"database/sql"
	"fmt"
	"net/url"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/lib/pq"
	"github.com/stretchr/testify/require"
	"go.uber.org/zap"
)

const baselineIntegrationTimeout = 4 * time.Minute

func TestPostgresFreshBaselineIsReentrantAndCreatesNoMigrationHistory(t *testing.T) {
	db := openDisposableBaselineDatabase(t)
	ctx, cancel := context.WithTimeout(context.Background(), baselineIntegrationTimeout)
	defer cancel()

	runFreshBaselineForIntegration(t, ctx, db)
	runFreshBaselineForIntegration(t, ctx, db)

	var historyRows int64
	require.NoError(t, db.QueryRowContext(ctx, `SELECT COUNT(*) FROM schema_migrations`).Scan(&historyRows))
	require.Zero(t, historyRows, "fresh baseline must not forge published upgrade history")
	state, err := ReadSchemaState(ctx, db)
	require.NoError(t, err)
	require.NoError(t, VerifySchemaState(state, CurrentRelease()))

	_, err = db.ExecContext(ctx, `
		DROP INDEX idx_process_instances_running_unique;
		CREATE UNIQUE INDEX idx_process_instances_running_unique
		ON process_instances (tenant_id, business_key);
	`)
	require.NoError(t, err)
	err = ApplyCurrentBaseline(ctx, mustAcquireIntegrationConnection(t, ctx, db))
	require.ErrorContains(t, err, "current baseline")
}

func TestPostgresUpgradeInterruptionRestartsFromCommittedVerifiedHistory(t *testing.T) {
	db := openDisposableBaselineDatabase(t)
	ctx, cancel := context.WithTimeout(context.Background(), baselineIntegrationTimeout)
	defer cancel()

	runFreshBaselineForIntegration(t, ctx, db)
	_, err := db.ExecContext(ctx, `DELETE FROM schema_state`)
	require.NoError(t, err)
	insertPublishedMigrationPrefix(t, ctx, db, RegisteredMigrations[:len(RegisteredMigrations)-1])

	locker, err := NewPostgresAdvisoryLock(db)
	require.NoError(t, err)
	logger := zap.NewNop().Sugar()
	injected := fmt.Errorf("injected interruption after committed forward migration")
	first := true
	upgrade := UpgradeBootstrap{
		Lock: locker,
		PlanForwardMigrations: func(ctx context.Context, conn BootstrapConnection) ([]Migration, error) {
			return NewMigratorOnConnection(conn, logger).GetPendingMigrations(ctx, PostSchemaMigrations())
		},
		ApplyForwardMigrations: func(ctx context.Context, conn BootstrapConnection, pending []Migration) error {
			migrator := NewMigratorOnConnection(conn, logger)
			for _, item := range pending {
				if err := migrator.ApplyMigration(ctx, item); err != nil {
					return err
				}
			}
			if first {
				first = false
				return injected
			}
			return nil
		},
		VerifySchema: VerifyCurrentSchema,
		ApplyPrivileges: func(context.Context, BootstrapConnection, SchemaStateRoles) error {
			return nil
		},
		PromoteState: PromoteSchemaState,
		Release:      CurrentRelease(),
		Roles:        SchemaStateRoles{MigrationRole: "migration", RuntimeRole: "runtime"},
	}

	err = RunUpgrade(ctx, upgrade)
	require.ErrorIs(t, err, injected)
	var stateRows int64
	require.NoError(t, db.QueryRowContext(ctx, `SELECT COUNT(*) FROM schema_state`).Scan(&stateRows))
	require.Zero(t, stateRows, "interrupted upgrade must remain unpromoted")

	require.NoError(t, RunUpgrade(ctx, upgrade))
	state, err := ReadSchemaState(ctx, db)
	require.NoError(t, err)
	require.NoError(t, VerifySchemaState(state, CurrentRelease()))
	var headRows int64
	require.NoError(t, db.QueryRowContext(ctx,
		`SELECT COUNT(*) FROM schema_migrations WHERE version = $1`,
		RegisteredMigrations[len(RegisteredMigrations)-1].Version,
	).Scan(&headRows))
	require.Equal(t, int64(1), headRows, "committed forward migration must not be replayed")
}

func runFreshBaselineForIntegration(t *testing.T, ctx context.Context, db *sql.DB) {
	t.Helper()
	locker, err := NewPostgresAdvisoryLock(db)
	require.NoError(t, err)
	err = RunFreshBootstrap(ctx, FreshBootstrap{
		Lock:    locker,
		Prepare: PrepareCurrentInfrastructure,
		CreateSchema: func(ctx context.Context, conn BootstrapConnection) error {
			client, err := NewEntClientOnConnection(conn)
			if err != nil {
				return err
			}
			defer client.Close()
			return client.Schema.Create(ctx)
		},
		ApplyBaseline: ApplyCurrentBaseline,
		VerifySchema:  VerifyCurrentSchema,
		ApplyPrivileges: func(context.Context, BootstrapConnection, SchemaStateRoles) error {
			return nil
		},
		PromoteState: PromoteSchemaState,
		Seed: func(context.Context, BootstrapConnection) error {
			return nil
		},
		Release: CurrentRelease(),
		Roles:   SchemaStateRoles{MigrationRole: "migration", RuntimeRole: "runtime"},
	})
	require.NoError(t, err)
}

func insertPublishedMigrationPrefix(t *testing.T, ctx context.Context, db *sql.DB, migrations []Migration) {
	t.Helper()
	for _, item := range migrations {
		lineage, ok := PublishedLineage(item.Version)
		require.True(t, ok)
		_, err := db.ExecContext(ctx, `
			INSERT INTO schema_migrations
				(version, description, checksum, execution_ms, release_version)
			VALUES ($1, $2, $3, 0, 'integration-fixture')
		`, item.Version, item.Description, lineage.SQLSHA256)
		require.NoError(t, err)
	}
}

func mustAcquireIntegrationConnection(t *testing.T, ctx context.Context, db *sql.DB) *sql.Conn {
	t.Helper()
	conn, err := db.Conn(ctx)
	require.NoError(t, err)
	t.Cleanup(func() { require.NoError(t, conn.Close()) })
	return conn
}

func openDisposableBaselineDatabase(t *testing.T) *sql.DB {
	t.Helper()
	adminDSN := strings.TrimSpace(os.Getenv("ITSM_MIGRATION_BASELINE_TEST_DSN"))
	if adminDSN == "" {
		t.Skip("ITSM_MIGRATION_BASELINE_TEST_DSN is not configured for an explicitly disposable PostgreSQL cluster")
	}
	ctx, cancel := context.WithTimeout(context.Background(), baselineIntegrationTimeout)
	defer cancel()
	adminDB, err := sql.Open("postgres", adminDSN)
	require.NoError(t, err)
	require.NoError(t, adminDB.PingContext(ctx))
	databaseName := "migration_baseline_" + strings.ReplaceAll(uuid.NewString(), "-", "")
	_, err = adminDB.ExecContext(ctx, `CREATE DATABASE `+pq.QuoteIdentifier(databaseName))
	require.NoError(t, err)

	t.Cleanup(func() {
		cleanupCtx, cleanupCancel := context.WithTimeout(context.Background(), baselineIntegrationTimeout)
		defer cleanupCancel()
		_, terminateErr := adminDB.ExecContext(cleanupCtx,
			`SELECT pg_terminate_backend(pid) FROM pg_stat_activity WHERE datname = $1`, databaseName)
		require.NoError(t, terminateErr)
		_, dropErr := adminDB.ExecContext(cleanupCtx, `DROP DATABASE IF EXISTS `+pq.QuoteIdentifier(databaseName))
		require.NoError(t, dropErr)
		require.NoError(t, adminDB.Close())
	})

	targetDB, err := sql.Open("postgres", baselineDatabaseDSN(t, adminDSN, databaseName))
	require.NoError(t, err)
	targetDB.SetMaxOpenConns(4)
	require.NoError(t, targetDB.PingContext(ctx))
	t.Cleanup(func() { require.NoError(t, targetDB.Close()) })
	return targetDB
}

func baselineDatabaseDSN(t *testing.T, base, databaseName string) string {
	t.Helper()
	if strings.HasPrefix(base, "postgres://") || strings.HasPrefix(base, "postgresql://") {
		parsed, err := url.Parse(base)
		require.NoError(t, err)
		parsed.Path = "/" + databaseName
		return parsed.String()
	}
	return strings.TrimSpace(base) + " dbname=" + databaseName
}
