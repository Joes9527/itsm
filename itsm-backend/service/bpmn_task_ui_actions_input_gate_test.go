package service

import (
	"context"
	"testing"

	"github.com/stretchr/testify/require"
	"itsm-backend/common"
	"itsm-backend/ent"
)

// seedTaskWithDeclaredCallback seeds a simple-UI fulfilment task that carries an
// already persisted callback descriptor: the state the Dev incident reached
// (task33 / callback2).
func seedTaskWithDeclaredCallback(t *testing.T, f *bpmnAuthorizationFixture, handlerID, taskType, action string) (*ent.ProcessTask, context.Context) {
	t.Helper()
	task := f.seedNonParticipantApprovalTask(t, "ui-actions-"+action)
	updated := f.client.ProcessTask.UpdateOne(task).
		SetCandidateUsers(f.actor.Email).
		SetTaskType("user_task").
		SetTaskVariables(map[string]interface{}{}).
		SetStatus(common.ProcessTaskStatusCreated).
		ClearAssignee().
		SetCallbackHandlerID(handlerID).
		SetCallbackTaskType(taskType).
		SetCallbackAction(action).
		SaveX(context.Background())
	return updated, f.typedTaskScopeOnlyCtx(f.actor, false)
}

// The simple entry has no form, so a task whose declared action binds actor
// input that the fixed configuration cannot supply must not offer Complete.
func TestBPMNTaskUIActionsWithholdCompleteForActorBoundCallbackInput(t *testing.T) {
	f := newBPMNAuthorizationFixture(t)
	task, ctx := seedTaskWithDeclaredCallback(t, f, "ticket_service_handler", "ticket_task", "assign")

	actions := f.engine.taskUIActions(ctx, task)

	require.False(t, actions.Complete, "assign binds assignee_id and the simple entry cannot supply it")
	require.NotEmpty(t, actions.Reason, "the operator must see an actionable reason")
}

// An action that keeps a fixed fallback must stay completable: update_status
// defaults a missing new_status to in_progress, so it must not be mis-disabled.
func TestBPMNTaskUIActionsKeepCompletableWhenFixedConfigurationSatisfiesInput(t *testing.T) {
	f := newBPMNAuthorizationFixture(t)
	task, ctx := seedTaskWithDeclaredCallback(t, f, "ticket_service_handler", "ticket_task", "update_status")

	actions := f.engine.taskUIActions(ctx, task)

	require.True(t, actions.Complete, "update_status owns an in_progress fallback")
}

// A task with no declared action keeps the existing authority-only behaviour.
func TestBPMNTaskUIActionsKeepCompletableWithoutDeclaredCallback(t *testing.T) {
	f := newBPMNAuthorizationFixture(t)
	task := f.seedNonParticipantApprovalTask(t, "ui-actions-no-callback")
	task = f.client.ProcessTask.UpdateOne(task).
		SetCandidateUsers(f.actor.Email).
		SetTaskType("user_task").
		SetTaskVariables(map[string]interface{}{}).
		SetStatus(common.ProcessTaskStatusCreated).
		ClearAssignee().
		SaveX(context.Background())

	actions := f.engine.taskUIActions(f.typedTaskScopeOnlyCtx(f.actor, false), task)

	require.True(t, actions.Complete, "a task without a callback has nothing the simple entry cannot supply")
}

// An unresolvable handler is a definition defect: the simple entry must fail
// closed instead of offering a completion that cannot be validated.
func TestBPMNTaskUIActionsWithholdCompleteForUnresolvableCallback(t *testing.T) {
	f := newBPMNAuthorizationFixture(t)
	task, ctx := seedTaskWithDeclaredCallback(t, f, "unknown_handler", "unknown_task", "assign")

	actions := f.engine.taskUIActions(ctx, task)

	require.False(t, actions.Complete)
	require.NotEmpty(t, actions.Reason)
}

// A historical task without a persisted descriptor must be read without writing
// one: the projection may not repair the row it is reporting on.
func TestBPMNTaskUIActionsProjectionDoesNotPersistDescriptor(t *testing.T) {
	f := newBPMNAuthorizationFixture(t)
	task := f.seedNonParticipantApprovalTask(t, "ui-actions-read-only")
	task = f.client.ProcessTask.UpdateOne(task).
		SetCandidateUsers(f.actor.Email).
		SetTaskType("user_task").
		SetTaskVariables(map[string]interface{}{}).
		SetStatus(common.ProcessTaskStatusCreated).
		ClearAssignee().
		SaveX(context.Background())
	ctx := f.typedTaskScopeOnlyCtx(f.actor, false)
	before := f.client.ProcessTask.GetX(ctx, task.ID)

	require.True(t, f.engine.taskUIActions(ctx, task).Complete)

	after := f.client.ProcessTask.GetX(ctx, task.ID)
	require.Empty(t, after.CallbackHandlerID, "the projection must not resolve and persist a descriptor")
	require.Equal(t, before.CallbackHandlerID, after.CallbackHandlerID)
	require.Equal(t, before.CallbackAction, after.CallbackAction)
	require.Equal(t, before.CallbackTaskType, after.CallbackTaskType)
	require.Equal(t, before.UpdatedAt, after.UpdatedAt, "the projection must not update the task row")
	require.Equal(t, before.AggregationVersion, after.AggregationVersion)
}
