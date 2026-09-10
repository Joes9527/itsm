package service_request

import (
	"context"
	"database/sql"
	"time"

	"itsm-backend/common"
	"itsm-backend/common/tenantctx"
	"itsm-backend/database"
	"itsm-backend/ent"
	"itsm-backend/ent/servicerequest"
	"itsm-backend/ent/ticket"
	creation "itsm-backend/handlers/common/workitemcreation"
	"itsm-backend/handlers/shared/workitemmutation"
	"itsm-backend/service"
)

func (s *Service) SetDirectorySnapshot(directory database.DirectorySnapshot) { s.directory = directory }

// Delete owns the Requested Item transaction. Shared reference safety and current
// class permissions are mandatory; requester/manage is an additional domain rule.
func (s *Service) Delete(ctx context.Context, id int, m workitemmutation.Meta) error {
	if s.client == nil {
		return common.NewInternalError("deletion application is unavailable", nil)
	}
	if scoped, ok := tenantctx.TenantID(ctx); ok && scoped != m.TenantID {
		return common.NewForbiddenError("tenant context mismatch")
	}
	ctx = tenantctx.WithTenantID(ctx, m.TenantID)
	tx, err := s.client.BeginTx(ctx, &sql.TxOptions{Isolation: sql.LevelRepeatableRead})
	if err != nil {
		return err
	}
	defer tx.Rollback()
	req, err := tx.ServiceRequest.Query().Where(servicerequest.ID(id), requestScope(m.TenantID)).Only(ctx)
	if err != nil {
		if ent.IsNotFound(err) {
			return common.NewNotFoundError("Service Request not found")
		}
		return err
	}
	guard := service.NewWorkItemRelationService(s.client, s.directory)
	err = guard.GuardDeletionTx(ctx, tx, m, req.TicketID)
	if err != nil {
		return err
	}
	now := time.Now().UTC()
	if err = tx.Ticket.UpdateOneID(req.TicketID).Where(ticket.TenantID(m.TenantID), ticket.RecordClassEQ(creation.RecordClassServiceRequestItem), ticket.DeletedAtIsNil()).SetDeletedAt(now).SetUpdatedAt(now).AddVersion(1).Exec(ctx); err != nil {
		return err
	}
	return tx.Commit()
}
