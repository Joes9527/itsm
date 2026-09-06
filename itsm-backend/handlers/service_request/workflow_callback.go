package service_request

import (
	"context"
	"fmt"
	"reflect"
	"time"

	"itsm-backend/ent"
	"itsm-backend/ent/servicerequest"
	"itsm-backend/ent/ticket"
	"itsm-backend/ent/user"
	"itsm-backend/handlers/shared/workflowcallback"
)

func callbackApplied(message string) workflowcallback.Result {
	return workflowcallback.Result{Status: workflowcallback.StatusApplied, Message: message}
}

func callbackIdempotent(message string) workflowcallback.Result {
	return workflowcallback.Result{Status: workflowcallback.StatusIdempotent, Message: message}
}

func callbackBlocked(message string) workflowcallback.Result {
	return workflowcallback.Result{Status: workflowcallback.StatusBlocked, BlockCode: "handler_contract", Message: message}
}

// ApplyServiceRequestWorkflowCallback is the owning application-service boundary
// for synchronous BPMN mutations. Handler packages only bind typed commands.
func (s *Service) ApplyServiceRequestWorkflowCallback(ctx context.Context, cmd workflowcallback.ServiceRequestCommand) (workflowcallback.Result, error) {
	if cmd.RequestID <= 0 || cmd.TenantID <= 0 {
		return workflowcallback.Result{}, fmt.Errorf("invalid tenant or service request identity")
	}
	switch cmd.Action {
	case "update_request":
		return s.applyWorkflowUpdate(ctx, cmd)
	case "approve_request":
		return s.applyWorkflowStatus(ctx, cmd, "in_progress")
	case "reject_request", "cancel_request":
		return s.applyWorkflowStatus(ctx, cmd, "closed")
	case "assign_request":
		return s.applyWorkflowAssignment(ctx, cmd)
	case "provision_resource":
		return s.applyWorkflowProvision(ctx, cmd)
	case "complete_request":
		return s.applyWorkflowComplete(ctx, cmd)
	default:
		return callbackBlocked("unsupported service request callback action"), nil
	}
}

func (s *Service) loadWorkflowRequest(ctx context.Context, id, tenantID int) (*ent.ServiceRequest, error) {
	request, err := s.client.ServiceRequest.Query().Where(
		servicerequest.ID(id), requestScope(tenantID),
	).WithWorkItem().Only(ctx)
	if err != nil {
		return nil, err
	}
	if _, err := request.Edges.WorkItemOrErr(); err != nil {
		return nil, fmt.Errorf("service request %d requires WorkItem: %w", id, err)
	}
	return request, nil
}

