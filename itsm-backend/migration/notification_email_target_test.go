package migration

import (
	"testing"

	"github.com/stretchr/testify/require"
)

func TestNotificationEmailTargetMigrationRequiresPriorPreparation(t *testing.T) {
	catalog := ControlledMigrationCatalog()
	var prefix []Migration
	for _, d := range catalog {
		if d.Migration.Version == NotificationEmailTargetVersion {
			break
		}
		prefix = append(prefix, d.Migration)
	}
	plan, err := PlanMigrations(catalog, controlledReceipts(prefix), OpUp, nil)
	require.NoError(t, err)
	require.Len(t, plan.Executable, 5)
	require.Equal(t, AuthTokenStateVersion, plan.Executable[1].Version)
	require.Equal(t, NotificationEmailTargetVersion, plan.Executable[0].Version)
	require.Equal(t, CTIGovernanceVersion, plan.Executable[3].Version)
	require.NotEmpty(t, GetMigrationSQL(NotificationEmailTargetVersion))
	for _, removed := range []string{WorkItemPrepareVersion, CandidateExecutionScopeVersion, SLAAlertNotificationVersion, ToolInvocationExecutionScopeVersion, ToolExecutionAuthorityLockVersion, ToolExecutionAuthorizationLockVersion, NotificationConnectorTargetVersion} {
		var invalid []Migration
		for _, m := range prefix {
			if m.Version != removed {
				invalid = append(invalid, m)
			}
		}
		invalid = append(invalid, Migration{Version: NotificationEmailTargetVersion})
		_, err := PlanMigrations(catalog, controlledReceipts(invalid), OpUp, nil)
		require.Error(t, err)
	}
}
