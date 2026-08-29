package bootstrap

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"os"
	"testing"

	_ "github.com/lib/pq"
	_ "github.com/mattn/go-sqlite3"
	"github.com/stretchr/testify/require"
	"go.uber.org/zap"
)

type fakeSQLStateError struct {
	code string
	msg  string
}

func (e fakeSQLStateError) Error() string {
	return e.msg
}

func (e fakeSQLStateError) SQLState() string {
	return e.code
}

func incidentProblemRelationMigrationTestDB(t *testing.T) *sql.DB {
	t.Helper()
	dsn := os.Getenv("INCIDENT_PROBLEM_RELATION_MIGRATION_TEST_DSN")
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

func withIncidentProblemRelationMigrationTestSchema(t *testing.T, db *sql.DB, fn func(ctx context.Context)) {
	t.Helper()
	ctx := context.Background()
	schemaName := fmt.Sprintf("incident_problem_relation_migration_test_%d", os.Getpid())

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

func TestIsMissingTableErrorStrictClassification(t *testing.T) {
	cases := []struct {
		name string
		err  error
		want bool
	}{
		{
			name: "nil",
			err:  nil,
			want: false,
		},
		{
			name: "sqlite target table missing",
			err:  errors.New("no such table: work_item_relations"),
			want: true,
		},
		{
			name: "wrapped sqlite target table missing",
			err:  fmt.Errorf("probe work_item_relations: %w", errors.New("no such table: work_item_relations")),
			want: true,
		},
		{
			name: "postgres undefined table sqlstate",
			err:  fakeSQLStateError{code: "42P01", msg: `pq: relation "work_item_relations" does not exist`},
			want: true,
		},
		{
			name: "wrapped postgres undefined table sqlstate",
			err:  fmt.Errorf("probe work_item_relations: %w", fakeSQLStateError{code: "42P01", msg: "undefined_table"}),
			want: true,
		},
		{
			name: "unrelated resource does not exist",
			err:  errors.New("resource does not exist"),
			want: false,
		},
		{
			name: "different sqlite table missing",
			err:  errors.New("no such table: service_requests"),
			want: false,
		},
		{
			name: "other sqlstate",
			err:  fakeSQLStateError{code: "42501", msg: "permission denied for table work_item_relations"},
			want: false,
		},
		{
			name: "syntax error",
			err:  errors.New(`near "FROMM": syntax error`),
			want: false,
		},
		{
			name: "connection failure mentioning target table",
			err:  errors.New("connection refused while probing work_item_relations"),
			want: false,
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			require.Equal(t, tc.want, isMissingTableError(tc.err))
		})
	}
}

func TestPrepareIncidentProblemRelationMigrationSkipsMissingTable(t *testing.T) {
	db := incidentProblemRelationMigrationTestDB(t)
	defer db.Close()

	withIncidentProblemRelationMigrationTestSchema(t, db, func(ctx context.Context) {
		err := prepareIncidentProblemRelationMigration(ctx, db, zap.NewNop().Sugar())
		require.NoError(t, err)
	})
}

func TestPrepareIncidentProblemRelationMigrationSQLiteSkipsMissingTable(t *testing.T) {
	db, err := sql.Open("sqlite3", "file:incident_problem_relation_missing?mode=memory&cache=shared")
	require.NoError(t, err)
	defer db.Close()

	err = prepareIncidentProblemRelationMigration(context.Background(), db, zap.NewNop().Sugar())
	require.NoError(t, err)
}

func TestPrepareIncidentProblemRelationMigrationSQLiteAllowsExistingTableWithoutDuplicates(t *testing.T) {
	db, err := sql.Open("sqlite3", "file:incident_problem_relation_unique?mode=memory&cache=shared")
	require.NoError(t, err)
	defer db.Close()

	_, err = db.ExecContext(context.Background(), `
		CREATE TABLE work_item_relations (
			id INTEGER PRIMARY KEY AUTOINCREMENT,
			tenant_id INTEGER NOT NULL,
			source_work_item_id INTEGER NOT NULL,
			target_work_item_id INTEGER NOT NULL,
			relation_type TEXT NOT NULL,
			deleted_at DATETIME
		)
	`)
	require.NoError(t, err)
	_, err = db.ExecContext(context.Background(), `
		INSERT INTO work_item_relations
			(tenant_id, source_work_item_id, target_work_item_id, relation_type, deleted_at)
		VALUES
			(1, 10, 20, 'investigated_by', NULL),
			(1, 11, 30, 'investigated_by', NULL),
			(1, 10, 40, 'blocks', NULL),
			(1, 10, 50, 'investigated_by', '2026-08-28 00:00:00')
	`)
	require.NoError(t, err)

	err = prepareIncidentProblemRelationMigration(context.Background(), db, zap.NewNop().Sugar())
	require.NoError(t, err)
}

func TestPrepareIncidentProblemRelationMigrationSQLiteRejectsDuplicateLiveRelations(t *testing.T) {
	db, err := sql.Open("sqlite3", "file:incident_problem_relation_duplicate?mode=memory&cache=shared")
	require.NoError(t, err)
	defer db.Close()

	_, err = db.ExecContext(context.Background(), `
		CREATE TABLE work_item_relations (
			id INTEGER PRIMARY KEY AUTOINCREMENT,
			tenant_id INTEGER NOT NULL,
			source_work_item_id INTEGER NOT NULL,
			target_work_item_id INTEGER NOT NULL,
			relation_type TEXT NOT NULL,
			deleted_at DATETIME
		)
	`)
	require.NoError(t, err)
	_, err = db.ExecContext(context.Background(), `
		INSERT INTO work_item_relations
			(tenant_id, source_work_item_id, target_work_item_id, relation_type)
		VALUES
			(2, 100, 200, 'investigated_by'),
			(2, 100, 300, 'investigated_by')
	`)
	require.NoError(t, err)

	err = prepareIncidentProblemRelationMigration(context.Background(), db, zap.NewNop().Sugar())
	require.Error(t, err)
	require.ErrorContains(t, err, "tenant_id=2")
	require.ErrorContains(t, err, "source_work_item_id=100")
	require.ErrorContains(t, err, "target_work_item_ids=[200 300]")
	require.ErrorContains(t, err, "relation_ids=[1 2]")
}

func TestPrepareIncidentProblemRelationMigrationRejectsDuplicateLiveRelations(t *testing.T) {
	db := incidentProblemRelationMigrationTestDB(t)
	defer db.Close()

	withIncidentProblemRelationMigrationTestSchema(t, db, func(ctx context.Context) {
		_, err := db.ExecContext(ctx, `
			CREATE TABLE work_item_relations (
				id BIGSERIAL PRIMARY KEY,
				tenant_id BIGINT NOT NULL,
				source_work_item_id BIGINT NOT NULL,
				target_work_item_id BIGINT NOT NULL,
				relation_type VARCHAR(64) NOT NULL,
				deleted_at TIMESTAMPTZ
			)
		`)
		require.NoError(t, err)
		_, err = db.ExecContext(ctx, `
			INSERT INTO work_item_relations
				(tenant_id, source_work_item_id, target_work_item_id, relation_type)
			VALUES
				(1, 10, 20, 'investigated_by'),
				(1, 10, 30, 'investigated_by')
		`)
		require.NoError(t, err)

		err = prepareIncidentProblemRelationMigration(ctx, db, zap.NewNop().Sugar())
		require.Error(t, err)
		require.ErrorContains(t, err, "tenant_id=1")
		require.ErrorContains(t, err, "source_work_item_id=10")
		require.ErrorContains(t, err, "target_work_item_ids=[20 30]")
		require.ErrorContains(t, err, "relation_ids=[1 2]")
	})
}
