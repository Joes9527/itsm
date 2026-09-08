//go:build integration_postgres

package integration

import (
	"context"
	"database/sql"
	"fmt"
	"os"
	"strings"
	"testing"
	"time"

	"itsm-backend/migration"

	_ "github.com/lib/pq"
	"github.com/stretchr/testify/require"
)

func TestPostgresWorkflowStartDigestMigration(t *testing.T) {
	const version = "023_add_process_start_request_digest"
	apply := migration.GetMigrationSQL(version)
	require.NotEmpty(t, apply, "durable start identity must have a registered upgrade")
	dsn := os.Getenv("INTAKE_POSTGRES_TEST_DSN")
	require.NotEmpty(t, dsn)
	require.Contains(t, dsn, "/sslvpn_test?")
	db, err := sql.Open("postgres", dsn)
	require.NoError(t, err)
	defer db.Close()
	tx, err := db.BeginTx(context.Background(), nil)
	require.NoError(t, err)
	defer tx.Rollback()
	schema := fmt.Sprintf("a4_start_upgrade_%d", time.Now().UnixNano())
	_, err = tx.Exec("CREATE SCHEMA " + schema)
	require.NoError(t, err)
	_, err = tx.Exec("SET LOCAL search_path TO " + schema)
	require.NoError(t, err)
	_, err = tx.Exec(`CREATE TABLE process_instances(id bigint PRIMARY KEY, tenant_id bigint NOT NULL, process_instance_id varchar UNIQUE NOT NULL, business_key varchar); ALTER TABLE process_instances ENABLE ROW LEVEL SECURITY; INSERT INTO process_instances VALUES(1,1,'legacy-1','ticket:1')`)
	require.NoError(t, err)
	asset, err := os.ReadFile("../../migrations/023_add_process_start_request_digest.sql")
	require.NoError(t, err)
	require.Equal(t, strings.TrimSpace(apply), strings.TrimSpace(string(asset)))
	verify, err := os.ReadFile("../../migrations/023_add_process_start_request_digest_verify.sql")
	require.NoError(t, err)
	reset, err := os.ReadFile("../../migrations/023_add_process_start_request_digest_dev_reset.sql")
	require.NoError(t, err)
	for _, registered := range migration.RegisteredMigrations {
		if registered.Version == version {
			require.Equal(t, strings.TrimSpace(registered.RollbackSQL), strings.TrimSpace(string(reset)))
		}
	}
	assertFails := func(statement string) {
		t.Helper()
		_, err := tx.Exec("SAVEPOINT expected_failure")
		require.NoError(t, err)
		_, err = tx.Exec(statement)
		require.Error(t, err)
		_, err = tx.Exec("ROLLBACK TO SAVEPOINT expected_failure")
		require.NoError(t, err)
	}
	// An empty table later in search_path is not the local reset target.
	shadow := schema + "_shadow"
	_, err = tx.Exec("CREATE SCHEMA " + shadow + "; CREATE TABLE " + shadow + ".process_instances(start_request_digest varchar)")
	require.NoError(t, err)
	_, err = tx.Exec("SET LOCAL search_path TO " + schema + "," + shadow)
	require.NoError(t, err)
	_, err = tx.Exec("ALTER TABLE " + schema + ".process_instances RENAME TO saved_instances")
	require.NoError(t, err)
	assertFails(string(reset))
	_, err = tx.Exec("ALTER TABLE " + schema + ".saved_instances RENAME TO process_instances; SET LOCAL search_path TO " + schema)
	require.NoError(t, err)
	assertFails(string(verify))
	for range 2 {
		_, err = tx.Exec(apply)
		require.NoError(t, err)
	}
	_, err = tx.Exec(string(verify))
	require.NoError(t, err)
	var identity string
	var digest sql.NullString
	var rls bool
	require.NoError(t, tx.QueryRow("SELECT process_instance_id,start_request_digest FROM process_instances WHERE id=1").Scan(&identity, &digest))
	require.Equal(t, "legacy-1", identity)
	require.False(t, digest.Valid)
	require.NoError(t, tx.QueryRow("SELECT relrowsecurity FROM pg_class WHERE oid='process_instances'::regclass").Scan(&rls))
	require.True(t, rls)
	assertFails("INSERT INTO process_instances(id,tenant_id,process_instance_id) VALUES(2,1,'legacy-1')")
	_, err = tx.Exec("UPDATE process_instances SET start_request_digest=repeat('a',64) WHERE id=1")
	require.NoError(t, err)
	assertFails(string(reset))
	// Only an empty development table may discard its durable start receipts.
	_, err = tx.Exec("DELETE FROM process_instances")
	require.NoError(t, err)
	for range 2 {
		_, err = tx.Exec(string(reset))
		require.NoError(t, err)
	}
	assertFails(string(verify))
	_, err = tx.Exec("ALTER TABLE process_instances ADD COLUMN start_request_digest integer")
	require.NoError(t, err)
	assertFails(apply)
	_, err = tx.Exec("ALTER TABLE process_instances DROP COLUMN start_request_digest")
	require.NoError(t, err)
	_, err = tx.Exec(apply)
	require.NoError(t, err)
	_, err = tx.Exec(string(verify))
	require.NoError(t, err)
}
