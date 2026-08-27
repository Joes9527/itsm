package main

import (
	"context"
	"fmt"
	"sync/atomic"
	"testing"
	"time"

	"itsm-backend/ent"
	"itsm-backend/ent/enttest"

	"github.com/stretchr/testify/require"

	_ "github.com/mattn/go-sqlite3"
)

// testDSN 为每个测试返回唯一的 SQLite 内存数据库 DSN，避免测试间数据库残留
// （与 cmd/backfill_process_instance_business_identity/main_test.go 同一做法）。
var testDBCounter int64

func testDSN() string {
	return fmt.Sprintf("file:backfill_incident_wi_test_%d?mode=memory&cache=shared&_fk=1", atomic.AddInt64(&testDBCounter, 1))
}

func setupTenantAndUser(t *testing.T, client *ent.Client, ctx context.Context, code string) (*ent.Tenant, *ent.User) {
	t.Helper()
	tenant, err := client.Tenant.Create().
		SetName("T-" + code).SetCode(code).SetDomain(code + ".example.com").SetStatus("active").
		Save(ctx)
	require.NoError(t, err)
	user, err := client.User.Create().
		SetUsername("reporter-" + code).SetEmail("reporter-" + code + "@example.com").
		SetName("Reporter").SetPasswordHash("hash").SetRole("end_user").
		SetActive(true).SetTenantID(tenant.ID).
		Save(ctx)
	require.NoError(t, err)
	return tenant, user
}

// TestFindCandidates_OnlyMissingWorkItemID 锁定候选口径：只有 work_item_id 为空
// （NULL，而不是新代码写入的合法非零值）且未被软删除的 Incident 才进候选。
func TestFindCandidates_OnlyMissingWorkItemID(t *testing.T) {
	client := enttest.Open(t, "sqlite3", testDSN())
	defer client.Close()
	ctx := context.Background()

	tenant, user := setupTenantAndUser(t, client, ctx, "cand")

	legacy, err := client.Incident.Create().
		SetTitle("存量事件-缺WorkItem").SetIncidentNumber("INC-CAND-1").
		SetReporterID(user.ID).SetTenantID(tenant.ID).
		Save(ctx)
	require.NoError(t, err)

	// 已经迁移过（有 work_item_id）的事件——不应该出现在候选里。
	wi, err := client.Ticket.Create().
		SetTitle("已迁移事件对应的WorkItem").SetTicketNumber("TKT-CAND-ALREADY").
		SetRecordClass("incident").SetRequesterID(user.ID).SetTenantID(tenant.ID).
		Save(ctx)
	require.NoError(t, err)
	_, err = client.Incident.Create().
		SetTitle("已迁移事件").SetIncidentNumber("INC-CAND-2").
		SetReporterID(user.ID).SetTenantID(tenant.ID).SetWorkItemID(wi.ID).
		Save(ctx)
	require.NoError(t, err)

	// 软删除且缺 work_item_id 的事件——不应该出现在候选里。
	deleted, err := client.Incident.Create().
		SetTitle("已软删除事件").SetIncidentNumber("INC-CAND-3").
		SetReporterID(user.ID).SetTenantID(tenant.ID).
		Save(ctx)
	require.NoError(t, err)
	now := time.Now()
	_, err = client.Incident.UpdateOneID(deleted.ID).SetDeletedAt(now).Save(ctx)
	require.NoError(t, err)

	candidates, err := findCandidates(ctx, client, 0)
	require.NoError(t, err)
	require.Len(t, candidates, 1, "只有缺 work_item_id 且未软删除的事件应该进候选")
	require.Equal(t, legacy.ID, candidates[0].ID)
}

// TestFindCandidates_TenantScoped 锁定 -tenant-id 真的收敛查询范围。
func TestFindCandidates_TenantScoped(t *testing.T) {
	client := enttest.Open(t, "sqlite3", testDSN())
	defer client.Close()
	ctx := context.Background()

	tenantA, userA := setupTenantAndUser(t, client, ctx, "ta")
	_, userB := setupTenantAndUser(t, client, ctx, "tb")

	_, err := client.Incident.Create().
		SetTitle("A租户事件").SetIncidentNumber("INC-TA-1").
		SetReporterID(userA.ID).SetTenantID(tenantA.ID).
		Save(ctx)
	require.NoError(t, err)
	_, err = client.Incident.Create().
		SetTitle("B租户事件").SetIncidentNumber("INC-TB-1").
		SetReporterID(userB.ID).SetTenantID(userB.TenantID).
		Save(ctx)
	require.NoError(t, err)

	scoped, err := findCandidates(ctx, client, tenantA.ID)
	require.NoError(t, err)
	require.Len(t, scoped, 1)
	require.Equal(t, tenantA.ID, scoped[0].TenantID)

	all, err := findCandidates(ctx, client, 0)
	require.NoError(t, err)
	require.Len(t, all, 2, "tenant-id<=0 时处理所有租户")
}

