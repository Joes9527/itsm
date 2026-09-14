package migration

import (
	"testing"

	"github.com/stretchr/testify/require"
)

func TestNotificationTargetMigrationRequiresPriorPreparation(t *testing.T) {
	catalog := ControlledMigrationCatalog()
	var prefix []Migration
	for _, d := range catalog {
		if d.Migration.Version == NotificationConnectorTargetVersion {
			break
		}
		prefix = append(prefix, d.Migration)
	}
	plan, err := PlanMigrations(catalog, controlledReceipts(prefix), OpUp, nil)
	require.NoError(t, err)
	require.Len(t, plan.Executable, 3)
	require.Equal(t, AuthTokenStateVersion, plan.Executable[2].Version)
	require.Equal(t, NotificationConnectorTargetVersion, plan.Executable[0].Version)
	require.NotEmpty(t, GetMigrationSQL(NotificationConnectorTargetVersion))
	for _, removed := range []string{WorkItemPrepareVersion, CandidateExecutionScopeVersion, SLAAlertNotificationVersion, ToolInvocationExecutionScopeVersion, ToolExecutionAuthorityLockVersion, ToolExecutionAuthorizationLockVersion} {
		var invalid []Migration
		for _, m := range prefix {
			if m.Version != removed {
				invalid = append(invalid, m)
			}
		}
		invalid = append(invalid, Migration{Version: NotificationConnectorTargetVersion})
		_, err := PlanMigrations(catalog, controlledReceipts(invalid), OpUp, nil)
		require.Error(t, err)
	}
}
