package service

import (
	"context"
	"strings"

	"itsm-backend/dto"
	"itsm-backend/ent"
)

// taskUIActions uses command authorization, including the owning transaction's
// execution policy and directory snapshot. It never grants command authority.
func (e *CustomProcessEngine) taskUIActions(ctx context.Context, task *ent.ProcessTask) dto.BPMNTaskUIActions {
	var actions dto.BPMNTaskUIActions
	purpose, _ := task.TaskVariables["taskPurpose"].(string)
	if task.TaskType != "user_task" || (purpose != "" && purpose != "fulfillment") || e.isAsyncProcessTask(task) {
		actions.Reason = "请通过该任务的专用入口处理"
		return actions
	}
	if ValidateBPMNTaskLifecycle(BPMNTaskCommandClaim, task.Status) == nil &&
		(task.Assignee == "" || task.Assignee == "0") &&
		e.authorizeTaskCommandActorWithClient(ctx, e.client, task, BPMNTaskCommandClaim) == nil {
		actions.Claim = true
	}
	if strings.TrimSpace(task.FormKey) != "" {
		actions.Reason = "此任务需要填写专用表单"
		return actions
	}
	if ValidateBPMNTaskLifecycle(BPMNTaskCommandComplete, task.Status) == nil &&
		e.authorizeTaskCommandActorWithClient(ctx, e.client, task, BPMNTaskCommandComplete) == nil {
		actions.Complete = true
	}
	return actions
}
