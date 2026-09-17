package migration

import (
	"testing"

	"github.com/stretchr/testify/require"
)

func TestCTIGovernanceMigrationIsAppendedOrdinaryWithoutRetirement(t *testing.T) {
	require.Equal(t, "048_cti_governance", CTIGovernanceVersion)
	require.NotEmpty(t, GetMigrationSQL(CTIGovernanceVersion))
	require.Contains(t, GetMigrationSQL(CTIGovernanceVersion), ctiGovernanceVerifySQL)
	found := false
	for _, definition := range ControlledMigrationCatalog() {
		if definition.Migration.Version != CTIGovernanceVersion {
			continue
		}
		found = true
		require.Equal(t, StageOrdinary, definition.Stage)
		require.Contains(t, definition.Requires, "047_bpmn_assignment_source")
		require.Contains(t, definition.Requires, WorkItemPrepareVersion)
		require.NotContains(t, definition.Requires, WorkItemRetireVersion)
	}
	require.True(t, found)
	// Appending after 047 is what makes a previously retired ledger upgradeable.
	last := RegisteredMigrations[len(RegisteredMigrations)-2]
	require.Equal(t, CTIGovernanceVersion, last.Version)
	require.NotEmpty(t, last.RollbackSQL, "development reset must be explicit, never a silent drop")
}

func TestCTIGovernanceMigrationPreflightsAndScopesStructure(t *testing.T) {
	sql := GetMigrationSQL(CTIGovernanceVersion)
	for _, required := range []string{
		"duplicated (tenant_id, code)",
		"outside levels 1..3",
		"missing or cross-tenant parent",
		"level inconsistent with their parent",
		"deeper than three levels or form a cycle",
		"parent cycle",
		"cti_governance_v1",
		"DROP INDEX IF EXISTS ticketcategory_code",
		"CREATE UNIQUE INDEX IF NOT EXISTS ticketcategory_tenant_id_code",
		"ADD COLUMN IF NOT EXISTS default_ticket_category_id integer",
		"service_catalogs_ticket_categories_default_catalogs",
		"system_configs_cti_governance_reserved_key_uq",
	} {
		require.Contains(t, sql, required)
	}
	// Structural preparation only: no data fixing, no gate activation, no history rewrite.
	for _, forbidden := range []string{
		"UPDATE ticket_categories", "DELETE FROM ticket_categories", "UPDATE service_catalogs SET default_ticket_category_id",
		"INSERT INTO system_configs", "DELETE FROM tickets", "ALTER TABLE tickets",
	} {
		require.NotContains(t, sql, forbidden)
	}
}

func TestCTIGovernanceDevelopmentResetGuardsTableGlobalUniqueness(t *testing.T) {
	reset := ctiGovernanceDevelopmentResetSQL
	require.Contains(t, reset, "shared across tenants")
	require.Contains(t, reset, "DROP COLUMN IF EXISTS default_ticket_category_id")
	require.Contains(t, reset, "system_configs_cti_governance_reserved_key_uq")
}
