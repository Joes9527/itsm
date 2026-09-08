package seeder

import (
	"encoding/json"
	"github.com/stretchr/testify/require"
	"itsm-backend/ent/menu"
	"itsm-backend/pkg/tenantmode"
	"testing"
)

func TestWorkflowMenuRepairCreatesAuditedTenantBaseline(t *testing.T) {
	s, ctx := newTestSeeder(t, tenantmode.DeploymentModePrivate)
	root := s.seedDefaultTenant(ctx)
	require.Error(t, s.ReconcileWorkflowMenus(ctx, 0, "operator"))
	require.Error(t, s.ReconcileWorkflowMenus(ctx, root.ID, ""))
	require.Error(t, s.ReconcileWorkflowMenus(ctx, root.ID+999, "operator"))
	require.Zero(t, s.client.Menu.Query().CountX(ctx))
	for i := 0; i < 2; i++ {
		require.NoError(t, s.ReconcileWorkflowMenus(ctx, root.ID, "test-operator"))
		require.Equal(t, 4, s.client.Menu.Query().CountX(ctx))
	}
	logs := s.client.AuditLog.Query().AllX(ctx)
	require.Len(t, logs, 2)
	require.Equal(t, root.ID, logs[0].TenantID)
	var body map[string]any
	require.NoError(t, json.Unmarshal([]byte(*logs[0].RequestBody), &body))
	require.Equal(t, "test-operator", body["requestedBy"])
	require.Len(t, body["before"], 0)
	require.Len(t, body["after"], 4)
}

func TestWorkflowMenuHierarchyConvergesWithoutCrossTenantChanges(t *testing.T) {
	s, ctx := newTestSeeder(t, tenantmode.DeploymentModePrivate)
	root := s.seedDefaultTenant(ctx)
	legacy := s.client.Menu.Create().SetName("工作流").SetPath("/workflow").SetTenantID(root.ID).SaveX(ctx)
	s.client.Menu.Create().SetName("工作流列表").SetPath("/workflow").SetParentID(legacy.ID).SetTenantID(root.ID).SaveX(ctx)
	custom := s.client.Menu.Create().SetName("自定义视图").SetPath("/custom-workflow-report").SetParentID(legacy.ID).SetTenantID(root.ID).SaveX(ctx)
	other := s.client.Menu.Create().SetName("其他租户流程").SetPath("/workflow").SetTenantID(root.ID + 1).SaveX(ctx)
	for i := 0; i < 2; i++ {
		s.seedMenus(ctx)
		s.seedMenuAndPermissionFixes(ctx)
		group, err := s.client.Menu.Query().Where(menu.TenantIDEQ(root.ID), menu.PathEQ("/workflow"), menu.ParentIDIsNil()).Only(ctx)
		require.NoError(t, err)
		for path, permission := range map[string]string{"/admin/workflows": "workflow:read", "/workflow/designer": "workflow:write", "/workflow/instances": "workflow:read"} {
			child, err := s.client.Menu.Query().Where(menu.TenantIDEQ(root.ID), menu.PathEQ(path)).Only(ctx)
			require.NoError(t, err)
			require.Equal(t, &group.ID, child.ParentID)
			require.Equal(t, permission, child.PermissionCode)
		}
		require.Equal(t, &group.ID, s.client.Menu.GetX(ctx, custom.ID).ParentID)
		unchanged := s.client.Menu.GetX(ctx, other.ID)
		require.Equal(t, other.Name, unchanged.Name)
		require.Equal(t, other.Path, unchanged.Path)
		require.Equal(t, other.ParentID, unchanged.ParentID)
		require.Equal(t, other.PermissionCode, unchanged.PermissionCode)
		require.Equal(t, 1, s.client.Menu.Query().Where(menu.TenantIDEQ(root.ID), menu.PathEQ("/workflow")).CountX(ctx))
	}
}
