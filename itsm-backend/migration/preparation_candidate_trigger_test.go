//go:build candidate_scope

package migration

import (
	"context"
	"database/sql"
	"fmt"
	"github.com/google/uuid"
	_ "github.com/lib/pq"
	"github.com/stretchr/testify/require"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestPreparationAdmitsOnlyRegisteredCandidateTrigger(t *testing.T) {
	socket := os.Getenv("CANDIDATE_SCOPE_TEST_SOCKET")
	if socket == "" {
		t.Skip("requires explicitly isolated PostgreSQL socket")
	}
	require.True(t, filepath.IsAbs(socket))
	marker, err := os.ReadFile(filepath.Join(socket, "candidate-test-instance"))
	require.NoError(t, err)
	require.Equal(t, "itsm-candidate-isolated-test\n", string(marker))
	dsn := func(db string) string {
		return fmt.Sprintf("host=%s port=25439 dbname=%s user=candidate_test_owner sslmode=disable", socket, db)
	}
	admin, err := sql.Open("postgres", dsn("postgres"))
	require.NoError(t, err)
	defer admin.Close()
	name := "prep_trigger_" + strings.ReplaceAll(uuid.NewString(), "-", "")
	_, err = admin.Exec("CREATE DATABASE " + name)
	require.NoError(t, err)
	defer func() { _, e := admin.Exec("DROP DATABASE " + name + " WITH (FORCE)"); require.NoError(t, e) }()
	db, err := sql.Open("postgres", dsn(name))
	require.NoError(t, err)
	defer db.Close()
	ctx := context.Background()
	_, err = db.Exec(`CREATE TABLE tenants(id bigint PRIMARY KEY); CREATE TABLE users(id bigint PRIMARY KEY); CREATE TABLE tickets(id bigint PRIMARY KEY,tenant_id bigint); CREATE TABLE outbox_events(id bigint PRIMARY KEY); CREATE TABLE process_instances(id bigint PRIMARY KEY); CREATE TABLE schema_migrations(version text PRIMARY KEY,checksum text); INSERT INTO tickets VALUES(1,1);`)
	require.NoError(t, err)
	require.NoError(t, validatePreparationTriggers(ctx, db, "public", "tickets", true))
	_, err = db.Exec(GetMigrationSQL(CandidateExecutionScopeVersion))
	require.NoError(t, err)
	require.Error(t, validatePreparationTriggers(ctx, db, "public", "tickets", true), "unrecorded trigger is not admitted")
	_, err = db.Exec("INSERT INTO schema_migrations VALUES($1,$2)", CandidateExecutionScopeVersion, checksumSQL(GetMigrationSQL(CandidateExecutionScopeVersion)))
	require.NoError(t, err)
	require.NoError(t, validatePreparationTriggers(ctx, db, "public", "tickets", true))
	require.Error(t, validatePreparationTriggers(ctx, db, "public", "tickets", false), "initial P cannot adopt later triggers")
	for name, statement := range map[string]string{
		"extra trigger":    `CREATE TRIGGER unexpected AFTER INSERT ON tickets FOR EACH ROW EXECUTE FUNCTION register_new_execution_member()`,
		"disabled trigger": `ALTER TABLE tickets DISABLE TRIGGER register_new_execution_member`,
		"missing trigger":  `DROP TRIGGER register_new_execution_member ON tickets`,
		"changed function": `CREATE OR REPLACE FUNCTION register_new_execution_member() RETURNS trigger LANGUAGE plpgsql SECURITY DEFINER SET search_path=pg_catalog AS $$ BEGIN RETURN NEW; END $$`,
		"public execute":   `GRANT EXECUTE ON FUNCTION register_new_execution_member() TO PUBLIC`,
		"wrong receipt":    `UPDATE schema_migrations SET checksum='changed'`,
	} {
		t.Run(name, func(t *testing.T) {
			tx, e := db.BeginTx(ctx, nil)
			require.NoError(t, e)
			defer tx.Rollback()
			_, e = tx.Exec(statement)
			require.NoError(t, e)
			require.Error(t, validatePreparationTriggers(ctx, tx, "public", "tickets", true))
		})
	}
	var count int
	require.NoError(t, db.QueryRow("SELECT count(*) FROM tickets WHERE id=1 AND tenant_id=1").Scan(&count))
	require.Equal(t, 1, count)
}
