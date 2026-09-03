package migration

import (
	"os"
	"testing"

	"github.com/stretchr/testify/require"
)

// TestUnifiedIntakeMigrationsRegisteredInOrder guards the migration numbering
// contract for the unified intake reconciliation: 023_unified_intake_rls,
// 024_service_catalog_target_class_authority, and 025_external_identity_version
// must all be registered, and ordered after 020_work_item_number_allocator with
// 023 < 024 < 025. Task 14 registered 024 in the same cutover commit as the code
// that stops reading service_catalogs.itsm_type — do not register it earlier.
func TestUnifiedIntakeMigrationsRegisteredInOrder(t *testing.T) {
	migrations := PostSchemaMigrations()
	var versions []string
	for _, m := range migrations {
		versions = append(versions, m.Version)
	}
	require.Contains(t, versions, "023_unified_intake_rls")
	require.Contains(t, versions, "024_service_catalog_target_class_authority")
	require.Contains(t, versions, "025_external_identity_version")
	idx020 := indexOf(versions, "020_work_item_number_allocator")
	idx023 := indexOf(versions, "023_unified_intake_rls")
	idx024 := indexOf(versions, "024_service_catalog_target_class_authority")
	idx025 := indexOf(versions, "025_external_identity_version")
	require.True(t, idx020 < idx023 && idx023 < idx024 && idx024 < idx025)
}

func indexOf(s []string, v string) int {
	for i, x := range s {
		if x == v {
			return i
		}
	}
	return -1
}

const migration023UnifiedIntakeRLSSQL = `
DO $$
BEGIN
    IF NOT EXISTS (
        SELECT 1 FROM pg_constraint
        WHERE conname = 'intake_requests_completed_work_item_check'
    ) THEN
        ALTER TABLE intake_requests
            ADD CONSTRAINT intake_requests_completed_work_item_check
            CHECK (status <> 'completed' OR (work_item_id IS NOT NULL AND completed_at IS NOT NULL));
    END IF;
END $$;

ALTER TABLE intake_requests ENABLE ROW LEVEL SECURITY;
ALTER TABLE intake_requests FORCE ROW LEVEL SECURITY;
DROP POLICY IF EXISTS intake_requests_tenant_isolation ON intake_requests;
CREATE POLICY intake_requests_tenant_isolation ON intake_requests
    USING (tenant_id = NULLIF(current_setting('app.current_tenant', true), '')::bigint)
    WITH CHECK (tenant_id = NULLIF(current_setting('app.current_tenant', true), '')::bigint);

ALTER TABLE intake_resolution_snapshots ENABLE ROW LEVEL SECURITY;
ALTER TABLE intake_resolution_snapshots FORCE ROW LEVEL SECURITY;
DROP POLICY IF EXISTS intake_resolution_snapshots_tenant_isolation ON intake_resolution_snapshots;
CREATE POLICY intake_resolution_snapshots_tenant_isolation ON intake_resolution_snapshots
    USING (tenant_id = NULLIF(current_setting('app.current_tenant', true), '')::bigint)
    WITH CHECK (tenant_id = NULLIF(current_setting('app.current_tenant', true), '')::bigint);

ALTER TABLE external_identities ENABLE ROW LEVEL SECURITY;
ALTER TABLE external_identities FORCE ROW LEVEL SECURITY;
DROP POLICY IF EXISTS external_identities_tenant_isolation ON external_identities;
CREATE POLICY external_identities_tenant_isolation ON external_identities
    USING (tenant_id = NULLIF(current_setting('app.current_tenant', true), '')::bigint)
    WITH CHECK (tenant_id = NULLIF(current_setting('app.current_tenant', true), '')::bigint);
`

const migration023UnifiedIntakeRLSDevResetSQL = `DO $$
BEGIN
    IF EXISTS (SELECT 1 FROM intake_requests LIMIT 1)
       OR EXISTS (SELECT 1 FROM intake_resolution_snapshots LIMIT 1)
       OR EXISTS (SELECT 1 FROM external_identities LIMIT 1) THEN
        RAISE EXCEPTION
            'reset requires empty intake_requests, intake_resolution_snapshots, and external_identities tables; run development reset first';
    END IF;
END $$;

DROP POLICY IF EXISTS intake_requests_tenant_isolation ON intake_requests;
ALTER TABLE intake_requests NO FORCE ROW LEVEL SECURITY;
ALTER TABLE intake_requests DISABLE ROW LEVEL SECURITY;
ALTER TABLE intake_requests DROP CONSTRAINT IF EXISTS intake_requests_completed_work_item_check;

DROP POLICY IF EXISTS intake_resolution_snapshots_tenant_isolation ON intake_resolution_snapshots;
ALTER TABLE intake_resolution_snapshots NO FORCE ROW LEVEL SECURITY;
ALTER TABLE intake_resolution_snapshots DISABLE ROW LEVEL SECURITY;

DROP POLICY IF EXISTS external_identities_tenant_isolation ON external_identities;
ALTER TABLE external_identities NO FORCE ROW LEVEL SECURITY;
ALTER TABLE external_identities DISABLE ROW LEVEL SECURITY;`

