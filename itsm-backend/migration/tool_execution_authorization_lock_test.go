package migration

import (
	"testing"

	"github.com/stretchr/testify/require"
)

func TestToolAuthorizationLockMigrationRequiresPriorPreparation(t *testing.T) {
	catalog := ControlledMigrationCatalog()
	var prefix []Migration
	for _, d := range catalog {
		if d.Migration.Version == ToolExecutionAuthorizationLockVersion {
			break
		}
		prefix = append(prefix, d.Migration)
	}
	plan, err := PlanMigrations(catalog, controlledReceipts(prefix), OpUp, nil)
	require.NoError(t, err)
	require.Len(t, plan.Executable, 8)
	require.Equal(t, AuthTokenStateVersion, plan.Executable[3].Version)
	require.Equal(t, ToolExecutionAuthorizationLockVersion, plan.Executable[0].Version)
	require.Equal(t, CTIGovernanceVersion, plan.Executable[5].Version)
	require.NotEmpty(t, GetMigrationSQL(ToolExecutionAuthorizationLockVersion))
	for _, removed := range []string{WorkItemPrepareVersion, CandidateExecutionScopeVersion, SLAAlertNotificationVersion, ToolInvocationExecutionScopeVersion, ToolExecutionAuthorityLockVersion} {
		var invalid []Migration
		for _, m := range prefix {
			if m.Version != removed {
				invalid = append(invalid, m)
			}
		}
		invalid = append(invalid, Migration{Version: ToolExecutionAuthorizationLockVersion})
		_, err = PlanMigrations(catalog, controlledReceipts(invalid), OpUp, nil)
		require.Error(t, err)
	}
}
