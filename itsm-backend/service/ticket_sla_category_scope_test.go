package service

import (
	"context"
	"testing"

	"github.com/stretchr/testify/require"
	"go.uber.org/zap/zaptest"
	"itsm-backend/ent/enttest"
)

// SLA 分类匹配必须在**全部活跃候选**中查找持有该分类的定义，并且顺序确定。
// 既有实现先取一条无排序的活跃定义再判断它是否含该分类，会漏掉真正持有该分类的定义
// 并静默退化到通用 SLA —— 本用例即该缺口的回归。
func TestTicketSLACategoryMatchScansAllActiveCandidates(t *testing.T) {
	client := enttest.Open(t, "sqlite3", testDSN())
	defer client.Close()
	ctx := context.Background()
	owner := NewTicketSLAService(client, zaptest.NewLogger(t).Sugar())
	categories := NewTicketCategoryService(client)

	tenant := client.Tenant.Create().SetName("SLA CTI").SetCode("sla-cti").SetDomain("sla.test").SetStatus("active").SaveX(ctx)
	root, err := categories.CreateCategory(ctx, &CreateCategoryRequest{Name: "sla-l1", Code: "sla-l1", IsActive: true, TenantID: tenant.ID})
	require.NoError(t, err)
	middle, err := categories.CreateCategory(ctx, &CreateCategoryRequest{Name: "sla-l2", Code: "sla-l2", ParentID: root.ID, IsActive: true, TenantID: tenant.ID})
	require.NoError(t, err)
	leaf, err := categories.CreateCategory(ctx, &CreateCategoryRequest{Name: "sla-l3", Code: "sla-l3", ParentID: middle.ID, IsActive: true, TenantID: tenant.ID})
	require.NoError(t, err)

	// 先建通用 SLA（type+priority 都能命中），再建持有该分类的 SLA。
	// 旧实现会先取到通用 SLA、判定不含分类后直接退化到 type/priority 匹配。
	generic := client.SLADefinition.Create().SetName("generic").SetServiceType("incident").SetPriority("high").
		SetResponseTime(480).SetResolutionTime(1440).SetIsActive(true).SetTenantID(tenant.ID).SaveX(ctx)
	categoryScoped := client.SLADefinition.Create().SetName("category").SetServiceType("incident").SetPriority("high").
		SetCategoryIds([]int{leaf.ID}).SetResponseTime(15).SetResolutionTime(60).SetIsActive(true).SetTenantID(tenant.ID).SaveX(ctx)

	definition, err := owner.getSLADefinition(ctx, tenant.ID, "incident", "high", leaf.ID)
	require.NoError(t, err)
	require.Equal(t, categoryScoped.ID, definition.ID, "the SLA that actually holds the classification must win")
	require.NotEqual(t, generic.ID, definition.ID)

	// 精确语义保持：祖先即使出现在 category_ids 中也不命中，回退到 type/priority。
	ancestorScoped := client.SLADefinition.Create().SetName("ancestor").SetServiceType("incident").SetPriority("high").
		SetCategoryIds([]int{middle.ID}).SetResponseTime(5).SetResolutionTime(10).SetIsActive(true).SetTenantID(tenant.ID).SaveX(ctx)
	_ = ancestorScoped
	definition, err = owner.getSLADefinition(ctx, tenant.ID, "incident", "high", leaf.ID)
	require.NoError(t, err)
	require.Equal(t, categoryScoped.ID, definition.ID)

	// 停用的分类 SLA 不参与匹配。
	client.SLADefinition.UpdateOneID(categoryScoped.ID).SetIsActive(false).ExecX(ctx)
	definition, err = owner.getSLADefinition(ctx, tenant.ID, "incident", "high", leaf.ID)
	require.NoError(t, err)
	require.Equal(t, generic.ID, definition.ID)

	// 未分类：保持既有 type/priority 行为。
	definition, err = owner.getSLADefinition(ctx, tenant.ID, "incident", "high", 0)
	require.NoError(t, err)
	require.Equal(t, generic.ID, definition.ID)
}

func TestTicketSLACategoryMatchIsDeterministicAndFailsClosed(t *testing.T) {
	client := enttest.Open(t, "sqlite3", testDSN())
	defer client.Close()
	ctx := context.Background()
	owner := NewTicketSLAService(client, zaptest.NewLogger(t).Sugar())
	categories := NewTicketCategoryService(client)

	tenant := client.Tenant.Create().SetName("SLA CTI 2").SetCode("sla-cti-2").SetDomain("sla2.test").SetStatus("active").SaveX(ctx)
	root, err := categories.CreateCategory(ctx, &CreateCategoryRequest{Name: "d-l1", Code: "d-l1", IsActive: true, TenantID: tenant.ID})
	require.NoError(t, err)
	middle, err := categories.CreateCategory(ctx, &CreateCategoryRequest{Name: "d-l2", Code: "d-l2", ParentID: root.ID, IsActive: true, TenantID: tenant.ID})
	require.NoError(t, err)
	leaf, err := categories.CreateCategory(ctx, &CreateCategoryRequest{Name: "d-l3", Code: "d-l3", ParentID: middle.ID, IsActive: true, TenantID: tenant.ID})
	require.NoError(t, err)

	first := client.SLADefinition.Create().SetName("first").SetServiceType("incident").SetPriority("high").
		SetCategoryIds([]int{leaf.ID}).SetResponseTime(10).SetResolutionTime(20).SetIsActive(true).SetTenantID(tenant.ID).SaveX(ctx)
	second := client.SLADefinition.Create().SetName("second").SetServiceType("incident").SetPriority("high").
		SetCategoryIds([]int{leaf.ID}).SetResponseTime(30).SetResolutionTime(60).SetIsActive(true).SetTenantID(tenant.ID).SaveX(ctx)
	require.Less(t, first.ID, second.ID)

	// 多个候选同时命中：顺序必须确定（id 升序，先建先得），不得依赖数据库返回顺序。
	for attempt := 0; attempt < 3; attempt++ {
		definition, err := owner.getSLADefinition(ctx, tenant.ID, "incident", "high", leaf.ID)
		require.NoError(t, err)
		require.Equal(t, first.ID, definition.ID)
	}

	// 分类存在但解析不出路径（跨租户/已删除）：必须报错，不得静默改用通用 SLA。
	foreign := client.Tenant.Create().SetName("Foreign").SetCode("sla-foreign").SetDomain("f.test").SetStatus("active").SaveX(ctx)
	foreignCategory, err := categories.CreateCategory(ctx, &CreateCategoryRequest{Name: "f", Code: "f", IsActive: true, TenantID: foreign.ID})
	require.NoError(t, err)
	_, err = owner.getSLADefinition(ctx, tenant.ID, "incident", "high", foreignCategory.ID)
	require.Error(t, err, "an unresolvable classification must fail closed instead of falling back")
}

func TestCategoryMatchesPathKeepsExactSemantics(t *testing.T) {
	path := ctiScopePath()
	matched, err := categoryMatchesPath([]int{1}, path)
	require.NoError(t, err)
	require.False(t, matched, "an ancestor in category_ids must not match under the preserved exact semantics")
	matched, err = categoryMatchesPath([]int{3}, path)
	require.NoError(t, err)
	require.True(t, matched)
	matched, err = categoryMatchesPath(nil, path)
	require.NoError(t, err)
	require.False(t, matched)
	matched, err = categoryMatchesPath([]int{3}, nil)
	require.NoError(t, err)
	require.False(t, matched, "an unclassified item cannot match")
}
