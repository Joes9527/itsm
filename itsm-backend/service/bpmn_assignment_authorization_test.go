package service

import (
	"context"
	"errors"
	"strconv"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
	"itsm-backend/authorization"
	"itsm-backend/common"
	"itsm-backend/ent"
)

func seedBoundAssignment(t *testing.T, f *bpmnAuthorizationFixture, suffix string) (*ent.Ticket, *ent.ProcessTask) {
	t.Helper()
	item := f.client.Ticket.Create().SetTitle("Bound work").SetDescription("Fixture").SetTicketNumber("BOUND-" + suffix).
		SetRecordClass("service_request_item").SetRequesterID(f.outsider.ID).SetAssigneeID(f.actor.ID).SetTenantID(f.tenant.ID).SaveX(f.userCtx)
	instance := f.client.ProcessInstance.Create().SetProcessInstanceID("bound-" + suffix).SetProcessDefinitionID(f.definition.ID).
		SetProcessDefinitionKey(f.definition.Key).SetBusinessType("service_request").SetBusinessID(item.ID).SetBusinessKey("untrusted:999").
		SetTenantID(f.tenant.ID).SaveX(f.userCtx)
	task := f.client.ProcessTask.Create().SetTaskID("bound-task-" + suffix).SetProcessInstanceID(instance.ID).
		SetProcessDefinitionKey(f.definition.Key).SetTaskDefinitionKey("fulfill").SetTaskName("Fulfill").SetTaskType("user_task").
		SetAssigneeSource("work_item_assignee").SetTaskVariables(map[string]interface{}{"taskPurpose": "fulfillment"}).
		SetStatus(common.ProcessTaskStatusCreated).SetTenantID(f.tenant.ID).SaveX(f.userCtx)
	return item, task
}

func grantBoundPermissions(t *testing.T, f *bpmnAuthorizationFixture, actor *ent.User, grants ...string) {
	t.Helper()
	roleName := "bound-role-" + strconv.Itoa(actor.ID)
	f.client.User.UpdateOne(actor).SetRole(roleName).SaveX(f.userCtx)
	r := f.client.Role.Create().SetCode(roleName).SetName(roleName).SetTenantID(f.tenant.ID).SaveX(f.userCtx)
	for i := 0; i < len(grants); i += 2 {
		p := f.client.Permission.Create().SetCode(roleName + "-" + grants[i] + "-" + grants[i+1]).SetName("permission").
			SetResource(grants[i]).SetAction(grants[i+1]).SetTenantID(f.tenant.ID).SaveX(f.userCtx)
		f.client.RolePermission.Create().SetRoleID(r.ID).SetPermissionID(p.ID).SetTenantID(f.tenant.ID).SaveX(f.userCtx)
	}
	authorization.InvalidateRolePermissionCache(roleName, f.tenant.ID)
	t.Cleanup(func() { authorization.InvalidateRolePermissionCache(roleName, f.tenant.ID) })
}

func TestBPMNBoundAssignmentTracksWorkItemAndFiltersBeforePagination(t *testing.T) {
	f := newBPMNAuthorizationFixture(t)
	item, task := seedBoundAssignment(t, f, "change")
	grantBoundPermissions(t, f, f.actor, "service_request", "read", "service_request", "provision", "task", "read", "task", "update")
	grantBoundPermissions(t, f, f.outsider, "service_request", "read", "service_request", "provision", "task", "read", "task", "update")
	ctxA := f.typedTaskScopeOnlyCtx(f.actor, false)
	ctxB := f.typedTaskScopeOnlyCtx(f.outsider, false)
	before, total, err := f.engine.TaskService().ListUserTaskViews(ctxA, &ListUserTasksRequest{Page: 1, PageSize: 1})
	require.NoError(t, err)
	require.Equal(t, 1, total)
	require.Equal(t, strconv.Itoa(f.actor.ID), before[0].Assignee)
	f.client.Ticket.UpdateOne(item).SetAssigneeID(f.outsider.ID).SaveX(f.userCtx)
	after, total, err := f.engine.TaskService().ListUserTaskViews(ctxA, &ListUserTasksRequest{Page: 1, PageSize: 1})
	require.NoError(t, err)
	require.Zero(t, total)
	require.Empty(t, after)
	elevatedA := WithBPMNAccessScope(f.userCtx, BPMNAccessScope{UserID: f.actor.ID, TenantID: f.tenant.ID, CanReadAllTasks: true})
	elevatedTasks, elevatedTotal, elevatedErr := f.engine.TaskService().ListUserTasks(elevatedA, &ListUserTasksRequest{})
	require.NoError(t, elevatedErr)
	require.Zero(t, elevatedTotal)
	require.Empty(t, elevatedTasks)
	after, total, err = f.engine.TaskService().ListUserTaskViews(ctxB, &ListUserTasksRequest{Page: 1, PageSize: 1})
	require.NoError(t, err)
	require.Equal(t, 1, total)
	require.Equal(t, strconv.Itoa(f.outsider.ID), after[0].Assignee)
	require.Empty(t, f.client.ProcessTask.GetX(f.userCtx, task.ID).Assignee)
	_, err = f.engine.TaskService().GetTask(ctxA, task.TaskID)
	require.Error(t, err)
	projected, err := f.engine.TaskService().GetTaskByID(ctxB, task.ID)
	require.NoError(t, err)
	require.Equal(t, strconv.Itoa(f.outsider.ID), projected.Assignee)
}

