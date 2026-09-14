package service

import (
	"context"
	"strconv"

	"itsm-backend/authorization"
	"itsm-backend/common"
	"itsm-backend/dto"
	"itsm-backend/ent"
	"itsm-backend/ent/processinstance"
	"itsm-backend/ent/ticket"
	"itsm-backend/ent/user"
)

// authorizeBoundTask intersects existing tenant, row, professional and BPMN
// authorities. Elevated task capabilities waive only the participant check.
func (e *CustomProcessEngine) authorizeBoundTask(ctx context.Context, client *ent.Client, task *ent.ProcessTask, scope BPMNAccessScope, command BPMNTaskCommand) error {
	if task == nil || task.TenantID != scope.TenantID {
		return common.NewForbiddenError("bound task tenant mismatch")
	}
	if task.AssigneeSource != BPMNAssigneeSourceWorkItem {
		return common.NewForbiddenError("unsupported task assignment source")
	}
	switch command {
	case "", BPMNTaskCommandComplete, BPMNTaskCommandCancel, BPMNTaskCommandSetVariables:
	case BPMNTaskCommandAssign, BPMNTaskCommandClaim, BPMNTaskCommandDelegate, BPMNTaskCommandCreateCounterSign, BPMNTaskCommandVote:
		return common.NewForbiddenError("bound task assignment is owned by WorkItem")
	default:
		return common.NewForbiddenError("unsupported bound task command")
	}
	actor, err := loadTaskMutationActor(ctx, client, scope)
	if err != nil {
		return common.NewForbiddenError("bound task actor unavailable")
	}
	item, policy, err := resolveBoundTaskWorkItem(ctx, client, task)
	if err != nil {
		return err
	}
	visible, err := client.Ticket.Query().Where(ticket.ID(item.ID), ticket.TenantID(scope.TenantID), authorization.WorkItemRowScope(actor.ID, actor.Role)).Exist(ctx)
	if err != nil {
		return err
	}
	if !visible {
		return common.NewForbiddenError("insufficient WorkItem row visibility")
	}
	taskAction := "read"
	elevated := scope.CanReadAllTasks
	if command != "" {
		taskAction = "update"
		elevated = scope.CanUpdateAllTasks
	}
	if !authorization.HasResourcePermission(client, actor.Role, "task", taskAction, scope.TenantID) ||
		!authorization.HasResourcePermission(client, actor.Role, policy.Resource, "read", scope.TenantID) {
		return common.NewForbiddenError("insufficient bound task read permission")
	}
	if command != "" && !authorization.HasResourcePermission(client, actor.Role, policy.Resource, policy.FulfillmentAction(), scope.TenantID) {
		return common.NewForbiddenError("insufficient professional fulfillment permission")
	}
	assignment, err := e.resolveTaskAssignment(ctx, client, task)
	if err != nil {
		return err
	}
	if command == BPMNTaskCommandComplete && assignment.State != "assigned" {
		return common.NewForbiddenError("bound task has no available responsible user")
	}
	if !elevated && assignment.ResponsibleUserID != scope.UserID {
		return common.NewForbiddenError("actor is not the current bound task participant")
	}
	return nil
}

func (e *CustomProcessEngine) projectTaskAssignment(ctx context.Context, task *ent.ProcessTask) (*ent.ProcessTask, error) {
	assignment, err := e.resolveTaskAssignment(ctx, e.client, task)
	if err != nil {
		return nil, err
	}
	projected := *task
	projected.Assignee = assignment.Assignee
	return &projected, nil
}

// ProjectTaskView accepts a service-authorized task and rechecks read authority.
// Controllers use this boundary instead of returning an Ent entity.
func (s *bpmnTaskService) ProjectTaskView(ctx context.Context, task *ent.ProcessTask) (*dto.BPMNTaskResponse, error) {
	scope, err := BPMNAccessScopeFromContext(ctx)
	if err != nil {
		return nil, err
	}
	if err = s.authorizeTaskRead(ctx, task, scope); err != nil {
		return nil, err
	}
	instance, err := s.client.ProcessInstance.Query().Where(processinstance.ID(task.ProcessInstanceID), processinstance.TenantID(scope.TenantID)).Only(ctx)
	if err != nil {
		return nil, err
	}
	assignment, err := s.engine.resolveTaskAssignment(ctx, s.client, task)
	if err != nil {
		return nil, err
	}
	result := dto.ToBPMNTaskResponse(task, instance)
	result.Assignee = assignment.Assignee
	result.AssigneeSource = assignment.Source
	result.AssignmentState = assignment.State
	result.UIActions = s.engine.taskUIActions(ctx, task)
	return result, nil
}

func (e *CustomProcessEngine) boundAssignmentMatchesIdentity(ctx context.Context, task *ent.ProcessTask, identity string) bool {
	tokens := map[string]struct{}{}
	addToken(tokens, identity)
	if containsToken(task.Assignee, tokens) {
		return true
	}
	id, err := strconv.Atoi(task.Assignee)
	if err != nil || id <= 0 {
		return false
	}
	owner, err := e.client.User.Query().Where(user.ID(id), user.TenantID(task.TenantID)).Only(ctx)
	return err == nil && (containsToken(owner.Username, tokens) || containsToken(owner.Email, tokens))
}
