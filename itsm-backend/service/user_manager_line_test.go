package service

import (
	"context"
	"testing"

	"itsm-backend/ent"
	"itsm-backend/ent/enttest"

	"github.com/stretchr/testify/require"
)

// 汇报线校验的测试夹具。users 对 tenants 有外键、且 email/password_hash 是必填，
// 因此必须先建租户、并给全这两个字段。
type managerLineFixture struct {
	client     *ent.Client
	ctx        context.Context
	homeTenant int
	otherTenn  int
}

func newManagerLineFixture(t *testing.T, dsn string) *managerLineFixture {
	t.Helper()
	client := enttest.Open(t, "sqlite3", dsn)
	t.Cleanup(func() { _ = client.Close() })
	ctx := context.Background()

	home := client.Tenant.Create().SetName("Home").SetCode("home-" + dsn).SetStatus("active").SaveX(ctx)
	other := client.Tenant.Create().SetName("Other").SetCode("other-" + dsn).SetStatus("active").SaveX(ctx)
	return &managerLineFixture{client: client, ctx: ctx, homeTenant: home.ID, otherTenn: other.ID}
}

func (f *managerLineFixture) user(username string, tenantID int, active bool) *ent.User {
	return f.client.User.Create().
		SetUsername(username).
		SetEmail(username + "@example.test").
		SetName(username).
		SetPasswordHash("hash").
		SetTenantID(tenantID).
		SetActive(active).
		SaveX(f.ctx)
}

func TestValidateUserManagerAllowsAnAbsentManager(t *testing.T) {
	f := newManagerLineFixture(t, "file:mgr_absent?mode=memory&cache=shared&_fk=1")
	staff := f.user("D20002", f.homeTenant, true)

	require.NoError(t, ValidateUserManager(f.ctx, f.client, f.homeTenant, staff.ID, 0),
		"上级暂缺是合法状态：提单/审批不得因此失败")
}

func TestValidateUserManagerRejectsSelfReference(t *testing.T) {
	f := newManagerLineFixture(t, "file:mgr_self?mode=memory&cache=shared&_fk=1")
	staff := f.user("D20002", f.homeTenant, true)

	require.Error(t, ValidateUserManager(f.ctx, f.client, f.homeTenant, staff.ID, staff.ID),
		"不能把自己设为上级")
}

func TestValidateUserManagerRejectsCrossTenantAndInactive(t *testing.T) {
	f := newManagerLineFixture(t, "file:mgr_tenant?mode=memory&cache=shared&_fk=1")
	staff := f.user("D20002", f.homeTenant, true)
	outsider := f.user("D20003", f.otherTenn, true)
	inactive := f.user("D20004", f.homeTenant, false)

	require.Error(t, ValidateUserManager(f.ctx, f.client, f.homeTenant, staff.ID, outsider.ID),
		"跨租户上级必须 fail-closed")
	require.Error(t, ValidateUserManager(f.ctx, f.client, f.homeTenant, staff.ID, inactive.ID),
		"非在职不能当上级")
	require.Error(t, ValidateUserManager(f.ctx, f.client, f.homeTenant, staff.ID, 999999),
		"不存在的上级必须 fail-closed")
}

func TestValidateUserManagerRejectsCycles(t *testing.T) {
	f := newManagerLineFixture(t, "file:mgr_cycle?mode=memory&cache=shared&_fk=1")
	boss := f.user("D20001", f.homeTenant, true)
	staff := f.user("D20002", f.homeTenant, true)

	require.NoError(t, ValidateUserManager(f.ctx, f.client, f.homeTenant, staff.ID, boss.ID))
	f.client.User.UpdateOneID(staff.ID).SetManagerID(boss.ID).SaveX(f.ctx)

	require.Error(t, ValidateUserManager(f.ctx, f.client, f.homeTenant, boss.ID, staff.ID),
		"不能造出 A→B→A")
}

// 历史数据里可能已有脏环（本计划 Task 4 才清理）。校验遇到它必须终止，
// 但不能因为别人的脏数据挡住一次合法写入。
func TestValidateUserManagerToleratesAPreExistingCycleFurtherUp(t *testing.T) {
	f := newManagerLineFixture(t, "file:mgr_dirty_cycle?mode=memory&cache=shared&_fk=1")
	boss := f.user("D20001", f.homeTenant, true)
	staff := f.user("D20002", f.homeTenant, true)

	f.client.User.UpdateOneID(boss.ID).SetManagerID(boss.ID).SaveX(f.ctx) // boss 自己是自己的上级

	require.NoError(t, ValidateUserManager(f.ctx, f.client, f.homeTenant, staff.ID, boss.ID),
		"上溯撞到既有脏环应停下，而不是死循环或误报")
}
