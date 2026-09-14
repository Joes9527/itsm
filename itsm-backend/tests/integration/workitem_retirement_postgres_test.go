//go:build integration_postgres

package integration

import (
	"bytes"
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
	"go.uber.org/zap"

	"itsm-backend/migration"
)

func TestWorkItemRetirementAutomaticMigrationPreservesLegacyEvidence(t *testing.T) {
	for _, tc := range []struct{ name, version, setup, preserved string }{
		{
			"legacy workflow rows", "022_drop_professional_extension_shared_fields",
			"CREATE TABLE workflows(id bigint PRIMARY KEY, content text); INSERT INTO workflows VALUES(1, 'preserve historical evidence')",
			"SELECT content FROM workflows WHERE id=1",
		},
		{
			"shared professional field", "022_drop_professional_extension_shared_fields",
			"ALTER TABLE changes ADD COLUMN title text; UPDATE changes SET title='preserve historical evidence'",
			"SELECT title FROM changes",
		},
		{
			"identity field", "027_work_item_identity_field_retirement",
			"ALTER TABLE tickets ADD COLUMN type text; UPDATE tickets SET type='change'",
			"SELECT type FROM tickets",
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			f := newCutoverFixture(t)
			f.canonicalChange(t, "CHG-RETIREMENT")
			_, err := f.scopedDB.ExecContext(f.ctx, tc.setup)
			require.NoError(t, err)
			runner := migration.NewMigrator(f.scopedDB, zap.NewNop().Sugar())
			require.NoError(t, runner.EnsureMigrationsTable(f.ctx))
			var before string
			require.NoError(t, f.scopedDB.QueryRowContext(f.ctx, tc.preserved).Scan(&before))
			digest := f.databaseDigest(t)
			err = runner.ApplyMigration(f.ctx, retirementLegacyMigration(t, tc.version))
			require.ErrorContains(t, err, "not in the active catalog")
			var after string
			require.NoError(t, f.scopedDB.QueryRowContext(f.ctx, tc.preserved).Scan(&after))
			require.Equal(t, before, after)
			require.Equal(t, digest, f.databaseDigest(t))
			var count int
			require.NoError(t, f.scopedDB.QueryRowContext(f.ctx, "SELECT count(*) FROM schema_migrations").Scan(&count))
			require.Zero(t, count, "blocked retirement cannot receive a migration receipt")
		})
	}
}

func TestWorkItemRetirementFreshSchemaRetainsCanonicalBootstrap(t *testing.T) {
	f := newCutoverFixture(t)
	f.canonicalChange(t, "CHG-FRESH")
	prepareCurrentWorkItemFixture(t, f.scopedDB, f.ctx)

	require.True(t, f.inspect(t).Switchable)
}

