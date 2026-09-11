//go:build integration_postgres

package integration

import (
	"context"
	"crypto/sha256"
	"database/sql"
	"fmt"
	"itsm-backend/config"
	"itsm-backend/database"
	appbootstrap "itsm-backend/internal/bootstrap"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
	"go.uber.org/zap"
	"itsm-backend/migration"
)

func migrationEntryFixture(t *testing.T) (*sql.DB, context.Context) {
	t.Helper()
	parsed, err := url.Parse(os.Getenv("INTAKE_POSTGRES_TEST_DSN"))
	require.NoError(t, err)
	require.Equal(t, "127.0.0.1:36444", parsed.Host)
	require.Equal(t, "/sslvpn_test", parsed.Path)
	admin, err := sql.Open("postgres", parsed.String())
	require.NoError(t, err)
	t.Cleanup(func() { require.NoError(t, admin.Close()) })
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	t.Cleanup(cancel)
	schema := fmt.Sprintf("v2_entry_%d", time.Now().UnixNano())
	_, err = admin.ExecContext(ctx, "CREATE SCHEMA "+schema)
	require.NoError(t, err)
	t.Cleanup(func() {
		_, err := admin.ExecContext(context.Background(), "DROP SCHEMA "+schema+" CASCADE")
		require.NoError(t, err)
		var remaining int
		require.NoError(t, admin.QueryRow(`SELECT count(*) FROM pg_namespace WHERE nspname=$1`, schema).Scan(&remaining))
		require.Zero(t, remaining)
	})
	q := parsed.Query()
	q.Set("search_path", schema)
	parsed.RawQuery = q.Encode()
	db, err := sql.Open("postgres", parsed.String())
	require.NoError(t, err)
	t.Cleanup(func() { require.NoError(t, db.Close()) })
	return db, ctx
}

func entryDigest(t *testing.T, db *sql.DB) string {
	t.Helper()
	// Entire schema column/index/constraint/function definitions and full ledger rows.
	queries := []string{
		`SELECT coalesce(json_agg(x ORDER BY x::text)::text,'[]') FROM (SELECT table_name,column_name,ordinal_position,column_default,is_nullable,data_type,udt_name,character_maximum_length FROM information_schema.columns WHERE table_schema=current_schema()) x`,
		`SELECT coalesce(json_agg(x ORDER BY x::text)::text,'[]') FROM (SELECT indexname,indexdef FROM pg_indexes WHERE schemaname=current_schema()) x`,
		`SELECT coalesce(json_agg(x ORDER BY x::text)::text,'[]') FROM (SELECT conname,pg_get_constraintdef(oid) AS definition FROM pg_constraint WHERE connamespace=current_schema()::regnamespace) x`,
		`SELECT coalesce(json_agg(x ORDER BY x::text)::text,'[]') FROM (SELECT proname,pg_get_functiondef(oid) AS definition FROM pg_proc WHERE pronamespace=current_schema()::regnamespace) x`,
		`SELECT coalesce(json_agg(x ORDER BY x::text)::text,'[]') FROM (SELECT c.* FROM pg_class c WHERE relnamespace=current_schema()::regnamespace) x`,
		`SELECT coalesce(json_agg(x ORDER BY x::text)::text,'[]') FROM (SELECT t.* FROM pg_trigger t JOIN pg_class c ON c.oid=t.tgrelid WHERE c.relnamespace=current_schema()::regnamespace) x`,
		`SELECT coalesce(json_agg(x ORDER BY x::text)::text,'[]') FROM (SELECT p.* FROM pg_policy p JOIN pg_class c ON c.oid=p.polrelid WHERE c.relnamespace=current_schema()::regnamespace) x`,
		`SELECT coalesce(json_agg(x ORDER BY x::text)::text,'[]') FROM (SELECT p.* FROM pg_sequence p JOIN pg_class c ON c.oid=p.seqrelid WHERE c.relnamespace=current_schema()::regnamespace) x`,
		`SELECT coalesce(json_agg(x ORDER BY x::text)::text,'[]') FROM (SELECT t.* FROM pg_type t WHERE typnamespace=current_schema()::regnamespace) x`,
		`SELECT coalesce(json_agg(x ORDER BY x::text)::text,'[]') FROM schema_migrations x`,
	}
	out := ""
	for _, q := range queries {
		var s string
		require.NoError(t, db.QueryRow(q).Scan(&s))
		out += s
	}
	return fmt.Sprintf("%x", sha256.Sum256([]byte(out)))
}

