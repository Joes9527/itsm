package migration

import (
	"context"
	"database/sql"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"
)

var _ func(context.Context, *sql.DB, SchemaStateRoles) error = ApplySchemaStatePrivileges
var _ SchemaStatePrivilegeApplier = ApplySchemaStatePrivilegesOnConnection

func TestLoadSchemaStateRolesRequiresDistinctNonemptyCategories(t *testing.T) {
	const migrationSecret = "migration_sensitive_identity"
	const runtimeSecret = "runtime_sensitive_identity"
	const bootstrapSecret = "bootstrap_sensitive_identity"

	roles, err := LoadSchemaStateRoles(func(name string) string {
		switch name {
		case "ITSM_MIGRATION_DB_USER":
			return "  " + migrationSecret + "  "
		case "ITSM_RUNTIME_DB_USER":
			return "  " + runtimeSecret + "  "
		case "ITSM_BOOTSTRAP_DB_USER":
			return "  " + bootstrapSecret + "  "
		default:
			return ""
		}
	})
	require.NoError(t, err)
	require.Equal(t, SchemaStateRoles{
		MigrationRole: migrationSecret,
		RuntimeRole:   runtimeSecret,
		BootstrapRole: bootstrapSecret,
	}, roles)

	tests := map[string]func(string) string{
		"missing migration role": func(name string) string {
			if name == "ITSM_RUNTIME_DB_USER" {
				return runtimeSecret
			}
			if name == "ITSM_BOOTSTRAP_DB_USER" {
				return bootstrapSecret
			}
			return ""
		},
		"missing runtime role": func(name string) string {
			if name == "ITSM_MIGRATION_DB_USER" {
				return migrationSecret
			}
			if name == "ITSM_BOOTSTRAP_DB_USER" {
				return bootstrapSecret
			}
			return ""
		},
		"missing bootstrap role": func(name string) string {
			if name == "ITSM_MIGRATION_DB_USER" {
				return migrationSecret
			}
			if name == "ITSM_RUNTIME_DB_USER" {
				return runtimeSecret
			}
			return ""
		},
		"identical migration and runtime roles": func(name string) string {
			if name == "ITSM_BOOTSTRAP_DB_USER" {
				return bootstrapSecret
			}
			return migrationSecret
		},
		"identical bootstrap and runtime roles": func(name string) string {
			if name == "ITSM_MIGRATION_DB_USER" {
				return migrationSecret
			}
			return runtimeSecret
		},
	}
	for name, getenv := range tests {
		t.Run(name, func(t *testing.T) {
			_, err := LoadSchemaStateRoles(getenv)
			require.Error(t, err)
			require.NotContains(t, err.Error(), migrationSecret)
			require.NotContains(t, err.Error(), runtimeSecret)
			require.NotContains(t, err.Error(), bootstrapSecret)
			require.True(t,
				strings.Contains(err.Error(), "migration role") ||
					strings.Contains(err.Error(), "runtime role") ||
					strings.Contains(err.Error(), "bootstrap role"),
			)
		})
	}
}

func TestSchemaStateRolesRejectNoncanonicalIdentifiersWithoutDisclosure(t *testing.T) {
	const sensitiveMixedCase = "Runtime_SecretRole"
	for name, roles := range map[string]SchemaStateRoles{
		"mixed case": {
			MigrationRole: "migration_role", RuntimeRole: sensitiveMixedCase, BootstrapRole: "bootstrap_role",
		},
		"leading digit": {
			MigrationRole: "1migration", RuntimeRole: "runtime_role", BootstrapRole: "bootstrap_role",
		},
		"punctuation": {
			MigrationRole: "migration_role", RuntimeRole: "runtime_role", BootstrapRole: "bootstrap-role",
		},
		"identifier too long": {
			MigrationRole: strings.Repeat("m", 64), RuntimeRole: "runtime_role", BootstrapRole: "bootstrap_role",
		},
	} {
		t.Run(name, func(t *testing.T) {
			err := validateSchemaStateRoles(roles)
			require.Error(t, err)
			require.NotContains(t, err.Error(), sensitiveMixedCase)
			require.NotContains(t, err.Error(), "1migration")
			require.NotContains(t, err.Error(), "bootstrap-role")
			require.NotContains(t, err.Error(), strings.Repeat("m", 64))
		})
	}
}

func TestApplySchemaStatePrivilegesRejectsInvalidInputsWithoutDisclosure(t *testing.T) {
	const migrationSecret = "migration_sensitive_identity"
	const runtimeSecret = "runtime_sensitive_identity"
	const bootstrapSecret = "bootstrap_sensitive_identity"

	tests := map[string]SchemaStateRoles{
		"missing migration role": {RuntimeRole: runtimeSecret, BootstrapRole: bootstrapSecret},
		"missing runtime role":   {MigrationRole: migrationSecret, BootstrapRole: bootstrapSecret},
		"missing bootstrap role": {MigrationRole: migrationSecret, RuntimeRole: runtimeSecret},
		"identical roles": {
			MigrationRole: migrationSecret, RuntimeRole: migrationSecret, BootstrapRole: bootstrapSecret,
		},
	}
	for name, roles := range tests {
		t.Run(name, func(t *testing.T) {
			err := ApplySchemaStatePrivileges(context.Background(), nil, roles)
			require.Error(t, err)
			require.NotContains(t, err.Error(), migrationSecret)
			require.NotContains(t, err.Error(), runtimeSecret)
			require.NotContains(t, err.Error(), bootstrapSecret)
		})
	}

	err := ApplySchemaStatePrivileges(context.Background(), nil, SchemaStateRoles{
		MigrationRole: migrationSecret,
		RuntimeRole:   runtimeSecret,
		BootstrapRole: bootstrapSecret,
	})
	require.ErrorContains(t, err, "database")
	require.NotContains(t, err.Error(), migrationSecret)
	require.NotContains(t, err.Error(), runtimeSecret)
	require.NotContains(t, err.Error(), bootstrapSecret)
}