func TestBPMNBoundAssignmentRejectsIndependentCommands(t *testing.T) {
	f := newBPMNAuthorizationFixture(t)
	_, task := seedBoundAssignment(t, f, "commands")
	grantBoundPermissions(t, f, f.actor, "service_request", "read", "service_request", "provision", "task", "read", "task", "update")
	ctx := f.typedTaskScopeOnlyCtx(f.actor, true)
	for _, command := range []BPMNTaskCommand{BPMNTaskCommandAssign, BPMNTaskCommandClaim, BPMNTaskCommandDelegate} {
		require.Error(t, f.engine.authorizeTaskCommandActorWithClient(ctx, f.client, task, command), string(command))
	}
	require.False(t, f.engine.taskUIActions(ctx, task).Claim)
}

func TestBPMNBoundAssignmentResolverFailsClosed(t *testing.T) {
	for _, scenario := range []string{"missing", "inactive", "foreign-owner", "foreign-instance", "foreign-item", "missing-item", "wrong-business", "unknown-source", "terminal-no-evidence"} {
		t.Run(scenario, func(t *testing.T) {
			f := newBPMNAuthorizationFixture(t)
			item, task := seedBoundAssignment(t, f, scenario)
			wantError := false
			switch scenario {
			case "missing":
				f.client.Ticket.UpdateOne(item).ClearAssigneeID().SaveX(f.userCtx)
			case "inactive":
				f.client.User.UpdateOne(f.actor).SetActive(false).SaveX(f.userCtx)
			case "foreign-owner":
				f.client.Ticket.UpdateOne(item).SetAssigneeID(f.otherActor.ID).SaveX(f.userCtx)
			case "foreign-instance":
				f.client.ProcessInstance.UpdateOneID(task.ProcessInstanceID).SetTenantID(f.otherTenant.ID).SaveX(f.userCtx)
				wantError = true
			case "foreign-item":
				f.client.Ticket.UpdateOne(item).SetTenantID(f.otherTenant.ID).SaveX(f.userCtx)
				wantError = true
			case "missing-item":
				f.client.ProcessInstance.UpdateOneID(task.ProcessInstanceID).SetBusinessID(item.ID + 999).SaveX(f.userCtx)
				wantError = true
			case "wrong-business":
				f.client.ProcessInstance.UpdateOneID(task.ProcessInstanceID).SetBusinessType("incident").SaveX(f.userCtx)
				wantError = true
			case "unknown-source":
				task.AssigneeSource = "unsupported"
				wantError = true
			case "terminal-no-evidence":
				task.Status = "completed"
			}
			assignment, err := f.engine.resolveTaskAssignment(f.userCtx, f.client, task)
			if wantError {
				require.Error(t, err)
				return
			}
			require.NoError(t, err)
			wantState := "unavailable"
			if scenario == "missing" {
				wantState = "unassigned"
			}
			require.Equal(t, wantState, assignment.State)
			require.Empty(t, assignment.Assignee)
		})
	}
}

