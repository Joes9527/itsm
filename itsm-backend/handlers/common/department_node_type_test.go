package common

import (
	"testing"

	"github.com/stretchr/testify/require"
)

func TestNormalizeDepartmentNodeTypeAcceptsTheFourTypes(t *testing.T) {
	for _, want := range []string{"company", "branch", "department", "team"} {
		got, err := normalizeDepartmentNodeType(want)
		require.NoError(t, err)
		require.Equal(t, want, got)
	}
}

// 类型写在节点上、不靠层级判断；未知取值必须 fail-closed，
// 否则组织树会长出无法解释的类型，审批找人也会按错误节点取负责人。
func TestNormalizeDepartmentNodeTypeRejectsUnknownValues(t *testing.T) {
	for _, bad := range []string{"集团", "DIVISION", " company", "Company"} {
		_, err := normalizeDepartmentNodeType(bad)
		require.Error(t, err, "unknown node type must fail closed: %q", bad)
	}
}
