//go:build integration

package integration

import (
	"context"
	"database/sql"
	"errors"
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

func TestPostgresLowLevelFreshReentryAndBaselineAwareUpgrade(t *testing.T) {
	fixture := openDisposableMigrationDatabase(t)
	ctx, cancel := context.WithTimeout(context.Background(), migrationBootstrapIntegrationTimeout)
	defer cancel()

	runFreshWithoutPrivileges(t, ctx, fixture.target)
	runFreshWithoutPrivileges(t, ctx, fixture.target)

	var historyRows int64
	require.NoError(t, fixture.target.QueryRowContext(ctx, `SELECT COUNT(*) FROM schema_migrations`).Scan(&historyRows))
	require.Zero(t, historyRows, "fresh install must not forge historical ledger rows")
	state, err := migration.ReadSchemaState(ctx, fixture.target)
	require.NoError(t, err)
	require.NoError(t, migration.VerifySchemaState(state, migration.CurrentRelease()))

	pendingCurrent, err := migration.NewMigrator(fixture.target, zap.NewNop().Sugar()).
		PlanCurrentUpgrade(ctx, migration.CurrentRelease())
	require.NoError(t, err)
	require.Empty(t, pendingCurrent, "normal upgrade after fresh must not replay covered history")
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

func TestPostgresProductionInitializationGateRefusesBeforeSchemaWrites(t *testing.T) {
	fixture := openDisposableMigrationDatabase(t)
	ctx, cancel := context.WithTimeout(context.Background(), migrationBootstrapIntegrationTimeout)
	defer cancel()
	runtimeRole := fixture.createRole(t, "runtime")
	t.Setenv("ITSM_MIGRATION_DB_USER", fixture.currentUser(t, ctx))
	t.Setenv("ITSM_RUNTIME_DB_USER", runtimeRole)

	previous := database.GetRawDB()
	database.SetRawDBForTest(fixture.target)
	t.Cleanup(func() { database.SetRawDBForTest(previous) })
	before := catalogSnapshot(t, ctx, fixture.target)
	err := bootstrap.InitializeStorage(&config.Config{Deployment: config.DeploymentConfig{
		BootstrapMode: "fresh",
		AutoMigrate:   true,
	}}, nil, zap.NewNop().Sugar())
	require.ErrorContains(t, err, "release publication gate is closed")
	require.Equal(t, before, catalogSnapshot(t, ctx, fixture.target))
	var vectorsExists, vectorExtensionExists bool
	require.NoError(t, fixture.target.QueryRowContext(ctx, `
		SELECT to_regclass('vectors') IS NOT NULL,
		       EXISTS (SELECT 1 FROM pg_extension WHERE extname = 'vector')
	`).Scan(&vectorsExists, &vectorExtensionExists))
	require.False(t, vectorsExists)
	require.False(t, vectorExtensionExists)
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

	err = migration.RunFreshBootstrap(ctx, freshBootstrapForTest(t, fixture.target))
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

	err = migration.RunFreshBootstrap(ctx, freshBootstrapForTest(t, fixture.target))
	require.ErrorContains(t, err, "fresh target catalog does not match verified empty phase")

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

	_, err = migration.NewMigrator(fixture.target, zap.NewNop().Sugar()).
		PlanCurrentUpgrade(ctx, migration.CurrentRelease())
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
	require.NoError(t, migration.VerifyCatalogedUpgradeSourceSchema(ctx, fixture.target, entry))
	_, err = fixture.target.ExecContext(ctx, `
		DROP INDEX ticketcc_tenant_id_ticket_id_user_id;
		CREATE UNIQUE INDEX ticketcc_tenant_id_ticket_id_user_id
			ON ticket_ccs (tenant_id, ticket_id, user_id);
		ALTER TABLE role_permissions DROP COLUMN tenant_id;
	`)
	require.NoError(t, err)
	require.Error(t, migration.VerifyCatalogedUpgradeSourceSchema(ctx, fixture.target, entry))
	before := catalogSnapshot(t, ctx, fixture.target)
	var stateUpdatedAt time.Time
	require.NoError(t, fixture.target.QueryRowContext(ctx, `SELECT updated_at FROM schema_state WHERE id = 1`).Scan(&stateUpdatedAt))

	_, err = migration.NewMigrator(fixture.target, zap.NewNop().Sugar()).
		PlanCurrentUpgrade(ctx, migration.CurrentRelease())
	require.ErrorContains(t, err, "schema does not match cataloged release 028_schema_release_state")
	require.NotContains(t, err.Error(), "ticket_ccs")
	require.NotContains(t, err.Error(), "role_permissions")
	require.Equal(t, before, catalogSnapshot(t, ctx, fixture.target))

	var afterUpdatedAt time.Time
	require.NoError(t, fixture.target.QueryRowContext(ctx, `SELECT updated_at FROM schema_state WHERE id = 1`).Scan(&afterUpdatedAt))
	require.Equal(t, stateUpdatedAt, afterUpdatedAt, "failed preflight must not promote state")
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

func TestPostgresCurrentSchemaVerifierRejectsEquivalentLookingUnsafePredicates(t *testing.T) {
	tests := []struct {
		name       string
		corruptSQL string
	}{
		{
			name: "RLS policy widened with OR true",
			corruptSQL: `
				DROP POLICY tenant_isolation_users ON users;
				CREATE POLICY tenant_isolation_users ON users AS PERMISSIVE FOR ALL TO PUBLIC
				USING ((tenant_id = NULLIF(current_setting('app.current_tenant', true), '')::bigint) OR true)
				WITH CHECK ((tenant_id = NULLIF(current_setting('app.current_tenant', true), '')::bigint) OR true)
			`,
		},
		{
			name: "running index predicate inverted",
			corruptSQL: `
				DROP INDEX idx_process_instances_running_unique;
				CREATE UNIQUE INDEX idx_process_instances_running_unique
				ON process_instances (tenant_id, business_key)
				WHERE status <> 'running' AND business_key IS NOT NULL AND business_key <> ''
			`,
		},
		{
			name:       "RLS activation state changed",
			corruptSQL: `ALTER TABLE users ENABLE ROW LEVEL SECURITY; ALTER TABLE users FORCE ROW LEVEL SECURITY`,
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
			require.Error(t, err, "version-pinned predicates must be compared exactly")
		})
	}
}

func TestPostgresCatalogedSourceFingerprintRejectsUnrelatedDriftBeforePlanning(t *testing.T) {
	tests := []struct {
		name       string
		corruptSQL string
	}{
		{
			name:       "extra raw table unique index",
			corruptSQL: `CREATE UNIQUE INDEX operator_extra_initialization_run ON initialization_runs (id)`,
		},
		{
			name: "extra raw table foreign key",
			corruptSQL: `
				ALTER TABLE change_approval_chains
				ADD CONSTRAINT operator_extra_approver_fk
				FOREIGN KEY (approver_id) REFERENCES users(id)
			`,
		},
		{
			name:       "unrelated Ent column default",
			corruptSQL: `ALTER TABLE applications ALTER COLUMN description SET DEFAULT 'operator-default'`,
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			fixture := openDisposableMigrationDatabase(t)
			ctx, cancel := context.WithTimeout(context.Background(), migrationBootstrapIntegrationTimeout)
			defer cancel()
			runFreshWithoutPrivileges(t, ctx, fixture.target)
			entry, err := migration.CurrentReleaseCatalogEntry()
			require.NoError(t, err)
			require.NoError(t, migration.VerifyCatalogedUpgradeSourceSchema(ctx, fixture.target, entry))
			_, err = fixture.target.ExecContext(ctx, test.corruptSQL)
			require.NoError(t, err)
			before := catalogSnapshot(t, ctx, fixture.target)

			err = migration.VerifyCatalogedUpgradeSourceSchema(ctx, fixture.target, entry)
			require.ErrorContains(t, err, "schema does not match cataloged release 028_schema_release_state")
			_, err = migration.NewMigrator(fixture.target, zap.NewNop().Sugar()).
				PlanCurrentUpgrade(ctx, migration.CurrentRelease())
			require.ErrorContains(t, err, "schema does not match cataloged release 028_schema_release_state")
			require.Equal(t, before, catalogSnapshot(t, ctx, fixture.target), "source preflight must be read-only")
		})
	}
}

func TestPostgresCatalogedSourceFingerprintRunsInReadOnlyTransaction(t *testing.T) {
	fixture := openDisposableMigrationDatabase(t)
	ctx, cancel := context.WithTimeout(context.Background(), migrationBootstrapIntegrationTimeout)
	defer cancel()
	runFreshWithoutPrivileges(t, ctx, fixture.target)
	entry, err := migration.CurrentReleaseCatalogEntry()
	require.NoError(t, err)
	tx, err := fixture.target.BeginTx(ctx, &sql.TxOptions{ReadOnly: true})
	require.NoError(t, err)
	defer tx.Rollback()
	require.NoError(t, migration.VerifyCatalogedUpgradeSourceSchema(ctx, tx, entry))
	require.NoError(t, tx.Commit())
}

func TestPostgresFreshReentryRejectsArbitraryExtensionMemberBeforeDDL(t *testing.T) {
	fixture := openDisposableMigrationDatabase(t)
	ctx, cancel := context.WithTimeout(context.Background(), migrationBootstrapIntegrationTimeout)
	defer cancel()
	runFreshWithoutPrivileges(t, ctx, fixture.target)

	_, err := fixture.target.ExecContext(ctx, `
		CREATE FUNCTION operator_extension_probe(integer) RETURNS integer
		LANGUAGE SQL IMMUTABLE AS 'SELECT $1';
		ALTER EXTENSION vector ADD FUNCTION operator_extension_probe(integer);
	`)
	require.NoError(t, err)
	before := catalogSnapshot(t, ctx, fixture.target)

	err = migration.RunFreshBootstrap(ctx, freshBootstrapForTest(t, fixture.target))
	require.ErrorContains(t, err, "fresh target catalog does not match verified current release phase")
	require.Equal(t, before, catalogSnapshot(t, ctx, fixture.target), "fresh re-entry refusal must precede DDL")
}

func TestPostgresUpgradeRestartDoesNotReplayCommittedForwardMigration(t *testing.T) {
	fixture := openDisposableMigrationDatabase(t)
	ctx, cancel := context.WithTimeout(context.Background(), migrationBootstrapIntegrationTimeout)
	defer cancel()
	runFreshWithoutPrivileges(t, ctx, fixture.target)
	stateBefore, err := migration.ReadSchemaState(ctx, fixture.target)
	require.NoError(t, err)
	entry, err := migration.CurrentReleaseCatalogEntry()
	require.NoError(t, err)

	// Model the first migration after a fresh baseline with a real published
	// transaction: for this test-only planning view 028 is outside coverage.
	// The immutable catalog entry itself remains unchanged.
	testCoverage := make([]string, 0, len(entry.CoveredMigrations)-1)
	for _, version := range entry.CoveredMigrations {
		if version != "028_schema_release_state" {
			testCoverage = append(testCoverage, version)
		}
	}
	pending, err := migration.PlanMigrationsByExplicitCoverage(
		migration.PostSchemaMigrations(), nil, testCoverage,
	)
	require.NoError(t, err)
	require.Equal(t, []string{"028_schema_release_state"}, migrationVersions(pending))

	lock, err := migration.NewPostgresAdvisoryLock(fixture.target)
	require.NoError(t, err)
	injected := errors.New("injected interruption after committed forward migration")
	promotions := 0
	err = migration.RunUpgrade(ctx, migration.UpgradeBootstrap{
		Lock: lock,
		PlanForwardMigrations: func(context.Context, migration.BootstrapConnection) ([]migration.Migration, error) {
			return pending, nil
		},
		ApplyForwardMigrations: func(ctx context.Context, conn migration.BootstrapConnection, items []migration.Migration) error {
			migrator := migration.NewMigratorOnConnection(conn, zap.NewNop().Sugar())
			for _, item := range items {
				if err := migrator.ApplyMigration(ctx, item); err != nil {
					return err
				}
			}
			return nil
		},
		VerifySchema: func(context.Context, migration.DBTX, migration.ReleaseManifest) error {
			return injected
		},
		ApplyPrivileges: func(context.Context, migration.BootstrapConnection, migration.SchemaStateRoles) error {
			return nil
		},
		PromoteState: func(context.Context, migration.DBTX, migration.ReleaseManifest) error {
			promotions++
			return nil
		},
		Release: migration.CurrentRelease(),
		Roles:   migration.SchemaStateRoles{MigrationRole: "migration", RuntimeRole: "runtime"},
	})
	require.ErrorIs(t, err, injected)
	require.Zero(t, promotions)

	stateAfterInterruption, err := migration.ReadSchemaState(ctx, fixture.target)
	require.NoError(t, err)
	require.Equal(t, stateBefore.UpdatedAt, stateAfterInterruption.UpdatedAt, "interruption must not promote schema state")
	var committedRows int64
	require.NoError(t, fixture.target.QueryRowContext(ctx, `
		SELECT COUNT(*) FROM schema_migrations WHERE version = '028_schema_release_state'
	`).Scan(&committedRows))
	require.Equal(t, int64(1), committedRows)

	secondLock, err := migration.NewPostgresAdvisoryLock(fixture.target)
	require.NoError(t, err)
	appliedOnRestart := 0
	err = migration.RunUpgrade(ctx, migration.UpgradeBootstrap{
		Lock: secondLock,
		PlanForwardMigrations: func(ctx context.Context, conn migration.BootstrapConnection) ([]migration.Migration, error) {
			return migration.NewMigratorOnConnection(conn, zap.NewNop().Sugar()).
				PlanCurrentUpgrade(ctx, migration.CurrentRelease())
		},
		ApplyForwardMigrations: func(context.Context, migration.BootstrapConnection, []migration.Migration) error {
			appliedOnRestart++
			return nil
		},
		VerifySchema: migration.VerifyCurrentSchema,
		ApplyPrivileges: func(context.Context, migration.BootstrapConnection, migration.SchemaStateRoles) error {
			return nil
		},
		PromoteState: func(ctx context.Context, db migration.DBTX, release migration.ReleaseManifest) error {
			promotions++
			return migration.PromoteSchemaState(ctx, db, release)
		},
		Release: migration.CurrentRelease(),
		Roles:   migration.SchemaStateRoles{MigrationRole: "migration", RuntimeRole: "runtime"},
	})
	require.NoError(t, err)
	require.Zero(t, appliedOnRestart, "committed forward migration must not be replayed")
	require.Equal(t, 1, promotions)
	require.NoError(t, fixture.target.QueryRowContext(ctx, `
		SELECT COUNT(*) FROM schema_migrations WHERE version = '028_schema_release_state'
	`).Scan(&committedRows))
	require.Equal(t, int64(1), committedRows)
}

func TestPostgresFreshRLSOffSupportsIndependentRuntimeRoleWithoutTenantGUC(t *testing.T) {
	fixture := openDisposableMigrationDatabase(t)
	ctx, cancel := context.WithTimeout(context.Background(), migrationBootstrapIntegrationTimeout)
	defer cancel()
	runtimeRole := fixture.createRole(t, "runtime")
	migrationRole := fixture.currentUser(t, ctx)

	lock, err := migration.NewPostgresAdvisoryLock(fixture.target)
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
		ApplyBaseline:   migration.ApplyCurrentBaseline,
		VerifySchema:    migration.VerifyCurrentSchema,
		ApplyPrivileges: migration.ApplySchemaStatePrivilegesOnConnection,
		PromoteState:    migration.PromoteSchemaState,
		Seed:            func(context.Context, migration.BootstrapConnection) error { return nil },
		Release:         migration.CurrentRelease(),
		Roles: migration.SchemaStateRoles{
			MigrationRole: migrationRole,
			RuntimeRole:   runtimeRole,
		},
	}))

	var activeRLS, forcedRLS, policyCount int64
	require.NoError(t, fixture.target.QueryRowContext(ctx, `
		SELECT COUNT(*) FILTER (WHERE relrowsecurity),
		       COUNT(*) FILTER (WHERE relforcerowsecurity),
		       (SELECT COUNT(*) FROM pg_policy WHERE polrelid IN
		          ('users'::regclass, 'roles'::regclass,
		           'kaf_task_action_ledgers'::regclass,
		           'kaf_task_completion_receipts'::regclass))
		FROM pg_class
		WHERE oid IN ('users'::regclass, 'roles'::regclass,
		              'kaf_task_action_ledgers'::regclass,
		              'kaf_task_completion_receipts'::regclass)
	`).Scan(&activeRLS, &forcedRLS, &policyCount))
	require.Zero(t, activeRLS, "RLS_MODE=off baseline must keep runtime tables disabled")
	require.Zero(t, forcedRLS, "RLS_MODE=off baseline must keep runtime tables unforced")
	require.Equal(t, int64(4), policyCount, "RLS_MODE=off must retain the canonical policies for later activation")

	_, err = fixture.target.ExecContext(ctx, fmt.Sprintf(`
		GRANT USAGE ON SCHEMA public TO %s;
		GRANT SELECT ON users, roles TO %s;
		GRANT SELECT, INSERT, UPDATE, DELETE ON kaf_task_action_ledgers, kaf_task_completion_receipts TO %s;
		GRANT USAGE, SELECT ON ALL SEQUENCES IN SCHEMA public TO %s;
	`, pq.QuoteIdentifier(runtimeRole), pq.QuoteIdentifier(runtimeRole),
		pq.QuoteIdentifier(runtimeRole), pq.QuoteIdentifier(runtimeRole)))
	require.NoError(t, err)

	conn, err := fixture.target.Conn(ctx)
	require.NoError(t, err)
	defer conn.Close()
	_, err = conn.ExecContext(ctx, `SET ROLE `+pq.QuoteIdentifier(runtimeRole))
	require.NoError(t, err)
	defer conn.ExecContext(context.Background(), `RESET ROLE`)

	var currentUser string
	var bypassRLS bool
	require.NoError(t, conn.QueryRowContext(ctx, `
		SELECT current_user, rolbypassrls FROM pg_roles WHERE rolname = current_user
	`).Scan(&currentUser, &bypassRLS))
	require.Equal(t, runtimeRole, currentUser)
	require.False(t, bypassRLS, "runtime role must never receive BYPASSRLS")
	var tenantGUC sql.NullString
	require.NoError(t, conn.QueryRowContext(ctx, `SELECT current_setting('app.current_tenant', true)`).Scan(&tenantGUC))
	require.False(t, tenantGUC.Valid, "the production runtime path does not establish a tenant GUC")
	for _, table := range []string{"users", "roles"} {
		var count int64
		require.NoError(t, conn.QueryRowContext(ctx, `SELECT COUNT(*) FROM `+pq.QuoteIdentifier(table)).Scan(&count))
	}

	var ledgerID int64
	require.NoError(t, conn.QueryRowContext(ctx, `
		INSERT INTO kaf_task_action_ledgers
			(tenant_id, task_id, run_id, step_id, action, idempotency_key,
			 correlation_id, procedure_ref, procedure_version, created_at, updated_at)
		VALUES (41, 'task', 'run', 'step', 'complete', 'idempotency',
		        'correlation', 'procedure', 'v1', now(), now())
		RETURNING id
	`).Scan(&ledgerID))
	_, err = conn.ExecContext(ctx, `
		INSERT INTO kaf_task_completion_receipts
			(ledger_id, tenant_id, task_id, created_at, updated_at)
		VALUES ($1, 41, 'task', now(), now())
	`, ledgerID)
	require.NoError(t, err)
	_, err = conn.ExecContext(ctx, `UPDATE kaf_task_action_ledgers SET result_status = 'succeeded' WHERE id = $1`, ledgerID)
	require.NoError(t, err)
	_, err = conn.ExecContext(ctx, `UPDATE schema_state SET release_id = 'forbidden' WHERE id = 1`)
	requirePostgresPermissionDenied(t, err)
	_, err = conn.ExecContext(ctx, `CREATE TABLE runtime_schema_write_forbidden (id bigint)`)
	requirePostgresPermissionDenied(t, err)
}

func requirePostgresPermissionDenied(t *testing.T, err error) {
	t.Helper()
	require.Error(t, err)
	var postgresError *pq.Error
	require.ErrorAs(t, err, &postgresError)
	require.Equal(t, pq.ErrorCode("42501"), postgresError.Code)
}

func runFreshWithoutPrivileges(t *testing.T, ctx context.Context, db *sql.DB) {
	t.Helper()
	require.NoError(t, migration.RunFreshBootstrap(ctx, freshBootstrapForTest(t, db)))
}

func freshBootstrapForTest(t *testing.T, db *sql.DB) migration.FreshBootstrap {
	t.Helper()
	lock, err := migration.NewPostgresAdvisoryLock(db)
	require.NoError(t, err)
	return migration.FreshBootstrap{
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
	}
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
