//go:build integration

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

	"itsm-backend/config"
	"itsm-backend/database"
	"itsm-backend/internal/bootstrap"
	"itsm-backend/migration"

	"github.com/google/uuid"
	"github.com/lib/pq"
	"github.com/stretchr/testify/require"
	"go.uber.org/zap"
)

const migrationBootstrapIntegrationTimeout = 5 * time.Minute

func TestPostgresProductionFreshReentryAndBaselineAwareUpgrade(t *testing.T) {
	fixture := openDisposableMigrationDatabase(t)
	ctx, cancel := context.WithTimeout(context.Background(), migrationBootstrapIntegrationTimeout)
	defer cancel()
	runtimeRole := fixture.createRole(t, "runtime")
	t.Setenv("ITSM_MIGRATION_DB_USER", fixture.currentUser(t, ctx))
	t.Setenv("ITSM_RUNTIME_DB_USER", runtimeRole)
	t.Setenv("ADMIN_PASSWORD", "Task4-production-fresh-admin-password-2026!")

	previous := database.GetRawDB()
	database.SetRawDBForTest(fixture.target)
	t.Cleanup(func() { database.SetRawDBForTest(previous) })
	cfg := &config.Config{Deployment: config.DeploymentConfig{
		Mode:          "private",
		BootstrapMode: "fresh",
		AutoMigrate:   true,
		AutoSeed:      true,
	}}
	logger := zap.NewNop().Sugar()

	require.NoError(t, bootstrap.InitializeStorage(cfg, nil, logger))
	require.NoError(t, bootstrap.InitializeStorage(cfg, nil, logger), "same-release fresh re-entry must be safe")

	var historyRows int64
	require.NoError(t, fixture.target.QueryRowContext(ctx, `SELECT COUNT(*) FROM schema_migrations`).Scan(&historyRows))
	require.Zero(t, historyRows, "fresh install must not forge historical ledger rows")
	var successfulComponents int64
	require.NoError(t, fixture.target.QueryRowContext(ctx,
		`SELECT COUNT(*) FROM initialization_installations WHERE status = 'succeeded'`,
	).Scan(&successfulComponents))
	require.Equal(t, int64(6), successfulComponents)
	state, err := migration.ReadSchemaState(ctx, fixture.target)
	require.NoError(t, err)
	require.NoError(t, migration.VerifySchemaState(state, migration.CurrentRelease()))

	cfg.Deployment.BootstrapMode = "upgrade"
	cfg.Deployment.AutoSeed = false
	require.NoError(t, bootstrap.InitializeStorage(cfg, nil, logger), "normal upgrade after fresh must not replay covered history")
	require.NoError(t, fixture.target.QueryRowContext(ctx, `SELECT COUNT(*) FROM schema_migrations`).Scan(&historyRows))
	require.Zero(t, historyRows)

	entry, err := migration.CatalogEntryForSchemaState(state)
	require.NoError(t, err)
	available := make([]migration.Migration, 0, len(migration.PostSchemaMigrations())+2)
	for _, item := range migration.PostSchemaMigrations() {
		if item.Version == "026_reconcile_change_execution_tenants" {
			available = append(available, migration.Migration{Version: "023_test_later_publication"})
		}
		available = append(available, item)
	}
	available = append(available, migration.Migration{Version: "029_test_next_release"})
	pending, err := migration.PlanMigrationsByExplicitCoverage(available, nil, entry.CoveredMigrations)
	require.NoError(t, err)
	require.Equal(t, []string{"023_test_later_publication", "029_test_next_release"}, migrationVersions(pending))
}

