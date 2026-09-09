package change

import (
	"context"
	"fmt"
	"time"

	"itsm-backend/ent"
	entchange "itsm-backend/ent/change"
	"itsm-backend/ent/ticket"
	"itsm-backend/handlers/shared/workflowcallback"
)

func workflowApplied(message string, output map[string]interface{}) workflowcallback.Result {
	return workflowcallback.Result{Status: workflowcallback.StatusApplied, Message: message, Output: output}
}

func workflowIdempotent(message string, output map[string]interface{}) workflowcallback.Result {
	return workflowcallback.Result{Status: workflowcallback.StatusIdempotent, Message: message, Output: output}
}

func workflowBlocked(message string) workflowcallback.Result {
	return workflowcallback.Result{Status: workflowcallback.StatusBlocked, BlockCode: "handler_contract", Message: message}
}

// ApplyChangeWorkflowCallback is the sole application-service write boundary
// for synchronous Change BPMN actions.
func (s *Service) ApplyChangeWorkflowCallback(ctx context.Context, cmd workflowcallback.ChangeCommand) (workflowcallback.Result, error) {
	if cmd.ChangeID <= 0 || cmd.TenantID <= 0 {
		return workflowcallback.Result{}, fmt.Errorf("invalid tenant or change identity")
	}
	if cmd.Action == "update_change" {
		return s.applyWorkflowUpdate(ctx, cmd)
	}
	actions := map[string]string{
		"assess_risk":      "assess",
		"approve_change":   "authorize",
		"authorize_change": "authorize",
		"reject_change":    "authorize",
		"schedule_change":  "schedule",
		"implement_change": "implement",
		"verify_change":    "record_outcome",
		"review_change":    "review",
		"close_change":     "close",
		"cancel_change":    "cancel",
	}
	if cmd.Meta.TenantID != cmd.TenantID {
		return workflowcallback.Result{}, fmt.Errorf("callback tenant metadata mismatch")
	}
	action, ok := actions[cmd.Action]
	if !ok {
		return workflowBlocked("unsupported change callback action"), nil
	}
	result, err := s.ApplyCommand(ctx, Command{Meta: cmd.Meta, ChangeID: cmd.ChangeID, Action: action, Outcome: cmd.Outcome, Evidence: cmd.Evidence, ApprovalDecisionID: cmd.ApprovalDecisionID, PlannedStart: cmd.PlannedStart, PlannedEnd: cmd.PlannedEnd, ActualEnd: cmd.ActualEnd, PIRID: cmd.PIRID})
	if err != nil {
		return workflowcallback.Result{}, err
	}
	status := workflowcallback.StatusApplied
	if result.Replayed {
		status = workflowcallback.StatusIdempotent
	}
	return workflowcallback.Result{Status: status, Message: "Change command committed", LifecycleResult: &result}, nil
}

func (s *Service) loadWorkflowChange(ctx context.Context, id, tenantID int) (*ent.Change, error) {
	return s.entClient.Change.Query().Where(
		entchange.ID(id), entchange.HasWorkItemWith(ticket.TenantID(tenantID), ticket.DeletedAtIsNil()),
	).WithWorkItem().Only(ctx)
}

func (s *Service) applyWorkflowUpdate(ctx context.Context, cmd workflowcallback.ChangeCommand) (workflowcallback.Result, error) {
	current, err := s.loadWorkflowChange(ctx, cmd.ChangeID, cmd.TenantID)
	if err != nil {
		return workflowcallback.Result{}, err
	}
	update := s.entClient.Ticket.Update().Where(
		ticket.ID(current.WorkItemID), ticket.TenantID(cmd.TenantID), ticket.DeletedAtIsNil(), ticket.VersionEQ(current.Edges.WorkItem.Version),
	)
	changed := false
	if cmd.Title != nil && current.Edges.WorkItem.Title != *cmd.Title {
		update.SetTitle(*cmd.Title)
		changed = true
	}
	if cmd.Description != nil && current.Edges.WorkItem.Description != *cmd.Description {
		update.SetDescription(*cmd.Description)
		changed = true
	}
	if !changed {
		return workflowIdempotent(fmt.Sprintf("change %d already matches", current.ID), nil), nil
	}
	count, err := update.SetUpdatedAt(time.Now()).AddVersion(1).Save(ctx)
	if err != nil {
		return workflowcallback.Result{}, err
	}
	if count == 1 {
		return workflowApplied(fmt.Sprintf("change %d updated", current.ID), nil), nil
	}
	latest, err := s.loadWorkflowChange(ctx, current.ID, cmd.TenantID)
	if err != nil {
		return workflowcallback.Result{}, err
	}
	if (cmd.Title == nil || latest.Edges.WorkItem.Title == *cmd.Title) && (cmd.Description == nil || latest.Edges.WorkItem.Description == *cmd.Description) {
		return workflowIdempotent(fmt.Sprintf("change %d already matches", current.ID), nil), nil
	}
	return workflowBlocked(fmt.Sprintf("change %d has a conflicting concurrent update", current.ID)), nil
}
