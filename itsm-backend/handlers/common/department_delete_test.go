package common

import (
	"context"
	"testing"

	"itsm-backend/ent/enttest"

	"github.com/stretchr/testify/require"
)

// 计划承诺："删除一个仍有下级的节点，其子树会在组织树上变成顶级节点。"
// 只软删目标行是不够的：子节点会指向一个已删除的父，于是从组织树上彻底不可达。
func TestDeleteDepartmentRerootsChildrenOfATopLevelNode(t *testing.T) {
	client := enttest.Open(t, "sqlite3", "file:deptdel_root?mode=memory&cache=shared&_fk=1")
	defer client.Close()
	ctx := context.Background()

	root, err := client.Department.Create().SetName("集团").SetCode("D1").SetTenantID(1).Save(ctx)
	require.NoError(t, err)
	child, err := client.Department.Create().SetName("分公司A").SetCode("D1A").SetTenantID(1).SetParentID(root.ID).Save(ctx)
	require.NoError(t, err)

	repo := &EntRepository{client: client}
	require.NoError(t, repo.DeleteDepartment(ctx, root.ID, 1))

	// 子节点必须被上提为顶层（parent_id 置空），而不是继续指向已删除的父
	reloaded, err := client.Department.Get(ctx, child.ID)
	require.NoError(t, err)
	require.Equal(t, 0, reloaded.ParentID)

	// 而且它必须真的能在树上取到——这才是"变成顶级节点"的实际含义
	roots, err := repo.ListDepartmentChildren(ctx, 1, 0)
	require.NoError(t, err)
	codes := make([]string, 0, len(roots))
	for _, r := range roots {
		codes = append(codes, r.Code)
	}
	require.Contains(t, codes, "D1A", "上提后的子节点必须作为顶层节点可见")
	require.NotContains(t, codes, "D1", "被删除的节点不应再出现在树里")
}

// 删除中间层节点时，子节点应继承被删节点的父级（而不是全部上提到顶层）。
func TestDeleteDepartmentRerootsChildrenToTheGrandparent(t *testing.T) {
	client := enttest.Open(t, "sqlite3", "file:deptdel_mid?mode=memory&cache=shared&_fk=1")
	defer client.Close()
	ctx := context.Background()

	grand, err := client.Department.Create().SetName("集团").SetCode("M1").SetTenantID(1).Save(ctx)
	require.NoError(t, err)
	mid, err := client.Department.Create().SetName("分公司").SetCode("M1A").SetTenantID(1).SetParentID(grand.ID).Save(ctx)
	require.NoError(t, err)
	leaf, err := client.Department.Create().SetName("运维组").SetCode("M1A01").SetTenantID(1).SetParentID(mid.ID).Save(ctx)
	require.NoError(t, err)

	repo := &EntRepository{client: client}
	require.NoError(t, repo.DeleteDepartment(ctx, mid.ID, 1))

	reloaded, err := client.Department.Get(ctx, leaf.ID)
	require.NoError(t, err)
	require.Equal(t, grand.ID, reloaded.ParentID, "子节点应挂到祖父节点上")

	children, err := repo.ListDepartmentChildren(ctx, 1, grand.ID)
	require.NoError(t, err)
	require.Len(t, children, 1)
	require.Equal(t, "M1A01", children[0].Code)
}

// 删除叶子节点仍然可用（没有下级可重挂）。
func TestDeleteDepartmentStillWorksForALeaf(t *testing.T) {
	client := enttest.Open(t, "sqlite3", "file:deptdel_leaf?mode=memory&cache=shared&_fk=1")
	defer client.Close()
	ctx := context.Background()

	only, err := client.Department.Create().SetName("独立部门").SetCode("L1").SetTenantID(1).Save(ctx)
	require.NoError(t, err)

	repo := &EntRepository{client: client}
	require.NoError(t, repo.DeleteDepartment(ctx, only.ID, 1))

	roots, err := repo.ListDepartmentChildren(ctx, 1, 0)
	require.NoError(t, err)
	require.Empty(t, roots, "被删除的叶子不应再出现在树里")
}

// 跨租户不得删除、也不得改动别人租户的父子关系。
func TestDeleteDepartmentIsTenantScoped(t *testing.T) {
	client := enttest.Open(t, "sqlite3", "file:deptdel_tenant?mode=memory&cache=shared&_fk=1")
	defer client.Close()
	ctx := context.Background()

	root, err := client.Department.Create().SetName("集团").SetCode("T1").SetTenantID(1).Save(ctx)
	require.NoError(t, err)
	child, err := client.Department.Create().SetName("分公司").SetCode("T1A").SetTenantID(1).SetParentID(root.ID).Save(ctx)
	require.NoError(t, err)

	repo := &EntRepository{client: client}
	require.Error(t, repo.DeleteDepartment(ctx, root.ID, 999), "别的租户不得删除本租户的部门")

	reloaded, err := client.Department.Get(ctx, child.ID)
	require.NoError(t, err)
	require.Equal(t, root.ID, reloaded.ParentID, "越权删除失败时不得留下被改动的父子关系")

	stillThere, err := client.Department.Get(ctx, root.ID)
	require.NoError(t, err)
	require.Nil(t, stillThere.DeletedAt)
}
