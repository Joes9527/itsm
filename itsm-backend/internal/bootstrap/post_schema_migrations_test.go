package bootstrap

import (
	"context"
	"errors"
	"testing"

	"itsm-backend/migration"

	"github.com/stretchr/testify/require"
)

type recordingPostSchemaMigrator struct {
	ensureErr  error
	runErr     error
	ensured    bool
	migrations []migration.Migration
}

func (m *recordingPostSchemaMigrator) EnsureMigrationsTable(context.Context) error {
	m.ensured = true
	return m.ensureErr
}

func (m *recordingPostSchemaMigrator) RunMigrations(_ context.Context, migrations []migration.Migration) (int, error) {
	m.migrations = migrations
	return len(migrations), m.runErr
}

func TestRunPostSchemaMigrationsAppliesVersion007(t *testing.T) {
	runner := &recordingPostSchemaMigrator{}

	err := runPostSchemaMigrations(context.Background(), runner)

	require.NoError(t, err)
	require.True(t, runner.ensured)
	require.Len(t, runner.migrations, 13)
	require.Equal(t, "007_add_change_execution_tables", runner.migrations[0].Version)
	require.Equal(t, "008_add_initialization_ledger", runner.migrations[1].Version)
	require.Equal(t, "009_enable_rls_tenant_isolation", runner.migrations[2].Version)
	require.Equal(t, "010_add_ticket_types", runner.migrations[3].Version)
	require.Equal(t, "011_add_tool_invocation_tenant_id", runner.migrations[4].Version)
	require.Equal(t, "012_drop_service_catalog_item", runner.migrations[5].Version)
	require.Equal(t, "013_service_request_delegates_to_ticket", runner.migrations[6].Version)
	require.Equal(t, "014_drop_legacy_approval_workflow", runner.migrations[7].Version)
	require.Equal(t, "015_process_instance_running_unique_guard", runner.migrations[8].Version)
	require.Equal(t, "016_add_service_request_contact_fields", runner.migrations[9].Version)
	require.Equal(t, "017_drop_ticket_type_legacy_approval_fields", runner.migrations[10].Version)
	require.Equal(t, "018_convert_legacy_serial_ids_to_identity", runner.migrations[11].Version)
	require.Equal(t, "019_kaf_execution_integrity_rls", runner.migrations[12].Version)
}

func TestRunPostSchemaMigrationsFailsClosed(t *testing.T) {
	t.Run("ledger", func(t *testing.T) {
		runner := &recordingPostSchemaMigrator{ensureErr: errors.New("ledger unavailable")}
		err := runPostSchemaMigrations(context.Background(), runner)
		require.ErrorContains(t, err, "ensure migration ledger")
		require.Empty(t, runner.migrations)
	})

	t.Run("migration", func(t *testing.T) {
		runner := &recordingPostSchemaMigrator{runErr: errors.New("migration failed")}
		err := runPostSchemaMigrations(context.Background(), runner)
		require.ErrorContains(t, err, "run post-schema migrations")
	})
}
