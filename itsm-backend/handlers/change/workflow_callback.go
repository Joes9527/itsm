package change

import (
	"context"
	"fmt"
	"time"

	"itsm-backend/common"
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
	switch cmd.Action {
	case "update_change":
		return s.applyWorkflowUpdate(ctx, cmd)
	case "reject_change":
		return s.applyWorkflowTransition(ctx, cmd, "rejected")
	case "schedule_change":
		return s.applyWorkflowSchedule(ctx, cmd)
	case "implement_change":
		return s.applyWorkflowStateWithTimestamp(ctx, cmd, "in_progress", true)
	case "verify_change":
		target := "completed"
		if cmd.VerificationResult == "failed" {
			target = "failed"
		}
		return s.applyWorkflowTransition(ctx, cmd, target)
	case "close_change":
		return s.applyWorkflowStateWithTimestamp(ctx, cmd, "completed", false)
	case "assess_risk":
		return s.applyWorkflowRisk(ctx, cmd)
	default:
		return workflowBlocked("unsupported change callback action"), nil
	}
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

func (s *Service) applyWorkflowTransition(ctx context.Context, cmd workflowcallback.ChangeCommand, target string) (workflowcallback.Result, error) {
	current, err := s.loadWorkflowChange(ctx, cmd.ChangeID, cmd.TenantID)
	if err != nil {
		return workflowcallback.Result{}, err
	}
	status := current.Edges.WorkItem.Status
	if status == target {
		return workflowIdempotent(fmt.Sprintf("change %d already %s", current.ID, target), nil), nil
	}
	if !common.IsValidChangeStatusTransition(status, target, current.Type) {
		return workflowBlocked(fmt.Sprintf("illegal change transition %s -> %s", status, target)), nil
	}
	count, err := s.entClient.Ticket.Update().Where(
		ticket.ID(current.WorkItemID), ticket.TenantID(cmd.TenantID), ticket.DeletedAtIsNil(),
		ticket.VersionEQ(current.Edges.WorkItem.Version), ticket.StatusEQ(status),
	).SetStatus(target).SetUpdatedAt(time.Now()).AddVersion(1).Save(ctx)
	if err != nil {
		return workflowcallback.Result{}, err
	}
	if count == 1 {
		return workflowApplied(fmt.Sprintf("change %d transitioned to %s", current.ID, target), nil), nil
	}
	latest, err := s.loadWorkflowChange(ctx, current.ID, cmd.TenantID)
	if err != nil {
		return workflowcallback.Result{}, err
	}
	if latest.Edges.WorkItem.Status == target {
		return workflowIdempotent(fmt.Sprintf("change %d already %s", current.ID, target), nil), nil
	}
	return workflowBlocked(fmt.Sprintf("change %d has a conflicting concurrent transition", current.ID)), nil
}

func (s *Service) applyWorkflowSchedule(ctx context.Context, cmd workflowcallback.ChangeCommand) (workflowcallback.Result, error) {
	tx, err := s.entClient.Tx(ctx)
	if err != nil {
		return workflowcallback.Result{}, err
	}
	rollback := func(cause error) (workflowcallback.Result, error) {
		if rbErr := tx.Rollback(); rbErr != nil {
			return workflowcallback.Result{}, fmt.Errorf("%w (rollback failed: %v)", cause, rbErr)
		}
		return workflowcallback.Result{}, cause
	}
	current, err := tx.Change.Query().Where(entchange.ID(cmd.ChangeID), entchange.HasWorkItemWith(ticket.TenantID(cmd.TenantID), ticket.DeletedAtIsNil())).WithWorkItem().Only(ctx)
	if err != nil {
		return rollback(err)
	}
	status := current.Edges.WorkItem.Status
	target := status
	if status != "approved" && status != "scheduled" {
		if !common.IsValidChangeStatusTransition(status, "approved", current.Type) {
			_ = tx.Rollback()
			return workflowBlocked(fmt.Sprintf("illegal change transition %s -> approved", status)), nil
		}
		target = "approved"
	}
	if target == "approved" && common.IsValidChangeStatusTransition("approved", "scheduled", current.Type) {
		target = "scheduled"
	}
	dateWrite := (cmd.PlannedStart != nil && !current.PlannedStartDate.Equal(*cmd.PlannedStart)) || (cmd.PlannedEnd != nil && !current.PlannedEndDate.Equal(*cmd.PlannedEnd))
	if status == target && !dateWrite {
		_ = tx.Rollback()
		return workflowIdempotent(fmt.Sprintf("change %d already scheduled", current.ID), nil), nil
	}
	count, err := tx.Ticket.Update().Where(
		ticket.ID(current.WorkItemID), ticket.TenantID(cmd.TenantID), ticket.DeletedAtIsNil(),
		ticket.VersionEQ(current.Edges.WorkItem.Version), ticket.StatusEQ(status),
	).SetStatus(target).SetUpdatedAt(time.Now()).AddVersion(1).Save(ctx)
	if err != nil {
		return rollback(err)
	}
	if count != 1 {
		_ = tx.Rollback()
		return s.classifyWorkflowSchedule(ctx, cmd, target)
	}
	if dateWrite {
		update := tx.Change.Update().Where(entchange.ID(current.ID), entchange.HasWorkItemWith(ticket.TenantID(cmd.TenantID), ticket.DeletedAtIsNil()))
		if cmd.PlannedStart != nil {
			update.SetPlannedStartDate(*cmd.PlannedStart)
		}
		if cmd.PlannedEnd != nil {
			update.SetPlannedEndDate(*cmd.PlannedEnd)
		}
		updated, updateErr := update.Save(ctx)
		if updateErr != nil {
			return rollback(updateErr)
		}
		if updated != 1 {
			return rollback(fmt.Errorf("change schedule extension was not updated"))
		}
	}
	if err := tx.Commit(); err != nil {
		return workflowcallback.Result{}, err
	}
	return workflowApplied(fmt.Sprintf("change %d scheduled", current.ID), nil), nil
}

func (s *Service) classifyWorkflowSchedule(ctx context.Context, cmd workflowcallback.ChangeCommand, target string) (workflowcallback.Result, error) {
	latest, err := s.loadWorkflowChange(ctx, cmd.ChangeID, cmd.TenantID)
	if err != nil {
		return workflowcallback.Result{}, err
	}
	exact := latest.Edges.WorkItem.Status == target && (cmd.PlannedStart == nil || latest.PlannedStartDate.Equal(*cmd.PlannedStart)) && (cmd.PlannedEnd == nil || latest.PlannedEndDate.Equal(*cmd.PlannedEnd))
	if exact {
		return workflowIdempotent(fmt.Sprintf("change %d already scheduled", latest.ID), nil), nil
	}
	return workflowBlocked(fmt.Sprintf("change %d has a conflicting concurrent schedule", latest.ID)), nil
}

func (s *Service) applyWorkflowStateWithTimestamp(ctx context.Context, cmd workflowcallback.ChangeCommand, target string, start bool) (workflowcallback.Result, error) {
	current, err := s.loadWorkflowChange(ctx, cmd.ChangeID, cmd.TenantID)
	if err != nil {
		return workflowcallback.Result{}, err
	}
	timestampSet := !current.ActualEndDate.IsZero()
	if start {
		timestampSet = !current.ActualStartDate.IsZero()
	}
	if current.Edges.WorkItem.Status == target && timestampSet {
		return workflowIdempotent(fmt.Sprintf("change %d already at target state", current.ID), nil), nil
	}
	if current.Edges.WorkItem.Status != target && !common.IsValidChangeStatusTransition(current.Edges.WorkItem.Status, target, current.Type) {
		return workflowBlocked(fmt.Sprintf("illegal change transition %s -> %s", current.Edges.WorkItem.Status, target)), nil
	}
	tx, err := s.entClient.Tx(ctx)
	if err != nil {
		return workflowcallback.Result{}, err
	}
	rollback := func(cause error) (workflowcallback.Result, error) {
		if rbErr := tx.Rollback(); rbErr != nil {
			return workflowcallback.Result{}, fmt.Errorf("%w (rollback failed: %v)", cause, rbErr)
		}
		return workflowcallback.Result{}, cause
	}
	count, err := tx.Ticket.Update().Where(
		ticket.ID(current.WorkItemID), ticket.TenantID(cmd.TenantID), ticket.DeletedAtIsNil(),
		ticket.VersionEQ(current.Edges.WorkItem.Version), ticket.StatusEQ(current.Edges.WorkItem.Status),
	).SetStatus(target).SetUpdatedAt(time.Now()).AddVersion(1).Save(ctx)
	if err != nil {
		return rollback(err)
	}
	if count != 1 {
		_ = tx.Rollback()
		return s.classifyWorkflowState(ctx, cmd, target, start)
	}
	if !timestampSet {
		update := tx.Change.Update().Where(entchange.ID(current.ID), entchange.HasWorkItemWith(ticket.TenantID(cmd.TenantID), ticket.DeletedAtIsNil()))
		now := time.Now()
		if start {
			update.SetActualStartDate(now)
		} else {
			update.SetActualEndDate(now)
		}
		updated, updateErr := update.Save(ctx)
		if updateErr != nil {
			return rollback(updateErr)
		}
		if updated != 1 {
			return rollback(fmt.Errorf("change timestamp extension was not updated"))
		}
	}
	if err := tx.Commit(); err != nil {
		return workflowcallback.Result{}, err
	}
	return workflowApplied(fmt.Sprintf("change %d reached target state", current.ID), nil), nil
}

func (s *Service) classifyWorkflowState(ctx context.Context, cmd workflowcallback.ChangeCommand, target string, start bool) (workflowcallback.Result, error) {
	latest, err := s.loadWorkflowChange(ctx, cmd.ChangeID, cmd.TenantID)
	if err != nil {
		return workflowcallback.Result{}, err
	}
	timestampSet := !latest.ActualEndDate.IsZero()
	if start {
		timestampSet = !latest.ActualStartDate.IsZero()
	}
	if latest.Edges.WorkItem.Status == target && timestampSet {
		return workflowIdempotent(fmt.Sprintf("change %d already at target state", latest.ID), nil), nil
	}
	return workflowBlocked(fmt.Sprintf("change %d has a conflicting concurrent state update", latest.ID)), nil
}

func (s *Service) applyWorkflowRisk(ctx context.Context, cmd workflowcallback.ChangeCommand) (workflowcallback.Result, error) {
	current, err := s.loadWorkflowChange(ctx, cmd.ChangeID, cmd.TenantID)
	if err != nil {
		return workflowcallback.Result{}, err
	}
	risk := "medium"
	if current.Type == "emergency" {
		risk = "high"
	} else if current.Type == "minor" {
		risk = "low"
	}
	output := map[string]interface{}{"risk_level": risk, "impact_scope": current.ImpactScope}
	if current.RiskLevel == risk {
		return workflowIdempotent(fmt.Sprintf("change %d risk already assessed", current.ID), output), nil
	}
	tx, err := s.entClient.Tx(ctx)
	if err != nil {
		return workflowcallback.Result{}, err
	}
	rollback := func(cause error) (workflowcallback.Result, error) {
		if rbErr := tx.Rollback(); rbErr != nil {
			return workflowcallback.Result{}, fmt.Errorf("%w (rollback failed: %v)", cause, rbErr)
		}
		return workflowcallback.Result{}, cause
	}
	count, err := tx.Ticket.Update().Where(
		ticket.ID(current.WorkItemID), ticket.TenantID(cmd.TenantID), ticket.DeletedAtIsNil(), ticket.VersionEQ(current.Edges.WorkItem.Version),
	).SetUpdatedAt(time.Now()).AddVersion(1).Save(ctx)
	if err != nil {
		return rollback(err)
	}
	if count != 1 {
		_ = tx.Rollback()
		latest, loadErr := s.loadWorkflowChange(ctx, current.ID, cmd.TenantID)
		if loadErr != nil {
			return workflowcallback.Result{}, loadErr
		}
		if latest.RiskLevel == risk {
			return workflowIdempotent(fmt.Sprintf("change %d risk already assessed", current.ID), output), nil
		}
		return workflowBlocked(fmt.Sprintf("change %d has a conflicting concurrent risk assessment", current.ID)), nil
	}
	updated, err := tx.Change.Update().Where(entchange.ID(current.ID), entchange.HasWorkItemWith(ticket.TenantID(cmd.TenantID), ticket.DeletedAtIsNil())).SetRiskLevel(risk).Save(ctx)
	if err != nil {
		return rollback(err)
	}
	if updated != 1 {
		return rollback(fmt.Errorf("change risk extension was not updated"))
	}
	if err := tx.Commit(); err != nil {
		return workflowcallback.Result{}, err
	}
	return workflowApplied(fmt.Sprintf("change %d risk assessed", current.ID), output), nil
}