func TestWorkItemRetirementBackupRestorePreservesEvidenceAndExposesNewWrites(t *testing.T) {
	f := newCutoverFixture(t)
	f.canonicalChange(t, "CHG-BACKUP")
	_, err := f.scopedDB.ExecContext(f.ctx, "CREATE TABLE workflows(id bigint PRIMARY KEY, content text); INSERT INTO workflows VALUES(1,'historical')")
	require.NoError(t, err)
	var schema string
	require.NoError(t, f.scopedDB.QueryRowContext(f.ctx, "SELECT current_schema()").Scan(&schema))
	dsn := migrationEntryTarget(t)
	container := "codex-workitem-convergence-pg-20260909"
	if os.Getenv("WORKITEM_V2_POSTGRES_TEST_DSN") != "" {
		container = "codex-workitem-v2-task2-fix1-pg"
	}
	// The fixture already enforces the selected disposable host/database. Verify that
	// the Docker name still points to that endpoint before using its v17 tools.
	inspect, err := exec.Command("docker", "inspect", container).Output()
	require.NoError(t, err)
	var info []struct {
		NetworkSettings struct {
			Ports map[string][]struct {
				HostIP   string `json:"HostIp"`
				HostPort string
			}
		}
	}
	require.NoError(t, json.Unmarshal(inspect, &info))
	require.Len(t, info, 1)
	matched := false
	for _, port := range info[0].NetworkSettings.Ports["5432/tcp"] {
		matched = matched || port.HostIP == dsn.Hostname() && port.HostPort == dsn.Port()
	}
	require.True(t, matched, "refuse a Docker target that differs from the disposable DSN")
	password, _ := dsn.User.Password()
	pgTool := func(tool string, input []byte, args ...string) []byte {
		t.Helper()
		command := exec.Command("docker", append([]string{"exec", "-i", "-e", "PGPASSWORD", container, tool, "--username", dsn.User.Username()}, args...)...)
		command.Env = append(os.Environ(), "PGPASSWORD="+password)
		command.Stdin = bytes.NewReader(input)
		var stderr bytes.Buffer
		command.Stderr = &stderr
		output, err := command.Output()
		// Do not include the command/environment or raw DB diagnostics in logs.
		require.NoError(t, err, "isolated PostgreSQL backup/restore command failed")
		return output
	}
	backup := pgTool("pg_dump", nil, "--dbname", strings.TrimPrefix(dsn.Path, "/"), "--schema", schema, "--format=custom", "--no-owner", "--no-privileges")
	require.NotEmpty(t, backup)
	t.Logf("isolated schema backup SHA256=%x", sha256.Sum256(backup))
	restoredDB := fmt.Sprintf("wi_restore_%d", time.Now().UnixNano())
	_, err = f.db.ExecContext(f.ctx, "CREATE DATABASE "+restoredDB)
	require.NoError(t, err)
	t.Cleanup(func() {
		_, err := f.db.ExecContext(context.Background(), "DROP DATABASE "+restoredDB+" WITH (FORCE)")
		require.NoError(t, err)
		var remaining int
		require.NoError(t, f.db.QueryRowContext(context.Background(), "SELECT count(*) FROM pg_database WHERE datname=$1", restoredDB).Scan(&remaining))
		require.Zero(t, remaining)
		t.Logf("isolated restored database removed; remaining=%d", remaining)
	})
	pgTool("pg_restore", backup, "--dbname", restoredDB, "--exit-on-error", "--no-owner", "--no-privileges")
	dsn.Path = "/" + restoredDB
	query := dsn.Query()
	query.Set("search_path", schema)
	dsn.RawQuery = query.Encode()
	restored, err := sql.Open("postgres", dsn.String())
	require.NoError(t, err)
	t.Cleanup(func() { require.NoError(t, restored.Close()) })
	snapshot := func(db *sql.DB) string {
		t.Helper()
		var digest string
		require.NoError(t, db.QueryRowContext(f.ctx, `SELECT md5(coalesce(string_agg(row_to_json(t)::text, ',' ORDER BY id),'')) FROM tickets t`).Scan(&digest))
		return digest
	}
	require.Equal(t, snapshot(f.scopedDB), snapshot(restored), "restoration must preserve every WorkItem field and original timestamp")
	var content string
	require.NoError(t, restored.QueryRowContext(f.ctx, "SELECT content FROM workflows WHERE id=1").Scan(&content))
	require.Equal(t, "historical", content)
	// A backup restore after the new application has written is not zero loss.
	newID := f.canonicalChange(t, "CHG-AFTER-BACKUP")
	var absent int
	require.NoError(t, restored.QueryRowContext(f.ctx, "SELECT count(*) FROM tickets WHERE id=$1", newID).Scan(&absent))
	require.Zero(t, absent)
	require.NotEqual(t, snapshot(f.scopedDB), snapshot(restored))
	t.Logf("compensation inventory: WorkItem %d CHG-AFTER-BACKUP plus its Change extension were written after this backup; coordinated app/data recovery required", newID)
}

