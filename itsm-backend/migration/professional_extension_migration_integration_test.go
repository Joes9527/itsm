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
	for tableName, removedColumns := range map[string][]string{
		"incidents": {"reporter_id", "assignee_id", "category", "subcategory", "source", "tenant_id", "version", "created_at", "updated_at", "resolved_at", "closed_at", "deleted_at"},
		"problems":  {"category", "assignee_id", "created_by", "tenant_id", "created_at", "updated_at", "resolved_at", "closed_at", "deleted_at"},
		"changes":   {"assignee_id", "created_by", "tenant_id", "related_tickets", "created_at", "updated_at"},
	} {
		for _, columnName := range removedColumns {
			var count int
			require.NoError(t, db.QueryRowContext(ctx, `
				SELECT COUNT(*) FROM information_schema.columns
				WHERE table_schema = current_schema() AND table_name = $1 AND column_name = $2
			`, tableName, columnName).Scan(&count))
			require.Zero(t, count, "%s.%s must be physically removed", tableName, columnName)
		}
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
			fmt.Sprintf(`INSERT INTO %s (work_item_id) VALUES ($1)`, testCase.table),
			testCase.workItemID,
		)
		requirePostgreSQLUniqueViolation(t, err)
	}

	for _, tableName := range []string{"incidents", "problems", "changes"} {
		_, err = db.ExecContext(ctx, fmt.Sprintf(`INSERT INTO %s (work_item_id) VALUES (999999)`, tableName))
		requirePostgreSQLForeignKeyViolation(t, err)
	}
}

func TestProfessionalExtensionMigrationUpgradesLegacyDirectChangePolicy(t *testing.T) {
	db := openProfessionalExtensionMigrationDB(t)
	ctx, cancel := context.WithTimeout(context.Background(), professionalExtensionMigrationIntegrationTimeout)
	defer cancel()

	_, err := db.ExecContext(ctx, `
		INSERT INTO tickets (id, tenant_id, record_class, deleted_at) VALUES
			(1, 101, 'change_request', NULL),
			(2, 202, 'change_request', NULL),
			(3, 101, 'change_request', NOW());
		INSERT INTO changes (work_item_id, tenant_id) VALUES (1, 101), (3, 101);
		ALTER TABLE changes ENABLE ROW LEVEL SECURITY;
		ALTER TABLE changes FORCE ROW LEVEL SECURITY;
		CREATE POLICY tenant_isolation ON changes
			USING (tenant_id = NULLIF(current_setting('app.current_tenant', true), '')::bigint)
			WITH CHECK (tenant_id = NULLIF(current_setting('app.current_tenant', true), '')::bigint);
	`)
	require.NoError(t, err)

	_, err = db.ExecContext(ctx, GetMigrationSQL("022_drop_professional_extension_shared_fields"))
	require.NoError(t, err)
	verifySQL := readProfessionalExtensionMigrationAsset(t, "20260901_drop_professional_extension_shared_fields_verify.sql")
	_, err = db.ExecContext(ctx, verifySQL)
	require.NoError(t, err)

	var usingExpression, checkExpression string
	require.NoError(t, db.QueryRowContext(ctx, `
		SELECT pg_get_expr(policy.polqual, policy.polrelid), pg_get_expr(policy.polwithcheck, policy.polrelid)
		FROM pg_policy policy
		JOIN pg_class relation ON relation.oid = policy.polrelid
		JOIN pg_namespace namespace ON namespace.oid = relation.relnamespace
		WHERE namespace.nspname = current_schema()
		  AND relation.relname = 'changes'
		  AND policy.polname = 'tenant_isolation_changes'
	`).Scan(&usingExpression, &checkExpression))
	for _, expression := range []string{usingExpression, checkExpression} {
		require.Contains(t, expression, "work_item.tenant_id")
		require.Contains(t, expression, "work_item.deleted_at IS NULL")
		require.Contains(t, expression, "app.current_tenant")
		require.NotContains(t, expression, "app.current_tenant_id")
		require.NotContains(t, expression, "changes.tenant_id")
	}
	var policyCount int
	require.NoError(t, db.QueryRowContext(ctx, `
		SELECT COUNT(*)
		FROM pg_policy policy
		JOIN pg_class relation ON relation.oid = policy.polrelid
		JOIN pg_namespace namespace ON namespace.oid = relation.relnamespace
		WHERE namespace.nspname = current_schema() AND relation.relname = 'changes'
	`).Scan(&policyCount))
	require.Equal(t, 1, policyCount)

	var schemaName string
	require.NoError(t, db.QueryRowContext(ctx, `SELECT current_schema()`).Scan(&schemaName))
	roleName := "professional_extension_rls_" + strings.ReplaceAll(uuid.NewString(), "-", "_")
	_, err = db.ExecContext(ctx, fmt.Sprintf(`
		CREATE ROLE %q NOLOGIN;
		GRANT USAGE ON SCHEMA %q TO %q;
		GRANT SELECT ON %q.tickets, %q.changes TO %q;
		GRANT INSERT ON %q.changes TO %q;
	`, roleName, schemaName, roleName, schemaName, schemaName, roleName, schemaName, roleName))
	require.NoError(t, err)
	t.Cleanup(func() {
		_, dropErr := db.ExecContext(context.Background(), fmt.Sprintf(`DROP OWNED BY %q; DROP ROLE IF EXISTS %q`, roleName, roleName))
		require.NoError(t, dropErr)
	})

	tx, err := db.BeginTx(ctx, nil)
	require.NoError(t, err)
	_, err = tx.ExecContext(ctx, fmt.Sprintf(`SET LOCAL ROLE %q`, roleName))
	require.NoError(t, err)
	_, err = tx.ExecContext(ctx, `SET LOCAL app.current_tenant = '101'`)
	require.NoError(t, err)
	var visibleWorkItems []byte
	require.NoError(t, tx.QueryRowContext(ctx, `SELECT COALESCE(json_agg(work_item_id ORDER BY work_item_id), '[]') FROM changes`).Scan(&visibleWorkItems))
	require.JSONEq(t, `[1]`, string(visibleWorkItems), "same-tenant soft-deleted WorkItem must be hidden")
	require.NoError(t, tx.Commit())

	tx, err = db.BeginTx(ctx, nil)
	require.NoError(t, err)
	_, err = tx.ExecContext(ctx, fmt.Sprintf(`SET LOCAL ROLE %q`, roleName))
	require.NoError(t, err)
	_, err = tx.ExecContext(ctx, `SET LOCAL app.current_tenant = '101'`)
	require.NoError(t, err)
	_, err = tx.ExecContext(ctx, `INSERT INTO changes (work_item_id) VALUES (2)`)
	require.Error(t, err, "cross-tenant WorkItem association must be rejected by WITH CHECK")
	require.NoError(t, tx.Rollback())
}

