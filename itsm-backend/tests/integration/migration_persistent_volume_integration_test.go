//go:build integration

package integration

import (
	"context"
	"database/sql"
	"fmt"
	"os/exec"
	"strings"
	"testing"
	"time"

	"itsm-backend/migration"

	"github.com/google/uuid"
	"github.com/lib/pq"
	"github.com/stretchr/testify/require"
	"go.uber.org/zap"
)

const task4PinnedPostgresImage = "pgvector/pgvector:pg17@sha256:7ae6051efd0e60444282c27c7e141af07f322ce033300e727a49c3dd11075e38"

type task4CatalogTransitionFixture struct {
	target  migration.ReleaseManifest
	catalog *migration.UpgradeReleaseCatalog
	forward migration.CatalogedMigration
}

// TestPostgresPersistentVolumeSurvivesFreshAndTwoReleaseUpgrade owns both its
// container and named volume. It proves that the explicit first-run fresh
// install and a later immutable catalog transition work across real PostgreSQL
// restarts without forging or replaying migration history.
func TestPostgresPersistentVolumeSurvivesFreshAndTwoReleaseUpgrade(t *testing.T) {
	if _, err := exec.LookPath("docker"); err != nil {
		t.Skip("docker is required for the disposable persistent-volume integration test")
	}

	suffix := strings.ReplaceAll(uuid.NewString(), "-", "")[:16]
	containerName := "itsm_task4_persist_" + suffix
	volumeName := "itsm_task4_persist_" + suffix
	runTask4Docker(t, "volume", "create", volumeName)
	t.Cleanup(func() {
		_ = exec.Command("docker", "rm", "-f", containerName).Run()
		_ = exec.Command("docker", "volume", "rm", "-f", volumeName).Run()
	})

	const bootstrapRole = "task4_persistent_bootstrap"
	const runtimeRole = "task4_persistent_runtime"
	db := startTask4PersistentPostgres(t, containerName, volumeName, bootstrapRole)
	ctx, cancel := context.WithTimeout(context.Background(), migrationBootstrapIntegrationTimeout)
	defer cancel()
	_, err := db.ExecContext(ctx, `CREATE ROLE `+pq.QuoteIdentifier(runtimeRole)+` NOLOGIN NOSUPERUSER NOBYPASSRLS NOINHERIT`)
	require.NoError(t, err)
	ctx = runFreshWithPrivileges(t, ctx, db, bootstrapRole, runtimeRole)

	var historyRows int64
	require.NoError(t, db.QueryRowContext(ctx, `SELECT COUNT(*) FROM schema_migrations`).Scan(&historyRows))
	require.Zero(t, historyRows, "fresh install must persist without a forged history ledger")
	sourceState, err := migration.ReadSchemaState(ctx, db)
	require.NoError(t, err)
	require.NoError(t, migration.VerifySchemaState(sourceState, migration.CurrentRelease()))

	stopTask4PersistentPostgres(t, db, containerName)
	db = startTask4PersistentPostgres(t, containerName, volumeName, bootstrapRole)
	ctx = task4PersistentRoleContext(t, ctx, bootstrapRole, runtimeRole)
	transitionFixture := newTask4CatalogTransitionFixture(t)

	lock, err := migration.NewPostgresAdvisoryLock(db)
	require.NoError(t, err)
	require.NoError(t, lock.WithLock(ctx, func(conn migration.BootstrapConnection) error {
		migrator := migration.NewMigratorOnConnection(conn, zap.NewNop().Sugar())
		pending, planErr := migrator.PlanCatalogedUpgrade(ctx, transitionFixture.target, transitionFixture.catalog)
		if planErr != nil {
			return planErr
		}
		if len(pending) != 1 || pending[0].Version() != transitionFixture.forward.Migration.Version {
			return fmt.Errorf("persistent upgrade did not produce the exact cataloged transition")
		}
		if applyErr := migrator.ApplyCatalogedMigration(ctx, pending[0]); applyErr != nil {
			return applyErr
		}
		pending, planErr = migrator.PlanCatalogedUpgrade(ctx, transitionFixture.target, transitionFixture.catalog)
		if planErr != nil {
			return planErr
		}
		if len(pending) != 0 {
			return fmt.Errorf("committed persistent transition remained pending")
		}
		roles := migration.SchemaStateRoles{
			MigrationRole: bootstrapRole,
			RuntimeRole:   runtimeRole,
			BootstrapRole: bootstrapRole,
		}
		if privilegeErr := migration.ApplySchemaStatePrivilegesOnConnection(ctx, conn, roles); privilegeErr != nil {
			return privilegeErr
		}
		if _, verifyErr := migrator.PlanCatalogedUpgrade(ctx, transitionFixture.target, transitionFixture.catalog); verifyErr != nil {
			return verifyErr
		}
		return migration.PromoteSchemaState(ctx, conn, transitionFixture.target)
	}))

	stopTask4PersistentPostgres(t, db, containerName)
	db = startTask4PersistentPostgres(t, containerName, volumeName, bootstrapRole)
	t.Cleanup(func() { _ = db.Close() })
	ctx = task4PersistentRoleContext(t, ctx, bootstrapRole, runtimeRole)

	targetState, err := migration.ReadSchemaState(ctx, db)
	require.NoError(t, err)
	require.NoError(t, migration.VerifySchemaState(targetState, transitionFixture.target))
	require.NoError(t, db.QueryRowContext(ctx, `
		SELECT COUNT(*) FROM schema_migrations WHERE version = $1
	`, transitionFixture.forward.Migration.Version).Scan(&historyRows))
	require.Equal(t, int64(1), historyRows, "persistent upgrade must record the transition exactly once")
	var markerExists bool
	require.NoError(t, db.QueryRowContext(ctx, `
		SELECT EXISTS (
			SELECT 1 FROM information_schema.columns
			WHERE table_schema = current_schema() AND table_name = 'applications'
			  AND column_name = 'task4_transition_marker'
		)
	`).Scan(&markerExists))
	require.True(t, markerExists)

	restartLock, err := migration.NewPostgresAdvisoryLock(db)
	require.NoError(t, err)
	require.NoError(t, restartLock.WithLock(ctx, func(conn migration.BootstrapConnection) error {
		pending, planErr := migration.NewMigratorOnConnection(conn, zap.NewNop().Sugar()).
			PlanCatalogedUpgrade(ctx, transitionFixture.target, transitionFixture.catalog)
		if planErr != nil {
			return planErr
		}
		if len(pending) != 0 {
			return fmt.Errorf("completed persistent release planned a replay")
		}
		return nil
	}))
}