func TestBPMNBoundAssignmentAuthorityIntersection(t *testing.T) {
	for _, scenario := range []string{"allowed", "no-professional", "submission-only", "no-bpmn", "no-row", "no-owner", "foreign-tenant", "inactive-actor", "elevated-only"} {
		t.Run(scenario, func(t *testing.T) {
			f := newBPMNAuthorizationFixture(t)
			item, task := seedBoundAssignment(t, f, scenario)
			grants := []string{"service_request", "read", "service_request", "provision", "task", "read", "task", "update"}
			switch scenario {
			case "no-professional", "elevated-only":
				grants = []string{"task", "read", "task", "update"}
			case "submission-only":
				grants = []string{"service_request", "read", "service_request", "write", "task", "read", "task", "update"}
			case "no-bpmn":
				grants = []string{"service_request", "read", "service_request", "provision"}
			}
			grantBoundPermissions(t, f, f.actor, grants...)
			scope := BPMNAccessScope{UserID: f.actor.ID, TenantID: f.tenant.ID, CanReadAllTasks: true, CanUpdateAllTasks: true}
			switch scenario {
			case "no-row":
				f.client.Ticket.UpdateOne(item).SetAssigneeID(f.outsider.ID).SaveX(f.userCtx)
			case "no-owner":
				f.client.Ticket.UpdateOne(item).ClearAssigneeID().SetRequesterID(f.actor.ID).SaveX(f.userCtx)
			case "foreign-tenant":
				scope.TenantID = f.otherTenant.ID
			case "inactive-actor":
				f.client.User.UpdateOne(f.actor).SetActive(false).SaveX(f.userCtx)
			}
			ctx := WithBPMNAccessScope(context.Background(), scope)
			err := f.engine.authorizeTaskCommandActorWithClient(ctx, f.client, task, BPMNTaskCommandComplete)
			if scenario == "allowed" {
				require.NoError(t, err)
			} else {
				require.Error(t, err)
			}
			if scenario != "submission-only" && scenario != "no-owner" {
				_, readErr := f.engine.TaskService().GetTaskByID(ctx, task.ID)
				if scenario == "allowed" {
					require.NoError(t, readErr)
				} else {
					require.Error(t, readErr)
				}
			}
		})
	}
}

func TestBPMNBoundAssignmentTerminalSnapshotIsUniqueAndFrozen(t *testing.T) {
	f := newBPMNAuthorizationFixture(t)
	item, task := seedBoundAssignment(t, f, "terminal")
	task = f.client.ProcessTask.UpdateOne(task).SetStatus("completed").SetAggregationVersion(3).SaveX(f.userCtx)
	instance := f.client.ProcessInstance.GetX(f.userCtx, task.ProcessInstanceID)
	createAudit := func(taskID, version int) {
		f.client.ProcessAuditLog.Create().SetProcessInstanceID(instance.ID).SetProcessInstanceKey(instance.ProcessInstanceID).
			SetProcessDefinitionID(instance.ProcessDefinitionID).SetProcessDefinitionKey(instance.ProcessDefinitionKey).
			SetActivityID(task.TaskDefinitionKey).SetActivityType("userTask").SetAction("completed").SetTenantID(task.TenantID).
			SetAssigneeID(f.actor.ID).SetUserID(f.outsider.ID).
			SetMetadata(map[string]interface{}{"taskId": taskID, "taskVersion": version, "terminalStatus": "completed", "assigneeSource": "work_item_assignee"}).SaveX(f.userCtx)
	}
	createAudit(task.ID+99, 3)
	createAudit(task.ID, 2)
	assignment, err := f.engine.resolveTaskAssignment(f.userCtx, f.client, task)
	require.NoError(t, err)
	require.Equal(t, "unavailable", assignment.State)
	createAudit(task.ID, 3)
	f.client.Ticket.UpdateOne(item).SetAssigneeID(f.outsider.ID).SaveX(f.userCtx)
	f.client.User.UpdateOne(f.actor).SetActive(false).SaveX(f.userCtx)
	assignment, err = f.engine.resolveTaskAssignment(f.userCtx, f.client, task)
	require.NoError(t, err)
	require.Equal(t, strconv.Itoa(f.actor.ID), assignment.Assignee)
	require.Equal(t, f.actor.ID, assignment.ResponsibleUserID)
	require.Equal(t, f.outsider.ID, assignment.ActorID)
	createAudit(task.ID, 3)
	assignment, err = f.engine.resolveTaskAssignment(f.userCtx, f.client, task)
	require.NoError(t, err)
	require.Equal(t, "unavailable", assignment.State)
}

