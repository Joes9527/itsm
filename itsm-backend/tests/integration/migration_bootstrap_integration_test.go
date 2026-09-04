//go:build integration

package integration

import (
	"context"
	"crypto/sha256"
	"database/sql"
	"embed"
	"encoding/hex"
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

//go:embed testdata/migration/source-schema/*.json testdata/migration/transition-schema/*.json
var migrationVerifierFixtures embed.FS

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

func TestPostgresCatalogFingerprintCoversManagedNamespaceInventory(t *testing.T) {
	tests := []struct {
		name   string
		setup  string
		mutate string
	}{
		{
			name:   "collation",
			mutate: `CREATE COLLATION public.task4_extra_collation (provider = libc, locale = 'C')`,
		},
		{
			name:   "extended statistics",
			setup:  `CREATE TABLE public.task4_statistics_probe (one integer, two integer)`,
			mutate: `CREATE STATISTICS public.task4_extra_statistics ON one, two FROM public.task4_statistics_probe`,
		},
		{
			name:   "table reloptions",
			setup:  `CREATE TABLE public.task4_reloptions_probe (id bigint)`,
			mutate: `ALTER TABLE public.task4_reloptions_probe SET (fillfactor = 70)`,
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			fixture := openDisposableMigrationDatabase(t)
			ctx, cancel := context.WithTimeout(context.Background(), migrationBootstrapIntegrationTimeout)
			defer cancel()
			if test.setup != "" {
				_, err := fixture.target.ExecContext(ctx, test.setup)
				require.NoError(t, err)
			}
			before, err := migration.CatalogFingerprint(ctx, fixture.target)
			require.NoError(t, err)
			_, err = fixture.target.ExecContext(ctx, test.mutate)
			require.NoError(t, err)
			after, err := migration.CatalogFingerprint(ctx, fixture.target)
			require.NoError(t, err)
			require.NotEqual(t, before, after, "managed namespace object must change the release fingerprint")
		})
	}
}