const migration023UnifiedIntakeRLSVerifySQL = `DO $verification$
DECLARE
    rls_table TEXT;
    canonical_policy_name TEXT;
    canonical_policy_expression TEXT;
    policy_using TEXT;
    policy_check TEXT;
    policy_roles OID[];
    policy_command "char";
    policy_permissive BOOLEAN;
BEGIN
    canonical_policy_expression := '(tenant_id = (NULLIF(current_setting(''app.current_tenant''::text, true), ''''::text))::bigint)';

    FOR rls_table IN SELECT unnest(ARRAY['intake_requests', 'intake_resolution_snapshots', 'external_identities']) LOOP
        IF to_regclass(format('%I.%I', current_schema(), rls_table)) IS NULL THEN
            RAISE EXCEPTION 'required table % is missing from schema %', rls_table, current_schema();
        END IF;

        IF NOT EXISTS (
            SELECT 1
            FROM pg_class relation
            JOIN pg_namespace namespace ON namespace.oid = relation.relnamespace
            WHERE namespace.nspname = current_schema()
              AND relation.relname = rls_table
              AND relation.relrowsecurity
              AND relation.relforcerowsecurity
        ) THEN
            RAISE EXCEPTION '%.% must have row level security enabled and forced', current_schema(), rls_table;
        END IF;

        canonical_policy_name := rls_table || '_tenant_isolation';
        policy_using := NULL;
        policy_check := NULL;
        policy_roles := NULL;
        policy_command := NULL;
        policy_permissive := NULL;

        SELECT pg_get_expr(policy.polqual, policy.polrelid),
               pg_get_expr(policy.polwithcheck, policy.polrelid),
               policy.polroles,
               policy.polcmd,
               policy.polpermissive
        INTO policy_using, policy_check, policy_roles, policy_command, policy_permissive
        FROM pg_policy policy
        JOIN pg_class relation ON relation.oid = policy.polrelid
        JOIN pg_namespace namespace ON namespace.oid = relation.relnamespace
        WHERE namespace.nspname = current_schema()
          AND relation.relname = rls_table
          AND policy.polname = canonical_policy_name;

        IF policy_using IS NULL OR policy_check IS NULL
           OR policy_using <> canonical_policy_expression
           OR policy_check <> canonical_policy_expression THEN
            RAISE EXCEPTION '%.% must use the authoritative tenant isolation predicate exactly',
                rls_table, canonical_policy_name;
        END IF;

        IF policy_roles <> ARRAY[0::OID] OR policy_command <> '*' OR NOT policy_permissive THEN
            RAISE EXCEPTION '%.% must have canonical PUBLIC/ALL/PERMISSIVE policy attributes',
                rls_table, canonical_policy_name;
        END IF;

        IF (SELECT COUNT(*)
            FROM pg_policy policy
            JOIN pg_class relation ON relation.oid = policy.polrelid
            JOIN pg_namespace namespace ON namespace.oid = relation.relnamespace
            WHERE namespace.nspname = current_schema()
              AND relation.relname = rls_table) <> 1 THEN
            RAISE EXCEPTION '% must have exactly one RLS policy', rls_table;
        END IF;
    END LOOP;

    IF NOT EXISTS (
        SELECT 1
        FROM pg_constraint constraint_relation
        JOIN pg_class table_relation ON table_relation.oid = constraint_relation.conrelid
        JOIN pg_namespace table_schema ON table_schema.oid = table_relation.relnamespace
        WHERE table_schema.nspname = current_schema()
          AND table_relation.relname = 'intake_requests'
          AND constraint_relation.conname = 'intake_requests_completed_work_item_check'
          AND constraint_relation.contype = 'c'
    ) THEN
        RAISE EXCEPTION 'intake_requests_completed_work_item_check constraint is missing from schema %', current_schema();
    END IF;
END $verification$;`

