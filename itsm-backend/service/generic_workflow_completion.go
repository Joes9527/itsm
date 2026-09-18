package service

import (
	"context"
	"strconv"
	"strings"

	"itsm-backend/ent"
	"itsm-backend/ent/processinstance"
)

type (
	genericApprovalIntentKey struct{}
	genericApprovalIntent    struct {
		taskID, action   string
		suppliedReserved bool
	}
)

// WithBPMNApprovalDecisionIntent marks only controller-validated intent. The
// engine still authorizes the actor and verifies the immutable approval node;
// it persists the decision in the same transaction before advancing the graph.
func WithBPMNApprovalDecisionIntent(ctx context.Context, taskID, action string, rawVariables map[string]interface{}) context.Context {
	return context.WithValue(ctx, genericApprovalIntentKey{}, genericApprovalIntent{taskID: taskID, action: action, suppliedReserved: RejectGenericFulfillmentReservedInputs(rawVariables) != nil})
}

func prepareGenericWorkflowCompletion(ctx context.Context, client *ent.Client, instance *ent.ProcessInstance, task *ent.ProcessTask, input map[string]interface{}) (map[string]interface{}, *genericWorkflowGate, error) {
	if instance.BusinessType != "generic" {
		return input, nil, nil
	}
	gate, err := loadGenericWorkflowGate(ctx, client, task.TenantID, instance.BusinessID, false)
	if err != nil || gate == nil {
		return input, gate, err
	}
	if gate.instance == nil || gate.instance.ID != instance.ID || !gate.facts.Running {
		return nil, nil, genericGateError("task workflow is not running")
	}
	node := genericWorkflowTaskNode(gate.process, task.TaskDefinitionKey)
	if node == nil {
		return nil, nil, genericGateError("task is missing from immutable definition")
	}
	variables := make(map[string]interface{}, len(input))
	for k, v := range input {
		variables[k] = v
	}
	intent, trusted := ctx.Value(genericApprovalIntentKey{}).(genericApprovalIntent)
	trusted = trusted && (intent.taskID == task.TaskID || intent.taskID == strconv.Itoa(task.ID))
	if trusted {
		if node.TaskPurpose != "approval" || intent.suppliedReserved || (intent.action != "approve" && intent.action != "reject") {
			return nil, nil, genericGateError("invalid approval decision intent")
		}
		delete(variables, "approvalResult")
	}
	if err := RejectGenericFulfillmentReservedInputs(variables); err != nil {
		return nil, nil, err
	}
	if node.TaskPurpose == "approval" {
		if !trusted {
			return nil, nil, genericGateError("use the approval decision command")
		}
		variables["approvalAction"] = intent.action
		if intent.action == "approve" {
			variables["approvalResult"] = "approved"
		} else {
			variables["approvalResult"] = "rejected"
		}
	} else {
		if _, ok := variables["approvalAction"]; ok {
			return nil, nil, genericGateError("approval decisions require an approval task")
		}
	}
	pre, err := node.WorkItemPrerequisite()
	if err != nil {
		return nil, nil, err
	}
	rawNote, notePresent := variables[WorkItemCompletionNote]
	note, noteString := rawNote.(string)
	if notePresent && (pre != WorkItemPrerequisiteInProgress || !noteString) {
		return nil, nil, genericGateError("handling evidence is accepted only on Handle completion")
	}
	if err := evaluateGenericWorkflowPrerequisite(pre, gate.facts, note, true); err != nil {
		return nil, nil, err
	}
	if pre == WorkItemPrerequisiteInProgress {
		variables[WorkItemCompletionNote] = strings.TrimSpace(note)
	}
	return variables, gate, nil
}

func rejectGenericWorkflowVariableUpdate(ctx context.Context, client *ent.Client, instance *ent.ProcessInstance, variables map[string]interface{}) (*genericWorkflowGate, error) {
	if instance.BusinessType != "generic" {
		return nil, nil
	}
	gate, err := loadGenericWorkflowGate(ctx, client, instance.TenantID, instance.BusinessID, true)
	if err != nil || gate == nil {
		return gate, err
	}
	if err := RejectGenericFulfillmentReservedInputs(variables); err != nil {
		return nil, err
	}
	if _, ok := variables[WorkItemCompletionNote]; ok {
		return nil, genericGateError("handling evidence is accepted only on Handle completion")
	}
	return gate, nil
}

func rejectGenericWorkflowTaskVariableUpdate(ctx context.Context, client *ent.Client, task *ent.ProcessTask, variables map[string]interface{}) error {
	instance, err := client.ProcessInstance.Query().Where(processinstance.ID(task.ProcessInstanceID), processinstance.TenantID(task.TenantID)).Only(ctx)
	if err != nil {
		return err
	}
	_, err = rejectGenericWorkflowVariableUpdate(ctx, client, instance, variables)
	return err
}

func projectGenericWorkflowVariables(variables map[string]interface{}, gate *genericWorkflowGate) {
	variables["approval_required"] = gate.config.ApprovalRequired
	variables["need_escalate"] = gate.config.NeedEscalate
	delete(variables, WorkItemCompletionNote)
	delete(variables, "approvalResult")
	if gate.facts.Approved {
		variables["approvalResult"] = "approved"
	}
}
