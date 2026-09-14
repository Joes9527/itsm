package seeder

import (
	"context"

	"itsm-backend/ent"
	"itsm-backend/ent/menu"
)

// Catalog administration and operational ticket classification are distinct
// configuration surfaces. Their menu permissions match their read endpoints.
func reconcileCatalogMenus(ctx context.Context, c *ent.Client, tenantID int) error {
	definitions := []struct {
		name, path, icon, permission, description string
		order                                     int
	}{
		{"工单分类", "/admin/ticket-categories", "Tag", "ticket_category:read", "维护工单的业务分类树，用于分派、服务级别、统计和自动化规则", 275},
		{"服务目录管理", "/admin/service-catalogs", "Book", "service_catalog:read", "维护可申请的服务目录项、申请表单、流程和服务级别", 276},
	}
	for _, d := range definitions {
		rows, err := c.Menu.Query().Where(menu.TenantIDEQ(tenantID), menu.PathEQ(d.path)).All(ctx)
		if err != nil {
			return err
		}
		if len(rows) == 0 {
			if _, err = c.Menu.Create().SetTenantID(tenantID).SetName(d.name).SetPath(d.path).SetIcon(d.icon).SetPermissionCode(d.permission).SetDescription(d.description).SetSortOrder(d.order).Save(ctx); err != nil {
				return err
			}
		} else {
			// Keep operator-controlled visibility, enabled state, hierarchy and order.
			if _, err = c.Menu.Update().Where(menu.TenantIDEQ(tenantID), menu.PathEQ(d.path)).SetName(d.name).SetPermissionCode(d.permission).SetDescription(d.description).Save(ctx); err != nil {
				return err
			}
		}
	}
	return nil
}
