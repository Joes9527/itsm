package service

import (
	"context"
	"testing"

	"github.com/stretchr/testify/require"
	"itsm-backend/authorization"
	"itsm-backend/ent"
	"itsm-backend/ent/enttest"
	"itsm-backend/ent/servicecatalog"
)

// catalogReferenceSourceForTest 模拟服务目录所有者注册的名称契约
// （生产实现见 handlers/service_catalog/cti_reference_source.go，由启动装配注册）。
type catalogReferenceSourceForTest struct{}

func (catalogReferenceSourceForTest) Kind() string     { return "catalog" }
func (catalogReferenceSourceForTest) Resource() string { return "service_catalog" }

func (catalogReferenceSourceForTest) Names(ctx context.Context, client *ent.Client, tenantID int, ids []int) (map[int]string, error) {
	rows, err := client.ServiceCatalog.Query().
		Where(servicecatalog.TenantIDEQ(tenantID), servicecatalog.IDIn(ids...)).
		All(ctx)
	if err != nil {
		return nil, err
	}
	names := make(map[int]string, len(rows))
	for _, row := range rows {
		names[row.ID] = row.Name
	}
	return names, nil
}

// 引用清单的核心安全契约（B3）：
//  1. 真实引用扫描不受调用者权限影响（维护保护必须看到全部引用）；
//  2. 无权查看某类对象的调用者只能得到"存在引用"，拿不到名称、条数与 ID；
//  3. 分页只在有权查看时生效，且不越权暴露其它租户对象。
func TestCTIReferenceViewFiltersDetailsByPermission(t *testing.T) {
	client := enttest.Open(t, "sqlite3", testDSN())
	defer client.Close()
	ctx := context.Background()
	owner := NewTicketCategoryService(client)

	tenant := client.Tenant.Create().SetName("Refs").SetCode("refs").SetDomain("refs.test").SetStatus("active").SaveX(ctx)
	other := client.Tenant.Create().SetName("Other").SetCode("refs-other").SetDomain("o.test").SetStatus("active").SaveX(ctx)
	root, err := owner.CreateCategory(ctx, &CreateCategoryRequest{Name: "ref-l1", Code: "ref-l1", IsActive: true, TenantID: tenant.ID})
	require.NoError(t, err)
	middle, err := owner.CreateCategory(ctx, &CreateCategoryRequest{Name: "ref-l2", Code: "ref-l2", ParentID: root.ID, IsActive: true, TenantID: tenant.ID})
	require.NoError(t, err)
	leaf, err := owner.CreateCategory(ctx, &CreateCategoryRequest{Name: "ref-l3", Code: "ref-l3", ParentID: middle.ID, IsActive: true, TenantID: tenant.ID})
	require.NoError(t, err)

	requester := client.User.Create().SetTenantID(tenant.ID).SetUsername("refs-user").SetName("U").
		SetRole("end_user").SetEmail("refs-user@example.test").SetPasswordHash("x").SaveX(ctx)
	// 引用对象：目录、SLA、分派规则、模板各一，全部指向该分类（按子树扫描）。
	client.ServiceCatalog.Create().SetTenantID(tenant.ID).SetName("引用目录").SetTargetClass("service_request_item").
		SetDefaultTicketCategoryID(leaf.ID).SetStatus("enabled").SaveX(ctx)
	client.SLADefinition.Create().SetTenantID(tenant.ID).SetName("引用SLA").SetServiceType("incident").
		SetPriority("high").SetCategoryIds([]int{leaf.ID}).SetIsActive(true).SaveX(ctx)
	client.TicketAssignmentRule.Create().SetTenantID(tenant.ID).SetName("引用分派规则").
		SetConditions([]map[string]interface{}{{"field": "category_id", "operator": "equals", "value": float64(middle.ID)}}).
		SetIsActive(true).SaveX(ctx)
	client.TicketTemplate.Create().SetTenantID(tenant.ID).SetName("引用模板").SetCategory("ref-l2").SetCategoryIds([]int{middle.ID}).SaveX(ctx)
	client.Ticket.Create().SetTenantID(tenant.ID).SetRequesterID(requester.ID).SetTitle("引用工单").
		SetTicketNumber("REF-1").SetRecordClass("generic").SetStatus("new").SetPriority("medium").
		SetCategoryID(leaf.ID).SaveX(ctx)
	// 其它租户的同名对象不得出现在结果里。
	foreignCategory := client.TicketCategory.Create().SetTenantID(other.ID).SetName("ref-l1").SetCode("ref-l1").
		SetLevel(1).SetIsActive(true).SaveX(ctx)
	client.ServiceCatalog.Create().SetTenantID(other.ID).SetName("外部目录").SetTargetClass("service_request_item").
		SetDefaultTicketCategoryID(foreignCategory.ID).SetStatus("enabled").SaveX(ctx)

	groups := func(view CTIReferenceView) map[string]CTIReferenceGroup {
		result := make(map[string]CTIReferenceGroup, len(view.Groups))
		for _, group := range view.Groups {
			result[group.Kind] = group
		}
		return result
	}

	// 无权查看任何模块的角色：只有"存在引用"，没有名称/条数/ID。
	restricted, err := owner.ReferenceView(ctx, CTIReferenceQuery{TenantID: tenant.ID, ActorRole: "viewer", CategoryID: middle.ID, IncludeWorkItems: true})
	require.NoError(t, err)
	require.True(t, restricted.Blocking, "真实引用必须阻止维护动作，即使调用者看不到对象")
	require.NotEmpty(t, restricted.CategoryPath)
	restrictedGroups := groups(restricted)
	for kind, group := range restrictedGroups {
		if !group.Referenced {
			continue
		}
		require.False(t, group.Visible, "kind %s must not expose details without permission", kind)
		require.Zero(t, group.Total, "kind %s must not leak a count", kind)
		require.Empty(t, group.Items, "kind %s must not leak names", kind)
	}
	require.True(t, restrictedGroups["catalog"].Referenced)
	require.True(t, restrictedGroups["ticket_template"].Referenced)
	require.True(t, restrictedGroups["work_item"].Referenced)

	// 授予各模块 read 后：可见名称与条数，且只包含本租户对象。
	RegisterCTIReferenceNameSource(catalogReferenceSourceForTest{})
	reader := client.Role.Create().SetTenantID(tenant.ID).SetCode("cti_reader").SetName("CTI reader").SaveX(ctx)
	grant := func(resource string) {
		p := client.Permission.Create().SetTenantID(tenant.ID).SetCode(resource + ":read").SetName(resource).
			SetResource(resource).SetAction("read").SaveX(ctx)
		client.RolePermission.Create().SetTenantID(tenant.ID).SetRoleID(reader.ID).SetPermissionID(p.ID).SaveX(ctx)
	}
	for _, resource := range []string{"service_catalog", "sla", "assignment_rule", "automation_rule", "ticket_template", "ticket"} {
		grant(resource)
	}
	authorization.InvalidateAllPermissionCaches()
	t.Cleanup(authorization.InvalidateAllPermissionCaches)
	full, err := owner.ReferenceView(ctx, CTIReferenceQuery{TenantID: tenant.ID, ActorRole: "cti_reader", CategoryID: middle.ID, IncludeWorkItems: true})
	require.NoError(t, err)
	fullGroups := groups(full)
	require.True(t, fullGroups["catalog"].Visible)
	require.Equal(t, 1, fullGroups["catalog"].Total)
	require.Equal(t, "引用目录", fullGroups["catalog"].Items[0].Name)
	require.Equal(t, 1, fullGroups["sla_definition"].Total)
	require.Equal(t, "引用SLA", fullGroups["sla_definition"].Items[0].Name)
	require.Equal(t, 1, fullGroups["assignment_rule"].Total)
	require.Equal(t, 1, fullGroups["ticket_template"].Total)
	require.Equal(t, 1, fullGroups["work_item"].Total)
	require.NotContains(t, fullGroups["catalog"].Items[0].Name, "外部")

	// 分页只在有权查看时生效：pageSize=1 时最多 1 条，但总数仍是真实条数。
	client.ServiceCatalog.Create().SetTenantID(tenant.ID).SetName("引用目录2").SetTargetClass("service_request_item").
		SetDefaultTicketCategoryID(middle.ID).SetStatus("enabled").SaveX(ctx)
	paged, err := owner.ReferenceView(ctx, CTIReferenceQuery{TenantID: tenant.ID, ActorRole: "cti_reader", CategoryID: middle.ID, Page: 1, PageSize: 1})
	require.NoError(t, err)
	pagedGroups := groups(paged)
	require.Equal(t, 2, pagedGroups["catalog"].Total)
	require.Len(t, pagedGroups["catalog"].Items, 1)
	require.Equal(t, 1, paged.Page)
	require.Equal(t, 1, paged.PageSize)

	// 越界页码返回空页而不是错误。
	beyond, err := owner.ReferenceView(ctx, CTIReferenceQuery{TenantID: tenant.ID, ActorRole: "cti_reader", CategoryID: middle.ID, Page: 99, PageSize: 10})
	require.NoError(t, err)
	require.Empty(t, groups(beyond)["catalog"].Items)

	// 跨租户分类：与不存在一致的错误，不泄露存在性。
	_, err = owner.ReferenceView(ctx, CTIReferenceQuery{TenantID: tenant.ID, ActorRole: "cti_reader", CategoryID: foreignCategory.ID})
	require.ErrorIs(t, err, ErrCTICategoryNotFound)
	_, err = owner.ReferenceView(ctx, CTIReferenceQuery{TenantID: tenant.ID, ActorRole: "cti_reader", CategoryID: 999999})
	require.ErrorIs(t, err, ErrCTICategoryNotFound)
	_, err = owner.ReferenceView(ctx, CTIReferenceQuery{TenantID: 0, ActorRole: "cti_reader", CategoryID: leaf.ID})
	require.ErrorIs(t, err, ErrCTIPathOutsideTenant)

	// pageSize 上限被强制收敛，避免一次拉取过大。
	capped, err := owner.ReferenceView(ctx, CTIReferenceQuery{TenantID: tenant.ID, ActorRole: "cti_reader", CategoryID: middle.ID, PageSize: 5000})
	require.NoError(t, err)
	require.Equal(t, CTIReferenceMaxPageSize, capped.PageSize)
}
