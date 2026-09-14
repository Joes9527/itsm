package service

import (
	"context"
	"strings"

	"itsm-backend/dto"
	"itsm-backend/ent"
)

// taskUIActions projects existing command authority for the simple ticket task UI.
// It is advisory: every mutation still rechecks authority and lifecycle in its transaction.
func (e *CustomProcessEngine) taskUIActions(ctx context.Context, task *ent.ProcessTask) dto.BPMNTaskUIActions {
	result := dto.BPMNTaskUIActions{}
	purpose, _ := task.TaskVariables["taskPurpose"].(string)
	if task.TaskType != "user_task" || (purpose != "" && purpose != "fulfillment") || e.isAsyncProcessTask(task) {
		result.Reason = "请通过该任务的专用入口处理"
		return result
	}
	if ValidateBPMNTaskLifecycle(BPMNTaskCommandClaim, task.Status) == nil && (task.Assignee == "" || task.Assignee == "0") && e.authorizeTaskCommandActorWithClient(ctx, e.client, task, BPMNTaskCommandClaim) == nil {
		result.Claim = true
	}
	if strings.TrimSpace(task.FormKey) != "" {
		result.Reason = "此任务需要填写专用表单"
		return result
	}
	if ValidateBPMNTaskLifecycle(BPMNTaskCommandComplete, task.Status) == nil && e.authorizeTaskCommandActorWithClient(ctx, e.client, task, BPMNTaskCommandComplete) == nil {
		result.Complete = true
	}
	return result
}

// Scope identity reuse to this response only; commands retain fresh transaction reads.
func (e *CustomProcessEngine) taskUIReadProjection(ctx context.Context) (*CustomProcessEngine, error) {
	scope, err := BPMNAccessScopeFromContext(ctx)
	if err != nil {
		return nil, err
	}
	projected := *e
	resolver := *e.participationResolver
	resolver.readActor = nil
	if !scope.CanUpdateAllTasks {
		actor, err := resolver.resolveActor(ctx, scope)
		if err != nil {
			return nil, err
		}
		resolver.readActor = actor
	}
	projected.participationResolver = &resolver
	return &projected, nil
}
