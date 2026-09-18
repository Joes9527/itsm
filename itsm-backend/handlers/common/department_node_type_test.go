package common

import (
	"context"
	"testing"

	"itsm-backend/ent/enttest"

	"github.com/stretchr/testify/require"
)

func TestNormalizeDepartmentNodeTypeAcceptsTheFourTypes(t *testing.T) {
	for _, want := range []string{orgNodeCompany, orgNodeBranch, orgNodeDepartment, orgNodeTeam} {
		got, err := NormalizeDepartmentNodeType(want)
		require.NoError(t, err)
		require.Equal(t, want, got)
	}
}

func TestNormalizeDepartmentNodeTypeTreatsBlankAsUnclassified(t *testing.T) {
	// 精确的空串是合法状态：canonical 050 的默认值，代表"历史数据尚未分流"。
	got, err := NormalizeDepartmentNodeType("")
	require.NoError(t, err)
	require.Equal(t, "", got)
}

func TestNormalizeDepartmentNodeTypeRejectsUnknownValues(t *testing.T) {
	for _, bad := range []string{"集团", "DIVISION", " company", "Company", "branch ", "   "} {
		_, err := NormalizeDepartmentNodeType(bad)
		require.Error(t, err, "%q must fail closed", bad)
	}
}

// 类型必须真的写进库：只加字段不接写入路径，等于"有存储没入口"。
func TestCreateDepartmentPersistsTheNodeType(t *testing.T) {
	client := enttest.Open(t, "sqlite3", "file:depttype_create?mode=memory&cache=shared&_fk=1")
	defer client.Close()
	ctx := context.Background()
	repo := NewEntRepository(client)

	typed, err := repo.CreateDepartment(ctx, &Department{Name: "集团", Code: "1", TenantID: 1, NodeType: orgNodeCompany})
	require.NoError(t, err)
	require.Equal(t, orgNodeCompany, typed.NodeType)

	// 不传类型 = 未分类，合法
	unclassified, err := repo.CreateDepartment(ctx, &Department{Name: "待分类", Code: "2", TenantID: 1})
	require.NoError(t, err)
	require.Equal(t, "", unclassified.NodeType)

	reloaded, err := repo.GetDepartment(ctx, typed.ID, 1)
	require.NoError(t, err)
	require.Equal(t, orgNodeCompany, reloaded.NodeType, "the type must survive a round trip")
}

func TestWritePathsRejectAnUnknownNodeType(t *testing.T) {
	client := enttest.Open(t, "sqlite3", "file:depttype_reject?mode=memory&cache=shared&_fk=1")
	defer client.Close()
	ctx := context.Background()
	repo := NewEntRepository(client)

	// 创建路径
	_, err := repo.CreateDepartment(ctx, &Department{Name: "坏类型", Code: "bad", TenantID: 1, NodeType: "集团"})
	require.Error(t, err, "create must fail closed on an unknown node type")

	// 未知取值不得落库
	count, err := client.Department.Query().Count(ctx)
	require.NoError(t, err)
	require.Equal(t, 0, count)

	// 更新路径
	good, err := repo.CreateDepartment(ctx, &Department{Name: "分公司", Code: "1A", TenantID: 1, NodeType: orgNodeBranch})
	require.NoError(t, err)
	good.NodeType = "DIVISION"
	_, err = repo.UpdateDepartment(ctx, good)
	require.Error(t, err, "update must fail closed on an unknown node type")

	reloaded, err := repo.GetDepartment(ctx, good.ID, 1)
	require.NoError(t, err)
	require.Equal(t, orgNodeBranch, reloaded.NodeType, "a rejected update must not change the stored type")
}
