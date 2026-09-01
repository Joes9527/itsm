package bpmn

import (
	"context"
	"fmt"
	"strings"

	"itsm-backend/ent"
	"itsm-backend/ent/processapprovaldecision"
)

// persistedApprovalDecisionEffect proves a completed approval callback against
// the immutable approval fact written by the engine. It deliberately performs
// no business-state transition: professional services remain the only approval
// lifecycle authority.
func persistedApprovalDecisionEffect(ctx context.Context, client *ent.Client, task *ent.ProcessTask, variables map[string]interface{}, message string) (*CallbackEffect, error) {
	if task == nil {
		return BlockedEffect(CallbackBlockTargetMissing, "approval task is missing"), nil
	}
	tenantID, err := RequireTenantID(ctx, variables)
	if err != nil {
		return nil, err
	}
	action, _ := task.TaskVariables["approvalAction"].(string)
	if strings.TrimSpace(action) == "" {
		return BlockedEffect(CallbackBlockTargetMissing, "authoritative approval action is missing"), nil
	}
	exists, err := client.ProcessApprovalDecision.Query().Where(
		processapprovaldecision.TenantID(tenantID),
		processapprovaldecision.ProcessInstanceID(task.ProcessInstanceID),
		processapprovaldecision.ProcessTaskID(task.ID),
		processapprovaldecision.NodeKey(task.TaskDefinitionKey),
		processapprovaldecision.Action(action),
	).Exist(ctx)
	if err != nil {
		return nil, fmt.Errorf("approval decision lookup failed: %w", err)
	}
	if !exists {
		return BlockedEffect(CallbackBlockTargetMissing, "authoritative approval decision is missing"), nil
	}
	return IdempotentEffect(message, nil), nil
}
