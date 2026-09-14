//go:build integration_postgres

package integration

import (
	"context"
	"database/sql"
	"fmt"
	"net/url"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
	_ "github.com/lib/pq"
	"github.com/stretchr/testify/require"
)

func TestBPMNAssignmentSourceMigrationEnforcesImmutabilityAndExactDefault(t *testing.T) {
	db := openBPMNAssignmentSourceMigrationDB(t)
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	_, err := db.ExecContext(ctx, `
		CREATE TABLE process_tasks (
			id BIGSERIAL PRIMARY KEY,
			assignee varchar,
			candidate_users varchar,
			candidate_groups varchar
		)
	`)
	require.NoError(t, err)
	require.NoError(t, execBPMNAssignmentSourceMigrationAsset(ctx, db, "032_bpmn_assignment_source.sql"))
	require.NoError(t, execBPMNAssignmentSourceMigrationAsset(ctx, db, "032_bpmn_assignment_source_verify.sql"))

	var legacyID, boundID int
	require.NoError(t, db.QueryRowContext(ctx, `INSERT INTO process_tasks DEFAULT VALUES RETURNING id`).Scan(&legacyID))
	require.NoError(t, db.QueryRowContext(ctx, `INSERT INTO process_tasks (assignee_source) VALUES ('work_item_assignee') RETURNING id`).Scan(&boundID))

	_, err = db.ExecContext(ctx, `UPDATE process_tasks SET assignee_source = 'work_item_assignee' WHERE id = $1`, legacyID)
	require.ErrorContains(t, err, "assignee_source is immutable")
	_, err = db.ExecContext(ctx, `UPDATE process_tasks SET assignee_source = '' WHERE id = $1`, boundID)
	require.ErrorContains(t, err, "assignee_source is immutable")

	_, err = db.ExecContext(ctx, `ALTER TABLE process_tasks ALTER COLUMN assignee_source SET DEFAULT 'work_item_assignee'`)
	require.NoError(t, err)
	err = execBPMNAssignmentSourceMigrationAsset(ctx, db, "032_bpmn_assignment_source_verify.sql")
	require.ErrorContains(t, err, "empty-string default")
}

func openBPMNAssignmentSourceMigrationDB(t *testing.T) *sql.DB {
	t.Helper()
	dsn := os.Getenv("ITSM_TEST_DB")
	require.NotEmpty(t, dsn, "ITSM_TEST_DB is required for PostgreSQL integration tests")
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	adminDB, err := sql.Open("postgres", dsn)
	require.NoError(t, err)
	require.NoError(t, adminDB.PingContext(ctx))
	schemaName := "bpmn_assignment_source_" + strings.ReplaceAll(uuid.NewString(), "-", "_")
	_, err = adminDB.ExecContext(ctx, fmt.Sprintf(`CREATE SCHEMA %q`, schemaName))
	require.NoError(t, err)

	db, err := sql.Open("postgres", bpmnAssignmentSourceDSNWithSearchPath(t, dsn, schemaName))
	require.NoError(t, err)
	db.SetMaxOpenConns(1)
	require.NoError(t, db.PingContext(ctx))
	t.Cleanup(func() {
		require.NoError(t, db.Close())
		_, dropErr := adminDB.ExecContext(context.Background(), fmt.Sprintf(`DROP SCHEMA IF EXISTS %q CASCADE`, schemaName))
		require.NoError(t, dropErr)
		require.NoError(t, adminDB.Close())
	})
	return db
}

func bpmnAssignmentSourceDSNWithSearchPath(t *testing.T, dsn, schemaName string) string {
	t.Helper()
	if strings.HasPrefix(dsn, "postgres://") || strings.HasPrefix(dsn, "postgresql://") {
		parsed, err := url.Parse(dsn)
		require.NoError(t, err)
		query := parsed.Query()
		query.Set("search_path", schemaName)
		parsed.RawQuery = query.Encode()
		return parsed.String()
	}
	return strings.TrimSpace(dsn) + " search_path=" + schemaName
}

func execBPMNAssignmentSourceMigrationAsset(ctx context.Context, db *sql.DB, name string) error {
	contents, err := os.ReadFile("../../migrations/" + name)
	if err != nil {
		return err
	}
	_, err = db.ExecContext(ctx, string(contents))
	return err
}
