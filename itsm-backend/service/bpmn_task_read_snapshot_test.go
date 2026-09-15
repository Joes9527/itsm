package service

import (
	"encoding/json"
	"testing"

	"github.com/stretchr/testify/require"
	"itsm-backend/ent/processauditlog"
)

func TestBoundLifecycleCancellationFreezesResponsibility(t *testing.T) {
	f := newBPMNAuthorizationFixture(t)
	item, task := seedBoundAssignment(t, f, "terminal-cancel")
	grantBoundPermissions(t, f, f.outsider, "service_request", "read", "service_request", "provision", "task", "read", "task", "update")
	ctx := WithBPMNAccessScope(f.userCtx, BPMNAccessScope{UserID: f.outsider.ID, TenantID: f.tenant.ID, CanUpdateAllTasks: true, CanReadAllTasks: true})
	require.NoError(t, f.engine.TaskService().CancelTask(ctx, task.TaskID, "administrative cancellation"))
	saved := f.client.ProcessTask.GetX(f.userCtx, task.ID)
	require.Empty(t, saved.Assignee)
	audit := f.client.ProcessAuditLog.Query().Where(processauditlog.Action(AuditActionTaskCancelled)).OnlyX(f.userCtx)
	require.Equal(t, f.actor.ID, audit.AssigneeID)
	require.Equal(t, f.outsider.ID, audit.UserID)
	require.Equal(t, ActivityTypeUserTask, audit.ActivityType)
	require.Equal(t, "bpmn_task_cancel", audit.Metadata["source"])
	require.EqualValues(t, saved.AggregationVersion, audit.Metadata["taskVersion"])
	require.EqualValues(t, saved.ID, audit.Metadata["taskId"])
	f.client.Ticket.UpdateOne(item).SetAssigneeID(f.outsider.ID).SaveX(f.userCtx)
	projection, err := f.engine.resolveTaskAssignment(f.userCtx, f.client, saved)
	require.NoError(t, err)
	require.Equal(t, "terminal", projection.State)
	require.Equal(t, f.actor.ID, projection.ResponsibleUserID)
	require.Equal(t, f.outsider.ID, projection.ActorID)
	view, err := f.engine.TaskService().ProjectTaskView(ctx, saved)
	require.NoError(t, err)
	raw, err := json.Marshal(view)
	require.NoError(t, err)
	var fields map[string]interface{}
	require.NoError(t, json.Unmarshal(raw, &fields))
	require.EqualValues(t, f.actor.ID, fields["responsibleUserId"])
	require.EqualValues(t, f.outsider.ID, fields["actorId"])
}

func TestBoundLifecycleUnassignedIsWaiting(t *testing.T) {
	f := newBPMNAuthorizationFixture(t)
	item, task := seedBoundAssignment(t, f, "waiting")
	f.client.Ticket.UpdateOne(item).ClearAssigneeID().SaveX(f.userCtx)
	projection, err := f.engine.resolveTaskAssignment(f.userCtx, f.client, task)
	require.NoError(t, err)
	require.Equal(t, "unassigned", projection.State)
	require.False(t, f.engine.taskUIActions(f.typedTaskScopeOnlyCtx(f.actor, true), task).Claim)
}

func TestBoundLifecycleOwnerlessCancellationStillTerminal(t *testing.T) {
	f := newBPMNAuthorizationFixture(t)
	item, task := seedBoundAssignment(t, f, "ownerless")
	f.client.Ticket.UpdateOne(item).ClearAssigneeID().SaveX(f.userCtx)
	grantBoundPermissions(t, f, f.outsider, "service_request", "read", "service_request", "provision", "task", "read", "task", "update")
	ctx := WithBPMNAccessScope(f.userCtx, BPMNAccessScope{UserID: f.outsider.ID, TenantID: f.tenant.ID, CanReadAllTasks: true, CanUpdateAllTasks: true})
	require.NoError(t, f.engine.TaskService().CancelTask(ctx, task.TaskID, "no owner"))
	task = f.client.ProcessTask.GetX(f.userCtx, task.ID)
	projection, err := f.engine.resolveTaskAssignment(f.userCtx, f.client, task)
	require.NoError(t, err)
	require.Equal(t, "terminal", projection.State)
	require.Zero(t, projection.ResponsibleUserID)
	require.Empty(t, projection.Assignee)
	require.Equal(t, f.outsider.ID, projection.ActorID)
}