func newTask4CatalogTransitionFixture(t *testing.T) task4CatalogTransitionFixture {
	t.Helper()
	sourceEntry, err := migration.CurrentReleaseCatalogEntry()
	require.NoError(t, err)

	const transitionSQL = `ALTER TABLE applications ADD COLUMN task4_transition_marker text NOT NULL DEFAULT 'ready'`
	forward := migration.CatalogedMigration{
		Migration: migration.Migration{Version: "task4_fixture_add_transition_marker", Description: "add transition marker"},
		SQL:       transitionSQL,
	}
	available := make([]migration.CatalogedMigration, 0, len(migration.PostSchemaMigrations())+1)
	for _, item := range migration.PostSchemaMigrations() {
		available = append(available, migration.CatalogedMigration{Migration: item, SQL: migration.GetMigrationSQL(item.Version)})
	}
	available = append(available, forward)

	targetSource := readVerifierFixture(t, "source-schema/task4_fixture_release_v2.json")
	transition := readVerifierFixture(t, "transition-schema/028--task4_fixture_v2.json")
	registry, err := migration.EmbeddedSchemaVerifierRegistry()
	require.NoError(t, err)
	registry, err = registry.WithAssets([]migration.VerifierAssetFile{targetSource, transition})
	require.NoError(t, err)
	target := migration.ReleaseManifest{
		ReleaseID:            "task4-fixture-release-v2",
		SchemaVersion:        "task4_fixture_schema_v2",
		BaselineVersion:      "task4-fixture-baseline-v2",
		EntSchemaFingerprint: strings.Repeat("d", 64),
		Assets: []migration.ReleaseAsset{
			{Name: targetSource.Name, SHA256: verifierFixtureChecksum(targetSource)},
			{Name: transition.Name, SHA256: verifierFixtureChecksum(transition)},
			{Name: forward.Migration.Version, SHA256: verifierFixtureSQLChecksum(transitionSQL)},
		},
		SeedComponents: []migration.SeedComponent{{Name: "task4-fixture", Version: "v2"}},
	}
	targetChecksum, err := target.Checksum()
	require.NoError(t, err)
	targetEntry := migration.ReleaseCatalogEntry{
		ReleaseID:             target.ReleaseID,
		SchemaVersion:         target.SchemaVersion,
		BaselineVersion:       target.BaselineVersion,
		ReleaseManifestSHA256: targetChecksum,
		BaselineAsset:         migration.ReleaseAsset{Name: "task4-fixture-baseline", SHA256: strings.Repeat("e", 64)},
		SourceSchemaAsset:     migration.ReleaseAsset{Name: targetSource.Name, SHA256: verifierFixtureChecksum(targetSource)},
		TransitionAssets:      []migration.ReleaseAsset{{Name: transition.Name, SHA256: verifierFixtureChecksum(transition)}},
		CoveredMigrations:     append(append([]string(nil), sourceEntry.CoveredMigrations...), forward.Migration.Version),
	}
	catalog, err := migration.NewUpgradeReleaseCatalog(
		[]migration.ReleaseCatalogEntry{sourceEntry, targetEntry}, registry, available,
	)
	require.NoError(t, err)
	return task4CatalogTransitionFixture{target: target, catalog: catalog, forward: forward}
}

