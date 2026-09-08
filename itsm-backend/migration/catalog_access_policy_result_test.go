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
	require.Equal(t, version, RegisteredMigrations[len(RegisteredMigrations)-2].Version)
	asset, err := os.ReadFile("../migrations/" + version + ".sql")
	require.NoError(t, err)
	require.Equal(t, strings.TrimSpace(GetMigrationSQL(version)), strings.TrimSpace(string(asset)))
}

func TestKafAccessRequestDigestMigrationRegistered(t *testing.T) {
	const version = "031_kaf_action_request_digest"
	require.Equal(t, version, RegisteredMigrations[len(RegisteredMigrations)-1].Version)
	asset, err := os.ReadFile("../migrations/" + version + ".sql")
	require.NoError(t, err)
	require.Contains(t, string(asset), strings.TrimSpace(GetMigrationSQL(version)))
}