// TestMigration023UnifiedIntakeRLSAssets guards the 023_unified_intake_rls.sql,
// _dev_reset.sql, and _verify.sql files on disk against drifting from what this
// test file pins them to, mirroring the sibling asset-content test pattern
// established by TestMigration021CallbackOptionalDeclaredAssets and
// TestProfessionalExtensionsDropSharedFieldsIsVersioned. Without this, an edit
// to any one of the three files (or to the registered case in migrations.go)
// could silently diverge from the other two with nothing in the suite to catch
// it.
func TestMigration023UnifiedIntakeRLSAssets(t *testing.T) {
	tests := []struct {
		name     string
		filename string
		want     string
	}{
		{
			name:     "apply",
			filename: "023_unified_intake_rls.sql",
			want:     migration023UnifiedIntakeRLSSQL,
		},
		{
			name:     "development reset",
			filename: "023_unified_intake_rls_dev_reset.sql",
			want:     migration023UnifiedIntakeRLSDevResetSQL,
		},
		{
			name:     "verify",
			filename: "023_unified_intake_rls_verify.sql",
			want:     migration023UnifiedIntakeRLSVerifySQL,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			script := readUnifiedIntakeMigrationAsset(t, tt.filename)
			require.Equal(t, normalizeMigrationSQL(tt.want), normalizeMigrationSQL(string(script)))
		})
	}
}

// TestMigration023UnifiedIntakeRLSMatchesRegisteredSQL guards the other
// direction of the same drift: the registered GetMigrationSQL case actually
// applied by the migrator must match the pinned apply-file content above.
func TestMigration023UnifiedIntakeRLSMatchesRegisteredSQL(t *testing.T) {
	registeredSQL := GetMigrationSQL("023_unified_intake_rls")
	require.Equal(t, normalizeMigrationSQL(migration023UnifiedIntakeRLSSQL), normalizeMigrationSQL(registeredSQL))
}

const migration024ServiceCatalogTargetClassAuthoritySQL = `
DO $$
BEGIN
    IF EXISTS (
        SELECT 1 FROM information_schema.columns
        WHERE table_schema = current_schema()
          AND table_name = 'service_catalogs'
          AND column_name = 'itsm_type'
    ) THEN
        IF EXISTS (
            SELECT 1 FROM service_catalogs
            WHERE itsm_type IS NULL
               OR itsm_type NOT IN ('Request', 'Incident', 'Change')
        ) THEN
            RAISE EXCEPTION 'unsupported historical itsm_type value in service_catalogs';
        END IF;

        UPDATE service_catalogs
        SET target_class = CASE itsm_type
            WHEN 'Request' THEN 'service_request_item'
            WHEN 'Incident' THEN 'incident'
            WHEN 'Change' THEN 'change_request'
        END
        WHERE target_class IS NULL
           OR target_class NOT IN ('service_request_item', 'incident', 'change_request');
    END IF;
END $$;

DO $$
BEGIN
    IF EXISTS (
        SELECT 1 FROM service_catalogs
        WHERE target_class IS NULL
           OR target_class NOT IN ('service_request_item', 'incident', 'change_request')
    ) THEN
        RAISE EXCEPTION 'service_catalogs.target_class has invalid or NULL values after backfill';
    END IF;
END $$;

ALTER TABLE service_catalogs ALTER COLUMN target_class SET NOT NULL;
ALTER TABLE service_catalogs DROP CONSTRAINT IF EXISTS service_catalogs_target_class_check;
ALTER TABLE service_catalogs
    ADD CONSTRAINT service_catalogs_target_class_check
    CHECK (target_class IN ('service_request_item', 'incident', 'change_request'));

ALTER TABLE service_catalogs DROP COLUMN IF EXISTS itsm_type;
`

const migration024ServiceCatalogTargetClassAuthorityDevResetSQL = `DO $$
BEGIN
    IF EXISTS (SELECT 1 FROM service_catalogs LIMIT 1) THEN
        RAISE EXCEPTION 'reset requires an empty service_catalogs table; run development reset first';
    END IF;
END $$;

ALTER TABLE service_catalogs ADD COLUMN IF NOT EXISTS itsm_type character varying NOT NULL DEFAULT 'Request';
ALTER TABLE service_catalogs DROP CONSTRAINT IF EXISTS service_catalogs_target_class_check;
ALTER TABLE service_catalogs ALTER COLUMN target_class DROP NOT NULL;`

