package seeder

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"

	"itsm-backend/ent"
	"itsm-backend/ent/menu"
)

// ReconcileMenus repairs only the requested tenant's navigation for an explicit scope. It does
// not seed identities, change grants, migrate schemas, or restart the API.
func (s *Seeder) ReconcileMenus(ctx context.Context, tenantID int, requestedBy, scope string) error {
	if tenantID <= 0 || strings.TrimSpace(requestedBy) == "" {
		return fmt.Errorf("positive tenant ID and requester are required")
	}
	reconcile, ok := map[string]func(context.Context, *ent.Client, int) error{
		"workflow":  reconcileWorkflowMenus,
		"catalog":   reconcileCatalogMenus,
		"approvals": reconcileApprovalMenus,
	}[scope]
	if !ok {
		return fmt.Errorf("unsupported menu reconciliation scope %q", scope)
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
	if err := reconcile(ctx, c, tenantID); err != nil {
		return err
	}
	after, err := c.Menu.Query().Where(menu.TenantIDEQ(tenantID)).Order(ent.Asc(menu.FieldID)).All(ctx)
	if err != nil {
		return err
	}
	body, err := json.Marshal(map[string]any{"requestedBy": requestedBy, "source": "cli:reconcile_menus", "scope": scope, "before": before, "after": after})
	if err != nil {
		return err
	}
	if _, err := c.AuditLog.Create().SetTenantID(tenantID).SetResource("menu").SetAction("reconcile_" + scope + "_menus").SetPath("/" + scope).SetMethod("CLI").SetStatusCode(200).SetRequestBody(string(body)).Save(ctx); err != nil {
		return err
	}
	return tx.Commit()
}
