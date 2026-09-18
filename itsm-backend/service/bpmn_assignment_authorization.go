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
	"itsm-backend/ent/processtask"
	creation "itsm-backend/handlers/common/workitemcreation"
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
	_, err := e.authorizeBoundTaskAssignment(ctx, client, task, scope, command)
	return err
}

// A list may reuse this successful projection for the same task. Mutation
// callers still resolve all authority afresh in their owning transaction.
func (e *CustomProcessEngine) authorizeBoundTaskAssignment(ctx context.Context, client *ent.Client, task *ent.ProcessTask, scope BPMNAccessScope, command BPMNTaskCommand) (BPMNTaskAssignment, error) {
	if task == nil || task.TenantID != scope.TenantID {
		return BPMNTaskAssignment{}, denyBPMNTaskAccess("bound task tenant mismatch")
	}
	if task.AssigneeSource != BPMNAssigneeSourceWorkItem {
		return BPMNTaskAssignment{}, fmt.Errorf("unsupported task assignment source %q", task.AssigneeSource)
	}
	switch command {
	case "", BPMNTaskCommandComplete, BPMNTaskCommandCancel, BPMNTaskCommandSetVariables:
	case BPMNTaskCommandAssign, BPMNTaskCommandClaim, BPMNTaskCommandDelegate, BPMNTaskCommandCreateCounterSign, BPMNTaskCommandVote:
		return BPMNTaskAssignment{}, denyBPMNTaskAccess("bound task assignment is owned by WorkItem")
	default:
		return BPMNTaskAssignment{}, fmt.Errorf("unsupported bound task command %q", command)
	}
	actor, err := e.resolveAssignmentUser(ctx, client, scope.UserID, scope.TenantID)
	if unavailableBPMNIdentity(err) {
		return BPMNTaskAssignment{}, denyBPMNTaskAccess("bound task actor unavailable")
	}
	if err != nil {
		return BPMNTaskAssignment{}, err
	}
	item, policy, err := resolveBoundTaskWorkItem(ctx, client, task)
	if err != nil {
		return BPMNTaskAssignment{}, err
	}
	role := authorization.EffectiveSessionRole(actor)
	visible, err := boundWorkItemVisible(ctx, client, item, actor, scope.TenantID)
	if err != nil {
		return BPMNTaskAssignment{}, err
	}
	if !visible {
		return BPMNTaskAssignment{}, denyBPMNTaskAccess("insufficient WorkItem row visibility")
	}
	taskAction := "read"
	elevated := scope.CanReadAllTasks
	if command != "" {
		taskAction = "update"
		elevated = scope.CanUpdateAllTasks
	}
	if e.owningTx == nil || e.owningTx.Client() != client {
		return BPMNTaskAssignment{}, fmt.Errorf("bound authorization requires owning transaction")
	}
	var permissions []authorization.Permission
	view := taskReadSnapshot(ctx, client)
	key := [2]int{scope.TenantID, actor.ID}
	var cached bool
	if view != nil {
		permissions, cached = view.permissions[key]
	}
	if !cached {
		permissions, err = authorization.CurrentSessionPermissions(ctx, e.owningTx, creation.Identity{ActorID: actor.ID, TenantID: scope.TenantID, Role: role})
		if err == nil && view != nil {
			view.permissions[key] = permissions
		}
	}
	if errors.Is(err, creation.ErrPermissionDenied) {
		return BPMNTaskAssignment{}, denyBPMNTaskAccess("active bound task role and permissions required")
	}
	if err != nil {
		return BPMNTaskAssignment{}, err
	}
	if !authorization.CheckPermissionMatch(permissions, "task", taskAction) ||
		!authorization.CheckPermissionMatch(permissions, policy.Resource, "read") {
		return BPMNTaskAssignment{}, denyBPMNTaskAccess("insufficient bound task read permission")
	}
	if command != "" && !authorization.CheckPermissionMatch(permissions, policy.Resource, policy.FulfillmentAction()) {
		return BPMNTaskAssignment{}, denyBPMNTaskAccess("insufficient professional fulfillment permission")
	}
	assignment, err := e.resolveTaskAssignment(ctx, client, task)
	if err != nil {
		return BPMNTaskAssignment{}, err
	}
	if command == BPMNTaskCommandComplete && assignment.State != "assigned" {
		return BPMNTaskAssignment{}, denyBPMNTaskAccess("bound task has no available responsible user")
	}
	if !elevated && assignment.ResponsibleUserID != scope.UserID {
		return BPMNTaskAssignment{}, denyBPMNTaskAccess("actor is not the current bound task participant")
	}
	return assignment, nil
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

// GetTaskView owns the entire detail response snapshot, including fresh entity,
// authorization, assignment history, directory identity and offered actions.
func (s *bpmnTaskService) GetTaskView(ctx context.Context, reference string) (*dto.BPMNTaskResponse, error) {
	key := &ent.ProcessTask{TaskID: reference}
	if id, err := strconv.Atoi(reference); err == nil {
		key.ID = id
		key.TaskID = ""
	}
	return s.ProjectTaskView(ctx, key)
}

// ProjectTaskView treats its argument only as an identity. Reloading inside RR
// prevents an old entity supplied by a caller from mixing pre/post-terminal facts.
func (s *bpmnTaskService) ProjectTaskView(ctx context.Context, reference *ent.ProcessTask) (*dto.BPMNTaskResponse, error) {
	if reference == nil {
		return nil, common.NewNotFoundError("process task")
	}
	if taskReadSnapshot(ctx, s.client) == nil {
		var result *dto.BPMNTaskResponse
		err := s.engine.withTaskReadSnapshot(ctx, func(ctx context.Context, e *CustomProcessEngine) error {
			var err error
			result, err = e.taskService.ProjectTaskView(ctx, reference)
			return err
		})
		if err != nil {
			return nil, err
		}
		return result, nil
	}
	scope, err := BPMNAccessScopeFromContext(ctx)
	if err != nil {
		return nil, err
	}
	query := s.client.ProcessTask.Query().Where(processtask.TenantID(scope.TenantID))
	if reference.ID > 0 {
		query.Where(processtask.ID(reference.ID))
	} else {
		query.Where(processtask.TaskID(reference.TaskID))
	}
	task, err := query.Only(ctx)
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
	blocks, err := loadTaskCallbackBlocks(ctx, s.client, []*ent.ProcessTask{task})
	if err != nil {
		return nil, err
	}
	result := dto.ToBPMNTaskResponse(task, instance)
	result.CallbackBlock = blocks[task.ID]
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