func TestBPMNBoundAssignmentInstanceParticipationFollowsOwner(t *testing.T) {
	f := newBPMNAuthorizationFixture(t)
	item, task := seedBoundAssignment(t, f, "instance")
	grantBoundPermissions(t, f, f.actor, "service_request", "read", "task", "read")
	grantBoundPermissions(t, f, f.outsider, "service_request", "read", "task", "read")
	ctxA := f.typedTaskScopeOnlyCtx(f.actor, false)
	actor, err := f.resolver.resolveActor(ctxA, BPMNAccessScope{UserID: f.actor.ID, TenantID: f.tenant.ID})
	require.NoError(t, err)
	ids, err := f.resolver.participatingInstanceIDs(ctxA, actor)
	require.NoError(t, err)
	require.Contains(t, ids, task.ProcessInstanceID)
	f.client.Ticket.UpdateOne(item).SetAssigneeID(f.outsider.ID).SaveX(f.userCtx)
	ids, err = f.resolver.participatingInstanceIDs(ctxA, actor)
	require.NoError(t, err)
	require.NotContains(t, ids, task.ProcessInstanceID)
}

func TestBPMNBoundAssignmentPageBoundariesAndIdentityFilters(t *testing.T) {
	f := newBPMNAuthorizationFixture(t)
	grantBoundPermissions(t, f, f.actor, "service_request", "read", "service_request", "provision", "task", "read", "task", "update")
	_, first := seedBoundAssignment(t, f, "page1")
	hidden, _ := seedBoundAssignment(t, f, "hidden")
	f.client.Ticket.UpdateOne(hidden).SetAssigneeID(f.outsider.ID).SaveX(f.userCtx)
	_, last := seedBoundAssignment(t, f, "page2")
	ctx := WithBPMNAccessScope(f.userCtx, BPMNAccessScope{UserID: f.actor.ID, TenantID: f.tenant.ID, CanReadAllTasks: true, CanUpdateAllTasks: true})
	for i, want := range []int{last.ID, first.ID, 0} {
		tasks, total, err := f.engine.TaskService().ListUserTasks(ctx, &ListUserTasksRequest{Page: i + 1, PageSize: 1})
		require.NoError(t, err)
		require.Equal(t, 2, total)
		if want == 0 {
			require.Empty(t, tasks)
		} else {
			require.Len(t, tasks, 1)
			require.Equal(t, want, tasks[0].ID)
		}
	}
	for _, identity := range []string{strconv.Itoa(f.actor.ID), f.actor.Username, f.actor.Email} {
		tasks, total, err := f.engine.TaskService().ListUserTasks(ctx, &ListUserTasksRequest{Assignee: identity, Page: 1, PageSize: 1})
		require.NoError(t, err)
		require.Equal(t, 2, total)
		require.Len(t, tasks, 1)
	}
}

func TestBPMNBoundAssignmentPublicCommandsAndInternalBoundary(t *testing.T) {
	f := newBPMNAuthorizationFixture(t)
	_, task := seedBoundAssignment(t, f, "routes")
	grantBoundPermissions(t, f, f.actor, "service_request", "read", "service_request", "provision", "task", "read", "task", "update")
	ctx := f.typedTaskScopeOnlyCtx(f.actor, true)
	require.Error(t, f.engine.TaskService().ClaimTask(ctx, task.TaskID, f.actor.Username))
	require.Error(t, f.engine.TaskService().ClaimTaskByID(ctx, task.ID, f.actor.ID))
	require.Error(t, f.engine.TaskService().AssignTask(ctx, task.TaskID, f.actor.Email))
	require.Error(t, f.engine.TaskService().DelegateTask(ctx, task.TaskID, f.outsider.Username))
	require.Empty(t, f.client.ProcessTask.GetX(f.userCtx, task.ID).Assignee)
	internal := context.WithValue(f.userCtx, bpmnInternalCascadeContextKey{}, bpmnInternalCascadeContext{BPMNInternalCascadeRequest{
		TenantID: task.TenantID, InstanceID: task.ProcessInstanceID, TaskID: task.TaskID, NodeKey: "Activity_Schedule", Source: BPMNInternalSourceChangeCABCascade}})
	task.TaskDefinitionKey = "Activity_Schedule"
	require.Error(t, f.engine.authorizeTaskActor(internal, task))
}