func seedEntryLedger(t *testing.T, db *sql.DB, ctx context.Context, prepare bool) migration.Migration {
	t.Helper()
	_, err := db.ExecContext(ctx, `CREATE TABLE schema_migrations (version varchar(255) PRIMARY KEY, description text NOT NULL, applied_at timestamp NOT NULL DEFAULT now(), rollback_sql text, checksum varchar(128) NOT NULL DEFAULT '', execution_ms bigint NOT NULL DEFAULT 0, release_version varchar(64) NOT NULL DEFAULT '', catalog_revision text, evidence_digest text); CREATE TABLE process_callback_outboxes (id bigint, optional_declared boolean NOT NULL DEFAULT false)`)
	require.NoError(t, err)
	var last migration.Migration
	for _, m := range migration.RegisteredMigrations {
		sum := fmt.Sprintf("%x", sha256.Sum256([]byte(migration.GetMigrationSQL(m.Version))))
		_, err = db.ExecContext(ctx, `INSERT INTO schema_migrations(version,description,rollback_sql,checksum) VALUES($1,$2,$3,$4)`, m.Version, m.Description, m.RollbackSQL, sum)
		require.NoError(t, err)
		last = m
		if m.Version == "021_add_callback_optional_declared" {
			break
		}
	}
	if prepare {
		_, err = db.ExecContext(ctx, `INSERT INTO schema_migrations(version,description,catalog_revision,evidence_digest) VALUES($1,'prepare',$2,'fixture-proof')`, migration.WorkItemPrepareVersion, migration.ControlledCatalogRevision)
		require.NoError(t, err)
	}
	return last
}

func TestControlledEntryDirectRollbackRejectsPreparationDependency(t *testing.T) {
	db, ctx := migrationEntryFixture(t)
	last := seedEntryLedger(t, db, ctx, true)
	before := entryDigest(t, db)
	err := migration.NewMigrator(db, zap.NewNop().Sugar()).RollbackMigration(ctx, last)
	require.Error(t, err, "P must prevent rollback of 021")
	require.Equal(t, before, entryDigest(t, db))
}

func TestControlledEntryRejectsForgedRollback(t *testing.T) {
	db, ctx := migrationEntryFixture(t)
	last := seedEntryLedger(t, db, ctx, false)
	last.RollbackSQL = "DROP TABLE process_callback_outboxes"
	before := entryDigest(t, db)
	err := migration.NewMigrator(db, zap.NewNop().Sugar()).RollbackMigration(ctx, last)
	require.Error(t, err, "caller SQL must never execute")
	require.Equal(t, before, entryDigest(t, db))
}

func TestControlledEntryResetRejectsBeforeAnyWrite(t *testing.T) {
	db, ctx := migrationEntryFixture(t)
	seedEntryLedger(t, db, ctx, true)
	before := entryDigest(t, db)
	require.Error(t, migration.NewMigrator(db, zap.NewNop().Sugar()).ReverseMigrations(ctx, migration.OpReset, nil))
	require.Equal(t, before, entryDigest(t, db))
}

func TestControlledEntryAdmitsCanonicalReverse(t *testing.T) {
	f := newCutoverFixture(t)
	db, ctx := f.scopedDB, f.ctx
	m := migration.NewMigrator(db, zap.NewNop().Sugar())
	var last migration.Migration
	for _, registered := range migration.RegisteredMigrations {
		require.NoError(t, m.ApplyMigration(ctx, registered))
		last = registered
		if registered.Version == "021_add_callback_optional_declared" {
			break
		}
	}
	require.NoError(t, m.RollbackMigration(ctx, last))
	var count int
	require.NoError(t, db.QueryRow(`SELECT count(*) FROM schema_migrations WHERE version=$1`, last.Version).Scan(&count))
	require.Zero(t, count)
	require.NoError(t, db.QueryRow(`SELECT count(*) FROM information_schema.columns WHERE table_schema=current_schema() AND table_name='process_callback_outboxes' AND column_name='optional_declared'`).Scan(&count))
	require.Zero(t, count)
}

func TestControlledEntryLegacyLayoutRejectedBeforeAlter(t *testing.T) {
	for _, kind := range []string{"unknown_version", "unknown_column_type"} {
		t.Run(kind, func(t *testing.T) {
			db, ctx := migrationEntryFixture(t)
			_, err := db.ExecContext(ctx, `CREATE TABLE schema_migrations(version varchar(255),description text NOT NULL,applied_at timestamp NOT NULL DEFAULT now(),rollback_sql text); INSERT INTO schema_migrations(version,description) VALUES('unknown','invalid')`)
			require.NoError(t, err)
			if kind == "unknown_column_type" {
				_, err = db.ExecContext(ctx, `ALTER TABLE schema_migrations ADD COLUMN checksum integer`)
				require.NoError(t, err)
			}
			before := entryDigest(t, db)
			m := migration.NewMigrator(db, zap.NewNop().Sugar())
			require.Error(t, m.EnsureMigrationsTable(ctx))
			require.Equal(t, before, entryDigest(t, db))
			called := false
			err = migration.RunCanonicalBootstrap(ctx, migration.CanonicalBootstrap{Migrator: m, Prepare: func(context.Context) error { called = true; return nil }, CreateSchema: func(context.Context) error { called = true; return nil }, Seed: func(context.Context) error { called = true; return nil }})
			require.Error(t, err)
			require.False(t, called)
			require.Equal(t, before, entryDigest(t, db))
		})
	}
}

