package seeder

import (
	"github.com/stretchr/testify/require"
	"itsm-backend/ent/menu"
	"itsm-backend/pkg/tenantmode"
	"testing"
)

func TestApprovalMenuConvergesToExistingPage(t *testing.T) {
	s, ctx := newTestSeeder(t, tenantmode.DeploymentModePrivate)
	root := s.seedDefaultTenant(ctx)
	s.client.Menu.Create().SetTenantID(root.ID).SetName("我的待办").SetPath("/approvals/pending").SetPermissionCode("task:read").SetIsVisible(false).SetIsEnabled(false).SaveX(ctx)
	other := s.client.Menu.Create().SetTenantID(root.ID + 1).SetName("其他租户待办").SetPath("/approvals/pending").SaveX(ctx)
	for i := 0; i < 2; i++ {
		s.seedMenus(ctx)
		s.seedMenuAndPermissionFixes(ctx)
		rows := s.client.Menu.Query().Where(menu.TenantIDEQ(root.ID), menu.PathIn("/approvals", "/approvals/pending")).AllX(ctx)
		require.Len(t, rows, 1)
		require.Equal(t, "/approvals", rows[0].Path)
		require.Equal(t, "task:read", rows[0].PermissionCode)
		require.Nil(t, rows[0].ParentID)
		require.False(t, rows[0].IsVisible)
		require.False(t, rows[0].IsEnabled)
		require.Equal(t, "/approvals/pending", s.client.Menu.GetX(ctx, other.ID).Path)
	}
}

func TestApprovalMenuRepairRetainsLegacyIdentityAndAudits(t *testing.T) {
	s, ctx := newTestSeeder(t, tenantmode.DeploymentModePrivate)
	root := s.seedDefaultTenant(ctx)
	old := s.client.Menu.Create().SetTenantID(root.ID).SetName("我的待办").SetPath("/approvals/pending").SetIsVisible(false).SaveX(ctx)
	for i := 0; i < 2; i++ {
		require.NoError(t, s.ReconcileMenus(ctx, root.ID, "operator", "approvals"))
	}
	row := s.client.Menu.GetX(ctx, old.ID)
	require.Equal(t, "/approvals", row.Path)
	require.Equal(t, "task:read", row.PermissionCode)
	require.False(t, row.IsVisible)
	require.Equal(t, 1, s.client.Menu.Query().CountX(ctx))
	logs := s.client.AuditLog.Query().AllX(ctx)
	require.Len(t, logs, 2)
	require.Equal(t, "reconcile_approvals_menus", logs[0].Action)
}
