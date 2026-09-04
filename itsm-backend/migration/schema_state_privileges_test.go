package migration

import (
	"context"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestLoadSchemaStateRolesRequiresDistinctNonemptyCategories(t *testing.T) {
	const migrationSecret = "migration_sensitive_identity"
	const runtimeSecret = "runtime_sensitive_identity"

	roles, err := LoadSchemaStateRoles(func(name string) string {
		switch name {
		case "ITSM_MIGRATION_DB_USER":
			return "  " + migrationSecret + "  "
		case "ITSM_RUNTIME_DB_USER":
			return "  " + runtimeSecret + "  "
		default:
			return ""
		}
	})
	require.NoError(t, err)
	require.Equal(t, SchemaStateRoles{MigrationRole: migrationSecret, RuntimeRole: runtimeSecret}, roles)

	tests := map[string]func(string) string{
		"missing migration role": func(name string) string {
			if name == "ITSM_RUNTIME_DB_USER" {
				return runtimeSecret
			}
			return ""
		},
		"missing runtime role": func(name string) string {
			if name == "ITSM_MIGRATION_DB_USER" {
				return migrationSecret
			}
			return ""
		},
		"identical roles": func(string) string { return migrationSecret },
	}
	for name, getenv := range tests {
		t.Run(name, func(t *testing.T) {
			_, err := LoadSchemaStateRoles(getenv)
			require.Error(t, err)
			require.NotContains(t, err.Error(), migrationSecret)
			require.NotContains(t, err.Error(), runtimeSecret)
			require.True(t,
				strings.Contains(err.Error(), "migration role") || strings.Contains(err.Error(), "runtime role"),
			)
		})
	}
}

func TestApplySchemaStatePrivilegesRejectsInvalidInputsWithoutDisclosure(t *testing.T) {
	const migrationSecret = "migration_sensitive_identity"
	const runtimeSecret = "runtime_sensitive_identity"

	tests := map[string]SchemaStateRoles{
		"missing migration role": {RuntimeRole: runtimeSecret},
		"missing runtime role":   {MigrationRole: migrationSecret},
		"identical roles":        {MigrationRole: migrationSecret, RuntimeRole: migrationSecret},
	}
	for name, roles := range tests {
		t.Run(name, func(t *testing.T) {
			err := ApplySchemaStatePrivileges(context.Background(), nil, roles)
			require.Error(t, err)
			require.NotContains(t, err.Error(), migrationSecret)
			require.NotContains(t, err.Error(), runtimeSecret)
		})
	}

	err := ApplySchemaStatePrivileges(context.Background(), nil, SchemaStateRoles{
		MigrationRole: migrationSecret,
		RuntimeRole:   runtimeSecret,
	})
	require.ErrorContains(t, err, "database")
	require.NotContains(t, err.Error(), migrationSecret)
	require.NotContains(t, err.Error(), runtimeSecret)
}
