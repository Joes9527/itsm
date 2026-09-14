package service

import (
	"context"
	"errors"
	"fmt"
	"strconv"

	"itsm-backend/authorization"
	"itsm-backend/common"
	"itsm-backend/dto"
	"itsm-backend/ent"
	"itsm-backend/ent/processinstance"
	"itsm-backend/ent/ticket"
)

// Only explicit actor/permission/participant denials may remove a row from a
// successful result. A forbidden error from identity/configuration resolution
// is not evidence of an ordinary row denial.
type bpmnTaskAccessDenial struct{ cause error }

func (e *bpmnTaskAccessDenial) Error() string { return e.cause.Error() }
func (e *bpmnTaskAccessDenial) Unwrap() error { return e.cause }

func denyBPMNTaskAccess(message string) error {
	return &bpmnTaskAccessDenial{cause: common.NewForbiddenError(message)}
}

func isBPMNTaskAccessDenial(err error) bool {
	var denial *bpmnTaskAccessDenial
	return errors.As(err, &denial)
}

// authorizeBoundTask intersects existing tenant, row, professional and BPMN
// authorities. Elevated task capabilities waive only the participant check.
func (e *CustomProcessEngine) authorizeBoundTask(ctx context.Context, client *ent.Client, task *ent.ProcessTask, scope BPMNAccessScope, command BPMNTaskCommand) error {
	if task == nil || task.TenantID != scope.TenantID {
		return denyBPMNTaskAccess("bound task tenant mismatch")
	}
	if task.AssigneeSource != BPMNAssigneeSourceWorkItem {
		return fmt.Errorf("unsupported task assignment source %q", task.AssigneeSource)
	}
	switch command {
	case "", BPMNTaskCommandComplete, BPMNTaskCommandCancel, BPMNTaskCommandSetVariables:
	case BPMNTaskCommandAssign, BPMNTaskCommandClaim, BPMNTaskCommandDelegate, BPMNTaskCommandCreateCounterSign, BPMNTaskCommandVote:
		return denyBPMNTaskAccess("bound task assignment is owned by WorkItem")
	default:
		return fmt.Errorf("unsupported bound task command %q", command)
	}
	actor, err := e.resolveAssignmentUser(ctx, client, scope.UserID, scope.TenantID)
	if unavailableBPMNIdentity(err) {
		return denyBPMNTaskAccess("bound task actor unavailable")
	}
	if err != nil {
		return err
	}
	item, policy, err := resolveBoundTaskWorkItem(ctx, client, task)
	if err != nil {
		return err
	}
	role := authorization.EffectiveSessionRole(actor)
	visible, err := client.Ticket.Query().Where(ticket.ID(item.ID), ticket.TenantID(scope.TenantID), authorization.WorkItemRowScope(actor.ID, role)).Exist(ctx)
	if err != nil {
		return err
	}
	if !visible {
		return denyBPMNTaskAccess("insufficient WorkItem row visibility")
	}
	taskAction := "read"
	elevated := scope.CanReadAllTasks
	if command != "" {
		taskAction = "update"
		elevated = scope.CanUpdateAllTasks
	}
	if !authorization.HasResourcePermission(client, role, "task", taskAction, scope.TenantID) ||
		!authorization.HasResourcePermission(client, role, policy.Resource, "read", scope.TenantID) {
		return denyBPMNTaskAccess("insufficient bound task read permission")
	}
	if command != "" && !authorization.HasResourcePermission(client, role, policy.Resource, policy.FulfillmentAction(), scope.TenantID) {
		return denyBPMNTaskAccess("insufficient professional fulfillment permission")
	}
	assignment, err := e.resolveTaskAssignment(ctx, client, task)
	if err != nil {
		return err
	}
	if command == BPMNTaskCommandComplete && assignment.State != "assigned" {
		return denyBPMNTaskAccess("bound task has no available responsible user")
	}
	if !elevated && assignment.ResponsibleUserID != scope.UserID {
		return denyBPMNTaskAccess("actor is not the current bound task participant")
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
	result.ResponsibleUserID = assignment.ResponsibleUserID
	result.ActorID = assignment.ActorID
	result.UIActions = s.engine.taskUIActions(ctx, task)
	return result, nil
}

func (e *CustomProcessEngine) boundAssignmentMatchesIdentity(ctx context.Context, task *ent.ProcessTask, identity string) (bool, error) {
	tokens := map[string]struct{}{}
	addToken(tokens, identity)
	if containsToken(task.Assignee, tokens) {
		return true, nil
	}
	id, err := strconv.Atoi(task.Assignee)
	if err != nil || id <= 0 {
		return false, nil
	}
	owner, err := e.resolveAssignmentUser(ctx, e.client, id, task.TenantID)
	if unavailableBPMNIdentity(err) {
		return false, nil
	}
	if err != nil {
		return false, err
	}
	return containsToken(owner.Username, tokens) || containsToken(owner.Email, tokens), nil
}
