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
	require.Equal(t, version, RegisteredMigrations[len(RegisteredMigrations)-1].Version)
	asset, err := os.ReadFile("../migrations/" + version + ".sql")
	require.NoError(t, err)
	require.Equal(t, strings.TrimSpace(GetMigrationSQL(version)), strings.TrimSpace(string(asset)))
}
