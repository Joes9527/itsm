package service

import (
	"context"
	"database/sql"
	"itsm-backend/common"
	"itsm-backend/common/tenantctx"
	"itsm-backend/database"
	"itsm-backend/ent"
	"itsm-backend/ent/incident"
	"itsm-backend/ent/ticket"
	"itsm-backend/handlers/shared/workitemmutation"
	"time"
)

func (s *TicketService) SetDirectorySnapshot(directory database.DirectorySnapshot) {
	s.directory = directory
}

func (s *TicketService) DeleteTicket(ctx context.Context, id int, m workitemmutation.Meta) error {
	return s.BatchDeleteTickets(ctx, []int{id}, m)
}

func (s *TicketService) BatchDeleteTickets(ctx context.Context, ids []int, m workitemmutation.Meta) error {
	if s.client == nil {
		return common.NewInternalError("deletion application unavailable", nil)
	}
	ctx, err := deletionContext(ctx, m)
	if err != nil {
		return err
	}
	tx, err := s.client.BeginTx(ctx, &sql.TxOptions{Isolation: sql.LevelRepeatableRead})
	if err != nil {
		return err
	}
	defer tx.Rollback()
	if err = NewWorkItemRelationService(s.client, s.directory).GuardDeletionsTx(ctx, tx, m, ids, func(item *ent.Ticket) error {
		return requireTicketDeletionPrecondition(ctx, tx.Client(), item.ID, m.TenantID, item.Status)
	}); err != nil {
		return err
	}
	if err = softDeleteWorkItemsTx(ctx, tx, m.TenantID, ids); err != nil {
		return err
	}
	return tx.Commit()
}

// DeleteSubtask verifies the actual parent relationship and both read scopes in
// the same RR snapshot and ordered lock set used by the deletion guard.
func (s *TicketService) DeleteSubtask(ctx context.Context, parentID, childID int, m workitemmutation.Meta) error {
	if s.client == nil {
		return common.NewInternalError("deletion application unavailable", nil)
	}
	ctx, err := deletionContext(ctx, m)
	if err != nil {
		return err
	}
	tx, err := s.client.BeginTx(ctx, &sql.TxOptions{Isolation: sql.LevelRepeatableRead})
	if err != nil {
		return err
	}
	defer tx.Rollback()
	if err = NewWorkItemRelationService(s.client, s.directory).GuardSubtaskDeletionTx(ctx, tx, m, parentID, childID, func(item *ent.Ticket) error {
		return requireTicketDeletionPrecondition(ctx, tx.Client(), item.ID, m.TenantID, item.Status)
	}); err != nil {
		return err
	}
	if err = softDeleteWorkItemsTx(ctx, tx, m.TenantID, []int{childID}); err != nil {
		return err
	}
	return tx.Commit()
}

func (s *IncidentService) DeleteIncident(ctx context.Context, id int, m workitemmutation.Meta) error {
	ctx, err := deletionContext(ctx, m)
	if err != nil {
		return err
	}
	tx, err := s.client.BeginTx(ctx, &sql.TxOptions{Isolation: sql.LevelRepeatableRead})
	if err != nil {
		return err
	}
	defer tx.Rollback()
	entity, err := tx.Incident.Query().Where(incident.ID(id), incidentTenantScope(m.TenantID)).Only(ctx)
	if err != nil {
		return err
	}
	if err = NewWorkItemRelationService(s.client, s.directory).GuardDeletionTx(ctx, tx, m, entity.WorkItemID); err != nil {
		return err
	}
	if err = softDeleteWorkItemsTx(ctx, tx, m.TenantID, []int{entity.WorkItemID}); err != nil {
		return err
	}
	return tx.Commit()
}

func deletionContext(ctx context.Context, m workitemmutation.Meta) (context.Context, error) {
	if scoped, ok := tenantctx.TenantID(ctx); ok && scoped != m.TenantID {
		return nil, common.NewForbiddenError("tenant context mismatch")
	}
	return tenantctx.WithTenantID(ctx, m.TenantID), nil
}

func softDeleteWorkItemsTx(ctx context.Context, tx *ent.Tx, tenantID int, ids []int) error {
	now := time.Now().UTC()
	count, err := tx.Ticket.Update().Where(ticket.IDIn(ids...), ticket.TenantID(tenantID), ticket.DeletedAtIsNil()).SetDeletedAt(now).SetUpdatedAt(now).AddVersion(1).Save(ctx)
	if err != nil {
		return err
	}
	if count != len(ids) {
		return common.NewConflictError("work item deletion", "deletion target changed")
	}
	return nil
}
