package change

import (
	"context"
	"fmt"

	"itsm-backend/dto"
	"itsm-backend/handlers/shared/workflowcallback"
	"itsm-backend/service/bpmn"
)

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

func (s *Service) applyWorkflowUpdate(ctx context.Context, cmd workflowcallback.ChangeCommand) (workflowcallback.Result, error) {
	key, ok := bpmn.BPMNCallbackExecutionKey(ctx)
	if !ok || key != cmd.Meta.OperationID || cmd.Meta.TenantID != cmd.TenantID {
		return workflowBlocked("metadata requires matching durable callback identity"), nil
	}
	result, err := s.ApplyMetadata(ctx, MetadataCommand{Meta: cmd.Meta, ChangeID: cmd.ChangeID, Patch: dto.UpdateChangeRequest{Title: cmd.Title, Description: cmd.Description}, processingCallback: true})
	if err != nil {
		return workflowcallback.Result{}, err
	}
	status := workflowcallback.StatusApplied
	if result.Replayed {
		status = workflowcallback.StatusIdempotent
	}
	return workflowcallback.Result{Status: status, Message: "Change metadata committed", LifecycleResult: &result}, nil
}