// TestBackfillOne_CreatesWorkItemAndLinksBack 是这个工具的核心回归：处理完一条候选后，
// tickets 表要有一条 record_class="incident" 的新行，incidents.work_item_id 要指回它，
// 且公共字段（标题/描述/优先级/请求人/租户）取自原 Incident。
func TestBackfillOne_CreatesWorkItemAndLinksBack(t *testing.T) {
	client := enttest.Open(t, "sqlite3", testDSN())
	defer client.Close()
	ctx := context.Background()

	tenant, user := setupTenantAndUser(t, client, ctx, "one")
	inc, err := client.Incident.Create().
		SetTitle("磁盘空间告警").SetDescription("根分区剩余不足5%").
		SetIncidentNumber("INC-ONE-1").SetPriority("high").
		SetReporterID(user.ID).SetTenantID(tenant.ID).
		Save(ctx)
	require.NoError(t, err)

	require.NoError(t, backfillOne(ctx, client, inc))

	after, err := client.Incident.Get(ctx, inc.ID)
	require.NoError(t, err)
	require.NotZero(t, after.WorkItemID, "回填后 work_item_id 必须非零")

	wi, err := client.Ticket.Get(ctx, after.WorkItemID)
	require.NoError(t, err)
	require.Equal(t, "incident", wi.RecordClass)
	require.Equal(t, inc.Title, wi.Title)
	require.Equal(t, inc.Description, wi.Description)
	require.Equal(t, inc.Priority, wi.Priority)
	require.Equal(t, inc.ReporterID, wi.RequesterID)
	require.Equal(t, inc.TenantID, wi.TenantID)
	require.Contains(t, wi.TicketNumber, "TKT-")
}

// TestBackfillOne_Idempotent 验证幂等性：对同一条已经回填过的 Incident 再跑一次
// backfillOne 不会产生第二条 WorkItem（真实使用场景是重复执行整个命令，候选查询本身
// 已经会把它过滤掉；这里直接测 backfillOne 这个更底层的函数，确认它自己也不会在
// work_item_id 已经非空时覆盖或新建）。
func TestBackfillOne_Idempotent(t *testing.T) {
	client := enttest.Open(t, "sqlite3", testDSN())
	defer client.Close()
	ctx := context.Background()

	tenant, user := setupTenantAndUser(t, client, ctx, "idem")
	inc, err := client.Incident.Create().
		SetTitle("重复运行测试").SetIncidentNumber("INC-IDEM-1").
		SetReporterID(user.ID).SetTenantID(tenant.ID).
		Save(ctx)
	require.NoError(t, err)

	require.NoError(t, backfillOne(ctx, client, inc))
	afterFirst, err := client.Incident.Get(ctx, inc.ID)
	require.NoError(t, err)
	firstWorkItemID := afterFirst.WorkItemID

	// 用回填之前的快照（work_item_id 仍为 0）再跑一次 backfillOne，模拟"候选列表算好之后、
	// 真正处理之前，这条记录已经被并发处理过"的竞态——backfillOne 内部的条件更新
	// （Where(incident.WorkItemIDIsNil())）必须拦住这次重复写入。
	err = backfillOne(ctx, client, inc)
	require.Error(t, err, "对已经回填过的 Incident 重复调用必须报错而不是静默创建第二条 WorkItem")

	afterSecond, err := client.Incident.Get(ctx, inc.ID)
	require.NoError(t, err)
	require.Equal(t, firstWorkItemID, afterSecond.WorkItemID, "已回填的 work_item_id 不应该被覆盖")

	count, err := client.Ticket.Query().Count(ctx)
	require.NoError(t, err)
	require.Equal(t, 1, count, "不应该产生第二条 WorkItem（也不应该留下孤儿 WorkItem 行）")
}

// TestGenerateBackfillTicketNumber_UsesIncidentCreatedAtMonth 锁定编号取的是 Incident
// 自己的创建月份，不是运行工具时的当前月份——回填的历史事件不应该全部堆到当月。
func TestGenerateBackfillTicketNumber_UsesIncidentCreatedAtMonth(t *testing.T) {
	client := enttest.Open(t, "sqlite3", testDSN())
	defer client.Close()
	ctx := context.Background()

	tenant, _ := setupTenantAndUser(t, client, ctx, "num")
	historical := time.Date(2024, 3, 15, 10, 0, 0, 0, time.UTC)

	number, err := generateBackfillTicketNumber(ctx, client, tenant.ID, historical)
	require.NoError(t, err)
	require.Equal(t, "TKT-202403-000001", number)
}
