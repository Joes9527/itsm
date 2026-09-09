package migration

import (
	"github.com/stretchr/testify/require"
	"os"
	"slices"
	"strings"
	"testing"
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
