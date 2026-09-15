package migration

import (
	"os"
	"slices"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestAccessPolicyResultMigrationRegistered(t *testing.T) {
	const version = "030_catalog_access_policy_result"
	require.NotEmpty(t, GetMigrationSQL(version))
	require.True(t, slices.ContainsFunc(RegisteredMigrations, func(m Migration) bool { return m.Version == version }))
	asset, err := os.ReadFile("../migrations/" + version + ".sql")
	require.NoError(t, err)
	require.Equal(t, strings.TrimSpace(GetMigrationSQL(version)), strings.TrimSpace(string(asset)))
}

func TestKafAccessRequestDigestMigrationRegistered(t *testing.T) {
	const version = "031_kaf_action_request_digest"
	require.True(t, slices.ContainsFunc(RegisteredMigrations, func(m Migration) bool { return m.Version == version }))
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