const migration024ServiceCatalogTargetClassAuthorityVerifySQL = `DO $$
BEGIN
    IF to_regclass(format('%I.service_catalogs', current_schema())) IS NULL THEN
        RAISE EXCEPTION 'required table service_catalogs is missing from schema %', current_schema();
    END IF;

    IF EXISTS (
        SELECT 1
        FROM pg_attribute column_attribute
        JOIN pg_class table_relation ON table_relation.oid = column_attribute.attrelid
        JOIN pg_namespace table_schema ON table_schema.oid = table_relation.relnamespace
        WHERE table_schema.nspname = current_schema()
          AND table_relation.relname = 'service_catalogs'
          AND column_attribute.attname = 'itsm_type'
          AND NOT column_attribute.attisdropped
    ) THEN
        RAISE EXCEPTION 'service_catalogs.itsm_type must be dropped';
    END IF;

    IF NOT EXISTS (
        SELECT 1
        FROM pg_attribute column_attribute
        JOIN pg_class table_relation ON table_relation.oid = column_attribute.attrelid
        JOIN pg_namespace table_schema ON table_schema.oid = table_relation.relnamespace
        WHERE table_schema.nspname = current_schema()
          AND table_relation.relname = 'service_catalogs'
          AND column_attribute.attname = 'target_class'
          AND column_attribute.attnotnull
          AND NOT column_attribute.attisdropped
    ) THEN
        RAISE EXCEPTION 'service_catalogs.target_class must be NOT NULL';
    END IF;

    IF NOT EXISTS (
        SELECT 1
        FROM pg_constraint constraint_relation
        JOIN pg_class table_relation ON table_relation.oid = constraint_relation.conrelid
        JOIN pg_namespace table_schema ON table_schema.oid = table_relation.relnamespace
        WHERE table_schema.nspname = current_schema()
          AND table_relation.relname = 'service_catalogs'
          AND constraint_relation.conname = 'service_catalogs_target_class_check'
          AND constraint_relation.contype = 'c'
          AND pg_get_constraintdef(constraint_relation.oid) =
              'CHECK (((target_class)::text = ANY ((ARRAY[''service_request_item''::character varying, ''incident''::character varying, ''change_request''::character varying])::text[])))'
    ) THEN
        RAISE EXCEPTION 'service_catalogs_target_class_check constraint is missing or does not match the authoritative definition';
    END IF;

    IF EXISTS (
        SELECT 1 FROM service_catalogs
        WHERE target_class IS NULL
           OR target_class NOT IN ('service_request_item', 'incident', 'change_request')
    ) THEN
        RAISE EXCEPTION 'service_catalogs contains rows with invalid target_class values';
    END IF;
END $$;`

// TestMigration024ServiceCatalogTargetClassAuthorityAssets guards the
// 024_service_catalog_target_class_authority.sql, _dev_reset.sql, and
// _verify.sql files on disk against drifting from what this test file pins
// them to, mirroring TestMigration023UnifiedIntakeRLSAssets.
func TestMigration024ServiceCatalogTargetClassAuthorityAssets(t *testing.T) {
	tests := []struct {
		name     string
		filename string
		want     string
	}{
		{
			name:     "apply",
			filename: "024_service_catalog_target_class_authority.sql",
			want:     migration024ServiceCatalogTargetClassAuthoritySQL,
		},
		{
			name:     "development reset",
			filename: "024_service_catalog_target_class_authority_dev_reset.sql",
			want:     migration024ServiceCatalogTargetClassAuthorityDevResetSQL,
		},
		{
			name:     "verify",
			filename: "024_service_catalog_target_class_authority_verify.sql",
			want:     migration024ServiceCatalogTargetClassAuthorityVerifySQL,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			script := readUnifiedIntakeMigrationAsset(t, tt.filename)
			require.Equal(t, normalizeMigrationSQL(tt.want), normalizeMigrationSQL(string(script)))
		})
	}
}

// TestMigration024ServiceCatalogTargetClassAuthorityMatchesRegisteredSQL guards
// the other direction of the same drift: the registered GetMigrationSQL case
// actually applied by the migrator must match the pinned apply-file content
// above.
func TestMigration024ServiceCatalogTargetClassAuthorityMatchesRegisteredSQL(t *testing.T) {
	registeredSQL := GetMigrationSQL("024_service_catalog_target_class_authority")
	require.Equal(t, normalizeMigrationSQL(migration024ServiceCatalogTargetClassAuthoritySQL), normalizeMigrationSQL(registeredSQL))
}