func TestControlledEntryEmptyAndMissingLedger(t *testing.T) {
	db, ctx := migrationEntryFixture(t)
	m := migration.NewMigrator(db, zap.NewNop().Sugar())
	require.NoError(t, m.InspectMigrationTarget(ctx))
	var count int
	require.NoError(t, db.QueryRow(`SELECT count(*) FROM pg_class WHERE relnamespace=current_schema()::regnamespace`).Scan(&count))
	require.Zero(t, count)
	require.ErrorContains(t, m.InspectRuntimeMigrations(ctx), "runtime requires migration")
	_, err := db.ExecContext(ctx, `CREATE TABLE existing_target(id integer)`)
	require.NoError(t, err)
	require.ErrorContains(t, m.EnsureMigrationsTable(ctx), "missing schema_migrations")
	require.NoError(t, db.QueryRow(`SELECT count(*) FROM pg_class WHERE relnamespace=current_schema()::regnamespace AND relname='schema_migrations'`).Scan(&count))
	require.Zero(t, count)
}

func TestControlledEntryRejectsMissingAndDecoySchema(t *testing.T) {
	db, ctx := migrationEntryFixture(t)
	db.SetMaxOpenConns(1)
	var schema string
	require.NoError(t, db.QueryRow(`SELECT current_schema()`).Scan(&schema))
	for _, search := range []string{"v2_missing_schema", "v2_missing_schema," + schema, schema + ",public"} {
		_, err := db.ExecContext(ctx, `SELECT set_config('search_path',$1,false)`, search)
		require.NoError(t, err)
		require.Error(t, migration.NewMigrator(db, zap.NewNop().Sugar()).InspectMigrationTarget(ctx))
	}
}

func TestControlledEntryLockSerializesAndReleases(t *testing.T) {
	db, ctx := migrationEntryFixture(t)
	m := migration.NewMigrator(db, zap.NewNop().Sugar())
	other := migration.NewMigrator(db, zap.NewNop().Sugar())
	require.NoError(t, m.EnsureMigrationsTable(ctx))
	err := m.WithMigrationLock(ctx, func(ctx context.Context) error {
		require.NoError(t, m.EnsureMigrationsTable(ctx), "same owner must nest without deadlocking")
		timeout, cancel := context.WithTimeout(context.Background(), 150*time.Millisecond)
		defer cancel()
		require.Error(t, other.EnsureMigrationsTable(timeout), "different owner must block on same key")
		return fmt.Errorf("abort callback")
	})
	require.ErrorContains(t, err, "abort callback")
	require.NoError(t, other.EnsureMigrationsTable(ctx), "lock must be released on error")
	func() {
		defer func() { require.Equal(t, "abort", recover()) }()
		_ = m.WithMigrationLock(ctx, func(context.Context) error { panic("abort") })
	}()
	require.NoError(t, other.EnsureMigrationsTable(ctx), "lock must be released on panic")
}

func TestControlledEntryApplyRejectsOutOfOrder(t *testing.T) {
	db, ctx := migrationEntryFixture(t)
	seedEntryLedger(t, db, ctx, false)
	before := entryDigest(t, db)
	for _, mig := range migration.RegisteredMigrations {
		if mig.Version == "023_add_process_start_request_digest" {
			require.Error(t, migration.NewMigrator(db, zap.NewNop().Sugar()).ApplyMigration(ctx, mig))
		}
	}
	require.Equal(t, before, entryDigest(t, db))
}

func TestControlledEntryExpiredLockContextCannotBypassLock(t *testing.T) {
	db, ctx := migrationEntryFixture(t)
	m := migration.NewMigrator(db, zap.NewNop().Sugar())
	other := migration.NewMigrator(db, zap.NewNop().Sugar())
	require.NoError(t, m.EnsureMigrationsTable(ctx))
	var captured context.Context
	require.NoError(t, m.WithMigrationLock(ctx, func(ctx context.Context) error { captured = ctx; return nil }))
	require.NoError(t, other.WithMigrationLock(ctx, func(context.Context) error {
		timeout, cancel := context.WithTimeout(captured, 150*time.Millisecond)
		defer cancel()
		require.Error(t, m.EnsureMigrationsTable(timeout), "expired ownership must reacquire real lock")
		return nil
	}))
}

