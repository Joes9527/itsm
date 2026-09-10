package change

import (
	"context"
	"database/sql"
	"itsm-backend/common"
	"itsm-backend/common/tenantctx"
	"itsm-backend/ent/change"
	"itsm-backend/ent/ticket"
	"itsm-backend/handlers/shared/workitemmutation"
	"itsm-backend/service"
	"time"
)

func (s *Service) DeleteChange(ctx context.Context, id int, m workitemmutation.Meta) error {
	if s.entClient == nil {
		return common.NewInternalError("deletion application is unavailable", nil)
	}
	if scoped, ok := tenantctx.TenantID(ctx); !ok {
		ctx = tenantctx.WithTenantID(ctx, m.TenantID)
	} else if scoped != m.TenantID {
		return common.NewForbiddenError("tenant context mismatch")
	}
	tx, err := s.entClient.BeginTx(ctx, &sql.TxOptions{Isolation: sql.LevelRepeatableRead})
	if err != nil {
		return err
	}
	defer tx.Rollback()
	c, err := tx.Change.Query().Where(change.ID(id), changeTenantScope(m.TenantID)).Only(ctx)
	if err != nil {
		return err
	}
	if err = service.NewWorkItemRelationService(s.entClient, s.directory).GuardDeletionTx(ctx, tx, m, c.WorkItemID); err != nil {
		return err
	}
	now := time.Now().UTC()
	if _, err = tx.Ticket.UpdateOneID(c.WorkItemID).Where(ticket.TenantID(m.TenantID), ticket.DeletedAtIsNil()).SetDeletedAt(now).SetUpdatedAt(now).AddVersion(1).Save(ctx); err != nil {
		return err
	}
	return tx.Commit()
}
