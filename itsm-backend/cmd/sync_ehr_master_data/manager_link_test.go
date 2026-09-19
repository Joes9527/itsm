package main

import (
	"context"
	"testing"

	"itsm-backend/ent"
	"itsm-backend/ent/enttest"

	"github.com/stretchr/testify/require"

	_ "github.com/mattn/go-sqlite3"
)

// 导入侧夹具：users 对 tenants 有外键，email/password_hash 必填。
func linkFixture(t *testing.T, dsn string) (*ent.Client, context.Context, int) {
	t.Helper()
	client := enttest.Open(t, "sqlite3", dsn)
	t.Cleanup(func() { _ = client.Close() })
	ctx := context.Background()
	tenant := client.Tenant.Create().SetName("T").SetCode("t-" + dsn).SetStatus("active").SaveX(ctx)
	return client, ctx, tenant.ID
}

func mkUser(t *testing.T, client *ent.Client, ctx context.Context, tenantID int, username string, active bool) *ent.User {
	t.Helper()
	u, err := client.User.Create().
		SetUsername(username).SetEmail(username + "@example.test").SetName(username).
		SetPasswordHash("hash").SetTenantID(tenantID).SetActive(active).
		Save(ctx)
	require.NoError(t, err)
	return u
}

// 自引用必须被跳过，且**不得写库**——数据里已有 31 条自引用，
// 若导入遇到非法值就整批失败，导入将永远跑不完。
func TestLinkManagersSkipsSelfReferenceInsteadOfCreatingIt(t *testing.T) {
	client, ctx, tenant := linkFixture(t, "file:linkmgr_self?mode=memory&cache=shared&_fk=1")
	u := mkUser(t, client, ctx, tenant, "D30001", true)

	_, skipped, err := linkManagers(ctx, client, tenant, map[string]managerLink{
		"D30001": {SelfID: u.ID, ManagerID: u.ID},
	})
	require.NoError(t, err)
	require.Equal(t, 1, skipped)

	reloaded, err := client.User.Get(ctx, u.ID)
	require.NoError(t, err)
	require.Equal(t, 0, reloaded.ManagerID, "self-reference must not be persisted")
}

func TestLinkManagersLinksTheValidOnesAndCountsTheRest(t *testing.T) {
	client, ctx, tenant := linkFixture(t, "file:linkmgr_mixed?mode=memory&cache=shared&_fk=1")
	boss := mkUser(t, client, ctx, tenant, "D30010", true)
	staff := mkUser(t, client, ctx, tenant, "D30011", true)
	inactive := mkUser(t, client, ctx, tenant, "D30012", false)

	_, skipped, err := linkManagers(ctx, client, tenant, map[string]managerLink{
		"D30011": {SelfID: staff.ID, ManagerID: boss.ID},     // 合法
		"D30010": {SelfID: boss.ID, ManagerID: boss.ID},      // 自引用 → 跳过
		"D30012": {SelfID: inactive.ID, ManagerID: staff.ID}, // 本人已离职、上级在职：只校验上级，允许
		"D39999": {SelfID: 424242, ManagerID: boss.ID},       // 本人不存在 → 跳过
	})
	require.NoError(t, err)
	require.Equal(t, 2, skipped, "自引用与不存在的人必须被跳过并计数")

	reloaded, err := client.User.Get(ctx, staff.ID)
	require.NoError(t, err)
	require.Equal(t, boss.ID, reloaded.ManagerID, "the valid link must be persisted")
}

// 上级不是在职用户时必须跳过：审批不能落到离职账号上。
func TestLinkManagersSkipsAnInactiveManager(t *testing.T) {
	client, ctx, tenant := linkFixture(t, "file:linkmgr_inactive?mode=memory&cache=shared&_fk=1")
	gone := mkUser(t, client, ctx, tenant, "D30020", false)
	staff := mkUser(t, client, ctx, tenant, "D30021", true)

	_, skipped, err := linkManagers(ctx, client, tenant, map[string]managerLink{
		"D30021": {SelfID: staff.ID, ManagerID: gone.ID},
	})
	require.NoError(t, err)
	require.Equal(t, 1, skipped)

	reloaded, err := client.User.Get(ctx, staff.ID)
	require.NoError(t, err)
	require.Equal(t, 0, reloaded.ManagerID)
}

// 计数必须**如实反映实际写库的条数**。
//
// 过去调用方用 len(pendingLinks) - skipped 当作"已链接"，有两个漏洞：
//   - 中途出错时循环已中断，未处理的链接照样被算成已链接；
//   - 上级名字没能映射成用户的（ManagerID==0）被静默 continue，既不计跳过，
//     也被算成已链接。
//
// 于是日志会报出一个比实际更大的"已链接"数字，而这类数字正是用来判断导入
// 是否成功的依据——虚报会让一次失败的导入看起来成功。
func TestLinkManagersCountsOnlyWhatItActuallyWrote(t *testing.T) {
	client, ctx, tenant := linkFixture(t, "file:linkmgr_counts?mode=memory&cache=shared&_fk=1")
	boss := mkUser(t, client, ctx, tenant, "D30030", true)
	staff := mkUser(t, client, ctx, tenant, "D30031", true)

	linked, skipped, err := linkManagers(ctx, client, tenant, map[string]managerLink{
		"D30031": {SelfID: staff.ID, ManagerID: boss.ID}, // 唯一真正写库的一条
		"D30030": {SelfID: boss.ID, ManagerID: boss.ID},  // 自引用 → 跳过
		"D30032": {SelfID: 0, ManagerID: boss.ID},        // 本人未映射 → 跳过（过去被静默吞掉）
		"D30033": {SelfID: staff.ID, ManagerID: 0},       // 上级未映射 → 跳过（过去被静默吞掉）
	})
	require.NoError(t, err)
	require.Equal(t, 1, linked, "只能把真正写库的那一条算作已链接")
	require.Equal(t, 3, skipped, "自引用与未映射都必须计入跳过，不能静默消失")
}

// 未映射的链接（上级名字没对应到用户）过去既不计跳过、也被算成已链接，
// 于是"没接上"这一事实在日志里完全消失。
func TestLinkManagersCountsUnmappedSupervisorsAsSkipped(t *testing.T) {
	client, ctx, tenant := linkFixture(t, "file:linkmgr_unmapped?mode=memory&cache=shared&_fk=1")
	staff := mkUser(t, client, ctx, tenant, "D30040", true)

	linked, skipped, err := linkManagers(ctx, client, tenant, map[string]managerLink{
		"D30040": {SelfID: staff.ID, ManagerID: 0},
	})
	require.NoError(t, err)
	require.Equal(t, 0, linked)
	require.Equal(t, 1, skipped)
}
