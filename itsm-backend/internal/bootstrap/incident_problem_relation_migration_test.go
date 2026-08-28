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

func TestPrepareIncidentProblemRelationMigrationSkipsMissingTable(t *testing.T) {
	db := incidentProblemRelationMigrationTestDB(t)
	defer db.Close()

	withIncidentProblemRelationMigrationTestSchema(t, db, func(ctx context.Context) {
		err := prepareIncidentProblemRelationMigration(ctx, db, zap.NewNop().Sugar())
		require.NoError(t, err)
	})
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