func (s *Service) applyWorkflowUpdate(ctx context.Context, cmd workflowcallback.ServiceRequestCommand) (workflowcallback.Result, error) {
	current, err := s.loadWorkflowRequest(ctx, cmd.RequestID, cmd.TenantID)
	if err != nil {
		return workflowcallback.Result{}, fmt.Errorf("load service request: %w", err)
	}
	tx, err := s.client.Tx(ctx)
	if err != nil {
		return workflowcallback.Result{}, err
	}
	defer tx.Rollback()
	update := tx.ServiceRequest.Update().Where(servicerequest.ID(current.ID), requestScope(cmd.TenantID))
	changed := false
	if cmd.FormData != nil && !reflect.DeepEqual(current.FormData, cmd.FormData) {
		update.SetFormData(cmd.FormData)
		changed = true
	}
	if cmd.CostCenter != nil && current.CostCenter != *cmd.CostCenter {
		update.SetCostCenter(*cmd.CostCenter)
		changed = true
	}
	if cmd.DataClassification != nil && current.DataClassification != *cmd.DataClassification {
		update.SetDataClassification(*cmd.DataClassification)
		changed = true
	}
	if cmd.NeedsPublicIP != nil && current.NeedsPublicIP != *cmd.NeedsPublicIP {
		update.SetNeedsPublicIP(*cmd.NeedsPublicIP)
		changed = true
	}
	if cmd.SourceIPWhitelist != nil && !reflect.DeepEqual(current.SourceIPWhitelist, *cmd.SourceIPWhitelist) {
		update.SetSourceIPWhitelist(*cmd.SourceIPWhitelist)
		changed = true
	}
	if cmd.ExpireAt != nil && !current.ExpireAt.Equal(*cmd.ExpireAt) {
		update.SetExpireAt(*cmd.ExpireAt)
		changed = true
	}
	if cmd.ComplianceAck != nil && current.ComplianceAck != *cmd.ComplianceAck {
		update.SetComplianceAck(*cmd.ComplianceAck)
		changed = true
	}
	if !changed {
		return callbackIdempotent(fmt.Sprintf("service request %d already matches", current.ID)), nil
	}
	if err := tx.Ticket.UpdateOneID(current.TicketID).Where(ticket.TenantID(cmd.TenantID), ticket.RecordClassEQ("service_request_item"), ticket.DeletedAtIsNil(), ticket.VersionEQ(current.Edges.WorkItem.Version)).SetUpdatedAt(time.Now()).AddVersion(1).Exec(ctx); err != nil {
		_ = tx.Rollback()
		if !ent.IsNotFound(err) {
			return workflowcallback.Result{}, err
		}
		latest, loadErr := s.loadWorkflowRequest(ctx, current.ID, cmd.TenantID)
		if loadErr != nil {
			return workflowcallback.Result{}, loadErr
		}
		if serviceRequestCommandMatches(latest, cmd) {
			return callbackIdempotent("service request already matches"), nil
		}
		return callbackBlocked("service request has a conflicting concurrent update"), nil
	}
	affected, err := update.Save(ctx)
	if err != nil {
		return workflowcallback.Result{}, err
	}
	if affected == 1 {
		if err := tx.Commit(); err != nil {
			return workflowcallback.Result{}, err
		}
		return callbackApplied(fmt.Sprintf("service request %d updated", current.ID)), nil
	}
	_ = tx.Rollback()
	latest, err := s.loadWorkflowRequest(ctx, current.ID, cmd.TenantID)
	if err != nil {
		return workflowcallback.Result{}, err
	}
	if serviceRequestCommandMatches(latest, cmd) {
		return callbackIdempotent(fmt.Sprintf("service request %d already matches", current.ID)), nil
	}
	return callbackBlocked(fmt.Sprintf("service request %d has a conflicting concurrent update", current.ID)), nil
}

func serviceRequestCommandMatches(current *ent.ServiceRequest, cmd workflowcallback.ServiceRequestCommand) bool {
	return (cmd.FormData == nil || reflect.DeepEqual(current.FormData, cmd.FormData)) &&
		(cmd.CostCenter == nil || current.CostCenter == *cmd.CostCenter) &&
		(cmd.DataClassification == nil || current.DataClassification == *cmd.DataClassification) &&
		(cmd.NeedsPublicIP == nil || current.NeedsPublicIP == *cmd.NeedsPublicIP) &&
		(cmd.SourceIPWhitelist == nil || reflect.DeepEqual(current.SourceIPWhitelist, *cmd.SourceIPWhitelist)) &&
		(cmd.ExpireAt == nil || current.ExpireAt.Equal(*cmd.ExpireAt)) &&
		(cmd.ComplianceAck == nil || current.ComplianceAck == *cmd.ComplianceAck)
}

func (s *Service) applyWorkflowAssignment(ctx context.Context, cmd workflowcallback.ServiceRequestCommand) (workflowcallback.Result, error) {
	if cmd.AssigneeID <= 0 {
		return callbackBlocked("assignee is required"), nil
	}
	current, err := s.loadWorkflowRequest(ctx, cmd.RequestID, cmd.TenantID)
	if err != nil {
		return workflowcallback.Result{}, err
	}
	if _, err := s.client.User.Query().Where(user.ID(cmd.AssigneeID), user.TenantID(cmd.TenantID), user.Active(true)).Only(ctx); err != nil {
		if ent.IsNotFound(err) {
			result := callbackBlocked("assignee does not exist in callback tenant")
			result.BlockCode = "target_missing"
			return result, nil
		}
		return workflowcallback.Result{}, err
	}
	workItem, err := s.client.Ticket.Query().Where(ticket.ID(current.TicketID), ticket.TenantID(cmd.TenantID), ticket.DeletedAtIsNil(), ticket.RecordClassEQ("service_request_item")).Only(ctx)
	if err != nil {
		return workflowcallback.Result{}, err
	}
	if workItem.AssigneeID == cmd.AssigneeID {
		return callbackIdempotent(fmt.Sprintf("service request %d already assigned", current.ID)), nil
	}
	affected, err := s.client.Ticket.Update().Where(
		ticket.ID(workItem.ID), ticket.TenantID(cmd.TenantID), ticket.DeletedAtIsNil(), ticket.RecordClassEQ("service_request_item"), ticket.VersionEQ(workItem.Version),
	).SetAssigneeID(cmd.AssigneeID).SetUpdatedAt(time.Now()).AddVersion(1).Save(ctx)
	if err != nil {
		return workflowcallback.Result{}, err
	}
	if affected == 1 {
		return callbackApplied(fmt.Sprintf("service request %d assigned", current.ID)), nil
	}
	latest, err := s.client.Ticket.Query().Where(ticket.ID(workItem.ID), ticket.TenantID(cmd.TenantID), ticket.DeletedAtIsNil(), ticket.RecordClassEQ("service_request_item")).Only(ctx)
	if err != nil {
		return workflowcallback.Result{}, err
	}
	if latest.AssigneeID == cmd.AssigneeID {
		return callbackIdempotent(fmt.Sprintf("service request %d already assigned", current.ID)), nil
	}
	return callbackBlocked(fmt.Sprintf("service request %d has a conflicting assignment", current.ID)), nil
}