func TestBPMNBoundAssignmentStatisticsUseAuthorizedOwnerProjection(t *testing.T) {
	f := newBPMNAuthorizationFixture(t)
	grantBoundPermissions(t, f, f.actor, "service_request", "read", "task", "read")
	_, visible := seedBoundAssignment(t, f, "stats-visible")
	hidden, _ := seedBoundAssignment(t, f, "stats-hidden")
	f.client.Ticket.UpdateOne(hidden).SetAssigneeID(f.outsider.ID).SaveX(f.userCtx)
	ctx := WithBPMNAccessScope(f.userCtx, BPMNAccessScope{UserID: f.actor.ID, TenantID: f.tenant.ID, CanReadAllTasks: true})
	stats, err := f.engine.TaskService().GetTaskStatistics(ctx, &TaskStatisticsRequest{})
	require.NoError(t, err)
	require.Equal(t, 1, stats.TotalTasks)
	require.Equal(t, 1, stats.AssigneeBreakdown[strconv.Itoa(f.actor.ID)])
	stats, err = f.engine.TaskService().GetTaskStatistics(ctx, &TaskStatisticsRequest{Assignee: f.actor.Username})
	require.NoError(t, err)
	require.Equal(t, 1, stats.TotalTasks)
	_ = visible
}

func TestBPMNBoundAssignmentNumericMutationReturnsForbidden(t *testing.T) {
	f := newBPMNAuthorizationFixture(t)
	_, task := seedBoundAssignment(t, f, "numeric-command")
	grantBoundPermissions(t, f, f.actor, "service_request", "read", "service_request", "provision", "task", "read", "task", "update")
	ctx := f.typedTaskScopeOnlyCtx(f.actor, true)
	for _, invoke := range []func() error{
		func() error { return f.engine.TaskService().AssignTask(ctx, strconv.Itoa(task.ID), f.actor.Username) },
		func() error { return f.engine.TaskService().DelegateTask(ctx, strconv.Itoa(task.ID), f.actor.Username) },
	} {
		var denied *common.AppError
		require.ErrorAs(t, invoke(), &denied)
		require.Equal(t, common.ErrCodeForbidden, denied.Code)
	}
}

func TestBPMNBoundAssignmentCancellationConsumesExistingAuditAction(t *testing.T) {
	f := newBPMNAuthorizationFixture(t)
	_, task := seedBoundAssignment(t, f, "cancelled")
	task = f.client.ProcessTask.UpdateOne(task).SetStatus("cancelled").SetAggregationVersion(2).SaveX(f.userCtx)
	instance := f.client.ProcessInstance.GetX(f.userCtx, task.ProcessInstanceID)
	f.client.ProcessAuditLog.Create().SetProcessInstanceID(instance.ID).SetProcessInstanceKey(instance.ProcessInstanceID).
		SetProcessDefinitionID(instance.ProcessDefinitionID).SetProcessDefinitionKey(instance.ProcessDefinitionKey).
		SetActivityID(task.TaskDefinitionKey).SetActivityType("userTask").SetAction("task_cancelled").SetTenantID(task.TenantID).
		SetAssigneeID(f.actor.ID).SetUserID(f.outsider.ID).
		SetMetadata(map[string]interface{}{"taskId": task.ID, "taskVersion": 2, "terminalStatus": "cancelled", "assigneeSource": "work_item_assignee"}).SaveX(f.userCtx)
	projection, err := f.engine.resolveTaskAssignment(f.userCtx, f.client, task)
	require.NoError(t, err)
	require.Equal(t, strconv.Itoa(f.actor.ID), projection.Assignee)
	require.Equal(t, f.outsider.ID, projection.ActorID)
}

