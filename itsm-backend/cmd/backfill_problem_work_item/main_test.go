package main

import (
	"context"
	"fmt"
	"sync/atomic"
	"testing"
	"time"

	"itsm-backend/ent"
	"itsm-backend/ent/enttest"
	"itsm-backend/ent/workitemrelation"

	"github.com/stretchr/testify/require"

	_ "github.com/mattn/go-sqlite3"
)

// testDSN 为每个测试返回唯一的 SQLite 内存数据库 DSN，避免测试间数据库残留
// （与 cmd/backfill_incident_work_item/main_test.go 同一做法）。
var testDBCounter int64

func testDSN() string {
	return fmt.Sprintf("file:backfill_problem_wi_test_%d?mode=memory&cache=shared&_fk=1", atomic.AddInt64(&testDBCounter, 1))
}

func setupTenantAndUser(t *testing.T, client *ent.Client, ctx context.Context, code string) (*ent.Tenant, *ent.User) {
	t.Helper()
	tenant, err := client.Tenant.Create().
		SetName("T-" + code).SetCode(code).SetDomain(code + ".example.com").SetStatus("active").
		Save(ctx)
	require.NoError(t, err)
	user, err := client.User.Create().
		SetUsername("creator-" + code).SetEmail("creator-" + code + "@example.com").
		SetName("Creator").SetPasswordHash("hash").SetRole("agent").
		SetActive(true).SetTenantID(tenant.ID).
		Save(ctx)
	require.NoError(t, err)
	return tenant, user
}

// TestFindCandidates_OnlyMissingWorkItemID 锁定候选口径：只有 work_item_id 为空（NULL，
// 而不是新代码写入的合法非零值）且未被软删除的 Problem 才进候选。
func TestFindCandidates_OnlyMissingWorkItemID(t *testing.T) {
	client := enttest.Open(t, "sqlite3", testDSN())
	defer client.Close()
	ctx := context.Background()

	tenant, user := setupTenantAndUser(t, client, ctx, "cand")

	legacy, err := client.Problem.Create().
		SetTitle("存量问题-缺WorkItem").SetCreatedBy(user.ID).SetTenantID(tenant.ID).
		Save(ctx)
	require.NoError(t, err)

	// 已经迁移过（有 work_item_id）的问题——不应该出现在候选里。
	wi, err := client.Ticket.Create().
		SetTitle("已迁移问题对应的WorkItem").SetTicketNumber("TKT-CAND-ALREADY").
		SetRecordClass("problem").SetRequesterID(user.ID).SetTenantID(tenant.ID).
		Save(ctx)
	require.NoError(t, err)
	_, err = client.Problem.Create().
		SetTitle("已迁移问题").SetCreatedBy(user.ID).SetTenantID(tenant.ID).SetWorkItemID(wi.ID).
		Save(ctx)
	require.NoError(t, err)

	// 软删除且缺 work_item_id 的问题——不应该出现在候选里。
	deleted, err := client.Problem.Create().
		SetTitle("已软删除问题").SetCreatedBy(user.ID).SetTenantID(tenant.ID).
		Save(ctx)
	require.NoError(t, err)
	now := time.Now()
	_, err = client.Problem.UpdateOneID(deleted.ID).SetDeletedAt(now).Save(ctx)
	require.NoError(t, err)

	candidates, err := findCandidates(ctx, client, 0)
	require.NoError(t, err)
	require.Len(t, candidates, 1, "只有缺 work_item_id 且未软删除的问题应该进候选")
	require.Equal(t, legacy.ID, candidates[0].ID)
}