func (s *Service) applyWorkflowProvision(ctx context.Context, cmd workflowcallback.ServiceRequestCommand) (workflowcallback.Result, error) {
	current, err := s.loadWorkflowRequest(ctx, cmd.RequestID, cmd.TenantID)
	if err != nil {
		return workflowcallback.Result{}, err
	}
	if !current.StartedAt.IsZero() {
		return callbackIdempotent(fmt.Sprintf("service request %d provisioning already started", current.ID)), nil
	}
	tx, err := s.client.Tx(ctx)
	if err != nil {
		return workflowcallback.Result{}, err
	}
	defer tx.Rollback()
	if err := tx.Ticket.UpdateOneID(current.TicketID).Where(ticket.TenantID(cmd.TenantID), ticket.RecordClassEQ("service_request_item"), ticket.DeletedAtIsNil(), ticket.VersionEQ(current.Edges.WorkItem.Version)).SetUpdatedAt(time.Now()).AddVersion(1).Exec(ctx); err != nil {
		_ = tx.Rollback()
		if !ent.IsNotFound(err) {
			return workflowcallback.Result{}, err
		}
		latest, loadErr := s.loadWorkflowRequest(ctx, current.ID, cmd.TenantID)
		if loadErr != nil {
			return workflowcallback.Result{}, loadErr
		}
		if !latest.StartedAt.IsZero() {
			return callbackIdempotent("service request provisioning already started"), nil
		}
		return callbackBlocked("service request has a conflicting provisioning update"), nil
	}
	affected, err := tx.ServiceRequest.Update().Where(servicerequest.ID(current.ID), requestScope(cmd.TenantID), servicerequest.StartedAtIsNil()).SetStartedAt(time.Now()).Save(ctx)
	if err != nil {
		return workflowcallback.Result{}, err
	}
	if affected == 1 {
		if err := tx.Commit(); err != nil {
			return workflowcallback.Result{}, err
		}
		return callbackApplied(fmt.Sprintf("service request %d provisioning started", current.ID)), nil
	}
	_ = tx.Rollback()
	latest, err := s.loadWorkflowRequest(ctx, current.ID, cmd.TenantID)
	if err != nil {
		return workflowcallback.Result{}, err
	}
	if !latest.StartedAt.IsZero() {
		return callbackIdempotent(fmt.Sprintf("service request %d provisioning already started", current.ID)), nil
	}
	return callbackBlocked(fmt.Sprintf("service request %d has a conflicting provisioning update", current.ID)), nil
}

func validRequestStatusTransition(current, target string) bool {
	if current == target {
		return true
	}
	allowed := map[string]map[string]bool{
		"new": {"in_progress": true, "resolved": true, "closed": true}, "open": {"in_progress": true, "resolved": true, "closed": true},
		"assigned": {"in_progress": true, "resolved": true, "closed": true}, "pending": {"in_progress": true, "resolved": true, "closed": true},
		"in_progress": {"resolved": true, "closed": true},
	}
	return allowed[current][target]
}

func (s *Service) applyWorkflowStatus(ctx context.Context, cmd workflowcallback.ServiceRequestCommand, target string) (workflowcallback.Result, error) {
	return s.applyWorkflowAggregate(ctx, cmd, target, false)
}

func (s *Service) applyWorkflowComplete(ctx context.Context, cmd workflowcallback.ServiceRequestCommand) (workflowcallback.Result, error) {
	return s.applyWorkflowAggregate(ctx, cmd, "resolved", true)
}

