package migration

import (
	"github.com/stretchr/testify/require"
	"os"
	"strings"
	"testing"
)

func TestServiceRequestAuthorityOperationalSQLMatchesStream(t *testing.T) {
	const version = "028_service_request_work_item_authority"
	asset, err := os.ReadFile("../migrations/" + version + ".sql")
	require.NoError(t, err)
	require.Equal(t, strings.TrimSpace(GetMigrationSQL(version)), strings.TrimSpace(string(asset)))
	verify, err := os.ReadFile("../migrations/" + version + "_verify.sql")
	require.NoError(t, err)
	require.Equal(t, strings.TrimSpace(serviceRequestWorkItemAuthorityVerifySQL), strings.TrimSpace(string(verify)))
	require.Equal(t, version, RegisteredMigrations[len(RegisteredMigrations)-2].Version)
}