func TestPostgresFreshRefusesNonemptyTargetBeforeDDL(t *testing.T) {
	fixture := openDisposableMigrationDatabase(t)
	ctx, cancel := context.WithTimeout(context.Background(), migrationBootstrapIntegrationTimeout)
	defer cancel()
	runtimeRole := fixture.createRole(t, "runtime")
	t.Setenv("ITSM_MIGRATION_DB_USER", fixture.currentUser(t, ctx))
	t.Setenv("ITSM_RUNTIME_DB_USER", runtimeRole)
	_, err := fixture.target.ExecContext(ctx, `CREATE TABLE operator_owned_data (id bigint PRIMARY KEY); INSERT INTO operator_owned_data VALUES (7)`)
	require.NoError(t, err)

	previous := database.GetRawDB()
	database.SetRawDBForTest(fixture.target)
	t.Cleanup(func() { database.SetRawDBForTest(previous) })
	err = bootstrap.InitializeStorage(&config.Config{Deployment: config.DeploymentConfig{
		BootstrapMode: "fresh",
		AutoMigrate:   true,
	}}, nil, zap.NewNop().Sugar())
	require.ErrorContains(t, err, "fresh target is neither empty nor a verified current-release phase")

	var rows int64
	require.NoError(t, fixture.target.QueryRowContext(ctx, `SELECT COUNT(*) FROM operator_owned_data`).Scan(&rows))
	require.Equal(t, int64(1), rows)
	var vectorsExists bool
	require.NoError(t, fixture.target.QueryRowContext(ctx, `SELECT to_regclass('vectors') IS NOT NULL`).Scan(&vectorsExists))
	require.False(t, vectorsExists, "fresh refusal must happen before preparation DDL")
}

func TestPostgresFreshRefusesStandaloneSchemaObjectBeforeDDL(t *testing.T) {
	fixture := openDisposableMigrationDatabase(t)
	ctx, cancel := context.WithTimeout(context.Background(), migrationBootstrapIntegrationTimeout)
	defer cancel()
	runtimeRole := fixture.createRole(t, "runtime")
	t.Setenv("ITSM_MIGRATION_DB_USER", fixture.currentUser(t, ctx))
	t.Setenv("ITSM_RUNTIME_DB_USER", runtimeRole)
	_, err := fixture.target.ExecContext(ctx, `CREATE SEQUENCE operator_owned_sequence`)
	require.NoError(t, err)

	previous := database.GetRawDB()
	database.SetRawDBForTest(fixture.target)
	t.Cleanup(func() { database.SetRawDBForTest(previous) })
	err = bootstrap.InitializeStorage(&config.Config{Deployment: config.DeploymentConfig{
		BootstrapMode: "fresh",
		AutoMigrate:   true,
	}}, nil, zap.NewNop().Sugar())
	require.ErrorContains(t, err, "unexpected standalone schema object")

	var sequenceExists, vectorsExists bool
	require.NoError(t, fixture.target.QueryRowContext(ctx, `
		SELECT to_regclass('operator_owned_sequence') IS NOT NULL,
		       to_regclass('vectors') IS NOT NULL
	`).Scan(&sequenceExists, &vectorsExists))
	require.True(t, sequenceExists)
	require.False(t, vectorsExists, "fresh refusal must happen before preparation DDL")
}

func TestPostgresFreshResumesVerifiedCommittedPreparation(t *testing.T) {
	fixture := openDisposableMigrationDatabase(t)
	ctx, cancel := context.WithTimeout(context.Background(), migrationBootstrapIntegrationTimeout)
	defer cancel()
	conn, err := fixture.target.Conn(ctx)
	require.NoError(t, err)
	require.NoError(t, migration.PrepareCurrentInfrastructure(ctx, conn))
	require.NoError(t, conn.Close())

	runFreshWithoutPrivileges(t, ctx, fixture.target)
	state, err := migration.ReadSchemaState(ctx, fixture.target)
	require.NoError(t, err)
	require.NoError(t, migration.VerifySchemaState(state, migration.CurrentRelease()))
}

