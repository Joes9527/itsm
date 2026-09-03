//go:build integration

package migration

import (
	"context"
	"database/sql"
	"fmt"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/lib/pq"
	"github.com/stretchr/testify/require"
)

const serviceCatalogTargetClassMigrationIntegrationTimeout = 2 * time.Minute

// TestServiceCatalogTargetClassMigrationBackfillsAndDropsITSMType proves, against a real
// PostgreSQL instance, that migration 024_service_catalog_target_class_authority:
//  1. backfills target_class from itsm_type for legacy rows (Incident/Change/Request mapping),
//  2. leaves already-correct target_class values untouched,
//  3. enforces NOT NULL and the three-value CHECK constraint afterward, and
//  4. drops the retired itsm_type column.
func TestServiceCatalogTargetClassMigrationBackfillsAndDropsITSMType(t *testing.T) {
	db := openServiceCatalogTargetClassMigrationDB(t)
	ctx, cancel := context.WithTimeout(context.Background(), serviceCatalogTargetClassMigrationIntegrationTimeout)
	defer cancel()

	_, err := db.ExecContext(ctx, `
		INSERT INTO service_catalogs (id, tenant_id, name, itsm_type, target_class) VALUES
			(1, 101, 'legacy request catalog', 'Request', NULL),
			(2, 101, 'legacy incident catalog', 'Incident', NULL),
			(3, 101, 'legacy change catalog', 'Change', NULL),
			(4, 101, 'already migrated catalog', 'Request', 'incident');
	`)
	require.NoError(t, err)

	applySQL := string(readUnifiedIntakeMigrationAsset(t, "024_service_catalog_target_class_authority.sql"))
	require.Equal(t,
		normalizeMigrationSQL(GetMigrationSQL("024_service_catalog_target_class_authority")),
		normalizeMigrationSQL(applySQL),
	)

	_, err = db.ExecContext(ctx, applySQL)
	require.NoError(t, err)

	rows, err := db.QueryContext(ctx, `SELECT id, target_class FROM service_catalogs ORDER BY id`)
	require.NoError(t, err)
	defer rows.Close()

	got := map[int]string{}
	for rows.Next() {
		var id int
		var targetClass string
		require.NoError(t, rows.Scan(&id, &targetClass))
		got[id] = targetClass
	}
	require.NoError(t, rows.Err())

	require.Equal(t, "service_request_item", got[1], "itsm_type=Request must backfill to service_request_item")
	require.Equal(t, "incident", got[2], "itsm_type=Incident must backfill to incident")
	require.Equal(t, "change_request", got[3], "itsm_type=Change must backfill to change_request")
	require.Equal(t, "incident", got[4], "an already-migrated row's target_class must be left untouched, not re-derived from itsm_type")

	// itsm_type must be gone.
	var itsmTypeColumnCount int
	require.NoError(t, db.QueryRowContext(ctx, `
		SELECT count(*) FROM information_schema.columns
		WHERE table_schema = current_schema() AND table_name = 'service_catalogs' AND column_name = 'itsm_type'
	`).Scan(&itsmTypeColumnCount))
	require.Equal(t, 0, itsmTypeColumnCount, "itsm_type column must be dropped")

	// target_class must now be NOT NULL.
	_, err = db.ExecContext(ctx, `INSERT INTO service_catalogs (id, tenant_id, name, target_class) VALUES (5, 101, 'null target class', NULL)`)
	requireServiceCatalogNotNullViolation(t, err)

	// target_class must be constrained to the three allowed values.
	_, err = db.ExecContext(ctx, `INSERT INTO service_catalogs (id, tenant_id, name, target_class) VALUES (6, 101, 'bogus target class', 'bogus_class')`)
	requireServiceCatalogCheckViolation(t, err)

	verifySQL := string(readUnifiedIntakeMigrationAsset(t, "024_service_catalog_target_class_authority_verify.sql"))
	_, err = db.ExecContext(ctx, verifySQL)
	require.NoError(t, err, "verify script must pass once the migration has fully applied")

	// Idempotent re-apply: running the same apply SQL again must not error and must not change
	// already-correct values.
	_, err = db.ExecContext(ctx, applySQL)
	require.NoError(t, err)
	var reapplied string
	require.NoError(t, db.QueryRowContext(ctx, `SELECT target_class FROM service_catalogs WHERE id = 4`).Scan(&reapplied))
	require.Equal(t, "incident", reapplied)
}

