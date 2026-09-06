package migration

import (
	"github.com/stretchr/testify/require"
	"os"
	"strings"
	"testing"
)

func TestCatalogAuthorityOperationalSQLMatchesStream(t *testing.T) {
	const version = "029_catalog_target_class_authority"
	asset, err := os.ReadFile("../migrations/" + version + ".sql")
	require.NoError(t, err)
	require.Equal(t, strings.TrimSpace(GetMigrationSQL(version)), strings.TrimSpace(string(asset)))
	verify, err := os.ReadFile("../migrations/" + version + "_verify.sql")
	require.NoError(t, err)
	require.Equal(t, strings.TrimSpace(catalogTargetClassAuthorityVerifySQL), strings.TrimSpace(string(verify)))
	require.Equal(t, version, RegisteredMigrations[len(RegisteredMigrations)-1].Version)
}