const migration025ExternalIdentityVersionSQL = `
ALTER TABLE external_identities
    ADD COLUMN IF NOT EXISTS version bigint NOT NULL DEFAULT 1;
ALTER TABLE external_identities
    DROP CONSTRAINT IF EXISTS external_identities_version_positive;
ALTER TABLE external_identities
    ADD CONSTRAINT external_identities_version_positive CHECK (version > 0);
`

const migration025ExternalIdentityVersionDevResetSQL = `ALTER TABLE external_identities
    DROP CONSTRAINT IF EXISTS external_identities_version_positive;
ALTER TABLE external_identities
    DROP COLUMN IF EXISTS version;`

const migration025ExternalIdentityVersionVerifySQL = `DO $$
BEGIN
    IF NOT EXISTS (
        SELECT 1
        FROM pg_attribute column_attribute
        JOIN pg_class table_relation
            ON table_relation.oid = column_attribute.attrelid
        JOIN pg_namespace table_schema
            ON table_schema.oid = table_relation.relnamespace
        JOIN pg_attrdef column_default
            ON column_default.adrelid = table_relation.oid
           AND column_default.adnum = column_attribute.attnum
        WHERE table_schema.nspname = current_schema()
          AND table_relation.relname = 'external_identities'
          AND table_relation.relkind IN ('r', 'p')
          AND column_attribute.attname = 'version'
          AND column_attribute.atttypid = 'bigint'::regtype
          AND column_attribute.attnotnull
          AND NOT column_attribute.attisdropped
          AND pg_get_expr(column_default.adbin, column_default.adrelid) = '1'
    ) THEN
        RAISE EXCEPTION
            'external_identities.version must be bigint NOT NULL DEFAULT 1';
    END IF;

    IF NOT EXISTS (
        SELECT 1
        FROM pg_constraint constraint_relation
        JOIN pg_class table_relation ON table_relation.oid = constraint_relation.conrelid
        JOIN pg_namespace table_schema ON table_schema.oid = table_relation.relnamespace
        WHERE table_schema.nspname = current_schema()
          AND table_relation.relname = 'external_identities'
          AND constraint_relation.conname = 'external_identities_version_positive'
          AND constraint_relation.contype = 'c'
    ) THEN
        RAISE EXCEPTION
            'external_identities_version_positive constraint is missing from schema %', current_schema();
    END IF;
END $$;`

// TestMigration025ExternalIdentityVersionAssets guards the
// 025_external_identity_version.sql, _dev_reset.sql, and _verify.sql files on
// disk against drifting from what this test file pins them to, mirroring the
// same sibling asset-content test pattern as TestMigration023UnifiedIntakeRLSAssets.
func TestMigration025ExternalIdentityVersionAssets(t *testing.T) {
	tests := []struct {
		name     string
		filename string
		want     string
	}{
		{
			name:     "apply",
			filename: "025_external_identity_version.sql",
			want:     migration025ExternalIdentityVersionSQL,
		},
		{
			name:     "development reset",
			filename: "025_external_identity_version_dev_reset.sql",
			want:     migration025ExternalIdentityVersionDevResetSQL,
		},
		{
			name:     "verify",
			filename: "025_external_identity_version_verify.sql",
			want:     migration025ExternalIdentityVersionVerifySQL,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			script := readUnifiedIntakeMigrationAsset(t, tt.filename)
			require.Equal(t, normalizeMigrationSQL(tt.want), normalizeMigrationSQL(string(script)))
		})
	}
}

// TestMigration025ExternalIdentityVersionMatchesRegisteredSQL guards the other
// direction of the same drift: the registered GetMigrationSQL case actually
// applied by the migrator must match the pinned apply-file content above.
func TestMigration025ExternalIdentityVersionMatchesRegisteredSQL(t *testing.T) {
	registeredSQL := GetMigrationSQL("025_external_identity_version")
	require.Equal(t, normalizeMigrationSQL(migration025ExternalIdentityVersionSQL), normalizeMigrationSQL(registeredSQL))
}

func readUnifiedIntakeMigrationAsset(t *testing.T, filename string) []byte {
	t.Helper()
	script, err := os.ReadFile("../migrations/" + filename)
	require.NoError(t, err)
	return script
}
