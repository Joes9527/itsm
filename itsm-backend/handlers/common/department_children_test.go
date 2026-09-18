package common

import (
	"context"
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

	repo := &EntRepository{client: client}
	children, err := repo.ListDepartmentChildren(ctx, 1, root.ID)
	require.NoError(t, err)
	require.Len(t, children, 1)
	require.Equal(t, "1A", children[0].Code)
	require.Equal(t, orgNodeBranch, children[0].NodeType)
	require.True(t, children[0].HasChildren, "a node with children must say so without loading them")

	leaves, err := repo.ListDepartmentChildren(ctx, 1, mid.ID)
	require.NoError(t, err)
	require.Len(t, leaves, 1)
	require.False(t, leaves[0].HasChildren)

	// 根节点在库里 parent_id 是 NULL，parentId=0 必须能取到。
	roots, err := repo.ListDepartmentChildren(ctx, 1, 0)
	require.NoError(t, err)
	require.Len(t, roots, 1)
	require.Equal(t, "1", roots[0].Code)
	require.True(t, roots[0].HasChildren)
}

func TestListDepartmentChildrenIsTenantScoped(t *testing.T) {
	client := enttest.Open(t, "sqlite3", "file:deptchildren2?mode=memory&cache=shared&_fk=1")
	defer client.Close()
	ctx := context.Background()

	root, err := client.Department.Create().SetName("集团").SetCode("1").SetTenantID(1).SetNodeType(orgNodeCompany).Save(ctx)
	require.NoError(t, err)
	_, err = client.Department.Create().SetName("别家").SetCode("2").SetTenantID(2).SetNodeType(orgNodeCompany).SetParentID(root.ID).Save(ctx)
	require.NoError(t, err)

	repo := &EntRepository{client: client}
	children, err := repo.ListDepartmentChildren(ctx, 1, root.ID)
	require.NoError(t, err)
	require.Empty(t, children, "cross-tenant children must never leak")
}
