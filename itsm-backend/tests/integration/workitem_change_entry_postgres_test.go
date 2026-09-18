//go:build integration_postgres

package integration

import (
	"context"
	"database/sql"
	"fmt"
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
	"itsm-backend/authorization"
	"itsm-backend/ent"
	"itsm-backend/ent/processauditlog"
	"itsm-backend/ent/processtask"
	"itsm-backend/service"
)

func TestWorkItemChangeLifecycleMSPStartEntry(t *testing.T) {
	f := newChangeLifecycleFixture(t, "normal")
	weak, err := f.runtime.BeginTx(f.ctx, &sql.TxOptions{Isolation: sql.LevelReadCommitted})
	require.NoError(t, err)
	_, err = f.engine.StartProcessTx(changeCallbackContext(f, f.actor), weak, "change_normal_flow", "weak-start", "change", f.c.WorkItemID, nil)
	require.ErrorContains(t, err, "repeatable read")
	require.NoError(t, weak.Rollback())
	require.Zero(t, f.client.ProcessInstance.Query().CountX(f.ctx))
	f.client.Tenant.UpdateOneID(f.tenant.ID).SetType("msp_customer").ExecX(f.ctx)
	provider := f.client.Tenant.Create().SetCode("entry-provider").SetName("Provider").SetType("msp_provider").SaveX(f.ctx)
	actor := f.client.User.Create().SetTenantID(provider.ID).SetUsername("provider").SetName("Provider").SetEmail("provider@example.test").SetPasswordHash("test").SetRole("admin").SetMspRole("provider_agent").SetActive(true).SaveX(f.ctx)
	allocation := f.client.MSPAllocation.Create().SetMspUserID(actor.ID).SetCustomerTenantID(f.tenant.ID).SetRole("primary").SaveX(f.ctx)
	role := f.client.Role.Create().SetTenantID(f.tenant.ID).SetCode("msp_tech").SetName("MSP technician").SetIsActive(true).SaveX(f.ctx)
	permission := f.client.Permission.Create().SetTenantID(f.tenant.ID).SetCode("change:write").SetName("Change write").SetResource("change").SetAction("write").SaveX(f.ctx)
	f.client.RolePermission.Create().SetTenantID(f.tenant.ID).SetRoleID(role.ID).SetPermissionID(permission.ID).SaveX(f.ctx)
	authorization.InvalidateAllPermissionCaches()
	t.Cleanup(authorization.InvalidateAllPermissionCaches)
	f.actor = actor
	result, err := f.owner.ApplyCommand(f.ctx, f.command("submit", "msp-submit"))
	require.NoError(t, err)
	require.Equal(t, 2, result.Version)
	instance := f.client.ProcessInstance.Query().OnlyX(f.ctx)
	require.Equal(t, fmt.Sprint(actor.ID), instance.Initiator)
	require.Equal(t, f.tenant.ID, instance.TenantID)
	require.Equal(t, "created", f.client.ProcessTask.Query().OnlyX(f.ctx).Status)
	allocation.Update().SetDeassignedAt(time.Now()).ExecX(f.ctx)
	_, err = f.engine.StartProcess(changeCallbackContext(f, actor), "change_normal_flow", "revoked-start", "change", f.c.WorkItemID, nil)
	require.Error(t, err)
	require.Equal(t, 1, f.client.ProcessInstance.Query().CountX(f.ctx))
}