func TestPostgresUnsupportedLegacyUpgradeFailsBeforeWrites(t *testing.T) {
	fixture := openDisposableMigrationDatabase(t)
	ctx, cancel := context.WithTimeout(context.Background(), migrationBootstrapIntegrationTimeout)
	defer cancel()
	runtimeRole := fixture.createRole(t, "runtime")
	t.Setenv("ITSM_MIGRATION_DB_USER", fixture.currentUser(t, ctx))
	t.Setenv("ITSM_RUNTIME_DB_USER", runtimeRole)
	_, err := fixture.target.ExecContext(ctx, `
		CREATE TABLE ticket_ccs (
			id bigint PRIMARY KEY,
			tenant_id bigint NOT NULL,
			ticket_id bigint NOT NULL,
			user_id bigint NOT NULL,
			is_active boolean NOT NULL DEFAULT true
		);
		CREATE UNIQUE INDEX ticketcc_tenant_id_ticket_id_user_id
			ON ticket_ccs (tenant_id, ticket_id, user_id);
		CREATE TABLE role_permissions (
			id bigint PRIMARY KEY,
			role_id bigint NOT NULL,
			permission_id bigint NOT NULL
		);
	`)
	require.NoError(t, err)
	before := catalogSnapshot(t, ctx, fixture.target)

	previous := database.GetRawDB()
	database.SetRawDBForTest(fixture.target)
	t.Cleanup(func() { database.SetRawDBForTest(previous) })
	err = bootstrap.InitializeStorage(&config.Config{Deployment: config.DeploymentConfig{
		BootstrapMode: "upgrade",
		AutoMigrate:   true,
	}}, nil, zap.NewNop().Sugar())
	require.ErrorContains(t, err, "unsupported upgrade source")
	require.ErrorContains(t, err, "028_schema_release_state")
	require.Equal(t, before, catalogSnapshot(t, ctx, fixture.target), "unsupported preflight must be read-only")

	var tenantColumnExists, predicateExists, schemaStateExists bool
	require.NoError(t, fixture.target.QueryRowContext(ctx, `
		SELECT
			EXISTS (SELECT 1 FROM information_schema.columns WHERE table_schema = current_schema() AND table_name = 'role_permissions' AND column_name = 'tenant_id'),
			EXISTS (
				SELECT 1 FROM pg_index i JOIN pg_class c ON c.oid = i.indexrelid
				WHERE c.relname = 'ticketcc_tenant_id_ticket_id_user_id' AND i.indpred IS NOT NULL
			),
			to_regclass('schema_state') IS NOT NULL
	`).Scan(&tenantColumnExists, &predicateExists, &schemaStateExists))
	require.False(t, tenantColumnExists)
	require.False(t, predicateExists)
	require.False(t, schemaStateExists)
}

