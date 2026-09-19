package migration

import (
	"testing"

	"github.com/stretchr/testify/require"
)

func TestSLANotificationMigrationRequiresCandidatePreparation(t *testing.T) {
	catalog := ControlledMigrationCatalog()
	var prefix []Migration
	for _, d := range catalog {
		if d.Migration.Version == SLAAlertNotificationVersion {
			break
		}
		prefix = append(prefix, d.Migration)
	}
	plan, err := PlanMigrations(catalog, controlledReceipts(prefix), OpUp, nil)
	require.NoError(t, err)
	require.Len(t, plan.Executable, 12)
	require.Equal(t, AuthTokenStateVersion, plan.Executable[6].Version)
	require.Equal(t, SLAAlertNotificationVersion, plan.Executable[0].Version)
	require.Equal(t, CTIGovernanceVersion, plan.Executable[8].Version)
	require.NotEmpty(t, GetMigrationSQL(SLAAlertNotificationVersion))
	for _, removed := range []string{CandidateExecutionScopeVersion, WorkItemPrepareVersion} {
		var invalid []Migration
		for _, m := range prefix {
			if m.Version != removed {
				invalid = append(invalid, m)
			}
		}
		invalid = append(invalid, Migration{Version: SLAAlertNotificationVersion})
		_, err := PlanMigrations(catalog, controlledReceipts(invalid), OpUp, nil)
		require.Error(t, err)
	}
}
