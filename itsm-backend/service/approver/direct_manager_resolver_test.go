package approver

import (
	"context"
	"testing"

	"itsm-backend/ent"
	"itsm-backend/ent/enttest"

	"github.com/stretchr/testify/require"

	_ "github.com/mattn/go-sqlite3"
)

// 汇报链夹具：users 对 tenants 有外键，email/password_hash 必填。
type chainFixture struct {
	client *ent.Client
	ctx    context.Context
	tenant int
	l0     int
	l1     int
	l2     int
}

func newChainFixture(t *testing.T, dsn string) *chainFixture {
	t.Helper()
	client := enttest.Open(t, "sqlite3", dsn)
	t.Cleanup(func() { _ = client.Close() })
	ctx := context.Background()
	tenant := client.Tenant.Create().SetName("T").SetCode("t-" + dsn).SetStatus("active").SaveX(ctx)

	f := &chainFixture{client: client, ctx: ctx, tenant: tenant.ID}
	// l0 → l1 → l2（l0 的上级是 l1，l1 的上级是 l2）
	l0 := f.user("D50001", true)
	l1 := f.user("D50002", true)
	l2 := f.user("D50003", true)
	f.setManager(l0.ID, l1.ID)
	f.setManager(l1.ID, l2.ID)
	f.l0, f.l1, f.l2 = l0.ID, l1.ID, l2.ID
	return f
}

func (f *chainFixture) user(username string, active bool) *ent.User {
	return f.client.User.Create().
		SetUsername(username).SetEmail(username + "@example.test").SetName(username).
		SetPasswordHash("hash").SetTenantID(f.tenant).SetActive(active).
		SaveX(f.ctx)
}

func (f *chainFixture) setManager(userID, managerID int) {
	f.client.User.UpdateOneID(userID).SetManagerID(managerID).SaveX(f.ctx)
}

func (f *chainFixture) resolve(level int) ([]*ApproverInfo, error) {
	return NewDirectManagerResolver(level).Resolve(f.ctx, f.client, &ApproverContext{
		TenantID:    f.tenant,
		RequesterID: f.l0,
	})
}

func TestDirectManagerLevelZeroIsTheImmediateManager(t *testing.T) {
	f := newChainFixture(t, "file:dm_l0?mode=memory&cache=shared&_fk=1")

	got, err := f.resolve(0)
	require.NoError(t, err)
	require.Len(t, got, 1)
	require.Equal(t, f.l1, got[0].UserID)
	require.Equal(t, "direct_manager", got[0].Source)
}

// level=1 表示"再往上第 1 级"，即从直属上级再上一跳。
func TestDirectManagerLevelOneClimbsOneLevelAboveTheImmediateManager(t *testing.T) {
	f := newChainFixture(t, "file:dm_l2?mode=memory&cache=shared&_fk=1")

	got, err := f.resolve(1)
	require.NoError(t, err)
	require.Len(t, got, 1)
	require.Equal(t, f.l2, got[0].UserID)

	// 再往上没有第 2 级了 → 未命中
	miss, err := f.resolve(2)
	require.NoError(t, err)
	require.Empty(t, miss)
}

// 链不够长时返回未命中，**绝不返回申请人自己**。
func TestDirectManagerReturnsMissInsteadOfTheRequesterThemselves(t *testing.T) {
	f := newChainFixture(t, "file:dm_miss?mode=memory&cache=shared&_fk=1")

	got, err := f.resolve(5)
	require.NoError(t, err)
	require.Empty(t, got, "a miss must be empty, never the requester")
}

// 历史脏环（本计划 Task 4 才清理）不得让解析跑飞。
func TestDirectManagerDoesNotRunAwayOnADirtyCycle(t *testing.T) {
	f := newChainFixture(t, "file:dm_cycle?mode=memory&cache=shared&_fk=1")
	f.setManager(f.l1, f.l0) // 造 A→B→A

	_, err := f.resolve(99)
	require.NoError(t, err, "a dirty cycle must terminate, not hang")
}

// 上级已离职时不能把他派成审批人：从第 N 级开始**继续往上**找第一个在职的人。
func TestDirectManagerSkipsAnInactiveManager(t *testing.T) {
	f := newChainFixture(t, "file:dm_inactive?mode=memory&cache=shared&_fk=1")
	f.client.User.UpdateOneID(f.l1).SetActive(false).SaveX(f.ctx)

	// 直属上级离职 → 继续上溯到 l2
	got, err := f.resolve(0)
	require.NoError(t, err)
	require.Len(t, got, 1)
	require.Equal(t, f.l2, got[0].UserID)

	// 从第 1 级（l2）开始也在职 → 仍是 l2
	got1, err := f.resolve(1)
	require.NoError(t, err)
	require.Len(t, got1, 1)
	require.Equal(t, f.l2, got1[0].UserID)
}

func TestDirectManagerIsTenantScoped(t *testing.T) {
	f := newChainFixture(t, "file:dm_tenant?mode=memory&cache=shared&_fk=1")

	// 用别的租户 ID 解析，不应拿到这个租户的人
	got, err := NewDirectManagerResolver(0).Resolve(f.ctx, f.client, &ApproverContext{
		TenantID:    f.tenant + 999,
		RequesterID: f.l0,
	})
	require.NoError(t, err)
	require.Empty(t, got)
}