func TestProfessionalExtensionMigrationRejectsConflictingNamedForeignKey(t *testing.T) {
	db := openProfessionalExtensionMigrationDB(t)
	ctx, cancel := context.WithTimeout(context.Background(), professionalExtensionMigrationIntegrationTimeout)
	defer cancel()

	_, err := db.ExecContext(ctx, `
		CREATE TABLE wrong_work_items (id BIGINT PRIMARY KEY);
		ALTER TABLE incidents ADD CONSTRAINT incidents_tickets_work_item
			FOREIGN KEY (work_item_id) REFERENCES wrong_work_items(id);
	`)
	require.NoError(t, err)

	_, err = db.ExecContext(ctx, GetMigrationSQL("022_drop_professional_extension_shared_fields"))
	require.ErrorContains(t, err, "conflicts with required incidents.work_item_id foreign key")
}

func TestProfessionalExtensionMigrationRejectsAdditionalForeignKey(t *testing.T) {
	db := openProfessionalExtensionMigrationDB(t)
	ctx, cancel := context.WithTimeout(context.Background(), professionalExtensionMigrationIntegrationTimeout)
	defer cancel()

	_, err := db.ExecContext(ctx, `
		CREATE TABLE wrong_work_items (id BIGINT PRIMARY KEY);
		ALTER TABLE incidents ADD CONSTRAINT incidents_tickets_work_item
			FOREIGN KEY (work_item_id) REFERENCES tickets(id);
		ALTER TABLE incidents ADD CONSTRAINT incidents_wrong_work_item
			FOREIGN KEY (work_item_id) REFERENCES wrong_work_items(id);
	`)
	require.NoError(t, err)

	_, err = db.ExecContext(ctx, GetMigrationSQL("022_drop_professional_extension_shared_fields"))
	require.ErrorContains(t, err, "additional foreign key")
}

func TestProfessionalExtensionVerificationRejectsAdditionalForeignKey(t *testing.T) {
	db := openProfessionalExtensionMigrationDB(t)
	ctx, cancel := context.WithTimeout(context.Background(), professionalExtensionMigrationIntegrationTimeout)
	defer cancel()

	_, err := db.ExecContext(ctx, GetMigrationSQL("022_drop_professional_extension_shared_fields"))
	require.NoError(t, err)
	_, err = db.ExecContext(ctx, `
		CREATE TABLE wrong_work_items (id BIGINT PRIMARY KEY);
		ALTER TABLE incidents ADD CONSTRAINT incidents_wrong_work_item
			FOREIGN KEY (work_item_id) REFERENCES wrong_work_items(id);
	`)
	require.NoError(t, err)

	verifySQL := readProfessionalExtensionMigrationAsset(t, "20260901_drop_professional_extension_shared_fields_verify.sql")
	_, err = db.ExecContext(ctx, verifySQL)
	require.ErrorContains(t, err, "exactly one foreign key")
}