func task4PersistentRoleContext(t *testing.T, ctx context.Context, migrationRole, runtimeRole string) context.Context {
	t.Helper()
	roleContext, err := migration.WithSchemaStateRoles(ctx, migration.SchemaStateRoles{
		MigrationRole: migrationRole,
		RuntimeRole:   runtimeRole,
		BootstrapRole: migrationRole,
	})
	require.NoError(t, err)
	return roleContext
}

func startTask4PersistentPostgres(t *testing.T, containerName, volumeName, bootstrapRole string) *sql.DB {
	t.Helper()
	runTask4Docker(t,
		"run", "--detach", "--name", containerName,
		"--mount", "type=volume,source="+volumeName+",target=/var/lib/postgresql/data",
		"--env", "POSTGRES_HOST_AUTH_METHOD=trust",
		"--env", "POSTGRES_USER="+bootstrapRole,
		"--env", "POSTGRES_DB=postgres",
		"--publish", "127.0.0.1::5432",
		task4PinnedPostgresImage,
	)
	portOutput := runTask4Docker(t, "port", containerName, "5432/tcp")
	separator := strings.LastIndex(portOutput, ":")
	require.Greater(t, separator, 0, "docker returned an invalid PostgreSQL port mapping")
	port := strings.TrimSpace(portOutput[separator+1:])
	dsn := fmt.Sprintf("postgres://%s@127.0.0.1:%s/postgres?sslmode=disable", bootstrapRole, port)
	db, err := sql.Open("postgres", dsn)
	require.NoError(t, err)
	db.SetMaxOpenConns(4)
	deadline := time.Now().Add(time.Minute)
	for {
		pingCtx, cancel := context.WithTimeout(context.Background(), time.Second)
		err = db.PingContext(pingCtx)
		cancel()
		if err == nil {
			return db
		}
		if time.Now().After(deadline) {
			_ = db.Close()
			t.Fatal("disposable PostgreSQL did not become ready")
		}
		time.Sleep(200 * time.Millisecond)
	}
}

func stopTask4PersistentPostgres(t *testing.T, db *sql.DB, containerName string) {
	t.Helper()
	require.NoError(t, db.Close())
	runTask4Docker(t, "rm", "--force", containerName)
}

func runTask4Docker(t *testing.T, arguments ...string) string {
	t.Helper()
	output, err := exec.Command("docker", arguments...).CombinedOutput()
	if err != nil {
		t.Fatalf("disposable Docker operation %q failed", arguments[0])
	}
	return strings.TrimSpace(string(output))
}
