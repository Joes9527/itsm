package service

import (
	"encoding/json"
	"testing"

	"github.com/stretchr/testify/require"
	"go.uber.org/zap"
	"itsm-backend/common"
	"itsm-backend/config"
	"itsm-backend/database"
)

// Removing the projection, bypassing participation, or ignoring lifecycle/form
// restrictions must change the actual list response seen by the workbench.
func TestBPMNTaskViewsProjectCommandAuthority(t *testing.T) {
	for _, tc := range []struct {
		name, status, kind, purpose, form, assignee string
		outsider, admin                             bool
		claim, complete                             bool
	}{
		{name: "candidate", status: "created", kind: "user_task", claim: true, complete: true},
		{name: "outsider-reader", status: "created", kind: "user_task", outsider: true},
		{name: "administrator", status: "created", kind: "user_task", outsider: true, admin: true, claim: true, complete: true},
		{name: "already-assigned", status: "assigned", kind: "user_task", assignee: "someone", complete: true},
		{name: "completed", status: "completed", kind: "user_task"},
		{name: "required-form", status: "created", kind: "user_task", form: "approval-form", claim: true},
		{name: "approval", status: "created", kind: "user_task", purpose: "approval"},
		{name: "unknown-purpose", status: "created", kind: "user_task", purpose: "unknown"},
		{name: "async", status: "created", kind: "kaf_delegate"},
		{name: "unknown-kind", status: "created", kind: "unknown"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			f := newBPMNAuthorizationFixture(t)
			task := f.seedNonParticipantApprovalTask(t, "ui-"+tc.name)
			f.client.ProcessTask.UpdateOne(task).SetCandidateUsers(f.actor.Email).SetTaskType(tc.kind).SetTaskVariables(map[string]interface{}{"taskPurpose": tc.purpose}).SetStatus(tc.status).SetAssignee(tc.assignee).SetFormKey(tc.form).SaveX(f.userCtx)
			actor := f.actor
			if tc.outsider {
				actor = f.outsider
			}
			ctx := WithBPMNAccessScope(f.userCtx, BPMNAccessScope{UserID: actor.ID, TenantID: f.tenant.ID, CanReadAllTasks: true, CanUpdateAllTasks: tc.admin})
			views, total, err := f.engine.TaskService().ListUserTaskViews(ctx, &ListUserTasksRequest{ProcessInstanceID: task.ProcessInstanceID})
			require.NoError(t, err)
			require.Equal(t, 1, total)
			require.Len(t, views, 1)
			raw, err := json.Marshal(views[0])
			require.NoError(t, err)
			var response map[string]interface{}
			require.NoError(t, json.Unmarshal(raw, &response))
			actions, ok := response["uiActions"].(map[string]interface{})
			require.True(t, ok, "task view must expose backend-authorized uiActions")
			require.Equal(t, tc.claim, actions["claim"])
			require.Equal(t, tc.complete, actions["complete"])
		})
	}
}

func TestBPMNTaskViewsDoNotRetainRevokedAuthority(t *testing.T) {
	f := newBPMNAuthorizationFixture(t)
	task := f.seedNonParticipantApprovalTask(t, "ui-revoke")
	task = f.client.ProcessTask.UpdateOne(task).SetCandidateUsers(f.actor.Email).SetTaskType("user_task").SetTaskVariables(map[string]interface{}{}).SetStatus(common.ProcessTaskStatusCreated).ClearAssignee().SaveX(f.userCtx)
	ctx := f.typedTaskScopeOnlyCtx(f.actor, false)
	views, total, err := f.engine.TaskService().ListUserTaskViews(ctx, &ListUserTasksRequest{})
	require.NoError(t, err)
	require.Equal(t, 1, total)
	raw, err := json.Marshal(views)
	require.NoError(t, err)
	require.Contains(t, string(raw), `"claim":true`)
	f.client.User.UpdateOne(f.actor).SetActive(false).SaveX(f.userCtx)
	require.Error(t, f.engine.TaskService().ClaimTaskByID(ctx, task.ID, f.actor.ID))
	_, _, err = f.engine.TaskService().ListUserTaskViews(ctx, &ListUserTasksRequest{})
	require.Error(t, err)
}

func TestBPMNTaskViewsCandidateHistoricalInstanceHasNoActions(t *testing.T) {
	f := newBPMNAuthorizationFixture(t)
	task := f.seedNonParticipantApprovalTask(t, "ui-historical")
	f.client.ProcessTask.UpdateOne(task).SetCandidateUsers(f.actor.Email).SetTaskType("user_task").SetTaskVariables(map[string]interface{}{}).SetStatus("created").ClearAssignee().SaveX(f.userCtx)
	policy, err := database.NewExecutionPolicy(config.ExecutionConfig{Mode: "candidate", DeploymentID: "task-ui", Scopes: []config.ExecutionScopeConfig{{TenantID: f.tenant.ID, ScopeID: "f0a0aa2d-b807-4d65-8433-86822245bdd6"}}})
	require.NoError(t, err)
	engine := NewCustomProcessEngine(f.client, zap.NewNop().Sugar(), policy)
	views, total, err := engine.TaskService().ListUserTaskViews(f.scopedCtx(true, true, true, true), &ListUserTasksRequest{})
	require.NoError(t, err)
	require.Equal(t, 1, total)
	raw, err := json.Marshal(views)
	require.NoError(t, err)
	require.Contains(t, string(raw), `"claim":false`)
	require.Contains(t, string(raw), `"complete":false`)
}
