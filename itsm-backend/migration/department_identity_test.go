package migration

import (
	"strings"
	"testing"

	"github.com/stretchr/testify/require"
)

// 049 必须真的进入 canonical 流，并且能取到 SQL——否则"编码唯一"只写在文档里。
func TestDepartmentCodeTenantUniqueMigrationIsRegistered(t *testing.T) {
	var found bool
	for _, m := range PostSchemaMigrations() {
		if m.Version == DepartmentCodeTenantUniqueVersion {
			found = true
			break
		}
	}
	require.True(t, found, "%s must be part of the canonical stream", DepartmentCodeTenantUniqueVersion)
}

func TestDepartmentCodeTenantUniqueMigrationSQLTargetsTheTenantScopedIndex(t *testing.T) {
	sql := GetMigrationSQL(DepartmentCodeTenantUniqueVersion)
	require.Contains(t, sql, "idx_departments_tenant_code")
	require.Contains(t, sql, "(tenant_id, code)")
	require.Contains(t, strings.ToLower(sql), "unique index")
}