func TestPostgresCatalogedUpgradeRejectsLegacyTicketCCAndRolePermissionShapesBeforeWrites(t *testing.T) {
	fixture := openDisposableMigrationDatabase(t)
	ctx, cancel := context.WithTimeout(context.Background(), migrationBootstrapIntegrationTimeout)
	defer cancel()
	runFreshWithoutPrivileges(t, ctx, fixture.target)
	entry, err := migration.CurrentReleaseCatalogEntry()
	require.NoError(t, err)
	require.NoError(t, migration.VerifyCatalogedUpgradeSourceCompatibility(ctx, fixture.target, entry))
	_, err = fixture.target.ExecContext(ctx, `
		DROP INDEX ticketcc_tenant_id_ticket_id_user_id;
		CREATE UNIQUE INDEX ticketcc_tenant_id_ticket_id_user_id
			ON ticket_ccs (tenant_id, ticket_id, user_id);
		ALTER TABLE role_permissions DROP COLUMN tenant_id;
	`)
	require.NoError(t, err)
	require.Error(t, migration.VerifyCatalogedUpgradeSourceCompatibility(ctx, fixture.target, entry))
	before := catalogSnapshot(t, ctx, fixture.target)
	var stateUpdatedAt time.Time
	require.NoError(t, fixture.target.QueryRowContext(ctx, `SELECT updated_at FROM schema_state WHERE id = 1`).Scan(&stateUpdatedAt))

	runtimeRole := fixture.createRole(t, "runtime")
	t.Setenv("ITSM_MIGRATION_DB_USER", fixture.currentUser(t, ctx))
	t.Setenv("ITSM_RUNTIME_DB_USER", runtimeRole)
	previous := database.GetRawDB()
	database.SetRawDBForTest(fixture.target)
	t.Cleanup(func() { database.SetRawDBForTest(previous) })
	err = bootstrap.InitializeStorage(&config.Config{Deployment: config.DeploymentConfig{
		BootstrapMode: "upgrade",
		AutoMigrate:   true,
	}}, nil, zap.NewNop().Sugar())
	require.ErrorContains(t, err, "schema does not match cataloged release 028_schema_release_state")
	require.NotContains(t, err.Error(), "ticket_ccs")
	require.NotContains(t, err.Error(), "role_permissions")
	require.Equal(t, before, catalogSnapshot(t, ctx, fixture.target))

	var afterUpdatedAt time.Time
	require.NoError(t, fixture.target.QueryRowContext(ctx, `SELECT updated_at FROM schema_state WHERE id = 1`).Scan(&afterUpdatedAt))
	require.Equal(t, stateUpdatedAt, afterUpdatedAt, "failed preflight must not promote state")
	var runtimeCanSelect bool
	require.NoError(t, fixture.target.QueryRowContext(ctx,
		`SELECT has_table_privilege($1, 'schema_state', 'SELECT')`, runtimeRole,
	).Scan(&runtimeCanSelect))
	require.False(t, runtimeCanSelect, "failed preflight must not provision privileges")
}

func TestPostgresCurrentSchemaVerifierRejectsEveryFingerprintObjectClassWithoutRepair(t *testing.T) {
	tests := []struct {
		name       string
		corruptSQL string
		stillBad   string
	}{
		{
			name:       "primary key",
			corruptSQL: `ALTER TABLE applications DROP CONSTRAINT applications_pkey CASCADE`,
			stillBad:   `SELECT NOT EXISTS (SELECT 1 FROM pg_constraint WHERE conrelid = 'applications'::regclass AND contype = 'p')`,
		},
		{
			name: "unique index columns and predicate",
			corruptSQL: `
				DROP INDEX ticketcc_tenant_id_ticket_id_user_id;
				CREATE UNIQUE INDEX ticketcc_tenant_id_ticket_id_user_id ON ticket_ccs (tenant_id, ticket_id, user_id)
			`,
			stillBad: `SELECT indpred IS NULL FROM pg_index WHERE indexrelid = 'ticketcc_tenant_id_ticket_id_user_id'::regclass`,
		},
		{
			name: "foreign key reference action",
			corruptSQL: `
				ALTER TABLE applications DROP CONSTRAINT applications_projects_applications;
				ALTER TABLE applications ADD CONSTRAINT applications_projects_applications
					FOREIGN KEY (project_id) REFERENCES projects(id) ON DELETE CASCADE
			`,
			stillBad: `SELECT confdeltype = 'c' FROM pg_constraint WHERE conname = 'applications_projects_applications'`,
		},
		{
			name:       "default",
			corruptSQL: `ALTER TABLE applications ALTER COLUMN type SET DEFAULT 'desktop'`,
			stillBad:   `SELECT column_default LIKE '%desktop%' FROM information_schema.columns WHERE table_schema = current_schema() AND table_name = 'applications' AND column_name = 'type'`,
		},
		{
			name:       "check",
			corruptSQL: `ALTER TABLE initialization_installations DROP CONSTRAINT initialization_installations_scope_type_check`,
			stillBad:   `SELECT COUNT(*) < 2 FROM pg_constraint WHERE conrelid = 'initialization_installations'::regclass AND contype = 'c'`,
		},
		{
			name:       "type",
			corruptSQL: `ALTER TABLE applications ALTER COLUMN name TYPE text`,
			stillBad:   `SELECT data_type = 'text' FROM information_schema.columns WHERE table_schema = current_schema() AND table_name = 'applications' AND column_name = 'name'`,
		},
		{
			name:       "nullability",
			corruptSQL: `ALTER TABLE applications ALTER COLUMN name DROP NOT NULL`,
			stillBad:   `SELECT is_nullable = 'YES' FROM information_schema.columns WHERE table_schema = current_schema() AND table_name = 'applications' AND column_name = 'name'`,
		},
		{
			name:       "identity",
			corruptSQL: `ALTER TABLE applications ALTER COLUMN id DROP IDENTITY`,
			stillBad:   `SELECT identity_generation IS NULL FROM information_schema.columns WHERE table_schema = current_schema() AND table_name = 'applications' AND column_name = 'id'`,
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			fixture := openDisposableMigrationDatabase(t)
			ctx, cancel := context.WithTimeout(context.Background(), migrationBootstrapIntegrationTimeout)
			defer cancel()
			runFreshWithoutPrivileges(t, ctx, fixture.target)
			_, err := fixture.target.ExecContext(ctx, test.corruptSQL)
			require.NoError(t, err)

			err = migration.VerifyCurrentSchema(ctx, fixture.target, migration.CurrentRelease())
			require.Error(t, err)
			var remainsCorrupt bool
			require.NoError(t, fixture.target.QueryRowContext(ctx, test.stillBad).Scan(&remainsCorrupt))
			require.True(t, remainsCorrupt, "verification must never repair drift")
		})
	}
}

