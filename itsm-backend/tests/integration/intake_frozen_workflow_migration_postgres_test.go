//go:build integration_postgres

package integration

import (
	"context"
	"database/sql"
	"fmt"
	"github.com/stretchr/testify/require"
	"itsm-backend/migration"
	"os"
	"testing"
	"time"
)

func TestIntakeFrozenWorkflowMigrationSelectedSchema(t *testing.T) {
	dsn := os.Getenv("INTAKE_POSTGRES_TEST_DSN")
	require.NotEmpty(t, dsn)
	require.Contains(t, dsn, "/sslvpn_test?")
	db, err := sql.Open("postgres", dsn)
	require.NoError(t, err)
	defer db.Close()
	tx, err := db.BeginTx(context.Background(), nil)
	require.NoError(t, err)
	defer tx.Rollback() // Both schemas and all fixtures are transaction-local.
	selected := fmt.Sprintf("frozen_036_%d", time.Now().UnixNano())
	shadow := selected + "_shadow"
	_, err = tx.Exec("CREATE SCHEMA " + selected + "; CREATE SCHEMA " + shadow + "; CREATE TABLE " + shadow + ".intake_resolution_snapshots(id bigint PRIMARY KEY); SET LOCAL search_path TO " + selected + "," + shadow)
	require.NoError(t, err)
	var actual string
	require.NoError(t, tx.QueryRow("SELECT current_schema()").Scan(&actual))
	require.Equal(t, selected, actual)
	apply := migration.GetMigrationSQL("036_intake_frozen_workflow_context")
	require.NotEmpty(t, apply)
	_, err = tx.Exec("SAVEPOINT missing_selected_table")
	require.NoError(t, err)
	_, migrationErr := tx.Exec(apply)
	if migrationErr == nil {
		var changed int
		require.NoError(t, tx.QueryRow("SELECT count(*) FROM information_schema.columns WHERE table_schema=$1 AND table_name='intake_resolution_snapshots' AND column_name IN ('workflow_definition_digest','workflow_variables')", shadow).Scan(&changed))
		t.Logf("migration incorrectly succeeded; decoy new columns=%d", changed)
	}
	require.Error(t, migrationErr, "missing selected table must fail, never alter a later search_path table")
	_, err = tx.Exec("ROLLBACK TO SAVEPOINT missing_selected_table")
	require.NoError(t, err)
	_, err = tx.Exec("CREATE TABLE " + selected + ".intake_resolution_snapshots(id bigint PRIMARY KEY); INSERT INTO " + selected + ".intake_resolution_snapshots VALUES(1)")
	require.NoError(t, err)
	for range 2 {
		_, err = tx.Exec(apply)
		require.NoError(t, err)
	}
	var digest, variables sql.NullString
	require.NoError(t, tx.QueryRow("SELECT workflow_definition_digest,workflow_variables::text FROM "+selected+".intake_resolution_snapshots WHERE id=1").Scan(&digest, &variables))
	require.False(t, digest.Valid, "historical definition must not be reconstructed")
	require.False(t, variables.Valid, "historical variables must not be reconstructed")
	var count int
	require.NoError(t, tx.QueryRow("SELECT count(*) FROM information_schema.columns WHERE table_schema=$1 AND table_name='intake_resolution_snapshots' AND column_name IN ('workflow_definition_digest','workflow_variables')", shadow).Scan(&count))
	require.Zero(t, count, "decoy table must remain untouched")
}
