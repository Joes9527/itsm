package service

import (
	"context"
	"fmt"
	"testing"

	"github.com/stretchr/testify/require"
	"itsm-backend/common"
	"itsm-backend/ent"
)

func TestBPMNTaskUIActionsReuseTaskAuthority(t *testing.T) {
	f := newBPMNAuthorizationFixture(t)
	task := f.seedNonParticipantApprovalTask(t, "ui-actions")
	task = f.client.ProcessTask.UpdateOne(task).SetCandidateUsers(f.actor.Email).SetTaskType("user_task").SetTaskVariables(map[string]interface{}{}).SetStatus(common.ProcessTaskStatusCreated).ClearAssignee().SaveX(f.userCtx)
	ctx := f.typedTaskScopeOnlyCtx(f.actor, false)
	actions := f.engine.taskUIActions(ctx, task)
	require.True(t, actions.Claim)
	require.True(t, actions.Complete)
	require.False(t, f.engine.taskUIActions(f.typedTaskScopeOnlyCtx(f.outsider, false), task).Complete)
	task.Assignee = "already-assigned"
	require.False(t, f.engine.taskUIActions(ctx, task).Claim)
	task.Assignee = ""
	task.FormKey = "required-form"
	require.False(t, f.engine.taskUIActions(ctx, task).Complete)
	task.FormKey = ""
	for _, purpose := range []string{"approval", "unsupported"} {
		task.TaskVariables = map[string]interface{}{"taskPurpose": purpose}
		require.False(t, f.engine.taskUIActions(ctx, task).Complete)
	}
	task.TaskVariables = map[string]interface{}{}
	for _, kind := range []string{"kaf_delegate", "unsupported"} {
		task.TaskType = kind
		require.False(t, f.engine.taskUIActions(ctx, task).Claim)
		require.False(t, f.engine.taskUIActions(ctx, task).Complete)
	}
	task.TaskType = "user_task"
	task.Status = common.ProcessTaskStatusCompleted
	require.False(t, f.engine.taskUIActions(ctx, task).Complete)
}

func TestBPMNTaskUIProjectionDoesNotCacheMutationAuthority(t *testing.T) {
	f := newBPMNAuthorizationFixture(t)
	task := f.seedNonParticipantApprovalTask(t, "ui-projection-revoke")
	task = f.client.ProcessTask.UpdateOne(task).SetCandidateUsers(f.actor.Email).SetTaskType("user_task").SetTaskVariables(map[string]interface{}{}).SetStatus(common.ProcessTaskStatusCreated).ClearAssignee().SaveX(f.userCtx)
	ctx := f.typedTaskScopeOnlyCtx(f.actor, false)
	projected, err := f.engine.taskUIReadProjection(ctx)
	require.NoError(t, err)
	require.NotSame(t, f.engine, projected)
	require.True(t, projected.taskUIActions(ctx, task).Claim)
	f.client.User.UpdateOne(f.actor).SetActive(false).SaveX(f.userCtx)
	require.Error(t, f.engine.TaskService().ClaimTaskByID(ctx, task.ID, f.actor.ID))
	_, err = f.engine.taskUIReadProjection(ctx)
	require.Error(t, err)
}

type unavailableTaskUIDirectory struct{ calls int }

func (d *unavailableTaskUIDirectory) Open(context.Context, *ent.Tx, int) (*ent.Client, func() error, error) {
	d.calls++
	return nil, nil, fmt.Errorf("directory unavailable")
}

func TestBPMNTaskUIProjectionPreservesDirectoryAuthority(t *testing.T) {
	f := newBPMNAuthorizationFixture(t)
	directory := &unavailableTaskUIDirectory{}
	tx, err := f.client.Tx(f.userCtx)
	require.NoError(t, err)
	defer tx.Rollback()
	f.engine.participationResolver.directory = directory
	f.engine.participationResolver.owningTx = tx
	f.engine.participationResolver.client = tx.Client()
	_, err = f.engine.taskUIReadProjection(f.typedTaskScopeOnlyCtx(f.actor, false))
	require.Error(t, err, "UI projection must not bypass an unavailable authoritative directory")
	require.Greater(t, directory.calls, 0)
	require.Nil(t, f.engine.participationResolver.readActor)
}
