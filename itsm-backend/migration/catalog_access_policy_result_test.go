package migration

import (
	"github.com/stretchr/testify/require"
	"os"
	"strings"
	"testing"
)

func TestAccessPolicyResultMigrationRegistered(t *testing.T) {
	const version = "030_catalog_access_policy_result"
	require.NotEmpty(t, GetMigrationSQL(version))
	index := migrationVersionIndex(t, version)
	require.Equal(t, "031_kaf_action_request_digest", RegisteredMigrations[index+1].Version)
	asset, err := os.ReadFile("../migrations/" + version + ".sql")
	require.NoError(t, err)
	require.Equal(t, strings.TrimSpace(GetMigrationSQL(version)), strings.TrimSpace(string(asset)))
}

func TestKafAccessRequestDigestMigrationRegistered(t *testing.T) {
	const version = "031_kaf_action_request_digest"
	index := migrationVersionIndex(t, version)
	require.Equal(t, "030_catalog_access_policy_result", RegisteredMigrations[index-1].Version)
	require.Equal(t, "032_bpmn_assignment_source", RegisteredMigrations[index+1].Version)
	asset, err := os.ReadFile("../migrations/" + version + ".sql")
	require.NoError(t, err)
	require.Contains(t, string(asset), strings.TrimSpace(GetMigrationSQL(version)))
}

func migrationVersionIndex(t *testing.T, version string) int {
	t.Helper()
	for index := range RegisteredMigrations {
		if RegisteredMigrations[index].Version == version {
			return index
		}
	}
	require.FailNow(t, "migration version is not registered", version)
	return -1
}
