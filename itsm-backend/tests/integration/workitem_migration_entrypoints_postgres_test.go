//go:build integration_postgres

package integration

import (
	"context"
	"crypto/ed25519"
	"crypto/sha256"
	"database/sql"
	"encoding/json"
	"fmt"
	"itsm-backend/config"
	"itsm-backend/database"
	appbootstrap "itsm-backend/internal/bootstrap"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/lib/pq"
	"github.com/stretchr/testify/require"
	"go.uber.org/zap"
	"itsm-backend/migration"
)

func migrationEntryFixture(t *testing.T) (*sql.DB, context.Context) {
	t.Helper()
	parsed := migrationEntryTarget(t)
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
	db, ctx := preparationFixture(t)
	m := migration.NewMigrator(db, zap.NewNop().Sugar())
	var last migration.Migration
	for _, candidate := range migration.RegisteredMigrations {
		if candidate.Version == "021_add_callback_optional_declared" {
			last = candidate
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
			cmd.Env = []string{"DB_HOST=" + parsed.Hostname(), "DB_PORT=" + parsed.Port(), "DB_USER=" + parsed.User.Username(), "DB_PASSWORD=" + password, "DB_NAME=" + strings.TrimPrefix(parsed.Path, "/"), "DB_SCHEMA=" + schema, "RLS_MODE=enforce"}
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
	port, err := strconv.Atoi(parsed.Port())
	require.NoError(t, err)
	cfg := &config.Config{Database: config.DatabaseConfig{Host: parsed.Hostname(), Port: port, User: parsed.User.Username(), Password: password, DBName: strings.TrimPrefix(parsed.Path, "/"), SSLMode: "disable", Schema: schema}, Deployment: config.DeploymentConfig{AutoMigrate: false, AutoSeed: true}}
	client, err := database.InitDatabase(&cfg.Database)
	require.NoError(t, err)
	defer client.Close()
	before := entryDigest(t, db)
	err = appbootstrap.InitializeStorage(cfg, client, zap.NewNop().Sugar())
	require.ErrorContains(t, err, "runtime migration admission")
	require.ErrorContains(t, err, "explicit inspection identity")
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

// The contender must attempt the database lock before the owner requests its
// callback connection. This forces the previous two-connection deadlock cycle.
func TestControlledEntryTwoOwnersDoNotExhaustCallbackPool(t *testing.T) {
	fixture, ctx := migrationEntryFixture(t)
	var schema string
	require.NoError(t, fixture.QueryRowContext(ctx, `SELECT current_schema()`).Scan(&schema))
	parsed := migrationEntryTarget(t)
	q := parsed.Query()
	q.Set("search_path", schema)
	q.Set("application_name", schema)
	parsed.RawQuery = q.Encode()
	pool, err := sql.Open("postgres", parsed.String())
	require.NoError(t, err)
	defer pool.Close()
	pool.SetMaxOpenConns(2)
	first := migration.NewMigrator(pool, zap.NewNop().Sugar())
	second := migration.NewMigrator(pool, zap.NewNop().Sugar())
	firstEntered := make(chan struct{})
	allowCallback := make(chan struct{})
	firstDone := make(chan error, 1)
	secondDone := make(chan error, 1)
	runCtx, cancel := context.WithCancel(context.Background())
	defer cancel()
	go func() {
		firstDone <- first.WithMigrationLock(runCtx, func(ctx context.Context) error {
			close(firstEntered)
			select {
			case <-allowCallback:
			case <-ctx.Done():
				return ctx.Err()
			}
			return first.EnsureMigrationsTable(ctx)
		})
	}()
	<-firstEntered
	go func() {
		secondDone <- second.WithMigrationLock(runCtx, func(ctx context.Context) error { return second.EnsureMigrationsTable(ctx) })
	}()
	// Observe both distinct server sessions having attempted an advisory lock.
	// A try-lock implementation may already be idle here, which is intentional.
	observed := false
	observationDeadline := time.After(3 * time.Second)
	tick := time.NewTicker(5 * time.Millisecond)
	defer tick.Stop()
observe:
	for {
		select {
		case <-tick.C:
			var attempts int
			require.NoError(t, fixture.QueryRowContext(ctx, `SELECT count(*) FROM pg_stat_activity WHERE application_name=$1 AND query LIKE '%advisory_lock%'`, schema).Scan(&attempts))
			if attempts == 2 {
				observed = true
				break observe
			}
		case <-observationDeadline:
			break observe
		}
	}
	close(allowCallback)
	completed := true
	for _, done := range []chan error{firstDone, secondDone} {
		select {
		case err := <-done:
			if completed {
				require.NoError(t, err)
			}
		case <-time.After(2 * time.Second):
			completed = false
			cancel()
			require.Error(t, <-done)
		}
	}
	require.True(t, observed, "both lock owners must attempt the database lock before owner callback")
	require.True(t, completed, "lock waiters must not retain the only connection the owner callback needs")
	require.NoError(t, first.InspectMigrationTarget(ctx))
}

// The optional V2 fixture is a separate, explicitly owned container. It does
// not relax or change the original cutover fixture's exact endpoint guard.
func migrationEntryTarget(t *testing.T) *url.URL {
	t.Helper()
	dedicated := os.Getenv("WORKITEM_V2_POSTGRES_TEST_DSN")
	dsn := dedicated
	if dsn == "" {
		dsn = os.Getenv("INTAKE_POSTGRES_TEST_DSN")
	}
	parsed, err := url.Parse(dsn)
	require.NoError(t, err)
	if dedicated == "" {
		require.Equal(t, "127.0.0.1:36444", parsed.Host)
		require.Equal(t, "/sslvpn_test", parsed.Path)
		return parsed
	}
	require.Equal(t, "127.0.0.1:36542", parsed.Host)
	require.Equal(t, "/workitem_v2_task2_test", parsed.Path)
	out, err := exec.Command("docker", "inspect", "--format", `{{json .Config.Labels}}
{{json .NetworkSettings.Ports}}
{{.State.Running}}`, "codex-workitem-v2-task2-fix1-pg").Output()
	require.NoError(t, err)
	lines := strings.Split(strings.TrimSpace(string(out)), "\n")
	require.Len(t, lines, 3)
	var labels map[string]string
	require.NoError(t, json.Unmarshal([]byte(lines[0]), &labels))
	require.Equal(t, "task2-fix1", labels["com.itsm.test.owner"])
	require.Equal(t, "true", labels["com.itsm.test.disposable"])
	var ports map[string][]struct {
		HostIP   string `json:"HostIp"`
		HostPort string
	}
	require.NoError(t, json.Unmarshal([]byte(lines[1]), &ports))
	require.Len(t, ports["5432/tcp"], 1)
	require.Equal(t, "127.0.0.1", ports["5432/tcp"][0].HostIP)
	require.Equal(t, "36542", ports["5432/tcp"][0].HostPort)
	require.Equal(t, "true", lines[2])
	return parsed
}

func TestControlledEntryPreparationOperatorBinding(t *testing.T) {
	db, ctx := preparationFixture(t)
	m := migration.NewMigrator(db, zap.NewNop().Sugar(), migration.MigrationControlConfig{DeploymentID: "owned-v2", Operator: "trusted-operator"})
	e := preparationEvidence(t, m, ctx)
	before := entryDigest(t, db)
	require.ErrorContains(t, m.ApplyPreparation(ctx, e), "operator")
	require.Equal(t, before, entryDigest(t, db))
	e.Operator = "trusted-operator"
	require.NoError(t, m.ApplyPreparation(ctx, e))
}

func TestControlledEntryCLIRejectsConflictingControlsBeforeConfig(t *testing.T) {
	root, err := filepath.Abs("../..")
	require.NoError(t, err)
	binary := filepath.Join(t.TempDir(), "migrate")
	cmd := exec.Command("go", "build", "-tags", "migrate", "-o", binary, "./cmd/migrate")
	cmd.Dir = root
	out, err := cmd.CombinedOutput()
	require.NoError(t, err, string(out))
	for _, other := range []string{"-up", "-down", "-reset", "-fresh", "-seed", "-seed-only", "-retire-workitem"} {
		t.Run(other, func(t *testing.T) {
			cmd := exec.Command(binary, "-prepare-workitem", other)
			cmd.Dir = t.TempDir()
			cmd.Env = []string{}
			out, err := cmd.CombinedOutput()
			require.Error(t, err)
			exit, ok := err.(*exec.ExitError)
			require.True(t, ok)
			require.Equal(t, 2, exit.ExitCode())
			require.Contains(t, string(out), "mutually exclusive")
		})
	}
}

// Synthetic ledger profiles exercise read-only admission; actual migration
// execution is separately covered by preparationFixture and the public P/up path.
func TestControlledEntryHistoricalDeletionTargetsBlockAdmission(t *testing.T) {
	cases := []struct{ version, ddl, object string }{
		{"012", "CREATE TABLE service_catalog_items(id bigint)", "service_catalog_items"},
		{"012", "CREATE TABLE service_catalogs(form_schema jsonb)", "service_catalogs.form_schema"},
		{"013", "CREATE TABLE service_requests(title text)", "service_requests.title"},
		{"013", "CREATE TABLE field_values(entity_type text); INSERT INTO field_values VALUES('service_request')", "field_values"},
		{"014", "CREATE TABLE approval_records(id bigint)", "approval_records"},
		{"017", "CREATE TABLE ticket_types(approval_chain jsonb)", "ticket_types.approval_chain"},
		{"028", "CREATE TABLE service_requests(tenant_id bigint)", "service_requests.tenant_id"},
		{"029", "CREATE TABLE service_catalogs(itsm_type text)", "service_catalogs.itsm_type"},
	}
	for _, tc := range cases {
		t.Run(tc.version+"/"+tc.object, func(t *testing.T) {
			db, ctx := migrationEntryFixture(t)
			seedEntryLedger(t, db, ctx, false)
			_, err := db.ExecContext(ctx, "DELETE FROM schema_migrations WHERE version >= $1", tc.version)
			require.NoError(t, err)
			_, err = db.ExecContext(ctx, tc.ddl)
			require.NoError(t, err)
			before := entryDigest(t, db)
			m := migration.NewMigrator(db, zap.NewNop().Sugar())
			require.ErrorContains(t, m.InspectMigrationTarget(ctx), tc.object)
			require.Equal(t, before, entryDigest(t, db))
		})
	}
}

func TestControlledEntryReadOnlyInspectorNeedsNoBusinessAccess(t *testing.T) {
	roleDB, ctx := migrationEntryFixture(t)
	role := fmt.Sprintf("v2_inspector_%d", time.Now().UnixNano())
	businessRole := role + "_business"
	_, roleErr := roleDB.ExecContext(ctx, "CREATE ROLE "+pq.QuoteIdentifier(businessRole)+" LOGIN PASSWORD 'owned-test-only'")
	require.NoError(t, roleErr)
	t.Cleanup(func() {
		_, err := roleDB.Exec("DROP ROLE " + pq.QuoteIdentifier(businessRole))
		require.NoError(t, err)
	})

	_, err := roleDB.ExecContext(ctx, "CREATE ROLE "+pq.QuoteIdentifier(role)+" LOGIN PASSWORD 'owned-test-only'")
	require.NoError(t, err)
	t.Cleanup(func() { _, err := roleDB.Exec("DROP ROLE " + pq.QuoteIdentifier(role)); require.NoError(t, err) })
	file := filepath.Join(t.TempDir(), "control.json")
	content, _ := json.Marshal(map[string]any{"DeploymentID": "owned-v2", "InspectionRole": role})
	require.NoError(t, os.WriteFile(file, content, 0600))
	t.Setenv("ITSM_MIGRATION_CONTROL_FILE", file)
	control, err := migration.LoadControlConfiguration()
	require.NoError(t, err)
	db, ctx, _ := currentRuntimeFixture(t, control)
	var schema string
	require.NoError(t, db.QueryRow("SELECT current_schema()").Scan(&schema))
	_, err = db.Exec("GRANT USAGE ON SCHEMA " + pq.QuoteIdentifier(schema) + " TO " + pq.QuoteIdentifier(role) + "; GRANT SELECT ON schema_migrations TO " + pq.QuoteIdentifier(role))
	require.NoError(t, err)
	target := migrationEntryTarget(t)
	target.User = url.UserPassword(role, "owned-test-only")
	query := target.Query()
	query.Set("search_path", schema)
	query.Set("default_transaction_read_only", "on")
	target.RawQuery = query.Encode()
	_, err = db.Exec("GRANT USAGE ON SCHEMA " + pq.QuoteIdentifier(schema) + " TO " + pq.QuoteIdentifier(businessRole))
	require.NoError(t, err)
	businessTarget := *target
	businessTarget.User = url.UserPassword(businessRole, "owned-test-only")
	businessDB, err := sql.Open("postgres", businessTarget.String())
	require.NoError(t, err)
	defer businessDB.Close()
	businessDB.SetMaxOpenConns(1)
	var businessPID int
	require.NoError(t, businessDB.QueryRow("SELECT pg_backend_pid()").Scan(&businessPID))
	assertProofReleased := func() {
		var count, currentPID int
		require.NoError(t, businessDB.QueryRow("SELECT pg_backend_pid()").Scan(&currentPID))
		require.NoError(t, db.QueryRow("SELECT count(*) FROM pg_locks WHERE pid IN ($1,$2) AND locktype='advisory'", businessPID, currentPID).Scan(&count))
		require.Zero(t, count, "no proof locks may survive success/failure/cancellation")
	}
	var businessEvidenceAccess, businessTableAccess bool
	require.NoError(t, businessDB.QueryRow("SELECT has_table_privilege(current_user,'work_item_migration_evidence','SELECT'),has_table_privilege(current_user,'tickets','SELECT')").Scan(&businessEvidenceAccess, &businessTableAccess))
	require.False(t, businessEvidenceAccess)
	require.False(t, businessTableAccess)

	inspector, err := sql.Open("postgres", target.String())
	require.NoError(t, err)
	defer inspector.Close()
	var businessAccess bool
	require.NoError(t, inspector.QueryRow("SELECT has_table_privilege(current_user,'tickets','SELECT')").Scan(&businessAccess))
	require.False(t, businessAccess)
	m := migration.NewMigrator(inspector, zap.NewNop().Sugar(), control)
	require.NoError(t, m.InspectRuntimeMigrations(ctx))
	t.Setenv("ITSM_MIGRATION_INSPECTION_DSN", target.String())
	require.NoError(t, migration.InspectRuntimeDatabase(ctx, businessDB, control))
	assertProofReleased()
	// Hold the ledger so admission waits after its live instance proof. Cancel
	// only after both business transaction locks are visible on real PostgreSQL.
	blocker, err := db.BeginTx(ctx, nil)
	require.NoError(t, err)
	defer blocker.Rollback()
	_, err = blocker.Exec("LOCK TABLE schema_migrations IN ACCESS EXCLUSIVE MODE")
	require.NoError(t, err)
	cancelCtx, cancel := context.WithCancel(ctx)
	defer cancel()
	result := make(chan error, 1)
	go func() { result <- migration.InspectRuntimeDatabase(cancelCtx, businessDB, control) }()
	require.Eventually(t, func() bool {
		var count int
		err := db.QueryRow("SELECT count(*) FROM pg_locks WHERE pid=$1 AND locktype='advisory'", businessPID).Scan(&count)
		return err == nil && count == 2
	}, 3*time.Second, 10*time.Millisecond)
	cancel()
	select {
	case err = <-result:
		require.Error(t, err)
	case <-time.After(3 * time.Second):
		t.Fatal("cancelled admission did not return")
	}
	require.NoError(t, blocker.Rollback())
	assertProofReleased()
	require.NoError(t, migration.InspectRuntimeDatabase(ctx, businessDB, control))
	assertProofReleased()

	wrong := *target
	wq := wrong.Query()
	wq.Set("search_path", "public")
	wrong.RawQuery = wq.Encode()
	t.Setenv("ITSM_MIGRATION_INSPECTION_DSN", wrong.String())
	require.ErrorContains(t, migration.InspectRuntimeDatabase(ctx, businessDB, control), "does not match")
	assertProofReleased()
	t.Setenv("ITSM_MIGRATION_INSPECTION_DSN", target.String())
	_, err = db.Exec("GRANT UPDATE ON work_item_migration_evidence TO " + pq.QuoteIdentifier(role))
	require.NoError(t, err)
	require.Error(t, m.InspectRuntimeMigrations(ctx))
	_, err = db.Exec("REVOKE UPDATE ON work_item_migration_evidence FROM " + pq.QuoteIdentifier(role))
	require.NoError(t, err)
	_, err = db.Exec("GRANT SELECT(id) ON tickets TO " + pq.QuoteIdentifier(role))
	require.NoError(t, err)
	require.ErrorContains(t, m.InspectRuntimeMigrations(ctx), "inspection role")
	_, err = db.Exec("REVOKE SELECT(id) ON tickets FROM " + pq.QuoteIdentifier(role))
	require.NoError(t, err)
	require.NoError(t, m.InspectRuntimeMigrations(ctx))
	_, err = db.Exec("GRANT UPDATE(description) ON schema_migrations TO " + pq.QuoteIdentifier(role))
	require.NoError(t, err)
	require.ErrorContains(t, m.InspectRuntimeMigrations(ctx), "inspection role")
	_, err = db.Exec("REVOKE UPDATE(description) ON schema_migrations FROM " + pq.QuoteIdentifier(role))
	require.NoError(t, err)
	var originalAttachment string
	require.NoError(t, db.QueryRow("SELECT md5(content::text) FROM work_item_migration_evidence WHERE version=$1", migration.WorkItemPrepareVersion).Scan(&originalAttachment))
	_, err = db.Exec("GRANT SELECT(description) ON schema_migrations TO " + pq.QuoteIdentifier(role) + " WITH GRANT OPTION")
	require.NoError(t, err)
	require.ErrorContains(t, migration.InspectRuntimeDatabase(ctx, businessDB, control), "inspection role")
	assertProofReleased()
	require.ErrorContains(t, m.InspectRuntimeMigrations(ctx), "inspection role")
	_, err = db.Exec("REVOKE SELECT(description) ON schema_migrations FROM " + pq.QuoteIdentifier(role))
	require.NoError(t, err)
	require.NoError(t, m.InspectRuntimeMigrations(ctx))
	var unchangedAttachment string
	require.NoError(t, db.QueryRow("SELECT md5(content::text) FROM work_item_migration_evidence WHERE version=$1", migration.WorkItemPrepareVersion).Scan(&unchangedAttachment))
	require.Equal(t, originalAttachment, unchangedAttachment)
	_, err = db.Exec("DROP INDEX incident_work_item_id")
	require.NoError(t, err)
	require.ErrorContains(t, m.InspectRuntimeMigrations(ctx), "structure")
}

func TestControlledEntryExistingBootstrapNeverOverlaysEntBeforeP(t *testing.T) {
	db, ctx := preparationFixture(t)
	m := migration.NewMigrator(db, zap.NewNop().Sugar())
	before := entryDigest(t, db)
	writes := 0
	err := migration.RunCanonicalBootstrap(ctx, migration.CanonicalBootstrap{
		Migrator: m, Prepare: func(context.Context) error { writes++; return nil }, CreateSchema: func(context.Context) error { writes++; return nil },
		Seed: func(context.Context) error { writes++; return nil },
	})
	require.Error(t, err)
	require.Equal(t, before, entryDigest(t, db))
	require.Zero(t, writes, "existing canonical migration targets cannot receive Ent overlay or seed before P")
}

func TestControlledEntryCompiledRetirementAuthorizationAndReplay(t *testing.T) {
	root, err := filepath.Abs("../..")
	require.NoError(t, err)
	binary := filepath.Join(t.TempDir(), "migrate")
	build := exec.Command("go", "build", "-tags", "migrate", "-o", binary, "./cmd/migrate")
	build.Dir = root
	out, err := build.CombinedOutput()
	require.NoError(t, err, string(out))
	folder := t.TempDir()
	controlFile := filepath.Join(folder, "control.json")
	require.NoError(t, os.WriteFile(controlFile, []byte("{\"DeploymentID\":\"owned-v2\"}"), 0600))
	t.Setenv("ITSM_MIGRATION_CONTROL_FILE", controlFile)
	control, err := migration.LoadControlConfiguration()
	require.NoError(t, err)
	db, ctx, m, priv := retirementFixtureConfigured(t, control)
	control.RetirementPublicKeys = map[string]ed25519.PublicKey{"fixture": priv.Public().(ed25519.PublicKey)}
	configBytes, err := json.Marshal(control)
	require.NoError(t, err)
	require.NoError(t, os.WriteFile(controlFile, configBytes, 0600))
	evidence := retirementEvidence(t, m, ctx, priv)
	evidence.Operator = control.Operator
	signRetirement(t, &evidence, priv)
	evidenceFile := filepath.Join(folder, "evidence.json")
	require.NoError(t, os.WriteFile(filepath.Join(folder, "config.yaml"), []byte("database:\n  host: \"${DB_HOST}\"\n  port: \"${DB_PORT}\"\n  user: \"${DB_USER}\"\n  dbname: \"${DB_NAME}\"\n  sslmode: disable\n"), 0600))
	target := migrationEntryTarget(t)
	password, _ := target.User.Password()
	var schema string
	require.NoError(t, db.QueryRow("SELECT current_schema()").Scan(&schema))
	run := func(e migration.MigrationEvidence) (string, error) {
		data, err := json.Marshal(e)
		require.NoError(t, err)
		require.NoError(t, os.WriteFile(evidenceFile, data, 0600))
		cmd := exec.CommandContext(ctx, binary, "-retire-workitem", "-evidence-file", evidenceFile)
		cmd.Dir = folder
		cmd.Env = []string{"DB_HOST=" + target.Hostname(), "DB_PORT=" + target.Port(), "DB_USER=" + target.User.Username(), "DB_PASSWORD=" + password, "DB_NAME=" + strings.TrimPrefix(target.Path, "/"), "DB_SCHEMA=" + schema, "ITSM_MIGRATION_CONTROL_FILE=" + controlFile}
		out, err := cmd.CombinedOutput()
		return strings.ReplaceAll(string(out), password, "[REDACTED]"), err
	}
	invalid := evidence
	invalid.Operator = "different-operational-identity"
	before := entryDigest(t, db)
	diagnostic, err := run(invalid)
	require.Error(t, err, diagnostic)
	exit, ok := err.(*exec.ExitError)
	require.True(t, ok)
	require.Equal(t, 1, exit.ExitCode())
	require.Equal(t, before, entryDigest(t, db))
	diagnostic, err = run(evidence)
	require.NoError(t, err, diagnostic)
	require.Contains(t, diagnostic, "Controlled migration committed")
	diagnostic, err = run(evidence)
	require.NoError(t, err, diagnostic)
	var receipts int
	require.NoError(t, db.QueryRow("SELECT count(*) FROM schema_migrations WHERE version=$1", migration.WorkItemRetireVersion).Scan(&receipts))
	require.Equal(t, 1, receipts)
}
