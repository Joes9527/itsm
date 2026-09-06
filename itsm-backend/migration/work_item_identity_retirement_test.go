package migration

import (
	"github.com/stretchr/testify/require"
	"os"
	"strings"
	"testing"
)

func TestIdentityRetirementOperationalSQLMatchesRegisteredMigration(t *testing.T) {
	const version = "027_work_item_identity_field_retirement"
	asset, err := os.ReadFile("../migrations/" + version + ".sql")
	require.NoError(t, err)
	require.Equal(t, strings.TrimSpace(GetMigrationSQL(version)), strings.TrimSpace(string(asset)))
	verify, err := os.ReadFile("../migrations/" + version + "_verify.sql")
	require.NoError(t, err)
	require.Equal(t, strings.TrimSpace(workItemIdentityRetirementVerifySQL), strings.TrimSpace(string(verify)))
	require.Equal(t, version, RegisteredMigrations[len(RegisteredMigrations)-3].Version)
}
