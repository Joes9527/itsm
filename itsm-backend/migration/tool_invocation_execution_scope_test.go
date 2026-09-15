package migration

import (
	"testing"

	"github.com/stretchr/testify/require"
)

func TestToolInvocationScopeMigrationRequiresPriorPreparation(t *testing.T) {
	catalog := ControlledMigrationCatalog()
	var prefix []Migration
	for _, d := range catalog {
		if d.Migration.Version == ToolInvocationExecutionScopeVersion {
			break
		}
		prefix = append(prefix, d.Migration)
	}
	plan, err := PlanMigrations(catalog, controlledReceipts(prefix), OpUp, nil)
	require.NoError(t, err)
	require.Len(t, plan.Executable, 7)
	require.Equal(t, AuthTokenStateVersion, plan.Executable[5].Version)
	require.Equal(t, ToolInvocationExecutionScopeVersion, plan.Executable[0].Version)
	require.NotEmpty(t, GetMigrationSQL(ToolInvocationExecutionScopeVersion))
	require.Equal(t, ToolExecutionAuthorityLockVersion, plan.Executable[1].Version)
	for _, removed := range []string{WorkItemPrepareVersion, CandidateExecutionScopeVersion, SLAAlertNotificationVersion} {
		var invalid []Migration
		for _, m := range prefix {
			if m.Version != removed {
				invalid = append(invalid, m)
			}
		}
		invalid = append(invalid, Migration{Version: ToolInvocationExecutionScopeVersion})
		_, err = PlanMigrations(catalog, controlledReceipts(invalid), OpUp, nil)
		require.Error(t, err)
	}
}