func TestPostgresCatalogFingerprintIgnoresExplicitlyUnmanagedSchemaObjects(t *testing.T) {
	fixture := openDisposableMigrationDatabase(t)
	ctx, cancel := context.WithTimeout(context.Background(), migrationBootstrapIntegrationTimeout)
	defer cancel()
	before, err := migration.CatalogFingerprint(ctx, fixture.target)
	require.NoError(t, err)
	_, err = fixture.target.ExecContext(ctx, `
		CREATE SCHEMA operator_unmanaged;
		CREATE TABLE operator_unmanaged.notes (id bigint PRIMARY KEY);
		CREATE COLLATION operator_unmanaged.extra_collation (provider = libc, locale = 'C')
	`)
	require.NoError(t, err)
	after, err := migration.CatalogFingerprint(ctx, fixture.target)
	require.NoError(t, err)
	require.Equal(t, before, after, "only public is a release-managed schema")
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

func TestPostgresFreshResumesEveryVerifiedCommittedSchemaPhase(t *testing.T) {
	for _, test := range []struct {
		name  string
		stage func(context.Context, migration.BootstrapConnection) error
	}{
		{
			name: "Ent schema",
			stage: func(ctx context.Context, conn migration.BootstrapConnection) error {
				if err := migration.PrepareCurrentInfrastructure(ctx, conn); err != nil {
					return err
				}
				return migration.CreateCurrentEntSchema(ctx, conn)
			},
		},
		{
			name: "baseline before promotion",
			stage: func(ctx context.Context, conn migration.BootstrapConnection) error {
				if err := migration.PrepareCurrentInfrastructure(ctx, conn); err != nil {
					return err
				}
				if err := migration.CreateCurrentEntSchema(ctx, conn); err != nil {
					return err
				}
				return migration.ApplyCurrentBaseline(ctx, conn)
			},
		},
	} {
		t.Run(test.name, func(t *testing.T) {
			fixture := openDisposableMigrationDatabase(t)
			ctx, cancel := context.WithTimeout(context.Background(), migrationBootstrapIntegrationTimeout)
			defer cancel()
			lock, err := migration.NewPostgresAdvisoryLock(fixture.target)
			require.NoError(t, err)
			require.NoError(t, lock.WithLock(ctx, func(conn migration.BootstrapConnection) error {
				return test.stage(ctx, conn)
			}))

			runFreshWithoutPrivileges(t, ctx, fixture.target)
			state, err := migration.ReadSchemaState(ctx, fixture.target)
			require.NoError(t, err)
			require.NoError(t, migration.VerifySchemaState(state, migration.CurrentRelease()))
		})
	}
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
		{
			name:       "extra managed collation",
			corruptSQL: `CREATE COLLATION public.operator_extra_collation (provider = libc, locale = 'C')`,
		},
		{
			name:       "extra managed extended statistics",
			corruptSQL: `CREATE STATISTICS public.operator_extra_user_statistics ON id, tenant_id FROM public.users`,
		},
		{
			name:       "managed table reloptions drift",
			corruptSQL: `ALTER TABLE public.users SET (fillfactor = 70)`,
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

func TestPostgresCatalogedTransitionRecoversCommittedSchemaChangeWithoutReplay(t *testing.T) {
	fixture := openDisposableMigrationDatabase(t)
	ctx, cancel := context.WithTimeout(context.Background(), migrationBootstrapIntegrationTimeout)
	defer cancel()
	runFreshWithoutPrivileges(t, ctx, fixture.target)
	stateBefore, err := migration.ReadSchemaState(ctx, fixture.target)
	require.NoError(t, err)
	sourceEntry, err := migration.CurrentReleaseCatalogEntry()
	require.NoError(t, err)

	const transitionSQL = `ALTER TABLE applications ADD COLUMN task4_transition_marker text NOT NULL DEFAULT 'ready'`
	fixtureMigration := migration.CatalogedMigration{
		Migration: migration.Migration{Version: "task4_fixture_add_transition_marker", Description: "add transition marker"},
		SQL:       transitionSQL,
	}
	available := make([]migration.CatalogedMigration, 0, len(migration.PostSchemaMigrations())+1)
	for _, item := range migration.PostSchemaMigrations() {
		available = append(available, migration.CatalogedMigration{Migration: item, SQL: migration.GetMigrationSQL(item.Version)})
	}
	available = append(available, fixtureMigration)

	targetSource := readVerifierFixture(t, "source-schema/task4_fixture_release_v2.json")
	transition := readVerifierFixture(t, "transition-schema/028--task4_fixture_v2.json")
	registry, err := migration.EmbeddedSchemaVerifierRegistry()
	require.NoError(t, err)
	registry, err = registry.WithAssets([]migration.VerifierAssetFile{targetSource, transition})
	require.NoError(t, err)
	targetRelease := migration.ReleaseManifest{
		ReleaseID:            "task4-fixture-release-v2",
		SchemaVersion:        "task4_fixture_schema_v2",
		BaselineVersion:      "task4-fixture-baseline-v2",
		EntSchemaFingerprint: strings.Repeat("d", 64),
		Assets: []migration.ReleaseAsset{
			{Name: targetSource.Name, SHA256: verifierFixtureChecksum(targetSource)},
			{Name: transition.Name, SHA256: verifierFixtureChecksum(transition)},
			{Name: fixtureMigration.Migration.Version, SHA256: verifierFixtureSQLChecksum(transitionSQL)},
		},
		SeedComponents: []migration.SeedComponent{{Name: "task4-fixture", Version: "v2"}},
	}
	targetChecksum, err := targetRelease.Checksum()
	require.NoError(t, err)
	targetEntry := migration.ReleaseCatalogEntry{
		ReleaseID:             targetRelease.ReleaseID,
		SchemaVersion:         targetRelease.SchemaVersion,
		BaselineVersion:       targetRelease.BaselineVersion,
		ReleaseManifestSHA256: targetChecksum,
		BaselineAsset:         migration.ReleaseAsset{Name: "task4-fixture-baseline", SHA256: strings.Repeat("e", 64)},
		SourceSchemaAsset:     migration.ReleaseAsset{Name: targetSource.Name, SHA256: verifierFixtureChecksum(targetSource)},
		TransitionAssets:      []migration.ReleaseAsset{{Name: transition.Name, SHA256: verifierFixtureChecksum(transition)}},
		CoveredMigrations:     append(append([]string(nil), sourceEntry.CoveredMigrations...), fixtureMigration.Migration.Version),
	}
	catalog, err := migration.NewUpgradeReleaseCatalog(
		[]migration.ReleaseCatalogEntry{sourceEntry, targetEntry}, registry, available,
	)
	require.NoError(t, err)

	lock, err := migration.NewPostgresAdvisoryLock(fixture.target)
	require.NoError(t, err)
	injected := errors.New("injected interruption after committed schema-changing migration")
	err = lock.WithLock(ctx, func(conn migration.BootstrapConnection) error {
		migrator := migration.NewMigratorOnConnection(conn, zap.NewNop().Sugar())
		pending, err := migrator.PlanCatalogedUpgrade(ctx, targetRelease, catalog)
		if err != nil {
			return err
		}
		require.Len(t, pending, 1)
		require.Equal(t, fixtureMigration.Migration.Version, pending[0].Version())
		if err := migrator.ApplyCatalogedMigration(ctx, pending[0]); err != nil {
			return err
		}
		return injected
	})
	require.ErrorIs(t, err, injected)

	stateAfterInterruption, err := migration.ReadSchemaState(ctx, fixture.target)
	require.NoError(t, err)
	require.Equal(t, stateBefore.UpdatedAt, stateAfterInterruption.UpdatedAt, "interruption must not promote schema state")
	require.Equal(t, sourceEntry.ReleaseID, stateAfterInterruption.ReleaseID)
	var committedRows int64
	require.NoError(t, fixture.target.QueryRowContext(ctx, `
		SELECT COUNT(*) FROM schema_migrations WHERE version = 'task4_fixture_add_transition_marker'
	`).Scan(&committedRows))
	require.Equal(t, int64(1), committedRows)
	var markerExists bool
	require.NoError(t, fixture.target.QueryRowContext(ctx, `
		SELECT EXISTS (
			SELECT 1 FROM information_schema.columns
			WHERE table_schema = current_schema() AND table_name = 'applications'
			  AND column_name = 'task4_transition_marker'
		)
	`).Scan(&markerExists))
	require.True(t, markerExists, "fixture migration must make a real catalog change")

	secondLock, err := migration.NewPostgresAdvisoryLock(fixture.target)
	require.NoError(t, err)
	err = secondLock.WithLock(ctx, func(conn migration.BootstrapConnection) error {
		migrator := migration.NewMigratorOnConnection(conn, zap.NewNop().Sugar())
		pending, err := migrator.PlanCatalogedUpgrade(ctx, targetRelease, catalog)
		if err != nil {
			return err
		}
		require.Empty(t, pending, "verified committed prefix must not replay")
		return migration.PromoteSchemaState(ctx, conn, targetRelease)
	})
	require.NoError(t, err)
	require.NoError(t, fixture.target.QueryRowContext(ctx, `
		SELECT COUNT(*) FROM schema_migrations WHERE version = 'task4_fixture_add_transition_marker'
	`).Scan(&committedRows))
	require.Equal(t, int64(1), committedRows)
	stateAfterRecovery, err := migration.ReadSchemaState(ctx, fixture.target)
	require.NoError(t, err)
	require.NoError(t, migration.VerifySchemaState(stateAfterRecovery, targetRelease))
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
		Lock:            lock,
		Prepare:         migration.PrepareCurrentInfrastructure,
		CreateSchema:    migration.CreateCurrentEntSchema,
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
		Lock:          lock,
		Prepare:       migration.PrepareCurrentInfrastructure,
		CreateSchema:  migration.CreateCurrentEntSchema,
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

func readVerifierFixture(t *testing.T, logicalName string) migration.VerifierAssetFile {
	t.Helper()
	content, err := migrationVerifierFixtures.ReadFile("testdata/migration/" + logicalName)
	require.NoError(t, err)
	return migration.VerifierAssetFile{Name: logicalName, Content: content}
}

func verifierFixtureChecksum(file migration.VerifierAssetFile) string {
	sum := sha256.Sum256(file.Content)
	return hex.EncodeToString(sum[:])
}

func verifierFixtureSQLChecksum(value string) string {
	sum := sha256.Sum256([]byte(value))
	return hex.EncodeToString(sum[:])
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
