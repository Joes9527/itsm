package seeder

import (
	"context"
	"fmt"

	"itsm-backend/ent"
	"itsm-backend/ent/menu"
)

func reconcileWorkflowMenus(ctx context.Context, c *ent.Client, tenantID int) error {
	// Prefer the existing group so custom children retain their parent ID.
	groups, err := c.Menu.Query().Where(menu.TenantIDEQ(tenantID), menu.PathEQ("/workflow"), menu.ParentIDIsNil()).Order(ent.Asc(menu.FieldID)).All(ctx)
	if err != nil {
		return err
	}
	var group *ent.Menu
	if len(groups) > 0 {
		group = groups[0]
	} else {
		group, err = c.Menu.Create().SetTenantID(tenantID).SetName("工作流").SetPath("/workflow").SetIcon("GitMerge").SetPermissionCode("workflow:read").SetSortOrder(120).Save(ctx)
		if err != nil {
			return err
		}
	}
	// Old scripts created both a /workflow group and a /workflow list child.
	// Move descendants before removing duplicate routes; never touch other tenants.
	duplicates, err := c.Menu.Query().Where(menu.TenantIDEQ(tenantID), menu.IDNEQ(group.ID), menu.PathIn("/workflow", "/workflow/list")).All(ctx)
	if err != nil {
		return err
	}
	for _, old := range duplicates {
		if _, err = c.Menu.Update().Where(menu.TenantIDEQ(tenantID), menu.ParentIDEQ(old.ID)).SetParentID(group.ID).Save(ctx); err != nil {
			return err
		}
		if _, err = c.Menu.Delete().Where(menu.TenantIDEQ(tenantID), menu.IDEQ(old.ID)).Exec(ctx); err != nil {
			return err
		}
	}
	definitions := []struct {
		name, path, icon, permission, legacyPath string
		order                                    int
	}{
		{"工作流管理", "/admin/workflows", "Workflow", "workflow:read", "", 121},
		{"流程设计器", "/workflow/designer", "Edit", "workflow:write", "", 122},
		{"流程实例", "/workflow/instances", "Play", "workflow:read", "", 123},
		{"审批链规则", "/admin/approval-chains", "CheckSquare", "approval:read", "/workflow/approval-chains", 124},
	}
	for _, d := range definitions {
		paths := []string{d.path}
		if d.legacyPath != "" {
			paths = append(paths, d.legacyPath)
		}
		rows, err := c.Menu.Query().Where(menu.TenantIDEQ(tenantID), menu.PathIn(paths...)).Order(ent.Asc(menu.FieldID)).All(ctx)
		if err != nil {
			return err
		}
		if len(rows) == 0 {
			create := c.Menu.Create().SetTenantID(tenantID).SetName(d.name).SetPath(d.path).SetIcon(d.icon).SetPermissionCode(d.permission).SetSortOrder(d.order)
			if d.legacyPath == "" {
				create.SetParentID(group.ID)
			}
			if _, err = create.Save(ctx); err != nil {
				return err
			}
			continue
		}
		canonical := rows[0]
		if d.legacyPath != "" && canonical.ParentID != nil {
			for _, row := range rows {
				if *canonical.ParentID == row.ID {
					return fmt.Errorf("approval menu %d has a conflicting parent %d; repair hierarchy before reconciliation", canonical.ID, row.ID)
				}
			}
		}
		update := canonical.Update().SetName(d.name).SetPath(d.path).SetIcon(d.icon).SetSortOrder(d.order)
		// Route repair preserves existing approval navigation and permission policy.
		if d.legacyPath == "" {
			update.SetPermissionCode(d.permission).SetParentID(group.ID)
		}
		if _, err = update.Save(ctx); err != nil {
			return err
		}
		for _, duplicate := range rows[1:] {
			if _, err = c.Menu.Update().Where(menu.TenantIDEQ(tenantID), menu.ParentIDEQ(duplicate.ID)).SetParentID(canonical.ID).Save(ctx); err != nil {
				return err
			}
			if _, err = c.Menu.Delete().Where(menu.TenantIDEQ(tenantID), menu.IDEQ(duplicate.ID)).Exec(ctx); err != nil {
				return err
			}
		}
	}
	// Existing optional workflow views remain available under the same group.
	_, err = c.Menu.Update().Where(menu.TenantIDEQ(tenantID), menu.PathIn("/workflow/versions", "/workflow/dashboard", "/workflow/audit", "/workflow/sla", "/workflow/bottlenecks", "/workflow/ticket-approval")).SetParentID(group.ID).Save(ctx)
	return err
}
