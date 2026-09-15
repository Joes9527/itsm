package service

import (
	"context"
	"fmt"

	"itsm-backend/common"
	"itsm-backend/ent/ticket"
	"itsm-backend/handlers/shared/workflowcallback"
)

func (s *TicketService) AssignTicketForWorkflow(ctx context.Context, id, target, tenantID int) (workflowcallback.Result, error) {
	if s.workflowAssignment == nil {
		return workflowcallback.Result{}, fmt.Errorf("verified callback assignment boundary is required")
	}
	tx, err := s.client.Tx(ctx)
	if err != nil {
		return workflowcallback.Result{}, err
	}
	defer tx.Rollback()
	item, err := tx.Ticket.Query().Where(ticket.ID(id), ticket.TenantID(tenantID), ticket.DeletedAtIsNil(), ticket.RecordClassEQ("generic")).Only(ctx)
	if err != nil {
		return workflowcallback.Result{}, err
	}
	if item.Status == "resolved" || item.Status == "closed" || item.Status == "cancelled" {
		return workflowcallback.Result{Status: workflowcallback.StatusBlocked, BlockCode: "handler_contract", Message: "terminal WorkItem cannot be assigned"}, nil
	}
	if s.execution == nil {
		return workflowcallback.Result{}, fmt.Errorf("execution policy required")
	}
	if err := s.execution.BindEnt(ctx, tx, tenantID); err != nil {
		return workflowcallback.Result{}, err
	}
	if err := s.execution.RequireEntMembers(ctx, tx, tenantID, item.ID); err != nil {
		return workflowcallback.Result{}, err
	}
	writer, cmd, err := s.workflowAssignment(ctx, tx, tenantID)
	if err != nil {
		return workflowcallback.Result{}, err
	}
	cmd.WorkItemID = id
	cmd.AssigneeID = target
	cmd.ExpectedVersion = item.Version
	assigned, err := writer.Apply(ctx, tx.Client(), cmd)
	if err != nil {
		return workflowcallback.Result{}, err
	}
	changed := assigned.Version != item.Version
	if item.Status != common.TicketStatusAssigned {
		version := assigned.Version
		if !changed {
			version++
		}
		if err := tx.Ticket.UpdateOneID(id).Where(ticket.VersionEQ(assigned.Version)).SetStatus(common.TicketStatusAssigned).SetVersion(version).Exec(ctx); err != nil {
			return workflowcallback.Result{}, err
		}
		changed = true
	}
	if err := tx.Commit(); err != nil {
		return workflowcallback.Result{}, err
	}
	status := workflowcallback.StatusApplied
	if !changed {
		status = workflowcallback.StatusIdempotent
	}
	return workflowcallback.Result{Status: status, Message: "WorkItem assigned"}, nil
}

func (s *TicketService) SetWorkflowAssignmentBoundary(boundary workflowcallback.AssignmentBoundary) {
	s.workflowAssignment = boundary
}