func (s *Service) applyWorkflowAggregate(ctx context.Context, cmd workflowcallback.ServiceRequestCommand, target string, complete bool) (workflowcallback.Result, error) {
	tx, err := s.client.Tx(ctx)
	if err != nil {
		return workflowcallback.Result{}, err
	}
	rollback := func(cause error) (workflowcallback.Result, error) {
		if rbErr := tx.Rollback(); rbErr != nil {
			return workflowcallback.Result{}, fmt.Errorf("%w (rollback failed: %v)", cause, rbErr)
		}
		return workflowcallback.Result{}, cause
	}
	request, err := tx.ServiceRequest.Query().Where(servicerequest.ID(cmd.RequestID), requestScope(cmd.TenantID)).Only(ctx)
	if err != nil {
		return rollback(err)
	}
	workItem, err := tx.Ticket.Query().Where(ticket.ID(request.TicketID), ticket.TenantID(cmd.TenantID), ticket.DeletedAtIsNil(), ticket.RecordClassEQ("service_request_item")).Only(ctx)
	if err != nil {
		return rollback(err)
	}
	if !validRequestStatusTransition(workItem.Status, target) {
		_ = tx.Rollback()
		return callbackBlocked(fmt.Sprintf("illegal service request transition %s -> %s", workItem.Status, target)), nil
	}
	requestWrite := (cmd.CompletionNote != "" && request.CompletionNote != cmd.CompletionNote) || (complete && request.CompletedAt.IsZero())
	workItemWrite := workItem.Status != target || (complete && workItem.ResolvedAt.IsZero())
	if !requestWrite && !workItemWrite {
		_ = tx.Rollback()
		return callbackIdempotent(fmt.Sprintf("service request %d already at target state", request.ID)), nil
	}
	now := time.Now()
	if requestWrite {
		update := tx.ServiceRequest.Update().Where(
			servicerequest.ID(request.ID), requestScope(cmd.TenantID),
		)
		if cmd.CompletionNote != "" {
			update.SetCompletionNote(cmd.CompletionNote)
		}
		if complete && request.CompletedAt.IsZero() {
			update.SetCompletedAt(now)
		}
		count, updateErr := update.Save(ctx)
		if updateErr != nil {
			return rollback(updateErr)
		}
		if count != 1 {
			_ = tx.Rollback()
			return s.classifyWorkflowAggregate(ctx, cmd, target, complete)
		}
	}
	if workItemWrite || requestWrite {
		update := tx.Ticket.Update().Where(
			ticket.ID(workItem.ID), ticket.TenantID(cmd.TenantID), ticket.DeletedAtIsNil(), ticket.RecordClassEQ("service_request_item"), ticket.VersionEQ(workItem.Version),
		).SetStatus(target).SetUpdatedAt(now).AddVersion(1)
		if target == "resolved" || target == "closed" {
			update.SetResolvedAt(now)
		}
		count, updateErr := update.Save(ctx)
		if updateErr != nil {
			return rollback(updateErr)
		}
		if count != 1 {
			_ = tx.Rollback()
			return s.classifyWorkflowAggregate(ctx, cmd, target, complete)
		}
	}
	if err := tx.Commit(); err != nil {
		return workflowcallback.Result{}, err
	}
	return callbackApplied(fmt.Sprintf("service request %d reached target state", request.ID)), nil
}

func (s *Service) classifyWorkflowAggregate(ctx context.Context, cmd workflowcallback.ServiceRequestCommand, target string, complete bool) (workflowcallback.Result, error) {
	request, err := s.loadWorkflowRequest(ctx, cmd.RequestID, cmd.TenantID)
	if err != nil {
		return workflowcallback.Result{}, err
	}
	workItem, err := s.client.Ticket.Query().Where(ticket.ID(request.TicketID), ticket.TenantID(cmd.TenantID), ticket.DeletedAtIsNil(), ticket.RecordClassEQ("service_request_item")).Only(ctx)
	if err != nil {
		return workflowcallback.Result{}, err
	}
	exact := workItem.Status == target && (cmd.CompletionNote == "" || request.CompletionNote == cmd.CompletionNote)
	if complete {
		exact = exact && !request.CompletedAt.IsZero() && !workItem.ResolvedAt.IsZero()
	}
	if exact {
		return callbackIdempotent(fmt.Sprintf("service request %d already at target state", request.ID)), nil
	}
	return callbackBlocked(fmt.Sprintf("service request %d has a conflicting aggregate update", request.ID)), nil
}
