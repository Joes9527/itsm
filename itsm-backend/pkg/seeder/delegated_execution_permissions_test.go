package seeder

import (
	"testing"

	"github.com/stretchr/testify/require"
)

func TestDelegatedExecutionRepairPermissionsAreNotInBroadRoleSet(t *testing.T) {
	permissions := allPermissionCodes()

	for _, code := range []string{
		"delegated_execution:view",
		"delegated_execution:reconcile",
		"delegated_execution:requeue",
	} {
		require.NotContains(t, permissions, code)
	}
}
