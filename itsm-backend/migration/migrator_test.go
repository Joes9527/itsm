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

func TestChangeExecutionTenantBackfillUsesWorkItemAuthority(t *testing.T) {
	sql := GetMigrationSQL("007_add_change_execution_tables")
	require.NotEmpty(t, sql)

	// Change is a professional extension and no longer owns tenant_id. Every
	// execution child keeps its direct tenant_id, sourced through Change's
	// authoritative WorkItem relation during migration.
	assert.NotContains(t, sql, "c.tenant_id")
	assert.Equal(t, 6, strings.Count(sql, "SET tenant_id = wi.tenant_id"))
	assert.Equal(t, 6, strings.Count(sql, "JOIN tickets wi ON wi.id = c.work_item_id"))
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

func TestProfessionalExtensionsDropSharedFieldsIsVersioned(t *testing.T) {
	const version = "022_drop_professional_extension_shared_fields"

	require.Equal(t, version, RegisteredMigrations[14].Version)
	canonicalSQL := GetMigrationSQL(version)
	require.NotEmpty(t, canonicalSQL)
	for _, asset := range []string{
		"20260901_drop_professional_extension_shared_fields.sql",
		"20260901_drop_professional_extension_shared_fields_dev_reset.sql",
		"20260901_drop_professional_extension_shared_fields_verify.sql",
	} {
		contents, err := os.ReadFile(filepath.Join("..", "migrations", asset))
		require.NoError(t, err)
		require.NotEmpty(t, contents)
		if asset == "20260901_drop_professional_extension_shared_fields.sql" {
			require.Equal(t, strings.TrimSpace(canonicalSQL), strings.TrimSpace(string(contents)),
				"canonical migration and retained apply asset must not drift")
		}
		if asset != "20260901_drop_professional_extension_shared_fields_verify.sql" {
			require.Contains(t, string(contents),
				"CREATE POLICY %I ON %I.%I AS PERMISSIVE FOR ALL TO PUBLIC")
		}
	}
	verificationSQL, err := os.ReadFile(filepath.Join("..", "migrations", "20260901_drop_professional_extension_shared_fields_verify.sql"))
	require.NoError(t, err)
	for _, expected := range []string{
		"policy.polroles", "policy.polcmd", "policy.polpermissive",
		"policy_roles <> ARRAY[0::OID]", "policy_command <> '*'", "OR NOT policy_permissive",
		"legacy ticket_approvals table still exists",
		"'workflows', 'workflow_instances', 'workflow_tasks', 'workflow_versions'",
		"legacy % table still exists",
		"release approval routing column releases.requires_approval still exists",
		"legacy ticket_categories.workflow_id column still exists",
	} {
		require.Contains(t, string(verificationSQL), expected)
	}
}

func TestProfessionalExtensionVerificationBindsExactReadyValidUniqueIndexes(t *testing.T) {
	verificationSQL, err := os.ReadFile(filepath.Join("..", "migrations", "20260901_drop_professional_extension_shared_fields_verify.sql"))
	require.NoError(t, err)

	sql := string(verificationSQL)
	for _, expected := range []string{
		"index_schema.nspname = current_schema()",
		"table_schema.nspname = current_schema()",
		"table_relation.relname = extension_table",
		"array_agg(attribute.attname ORDER BY key_column.ordinal) = ARRAY['work_item_id']::name[]",
		"i.indisunique",
		"i.indisvalid",
		"i.indisready",
		"i.indnkeyatts = 1",
		"i.indnatts = 1",
		"i.indexprs IS NULL",
		"i.indpred IS NULL",
	} {
		assert.Contains(t, sql, expected)
	}
}

func TestMigrationStreamAndLedgerFrozenHistoricalSQL(t *testing.T) {
	expected := map[string]string{
		"001_initial_schema":                            "",
		"002_add_notification_preferences":              "0ec1d18b69ccfb0659ae0e83808a936e6a6fd6e01b040e2f3ce059d3017eac19",
		"003_add_audit_indexes":                         "da35d6c44dc1a44d8701a8f9a4c122a5f784c44cfed28a567becca96f7dff66d",
		"004_add_sla_calendar":                          "4e5fae39324d29f7509f57b8d773471933be5b23683148233f8a5a6cb92dffff",
		"005_add_external_id_mapping":                   "7750c54cbe1aa076420e3583d4f644c0848818fb2ee4831b209f5a81368af836",
		"006_add_change_approvals":                      "ced92ec8b3ebb694972edcb1e71b71443374569865663f732a3edc8408531c5b",
		"010_add_ticket_types":                          "8add8034d53c5061a3fe6b87ca98c671a3e0ce596ce332389a82bdba06f9ca6b",
		"007_add_change_execution_tables":               "d6ac16b327fd49b417da1b3fc5fff118469aaf15b8e00b95d23ea5022e2117dd",
		"008_add_initialization_ledger":                 "772eeef5f6485595f13103f76bb1344aa9526f5f0a7b94f5a062b86ea227d5be",
		"009_enable_rls_tenant_isolation":               "9d1c092db4cb9513cc96b92b644e3e97e5b7593ea681d60d24a4d1c597c907f0",
		"011_add_tool_invocation_tenant_id":             "2fa2d054c022076aa247b495172d9a8f7b6a967acd6db4063ccf68522bdf50b4",
		"012_drop_service_catalog_item":                 "3324423cc96510e30caa4671c6456db52fb8ba4d9215a837ccdc3910bcae27fc",
		"013_service_request_delegates_to_ticket":       "3710d2d730ad1982cae58b48d1dd67ab02cd85dfadee1b1a53011141501f4490",
		"014_drop_legacy_approval_workflow":             "6fdb74eeb8902f6840153df765f65a60a48b3a93995323e727cf11ca1974a8df",
		"015_process_instance_running_unique_guard":     "b57eab5dbe75325b50001ee3b4c111e88e4f6d92ebbae9782d82e8a4ac5e4d33",
		"016_add_service_request_contact_fields":        "917e74af4aca87b2f40370239ed41c61c847b9c32588ffc8680ecaaad73a0b67",
		"017_drop_ticket_type_legacy_approval_fields":   "5992dbcc15be797e03dacf9fa12e6d9b0fce3e4953bd4d8f15d0ac4e51465be5",
		"018_convert_legacy_serial_ids_to_identity":     "c1d945e62a9f216b38f853defdf0b6bbfb55311d648114b7cc93487d94eb309c",
		"019_kaf_execution_integrity_rls":               "70bc70ef836a6de495edcf8eff7626c52dd185dc11da121a56dbbe283731af66",
		"020_work_item_number_allocator":                "5bec8fc09daa852ba36f5b5aa96a06118a57adf3ee24a1150a856efd015159e9",
		"021_add_callback_optional_declared":            "ead5b58943c84cd2d6ba3dfcc2da03a1ae79a1222432e90e8bfa57e3e94a746e",
		"022_drop_professional_extension_shared_fields": "20f9c60120e3b2e93a0197a121e8c32e09e34108f817f0ee62ce9cdbc3d565d9",
		"023_add_process_start_request_digest":          "82ab09e7e1b60092ef40db420e7f14013a64085e4dd8888251e8d77be2987010",
		"024_incident_rule_action_receipts":             "3a2226f533cd960bdd14a45a2348ce545646ad6891ca2a82470a1c233b7fe8d4",
		"025_email_attachment_source_identity":          "a8a9660ed99114dad9e9078ad2f08976a536add1b475e3bbcf20887f7e500822",
		"026_intake_actor_provenance":                   "71668ba724d24010f294ec1bb55dad9e2e4c5419ccd85a19131204019819a97e",
		"027_work_item_identity_field_retirement":       "ef5748e8b1c96ca2ef52b917e2793e705ee2d753fcf94cd90fcceb1ac98a36c0",
		"028_service_request_work_item_authority":       "c145c16991841983599da34362f003d2db0a785ff5ed89918c6b7fb0b58571c5",
		"029_catalog_target_class_authority":            "77ab6397f6b171308d98289587e4731f4406e1285118ae9e8cbee79dd95d7550",
		"030_catalog_access_policy_result":              "428a27eb050807c8271f0056e9ff488c98be76b4bc6be7b63dd084eda85dac84",
		"031_kaf_action_request_digest":                 "9db8c0bf162254902c3acb8655cc1591febb9cb3f5e640f2155318d4dbbaa7ef",
		"032_workitem_sla_cycle":                        "cf56f54a7ecc05a5af5ef8982df35ef18df08a9c6ba7cea6fe51e16557509180",
		"033_incident_status_events":                    "3046ce9cbf1b9fbd97cc815ea89833edfbfb8490bb3ea07f5ef97dfde789bec3",
		"034_problem_investigation_completion":          "c1eb574e4262b132b4feee17d661785c7192de70129fc5b5f9a29b973c02e454",
		"035_change_professional_evidence":              "41f5c672e9357389fb9fdead1561fc3c80faf6cb9f0d7bef4d6368df44fccbae",
		"036_intake_frozen_workflow_context":            "bb702d702ff5a31a317a2dffd08b1be1c3032f52b8481a179f8670f801337190",
	}
	for v, h := range expected {
		require.Equal(t, h, checksumSQL(GetMigrationSQL(v)), v)
	}
	require.Equal(t, []string{"007_add_change_execution_tables", "008_add_initialization_ledger", "009_enable_rls_tenant_isolation", "011_add_tool_invocation_tenant_id", "012_drop_service_catalog_item", "013_service_request_delegates_to_ticket", "014_drop_legacy_approval_workflow", "015_process_instance_running_unique_guard", "016_add_service_request_contact_fields", "017_drop_ticket_type_legacy_approval_fields", "018_convert_legacy_serial_ids_to_identity", "019_kaf_execution_integrity_rls", "020_work_item_number_allocator", "021_add_callback_optional_declared", "022_drop_professional_extension_shared_fields", "023_add_process_start_request_digest", "024_incident_rule_action_receipts", "025_email_attachment_source_identity", "026_intake_actor_provenance", "027_work_item_identity_field_retirement", "028_service_request_work_item_authority", "029_catalog_target_class_authority", "030_catalog_access_policy_result", "031_kaf_action_request_digest", "032_workitem_sla_cycle", "033_incident_status_events", "034_problem_investigation_completion", "035_change_professional_evidence", "036_intake_frozen_workflow_context"}, frozenMigrationVersions())
}