func TestWorkItemChangeLifecycleConcurrentVoteEntry(t *testing.T) {
	f := newChangeLifecycleFixture(t, "normal")
	f.apply(t, f.command("submit", "vote-submit"))
	parent := f.client.ProcessTask.Query().OnlyX(f.ctx)
	parent.Update().SetAssignee(fmt.Sprint(f.actor.ID)).ExecX(f.ctx)
	other := f.client.User.Create().SetTenantID(f.tenant.ID).SetUsername("second-voter").SetName("Second voter").SetEmail("voter@example.test").SetPasswordHash("test").SetRole("super_admin").SetActive(true).SaveX(f.ctx)
	children, err := f.engine.TaskService().CreateCounterSignTasks(changeCallbackContext(f, f.actor), parent.TaskID, &service.CounterSignRequest{Approvers: []string{fmt.Sprint(f.actor.ID), fmt.Sprint(other.ID)}, ApprovalType: "parallel", Threshold: 2})
	require.NoError(t, err)
	require.Len(t, children, 2)
	parent = f.client.ProcessTask.GetX(f.ctx, parent.ID)
	initialVersion := parent.AggregationVersion
	arrived := make(chan struct{}, 2)
	release := make(chan struct{})
	once := map[int]*sync.Once{children[0].ID: {}, children[1].ID: {}}
	f.runtime.ProcessTask.Intercept(ent.InterceptFunc(func(next ent.Querier) ent.Querier {
		return ent.QuerierFunc(func(ctx context.Context, q ent.Query) (ent.Value, error) {
			v, err := next.Query(ctx, q)
			if err == nil {
				if tasks, ok := v.([]*ent.ProcessTask); ok && len(tasks) == 1 {
					if barrier := once[tasks[0].ID]; barrier != nil {
						barrier.Do(func() {
							arrived <- struct{}{}
							select {
							case <-release:
							case <-ctx.Done():
							}
						})
					}
				}
			}
			return v, err
		})
	}))
	ctx, cancel := context.WithTimeout(f.ctx, 20*time.Second)
	defer cancel()
	results := make(chan error, 2)
	for i, actor := range []*ent.User{f.actor, other} {
		go func(index int, actor *ent.User) {
			scope := service.WithBPMNAccessScope(ctx, service.BPMNAccessScope{TenantID: f.tenant.ID, UserID: actor.ID})
			results <- f.engine.TaskService().Vote(scope, children[index].TaskID, &service.VoteRequest{Approved: true, Comment: "verified"})
		}(i, actor)
	}
	for range 2 {
		select {
		case <-arrived:
		case <-ctx.Done():
			require.FailNow(t, "vote barrier timed out")
		}
	}
	close(release)
	for range 2 {
		require.NoError(t, <-results)
	}
	persisted := f.client.ProcessTask.GetX(f.ctx, parent.ID)
	require.Equal(t, "completed", persisted.Status)
	require.Equal(t, initialVersion+3, persisted.AggregationVersion, "two accepted votes and one final transition")
	require.Equal(t, 2, f.client.ProcessTask.Query().Where(processtask.ParentTaskID(parent.TaskID), processtask.Status("completed")).CountX(f.ctx))
	require.Equal(t, 1, f.client.ProcessAuditLog.Query().Where(processauditlog.ActivityID(parent.TaskDefinitionKey), processauditlog.Action(service.AuditActionTaskCompleted)).CountX(f.ctx))
	require.Error(t, f.engine.TaskService().Vote(changeCallbackContext(f, f.actor), children[0].TaskID, &service.VoteRequest{Approved: true}))
	require.Equal(t, persisted.AggregationVersion, f.client.ProcessTask.GetX(f.ctx, parent.ID).AggregationVersion)
}

