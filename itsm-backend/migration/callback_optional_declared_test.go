package migration

import (
	"context"
	"database/sql"
	"fmt"
	"os"
	"strings"
	"testing"
	"time"

	_ "github.com/lib/pq"
	"github.com/stretchr/testify/require"
)

const migration021CallbackOptionalDeclaredSQL = `ALTER TABLE process_callback_outboxes
    ADD COLUMN IF NOT EXISTS optional_declared boolean NOT NULL DEFAULT false;`

const migration021CallbackOptionalDeclaredResetSQL = `ALTER TABLE process_callback_outboxes
    DROP COLUMN IF EXISTS optional_declared;`

const migration021CallbackOptionalDeclaredVerifySQL = `DO $$
BEGIN
    IF NOT EXISTS (
        SELECT 1
        FROM pg_attribute column_attribute
        JOIN pg_class table_relation
            ON table_relation.oid = column_attribute.attrelid
        JOIN pg_namespace table_schema
            ON table_schema.oid = table_relation.relnamespace
        JOIN pg_attrdef column_default
            ON column_default.adrelid = table_relation.oid
           AND column_default.adnum = column_attribute.attnum
        WHERE table_schema.nspname = current_schema()
          AND table_relation.relname = 'process_callback_outboxes'
          AND table_relation.relkind IN ('r', 'p')
          AND column_attribute.attname = 'optional_declared'
          AND column_attribute.atttypid = 'boolean'::regtype
          AND column_attribute.attnotnull
          AND NOT column_attribute.attisdropped
          AND pg_get_expr(column_default.adbin, column_default.adrelid) = 'false'
    ) THEN
        RAISE EXCEPTION
            'process_callback_outboxes.optional_declared must be boolean NOT NULL DEFAULT false';
    END IF;
END $$;`

func TestMigration021CallbackOptionalDeclaredAssets(t *testing.T) {
	tests := []struct {
		name     string
		filename string
		want     string
	}{
		{
			name:     "apply",
			filename: "021_add_callback_optional_declared.sql",
			want:     migration021CallbackOptionalDeclaredSQL,
		},
		{
			name:     "development reset",
			filename: "021_add_callback_optional_declared_dev_reset.sql",
			want:     migration021CallbackOptionalDeclaredResetSQL,
		},
		{
			name:     "verify",
			filename: "021_add_callback_optional_declared_verify.sql",
			want:     migration021CallbackOptionalDeclaredVerifySQL,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			script := readMigration021Asset(t, tt.filename)
			require.Equal(t, normalizeMigrationSQL(tt.want), normalizeMigrationSQL(string(script)))
		})
	}
}

func TestMigration021CallbackOptionalDeclaredIsRegisteredBetweenWorkItemMigrations(t *testing.T) {
	registeredSQL := GetMigrationSQL("021_add_callback_optional_declared")
	require.Equal(t, normalizeMigrationSQL(migration021CallbackOptionalDeclaredSQL), normalizeMigrationSQL(registeredSQL))

	versions := make([]string, 0, len(RegisteredMigrations))
	for _, migration := range RegisteredMigrations {
		versions = append(versions, migration.Version)
	}
	require.Equal(t, []string{
		"020_work_item_number_allocator",
		"021_add_callback_optional_declared",
		"022_drop_professional_extension_shared_fields",
	}, versions[12:15])
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

	ctx := context.Background()
	tx, err := db.BeginTx(ctx, nil)
	require.NoError(t, err)
	t.Cleanup(func() { _ = tx.Rollback() })
	applyScript := readMigration021Asset(t, "021_add_callback_optional_declared.sql")
	resetScript := readMigration021Asset(t, "021_add_callback_optional_declared_dev_reset.sql")
	verifyScript := readMigration021Asset(t, "021_add_callback_optional_declared_verify.sql")

	schemaName := fmt.Sprintf("migration_021_test_%d", time.Now().UnixNano())
	_, err = tx.ExecContext(ctx, fmt.Sprintf(`CREATE SCHEMA %q`, schemaName))
	require.NoError(t, err)
	_, err = tx.ExecContext(ctx, fmt.Sprintf(`SET LOCAL search_path TO %q`, schemaName))
	require.NoError(t, err)

	_, err = tx.ExecContext(ctx, `
		CREATE TABLE process_callback_outboxes (
			id bigint PRIMARY KEY
		);
		INSERT INTO process_callback_outboxes (id) VALUES (1);
	`)
	require.NoError(t, err)
	requireMigration021VerificationFails(t, tx, verifyScript)

	for range 2 {
		_, err = tx.ExecContext(ctx, string(applyScript))
		require.NoError(t, err)
	}
	_, err = tx.ExecContext(ctx, string(verifyScript))
	require.NoError(t, err)

	var optionalDeclared bool
	require.NoError(t, tx.QueryRowContext(ctx, `
		SELECT optional_declared
		FROM process_callback_outboxes
		WHERE id = 1
	`).Scan(&optionalDeclared))
	require.False(t, optionalDeclared)

	for range 2 {
		_, err = tx.ExecContext(ctx, string(resetScript))
		require.NoError(t, err)
	}
	requireMigration021VerificationFails(t, tx, verifyScript)

	_, err = tx.ExecContext(ctx, string(applyScript))
	require.NoError(t, err)
	_, err = tx.ExecContext(ctx, string(verifyScript))
	require.NoError(t, err)
}

func readMigration021Asset(t *testing.T, filename string) []byte {
	t.Helper()
	script, err := os.ReadFile("../migrations/" + filename)
	require.NoError(t, err)
	return script
}

func requireMigration021VerificationFails(t *testing.T, tx *sql.Tx, verifyScript []byte) {
	t.Helper()
	ctx := context.Background()
	require.NoError(t, execMigration021SQL(ctx, tx, "SAVEPOINT migration_021_verify"))
	require.Error(t, execMigration021SQL(ctx, tx, string(verifyScript)))
	require.NoError(t, execMigration021SQL(ctx, tx, "ROLLBACK TO SAVEPOINT migration_021_verify"))
	require.NoError(t, execMigration021SQL(ctx, tx, "RELEASE SAVEPOINT migration_021_verify"))
}

func execMigration021SQL(ctx context.Context, tx *sql.Tx, statement string) error {
	_, err := tx.ExecContext(ctx, statement)
	return err
}

func normalizeMigrationSQL(value string) string {
	return strings.Join(strings.Fields(value), " ")
}
