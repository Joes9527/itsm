package migration

import (
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestMigration_Struct(t *testing.T) {
	// Test that Migration struct can be created correctly
	mig := Migration{
		Version:     "002_add_notification_preferences",
		Description: "Test migration",
		RollbackSQL: "DROP TABLE user_notification_preferences",
	}

	assert.Equal(t, "002_add_notification_preferences", mig.Version)
	assert.Equal(t, "Test migration", mig.Description)
	assert.NotEmpty(t, mig.RollbackSQL)
}

func TestMigration_NoRollbackSQL(t *testing.T) {
	// Test migration without rollback SQL
	mig := Migration{
		Version:     "001_initial_schema",
		Description: "Initial schema",
		RollbackSQL: "",
	}

	assert.Equal(t, "001_initial_schema", mig.Version)
	assert.Empty(t, mig.RollbackSQL)
}

func TestMigrationSlice_Versions(t *testing.T) {
	// Test that migrations can be sorted by version
	migrations := []Migration{
		{Version: "003_add_audit_indexes"},
		{Version: "001_initial_schema"},
		{Version: "002_add_notification_preferences"},
	}

	assert.Equal(t, 3, len(migrations))
	assert.Equal(t, "003_add_audit_indexes", migrations[0].Version)
}

func TestRegisteredMigrations(t *testing.T) {
	// Test that RegisteredMigrations contains expected migrations
	assert.NotEmpty(t, RegisteredMigrations)

	assert.Equal(t, "007_add_change_execution_tables", RegisteredMigrations[0].Version)
	assert.Equal(t, "001_initial_schema", LegacyMigrations[0].Version)
}

func TestGetMigrationSQL(t *testing.T) {
	// Test GetMigrationSQL returns SQL for known migrations
	sql := GetMigrationSQL("002_add_notification_preferences")
	assert.NotEmpty(t, sql)
	assert.Contains(t, sql, "CREATE TABLE")

	// Test GetMigrationSQL returns empty for unknown migrations
	sql = GetMigrationSQL("999_unknown")
	assert.Empty(t, sql)
}

func TestGetMigrationSQL_InitialSchema(t *testing.T) {
	// Test that initial schema returns empty (handled by Ent)
	sql := GetMigrationSQL("001_initial_schema")
	assert.Empty(t, sql)
}

func TestChangeExecutionTablesAreVersioned(t *testing.T) {
	sql := GetMigrationSQL("007_add_change_execution_tables")
	assert.NotEmpty(t, sql)

	for _, table := range []string{
		"change_approval_chains",
		"change_risk_assessments",
		"change_rollback_plans",
		"change_rollback_executions",
		"change_implementation_plans",
	} {
		assert.Contains(t, sql, "CREATE TABLE IF NOT EXISTS "+table)
	}
	assert.Contains(t, sql, "tenant_id BIGINT NOT NULL")
	assert.Contains(t, sql, "ALTER COLUMN tenant_id DROP DEFAULT")
	assert.Contains(t, sql, "ALTER TABLE change_approvals ALTER COLUMN tenant_id SET NOT NULL")
	assert.Contains(t, sql, "rows without a resolvable tenant")
	for _, table := range []string{
		"change_approval_chains",
		"change_risk_assessments",
		"change_rollback_plans",
		"change_rollback_executions",
		"change_implementation_plans",
	} {
		assert.Contains(t, sql, "ALTER TABLE "+table+" ALTER COLUMN tenant_id DROP DEFAULT")
	}
}

func TestPostSchemaMigrationsStartsAtUnifiedVersion(t *testing.T) {
	migrations := PostSchemaMigrations()
	assert.NotEmpty(t, migrations)
	assert.Equal(t, "007_add_change_execution_tables", migrations[0].Version)
	for _, migration := range migrations {
		assert.GreaterOrEqual(t, migration.Version, "007_")
		assert.NotEmpty(t, GetMigrationSQL(migration.Version))
	}
}

func TestInitializationLedgerIsVersioned(t *testing.T) {
	sql := GetMigrationSQL("008_add_initialization_ledger")
	assert.NotEmpty(t, sql)
	for _, table := range []string{
		"initialization_installations",
		"initialization_runs",
		"initialization_component_attempts",
		"initialization_managed_records",
	} {
		assert.Contains(t, sql, "CREATE TABLE IF NOT EXISTS "+table)
	}
	assert.Contains(t, sql, "fencing_token")
	assert.Contains(t, sql, "lease_expires_at")
	assert.Contains(t, sql, "UNIQUE(scope_type, scope_id, component)")
}

func TestMigrationSQLChecksumIsDeterministic(t *testing.T) {
	sql := GetMigrationSQL("008_add_initialization_ledger")
	first := checksumSQL(sql)
	second := checksumSQL(sql)
	assert.NotEmpty(t, first)
	assert.Equal(t, first, second)
	assert.NotEqual(t, first, checksumSQL(sql+" -- changed"))
}

func TestProcessInstanceRunningUniqueGuardIsVersioned(t *testing.T) {
	sql := GetMigrationSQL("015_process_instance_running_unique_guard")
	assert.NotEmpty(t, sql)
	assert.Contains(t, sql, "CREATE UNIQUE INDEX IF NOT EXISTS idx_process_instances_running_unique")
	assert.Contains(t, sql, "ON process_instances (tenant_id, business_key)")
	assert.Contains(t, sql, "WHERE status = 'running'")
	// Cleanup step must run before the index creation, otherwise pre-existing
	// duplicate running rows would make the CREATE UNIQUE INDEX statement fail.
	assert.Less(t,
		strings.Index(sql, "UPDATE process_instances"),
		strings.Index(sql, "CREATE UNIQUE INDEX"),
	)

	rollback := ""
	for _, m := range RegisteredMigrations {
		if m.Version == "015_process_instance_running_unique_guard" {
			rollback = m.RollbackSQL
		}
	}
	assert.Contains(t, rollback, "DROP INDEX IF EXISTS idx_process_instances_running_unique")
}

func TestKafExecutionIntegrityTablesHaveRegisteredTenantRLS(t *testing.T) {
	sql := GetMigrationSQL("019_kaf_execution_integrity_rls")
	require.NotEmpty(t, sql)
	for _, table := range []string{"kaf_task_action_ledgers", "kaf_task_completion_receipts"} {
		assert.Contains(t, sql, "ALTER TABLE "+table+" ENABLE ROW LEVEL SECURITY")
		assert.Contains(t, sql, "ALTER TABLE "+table+" FORCE ROW LEVEL SECURITY")
		assert.Contains(t, sql, "CREATE POLICY tenant_isolation_"+table+" ON "+table)
		assert.Contains(t, sql, "USING (tenant_id = NULLIF(current_setting('app.current_tenant', true), '')::bigint)")
		assert.Contains(t, sql, "WITH CHECK (tenant_id = NULLIF(current_setting('app.current_tenant', true), '')::bigint)")
	}
	assert.Equal(t, checksumSQL(sql), checksumSQL(GetMigrationSQL("019_kaf_execution_integrity_rls")))
}

func TestUnifiedIntakeMigrationEnablesRLS(t *testing.T) {
	require.NotEmpty(t, RegisteredMigrations)
	assert.Equal(t, "020_unified_intake_rls", RegisteredMigrations[len(RegisteredMigrations)-1].Version)

	sql := GetMigrationSQL("020_unified_intake_rls")
	require.NotEmpty(t, sql)
	for _, table := range []string{"intake_requests", "intake_resolution_snapshots", "external_identities"} {
		assert.Contains(t, sql, "ALTER TABLE "+table+" ENABLE ROW LEVEL SECURITY")
		assert.Contains(t, sql, "ALTER TABLE "+table+" FORCE ROW LEVEL SECURITY")
		assert.Contains(t, sql, "CREATE POLICY "+table+"_tenant_isolation ON "+table)
		assert.Contains(t, sql, "USING (tenant_id = NULLIF(current_setting('app.current_tenant', true), '')::bigint)")
		assert.Contains(t, sql, "WITH CHECK (tenant_id = NULLIF(current_setting('app.current_tenant', true), '')::bigint)")
	}
	assert.Contains(t, sql, "CONSTRAINT intake_requests_completed_work_item_check")
	assert.Contains(t, sql, "status <> 'completed' OR (work_item_id IS NOT NULL AND completed_at IS NOT NULL)")
}

func TestWorkItemAuthorityMigrationDropsDuplicateColumnsAndUsesJoinRLS(t *testing.T) {
	require.NotEmpty(t, RegisteredMigrations)
	assert.Equal(t, "021_work_item_authority", RegisteredMigrations[len(RegisteredMigrations)-1].Version)

	sql := GetMigrationSQL("021_work_item_authority")
	require.NotEmpty(t, sql)
	for _, column := range []string{"title", "description", "status", "priority", "reporter_id", "tenant_id", "created_at", "updated_at"} {
		assert.Contains(t, sql, "ALTER TABLE incidents DROP COLUMN IF EXISTS "+column)
	}
	for _, column := range []string{"requester_id", "tenant_id", "created_at", "updated_at"} {
		assert.Contains(t, sql, "ALTER TABLE service_requests DROP COLUMN IF EXISTS "+column)
	}
	assert.Contains(t, sql, "EXISTS (SELECT 1 FROM tickets")
	assert.Contains(t, sql, "tickets.id = incidents.work_item_id")
	assert.Contains(t, sql, "tickets.id = service_requests.ticket_id")
	assert.Contains(t, sql, "ALTER TABLE service_catalogs DROP COLUMN IF EXISTS itsm_type")
	assert.Contains(t, sql, "target_class IN ('service_request_item', 'incident', 'change_request')")
}