func installBoundAssignmentReadFault(f *bpmnAuthorizationFixture, task *ent.ProcessTask, stage string, fault error) {
	listed := false
	userQueries := 0
	f.client.ProcessTask.Intercept(ent.InterceptFunc(func(next ent.Querier) ent.Querier {
		return ent.QuerierFunc(func(ctx context.Context, q ent.Query) (ent.Value, error) {
			value, err := next.Query(ctx, q)
			if err == nil {
				if rows, ok := value.([]*ent.ProcessTask); ok {
					listed = true
					for _, row := range rows {
						if row.ID == task.ID && stage == "source" {
							row.AssigneeSource = "unsupported_source"
						}
					}
				}
			}
			return value, err
		})
	}))
	fail := ent.InterceptFunc(func(next ent.Querier) ent.Querier {
		return ent.QuerierFunc(func(ctx context.Context, q ent.Query) (ent.Value, error) {
			if listed {
				if stage == "owner" || stage == "identity" {
					userQueries++
					target := 2
					if stage == "identity" {
						target = 3 // actor, authorized owner, then textual identity filter
					}
					if userQueries == target {
						return nil, fault
					}
				} else {
					return nil, fault
				}
			}
			return next.Query(ctx, q)
		})
	})
	switch stage {
	case "actor", "owner", "identity":
		f.client.User.Intercept(fail)
	case "instance":
		f.client.ProcessInstance.Intercept(fail)
	case "workitem":
		f.client.Ticket.Intercept(fail)
	case "audit":
		f.client.ProcessAuditLog.Intercept(fail)
	}
}

func TestBPMNBoundAssignmentListPropagatesResolutionFailure(t *testing.T) {
	for _, api := range []string{"list", "views", "statistics"} {
		for _, stage := range []string{"source", "actor", "owner", "identity", "instance", "workitem", "audit"} {
			for _, failure := range []string{"storage", "cancelled"} {
				t.Run(api+"/"+stage+"/"+failure, func(t *testing.T) {
					f := newBPMNAuthorizationFixture(t)
					grantBoundPermissions(t, f, f.actor, "service_request", "read", "task", "read")
					_, task := seedBoundAssignment(t, f, "read-failure")
					f.seedNonParticipantApprovalTask(t, "earlier-list-row")
					if stage == "audit" {
						task = f.client.ProcessTask.UpdateOne(task).SetStatus("completed").SaveX(f.userCtx)
					}
					fault := errors.New("owned fixture storage failure")
					if failure == "cancelled" {
						fault = context.Canceled
					}
					installBoundAssignmentReadFault(f, task, stage, fault)
					ctx := WithBPMNAccessScope(f.userCtx, BPMNAccessScope{UserID: f.actor.ID, TenantID: f.tenant.ID, CanReadAllTasks: true})
					var err error
					listRequest := &ListUserTasksRequest{}
					statsRequest := &TaskStatisticsRequest{}
					if stage == "identity" {
						listRequest.Assignee = f.actor.Username
						statsRequest.Assignee = f.actor.Username
					}
					switch api {
					case "list":
						rows, total, listErr := f.engine.TaskService().ListUserTasks(ctx, listRequest)
						err = listErr
						require.Empty(t, rows)
						require.Zero(t, total)
					case "views":
						rows, total, listErr := f.engine.TaskService().ListUserTaskViews(ctx, listRequest)
						err = listErr
						require.Empty(t, rows)
						require.Zero(t, total)
					case "statistics":
						stats, statsErr := f.engine.TaskService().GetTaskStatistics(ctx, statsRequest)
						err = statsErr
						require.Nil(t, stats)
					}
					if stage == "source" {
						require.ErrorContains(t, err, "unsupported")
					} else {
						require.ErrorIs(t, err, fault)
					}
				})
			}
		}
	}
}

func TestBPMNBoundAssignmentMissingActorIsDenialNotPartialFailure(t *testing.T) {
	f := newBPMNAuthorizationFixture(t)
	grantBoundPermissions(t, f, f.actor, "service_request", "read", "task", "read")
	_, task := seedBoundAssignment(t, f, "missing-actor")
	f.client.User.UpdateOne(f.actor).SetActive(false).SaveX(f.userCtx)
	scope := BPMNAccessScope{UserID: f.actor.ID, TenantID: f.tenant.ID, CanReadAllTasks: true}
	var denial *common.AppError
	require.ErrorAs(t, f.engine.authorizeBoundTask(f.userCtx, f.client, task, scope, ""), &denial)
	require.Equal(t, common.ErrCodeForbidden, denial.Code)
	rows, total, err := f.engine.TaskService().ListUserTasks(WithBPMNAccessScope(f.userCtx, scope), &ListUserTasksRequest{})
	require.NoError(t, err)
	require.Empty(t, rows)
	require.Zero(t, total)
}

