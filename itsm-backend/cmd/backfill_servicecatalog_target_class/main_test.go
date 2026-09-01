package main

import (
	"context"
	"fmt"
	"sync/atomic"
	"testing"

	"itsm-backend/ent"
	"itsm-backend/ent/enttest"
	"itsm-backend/handlers/service_catalog"

	"github.com/stretchr/testify/require"

	_ "github.com/mattn/go-sqlite3"
)

// testDSN 为每个测试返回唯一的 SQLite 内存数据库 DSN，避免测试间数据库残留。
var testDBCounter int64

func testDSN() string {
	return fmt.Sprintf("file:backfill_sc_target_class_test_%d?mode=memory&cache=shared&_fk=1", atomic.AddInt64(&testDBCounter, 1))
}

func setupTenant(t *testing.T, client *ent.Client, ctx context.Context, code string) *ent.Tenant {
	t.Helper()
	tenant, err := client.Tenant.Create().
		SetName("T-" + code).SetCode(code).SetDomain(code + ".example.com").SetStatus("active").
		Save(ctx)
	require.NoError(t, err)
	return tenant
}

// TestFindCandidates_OnlyEmptyTargetClass 锁定候选口径：target_class 为 NULL 或空字符串
// 的行才进候选；已经有值（无论是回填过的还是自愈同步写过的）的行不应该被再次触碰。
func TestFindCandidates_OnlyEmptyTargetClass(t *testing.T) {
	client := enttest.Open(t, "sqlite3", testDSN())
	defer client.Close()
	ctx := context.Background()

	tenant := setupTenant(t, client, ctx, "cand")

	// 从未写过 target_class 的存量行——NULL，进候选。
	neverSet, err := client.ServiceCatalog.Create().
		SetName("存量-未回填").SetCategory("cat").SetDeliveryTime(1).SetStatus("enabled").
		SetTenantID(tenant.ID).SetItsmType("Incident").
		Save(ctx)
	require.NoError(t, err)

	// 显式写过空字符串的行——同样应该进候选（Optional() 非 Nillable 的字符串列，Go 侧
	// 空值和 NULL 都表现为 ""，两种取值都要覆盖）。
	emptyString, err := client.ServiceCatalog.Create().
		SetName("存量-空字符串").SetCategory("cat").SetDeliveryTime(1).SetStatus("enabled").
		SetTenantID(tenant.ID).SetItsmType("Change").SetTargetClass("").
		Save(ctx)
	require.NoError(t, err)

	// 已经有值的行——不应该进候选。
	_, err = client.ServiceCatalog.Create().
		SetName("已回填").SetCategory("cat").SetDeliveryTime(1).SetStatus("enabled").
		SetTenantID(tenant.ID).SetItsmType("Incident").SetTargetClass("incident").
		Save(ctx)
	require.NoError(t, err)

	candidates, err := findCandidates(ctx, client, 0)
	require.NoError(t, err)
	gotIDs := make(map[int]bool, len(candidates))
	for _, c := range candidates {
		gotIDs[c.ID] = true
	}
	require.Len(t, candidates, 2, "只有 target_class 为空的行应该进候选")
	require.True(t, gotIDs[neverSet.ID])
	require.True(t, gotIDs[emptyString.ID])
}

// TestFindCandidates_TenantScoped 锁定 -tenant-id 真的收敛查询范围。
func TestFindCandidates_TenantScoped(t *testing.T) {
	client := enttest.Open(t, "sqlite3", testDSN())
	defer client.Close()
	ctx := context.Background()

	tenantA := setupTenant(t, client, ctx, "ta")
	tenantB := setupTenant(t, client, ctx, "tb")

	_, err := client.ServiceCatalog.Create().
		SetName("A租户目录").SetCategory("cat").SetDeliveryTime(1).SetStatus("enabled").
		SetTenantID(tenantA.ID).SetItsmType("Request").
		Save(ctx)
	require.NoError(t, err)
	_, err = client.ServiceCatalog.Create().
		SetName("B租户目录").SetCategory("cat").SetDeliveryTime(1).SetStatus("enabled").
		SetTenantID(tenantB.ID).SetItsmType("Request").
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

// TestBackfillOne_MapsEachITSMTypeToExpectedTargetClass 是这个工具的核心回归：验收标准
// 明确要求的三个映射——Incident→incident、Change→change_request、Request→service_request_item——
// 逐一验证跑完之后落库的 target_class 精确等于预期值。
func TestBackfillOne_MapsEachITSMTypeToExpectedTargetClass(t *testing.T) {
	cases := []struct {
		itsmType string
		want     string
	}{
		{"Incident", service_catalog.TargetClassIncident},
		{"Change", service_catalog.TargetClassChangeRequest},
		{"Request", service_catalog.TargetClassServiceRequestItem},
		{"", service_catalog.TargetClassServiceRequestItem}, // 未知/空值 fail-safe
		{"SomethingUnexpected", service_catalog.TargetClassServiceRequestItem},
	}

	for _, tc := range cases {
		t.Run(tc.itsmType, func(t *testing.T) {
			client := enttest.Open(t, "sqlite3", testDSN())
			defer client.Close()
			ctx := context.Background()

			tenant := setupTenant(t, client, ctx, "map")
			cat, err := client.ServiceCatalog.Create().
				SetName("目录-" + tc.itsmType).SetCategory("cat").SetDeliveryTime(1).SetStatus("enabled").
				SetTenantID(tenant.ID).SetItsmType(tc.itsmType).
				Save(ctx)
			require.NoError(t, err)

			require.NoError(t, backfillOne(ctx, client, cat))

			after, err := client.ServiceCatalog.Get(ctx, cat.ID)
			require.NoError(t, err)
			require.Equal(t, tc.want, after.TargetClass)
		})
	}
}

// TestBackfillOne_Idempotent 验证幂等性：对同一条已经回填过的行再跑一次 backfillOne
// 必须报错而不是静默覆盖（万一并发运行或重复调用，不应该有第二次生效的写入）。
func TestBackfillOne_Idempotent(t *testing.T) {
	client := enttest.Open(t, "sqlite3", testDSN())
	defer client.Close()
	ctx := context.Background()

	tenant := setupTenant(t, client, ctx, "idem")
	cat, err := client.ServiceCatalog.Create().
		SetName("重复运行测试").SetCategory("cat").SetDeliveryTime(1).SetStatus("enabled").
		SetTenantID(tenant.ID).SetItsmType("Incident").
		Save(ctx)
	require.NoError(t, err)

	require.NoError(t, backfillOne(ctx, client, cat))
	afterFirst, err := client.ServiceCatalog.Get(ctx, cat.ID)
	require.NoError(t, err)
	require.Equal(t, service_catalog.TargetClassIncident, afterFirst.TargetClass)

	// 用回填之前的快照（target_class 仍为空）再跑一次，模拟并发竞态——backfillOne 内部的
	// 条件更新必须拦住这次重复写入。
	err = backfillOne(ctx, client, cat)
	require.Error(t, err, "对已经回填过的行重复调用必须报错")

	afterSecond, err := client.ServiceCatalog.Get(ctx, cat.ID)
	require.NoError(t, err)
	require.Equal(t, afterFirst.TargetClass, afterSecond.TargetClass)
}
