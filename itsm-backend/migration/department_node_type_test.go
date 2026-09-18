package migration

import (
	"testing"

	"github.com/stretchr/testify/require"
)

// 050 必须真的进入 canonical 流并且能取到 SQL——否则"节点自带类型"只写在文档里。
func TestDepartmentNodeTypeMigrationIsRegistered(t *testing.T) {
	var found bool
	for _, m := range PostSchemaMigrations() {
		if m.Version == DepartmentNodeTypeVersion {
			found = true
			break
		}
	}
	require.True(t, found, "%s must be part of the canonical stream", DepartmentNodeTypeVersion)
}

// 取值范围与 handlers/common 的 normalizeDepartmentNodeType 必须一致，
// 否则应用层接受的类型会被数据库约束拒绝（或反之，约束形同虚设）。
func TestDepartmentNodeTypeMigrationMatchesTheApplicationVocabulary(t *testing.T) {
	sql := GetMigrationSQL(DepartmentNodeTypeVersion)
	require.Contains(t, sql, "departments_node_type_value_check")
	for _, value := range []string{"''", "'company'", "'branch'", "'department'", "'team'"} {
		require.Contains(t, sql, value)
	}
}