// TestServiceCatalogTargetClassMigrationFreshSchemaIsNoOp proves the apply SQL is safe against a
// schema where service_catalogs was already created in its final Task 14 shape (target_class NOT
// NULL, itsm_type never existed) — the scenario Ent's Schema.Create produces on a brand new
// database once ent/schema/servicecatalog.go no longer declares itsm_type. The migration must not
// error just because the legacy column is absent.
func TestServiceCatalogTargetClassMigrationFreshSchemaIsNoOp(t *testing.T) {
	db := openServiceCatalogTargetClassMigrationDBWithoutITSMType(t)
	ctx, cancel := context.WithTimeout(context.Background(), serviceCatalogTargetClassMigrationIntegrationTimeout)
	defer cancel()

	_, err := db.ExecContext(ctx, `
		INSERT INTO service_catalogs (id, tenant_id, name, target_class) VALUES (1, 101, 'fresh catalog', 'service_request_item');
	`)
	require.NoError(t, err)

	applySQL := string(readUnifiedIntakeMigrationAsset(t, "024_service_catalog_target_class_authority.sql"))
	_, err = db.ExecContext(ctx, applySQL)
	require.NoError(t, err, "apply SQL must be a safe no-op when itsm_type never existed")

	var targetClass string
	require.NoError(t, db.QueryRowContext(ctx, `SELECT target_class FROM service_catalogs WHERE id = 1`).Scan(&targetClass))
	require.Equal(t, "service_request_item", targetClass)

	verifySQL := string(readUnifiedIntakeMigrationAsset(t, "024_service_catalog_target_class_authority_verify.sql"))
	_, err = db.ExecContext(ctx, verifySQL)
	require.NoError(t, err)
}

// TestServiceCatalogTargetClassMigrationDevResetRoundTrips proves the dev_reset script reverses
// the apply script (itsm_type re-added, target_class nullable again) and that verify then
// correctly fails again — matching the round-trip contract already established for 023/025.
func TestServiceCatalogTargetClassMigrationDevResetRoundTrips(t *testing.T) {
	db := openServiceCatalogTargetClassMigrationDB(t)
	ctx, cancel := context.WithTimeout(context.Background(), serviceCatalogTargetClassMigrationIntegrationTimeout)
	defer cancel()

	_, err := db.ExecContext(ctx, `
		INSERT INTO service_catalogs (id, tenant_id, name, itsm_type, target_class) VALUES
			(1, 101, 'legacy request catalog', 'Request', NULL);
	`)
	require.NoError(t, err)

	applySQL := string(readUnifiedIntakeMigrationAsset(t, "024_service_catalog_target_class_authority.sql"))
	_, err = db.ExecContext(ctx, applySQL)
	require.NoError(t, err)

	verifySQL := string(readUnifiedIntakeMigrationAsset(t, "024_service_catalog_target_class_authority_verify.sql"))
	_, err = db.ExecContext(ctx, verifySQL)
	require.NoError(t, err)

	resetSQL := string(readUnifiedIntakeMigrationAsset(t, "024_service_catalog_target_class_authority_dev_reset.sql"))
	_, err = db.ExecContext(ctx, resetSQL)
	require.ErrorContains(t, err, "reset requires an empty service_catalogs table")

	_, err = db.ExecContext(ctx, `DELETE FROM service_catalogs`)
	require.NoError(t, err)

	_, err = db.ExecContext(ctx, resetSQL)
	require.NoError(t, err)

	var itsmTypeColumnCount int
	require.NoError(t, db.QueryRowContext(ctx, `
		SELECT count(*) FROM information_schema.columns
		WHERE table_schema = current_schema() AND table_name = 'service_catalogs' AND column_name = 'itsm_type'
	`).Scan(&itsmTypeColumnCount))
	require.Equal(t, 1, itsmTypeColumnCount, "dev_reset must re-add itsm_type")

	_, err = db.ExecContext(ctx, verifySQL)
	require.ErrorContains(t, err, "itsm_type must be dropped", "verify must fail again once reset has reintroduced itsm_type")
}

