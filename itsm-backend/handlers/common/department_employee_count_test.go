package common

import (
	"context"
	"testing"

	"itsm-backend/ent/enttest"

	"github.com/stretchr/testify/require"
)

func TestCountDepartmentSubtreeEmployeesCountsTheWholeSubtree(t *testing.T) {
	client := enttest.Open(t, "sqlite3", "file:deptcount?mode=memory&cache=shared&_fk=1")
	t.Cleanup(func() { _ = client.Close() })
	ctx := context.Background()
	repo := NewEntRepository(client)

	home := seedTenant(t, client, "T-HOME")
	other := seedTenant(t, client, "T-OTHER")

	root, err := client.Department.Create().SetName("集团").SetCode("1").SetTenantID(home).SetNodeType(orgNodeCompany).Save(ctx)
	require.NoError(t, err)
	child, err := client.Department.Create().SetName("分公司A").SetCode("1A").SetTenantID(home).SetNodeType(orgNodeBranch).SetParentID(root.ID).Save(ctx)
	require.NoError(t, err)

	// root 直属 1 人，child 直属 2 人
	for _, spec := range []struct {
		username string
		deptID   int
	}{{"D1", root.ID}, {"D2", child.ID}, {"D3", child.ID}} {
		_, err := client.User.Create().
			SetUsername(spec.username).SetEmail(spec.username + "@example.test").SetName("员工").
			SetPasswordHash("x").SetTenantID(home).SetActive(true).SetDepartmentID(spec.deptID).
			Save(ctx)
		require.NoError(t, err)
	}

	// 别租户的同名结构不得计入
	otherRoot, err := client.Department.Create().SetName("别家").SetCode("1").SetTenantID(other).SetNodeType(orgNodeCompany).Save(ctx)
	require.NoError(t, err)
	_, err = client.User.Create().
		SetUsername("D9").SetEmail("D9@example.test").SetName("别家员工").
		SetPasswordHash("x").SetTenantID(other).SetActive(true).SetDepartmentID(otherRoot.ID).
		Save(ctx)
	require.NoError(t, err)

	total, err := repo.CountDepartmentSubtreeEmployees(ctx, home, root.ID)
	require.NoError(t, err)
	require.Equal(t, 3, total)

	childOnly, err := repo.CountDepartmentSubtreeEmployees(ctx, home, child.ID)
	require.NoError(t, err)
	require.Equal(t, 2, childOnly)
}

func TestCountDepartmentSubtreeEmployeesIgnoresInactiveStaff(t *testing.T) {
	client := enttest.Open(t, "sqlite3", "file:deptcount_inactive?mode=memory&cache=shared&_fk=1")
	t.Cleanup(func() { _ = client.Close() })
	ctx := context.Background()
	repo := NewEntRepository(client)

	home := seedTenant(t, client, "T-HOME")
	dept, err := client.Department.Create().SetName("运维部").SetCode("OPS").SetTenantID(home).SetNodeType(orgNodeDepartment).Save(ctx)
	require.NoError(t, err)

	_, err = client.User.Create().SetUsername("D1").SetEmail("D1@example.test").SetName("在职").
		SetPasswordHash("x").SetTenantID(home).SetActive(true).SetDepartmentID(dept.ID).Save(ctx)
	require.NoError(t, err)
	_, err = client.User.Create().SetUsername("D2").SetEmail("D2@example.test").SetName("离职").
		SetPasswordHash("x").SetTenantID(home).SetActive(false).SetDepartmentID(dept.ID).Save(ctx)
	require.NoError(t, err)

	count, err := repo.CountDepartmentSubtreeEmployees(ctx, home, dept.ID)
	require.NoError(t, err)
	require.Equal(t, 1, count, "离职员工不计入部门人数")
}

func TestCountDepartmentSubtreeEmployeesStopsAtTheNodeBudget(t *testing.T) {
	client := enttest.Open(t, "sqlite3", "file:deptcount_budget?mode=memory&cache=shared&_fk=1")
	t.Cleanup(func() { _ = client.Close() })
	ctx := context.Background()

	// 用很小的预算走同一条代码路径：链长超过预算时必须返回错误，而不是无界遍历。
	repo := NewEntRepository(client)
	repo.subtreeBudget = 5

	home := seedTenant(t, client, "T-HOME")
	parentID := 0
	for i := 1; i <= 12; i++ {
		create := client.Department.Create().
			SetName("节点").SetCode("N" + itoa(i)).SetTenantID(home).SetNodeType(orgNodeDepartment)
		if parentID != 0 {
			create = create.SetParentID(parentID)
		}
		d, err := create.Save(ctx)
		require.NoError(t, err)
		parentID = d.ID
	}

	_, err := repo.CountDepartmentSubtreeEmployees(ctx, home, 1)
	require.Error(t, err, "a subtree larger than the budget must fail visibly instead of running away")
	require.Contains(t, err.Error(), "budget")
}

func itoa(i int) string {
	if i == 0 {
		return "0"
	}
	digits := ""
	for i > 0 {
		digits = string(rune('0'+i%10)) + digits
		i /= 10
	}
	return digits
}
