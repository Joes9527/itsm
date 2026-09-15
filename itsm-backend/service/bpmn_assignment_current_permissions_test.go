package service

import (
	"testing"

	"github.com/stretchr/testify/require"
	"itsm-backend/authorization"
	"itsm-backend/common"
)

func TestBPMNBoundPermissionRevocation(t *testing.T) {
	for _, change := range []string{"grants", "inactive-role"} {
		t.Run(change, func(t *testing.T) {
			f := newBPMNAuthorizationFixture(t)
			_, task := seedBoundAssignment(t, f, "review-rbac-"+change)
			grantBoundPermissions(t, f, f.actor, "service_request", "read", "service_request", "provision", "task", "read", "task", "update")
			old := authorization.PermissionConfig
			authorization.PermissionConfig.EnableCache = true
			t.Cleanup(func() { authorization.PermissionConfig = old })
			ctx := f.typedTaskScopeOnlyCtx(f.actor, false)
			require.NoError(t, f.engine.TaskService().SetTaskVariables(ctx, task.TaskID, map[string]interface{}{"reviewMemo": "before"}))
			if change == "grants" {
				f.client.RolePermission.Delete().ExecX(f.userCtx)
			} else {
				f.client.Role.Update().SetIsActive(false).ExecX(f.userCtx)
			}
			err := f.engine.TaskService().SetTaskVariables(ctx, task.TaskID, map[string]interface{}{"reviewMemo": "after revoked"})
			require.Error(t, err, "revoked or inactive role must not execute bound task mutation")
		})
	}
}

func TestBPMNDetailReloadsTerminalEntityWithinSnapshot(t *testing.T) {
	f := newBPMNAuthorizationFixture(t)
	item, stale := seedBoundAssignment(t, f, "detail-stale")
	grantBoundPermissions(t, f, f.actor, "service_request", "read", "service_request", "provision", "task", "read", "task", "update")
	f.client.Ticket.UpdateOne(item).SetRequesterID(f.actor.ID).SetAssigneeID(f.outsider.ID).ExecX(f.userCtx)
	terminal := f.client.ProcessTask.UpdateOne(stale).SetStatus(common.ProcessTaskStatusCompleted).SetAggregationVersion(2).SaveX(f.userCtx)
	instance := f.client.ProcessInstance.GetX(f.userCtx, terminal.ProcessInstanceID)
	f.client.ProcessAuditLog.Create().SetProcessInstanceID(instance.ID).SetProcessInstanceKey(instance.ProcessInstanceID).SetProcessDefinitionID(instance.ProcessDefinitionID).SetProcessDefinitionKey(instance.ProcessDefinitionKey).SetActivityID(terminal.TaskDefinitionKey).SetActivityType(ActivityTypeUserTask).SetAction(AuditActionTaskCompleted).SetTenantID(terminal.TenantID).SetAssigneeID(f.actor.ID).SetUserID(f.outsider.ID).SetMetadata(map[string]interface{}{"taskId": terminal.ID, "taskVersion": terminal.AggregationVersion, "terminalStatus": "completed", "assigneeSource": BPMNAssigneeSourceWorkItem}).SaveX(f.userCtx)
	ctx := WithBPMNAccessScope(f.userCtx, BPMNAccessScope{UserID: f.actor.ID, TenantID: f.tenant.ID, CanReadAllTasks: true})
	view, err := f.engine.taskService.ProjectTaskView(ctx, stale)
	require.NoError(t, err)
	require.Equal(t, "completed", view.Status)
	require.Equal(t, "terminal", view.AssignmentState)
	require.Equal(t, f.actor.ID, view.ResponsibleUserID)
	require.Equal(t, f.outsider.ID, view.ActorID)
	require.False(t, view.UIActions.Complete)
	require.False(t, view.UIActions.Claim)
	direct, err := f.engine.TaskService().GetTaskView(ctx, stale.TaskID)
	require.NoError(t, err)
	require.Equal(t, view.Status, direct.Status)
	require.Equal(t, view.ResponsibleUserID, direct.ResponsibleUserID)
}
