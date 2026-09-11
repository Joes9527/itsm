//go:build integration_postgres

package integration

import (
	"bytes"
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/json"
	"fmt"
	"net/url"
	"os"
	"os/exec"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
	"go.uber.org/zap"

	"itsm-backend/migration"
)

func TestWorkItemRetirementAutomaticMigrationPreservesLegacyEvidence(t *testing.T) {
	for _, tc := range []struct{ name, version, setup, preserved string }{
		{"legacy workflow rows", "022_drop_professional_extension_shared_fields",
			"CREATE TABLE workflows(id bigint PRIMARY KEY, content text); INSERT INTO workflows VALUES(1, 'preserve historical evidence')",
			"SELECT content FROM workflows WHERE id=1"},
		{"shared professional field", "022_drop_professional_extension_shared_fields",
			"ALTER TABLE changes ADD COLUMN title text; UPDATE changes SET title='preserve historical evidence'",
			"SELECT title FROM changes"},
		{"identity field", "027_work_item_identity_field_retirement",
			"ALTER TABLE tickets ADD COLUMN type text; UPDATE tickets SET type='change'",
			"SELECT type FROM tickets"},
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
			for _, m := range migration.RegisteredMigrations {
				if m.Version != tc.version {
					continue
				}
				err = runner.ApplyMigration(f.ctx, m)
				require.ErrorContains(t, err, "automatic WorkItem retirement is blocked")
			}
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
	runner := migration.NewMigrator(f.scopedDB, zap.NewNop().Sugar())
	require.NoError(t, runner.EnsureMigrationsTable(f.ctx))
	for _, m := range migration.RegisteredMigrations {
		if m.Version == "022_drop_professional_extension_shared_fields" || m.Version == "027_work_item_identity_field_retirement" {
			require.NoError(t, runner.ApplyMigration(f.ctx, m))
		}
	}
	require.True(t, f.inspect(t).Switchable)
}

func TestWorkItemRetirementBackupRestorePreservesEvidenceAndExposesNewWrites(t *testing.T) {
	f := newCutoverFixture(t)
	f.canonicalChange(t, "CHG-BACKUP")
	_, err := f.scopedDB.ExecContext(f.ctx, "CREATE TABLE workflows(id bigint PRIMARY KEY, content text); INSERT INTO workflows VALUES(1,'historical')")
	require.NoError(t, err)
	var schema string
	require.NoError(t, f.scopedDB.QueryRowContext(f.ctx, "SELECT current_schema()").Scan(&schema))
	dsn, err := url.Parse(os.Getenv("INTAKE_POSTGRES_TEST_DSN"))
	require.NoError(t, err)
	// The fixture already enforces the one disposable host/database. Verify that
	// the Docker name still points to that endpoint before using its v17 tools.
	inspect, err := exec.Command("docker", "inspect", "codex-workitem-convergence-pg-20260909").Output()
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
		matched = matched || port.HostIP == "127.0.0.1" && port.HostPort == "36444"
	}
	require.True(t, matched, "refuse a Docker target that differs from the disposable DSN")
	password, _ := dsn.User.Password()
	pgTool := func(tool string, input []byte, args ...string) []byte {
		t.Helper()
		command := exec.Command("docker", append([]string{"exec", "-i", "-e", "PGPASSWORD", "codex-workitem-convergence-pg-20260909", tool, "--username", dsn.User.Username()}, args...)...)
		command.Env = append(os.Environ(), "PGPASSWORD="+password)
		command.Stdin = bytes.NewReader(input)
		var stderr bytes.Buffer
		command.Stderr = &stderr
		output, err := command.Output()
		// Do not include the command/environment or raw DB diagnostics in logs.
		require.NoError(t, err, "isolated PostgreSQL backup/restore command failed")
		return output
	}
	backup := pgTool("pg_dump", nil, "--dbname", "sslvpn_test", "--schema", schema, "--format=custom", "--no-owner", "--no-privileges")
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
	dsn, err := url.Parse(os.Getenv("INTAKE_POSTGRES_TEST_DSN"))
	require.NoError(t, err)
	query := dsn.Query()
	query.Set("search_path", emptySchema+","+schema)
	dsn.RawQuery = query.Encode()
	wrong, err := sql.Open("postgres", dsn.String())
	require.NoError(t, err)
	t.Cleanup(func() { require.NoError(t, wrong.Close()) })
	runner := migration.NewMigrator(wrong, zap.NewNop().Sugar())
	require.NoError(t, runner.EnsureMigrationsTable(f.ctx))
	for _, m := range migration.RegisteredMigrations {
		if m.Version == "027_work_item_identity_field_retirement" {
			require.ErrorContains(t, runner.ApplyMigration(f.ctx, m), "automatic WorkItem retirement is blocked")
		}
	}
	var preserved string
	require.NoError(t, f.scopedDB.QueryRowContext(f.ctx, "SELECT type FROM tickets").Scan(&preserved))
	require.Equal(t, "change", preserved, "unqualified historical SQL must not reach a later schema")
}
