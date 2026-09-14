package seeder

import (
	"context"
	"itsm-backend/ent"
	"itsm-backend/ent/menu"
)

// The BPMN task inbox has one page: /approvals. /approvals/pending is not a route.
func reconcileApprovalMenus(ctx context.Context, c *ent.Client, tenantID int) error {
	rows, err := c.Menu.Query().Where(menu.TenantIDEQ(tenantID), menu.PathIn("/approvals", "/approvals/pending")).Order(ent.Asc(menu.FieldID)).All(ctx)
	if err != nil {
		return err
	}
	if len(rows) == 0 {
		_, err = c.Menu.Create().SetTenantID(tenantID).SetName("我的待办").SetPath("/approvals").SetIcon("CheckSquare").SetPermissionCode("task:read").SetSortOrder(75).Save(ctx)
		return err
	}
	canonical := rows[0]
	for _, row := range rows {
		if row.Path == "/approvals" {
			canonical = row
			break
		}
	}
	if _, err = canonical.Update().SetName("我的待办").SetPath("/approvals").SetPermissionCode("task:read").SetIcon("CheckSquare").ClearParentID().Save(ctx); err != nil {
		return err
	}
	for _, row := range rows {
		if row.ID == canonical.ID {
			continue
		}
		if _, err = c.Menu.Update().Where(menu.TenantIDEQ(tenantID), menu.ParentIDEQ(row.ID)).SetParentID(canonical.ID).Save(ctx); err != nil {
			return err
		}
		if _, err = c.Menu.Delete().Where(menu.TenantIDEQ(tenantID), menu.IDEQ(row.ID)).Exec(ctx); err != nil {
			return err
		}
	}
	return nil
}