func TestWorkItemRetirementCannotFallThroughToAnotherSchema(t *testing.T) {
	f := newCutoverFixture(t)
	f.canonicalChange(t, "CHG-SCHEMA")
	_, err := f.scopedDB.ExecContext(f.ctx, "ALTER TABLE tickets ADD COLUMN type text; UPDATE tickets SET type='change'")
	require.NoError(t, err)
	var schema string
	require.NoError(t, f.scopedDB.QueryRowContext(f.ctx, "SELECT current_schema()").Scan(&schema))
	emptySchema := schema + "_empty"
	_, err = f.db.ExecContext(f.ctx, "CREATE SCHEMA "+emptySchema)
	require.NoError(t, err)
	t.Cleanup(func() {
		_, err := f.db.ExecContext(context.Background(), "DROP SCHEMA "+emptySchema+" CASCADE")
		require.NoError(t, err)
	})
	dsn := migrationEntryTarget(t)
	query := dsn.Query()
	query.Set("search_path", emptySchema+","+schema)
	dsn.RawQuery = query.Encode()
	wrong, err := sql.Open("postgres", dsn.String())
	require.NoError(t, err)
	t.Cleanup(func() { require.NoError(t, wrong.Close()) })
	runner := migration.NewMigrator(wrong, zap.NewNop().Sugar())
	require.ErrorContains(t, runner.EnsureMigrationsTable(f.ctx), "single explicit schema")
	require.ErrorContains(t, runner.ApplyMigration(f.ctx, retirementLegacyMigration(t, "027_work_item_identity_field_retirement")), "single explicit schema")
	// An active migration reaches the independent exact-schema gate.
	require.NotEmpty(t, migration.RegisteredMigrations)
	require.ErrorContains(t, runner.ApplyMigration(f.ctx, migration.RegisteredMigrations[0]), "single explicit schema")
	var preserved string
	require.NoError(t, f.scopedDB.QueryRowContext(f.ctx, "SELECT type FROM tickets").Scan(&preserved))
	require.Equal(t, "change", preserved, "unqualified historical SQL must not reach a later schema")
	var receipts, decoyTables int
	require.NoError(t, f.scopedDB.QueryRowContext(f.ctx, "SELECT count(*) FROM schema_migrations").Scan(&receipts))
	require.Zero(t, receipts)
	require.NoError(t, f.db.QueryRowContext(f.ctx, "SELECT count(*) FROM pg_tables WHERE schemaname=$1", emptySchema).Scan(&decoyTables))
	require.Zero(t, decoyTables, "refused migration must not create a decoy ledger")
}

// Require an actual historical target; catalog activation must never make a
// fail-closed assertion vacuous by removing the target from the active list.
func retirementLegacyMigration(t *testing.T, version string) migration.Migration {
	t.Helper()
	for _, m := range migration.LegacyMigrations {
		if m.Version == version {
			return m
		}
	}
	t.Fatalf("required historical migration %s not found", version)
	return migration.Migration{}
}

// Current Ent fixtures need actual prerequisite receipts and controlled P before
// the complete ordinary stream. Evidence is isolated test admission, not a
// deployment backup, restore, or business acceptance report.
func prepareCurrentWorkItemFixture(t *testing.T, db *sql.DB, ctx context.Context) {
	t.Helper()
	m := migration.NewMigrator(db, zap.NewNop().Sugar(), migration.MigrationControlConfig{Operator: "test", DeploymentID: "owned-v2"})
	require.NoError(t, m.EnsureMigrationsTable(ctx))
	found := false
	for _, mig := range migration.RegisteredMigrations {
		if mig.Version == migration.WorkItemPrepareVersion {
			found = true
			break
		}
		require.NoError(t, m.ApplyMigration(ctx, mig), mig.Version)
	}
	require.True(t, found, "canonical preparation entry is required")
	require.NoError(t, m.ApplyPreparation(ctx, preparationEvidence(t, m, ctx)))
	_, err := m.RunMigrations(ctx, migration.PostSchemaMigrations())
	require.NoError(t, err)
}