func openServiceCatalogTargetClassMigrationDB(t *testing.T) *sql.DB {
	t.Helper()
	db, _ := openServiceCatalogTargetClassMigrationSchema(t, true)
	return db
}

func openServiceCatalogTargetClassMigrationDBWithoutITSMType(t *testing.T) *sql.DB {
	t.Helper()
	db, _ := openServiceCatalogTargetClassMigrationSchema(t, false)
	return db
}

func openServiceCatalogTargetClassMigrationSchema(t *testing.T, withITSMType bool) (*sql.DB, string) {
	t.Helper()
	dsn := requireServiceCatalogTargetClassTestDSN(t)
	ctx, cancel := context.WithTimeout(context.Background(), serviceCatalogTargetClassMigrationIntegrationTimeout)
	defer cancel()

	adminDB, err := sql.Open("postgres", dsn)
	require.NoError(t, err)
	require.NoError(t, adminDB.PingContext(ctx))
	schemaName := "service_catalog_target_class_" + strings.ReplaceAll(uuid.NewString(), "-", "_")
	_, err = adminDB.ExecContext(ctx, fmt.Sprintf(`CREATE SCHEMA "%s"`, schemaName))
	require.NoError(t, err)

	db, err := sql.Open("postgres", professionalExtensionDSNWithSearchPath(t, dsn, schemaName))
	require.NoError(t, err)
	db.SetMaxOpenConns(1)
	require.NoError(t, db.PingContext(ctx))

	if withITSMType {
		_, err = db.ExecContext(ctx, `
			CREATE TABLE service_catalogs (
				id BIGINT PRIMARY KEY,
				tenant_id BIGINT NOT NULL,
				name TEXT NOT NULL,
				itsm_type character varying NOT NULL DEFAULT 'Request',
				target_class character varying
			);
		`)
	} else {
		_, err = db.ExecContext(ctx, `
			CREATE TABLE service_catalogs (
				id BIGINT PRIMARY KEY,
				tenant_id BIGINT NOT NULL,
				name TEXT NOT NULL,
				target_class character varying NOT NULL
			);
		`)
	}
	require.NoError(t, err)

	t.Cleanup(func() {
		cleanupCtx, cleanupCancel := context.WithTimeout(context.Background(), serviceCatalogTargetClassMigrationIntegrationTimeout)
		defer cleanupCancel()
		_, _ = adminDB.ExecContext(cleanupCtx, fmt.Sprintf(`DROP SCHEMA IF EXISTS "%s" CASCADE`, schemaName))
		_ = db.Close()
		_ = adminDB.Close()
	})

	return db, schemaName
}

func requireServiceCatalogTargetClassTestDSN(t *testing.T) string {
	t.Helper()
	dsn := os.Getenv("ITSM_TEST_DB")
	require.NotEmpty(t, dsn, "ITSM_TEST_DB is required for PostgreSQL integration tests")
	return dsn
}

func requireServiceCatalogNotNullViolation(t *testing.T, err error) {
	t.Helper()
	require.Error(t, err)
	var pqErr *pq.Error
	require.ErrorAs(t, err, &pqErr)
	require.Equal(t, pq.ErrorCode("23502"), pqErr.Code)
}

func requireServiceCatalogCheckViolation(t *testing.T, err error) {
	t.Helper()
	require.Error(t, err)
	var pqErr *pq.Error
	require.ErrorAs(t, err, &pqErr)
	require.Equal(t, pq.ErrorCode("23514"), pqErr.Code)
}
