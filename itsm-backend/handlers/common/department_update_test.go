package common

import (
	"context"
	"testing"

	"itsm-backend/ent"
	"itsm-backend/ent/enttest"

	"github.com/stretchr/testify/require"
)

func deptFixture(t *testing.T, dsn string) (*ent.Client, context.Context, *Department, *Department) {
	t.Helper()
	client := enttest.Open(t, "sqlite3", dsn)
	t.Cleanup(func() { _ = client.Close() })
	ctx := context.Background()
	repo := NewEntRepository(client)

	root, err := repo.CreateDepartment(ctx, &Department{Name: "集团", Code: "1", TenantID: 1, NodeType: orgNodeCompany})
	require.NoError(t, err)
	child, err := repo.CreateDepartment(ctx, &Department{
		Name: "分公司A", Code: "1A", TenantID: 1, NodeType: orgNodeBranch, ParentID: root.ID,
	})
	require.NoError(t, err)
	return client, ctx, root, child
}

func TestApplyDepartmentUpdateCanClearTheManager(t *testing.T) {
	client, ctx, _, child := deptFixture(t, "file:deptupd_clear?mode=memory&cache=shared&_fk=1")
	repo := NewEntRepository(client)

	// users 对 tenants 有外键；本 fixture 的第一个租户 ID 必然是 1（部门不建租户）。
	home := seedTenant(t, client, "T-HOME")
	require.Equal(t, 1, home, "fixture assumes the first seeded tenant is tenant 1")

	manager, err := client.User.Create().
		SetUsername("D10001").SetEmail("D10001@example.test").SetName("在职").
		SetPasswordHash("x").SetTenantID(home).SetActive(true).Save(ctx)
	require.NoError(t, err)

	child.ManagerID = manager.ID
	updated, err := repo.UpdateDepartment(ctx, child)
	require.NoError(t, err)
	require.Equal(t, manager.ID, updated.ManagerID)

	zero := 0
	_, change, err := applyDepartmentUpdate(ctx, client, updated, departmentUpdateRequest{
		ManagerID: &zero, Reason: "负责人离职，暂时空缺",
	})
	require.NoError(t, err)
	require.True(t, change.Changed)
	require.Equal(t, 0, change.ManagerTo)

	reloaded, err := repo.GetDepartment(ctx, child.ID, 1)
	require.NoError(t, err)
	require.Equal(t, 0, reloaded.ManagerID, "manager must actually be cleared")
}

func TestApplyDepartmentUpdateRequiresAReasonForAnyChange(t *testing.T) {
	client, ctx, _, child := deptFixture(t, "file:deptupd_reason?mode=memory&cache=shared&_fk=1")

	top := 0
	_, _, err := applyDepartmentUpdate(ctx, client, child, departmentUpdateRequest{ParentID: &top})
	require.Error(t, err, "a change without a reason must be rejected")
}

func TestApplyDepartmentUpdateRejectsCycles(t *testing.T) {
	client, ctx, root, child := deptFixture(t, "file:deptupd_cycle?mode=memory&cache=shared&_fk=1")

	// 把父节点挂到自己的子节点下 → 成环
	descendant := child.ID
	_, _, err := applyDepartmentUpdate(ctx, client, root, departmentUpdateRequest{ParentID: &descendant, Reason: "错误操作"})
	require.Error(t, err, "a node must not be moved under its own descendant")

	self := root.ID
	_, _, err = applyDepartmentUpdate(ctx, client, root, departmentUpdateRequest{ParentID: &self, Reason: "错误操作"})
	require.Error(t, err, "a node must not be its own parent")
}

func TestApplyDepartmentUpdateReportsNoChangeWhenNothingDiffers(t *testing.T) {
	client, ctx, _, child := deptFixture(t, "file:deptupd_noop?mode=memory&cache=shared&_fk=1")

	sameName := child.Name
	_, change, err := applyDepartmentUpdate(ctx, client, child, departmentUpdateRequest{Name: &sameName})
	require.NoError(t, err)
	require.False(t, change.Changed, "a request that changes nothing needs no reason")
}

func TestApplyDepartmentUpdateCanMoveANodeToTheTopLevel(t *testing.T) {
	client, ctx, _, child := deptFixture(t, "file:deptupd_top?mode=memory&cache=shared&_fk=1")

	top := 0
	_, change, err := applyDepartmentUpdate(ctx, client, child, departmentUpdateRequest{
		ParentID: &top, Reason: "分公司独立为法人实体",
	})
	require.NoError(t, err)
	require.True(t, change.Changed)
	require.Equal(t, 0, change.ParentTo, "moving to the top level must be expressible")
}
