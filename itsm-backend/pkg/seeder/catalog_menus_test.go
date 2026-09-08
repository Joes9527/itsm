package seeder

import (
	"github.com/stretchr/testify/require"
	"itsm-backend/ent/menu"
	"itsm-backend/pkg/tenantmode"
	"testing"
)

func TestCatalogMenusSeparateBrowsingFromAdministration(t *testing.T) {
	s, ctx := newTestSeeder(t, tenantmode.DeploymentModePrivate)
	root := s.seedDefaultTenant(ctx)
	existing := s.client.Menu.Create().SetTenantID(root.ID).SetPath("/admin/ticket-categories").SetName("工单分类").SetPermissionCode("ticket:write").SetIsVisible(false).SaveX(ctx)
	other := s.client.Menu.Create().SetTenantID(root.ID + 1).SetPath("/admin/ticket-categories").SetName("其他租户分类").SetPermissionCode("ticket:write").SaveX(ctx)
	for i := 0; i < 2; i++ {
		s.seedMenus(ctx)
		s.seedMenuAndPermissionFixes(ctx)
		browse := s.client.Menu.Query().Where(menu.TenantIDEQ(root.ID), menu.PathEQ("/service-catalog")).OnlyX(ctx)
		require.Equal(t, "服务目录", browse.Name)
		admin, err := s.client.Menu.Query().Where(menu.TenantIDEQ(root.ID), menu.PathEQ("/admin/service-catalogs")).Only(ctx)
		require.NoError(t, err)
		require.Equal(t, "服务目录管理", admin.Name)
		require.Equal(t, "service_catalog:read", admin.PermissionCode)
		category := s.client.Menu.GetX(ctx, existing.ID)
		require.Equal(t, "ticket_category:read", category.PermissionCode)
		require.False(t, category.IsVisible)
		require.Equal(t, "ticket:write", s.client.Menu.GetX(ctx, other.ID).PermissionCode)
	}
}

func TestCatalogMenuRepairIsScopedAuditedAndRejectsUnknownScope(t *testing.T) {
	s, ctx := newTestSeeder(t, tenantmode.DeploymentModePrivate)
	root := s.seedDefaultTenant(ctx)
	untouched := s.client.Menu.Create().SetTenantID(root.ID).SetPath("/workflow").SetName("工作流").SaveX(ctx)
	other := s.client.Menu.Create().SetTenantID(root.ID + 1).SetPath("/admin/ticket-categories").SetName("其他租户").SetPermissionCode("ticket:write").SaveX(ctx)
	require.Error(t, s.ReconcileMenus(ctx, root.ID, "operator", "unknown"))
	require.Equal(t, 2, s.client.Menu.Query().CountX(ctx))
	require.Zero(t, s.client.AuditLog.Query().CountX(ctx))
	for i := 0; i < 2; i++ {
		require.NoError(t, s.ReconcileMenus(ctx, root.ID, "operator", "catalog"))
		require.Equal(t, 4, s.client.Menu.Query().CountX(ctx))
	}
	require.Equal(t, "工作流", s.client.Menu.GetX(ctx, untouched.ID).Name)
	require.Equal(t, "ticket:write", s.client.Menu.GetX(ctx, other.ID).PermissionCode)
	logs := s.client.AuditLog.Query().AllX(ctx)
	require.Len(t, logs, 2)
	require.Equal(t, "reconcile_catalog_menus", logs[0].Action)
	require.Equal(t, root.ID, logs[0].TenantID)
}
