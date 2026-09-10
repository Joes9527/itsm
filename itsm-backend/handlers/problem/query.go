package problem

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

// Get projects base identity and both relation endpoints in one current actor RR snapshot.
func (s *Service) Get(ctx context.Context, id int, m workitemmutation.Meta) (*Problem, error) {
	if m.ActorID <= 0 || m.TenantID <= 0 {
		return nil, common.NewUnauthorizedError("authenticated actor and tenant required")
	}
	if s.client == nil {
		return nil, common.NewInternalError("Problem query application unavailable", nil)
	}
	if tenant, ok := tenantctx.TenantID(ctx); ok && tenant != m.TenantID {
		return nil, common.NewForbiddenError("tenant mismatch")
	}
	ctx = tenantctx.WithTenantID(ctx, m.TenantID)
	tx, err := s.client.BeginTx(ctx, &sql.TxOptions{Isolation: sql.LevelRepeatableRead, ReadOnly: true})
	if err != nil {
		return nil, err
	}
	defer tx.Rollback()
	item, err := NewEntRepository(tx.Client()).Get(ctx, id, m.TenantID)
	if err != nil {
		return nil, err
	}
	if err = s.projectRelationsTx(ctx, tx, m, item); err != nil {
		return nil, err
	}
	return item, nil
}
func (s *Service) projectRelationsTx(ctx context.Context, tx *ent.Tx, m workitemmutation.Meta, p *Problem) error {
	if p == nil || p.WorkItemID == nil {
		return common.NewInternalError("Problem WorkItem is missing", nil)
	}
	views, err := service.NewWorkItemRelationService(s.client, s.directory).ListTx(ctx, tx, m, *p.WorkItemID)
	if err != nil {
		return err
	}
	p.Relations = views
	return nil
}
func (s *Service) List(ctx context.Context, m workitemmutation.Meta, page, size int, filters map[string]interface{}) ([]*Problem, int, error) {
	if m.ActorID <= 0 || m.TenantID <= 0 {
		return nil, 0, common.NewUnauthorizedError("authenticated actor and tenant required")
	}
	if s.client == nil {
		return nil, 0, common.NewInternalError("Problem query application unavailable", nil)
	}
	if tenant, ok := tenantctx.TenantID(ctx); ok && tenant != m.TenantID {
		return nil, 0, common.NewForbiddenError("tenant mismatch")
	}
	ctx = tenantctx.WithTenantID(ctx, m.TenantID)
	tx, err := s.client.BeginTx(ctx, &sql.TxOptions{Isolation: sql.LevelRepeatableRead, ReadOnly: true})
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
	if err = authorization.RequireCurrentPermission(ctx, tx, identity, "problem", "read"); err != nil {
		return nil, 0, err
	}
	rows, total, err := NewEntRepository(tx.Client()).List(ctx, m.TenantID, page, size, filters, authorization.WorkItemReadScope(actor.ID, role))
	if err != nil {
		return nil, 0, err
	}
	for _, p := range rows {
		if err = s.projectRelationsTx(ctx, tx, m, p); err != nil {
			return nil, 0, err
		}
	}
	return rows, total, nil
}
