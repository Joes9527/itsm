package common

import (
	"context"
	"testing"

	"itsm-backend/ent"
	"itsm-backend/ent/enttest"
	enttenant "itsm-backend/ent/tenant"

	"github.com/stretchr/testify/require"
)

// seedTenant 建一个可用租户并返回其 ID。
// users 对 tenants 有外键，测试里必须先有租户才能建用户。
// 本文件定义一次，package common 的其它测试文件复用。
func seedTenant(t *testing.T, client *ent.Client, code string) int {
	t.Helper()
	tenant, err := client.Tenant.Create().
		SetName(code).
		SetCode(code).
		SetType(enttenant.TypeStandard).
		SetStatus("active").
		Save(context.Background())
	require.NoError(t, err)
	return tenant.ID
}

func TestValidateDepartmentManagerAllowsAbsentManager(t *testing.T) {
	client := enttest.Open(t, "sqlite3", "file:deptmgr_absent?mode=memory&cache=shared&_fk=1")
	defer client.Close()

	require.NoError(t, validateDepartmentManager(context.Background(), client, 1, 0))
}

func TestValidateDepartmentManagerRequiresAnActiveSameTenantUser(t *testing.T) {
	client := enttest.Open(t, "sqlite3", "file:deptmgr_active?mode=memory&cache=shared&_fk=1")
	defer client.Close()
	ctx := context.Background()

	home := seedTenant(t, client, "T-HOME")
	other := seedTenant(t, client, "T-OTHER")

	active, err := client.User.Create().SetUsername("D10001").SetEmail("D10001@example.test").SetName("在职").SetPasswordHash("x").SetTenantID(home).SetActive(true).Save(ctx)
	require.NoError(t, err)
	require.NoError(t, validateDepartmentManager(ctx, client, home, active.ID))

	inactive, err := client.User.Create().SetUsername("D10002").SetEmail("D10002@example.test").SetName("离职").SetPasswordHash("x").SetTenantID(home).SetActive(false).Save(ctx)
	require.NoError(t, err)
	require.Error(t, validateDepartmentManager(ctx, client, home, inactive.ID), "an inactive user must not be a department manager")

	otherTenant, err := client.User.Create().SetUsername("D10003").SetEmail("D10003@example.test").SetName("别家").SetPasswordHash("x").SetTenantID(other).SetActive(true).Save(ctx)
	require.NoError(t, err)
	require.Error(t, validateDepartmentManager(ctx, client, home, otherTenant.ID), "cross-tenant manager must fail closed")

	require.Error(t, validateDepartmentManager(ctx, client, home, 999999), "unknown manager must fail closed")
}

func TestValidateDepartmentManagerRejectsScaffoldAccounts(t *testing.T) {
	client := enttest.Open(t, "sqlite3", "file:deptmgr_scaffold?mode=memory&cache=shared&_fk=1")
	defer client.Close()
	ctx := context.Background()

	home := seedTenant(t, client, "T-HOME")

	for _, username := range []string{"supervisor_test", "qa_manager_ops", "admin", "ui-runtime-1789377159595-dept_manager", "engineer-workspace-1-engineer", "kaf_closeout_t1_20260831"} {
		u, err := client.User.Create().
			SetUsername(username).
			SetEmail(username + "@example.test").
			SetName("脚手架").
			SetPasswordHash("x").
			SetTenantID(home).
			SetActive(true).
			Save(ctx)
		require.NoError(t, err)
		require.Error(t, validateDepartmentManager(ctx, client, home, u.ID), "%s is a scaffold account and must not own a department", username)
	}
}
