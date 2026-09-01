//go:build integration

package migration

import (
	"context"
	"database/sql"
	"fmt"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/lib/pq"
	"github.com/stretchr/testify/require"
)

const professionalExtensionMigrationIntegrationTimeout = 2 * time.Minute

func TestProfessionalExtensionMigrationRejectsDuplicateWorkItemOwnership(t *testing.T) {
	db := openProfessionalExtensionMigrationDB(t)
	ctx, cancel := context.WithTimeout(context.Background(), professionalExtensionMigrationIntegrationTimeout)
	defer cancel()

	_, err := db.ExecContext(ctx, `
		INSERT INTO tickets (id, tenant_id, record_class) VALUES (1, 101, 'incident');
		INSERT INTO incidents (work_item_id, tenant_id) VALUES (1, 101), (1, 101);
	`)
	require.NoError(t, err)

	_, err = db.ExecContext(ctx, GetMigrationSQL("022_drop_professional_extension_shared_fields"))
	require.ErrorContains(t, err, "duplicate work_item_id")
}

func TestProfessionalExtensionMigrationEnforcesOneToOneAndAssetLifecycle(t *testing.T) {
	db := openProfessionalExtensionMigrationDB(t)
	ctx, cancel := context.WithTimeout(context.Background(), professionalExtensionMigrationIntegrationTimeout)
	defer cancel()

	_, err := db.ExecContext(ctx, `
		INSERT INTO tickets (id, tenant_id, record_class) VALUES
			(1, 101, 'incident'), (2, 101, 'problem'), (3, 101, 'change_request');
		INSERT INTO incidents (work_item_id, tenant_id) VALUES (1, 101);
		INSERT INTO problems (work_item_id, tenant_id) VALUES (2, 101);
		INSERT INTO changes (work_item_id, tenant_id) VALUES (3, 101);
	`)
	require.NoError(t, err)

	applySQL := readProfessionalExtensionMigrationAsset(t, "20260901_drop_professional_extension_shared_fields.sql")
	resetSQL := readProfessionalExtensionMigrationAsset(t, "20260901_drop_professional_extension_shared_fields_dev_reset.sql")
	verifySQL := readProfessionalExtensionMigrationAsset(t, "20260901_drop_professional_extension_shared_fields_verify.sql")
	require.Equal(t, strings.TrimSpace(GetMigrationSQL("022_drop_professional_extension_shared_fields")), strings.TrimSpace(applySQL))
	decoySchema := "professional_extension_decoy_" + strings.ReplaceAll(uuid.NewString(), "-", "_")
	_, err = db.ExecContext(ctx, fmt.Sprintf(`
		CREATE SCHEMA "%s";
		CREATE TABLE "%s".incidents (title TEXT, work_item_id BIGINT);
	`, decoySchema, decoySchema))
	require.NoError(t, err)
	t.Cleanup(func() {
		_, _ = db.ExecContext(context.Background(), fmt.Sprintf(`DROP SCHEMA IF EXISTS "%s" CASCADE`, decoySchema))
	})

	for _, statement := range []string{applySQL, verifySQL, applySQL, verifySQL, resetSQL, verifySQL} {
		_, err = db.ExecContext(ctx, statement)
		require.NoError(t, err)
	}
	var decoyTitleColumns int
	require.NoError(t, db.QueryRowContext(ctx, `
		SELECT COUNT(*) FROM information_schema.columns
		WHERE table_schema = $1 AND table_name = 'incidents' AND column_name = 'title'
	`, decoySchema).Scan(&decoyTitleColumns))
	require.Equal(t, 1, decoyTitleColumns, "development reset must not mutate another schema")

	for _, testCase := range []struct {
		table      string
		workItemID int
	}{
		{table: "incidents", workItemID: 1},
		{table: "problems", workItemID: 2},
		{table: "changes", workItemID: 3},
	} {
		_, err = db.ExecContext(ctx,
			fmt.Sprintf(`INSERT INTO %s (work_item_id, tenant_id) VALUES ($1, 101)`, testCase.table),
			testCase.workItemID,
		)
		requirePostgreSQLUniqueViolation(t, err)
	}
}

func TestProfessionalExtensionMigrationRejectsConflictingNamedIndex(t *testing.T) {
	db := openProfessionalExtensionMigrationDB(t)
	ctx, cancel := context.WithTimeout(context.Background(), professionalExtensionMigrationIntegrationTimeout)
	defer cancel()

	_, err := db.ExecContext(ctx, `
		DROP INDEX incident_work_item_id;
		CREATE INDEX incident_work_item_id ON incidents (tenant_id);
	`)
	require.NoError(t, err)

	_, err = db.ExecContext(ctx, GetMigrationSQL("022_drop_professional_extension_shared_fields"))
	require.ErrorContains(t, err, "conflicts with required incidents.work_item_id index")
}

