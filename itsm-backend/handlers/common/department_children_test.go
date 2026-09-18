package common

import (
	"context"
	"strconv"
	"testing"

	"itsm-backend/ent/enttest"

	"github.com/stretchr/testify/require"
)

func TestListDepartmentChildrenReturnsProjectionWithHasChildren(t *testing.T) {
	client := enttest.Open(t, "sqlite3", "file:deptchildren?mode=memory&cache=shared&_fk=1")
	defer client.Close()
	ctx := context.Background()

	root, err := client.Department.Create().SetName("集团").SetCode("1").SetTenantID(1).SetNodeType(orgNodeCompany).Save(ctx)
	require.NoError(t, err)
	mid, err := client.Department.Create().SetName("分公司A").SetCode("1A").SetTenantID(1).SetNodeType(orgNodeBranch).SetParentID(root.ID).Save(ctx)
	require.NoError(t, err)
	_, err = client.Department.Create().SetName("运维组").SetCode("1A01").SetTenantID(1).SetNodeType(orgNodeTeam).SetParentID(mid.ID).Save(ctx)
	require.NoError(t, err)

	repo := NewEntRepository(client)
	children, err := repo.ListDepartmentChildren(ctx, 1, root.ID)
	require.NoError(t, err)
	require.Len(t, children.Items, 1)
	require.Equal(t, "1A", children.Items[0].Code)
	require.Equal(t, orgNodeBranch, children.Items[0].NodeType)
	require.True(t, children.Items[0].HasChildren, "a node with children must say so without loading them")
	require.False(t, children.Truncated)

	leaves, err := repo.ListDepartmentChildren(ctx, 1, mid.ID)
	require.NoError(t, err)
	require.Len(t, leaves.Items, 1)
	require.False(t, leaves.Items[0].HasChildren)

	// 根节点在库里 parent_id 是 NULL，parentId=0 必须能取到。
	roots, err := repo.ListDepartmentChildren(ctx, 1, 0)
	require.NoError(t, err)
	require.Len(t, roots.Items, 1)
	require.Equal(t, "1", roots.Items[0].Code)
	require.True(t, roots.Items[0].HasChildren)
}

func TestListDepartmentChildrenIsTenantScoped(t *testing.T) {
	client := enttest.Open(t, "sqlite3", "file:deptchildren2?mode=memory&cache=shared&_fk=1")
	defer client.Close()
	ctx := context.Background()

	root, err := client.Department.Create().SetName("集团").SetCode("1").SetTenantID(1).SetNodeType(orgNodeCompany).Save(ctx)
	require.NoError(t, err)
	_, err = client.Department.Create().SetName("别家").SetCode("2").SetTenantID(2).SetNodeType(orgNodeCompany).SetParentID(root.ID).Save(ctx)
	require.NoError(t, err)

	repo := NewEntRepository(client)
	children, err := repo.ListDepartmentChildren(ctx, 1, root.ID)
	require.NoError(t, err)
	require.Empty(t, children.Items, "cross-tenant children must never leak")
}

// 一页装不下时必须**显式标记**，不能返回一份"看起来完整、实际不全"的列表。
func TestListDepartmentChildrenFlagsTruncationInsteadOfHidingIt(t *testing.T) {
	client := enttest.Open(t, "sqlite3", "file:deptchildren_trunc?mode=memory&cache=shared&_fk=1")
	defer client.Close()
	ctx := context.Background()

	repo := NewEntRepository(client)
	repo.childrenPageSize = 3 // 用小页长走同一条代码路径

	root, err := client.Department.Create().SetName("集团").SetCode("1").SetTenantID(1).SetNodeType(orgNodeCompany).Save(ctx)
	require.NoError(t, err)
	for i := 1; i <= 5; i++ {
		_, err := client.Department.Create().
			SetName("子" + strconv.Itoa(i)).
			SetCode("C" + strconv.Itoa(i)).
			SetTenantID(1).SetNodeType(orgNodeDepartment).SetParentID(root.ID).
			Save(ctx)
		require.NoError(t, err)
	}

	page, err := repo.ListDepartmentChildren(ctx, 1, root.ID)
	require.NoError(t, err)
	require.Len(t, page.Items, 3)
	require.True(t, page.Truncated, "a partial page must say it is partial")

	// 刚好装下时不标记
	small, err := repo.ListDepartmentChildren(ctx, 1, 0)
	require.NoError(t, err)
	require.Len(t, small.Items, 1)
	require.False(t, small.Truncated)
}