// TestFindCandidates_TenantScoped 锁定 -tenant-id 真的收敛查询范围。
func TestFindCandidates_TenantScoped(t *testing.T) {
	client := enttest.Open(t, "sqlite3", testDSN())
	defer client.Close()
	ctx := context.Background()

	tenantA, userA := setupTenantAndUser(t, client, ctx, "ta")
	_, userB := setupTenantAndUser(t, client, ctx, "tb")

	_, err := client.Problem.Create().
		SetTitle("A租户问题").SetCreatedBy(userA.ID).SetTenantID(tenantA.ID).
		Save(ctx)
	require.NoError(t, err)
	_, err = client.Problem.Create().
		SetTitle("B租户问题").SetCreatedBy(userB.ID).SetTenantID(userB.TenantID).
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
// tickets 表要有一条 record_class="problem" 的新行，problems.work_item_id 要指回它，
// 且公共字段（标题/描述/优先级/创建人/租户）取自原 Problem。
func TestBackfillOne_CreatesWorkItemAndLinksBack(t *testing.T) {
	client := enttest.Open(t, "sqlite3", testDSN())
	defer client.Close()
	ctx := context.Background()

	tenant, user := setupTenantAndUser(t, client, ctx, "one")
	prob, err := client.Problem.Create().
		SetTitle("数据库连接池耗尽").SetDescription("高峰期连接数打满").
		SetPriority("high").SetCreatedBy(user.ID).SetTenantID(tenant.ID).
		Save(ctx)
	require.NoError(t, err)

	migrated, err := backfillOne(ctx, client, prob)
	require.NoError(t, err)
	require.Zero(t, migrated, "没有旧关联工单时迁移条数应为 0")

	after, err := client.Problem.Get(ctx, prob.ID)
	require.NoError(t, err)
	require.NotZero(t, after.WorkItemID, "回填后 work_item_id 必须非零")

	wi, err := client.Ticket.Get(ctx, after.WorkItemID)
	require.NoError(t, err)
	require.Equal(t, "problem", wi.RecordClass)
	require.Equal(t, prob.Title, wi.Title)
	require.Equal(t, prob.Description, wi.Description)
	require.Equal(t, prob.Priority, wi.Priority)
	require.Equal(t, prob.CreatedBy, wi.RequesterID)
	require.Equal(t, prob.TenantID, wi.TenantID)
	require.Contains(t, wi.TicketNumber, "TKT-")
}

// TestBackfillOne_MigratesLegacyTicketEdgeToWorkItemRelation 锁定"旧 Problem<->Ticket
// 多对多 edge 迁移到 WorkItemRelation"这条要求：回填 work_item_id 的同时，把这条 Problem
// 通过旧 edge 关联的工单，逐条写成 WorkItemRelation（relation_type=related_to）。
func TestBackfillOne_MigratesLegacyTicketEdgeToWorkItemRelation(t *testing.T) {
	client := enttest.Open(t, "sqlite3", testDSN())
	defer client.Close()
	ctx := context.Background()

	tenant, user := setupTenantAndUser(t, client, ctx, "edge")
	prob, err := client.Problem.Create().
		SetTitle("存量问题带旧关联").SetCreatedBy(user.ID).SetTenantID(tenant.ID).
		Save(ctx)
	require.NoError(t, err)

	relatedTicket, err := client.Ticket.Create().
		SetTitle("旧关联工单").SetTicketNumber("TKT-EDGE-OLD").
		SetRequesterID(user.ID).SetTenantID(tenant.ID).
		Save(ctx)
	require.NoError(t, err)

	// 模拟迁移前的写路径：直接建立旧的 ent 多对多 edge。
	require.NoError(t, client.Problem.UpdateOneID(prob.ID).AddTicketIDs(relatedTicket.ID).Exec(ctx))

	migrated, err := backfillOne(ctx, client, prob)
	require.NoError(t, err)
	require.Equal(t, 1, migrated)

	after, err := client.Problem.Get(ctx, prob.ID)
	require.NoError(t, err)

	relations, err := client.WorkItemRelation.Query().
		Where(
			workitemrelation.TenantID(tenant.ID),
			workitemrelation.SourceWorkItemID(after.WorkItemID),
			workitemrelation.TargetWorkItemID(relatedTicket.ID),
			workitemrelation.RelationType(problemTicketRelationType),
			workitemrelation.DeletedAtIsNil(),
		).All(ctx)
	require.NoError(t, err)
	require.Len(t, relations, 1)
	require.Equal(t, prob.CreatedBy, relations[0].CreatedByID)
}

// TestBackfillOne_Idempotent 验证幂等性：对同一条已经回填过的 Problem 再跑一次
// backfillOne 不会产生第二条 WorkItem。
func TestBackfillOne_Idempotent(t *testing.T) {
	client := enttest.Open(t, "sqlite3", testDSN())
	defer client.Close()
	ctx := context.Background()

	tenant, user := setupTenantAndUser(t, client, ctx, "idem")
	prob, err := client.Problem.Create().
		SetTitle("重复运行测试").SetCreatedBy(user.ID).SetTenantID(tenant.ID).
		Save(ctx)
	require.NoError(t, err)

	_, err = backfillOne(ctx, client, prob)
	require.NoError(t, err)
	afterFirst, err := client.Problem.Get(ctx, prob.ID)
	require.NoError(t, err)
	firstWorkItemID := afterFirst.WorkItemID

	// 用回填之前的快照（work_item_id 仍为 0）再跑一次 backfillOne，模拟"候选列表算好之后、
	// 真正处理之前，这条记录已经被并发处理过"的竞态。
	_, err = backfillOne(ctx, client, prob)
	require.Error(t, err, "对已经回填过的 Problem 重复调用必须报错而不是静默创建第二条 WorkItem")

	afterSecond, err := client.Problem.Get(ctx, prob.ID)
	require.NoError(t, err)
	require.Equal(t, firstWorkItemID, afterSecond.WorkItemID, "已回填的 work_item_id 不应该被覆盖")

	count, err := client.Ticket.Query().Count(ctx)
	require.NoError(t, err)
	require.Equal(t, 1, count, "不应该产生第二条 WorkItem（也不应该留下孤儿 WorkItem 行）")
}

// TestGenerateBackfillTicketNumber_UsesProblemCreatedAtMonth 锁定编号取的是 Problem
// 自己的创建月份，不是运行工具时的当前月份。
func TestGenerateBackfillTicketNumber_UsesProblemCreatedAtMonth(t *testing.T) {
	client := enttest.Open(t, "sqlite3", testDSN())
	defer client.Close()
	ctx := context.Background()

	historical := time.Date(2024, 3, 15, 10, 0, 0, 0, time.UTC)

	number, err := generateBackfillTicketNumber(ctx, client, historical)
	require.NoError(t, err)
	require.Equal(t, "TKT-202403-000001", number)
}

// TestGenerateBackfillTicketNumber_GlobalAcrossTenants 锁定编号计数是全局维度（不区分
// 租户），与 tickets.ticket_number 的全局唯一索引保持一致——如果按租户维度计数，两个不同
// 租户各自当月第一次建单都会生成同一个 "TKT-YYYYMM-000001" 并撞上全局唯一约束（这是
// handlers/problem.EntRepository.generateWorkItemTicketNumber 在实现过程中发现并修正的
// 同类缺陷，这里为回填工具锁定同样正确的行为）。
func TestGenerateBackfillTicketNumber_GlobalAcrossTenants(t *testing.T) {
	client := enttest.Open(t, "sqlite3", testDSN())
	defer client.Close()
	ctx := context.Background()

	tenantA, userA := setupTenantAndUser(t, client, ctx, "gnum-a")
	tenantB, userB := setupTenantAndUser(t, client, ctx, "gnum-b")
	createdAt := time.Date(2024, 6, 1, 0, 0, 0, 0, time.UTC)

	probA, err := client.Problem.Create().
		SetTitle("A first").SetCreatedBy(userA.ID).SetTenantID(tenantA.ID).SetCreatedAt(createdAt).
		Save(ctx)
	require.NoError(t, err)
	_, err = backfillOne(ctx, client, probA)
	require.NoError(t, err)

	probB, err := client.Problem.Create().
		SetTitle("B first").SetCreatedBy(userB.ID).SetTenantID(tenantB.ID).SetCreatedAt(createdAt).
		Save(ctx)
	require.NoError(t, err)
	_, err = backfillOne(ctx, client, probB)
	require.NoError(t, err, "tenant B 的首条问题回填不应该因为工单编号撞号而失败")

	afterA, err := client.Problem.Get(ctx, probA.ID)
	require.NoError(t, err)
	afterB, err := client.Problem.Get(ctx, probB.ID)
	require.NoError(t, err)
	wiA, err := client.Ticket.Get(ctx, afterA.WorkItemID)
	require.NoError(t, err)
	wiB, err := client.Ticket.Get(ctx, afterB.WorkItemID)
	require.NoError(t, err)
	require.NotEqual(t, wiA.TicketNumber, wiB.TicketNumber)
}
