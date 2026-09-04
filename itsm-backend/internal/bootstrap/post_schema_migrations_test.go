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
	require.NotEmpty(t, runner.migrations)

	positions := make(map[string]int, len(runner.migrations))
	for index, registered := range runner.migrations {
		positions[registered.Version] = index
	}
	requiredOrder := []string{
		"007_add_change_execution_tables",
		"008_add_initialization_ledger",
		"009_enable_rls_tenant_isolation",
		"011_add_tool_invocation_tenant_id",
		"012_drop_service_catalog_item",
		"013_service_request_delegates_to_ticket",
		"014_drop_legacy_approval_workflow",
		"015_process_instance_running_unique_guard",
		"016_add_service_request_contact_fields",
		"017_drop_ticket_type_legacy_approval_fields",
		"018_convert_legacy_serial_ids_to_identity",
		"019_kaf_execution_integrity_rls",
		"020_work_item_number_allocator",
		"021_add_callback_optional_declared",
		"022_drop_professional_extension_shared_fields",
		"023_reconcile_change_execution_tenants",
		"024_reconcile_current_rls_policies",
	}
	for index, version := range requiredOrder {
		position, ok := positions[version]
		require.True(t, ok, "required post-schema migration %s is missing", version)
		if index > 0 {
			require.Greater(t, position, positions[requiredOrder[index-1]], "%s must follow %s", version, requiredOrder[index-1])
		}
	}
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
