package migration

import (
	"testing"

	"github.com/stretchr/testify/require"
)

// 051 必须真的进入 canonical 流并且能取到 SQL——否则"归一为 NULL"只写在文档里，
// 每个环境仍会带着两种"没有负责人"的写法。
func TestDepartmentManagerNoneMigrationIsRegistered(t *testing.T) {
	var found bool
	for _, m := range PostSchemaMigrations() {
		if m.Version == DepartmentManagerNoneVersion {
			found = true
			break
		}
	}
	require.True(t, found, "%s must be part of the canonical stream", DepartmentManagerNoneVersion)
}

// 归一方向必须是 0 → NULL：把 NULL 改写成 0 会与表内 7974 行的既有惯例相反，
// 也会让 `manager_id IS NULL` 这类查询漏掉全部"没有负责人"的部门。
func TestDepartmentManagerNoneMigrationNormalisesZeroToNull(t *testing.T) {
	sql := GetMigrationSQL(DepartmentManagerNoneVersion)
	require.Contains(t, sql, "SET manager_id = NULL")
	require.Contains(t, sql, "WHERE manager_id = 0")
	require.NotContains(t, sql, "SET manager_id = 0", "归一方向不能反")
}
