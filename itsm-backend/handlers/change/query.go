package change

import (
	"context"
	"database/sql"

	"itsm-backend/authorization"
	"itsm-backend/common"
	"itsm-backend/common/tenantctx"
	"itsm-backend/ent"
	creation "itsm-backend/handlers/common/workitemcreation"
	"itsm-backend/handlers/shared/workitemmutation"
	"itsm-backend/service"
)

func (s *Service) GetChange(ctx context.Context, id int, m workitemmutation.Meta) (*Change, error) {
	if m.ActorID <= 0 || m.TenantID <= 0 {
		return nil, common.NewUnauthorizedError("authenticated actor and tenant required")
	}
	if tenant, ok := tenantctx.TenantID(ctx); ok && tenant != m.TenantID {
		return nil, common.NewForbiddenError("tenant mismatch")
	}
	ctx = tenantctx.WithTenantID(ctx, m.TenantID)
	tx, err := s.entClient.BeginTx(ctx, &sql.TxOptions{Isolation: sql.LevelRepeatableRead, ReadOnly: true})
	if err != nil {
		return nil, err
	}
	defer tx.Rollback()
	c, err := NewEntRepository(tx.Client(), nil).Get(ctx, id, m.TenantID)
	if err != nil {
		return nil, err
	}
	if err = s.projectRelationsTx(ctx, tx, m, c); err != nil {
		return nil, err
	}
	return c, nil
}

func (s *Service) projectRelationsTx(ctx context.Context, tx *ent.Tx, m workitemmutation.Meta, c *Change) error {
	if c == nil || c.WorkItemID == nil {
		return common.NewInternalError("Change WorkItem is missing", nil)
	}
	views, err := service.NewWorkItemRelationService(s.entClient, s.directory).ListTx(ctx, tx, m, *c.WorkItemID)
	if err != nil {
		return err
	}
	c.Relations = views
	return nil
}

func (s *Service) ListChanges(ctx context.Context, m workitemmutation.Meta, page, size int, status, search, riskLevel string) ([]*Change, int, error) {
	if m.ActorID <= 0 || m.TenantID <= 0 {
		return nil, 0, common.NewUnauthorizedError("authenticated actor and tenant required")
	}
	if tenant, ok := tenantctx.TenantID(ctx); ok && tenant != m.TenantID {
		return nil, 0, common.NewForbiddenError("tenant mismatch")
	}
	ctx = tenantctx.WithTenantID(ctx, m.TenantID)
	tx, err := s.entClient.BeginTx(ctx, &sql.TxOptions{Isolation: sql.LevelRepeatableRead, ReadOnly: true})
	if err != nil {
		return nil, 0, err
	}
	defer tx.Rollback()
	actor, err := authorization.ResolveLifecycleActor(ctx, tx, s.directory, m.ActorID, m.TenantID)
	if err != nil {
		return nil, 0, err
	}
	role := authorization.EffectiveSessionRole(actor)
	identity := creation.Identity{ActorID: actor.ID, ActorTenantID: actor.TenantID, TenantID: m.TenantID, Role: role}
	if err = authorization.RequireCurrentPermission(ctx, tx, identity, "change", "read"); err != nil {
		return nil, 0, err
	}
	rows, total, err := NewEntRepository(tx.Client(), nil).List(ctx, m.TenantID, page, size, status, search, riskLevel, authorization.WorkItemReadScope(actor.ID, role))
	if err != nil {
		return nil, 0, err
	}
	for _, c := range rows {
		if err = s.projectRelationsTx(ctx, tx, m, c); err != nil {
			return nil, 0, err
		}
	}
	return rows, total, nil
}
