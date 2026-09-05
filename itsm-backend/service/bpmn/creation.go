package bpmn

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"itsm-backend/ent"
	"itsm-backend/ent/processcallbackoutbox"
	"itsm-backend/ent/processinstance"
	"itsm-backend/ent/user"
	creation "itsm-backend/handlers/common/workitemcreation"
	"strconv"
	"strings"
)

func executeWorkItemCreation(ctx context.Context, client *ent.Client, app creation.Application, handlerID, action, class string) (*CallbackEffect, error) {
	key, ok := BPMNCallbackExecutionKey(ctx)
	if !ok {
		return BlockedEffect(CallbackBlockHandlerContract, "creation requires durable callback identity"), nil
	}
	tenantID, err := RequireTenantID(ctx, nil)
	if err != nil {
		return nil, err
	}
	if app == nil {
		return BlockedEffect(CallbackBlockHandlerContract, "creation application is unavailable"), nil
	}
	row, err := client.ProcessCallbackOutbox.Query().Where(processcallbackoutbox.ExecutionKeyEQ(key), processcallbackoutbox.TenantIDEQ(tenantID), processcallbackoutbox.HandlerIDEQ(handlerID), processcallbackoutbox.ActionEQ(action), processcallbackoutbox.StatusEQ("processing")).Only(ctx)
	if ent.IsNotFound(err) {
		return BlockedEffect(CallbackBlockHandlerContract, "creation callback is not claimed"), nil
	}
	if err != nil {
		return nil, err
	}
	instance, err := client.ProcessInstance.Query().Where(processinstance.IDEQ(row.ProcessInstanceID), processinstance.TenantIDEQ(tenantID), processinstance.StatusEQ("running")).Only(ctx)
	if ent.IsNotFound(err) {
		return BlockedEffect(CallbackBlockTargetMissing, "source process is unavailable"), nil
	}
	if err != nil {
		return nil, err
	}
	if row.CallbackKind != "service_task" || instance.CurrentActivityID != row.ElementID {
		return BlockedEffect(CallbackBlockHandlerContract, "creation callback does not own the source activity"), nil
	}
	actorID, err := strconv.Atoi(strings.TrimSpace(instance.Initiator))
	if err != nil || actorID <= 0 {
		return BlockedEffect(CallbackBlockHandlerContract, "source process has no authenticated initiator"), nil
	}
	actor, err := client.User.Query().Where(user.IDEQ(actorID), user.TenantIDEQ(tenantID), user.ActiveEQ(true)).Only(ctx)
	if ent.IsNotFound(err) {
		return BlockedEffect(CallbackBlockTargetMissing, "source initiator is unavailable"), nil
	}
	if err != nil {
		return nil, err
	}
	command, requesterID, err := creationCommandFromCallback(row.Variables, key, class, actorID)
	if err != nil {
		return BlockedEffect(CallbackBlockHandlerContract, "invalid declared creation payload"), nil
	}
	result, err := app.Create(ctx, creation.Identity{TenantID: tenantID, ActorID: actor.ID, RequesterID: requesterID, Role: actor.Role, Channel: "bpmn", Provider: "bpmn"}, command)
	if err != nil {
		var typed *creation.IntakeError
		if errors.As(err, &typed) && !typed.Retryable {
			return BlockedEffect(CallbackBlockHandlerContract, typed.Message), nil
		}
		return nil, err
	}
	if result == nil {
		return nil, creation.NewInternalFailure("creation application returned no receipt", nil)
	}
	effect := AppliedEffect("work item created", nil)
	if result.Replayed {
		effect = IdempotentEffect("work item creation replayed", nil)
	}
	effect.CreationResult = result
	return effect, nil
}
func creationCommandFromCallback(values map[string]any, key, class string, actorID int) (creation.CreateWorkItemCommand, int, error) {
	requester := actorID
	requesterKey := "reporter_id"
	if class == creation.RecordClassChangeRequest {
		requesterKey = "created_by"
	}
	if raw, present := values[requesterKey]; present {
		value, err := CallbackInteger(raw)
		if err != nil || value <= 0 {
			return creation.CreateWorkItemCommand{}, 0, fmt.Errorf("invalid requester")
		}
		requester = value
	}
	raw := map[string]any{"idempotencyKey": "bpmn-create:" + key, "confirmation": "confirmed", "recordClass": class, "intakeKind": class, "sourceReference": map[string]any{"provider": "bpmn", "eventId": key}}
	for from, to := range map[string]string{"title": "title", "description": "description", "priority": "priority", "assignee_id": "assigneeId", "ci_ids": "ciIds", "template_id": "templateId", "parent_ticket_id": "parentTicketId", "tag_ids": "tagIds", "workflow_definition_key": "workflowDefinitionKey", "form_values": "formValues"} {
		if value, ok := values[from]; ok {
			raw[to] = value
		}
	}
	input := map[string]any{}
	fieldMap := map[string]string{}
	professional := "incident"
	if class == creation.RecordClassIncident {
		fieldMap = map[string]string{"type": "type", "severity": "severity", "impact": "impact", "urgency": "urgency", "category": "category", "subcategory": "subcategory", "detected_at": "detectedAt", "impact_analysis": "impactAnalysis", "metadata": "metadata", "source": "source"}
	} else if class == creation.RecordClassChangeRequest {
		professional = "change"
		fieldMap = map[string]string{"type": "type", "justification": "justification", "impact_scope": "impactScope", "risk_level": "riskLevel", "planned_start_date": "plannedStartDate", "planned_end_date": "plannedEndDate", "implementation_plan": "implementationPlan", "rollback_plan": "rollbackPlan", "affected_cis": "affectedCis", "related_tickets": "relatedTickets", "related_ticket_numbers": "relatedTicketNumbers"}
	} else {
		return creation.CreateWorkItemCommand{}, 0, fmt.Errorf("unsupported creation class")
	}
	for from, to := range fieldMap {
		if value, ok := values[from]; ok {
			input[to] = value
		}
	}
	raw[professional] = input
	encoded, err := json.Marshal(raw)
	if err != nil {
		return creation.CreateWorkItemCommand{}, 0, err
	}
	command, err := creation.DecodeCreateWorkItemCommand(bytes.NewReader(encoded))
	return command, requester, err
}