func TestBPMNBoundAssignmentRBACFailureIsObservableAndNotCached(t *testing.T) {
	for _, stage := range []string{"role", "role_permission", "permission"} {
		t.Run(stage, func(t *testing.T) {
			f := newBPMNAuthorizationFixture(t)
			seedBoundAssignment(t, f, "rbac-fault")
			grantBoundPermissions(t, f, f.actor, "service_request", "read", "task", "read")
			fault := errors.New("injected RBAC storage failure")
			fail := true
			interceptor := ent.InterceptFunc(func(next ent.Querier) ent.Querier {
				return ent.QuerierFunc(func(ctx context.Context, q ent.Query) (ent.Value, error) {
					if fail {
						return nil, fault
					}
					return next.Query(ctx, q)
				})
			})
			switch stage {
			case "role":
				f.client.Role.Intercept(interceptor)
			case "role_permission":
				f.client.RolePermission.Intercept(interceptor)
			case "permission":
				f.client.Permission.Intercept(interceptor)
			}
			ctx := f.typedTaskScopeOnlyCtx(f.actor, false)
			rows, total, err := f.engine.TaskService().ListUserTasks(ctx, &ListUserTasksRequest{Page: 1, PageSize: 10})
			require.ErrorIs(t, err, fault)
			require.Empty(t, rows)
			require.Zero(t, total)
			fail = false
			rows, total, err = f.engine.TaskService().ListUserTasks(ctx, &ListUserTasksRequest{Page: 1, PageSize: 10})
			require.NoError(t, err)
			require.Equal(t, 1, total)
			require.Len(t, rows, 1)
		})
	}
}

// Characterize tenant-history work separately from authorization correctness.
// This records query/row work without an environment-sensitive latency threshold.
func TestBPMNAssignmentListHistoryScale(t *testing.T) {
	for _, bound := range []bool{false, true} {
		t.Run(strconv.FormatBool(bound), func(t *testing.T) {
			f := newBPMNAuthorizationFixture(t)
			_, original := seedBoundAssignment(t, f, "scale")
			if !bound {
				f.client.ProcessTask.DeleteOne(original).ExecX(f.userCtx)
			}
			count := 1000
			if bound {
				count = 100
			}
			creates := make([]*ent.ProcessTaskCreate, 0, count)
			for i := 0; i < count; i++ {
				create := f.client.ProcessTask.Create().SetTenantID(f.tenant.ID).SetTaskID("scale-" + strconv.Itoa(i)).SetProcessInstanceID(original.ProcessInstanceID).SetProcessDefinitionKey(f.definition.Key).SetTaskDefinitionKey("fulfill").SetTaskName("Scale").SetStatus("created")
				if bound {
					create.SetAssigneeSource("work_item_assignee")
				}
				creates = append(creates, create)
			}
			f.client.ProcessTask.CreateBulk(creates...).SaveX(f.userCtx)
			if bound {
				count++
			}
			grantBoundPermissions(t, f, f.actor, "task", "read", "service_request", "read")
			queries, materialized := 0, 0
			f.client.Intercept(ent.InterceptFunc(func(next ent.Querier) ent.Querier {
				return ent.QuerierFunc(func(ctx context.Context, q ent.Query) (ent.Value, error) {
					queries++
					value, err := next.Query(ctx, q)
					if rows, ok := value.([]*ent.ProcessTask); ok {
						materialized += len(rows)
					}
					return value, err
				})
			}))
			started := time.Now()
			rows, total, err := f.engine.TaskService().ListUserTasks(WithBPMNAccessScope(f.userCtx, BPMNAccessScope{UserID: f.actor.ID, TenantID: f.tenant.ID, CanReadAllTasks: true}), &ListUserTasksRequest{Page: 1, PageSize: 10})
			require.NoError(t, err)
			require.Equal(t, count, total)
			require.Len(t, rows, 10)
			if bound {
				require.LessOrEqual(t, queries, count*7+4, "one assignment projection per authorized task")
			}
			t.Logf("bound=%v history=%d page=%d materialized=%d queries=%d elapsed=%s", bound, count, len(rows), materialized, queries, time.Since(started))
		})
	}
}
