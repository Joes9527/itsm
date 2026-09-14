package bpmn

import (
	"context"
	"fmt"
	"strconv"
	"time"

	"itsm-backend/common"
	"itsm-backend/common/workitemidentity"
	"itsm-backend/ent"
	"itsm-backend/ent/change"
	"itsm-backend/ent/processapprovaldecision"
	"itsm-backend/ent/processcallbackoutbox"
	"itsm-backend/ent/processinstance"
	"itsm-backend/ent/processtask"
	"itsm-backend/ent/ticket"

	"itsm-backend/handlers/shared/workflowcallback"
	"itsm-backend/handlers/shared/workitemmutation"
)

func (h *ChangeServiceTaskHandler) applyChangeLifecycle(ctx context.Context, action string) (*CallbackEffect, error) {
	tenantID, err := RequireTenantID(ctx, nil)
	if err != nil {
		return nil, err
	}
	key, ok := BPMNCallbackExecutionKey(ctx)
	if !ok {
		return BlockedEffect(CallbackBlockHandlerContract, "Change lifecycle requires durable callback identity"), nil
	}
	if h.client == nil || h.changeService == nil {
		//lint:ignore ST1005 Preserve the existing domain term in this public error message.
		return nil, fmt.Errorf("Change command service unavailable")
	}
	row, err := h.client.ProcessCallbackOutbox.Query().Where(processcallbackoutbox.ExecutionKey(key), processcallbackoutbox.TenantID(tenantID), processcallbackoutbox.HandlerID(h.GetHandlerID()), processcallbackoutbox.Action(action), processcallbackoutbox.Status("processing")).Only(ctx)
	if err != nil {
		return nil, err
	}
	instance, err := h.client.ProcessInstance.Query().Where(processinstance.ID(row.ProcessInstanceID), processinstance.TenantID(tenantID)).Only(ctx)
	if err != nil {
		return nil, err
	}
	actorID := row.ActorID
	source := row.ActorSource
	switch row.CallbackKind {
	case "service_task":
		actorID, err = strconv.Atoi(instance.Initiator)
		source = "workflow"
		if err != nil || instance.CurrentActivityID != row.ElementID {
			return BlockedEffect(CallbackBlockHandlerContract, "workflow initiator or activity identity unavailable"), nil
		}
	case "user_task_callback":
		if row.ProcessTaskID <= 0 || actorID <= 0 || source != "workflow" {
			return BlockedEffect(CallbackBlockHandlerContract, "callback lacks authenticated completion actor"), nil
		}
		exists, err := h.client.ProcessTask.Query().Where(processtask.ID(row.ProcessTaskID), processtask.TenantID(tenantID), processtask.ProcessInstanceID(instance.ID), processtask.Status("completed"), processtask.TaskDefinitionKey(row.ElementID)).Exist(ctx)
		if err != nil {
			return nil, err
		}
		if !exists {
			return BlockedEffect(CallbackBlockHandlerContract, "callback does not own a completed task"), nil
		}
	default:
		return BlockedEffect(CallbackBlockHandlerContract, "unsupported Change callback kind"), nil
	}
	version := GetIntFromVars(row.Variables, "version")
	if version <= 0 || actorID <= 0 {
		return BlockedEffect(CallbackBlockHandlerContract, "Change callback requires actor and expected version"), nil
	}
	workItemID := instance.BusinessID
	if workItemID <= 0 {
		return BlockedEffect(CallbackBlockHandlerContract, "Change callback WorkItem missing"), nil
	}
	expectedKey, identityErr := workitemidentity.BusinessKey(workitemidentity.RecordClassChangeRequest, workItemID)
	if identityErr != nil {
		return BlockedEffect(CallbackBlockHandlerContract, "Change callback WorkItem identity invalid"), nil
	}
	if instance.BusinessType != workitemidentity.RecordClassChangeRequest || instance.BusinessKey != expectedKey {
		return BlockedEffect(CallbackBlockHandlerContract, "Change callback business type mismatch"), nil
	}
	current, err := h.client.Change.Query().Where(change.WorkItemID(workItemID), change.HasWorkItemWith(ticket.TenantID(tenantID), ticket.DeletedAtIsNil())).WithWorkItem().Only(ctx)
	if err != nil {
		return nil, err
	}
	if requested := GetIntFromVars(row.Variables, "change_id"); requested > 0 && requested != current.ID {
		return BlockedEffect(CallbackBlockHandlerContract, "Change callback target mismatch"), nil
	}
	cmd := workflowcallback.ChangeCommand{Meta: workitemmutation.Meta{TenantID: tenantID, ActorID: actorID, ExpectedVersion: version, Source: source, OperationID: key, CorrelationID: instance.ProcessInstanceID}, ChangeID: current.ID, TenantID: tenantID, Action: action}
	if action == "update_change" {
		if raw, exists := row.Variables["status"]; exists && raw != current.Edges.WorkItem.Status {
			return BlockedEffect(CallbackBlockHandlerContract, "update_change cannot mutate lifecycle status"), nil
		}
		for _, field := range []string{"outcome", "review_status", "approval_decision_id", "actual_start_date", "actual_end_date", "planned_start_date", "planned_end_date", "pir_id"} {
			if _, exists := row.Variables[field]; exists {
				return BlockedEffect(CallbackBlockHandlerContract, "update_change cannot mutate lifecycle facts"), nil
			}
		}
		for _, field := range []struct {
			key   string
			value **string
		}{{"title", &cmd.Title}, {"description", &cmd.Description}} {
			if raw, exists := row.Variables[field.key]; exists {
				value, ok := raw.(string)
				if !ok {
					return BlockedEffect(CallbackBlockHandlerContract, "metadata must be text"), nil
				}
				*field.value = &value
			}
		}
	}
	cmd.Evidence, _ = row.Variables["evidence"].(string)
	cmd.Outcome, _ = row.Variables["outcome"].(string)
	cmd.PIRID = GetIntFromVars(row.Variables, "pir_id")
	for _, field := range []struct {
		key   string
		value **time.Time
	}{{"planned_start_date", &cmd.PlannedStart}, {"planned_end_date", &cmd.PlannedEnd}, {"actual_end_date", &cmd.ActualEnd}} {
		if raw, exists := row.Variables[field.key]; exists {
			text, ok := raw.(string)
			parsed, parseErr := time.Parse(time.RFC3339, text)
			if !ok || parseErr != nil {
				return BlockedEffect(CallbackBlockHandlerContract, field.key+" must be RFC3339"), nil
			}
			*field.value = &parsed
		}
	}
	// Decisions are immutable engine facts associated with this exact completed
	// task. Neither instance variables nor participant forms select a decision.
	if action == "approve_change" || action == "reject_change" {
		if row.CallbackKind != "user_task_callback" {
			return BlockedEffect(CallbackBlockHandlerContract, "CAB authorization requires a completed approval task"), nil
		}
		decision, err := h.client.ProcessApprovalDecision.Query().Where(processapprovaldecision.TenantID(tenantID), processapprovaldecision.ProcessInstanceID(instance.ID), processapprovaldecision.ProcessTaskID(row.ProcessTaskID), processapprovaldecision.NodeKey(row.ElementID), processapprovaldecision.ActorID(actorID), processapprovaldecision.DecisionIn("approved", "rejected")).Only(ctx)
		if err != nil {
			if ent.IsNotFound(err) || ent.IsNotSingular(err) {
				return BlockedEffect(CallbackBlockHandlerContract, "matching immutable CAB decision missing or ambiguous"), nil
			}
			return nil, err
		}
		cmd.ApprovalDecisionID = decision.ID
	}
	result, err := h.changeService.ApplyChangeWorkflowCallback(ctx, cmd)
	if err != nil {
		// Frozen invalid domain input cannot improve on a worker retry. Keep actual
		// storage/audit/transport failures retryable through the existing outbox.
		if app, ok := common.AsAppError(err); ok {
			switch app.Code {
			case common.ErrCodeValidation, common.ErrCodeBadRequest:
				return BlockedEffect(CallbackBlockHandlerContract, "Change callback domain preconditions rejected"), nil
			}
		}

		return nil, err
	}
	if result.LifecycleResult == nil {
		return BlockedEffect(CallbackBlockHandlerContract, "Change lifecycle result missing"), nil
	}
	return callbackEffectFromWorkflowResult(result)
}