func TestProfessionalExtensionVerificationRejectsWrongNonUniqueIndex(t *testing.T) {
	db := openProfessionalExtensionMigrationDB(t)
	ctx, cancel := context.WithTimeout(context.Background(), professionalExtensionMigrationIntegrationTimeout)
	defer cancel()

	_, err := db.ExecContext(ctx, GetMigrationSQL("022_drop_professional_extension_shared_fields"))
	require.NoError(t, err)
	_, err = db.ExecContext(ctx, `
		DROP INDEX incident_work_item_id;
		CREATE INDEX incident_work_item_id ON incidents (tenant_id);
	`)
	require.NoError(t, err)

	verifySQL := readProfessionalExtensionMigrationAsset(t, "20260901_drop_professional_extension_shared_fields_verify.sql")
	_, err = db.ExecContext(ctx, verifySQL)
	require.ErrorContains(t, err, "must be a ready, valid, one-column unique index")
}

func openProfessionalExtensionMigrationDB(t *testing.T) *sql.DB {
	t.Helper()
	dsn := os.Getenv("ITSM_TEST_DB")
	require.NotEmpty(t, dsn, "ITSM_TEST_DB is required for PostgreSQL integration tests")
	ctx, cancel := context.WithTimeout(context.Background(), professionalExtensionMigrationIntegrationTimeout)
	defer cancel()

	adminDB, err := sql.Open("postgres", dsn)
	require.NoError(t, err)
	require.NoError(t, adminDB.PingContext(ctx))
	schemaName := "professional_extension_" + strings.ReplaceAll(uuid.NewString(), "-", "_")
	_, err = adminDB.ExecContext(ctx, fmt.Sprintf(`CREATE SCHEMA "%s"`, schemaName))
	require.NoError(t, err)

	db, err := sql.Open("postgres", professionalExtensionDSNWithSearchPath(t, dsn, schemaName))
	require.NoError(t, err)
	db.SetMaxOpenConns(1)
	require.NoError(t, db.PingContext(ctx))
	_, err = db.ExecContext(ctx, `
		CREATE TABLE tickets (
			id BIGINT PRIMARY KEY,
			tenant_id BIGINT NOT NULL,
			record_class TEXT NOT NULL
		);
		CREATE TABLE incidents (
			id BIGSERIAL PRIMARY KEY,
			title TEXT, description TEXT, status TEXT, priority TEXT,
			work_item_id BIGINT NOT NULL REFERENCES tickets(id),
			tenant_id BIGINT NOT NULL
		);
		CREATE TABLE problems (LIKE incidents INCLUDING ALL);
		ALTER TABLE problems ADD CONSTRAINT problems_work_item_fk FOREIGN KEY (work_item_id) REFERENCES tickets(id);
		CREATE TABLE changes (LIKE incidents INCLUDING ALL);
		ALTER TABLE changes ADD CONSTRAINT changes_work_item_fk FOREIGN KEY (work_item_id) REFERENCES tickets(id);
		CREATE INDEX incident_work_item_id ON incidents (work_item_id);
		CREATE INDEX problem_work_item_id ON problems (work_item_id);
		CREATE INDEX change_work_item_id ON changes (work_item_id);
	`)
	require.NoError(t, err)

	t.Cleanup(func() {
		cleanupCtx, cleanupCancel := context.WithTimeout(context.Background(), professionalExtensionMigrationIntegrationTimeout)
		defer cleanupCancel()
		require.NoError(t, db.Close())
		_, dropErr := adminDB.ExecContext(cleanupCtx, fmt.Sprintf(`DROP SCHEMA IF EXISTS "%s" CASCADE`, schemaName))
		require.NoError(t, dropErr)
		require.NoError(t, adminDB.Close())
	})
	return db
}

func professionalExtensionDSNWithSearchPath(t *testing.T, dsn, schemaName string) string {
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

func readProfessionalExtensionMigrationAsset(t *testing.T, name string) string {
	t.Helper()
	contents, err := os.ReadFile(filepath.Join("..", "migrations", name))
	require.NoError(t, err)
	return string(contents)
}

func requirePostgreSQLUniqueViolation(t *testing.T, err error) {
	t.Helper()
	require.Error(t, err)
	var pqErr *pq.Error
	require.ErrorAs(t, err, &pqErr)
	require.Equal(t, pq.ErrorCode("23505"), pqErr.Code)
}