func TestWorkItemChangeLifecycleMSPTaskEntry(t *testing.T) {
	for _, mode := range []string{"assigned", "group", "foreign_group", "role", "revoked", "unallocated", "forged", "privileged", "revoked_permission", "claim", "assign", "delegate", "cancel", "variables", "counter_sign", "vote", "rollback", "weak_tx"} {
		t.Run(mode, func(t *testing.T) {
			f := newChangeLifecycleFixture(t, "normal")
			f.apply(t, f.command("submit", "native-submit"))
			task := f.client.ProcessTask.Query().OnlyX(f.ctx)
			f.client.Tenant.UpdateOneID(f.tenant.ID).SetType("msp_customer").ExecX(f.ctx)
			provider := f.client.Tenant.Create().SetCode("task-provider").SetName("Provider").SetType("msp_provider").SaveX(f.ctx)
			actor := f.client.User.Create().SetTenantID(provider.ID).SetUsername("task-provider").SetName("Provider").SetEmail("task-provider@example.test").SetPasswordHash("test").SetRole("admin").SetMspRole("provider_agent").SetActive(true).SaveX(f.ctx)
			allocation := f.client.MSPAllocation.Create().SetMspUserID(actor.ID).SetCustomerTenantID(f.tenant.ID).SetRole("primary").SaveX(f.ctx)
			task.Update().SetAssignee(fmt.Sprint(actor.ID)).ExecX(f.ctx)
			if mode == "group" || mode == "foreign_group" {
				tenantID := f.tenant.ID
				if mode == "foreign_group" {
					tenantID = provider.ID
				}
				f.client.Group.Create().SetTenantID(tenantID).SetName("Target CAB").AddMemberIDs(actor.ID).SaveX(f.ctx)
				task.Update().SetAssignee("").SetCandidateGroups("Target CAB").ExecX(f.ctx)
			}
			if mode == "role" {
				role := f.client.Role.Create().SetTenantID(f.tenant.ID).SetCode("task_reviewer").SetName("Reviewer").SetIsActive(true).AddUserIDs(actor.ID).SaveX(f.ctx)
				task.Update().SetAssignee("").SetCandidateGroups(role.Code).ExecX(f.ctx)
			}
			if mode == "revoked" {
				allocation.Update().SetDeassignedAt(time.Now()).ExecX(f.ctx)
			}
			if mode == "unallocated" {
				f.client.MSPAllocation.DeleteOneID(allocation.ID).ExecX(f.ctx)
			}
			if mode == "forged" {
				actor.Update().ClearMspRole().ExecX(f.ctx)
			}
			privileged := mode == "privileged" || mode == "revoked_permission"
			if privileged {
				task.Update().SetAssignee("999999").ExecX(f.ctx)
				role := f.client.Role.Create().SetTenantID(f.tenant.ID).SetCode("msp_tech").SetName("MSP technician").SetIsActive(true).SaveX(f.ctx)
				permission := f.client.Permission.Create().SetTenantID(f.tenant.ID).SetCode("task:update").SetName("Task update").SetResource("task").SetAction("update").SaveX(f.ctx)
				link := f.client.RolePermission.Create().SetTenantID(f.tenant.ID).SetRoleID(role.ID).SetPermissionID(permission.ID).SaveX(f.ctx)
				if mode == "revoked_permission" {
					f.client.RolePermission.DeleteOneID(link.ID).ExecX(f.ctx)
				}
			}
			ctx := service.WithBPMNAccessScope(f.ctx, service.BPMNAccessScope{UserID: actor.ID, TenantID: f.tenant.ID, CanUpdateAllTasks: privileged})
			if mode == "weak_tx" || mode == "rollback" {
				isolation := sql.LevelReadCommitted
				if mode == "rollback" {
					isolation = sql.LevelRepeatableRead
				}
				tx, err := f.runtime.BeginTx(ctx, &sql.TxOptions{Isolation: isolation})
				require.NoError(t, err)
				defer tx.Rollback()
				err = f.engine.CompleteTaskTx(ctx, tx, task.TaskID, nil)
				if mode == "weak_tx" {
					require.ErrorContains(t, err, "repeatable read")
				} else {
					require.NoError(t, err)
				}
				require.NoError(t, tx.Rollback())
				require.Equal(t, "created", f.client.ProcessTask.GetX(f.ctx, task.ID).Status)
				require.Zero(t, f.client.ProcessAuditLog.Query().Where(processauditlog.UserID(actor.ID)).CountX(f.ctx))
				return
			}
			var mutationErr error
			switch mode {
			case "claim":
				task.Update().SetAssignee("").SetCandidateUsers(fmt.Sprint(actor.ID)).ExecX(f.ctx)
				mutationErr = f.engine.TaskService().ClaimTask(ctx, task.TaskID, fmt.Sprint(actor.ID))
			case "assign":
				mutationErr = f.engine.TaskService().AssignTask(ctx, task.TaskID, fmt.Sprint(f.actor.ID))
			case "delegate":
				mutationErr = f.engine.TaskService().DelegateTask(ctx, task.TaskID, fmt.Sprint(f.actor.ID))
			case "cancel":
				mutationErr = f.engine.TaskService().CancelTask(ctx, task.TaskID, "cancel task")
			case "variables":
				mutationErr = f.engine.TaskService().SetTaskVariables(ctx, task.TaskID, map[string]interface{}{"note": "updated"})
			case "counter_sign":
				_, mutationErr = f.engine.TaskService().CreateCounterSignTasks(ctx, task.TaskID, &service.CounterSignRequest{Approvers: []string{fmt.Sprint(f.actor.ID)}, ApprovalType: "parallel", Threshold: 1})
			case "vote":
				task.Update().SetStatus("assigned").ExecX(f.ctx)
				mutationErr = f.engine.TaskService().Vote(ctx, task.TaskID, &service.VoteRequest{Approved: true, Comment: "reviewed"})
			}
			switch mode {
			case "claim", "assign", "delegate", "cancel", "variables", "counter_sign", "vote":
				require.NoError(t, mutationErr)
				persisted := f.client.ProcessTask.GetX(f.ctx, task.ID)
				switch mode {
				case "claim":
					require.Equal(t, fmt.Sprint(actor.ID), persisted.Assignee)
					require.Equal(t, "assigned", persisted.Status)
				case "assign", "delegate":
					require.Equal(t, fmt.Sprint(f.actor.ID), persisted.Assignee)
				case "cancel":
					require.Equal(t, "cancelled", persisted.Status)
				case "variables":
					require.Equal(t, "updated", persisted.TaskVariables["note"])
				case "counter_sign":
					require.Equal(t, 1, f.client.ProcessTask.Query().Where(processtask.ParentTaskID(task.TaskID)).CountX(f.ctx))
				case "vote":
					require.Equal(t, "completed", persisted.Status)
				}
				require.Positive(t, f.client.ProcessAuditLog.Query().Where(processauditlog.UserID(actor.ID), processauditlog.TenantID(f.tenant.ID)).CountX(f.ctx))
				return
			}
			err := f.engine.CompleteTask(ctx, task.TaskID, nil)
			denied := mode == "foreign_group" || mode == "revoked" || mode == "unallocated" || mode == "forged" || mode == "revoked_permission"
			if denied {
				require.Error(t, err)
				require.Equal(t, "created", f.client.ProcessTask.GetX(f.ctx, task.ID).Status)
				require.Zero(t, f.client.ProcessAuditLog.Query().Where(processauditlog.UserID(actor.ID)).CountX(f.ctx))
				return
			}
			require.NoError(t, err)
			require.Equal(t, "completed", f.client.ProcessTask.GetX(f.ctx, task.ID).Status)
			require.Positive(t, f.client.ProcessAuditLog.Query().Where(processauditlog.UserID(actor.ID), processauditlog.TenantID(f.tenant.ID)).CountX(f.ctx))
			require.Error(t, f.engine.CompleteTask(ctx, task.TaskID, nil))
			require.Zero(t, f.client.ProcessCallbackOutbox.Query().CountX(f.ctx))
		})
	}
}
