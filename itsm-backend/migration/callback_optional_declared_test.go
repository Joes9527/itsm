package migration

import (
	"context"
	"database/sql"
	"os"
	"strings"
	"testing"

	_ "github.com/lib/pq"
	"github.com/stretchr/testify/require"
)

const migration021CallbackOptionalDeclaredSQL = `ALTER TABLE process_callback_outboxes
    ADD COLUMN IF NOT EXISTS optional_declared boolean NOT NULL DEFAULT false;`

func TestMigration021CallbackOptionalDeclaredAsset(t *testing.T) {
	script := readMigration021CallbackOptionalDeclared(t)
	require.Equal(t, normalizeMigrationSQL(migration021CallbackOptionalDeclaredSQL), normalizeMigrationSQL(string(script)))
}

func TestMigration021CallbackOptionalDeclaredIsIdempotent(t *testing.T) {
	dsn := os.Getenv("ITSM_TEST_DB")
	if dsn == "" {
		t.Skip("ITSM_TEST_DB is not set; skipping PostgreSQL migration execution")
	}

	db, err := sql.Open("postgres", dsn)
	require.NoError(t, err)
	t.Cleanup(func() { require.NoError(t, db.Close()) })
	require.NoError(t, db.PingContext(context.Background()))

	tx, err := db.BeginTx(context.Background(), nil)
	require.NoError(t, err)
	t.Cleanup(func() { _ = tx.Rollback() })
	script := readMigration021CallbackOptionalDeclared(t)

	_, err = tx.Exec(`
		CREATE TEMPORARY TABLE process_callback_outboxes (
			id bigint PRIMARY KEY
		) ON COMMIT DROP;
		INSERT INTO process_callback_outboxes (id) VALUES (1);
	`)
	require.NoError(t, err)

	for range 2 {
		_, err = tx.Exec(string(script))
		require.NoError(t, err)
	}

	var optionalDeclared bool
	require.NoError(t, tx.QueryRow(`
		SELECT optional_declared
		FROM process_callback_outboxes
		WHERE id = 1
	`).Scan(&optionalDeclared))
	require.False(t, optionalDeclared)
}

func readMigration021CallbackOptionalDeclared(t *testing.T) []byte {
	t.Helper()
	script, err := os.ReadFile("../migrations/021_add_callback_optional_declared.sql")
	require.NoError(t, err)
	return script
}

func normalizeMigrationSQL(value string) string {
	return strings.Join(strings.Fields(value), " ")
}