func TestProfessionalExtensionResetEstablishesExactForeignKeys(t *testing.T) {
	db := openProfessionalExtensionMigrationDB(t)
	ctx, cancel := context.WithTimeout(context.Background(), professionalExtensionMigrationIntegrationTimeout)
	defer cancel()

	resetSQL := readProfessionalExtensionMigrationAsset(t, "20260901_drop_professional_extension_shared_fields_dev_reset.sql")
	verifySQL := readProfessionalExtensionMigrationAsset(t, "20260901_drop_professional_extension_shared_fields_verify.sql")
	_, err := db.ExecContext(ctx, resetSQL)
	require.NoError(t, err)
	_, err = db.ExecContext(ctx, verifySQL)
	require.NoError(t, err)
	for _, tableName := range []string{"incidents", "problems", "changes"} {
		_, err = db.ExecContext(ctx, fmt.Sprintf(`INSERT INTO %s (work_item_id) VALUES (999999)`, tableName))
		requirePostgreSQLForeignKeyViolation(t, err)
	}
}

func TestProfessionalExtensionResetRejectsAdditionalForeignKey(t *testing.T) {
	db := openProfessionalExtensionMigrationDB(t)
	ctx, cancel := context.WithTimeout(context.Background(), professionalExtensionMigrationIntegrationTimeout)
	defer cancel()

	_, err := db.ExecContext(ctx, `
		CREATE TABLE wrong_work_items (id BIGINT PRIMARY KEY);
		ALTER TABLE incidents ADD CONSTRAINT incidents_tickets_work_item
			FOREIGN KEY (work_item_id) REFERENCES tickets(id);
		ALTER TABLE incidents ADD CONSTRAINT incidents_wrong_work_item
			FOREIGN KEY (work_item_id) REFERENCES wrong_work_items(id);
	`)
	require.NoError(t, err)

	resetSQL := readProfessionalExtensionMigrationAsset(t, "20260901_drop_professional_extension_shared_fields_dev_reset.sql")
	_, err = db.ExecContext(ctx, resetSQL)
	require.ErrorContains(t, err, "additional foreign key")
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
		CREATE INDEX incident_work_item_id ON incidents (id);
	`)
	require.NoError(t, err)

	verifySQL := readProfessionalExtensionMigrationAsset(t, "20260901_drop_professional_extension_shared_fields_verify.sql")
	_, err = db.ExecContext(ctx, verifySQL)
	require.ErrorContains(t, err, "must be a ready, valid, one-column unique index")
}

func TestProfessionalExtensionVerificationRejectsUnvalidatedForeignKey(t *testing.T) {
	db := openProfessionalExtensionMigrationDB(t)
	ctx, cancel := context.WithTimeout(context.Background(), professionalExtensionMigrationIntegrationTimeout)
	defer cancel()

	_, err := db.ExecContext(ctx, GetMigrationSQL("022_drop_professional_extension_shared_fields"))
	require.NoError(t, err)
	_, err = db.ExecContext(ctx, `
		ALTER TABLE incidents DROP CONSTRAINT incidents_tickets_work_item;
		ALTER TABLE incidents ADD CONSTRAINT incidents_tickets_work_item
			FOREIGN KEY (work_item_id) REFERENCES tickets(id) NOT VALID;
	`)
	require.NoError(t, err)

	verifySQL := readProfessionalExtensionMigrationAsset(t, "20260901_drop_professional_extension_shared_fields_verify.sql")
	_, err = db.ExecContext(ctx, verifySQL)
	require.ErrorContains(t, err, "must be an exact validated foreign key")
}

func TestProfessionalExtensionVerificationRejectsPermissivePolicyShape(t *testing.T) {
	db := openProfessionalExtensionMigrationDB(t)
	ctx, cancel := context.WithTimeout(context.Background(), professionalExtensionMigrationIntegrationTimeout)
	defer cancel()

	_, err := db.ExecContext(ctx, GetMigrationSQL("022_drop_professional_extension_shared_fields"))
	require.NoError(t, err)
	_, err = db.ExecContext(ctx, `
		DROP POLICY tenant_isolation_changes ON changes;
		CREATE POLICY tenant_isolation_changes ON changes USING (true) WITH CHECK (true);
	`)
	require.NoError(t, err)

	verifySQL := readProfessionalExtensionMigrationAsset(t, "20260901_drop_professional_extension_shared_fields_verify.sql")
	_, err = db.ExecContext(ctx, verifySQL)
	require.ErrorContains(t, err, "must use authoritative WorkItem tenant and soft-delete scope")
}

func TestProfessionalExtensionVerificationRejectsCanonicalPolicyWithPermissiveBranch(t *testing.T) {
	db := openProfessionalExtensionMigrationDB(t)
	ctx, cancel := context.WithTimeout(context.Background(), professionalExtensionMigrationIntegrationTimeout)
	defer cancel()

	_, err := db.ExecContext(ctx, GetMigrationSQL("022_drop_professional_extension_shared_fields"))
	require.NoError(t, err)
	_, err = db.ExecContext(ctx, `
		DROP POLICY tenant_isolation_changes ON changes;
		CREATE POLICY tenant_isolation_changes ON changes
			USING ((EXISTS (
				SELECT 1 FROM tickets work_item
				WHERE work_item.id = changes.work_item_id
				  AND work_item.tenant_id = NULLIF(current_setting('app.current_tenant', true), '')::bigint
				  AND work_item.deleted_at IS NULL
			)) OR true)
			WITH CHECK ((EXISTS (
				SELECT 1 FROM tickets work_item
				WHERE work_item.id = changes.work_item_id
				  AND work_item.tenant_id = NULLIF(current_setting('app.current_tenant', true), '')::bigint
				  AND work_item.deleted_at IS NULL
			)) OR true);
	`)
	require.NoError(t, err)

	verifySQL := readProfessionalExtensionMigrationAsset(t, "20260901_drop_professional_extension_shared_fields_verify.sql")
	_, err = db.ExecContext(ctx, verifySQL)
	require.ErrorContains(t, err, "must use authoritative WorkItem tenant and soft-delete scope exactly")
}

func TestProfessionalExtensionVerificationRejectsAdditionalPolicy(t *testing.T) {
	db := openProfessionalExtensionMigrationDB(t)
	ctx, cancel := context.WithTimeout(context.Background(), professionalExtensionMigrationIntegrationTimeout)
	defer cancel()

	_, err := db.ExecContext(ctx, GetMigrationSQL("022_drop_professional_extension_shared_fields"))
	require.NoError(t, err)
	_, err = db.ExecContext(ctx, `CREATE POLICY tenant_isolation_extra ON changes USING (true) WITH CHECK (true)`)
	require.NoError(t, err)

	verifySQL := readProfessionalExtensionMigrationAsset(t, "20260901_drop_professional_extension_shared_fields_verify.sql")
	_, err = db.ExecContext(ctx, verifySQL)
	require.ErrorContains(t, err, "exactly one canonical RLS policy")
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
			record_class TEXT NOT NULL,
			deleted_at TIMESTAMPTZ
		);
		CREATE TABLE incidents (
			id BIGSERIAL PRIMARY KEY,
			title TEXT, description TEXT, status TEXT, priority TEXT,
			reporter_id BIGINT, assignee_id BIGINT, category TEXT, subcategory TEXT,
			source TEXT, version BIGINT, created_at TIMESTAMPTZ, updated_at TIMESTAMPTZ,
			resolved_at TIMESTAMPTZ, closed_at TIMESTAMPTZ, deleted_at TIMESTAMPTZ,
			work_item_id BIGINT NOT NULL,
			tenant_id BIGINT NOT NULL
		);
		CREATE TABLE problems (
			id BIGSERIAL PRIMARY KEY,
			title TEXT, description TEXT, status TEXT, priority TEXT, category TEXT,
			assignee_id BIGINT, created_by BIGINT, tenant_id BIGINT NOT NULL,
			created_at TIMESTAMPTZ, updated_at TIMESTAMPTZ, resolved_at TIMESTAMPTZ,
			closed_at TIMESTAMPTZ, deleted_at TIMESTAMPTZ,
			work_item_id BIGINT NOT NULL
		);
		CREATE TABLE changes (
			id BIGSERIAL PRIMARY KEY,
			title TEXT, description TEXT, status TEXT, priority TEXT, assignee_id BIGINT,
			created_by BIGINT, tenant_id BIGINT NOT NULL, related_tickets JSONB,
			created_at TIMESTAMPTZ, updated_at TIMESTAMPTZ,
			work_item_id BIGINT NOT NULL
		);
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

func requirePostgreSQLForeignKeyViolation(t *testing.T, err error) {
	t.Helper()
	require.Error(t, err)
	var pqErr *pq.Error
	require.ErrorAs(t, err, &pqErr)
	require.Equal(t, pq.ErrorCode("23503"), pqErr.Code)
}
