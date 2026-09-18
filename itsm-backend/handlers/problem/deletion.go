package problem

import (
	"context"
	"database/sql"
	"time"

	"itsm-backend/common"
	"itsm-backend/common/tenantctx"
	"itsm-backend/ent/problem"
	"itsm-backend/ent/ticket"
	"itsm-backend/handlers/shared/workitemmutation"
	"itsm-backend/service"
)

func (s *Service) Delete(ctx context.Context, id int, m workitemmutation.Meta) error {
	if s.client == nil {
		return common.NewInternalError("deletion application is unavailable", nil)
	}
	if scoped, ok := tenantctx.TenantID(ctx); !ok {
		ctx = tenantctx.WithTenantID(ctx, m.TenantID)
	} else if scoped != m.TenantID {
		return common.NewForbiddenError("tenant context mismatch")
	}
	tx, err := s.client.BeginTx(ctx, &sql.TxOptions{Isolation: sql.LevelRepeatableRead})
	if err != nil {
		return err
	}
	defer tx.Rollback()
	p, err := tx.Problem.Query().Where(problem.ID(id), problemTenantScope(m.TenantID)).Only(ctx)
	if err != nil {
		return err
	}
	if err := s.requireExecutionTx(ctx, tx, m.TenantID, p.WorkItemID); err != nil {
		return err
	}
	if err = service.NewWorkItemRelationService(s.client, s.directory).GuardDeletionTx(ctx, tx, m, p.WorkItemID); err != nil {
		return err
	}
	now := time.Now().UTC()
	if _, err = tx.Ticket.UpdateOneID(p.WorkItemID).Where(ticket.TenantID(m.TenantID), ticket.DeletedAtIsNil()).SetDeletedAt(now).SetUpdatedAt(now).AddVersion(1).Save(ctx); err != nil {
		return err
	}
	return tx.Commit()
}
