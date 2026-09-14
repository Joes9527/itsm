package migration

import (
	"testing"

	"github.com/stretchr/testify/require"
)

func TestAuthTokenStateMigrationFollowsCurrentSchemaWithoutRetirement(t *testing.T) {
	const version = "046_auth_token_state"
	require.NotEmpty(t, GetMigrationSQL(version))
	found := false
	for _, definition := range ControlledMigrationCatalog() {
		if definition.Migration.Version != version {
			continue
		}
		found = true
		require.Equal(t, StageOrdinary, definition.Stage)
		require.Contains(t, definition.Requires, NotificationEmailTargetVersion)
		require.Contains(t, definition.Requires, WorkItemPrepareVersion)
		require.NotContains(t, definition.Requires, WorkItemRetireVersion)
	}
	require.True(t, found)
}
