package bpmn

import (
	"context"
	"fmt"
	"strconv"
	"strings"

	"itsm-backend/dto"
	"itsm-backend/ent/incident"
	"itsm-backend/ent/processcallbackoutbox"
	"itsm-backend/ent/processinstance"
	"itsm-backend/ent/processtask"
	"itsm-backend/ent/ticket"
	"itsm-backend/handlers/shared/workitemmutation"
)

func (h *IncidentServiceTaskHandler) applyLifecycle(ctx context.Context, action string) (*CallbackEffect, error) {
	tenantID, err := RequireTenantID(ctx, nil)
	if err != nil {
		return nil, err
	}
	key, ok := BPMNCallbackExecutionKey(ctx)
	if !ok {
		return BlockedEffect(CallbackBlockHandlerContract, "Incident lifecycle requires durable callback identity"), nil
	}
	if h.client == nil || h.incidentService == nil {
		//lint:ignore ST1005 Preserve the existing domain term in this public error message.
		return nil, fmt.Errorf("Incident command service unavailable")
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
		return BlockedEffect(CallbackBlockHandlerContract, "unsupported Incident callback kind"), nil
	}
	version := GetIntFromVars(row.Variables, "version")
	if version <= 0 || actorID <= 0 {
		return BlockedEffect(CallbackBlockHandlerContract, "Incident callback requires actor and expected version"), nil
	}
	workItemID := instance.BusinessID
	if workItemID <= 0 {
		return BlockedEffect(CallbackBlockHandlerContract, "Incident callback WorkItem missing"), nil
	}
	if instance.BusinessType != "incident" {
		return BlockedEffect(CallbackBlockHandlerContract, "Incident callback business type mismatch"), nil
	}
	current, err := h.client.Incident.Query().Where(incident.WorkItemID(workItemID), incident.HasWorkItemWith(ticket.TenantID(tenantID), ticket.DeletedAtIsNil())).Only(ctx)
	if err != nil {
		return nil, err
	}
	if requested := GetIntFromVars(row.Variables, "incident_id"); requested > 0 && requested != current.ID {
		return BlockedEffect(CallbackBlockHandlerContract, "Incident callback target mismatch"), nil
	}
	resolution, _ := row.Variables["resolution"].(string)
	reason, _ := row.Variables["reason"].(string)
	if reason == "" {
		reason, _ = row.Variables["feedback"].(string)
	}
	if action == "escalate_incident" {
		reason, _ = row.Variables["escalation_reason"].(string)
	}
	result, err := h.incidentService.ApplyIncidentCommand(ctx, dto.IncidentCommand{Meta: workitemmutation.Meta{TenantID: tenantID, ActorID: actorID, ExpectedVersion: version, Source: source, OperationID: key, CorrelationID: instance.ProcessInstanceID}, IncidentID: current.ID, Action: strings.TrimSuffix(action, "_incident"), Reason: reason, Resolution: resolution, AssigneeID: GetIntFromVars(row.Variables, "assignee_id"), EscalationLevel: GetIntFromVars(row.Variables, "escalation_level")})
	if err != nil {
		return nil, err
	}
	effect := &CallbackEffect{Status: CallbackEffectApplied, Message: "Incident command applied", LifecycleResult: &result}
	if result.Replayed {
		effect.Status = CallbackEffectIdempotent
		effect.Message = "Incident command replayed"
	}
	return effect, nil
}
