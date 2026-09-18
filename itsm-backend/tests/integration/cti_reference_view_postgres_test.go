//go:build integration_postgres

package integration

import (
	"testing"

	"itsm-backend/authorization"
	service_catalog "itsm-backend/handlers/service_catalog"
	"itsm-backend/service"

	"github.com/stretchr/testify/require"
)

// B3 的真实 PostgreSQL 证据：分类引用清单的 RBAC 过滤与租户隔离。
//
// 维护保护必须看到**全部**真实引用（即使调用者无权查看对象）；
// 明细（名称/条数/ID）只在调用者拥有对应模块 read 权限时返回；
// 跨租户分类与不存在的分类返回同一错误，不泄露存在性。
func TestCTIReferenceViewPostgres(t *testing.T) {
	// 引用名称由各资源所有者提供：这里使用**生产注册函数**装配目录域，
	// 与 internal/bootstrap 的启动装配走同一条代码路径。
	service_catalog.RegisterCTIReferenceNameSource()
	f := newCTIStructureFixture(t)
	categories := service.NewTicketCategoryService(f.client)
	tenant := f.tenant(t, "cti-reference-view")
	other := f.tenant(t, "cti-reference-foreign")

	tree := func(tenantID int, prefix string) [3]int {
		ids := [3]int{}
		parent := 0
		for index, code := range []string{prefix + "-l1", prefix + "-l2", prefix + "-l3"} {
			record, err := categories.CreateCategory(f.ctx, &service.CreateCategoryRequest{Name: code, Code: code, ParentID: parent, IsActive: true, TenantID: tenantID})
			require.NoError(t, err)
			ids[index] = record.ID
			parent = record.ID
		}
		return ids
	}
	primary := tree(tenant, "refview")
	foreignTree := tree(other, "refview-foreign")
	requester := f.client.User.Create().SetTenantID(tenant).SetUsername("refview-user").SetName("User").
		SetRole("end_user").SetEmail("refview-user@example.test").SetPasswordHash("x").SaveX(f.ctx)

	// 本租户引用：目录、SLA、分派规则、模板、工单各一（都指向子树内节点）。
	catalog := f.client.ServiceCatalog.Create().SetTenantID(tenant).SetName("引用目录").SetTargetClass("service_request_item").
		SetDefaultTicketCategoryID(primary[2]).SetStatus("enabled").SaveX(f.ctx)
	sla := f.client.SLADefinition.Create().SetTenantID(tenant).SetName("引用SLA").SetServiceType("incident").
		SetPriority("high").SetCategoryIds([]int{primary[2]}).SetIsActive(true).SaveX(f.ctx)
	rule := f.client.TicketAssignmentRule.Create().SetTenantID(tenant).SetName("引用规则").
		SetConditions([]map[string]interface{}{{"field": "category_id", "operator": "equals", "value": float64(primary[1])}}).SetIsActive(true).SaveX(f.ctx)
	template := f.client.TicketTemplate.Create().SetTenantID(tenant).SetName("引用模板").SetCategory("refview-l2").
		SetCategoryIds([]int{primary[1]}).SaveX(f.ctx)
	item := f.client.Ticket.Create().SetTenantID(tenant).SetRequesterID(requester.ID).SetTitle("引用工单").
		SetTicketNumber("REFVIEW-1").SetRecordClass("generic").SetStatus("new").SetPriority("medium").
		SetCategoryID(primary[2]).SaveX(f.ctx)
	// 其它租户对象：即使 ID 相邻也不得出现在本租户结果中。
	f.client.ServiceCatalog.Create().SetTenantID(other).SetName("外部目录").SetTargetClass("service_request_item").
		SetDefaultTicketCategoryID(foreignTree[2]).SetStatus("enabled").SaveX(f.ctx)

	groups := func(view service.CTIReferenceView) map[string]service.CTIReferenceGroup {
		result := make(map[string]service.CTIReferenceGroup, len(view.Groups))
		for _, group := range view.Groups {
			result[group.Kind] = group
		}
		return result
	}

	t.Run("without_module_permissions_only_existence_is_reported", func(t *testing.T) {
		view, err := categories.ReferenceView(f.ctx, service.CTIReferenceQuery{
			TenantID: tenant, ActorRole: "viewer", CategoryID: primary[1], IncludeWorkItems: true,
		})
		require.NoError(t, err)
		require.True(t, view.Blocking, "真实引用必须阻止维护动作")
		require.Len(t, view.CategoryPath, 2)
		for kind, group := range groups(view) {
			if !group.Referenced {
				continue
			}
			require.False(t, group.Visible, "kind %s", kind)
			require.Zero(t, group.Total, "kind %s must not leak a count", kind)
			require.Empty(t, group.Items, "kind %s must not leak names", kind)
		}
	})

	t.Run("with_module_permissions_details_are_scoped_to_the_tenant", func(t *testing.T) {
		role := f.client.Role.Create().SetTenantID(tenant).SetCode("cti_ref_reader").SetName("reader").SaveX(f.ctx)
		for _, resource := range []string{"service_catalog", "sla", "assignment_rule", "automation_rule", "ticket_template", "ticket"} {
			permission := f.client.Permission.Create().SetTenantID(tenant).SetCode(resource + ":read").SetName(resource).
				SetResource(resource).SetAction("read").SaveX(f.ctx)
			f.client.RolePermission.Create().SetTenantID(tenant).SetRoleID(role.ID).SetPermissionID(permission.ID).SaveX(f.ctx)
		}
		authorization.InvalidateAllPermissionCaches()
		t.Cleanup(authorization.InvalidateAllPermissionCaches)

		view, err := categories.ReferenceView(f.ctx, service.CTIReferenceQuery{
			TenantID: tenant, ActorRole: "cti_ref_reader", CategoryID: primary[1], IncludeWorkItems: true,
		})
		require.NoError(t, err)
		visible := groups(view)
		require.True(t, visible["catalog"].Visible)
		require.Equal(t, []service.CTIReferenceItem{{ID: catalog.ID, Name: "引用目录"}}, visible["catalog"].Items)
		require.Equal(t, []service.CTIReferenceItem{{ID: sla.ID, Name: "引用SLA"}}, visible["sla_definition"].Items)
		require.Equal(t, []service.CTIReferenceItem{{ID: rule.ID, Name: "引用规则"}}, visible["assignment_rule"].Items)
		require.Equal(t, []service.CTIReferenceItem{{ID: template.ID, Name: "引用模板"}}, visible["ticket_template"].Items)
		require.Equal(t, 1, visible["work_item"].Total, "the referencing work item is counted, not listed")
		require.Empty(t, visible["work_item"].Items)

		// 无权查看明细的种类只报告存在性，且不提供名称/条数。
		require.False(t, visible["process_binding"].Visible)
		require.False(t, visible["incident_escalation_rule"].Visible)

		// 分页：pageSize=1 时目录组最多 1 条，总数仍为真实条数。
		f.client.ServiceCatalog.Create().SetTenantID(tenant).SetName("引用目录2").SetTargetClass("service_request_item").
			SetDefaultTicketCategoryID(primary[2]).SetStatus("enabled").SaveX(f.ctx)
		paged, err := categories.ReferenceView(f.ctx, service.CTIReferenceQuery{
			TenantID: tenant, ActorRole: "cti_ref_reader", CategoryID: primary[1], IncludeWorkItems: true, Page: 1, PageSize: 1,
		})
		require.NoError(t, err)
		require.Equal(t, 2, groups(paged)["catalog"].Total)
		require.Len(t, groups(paged)["catalog"].Items, 1)
	})

	t.Run("cross_tenant_and_missing_categories_fail_identically", func(t *testing.T) {
		_, err := categories.ReferenceView(f.ctx, service.CTIReferenceQuery{TenantID: tenant, ActorRole: "super_admin", CategoryID: foreignTree[2]})
		require.ErrorIs(t, err, service.ErrCTICategoryNotFound)
		_, err = categories.ReferenceView(f.ctx, service.CTIReferenceQuery{TenantID: tenant, ActorRole: "super_admin", CategoryID: 999999})
		require.ErrorIs(t, err, service.ErrCTICategoryNotFound)
		_, err = categories.ReferenceView(f.ctx, service.CTIReferenceQuery{TenantID: 0, ActorRole: "super_admin", CategoryID: item.CategoryID})
		require.ErrorIs(t, err, service.ErrCTIPathOutsideTenant)

		// super_admin 可以查看明细（用于维护决策），且不会看到其它租户对象。
		view, err := categories.ReferenceView(f.ctx, service.CTIReferenceQuery{TenantID: tenant, ActorRole: "super_admin", CategoryID: primary[1], IncludeWorkItems: true})
		require.NoError(t, err)
		require.True(t, groups(view)["catalog"].Visible)
		for _, entry := range groups(view)["catalog"].Items {
			require.NotEqual(t, "外部目录", entry.Name)
		}
	})
}
