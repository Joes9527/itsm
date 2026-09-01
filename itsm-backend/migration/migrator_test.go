package migration

import (
	"os"
	"path/filepath"
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

func TestValidateMigrationCatalogRejectsInvalidActiveAndLegacyCatalogs(t *testing.T) {
	validActive := []Migration{{Version: "100_active", Description: "active"}}
	validLegacy := []Migration{{Version: "001_history", Description: "history"}}
	require.NoError(t, validateMigrationCatalog(validActive, validLegacy, func(version string) string {
		if version == "100_active" {
			return "SELECT 1;"
		}
		return ""
	}))

	require.ErrorContains(t, validateMigrationCatalog([]Migration{{Version: "200", Description: "late"}, {Version: "100", Description: "early"}}, validLegacy, func(string) string { return "SELECT 1;" }), "ordered")
	require.ErrorContains(t, validateMigrationCatalog([]Migration{{Version: "100", Description: "one"}, {Version: "100", Description: "two"}}, validLegacy, func(string) string { return "SELECT 1;" }), "duplicate")
	invalidAvailable := PostSchemaMigrations()
	invalidAvailable[0] = LegacyMigrations[0]
	require.ErrorContains(t, validateAvailableMigrations(invalidAvailable), "canonical order")
	require.ErrorContains(t, validateAvailableMigrations(nil), "incomplete")
	require.ErrorContains(t, validateMigrationCatalog(validActive, validLegacy, func(string) string { return "" }), "empty SQL")
}

func TestValidateMigrationLedgerFailsClosedForUnknownDuplicateAndChecksumDrift(t *testing.T) {
	require.ErrorContains(t, validateMigrationLedger([]Migration{{Version: "999_unknown"}}), "unknown version")
	known := RegisteredMigrations[0]
	require.ErrorContains(t, validateMigrationLedger([]Migration{{Version: known.Version, Checksum: "wrong"}}), "checksum mismatch")
	checksum := checksumSQL(GetMigrationSQL(known.Version))
	require.ErrorContains(t, validateMigrationLedger([]Migration{{Version: known.Version, Checksum: checksum}, {Version: known.Version, Checksum: checksum}}), "duplicate")
	require.NoError(t, validateMigrationLedger([]Migration{{Version: known.Version, Checksum: checksum}}))
}

func TestMigrationStreamAndLedgerRequireCanonicalOrderAndActivePrefix(t *testing.T) {
	available := PostSchemaMigrations()
	available[0], available[1] = available[1], available[0]
	require.ErrorContains(t, validateAvailableMigrations(available), "canonical order")

	later := RegisteredMigrations[1]
	require.ErrorContains(t, validateMigrationLedger([]Migration{{Version: later.Version, Checksum: checksumSQL(GetMigrationSQL(later.Version))}}), "continuous prefix")

	legacy := LegacyMigrations[0]
	first := RegisteredMigrations[0]
	require.NoError(t, validateMigrationLedger([]Migration{
		{Version: legacy.Version, Checksum: checksumSQL(GetMigrationSQL(legacy.Version))},
		{Version: first.Version, Checksum: checksumSQL(GetMigrationSQL(first.Version))},
	}))
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

func TestTenantRLSReconcilerUsesTheCurrentSchemaAndRuntimeGUC(t *testing.T) {
	sql := GetMigrationSQL("009_enable_rls_tenant_isolation")
	require.NotEmpty(t, sql)
	assert.Contains(t, sql, "pg_class")
	assert.Contains(t, sql, "pg_namespace")
	assert.Contains(t, sql, "pg_attribute")
	assert.Contains(t, sql, "relkind = 'r'")
	assert.Contains(t, sql, "schema.oid = current_schema()::regnamespace")
	assert.Contains(t, sql, "tenant_id = NULLIF(current_setting(''app.current_tenant'', true), '''')::bigint")
	assert.Contains(t, sql, "DROP POLICY IF EXISTS %I ON %I.%I")
	assert.Contains(t, sql, "DROP FUNCTION IF EXISTS get_current_tenant_id()")
	assert.NotContains(t, sql, "sla_policies")
	assert.NotContains(t, sql, "approval_workflows")
	assert.NotContains(t, sql, "app.current_tenant_id")
	assert.NotContains(t, sql, "FORCE ROW LEVEL SECURITY")
}

func TestTicketTypesMigrationIsRetiredFromTheActivePostSchemaStream(t *testing.T) {
	for _, migration := range RegisteredMigrations {
		assert.NotEqual(t, "010_add_ticket_types", migration.Version)
	}
	recorded := false
	for _, migration := range LegacyMigrations {
		if migration.Version == "010_add_ticket_types" {
			recorded = true
		}
	}
	assert.True(t, recorded)
	assert.NotEmpty(t, GetMigrationSQL("010_add_ticket_types"))
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

func TestWorkItemNumberAllocatorIsVersioned(t *testing.T) {
	const version = "020_work_item_number_allocator"

	var registered *Migration
	for i := range RegisteredMigrations {
		if RegisteredMigrations[i].Version == version {
			registered = &RegisteredMigrations[i]
			break
		}
	}
	require.NotNil(t, registered)
	assert.Equal(t,
		"Create tenant/month WorkItem number sequences and replace global ticket_number uniqueness with tenant-scoped uniqueness",
		registered.Description,
	)
	assert.Contains(t, registered.RollbackSQL, "rollback requires an empty tickets table")
	assert.Contains(t, registered.RollbackSQL, "DROP INDEX IF EXISTS ticket_tenant_id_ticket_number")
	assert.Contains(t, registered.RollbackSQL, "CREATE UNIQUE INDEX IF NOT EXISTS ticket_ticket_number")
	assert.Contains(t, registered.RollbackSQL, "DROP TABLE IF EXISTS work_item_number_sequences")

	sql := GetMigrationSQL(version)
	require.NotEmpty(t, sql)
	assert.Contains(t, sql, "CREATE TABLE IF NOT EXISTS work_item_number_sequences")
	assert.Contains(t, sql, "work_item_number_sequences_period_check")
	assert.Contains(t, sql, "work_item_number_sequences_last_value_check")
	assert.Contains(t, sql, "DO $$")
	assert.Contains(t, sql, "ALTER TABLE work_item_number_sequences\n            ADD CONSTRAINT work_item_number_sequences_period_check")
	assert.Contains(t, sql, "ALTER TABLE work_item_number_sequences\n            ADD CONSTRAINT work_item_number_sequences_last_value_check")
	assert.Contains(t, sql, "CREATE UNIQUE INDEX IF NOT EXISTS workitemnumbersequence_tenant_id_period")
	assert.Contains(t, sql, "CREATE UNIQUE INDEX IF NOT EXISTS ticket_tenant_id_ticket_number")
}

func TestWorkItemNumberAllocatorVerificationBindsReadyValidIndexes(t *testing.T) {
	verificationSQL, err := os.ReadFile(filepath.Join("..", "migrations", "20260901_work_item_number_allocator_verify.sql"))
	require.NoError(t, err)

	sql := string(verificationSQL)
	for _, expected := range []string{
		"JOIN pg_namespace index_schema ON index_schema.oid = index_relation.relnamespace",
		"JOIN pg_class table_relation ON table_relation.oid = i.indrelid",
		"JOIN pg_namespace table_schema ON table_schema.oid = table_relation.relnamespace",
		"index_schema.nspname = current_schema()",
		"AND table_schema.nspname = current_schema()",
		"AND table_relation.relname = 'work_item_number_sequences'\n          AND index_relation.relname = 'workitemnumbersequence_tenant_id_period'\n          AND i.indisunique\n          AND i.indisvalid\n          AND i.indisready",
		"AND table_relation.relname = 'tickets'\n          AND index_relation.relname = 'ticket_tenant_id_ticket_number'\n          AND i.indisunique\n          AND i.indisvalid\n          AND i.indisready",
	} {
		assert.Contains(t, sql, expected)
	}
}