func runFreshWithoutPrivileges(t *testing.T, ctx context.Context, db *sql.DB) {
	t.Helper()
	lock, err := migration.NewPostgresAdvisoryLock(db)
	require.NoError(t, err)
	require.NoError(t, migration.RunFreshBootstrap(ctx, migration.FreshBootstrap{
		Lock:    lock,
		Prepare: migration.PrepareCurrentInfrastructure,
		CreateSchema: func(ctx context.Context, conn migration.BootstrapConnection) error {
			client, err := migration.NewEntClientOnConnection(conn)
			if err != nil {
				return err
			}
			defer client.Close()
			return client.Schema.Create(ctx)
		},
		ApplyBaseline: migration.ApplyCurrentBaseline,
		VerifySchema:  migration.VerifyCurrentSchema,
		ApplyPrivileges: func(context.Context, migration.BootstrapConnection, migration.SchemaStateRoles) error {
			return nil
		},
		PromoteState: migration.PromoteSchemaState,
		Seed: func(context.Context, migration.BootstrapConnection) error {
			return nil
		},
		Release: migration.CurrentRelease(),
		Roles:   migration.SchemaStateRoles{MigrationRole: "migration", RuntimeRole: "runtime"},
	}))
}

func migrationVersions(items []migration.Migration) []string {
	versions := make([]string, 0, len(items))
	for _, item := range items {
		versions = append(versions, item.Version)
	}
	return versions
}

func catalogSnapshot(t *testing.T, ctx context.Context, db *sql.DB) string {
	t.Helper()
	var snapshot string
	require.NoError(t, db.QueryRowContext(ctx, `
		SELECT COALESCE(string_agg(definition, E'\n' ORDER BY definition), '')
		FROM (
			SELECT 'table:' || c.relname || ':' || pg_get_userbyid(c.relowner) AS definition
			FROM pg_class c JOIN pg_namespace n ON n.oid = c.relnamespace
			WHERE n.nspname = current_schema() AND c.relkind IN ('r', 'p')
			UNION ALL
			SELECT 'column:' || table_name || ':' || column_name || ':' || data_type || ':' || is_nullable || ':' || COALESCE(column_default, '')
			FROM information_schema.columns WHERE table_schema = current_schema()
			UNION ALL
			SELECT 'index:' || c.relname || ':' || pg_get_indexdef(c.oid)
			FROM pg_class c JOIN pg_namespace n ON n.oid = c.relnamespace
			WHERE n.nspname = current_schema() AND c.relkind = 'i'
		) catalog
	`).Scan(&snapshot))
	return snapshot
}

