package service

import (
	"context"
	"fmt"

	"itsm-backend/common"
	"itsm-backend/dto"
	"itsm-backend/ent"
	"itsm-backend/ent/processinstance"
	"itsm-backend/ent/processtask"
	"itsm-backend/service/bpmn"
)

// completeReleaseWorkflowTask translates a professional Release command to
// the one authoritative ProcessTask. Missing engine, instance, or task is a
// hard error: there is no non-BPMN release lifecycle fallback.
func (s *ReleaseService) completeReleaseWorkflowTask(
	ctx context.Context,
	tenantID, actorID, releaseID int,
	taskDefinitionKey string,
	variables map[string]interface{},
) error {
	if s.processEngine == nil {
		return fmt.Errorf("release workflow engine is unavailable")
	}
	if tenantID <= 0 || actorID <= 0 || releaseID <= 0 || taskDefinitionKey == "" {
		return fmt.Errorf("release workflow command identity is invalid")
	}
	instance, err := s.client.ProcessInstance.Query().Where(
		processinstance.TenantID(tenantID),
		processinstance.BusinessType(string(dto.BusinessTypeRelease)),
		processinstance.BusinessID(releaseID),
		processinstance.Status("running"),
	).Only(ctx)
	if err != nil {
		if ent.IsNotFound(err) {
			return fmt.Errorf("release workflow instance not found")
		}
		return fmt.Errorf("load release workflow instance: %w", err)
	}
	task, err := s.client.ProcessTask.Query().Where(
		processtask.TenantID(tenantID),
		processtask.ProcessInstanceID(instance.ID),
		processtask.TaskDefinitionKey(taskDefinitionKey),
		processtask.StatusIn(
			common.ProcessTaskStatusCreated,
			common.ProcessTaskStatusAssigned,
			common.ProcessTaskStatusStarted,
			common.ProcessTaskStatusDelegated,
		),
	).Only(ctx)
	if err != nil {
		if ent.IsNotFound(err) {
			return fmt.Errorf("release workflow task %s not found", taskDefinitionKey)
		}
		return fmt.Errorf("load release workflow task %s: %w", taskDefinitionKey, err)
	}
	workflowCtx := context.WithValue(ctx, bpmn.BPMNTenantIDContextKey, tenantID)
	workflowCtx = context.WithValue(workflowCtx, bpmn.BPMNUserIDContextKey, actorID)
	workflowCtx = WithBPMNAccessScope(workflowCtx, BPMNAccessScope{
		UserID: actorID, TenantID: tenantID, CanUpdateAllTasks: true,
	})
	if err := s.processEngine.CompleteTask(workflowCtx, task.TaskID, variables); err != nil {
		return fmt.Errorf("complete ProcessTask %s: %w", task.TaskID, err)
	}
	return nil
}