func TestControlledEntryCLIRejectsBeforeWrites(t *testing.T) {
	root, err := filepath.Abs("../..")
	require.NoError(t, err)
	binary := filepath.Join(t.TempDir(), "migrate")
	build := exec.Command("go", "build", "-tags", "migrate", "-o", binary, "./cmd/migrate")
	build.Dir = root
	output, err := build.CombinedOutput()
	require.NoError(t, err, "build: %s", output)
	for _, flag := range []string{"-up", "-down", "-reset", "-seed", "-seed-only", "-dry-run", "-status"} {
		t.Run(flag, func(t *testing.T) {
			db, ctx := migrationEntryFixture(t)
			seedEntryLedger(t, db, ctx, true)
			var schema string
			require.NoError(t, db.QueryRowContext(ctx, `SELECT current_schema()`).Scan(&schema))
			parsed, err := url.Parse(os.Getenv("INTAKE_POSTGRES_TEST_DSN"))
			require.NoError(t, err)
			password, _ := parsed.User.Password()
			dir := t.TempDir()
			require.NoError(t, os.WriteFile(filepath.Join(dir, "config.yaml"), []byte("database:\n  host: \"${DB_HOST}\"\n  port: \"${DB_PORT}\"\n  user: \"${DB_USER}\"\n  dbname: \"${DB_NAME}\"\n  sslmode: disable\n"), 0600))
			before := entryDigest(t, db)
			cmd := exec.CommandContext(ctx, binary, flag)
			cmd.Dir = dir
			cmd.Env = []string{"DB_HOST=" + parsed.Hostname(), "DB_PORT=" + parsed.Port(), "DB_USER=" + parsed.User.Username(), "DB_PASSWORD=" + password, "DB_NAME=sslvpn_test", "DB_SCHEMA=" + schema, "RLS_MODE=enforce"}
			out, err := cmd.CombinedOutput()
			require.Error(t, err)
			diagnostic := strings.ReplaceAll(string(out), password, "[REDACTED]")
			require.Contains(t, diagnostic, "Migration target admission failed", diagnostic)
			require.Equal(t, before, entryDigest(t, db))
		})
	}
}

func TestControlledEntryAutoMigrateDisabledStillRequiresRuntime(t *testing.T) {
	db, ctx := migrationEntryFixture(t)
	seedEntryLedger(t, db, ctx, false)
	var schema string
	require.NoError(t, db.QueryRowContext(ctx, `SELECT current_schema()`).Scan(&schema))
	parsed, err := url.Parse(os.Getenv("INTAKE_POSTGRES_TEST_DSN"))
	require.NoError(t, err)
	password, _ := parsed.User.Password()
	cfg := &config.Config{Database: config.DatabaseConfig{Host: "127.0.0.1", Port: 36444, User: parsed.User.Username(), Password: password, DBName: "sslvpn_test", SSLMode: "disable", Schema: schema}, Deployment: config.DeploymentConfig{AutoMigrate: false, AutoSeed: true}}
	client, err := database.InitDatabase(&cfg.Database)
	require.NoError(t, err)
	defer client.Close()
	before := entryDigest(t, db)
	err = appbootstrap.InitializeStorage(cfg, client, zap.NewNop().Sugar())
	require.ErrorContains(t, err, "runtime migration admission")
	require.ErrorContains(t, err, migration.WorkItemPrepareVersion)
	require.Equal(t, before, entryDigest(t, db))
}

func TestControlledEntryFreshRequiresEmptyTarget(t *testing.T) {
	db, ctx := migrationEntryFixture(t)
	m := migration.NewMigrator(db, zap.NewNop().Sugar())
	require.NoError(t, m.InspectEmptyMigrationTarget(ctx))
	seedEntryLedger(t, db, ctx, false)
	before := entryDigest(t, db)
	require.ErrorContains(t, m.InspectEmptyMigrationTarget(ctx), "fresh requires an empty")
	require.Equal(t, before, entryDigest(t, db))
}

func TestControlledEntryRejectsInsufficientLockPool(t *testing.T) {
	db, _ := migrationEntryFixture(t)
	db.SetMaxOpenConns(1)
	ctx, cancel := context.WithTimeout(context.Background(), 200*time.Millisecond)
	defer cancel()
	require.ErrorContains(t, migration.NewMigrator(db, zap.NewNop().Sugar()).EnsureMigrationsTable(ctx), "requires at least two")
}
