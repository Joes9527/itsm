package seeder

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"

	"itsm-backend/ent"
	"itsm-backend/ent/menu"
)

// ReconcileWorkflowMenus repairs only the requested tenant's navigation. It does
// not seed identities, change grants, migrate schemas, or restart the API.
func (s *Seeder) ReconcileWorkflowMenus(ctx context.Context, tenantID int, requestedBy string) error {
	if tenantID <= 0 || strings.TrimSpace(requestedBy) == "" {
		return fmt.Errorf("positive tenant ID and requester are required")
	}
	tx, err := s.client.Tx(ctx)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	c := tx.Client()
	if _, err := c.Tenant.Get(ctx, tenantID); err != nil {
		return err
	}
	before, err := c.Menu.Query().Where(menu.TenantIDEQ(tenantID)).Order(ent.Asc(menu.FieldID)).All(ctx)
	if err != nil {
		return err
	}
	if err := reconcileWorkflowMenus(ctx, c, tenantID); err != nil {
		return err
	}
	after, err := c.Menu.Query().Where(menu.TenantIDEQ(tenantID)).Order(ent.Asc(menu.FieldID)).All(ctx)
	if err != nil {
		return err
	}
	body, err := json.Marshal(map[string]any{"requestedBy": requestedBy, "source": "cli:reconcile_workflow_menus", "before": before, "after": after})
	if err != nil {
		return err
	}
	if _, err := c.AuditLog.Create().SetTenantID(tenantID).SetResource("menu").SetAction("reconcile_workflow_menus").SetPath("/workflow").SetMethod("CLI").SetStatusCode(200).SetRequestBody(string(body)).Save(ctx); err != nil {
		return err
	}
	return tx.Commit()
}

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
		name, path, icon, permission string
		order                        int
	}{
		{"工作流管理", "/admin/workflows", "Workflow", "workflow:read", 121},
		{"流程设计器", "/workflow/designer", "Edit", "workflow:write", 122},
		{"流程实例", "/workflow/instances", "Play", "workflow:read", 123},
	}
	for _, d := range definitions {
		rows, err := c.Menu.Query().Where(menu.TenantIDEQ(tenantID), menu.PathEQ(d.path)).Order(ent.Asc(menu.FieldID)).All(ctx)
		if err != nil {
			return err
		}
		if len(rows) == 0 {
			if _, err = c.Menu.Create().SetTenantID(tenantID).SetName(d.name).SetPath(d.path).SetIcon(d.icon).SetPermissionCode(d.permission).SetSortOrder(d.order).SetParentID(group.ID).Save(ctx); err != nil {
				return err
			}
			continue
		}
		canonical := rows[0]
		if _, err = canonical.Update().SetName(d.name).SetIcon(d.icon).SetPermissionCode(d.permission).SetSortOrder(d.order).SetParentID(group.ID).Save(ctx); err != nil {
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
