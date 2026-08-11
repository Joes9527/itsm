package bootstrap

import (
	"context"
	"database/sql"
	"fmt"
	"os"
	"testing"

	_ "github.com/lib/pq"
	"github.com/stretchr/testify/require"
	"go.uber.org/zap"
)

// serviceRequestMigrationTestDB opens a real *sql.DB against the local dev
// Postgres instance, mirroring the connection pattern used by
// internal/initialization/fence_test.go. It is skipped (not failed) when no
// database is reachable, since this is an integration test.
//
// Tests run against a dedicated, per-test Postgres schema (not "public") so
// they never touch the real service_requests table or its data. SetMaxOpenConns(1)
// keeps every query on the same underlying connection so the session-scoped
// `SET search_path` sticks for the lifetime of the test.
func serviceRequestMigrationTestDB(t *testing.T) *sql.DB {
	t.Helper()
	dsn := os.Getenv("SERVICE_REQUEST_MIGRATION_TEST_DSN")
	if dsn == "" {
		dsn = "host=127.0.0.1 port=5432 user=itsm_user password=dev123 dbname=itsm sslmode=disable"
	}
	db, err := sql.Open("postgres", dsn)
	if err != nil {
		t.Skip("database not available, skipping integration test")
	}
	if err := db.Ping(); err != nil {
		t.Skip("cannot ping database, skipping integration test: " + err.Error())
	}
	db.SetMaxOpenConns(1)
	return db
}

// withTestSchema creates an isolated schema, points the test connection's
// search_path at it, runs fn, then drops the schema.
func withTestSchema(t *testing.T, db *sql.DB, fn func(ctx context.Context)) {
	t.Helper()
	ctx := context.Background()
	schemaName := fmt.Sprintf("sr_ticket_migration_test_%d", os.Getpid())

	_, err := db.ExecContext(ctx, fmt.Sprintf(`DROP SCHEMA IF EXISTS %s CASCADE`, schemaName))
	require.NoError(t, err)
	_, err = db.ExecContext(ctx, fmt.Sprintf(`CREATE SCHEMA %s`, schemaName))
	require.NoError(t, err)
	_, err = db.ExecContext(ctx, fmt.Sprintf(`SET search_path TO %s`, schemaName))
	require.NoError(t, err)

	t.Cleanup(func() {
		_, _ = db.ExecContext(ctx, `SET search_path TO public`)
		_, _ = db.ExecContext(ctx, fmt.Sprintf(`DROP SCHEMA IF EXISTS %s CASCADE`, schemaName))
	})

	fn(ctx)
}

func TestPrepareServiceRequestTicketMigration_TableDoesNotExist(t *testing.T) {
	db := serviceRequestMigrationTestDB(t)
	defer db.Close()
	logger := zap.NewNop().Sugar()

	withTestSchema(t, db, func(ctx context.Context) {
		// No service_requests table created in this schema at all.
		err := prepareServiceRequestTicketMigration(ctx, db, logger)
		require.NoError(t, err)
	})
}

func TestPrepareServiceRequestTicketMigration_ColumnAlreadyExists(t *testing.T) {
	db := serviceRequestMigrationTestDB(t)
	defer db.Close()
	logger := zap.NewNop().Sugar()

	withTestSchema(t, db, func(ctx context.Context) {
		_, err := db.ExecContext(ctx, `
			CREATE TABLE service_requests (
				id BIGSERIAL PRIMARY KEY,
				ticket_id BIGINT NOT NULL
			)
		`)
		require.NoError(t, err)
		_, err = db.ExecContext(ctx, `INSERT INTO service_requests (ticket_id) VALUES (1)`)
		require.NoError(t, err)

		err = prepareServiceRequestTicketMigration(ctx, db, logger)
		require.NoError(t, err)
	})
}

func TestPrepareServiceRequestTicketMigration_TableExistsZeroRows(t *testing.T) {
	db := serviceRequestMigrationTestDB(t)
	defer db.Close()
	logger := zap.NewNop().Sugar()

	withTestSchema(t, db, func(ctx context.Context) {
		_, err := db.ExecContext(ctx, `
			CREATE TABLE service_requests (
				id BIGSERIAL PRIMARY KEY,
				status VARCHAR(32)
			)
		`)
		require.NoError(t, err)

		err = prepareServiceRequestTicketMigration(ctx, db, logger)
		require.NoError(t, err)
	})
}

func TestPrepareServiceRequestTicketMigration_TableExistsWithRowsNoColumn(t *testing.T) {
	db := serviceRequestMigrationTestDB(t)
	defer db.Close()
	logger := zap.NewNop().Sugar()

	withTestSchema(t, db, func(ctx context.Context) {
		_, err := db.ExecContext(ctx, `
			CREATE TABLE service_requests (
				id BIGSERIAL PRIMARY KEY,
				status VARCHAR(32)
			)
		`)
		require.NoError(t, err)
		_, err = db.ExecContext(ctx, `INSERT INTO service_requests (status) VALUES ('pending')`)
		require.NoError(t, err)

		err = prepareServiceRequestTicketMigration(ctx, db, logger)
		require.Error(t, err)
		require.ErrorContains(t, err, "has 1 existing row")
		require.ErrorContains(t, err, "ticket_id")
	})
}