type disposableMigrationDatabase struct {
	admin     *sql.DB
	target    *sql.DB
	name      string
	roleNames []string
}

func openDisposableMigrationDatabase(t *testing.T) *disposableMigrationDatabase {
	t.Helper()
	adminDSN := strings.TrimSpace(os.Getenv("ITSM_MIGRATION_BASELINE_TEST_DSN"))
	if adminDSN == "" {
		t.Skip("ITSM_MIGRATION_BASELINE_TEST_DSN is not configured for an explicitly disposable PostgreSQL cluster")
	}
	ctx, cancel := context.WithTimeout(context.Background(), migrationBootstrapIntegrationTimeout)
	defer cancel()
	admin, err := sql.Open("postgres", adminDSN)
	require.NoError(t, err)
	require.NoError(t, admin.PingContext(ctx))
	databaseName := "migration_bootstrap_" + strings.ReplaceAll(uuid.NewString(), "-", "")
	_, err = admin.ExecContext(ctx, `CREATE DATABASE `+pq.QuoteIdentifier(databaseName))
	require.NoError(t, err)
	fixture := &disposableMigrationDatabase{admin: admin, name: databaseName}
	t.Cleanup(func() {
		cleanupCtx, cleanupCancel := context.WithTimeout(context.Background(), migrationBootstrapIntegrationTimeout)
		defer cleanupCancel()
		if fixture.target != nil {
			require.NoError(t, fixture.target.Close())
		}
		_, err := admin.ExecContext(cleanupCtx,
			`SELECT pg_terminate_backend(pid) FROM pg_stat_activity WHERE datname = $1`, databaseName)
		require.NoError(t, err)
		_, err = admin.ExecContext(cleanupCtx, `DROP DATABASE IF EXISTS `+pq.QuoteIdentifier(databaseName))
		require.NoError(t, err)
		for _, role := range fixture.roleNames {
			_, err = admin.ExecContext(cleanupCtx, `DROP ROLE IF EXISTS `+pq.QuoteIdentifier(role))
			require.NoError(t, err)
		}
		require.NoError(t, admin.Close())
	})
	target, err := sql.Open("postgres", migrationDatabaseDSN(t, adminDSN, databaseName))
	require.NoError(t, err)
	target.SetMaxOpenConns(4)
	require.NoError(t, target.PingContext(ctx))
	fixture.target = target
	return fixture
}

func (fixture *disposableMigrationDatabase) createRole(t *testing.T, category string) string {
	t.Helper()
	role := fmt.Sprintf("task4_%s_%s", category, strings.ReplaceAll(uuid.NewString(), "-", ""))
	_, err := fixture.admin.Exec(`CREATE ROLE ` + pq.QuoteIdentifier(role) + ` NOLOGIN`)
	require.NoError(t, err)
	fixture.roleNames = append(fixture.roleNames, role)
	return role
}

func (fixture *disposableMigrationDatabase) currentUser(t *testing.T, ctx context.Context) string {
	t.Helper()
	var user string
	require.NoError(t, fixture.target.QueryRowContext(ctx, `SELECT current_user`).Scan(&user))
	return user
}

func migrationDatabaseDSN(t *testing.T, base, databaseName string) string {
	t.Helper()
	if strings.HasPrefix(base, "postgres://") || strings.HasPrefix(base, "postgresql://") {
		parsed, err := url.Parse(base)
		require.NoError(t, err)
		parsed.Path = "/" + databaseName
		return parsed.String()
	}
	return strings.TrimSpace(base) + " dbname=" + databaseName
}
