package migration

import (
	"github.com/stretchr/testify/require"
	"testing"
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
	require.Len(t, plan.Executable, 1)
	require.Equal(t, ToolInvocationExecutionScopeVersion, plan.Executable[0].Version)
	require.NotEmpty(t, GetMigrationSQL(ToolInvocationExecutionScopeVersion))
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
