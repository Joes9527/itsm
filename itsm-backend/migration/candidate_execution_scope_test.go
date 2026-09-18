package migration

import (
	"testing"

	"github.com/stretchr/testify/require"
)

func TestCandidateScopeMigrationDoesNotRequireRetirement(t *testing.T) {
	catalog := ControlledMigrationCatalog()
	var applied []Migration
	for _, d := range catalog {
		if d.Migration.Version == "039_candidate_execution_scope" {
			break
		}
		if d.Stage != StageRetire {
			applied = append(applied, d.Migration)
		}
	}
	p, err := PlanMigrations(catalog, controlledReceipts(applied), OpUp, nil)
	require.NoError(t, err)
	require.Len(t, p.Executable, 11)
	require.Equal(t, AuthTokenStateVersion, p.Executable[7].Version)
	require.Equal(t, ToolExecutionAuthorityLockVersion, p.Executable[3].Version)
	require.Equal(t, "039_candidate_execution_scope", p.Executable[0].Version)
	require.Equal(t, CTIGovernanceVersion, p.Executable[9].Version)
	require.Equal(t, DepartmentCodeTenantUniqueVersion, p.Executable[10].Version)
	require.NotEmpty(t, GetMigrationSQL("039_candidate_execution_scope"))
	for _, d := range catalog {
		if d.Migration.Version == "039_candidate_execution_scope" {
			require.NotContains(t, d.Requires, WorkItemRetireVersion)
		}
	}
}

func TestCandidateScopeReceiptRequiresPreparation(t *testing.T) {
	applied := append(frozenHistoricalMigrations(), Migration{Version: "039_candidate_execution_scope"})
	_, err := PlanMigrations(ControlledMigrationCatalog(), controlledReceipts(applied), OpUp, nil)
	require.Error(t, err)
}

func TestCandidateScopeUpgradeAcceptsPreviouslyRetiredLedger(t *testing.T) {
	// Use the immutable pre-039 stream, not the newly generated ordinary catalog.
	applied := frozenHistoricalMigrations()
	applied = append(applied, Migration{Version: WorkItemPrepareVersion}, Migration{Version: WorkItemRetireVersion})
	p, err := PlanMigrations(ControlledMigrationCatalog(), controlledReceipts(applied), OpUp, nil)
	require.NoError(t, err)
	require.Len(t, p.Executable, 11)
	require.Equal(t, AuthTokenStateVersion, p.Executable[7].Version)
	require.Equal(t, ToolExecutionAuthorityLockVersion, p.Executable[3].Version)
	require.Equal(t, "039_candidate_execution_scope", p.Executable[0].Version)
	require.Equal(t, CTIGovernanceVersion, p.Executable[9].Version)
	require.Equal(t, DepartmentCodeTenantUniqueVersion, p.Executable[10].Version)
}
