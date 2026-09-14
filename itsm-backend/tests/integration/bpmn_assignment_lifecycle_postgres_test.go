//go:build integration_postgres

package integration

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"sync"
	"testing"
	"time"

	entcore "entgo.io/ent"
	"github.com/lib/pq"
	"github.com/stretchr/testify/require"
	"go.uber.org/zap"
	"itsm-backend/authorization"
	"itsm-backend/common/tenantctx"
	"itsm-backend/database"
	"itsm-backend/ent"
	"itsm-backend/ent/outboxevent"
	"itsm-backend/ent/processauditlog"
	"itsm-backend/ent/processtask"
	"itsm-backend/ent/ticketworkflowrecord"
	"itsm-backend/handlers/common/workitemassignment"
	creation "itsm-backend/handlers/common/workitemcreation"
	"itsm-backend/service"
	"itsm-backend/service/bpmn"
)

const boundLifecycleXML = `<?xml version="1.0"?><definitions xmlns="http://www.omg.org/spec/BPMN/20100524/MODEL"><process id="bound" isExecutable="true"><startEvent id="start"/><userTask id="fulfill" name="Fulfill" taskPurpose="fulfillment" assigneeSource="work_item_assignee"/><endEvent id="end"/><sequenceFlow id="a" sourceRef="start" targetRef="fulfill"/><sequenceFlow id="b" sourceRef="fulfill" targetRef="end"/></process></definitions>`

type boundLifecycleFixture struct {
	setup              *incidentEffectsFixture
	runtime            *database.RuntimeClients
	client             *ent.Client
	engine             *service.CustomProcessEngine
	ctx                context.Context
	owner, next, admin *ent.User
	item               *ent.Ticket
	definition         *ent.ProcessDefinition
	task               *ent.ProcessTask
}

func newBoundLifecycleFixture(t *testing.T) *boundLifecycleFixture {
	t.Helper()
	f := newIncidentEffectsFixture(t)
	f.client.User.UpdateOne(f.actor).SetRole("super_admin").ExecX(f.ctx)
	owner := f.client.User.GetX(f.ctx, f.actor.ID)
	next := f.client.User.Create().SetTenantID(f.tenant.ID).SetUsername("next").SetName("Next").SetEmail("next@example.test").SetPasswordHash("unused").SetRole("super_admin").SaveX(f.ctx)
	admin := f.client.User.Create().SetTenantID(f.tenant.ID).SetUsername("admin").SetName("Admin").SetEmail("admin@example.test").SetPasswordHash("unused").SetRole("super_admin").SaveX(f.ctx)
	item := f.client.Ticket.Create().SetTitle("Bound lifecycle").SetTicketNumber("BOUND-LIFECYCLE").SetRequesterID(owner.ID).SetAssigneeID(owner.ID).SetTenantID(f.tenant.ID).SaveX(f.ctx)
	deployment := f.client.ProcessDeployment.Create().SetDeploymentID("bound-deploy").SetDeploymentName("Bound").SetTenantID(f.tenant.ID).SaveX(f.ctx)
	definition := f.client.ProcessDefinition.Create().SetDeploymentID(deployment.ID).SetKey("bound").SetName("Bound").SetVersion("1").SetBpmnXML([]byte(boundLifecycleXML)).SetTenantID(f.tenant.ID).SaveX(f.ctx)
	instance := f.client.ProcessInstance.Create().SetProcessInstanceID("bound-instance").SetProcessDefinitionID(definition.ID).SetProcessDefinitionKey(definition.Key).SetBusinessType("ticket").SetBusinessID(item.ID).SetStatus("running").SetCurrentActivityID("fulfill").SetTenantID(f.tenant.ID).SaveX(f.ctx)
	task := f.client.ProcessTask.Create().SetTaskID("bound-task").SetProcessInstanceID(instance.ID).SetProcessDefinitionKey(definition.Key).SetTaskDefinitionKey("fulfill").SetTaskName("Fulfill").SetTaskType("user_task").SetTaskVariables(map[string]interface{}{"taskPurpose": "fulfillment"}).SetAssigneeSource("work_item_assignee").SetStatus("created").SetTenantID(f.tenant.ID).SaveX(f.ctx)
	clients, cfg := runtimeClients(t, f)
	for _, table := range []string{"process_definitions", "process_instances", "process_tasks", "process_audit_logs", "process_callback_outboxes", "process_execution_histories", "process_approval_decisions", "ticket_workflow_records", "service_requests", "groups", "user_roles"} {
		_, err := f.db.ExecContext(f.ctx, "GRANT SELECT,INSERT,UPDATE ON "+table+" TO "+cfg.User)
		require.NoError(t, err)
		if table == "group_members" || table == "user_roles" {
			continue
		}
		var sequence *string
		require.NoError(t, f.db.QueryRowContext(f.ctx, "SELECT pg_get_serial_sequence($1,'id')", table).Scan(&sequence))
		if sequence != nil {
			_, err = f.db.ExecContext(f.ctx, "GRANT USAGE ON SEQUENCE "+*sequence+" TO "+cfg.User)
			require.NoError(t, err)
		}
	}
	ctx := tenantctx.WithTenantID(f.ctx, f.tenant.ID)
	// Compare persisted PostgreSQL timestamp precision, not the Create builder's
	// in-memory nanoseconds, when checking the MVCC fence leaves values intact.
	item = clients.Tenant.Ticket.GetX(ctx, item.ID)
	engine := service.NewCustomProcessEngine(clients.Tenant, zap.NewNop().Sugar()).(*service.CustomProcessEngine)
	engine.SetAssignmentDirectory(clients.IntakeDirectorySnapshot())
	return &boundLifecycleFixture{f, clients, clients.Tenant, engine, ctx, owner, next, admin, item, definition, task}
}

func (f *boundLifecycleFixture) scope(actor *ent.User) context.Context {
	return service.WithBPMNAccessScope(f.ctx, service.BPMNAccessScope{TenantID: f.item.TenantID, UserID: actor.ID, CanUpdateAllTasks: actor.ID == f.admin.ID, CanReadAllTasks: actor.ID == f.admin.ID, CanUpdateAllInstances: actor.ID == f.admin.ID})
}

func (f *boundLifecycleFixture) assign(ctx context.Context, id int, enqueue workitemassignment.Enqueue) error {
	reader := authorization.NewSessionReader(f.client, f.runtime.IntakeDirectorySnapshot())
	return reader.Write(ctx, creation.Identity{ActorID: f.admin.ID, TenantID: f.item.TenantID, Role: "super_admin", Channel: "http"}, func(session *authorization.SessionSnapshot) error {
		item, err := session.Tx.Ticket.Get(ctx, f.item.ID)
		if err != nil {
			return err
		}
		cmd := workitemassignment.Command{TenantID: item.TenantID, WorkItemID: item.ID, ExpectedVersion: item.Version, ActorID: f.admin.ID, ActorTenantID: f.admin.TenantID, AssigneeID: id, Source: "http", Reason: "race"}
		if enqueue == nil {
			_, err = service.NewWorkItemAssignmentWriter(session).Apply(ctx, session.Tx.Client(), cmd)
		} else {
			_, err = workitemassignment.NewWriter(enqueue, func(ctx context.Context, client *ent.Client, cmd workitemassignment.Command) error {
				return session.ValidateAssignmentIdentities(ctx, client, cmd.ActorID, cmd.ActorTenantID, cmd.TenantID, cmd.AssigneeID)
			}).Apply(ctx, session.Tx.Client(), cmd)
		}
		return err
	})
}

func awaitBlocked(t *testing.T, ch <-chan error) {
	t.Helper()
	select {
	case err := <-ch:
		t.Fatalf("command passed held WorkItem lock: %v", err)
	case <-time.After(120 * time.Millisecond):
	}
}

func TestPostgresBoundLifecycleAssignmentWinsCompletion(t *testing.T) {
	f := newBoundLifecycleFixture(t)
	entered, release := make(chan struct{}), make(chan struct{})
	assigned := make(chan error, 1)
	go func() {
		assigned <- f.assign(f.ctx, f.next.ID, func(ctx context.Context, client *ent.Client, event workitemassignment.Event) error {
			close(entered)
			<-release
			return service.EnqueueWorkItemAssignment(ctx, client, event)
		})
	}()
	<-entered
	completed := make(chan error, 1)
	go func() { completed <- f.engine.CompleteTask(f.scope(f.owner), f.task.TaskID, nil) }()
	awaitBlocked(t, completed)
	close(release)
	require.NoError(t, <-assigned)
	require.Error(t, <-completed)
	require.Error(t, f.engine.CompleteTask(f.scope(f.owner), f.task.TaskID, nil), "old actor denied after assignment response")
	require.NoError(t, f.engine.CompleteTask(f.scope(f.next), f.task.TaskID, nil))
	saved := f.client.ProcessTask.GetX(f.ctx, f.task.ID)
	require.Empty(t, saved.Assignee)
	audit := f.client.ProcessAuditLog.Query().Where(processauditlog.ActionEQ(service.AuditActionTaskCompleted)).OnlyX(f.ctx)
	require.Equal(t, f.next.ID, audit.AssigneeID)
	require.Equal(t, f.next.ID, audit.UserID)
	require.EqualValues(t, saved.AggregationVersion, audit.Metadata["taskVersion"])
	t.Log("serial outcome: assignment commits; old completion denied; new owner completes with frozen new responsibility")
}

func TestPostgresBoundLifecycleTerminalWinsAssignment(t *testing.T) {
	for _, command := range []string{"complete", "cancel", "terminate"} {
		t.Run(command, func(t *testing.T) {
			f := newBoundLifecycleFixture(t)
			entered, release := make(chan struct{}), make(chan struct{})
			var once sync.Once
			f.client.ProcessTask.Use(func(next ent.Mutator) ent.Mutator {
				return ent.MutateFunc(func(ctx context.Context, m ent.Mutation) (ent.Value, error) {
					if m.Op().Is(ent.OpUpdate) {
						once.Do(func() { close(entered); <-release })
					}
					return next.Mutate(ctx, m)
				})
			})
			terminal := make(chan error, 1)
			go func() {
				switch command {
				case "complete":
					terminal <- f.engine.CompleteTask(f.scope(f.owner), f.task.TaskID, nil)
				case "cancel":
					terminal <- f.engine.TaskService().CancelTask(f.scope(f.admin), f.task.TaskID, "cancel")
				case "terminate":
					terminal <- f.engine.TerminateProcess(f.scope(f.admin), "bound-instance", "terminate")
				}
			}()
			select {
			case <-entered:
			case err := <-terminal:
				t.Fatalf("terminal failed before lock barrier: %v", err)
			}
			assigned := make(chan error, 1)
			go func() { assigned <- f.assign(f.ctx, f.next.ID, nil) }()
			awaitBlocked(t, assigned)
			close(release)
			require.NoError(t, <-terminal)
			assignmentErr := <-assigned
			if assignmentErr != nil {
				var pgErr *pq.Error
				require.ErrorAs(t, assignmentErr, &pgErr)
				require.Equal(t, pq.ErrorCode("40001"), pgErr.Code, "snapshot loser must fail explicitly, never deadlock")
				require.Zero(t, f.client.TicketWorkflowRecord.Query().Where(ticketworkflowrecord.ActionEQ("assign")).CountX(f.ctx))
				require.Zero(t, f.client.OutboxEvent.Query().Where(outboxevent.EventTypeEQ("work_item.assigned")).CountX(f.ctx))
				require.NoError(t, f.assign(f.ctx, f.next.ID, nil))
			}
			action := service.AuditActionTaskCancelled
			actor := f.admin.ID
			if command == "complete" {
				action = service.AuditActionTaskCompleted
				actor = f.owner.ID
			}
			audit := f.client.ProcessAuditLog.Query().Where(processauditlog.ActionEQ(action)).OnlyX(f.ctx)
			require.Equal(t, f.owner.ID, audit.AssigneeID)
			require.Equal(t, actor, audit.UserID)
			view, err := f.engine.TaskService().GetTaskByID(f.scope(f.admin), f.task.ID)
			require.NoError(t, err)
			require.Equal(t, fmt.Sprint(f.owner.ID), view.Assignee)
			require.Equal(t, f.next.ID, f.client.Ticket.GetX(f.ctx, f.item.ID).AssigneeID)
			t.Logf("serial outcome: %s commits with original responsibility; assignment commits afterward", command)
		})
	}
}

func TestPostgresBoundLifecycleTerminationAuditFailureRollsBackAllTasks(t *testing.T) {
	f := newBoundLifecycleFixture(t)
	f.setup.client.ProcessTask.Create().SetTaskID("bound-second").SetProcessInstanceID(f.task.ProcessInstanceID).SetProcessDefinitionKey(f.definition.Key).SetTaskDefinitionKey("fulfill").SetTaskName("Second").SetTaskType("user_task").SetAssigneeSource("work_item_assignee").SetStatus("created").SetTenantID(f.item.TenantID).SaveX(f.setup.ctx)
	audits := 0
	f.client.ProcessAuditLog.Use(func(next ent.Mutator) ent.Mutator {
		return ent.MutateFunc(func(ctx context.Context, m ent.Mutation) (ent.Value, error) {
			a := m.(*ent.ProcessAuditLogMutation)
			action, _ := a.Action()
			if action == service.AuditActionTaskCancelled {
				audits++
				if audits == 2 {
					return nil, errors.New("second terminal audit fault")
				}
			}
			return next.Mutate(ctx, m)
		})
	})
	require.ErrorContains(t, f.engine.TerminateProcess(f.scope(f.admin), "bound-instance", "terminate"), "second terminal audit fault")
	require.Equal(t, 2, audits)
	tasks := f.client.ProcessTask.Query().Where(processtask.ProcessInstanceID(f.task.ProcessInstanceID)).AllX(f.ctx)
	for _, task := range tasks {
		require.Equal(t, "created", task.Status)
		require.Equal(t, f.task.AggregationVersion, task.AggregationVersion)
	}
	require.Equal(t, "running", f.client.ProcessInstance.GetX(f.ctx, f.task.ProcessInstanceID).Status)
	require.Zero(t, f.client.ProcessAuditLog.Query().CountX(f.ctx))
	t.Log("second task audit failure rolls back both task transitions, versions, and instance termination")
	require.NoError(t, f.engine.TerminateProcess(f.scope(f.admin), "bound-instance", "retry"))
	require.Equal(t, 2, f.client.ProcessAuditLog.Query().Where(processauditlog.ActionEQ(service.AuditActionTaskCancelled)).CountX(f.ctx))
	require.Equal(t, 1, f.client.ProcessAuditLog.Query().Where(processauditlog.ActionEQ(service.AuditActionProcessTerminated)).CountX(f.ctx))
	for _, task := range f.client.ProcessTask.Query().Where(processtask.ProcessInstanceID(f.task.ProcessInstanceID)).AllX(f.ctx) {
		require.Equal(t, "cancelled", task.Status)
		require.Empty(t, task.Assignee)
	}
}

func TestPostgresBoundLifecycleMSPCurrentAllocation(t *testing.T) {
	for _, scenario := range []string{"allocated", "revoked", "unallocated", "inactive", "ordinary-foreign", "no-professional-permission"} {
		t.Run(scenario, func(t *testing.T) {
			f := newBoundLifecycleFixture(t)
			f.setup.client.Tenant.UpdateOneID(f.item.TenantID).SetType("msp_customer").ExecX(f.setup.ctx)
			provider := f.setup.client.Tenant.Create().SetCode("provider").SetName("Provider").SetType("msp_provider").SaveX(f.setup.ctx)
			actor := f.setup.client.User.Create().SetTenantID(provider.ID).SetUsername("msp").SetName("MSP").SetEmail("msp@example.test").SetPasswordHash("unused").SetRole("admin").SetMspRole("provider_agent").SaveX(f.setup.ctx)
			if scenario != "unallocated" {
				allocation := f.setup.client.MSPAllocation.Create().SetMspUserID(actor.ID).SetCustomerTenantID(f.item.TenantID).SetRole("primary").SaveX(f.setup.ctx)
				if scenario == "revoked" {
					f.setup.client.MSPAllocation.UpdateOne(allocation).SetDeassignedAt(time.Now()).ExecX(f.setup.ctx)
				}
			}
			if scenario == "inactive" {
				f.setup.client.User.UpdateOne(actor).SetActive(false).ExecX(f.setup.ctx)
			}
			if scenario == "ordinary-foreign" {
				f.setup.client.User.UpdateOne(actor).ClearMspRole().ExecX(f.setup.ctx)
			}
			role := f.setup.client.Role.Create().SetTenantID(f.item.TenantID).SetCode("msp_tech").SetName("MSP").SaveX(f.setup.ctx)
			for _, grant := range [][2]string{{"task", "read"}, {"task", "update"}, {"ticket", "read"}, {"ticket", "update"}} {
				if scenario == "no-professional-permission" && grant == [2]string{"ticket", "update"} {
					continue
				}
				permission := f.setup.client.Permission.Create().SetTenantID(f.item.TenantID).SetCode(grant[0] + ":" + grant[1]).SetName("Grant").SetResource(grant[0]).SetAction(grant[1]).SaveX(f.setup.ctx)
				f.setup.client.RolePermission.Create().SetTenantID(f.item.TenantID).SetRoleID(role.ID).SetPermissionID(permission.ID).ExecX(f.setup.ctx)
			}
			authorization.InvalidateRolePermissionCache("msp_tech", f.item.TenantID)
			t.Cleanup(func() { authorization.InvalidateRolePermissionCache("msp_tech", f.item.TenantID) })
			f.setup.client.Ticket.UpdateOne(f.item).SetAssigneeID(actor.ID).ExecX(f.setup.ctx)
			_, err := f.client.User.Get(f.ctx, actor.ID)
			require.True(t, ent.IsNotFound(err), "ordinary target RLS hides native MSP identity")
			ctx := service.WithBPMNAccessScope(f.ctx, service.BPMNAccessScope{UserID: actor.ID, TenantID: f.item.TenantID})
			if scenario == "allocated" {
				_, err := f.engine.ProcessInstanceService().GetProcessInstance(ctx, "bound-instance")
				require.NoError(t, err, "bound instance participation uses current MSP allocation")
				tasks, total, err := f.engine.TaskService().ListUserTaskViews(ctx, &service.ListUserTasksRequest{})
				require.NoError(t, err)
				require.Equal(t, 1, total)
				require.Equal(t, fmt.Sprint(actor.ID), tasks[0].Assignee)
				require.True(t, tasks[0].UIActions.Complete)
				require.NoError(t, f.engine.CompleteTask(ctx, f.task.TaskID, nil))
				audit := f.client.ProcessAuditLog.Query().Where(processauditlog.ActionEQ(service.AuditActionTaskCompleted)).OnlyX(f.ctx)
				require.Equal(t, actor.ID, audit.AssigneeID)
				require.Equal(t, actor.ID, audit.UserID)
			} else {
				if scenario != "no-professional-permission" {
					rows, total, err := f.engine.TaskService().ListUserTaskViews(f.scope(f.admin), &service.ListUserTasksRequest{Page: 1, PageSize: 10})
					require.NoError(t, err)
					require.Equal(t, 1, total)
					require.Equal(t, "unavailable", rows[0].AssignmentState)
					_, _, err = f.engine.TaskService().ListUserTaskViews(ctx, &service.ListUserTasksRequest{Page: 1, PageSize: 10})
					require.Error(t, err, "ineligible MSP actor cannot use the read snapshot")
				}
				require.Error(t, f.engine.CompleteTask(ctx, f.task.TaskID, nil))
				if scenario != "no-professional-permission" {
					require.Error(t, f.engine.CompleteTask(f.scope(f.admin), f.task.TaskID, nil), "valid administrator cannot complete for an unavailable owner")
				}
				require.Equal(t, "created", f.client.ProcessTask.GetX(f.ctx, f.task.ID).Status)
				require.Zero(t, f.client.ProcessAuditLog.Query().Where(processauditlog.ActionEQ(service.AuditActionTaskCompleted)).CountX(f.ctx))
			}
		})
	}
}

func TestPostgresBoundLifecycleCreationWinsAssignment(t *testing.T) {
	f := newBoundLifecycleFixture(t)
	entered, release := make(chan struct{}), make(chan struct{})
	var once sync.Once
	f.client.ProcessTask.Use(func(next ent.Mutator) ent.Mutator {
		return ent.MutateFunc(func(ctx context.Context, m ent.Mutation) (ent.Value, error) {
			if m.Op().Is(ent.OpCreate) {
				once.Do(func() { close(entered); <-release })
			}
			return next.Mutate(ctx, m)
		})
	})
	created := make(chan error, 1)
	go func() {
		_, err := f.engine.StartProcess(f.scope(f.owner), f.definition.Key, "ticket:new", "ticket", f.item.ID, nil)
		created <- err
	}()
	select {
	case <-entered:
	case err := <-created:
		t.Fatalf("start failed before creation barrier: %v", err)
	}
	assigned := make(chan error, 1)
	go func() { assigned <- f.assign(f.ctx, f.next.ID, nil) }()
	awaitBlocked(t, assigned)
	close(release)
	require.NoError(t, <-created)
	assignmentErr := <-assigned
	if assignmentErr != nil {
		var pgErr *pq.Error
		require.ErrorAs(t, assignmentErr, &pgErr)
		require.Equal(t, pq.ErrorCode("40001"), pgErr.Code)
		savedItem := f.client.Ticket.GetX(f.ctx, f.item.ID)
		require.Equal(t, f.item.Version, savedItem.Version, "creation fence preserves aggregate version")
		require.True(t, f.item.UpdatedAt.Equal(savedItem.UpdatedAt), "creation fence preserves public timestamp")
		require.Equal(t, f.item.AssigneeID, savedItem.AssigneeID)
		require.Zero(t, f.client.TicketWorkflowRecord.Query().Where(ticketworkflowrecord.ActionEQ("assign")).CountX(f.ctx))
		require.Zero(t, f.client.OutboxEvent.Query().Where(outboxevent.EventTypeEQ("work_item.assigned")).CountX(f.ctx))
		require.NoError(t, f.assign(f.ctx, f.next.ID, nil))
	}
	tasks := f.client.ProcessTask.Query().Where(processtask.AssigneeSourceEQ("work_item_assignee")).AllX(f.ctx)
	require.Len(t, tasks, 2)
	for _, task := range tasks {
		require.Empty(t, task.Assignee)
	}
	audit := f.client.TicketWorkflowRecord.Query().Where(ticketworkflowrecord.ActionEQ("assign")).OnlyX(f.ctx)
	require.Len(t, audit.Metadata["affectedTaskIds"], 2, "assignment serialized after creation must include newly created bound task")
	t.Log("serial outcome: create commits before assignment; assignment audit includes both bound tasks")
}

func TestPostgresBoundLifecycleAssignmentWinsCreation(t *testing.T) {
	f := newBoundLifecycleFixture(t)
	entered, release := make(chan struct{}), make(chan struct{})
	assigned := make(chan error, 1)
	go func() {
		assigned <- f.assign(f.ctx, f.next.ID, func(ctx context.Context, client *ent.Client, event workitemassignment.Event) error {
			close(entered)
			<-release
			return service.EnqueueWorkItemAssignment(ctx, client, event)
		})
	}()
	<-entered
	created := make(chan error, 1)
	go func() {
		_, err := f.engine.StartProcess(f.scope(f.owner), f.definition.Key, "ticket:new", "ticket", f.item.ID, nil)
		created <- err
	}()
	awaitBlocked(t, created)
	close(release)
	require.NoError(t, <-assigned)
	require.NoError(t, <-created)
	tasks, total, err := f.engine.TaskService().ListUserTaskViews(f.scope(f.next), &service.ListUserTasksRequest{})
	require.NoError(t, err)
	require.Equal(t, 2, total)
	for _, task := range tasks {
		require.Equal(t, fmt.Sprint(f.next.ID), task.Assignee)
	}
	t.Log("serial outcome: assignment commits first; creation observes current WorkItem owner without stored assignee")
}

type boundLifecycleCallback struct{}

func (boundLifecycleCallback) GetTaskType() string  { return "bound_lifecycle_probe" }
func (boundLifecycleCallback) GetHandlerID() string { return "bound_lifecycle_probe" }
func (boundLifecycleCallback) CallbackContract(string) (bpmn.CallbackActionContract, bool) {
	return bpmn.CallbackActionContract{}, true
}
func (boundLifecycleCallback) Execute(context.Context, *ent.ProcessTask, map[string]interface{}) (*bpmn.CallbackEffect, error) {
	return bpmn.AppliedEffect("test-only local effect", nil), nil
}

func TestPostgresBoundLifecycleCallbackCreationRace(t *testing.T) {
	for _, kind := range []string{"service_task", "user_task_callback"} {
		for _, first := range []string{"callback", "assignment"} {
			t.Run(kind+"/"+first, func(t *testing.T) {
				f := newBoundLifecycleFixture(t)
				xml := `<?xml version="1.0"?><definitions xmlns="http://www.omg.org/spec/BPMN/20100524/MODEL"><process id="bound" isExecutable="true"><startEvent id="start"/><serviceTask id="callback" implementation="bound_lifecycle_probe"/><userTask id="next" name="Next" taskPurpose="fulfillment" assigneeSource="work_item_assignee"/><endEvent id="end"/><sequenceFlow id="a" sourceRef="start" targetRef="callback"/><sequenceFlow id="b" sourceRef="callback" targetRef="next"/><sequenceFlow id="c" sourceRef="next" targetRef="end"/></process></definitions>`
				f.setup.client.ProcessDefinition.UpdateOne(f.definition).SetBpmnXML([]byte(xml)).ExecX(f.setup.ctx)
				f.setup.client.ProcessInstance.UpdateOneID(f.task.ProcessInstanceID).SetCurrentActivityID("callback").ExecX(f.setup.ctx)
				row := f.setup.client.ProcessCallbackOutbox.Create().SetExecutionKey("callback-race").SetProcessInstanceID(f.task.ProcessInstanceID).SetCallbackKind(kind).SetHandlerID("bound_lifecycle_probe").SetTaskType("bound_lifecycle_probe").SetElementID("callback").SetTenantID(f.item.TenantID).SaveX(f.setup.ctx)
				if kind == "user_task_callback" {
					xml = strings.Replace(xml, `<serviceTask id="callback" implementation="bound_lifecycle_probe"/>`, `<userTask id="callback" name="Callback"/>`, 1)
					f.setup.client.ProcessDefinition.UpdateOne(f.definition).SetBpmnXML([]byte(xml)).ExecX(f.setup.ctx)
					completed := f.setup.client.ProcessTask.Create().SetTenantID(f.item.TenantID).SetTaskID("completed-callback").SetProcessInstanceID(f.task.ProcessInstanceID).SetProcessDefinitionKey(f.definition.Key).SetTaskDefinitionKey("callback").SetTaskName("Callback").SetTaskType("user_task").SetAssignee(fmt.Sprint(f.owner.ID)).SetStatus("completed").SaveX(f.setup.ctx)
					row = f.setup.client.ProcessCallbackOutbox.UpdateOne(row).SetProcessTaskID(completed.ID).SetTaskID(completed.TaskID).SaveX(f.setup.ctx)
				}
				f.engine.CallbackRegistry().RegisterHandler(boundLifecycleCallback{})
				entered, release := make(chan struct{}), make(chan struct{})
				assigned, advanced := make(chan error, 1), make(chan error, 1)
				advance := func() {
					count, err := f.engine.ProcessPendingCallbacks(f.ctx, "race-worker", 10)
					if err == nil && count != 1 {
						err = fmt.Errorf("callback completed count %d", count)
					}
					advanced <- err
				}
				if first == "callback" {
					var once sync.Once
					f.client.ProcessTask.Use(func(next ent.Mutator) ent.Mutator {
						return ent.MutateFunc(func(ctx context.Context, m ent.Mutation) (ent.Value, error) {
							if m.Op().Is(ent.OpCreate) {
								once.Do(func() { close(entered); <-release })
							}
							return next.Mutate(ctx, m)
						})
					})
					go advance()
					select {
					case <-entered:
					case err := <-advanced:
						t.Fatalf("callback failed before create barrier: %v", err)
					}
					go func() { assigned <- f.assign(f.ctx, f.next.ID, nil) }()
					awaitBlocked(t, assigned)
				} else {
					go func() {
						assigned <- f.assign(f.ctx, f.next.ID, func(ctx context.Context, client *ent.Client, event workitemassignment.Event) error {
							close(entered)
							<-release
							return service.EnqueueWorkItemAssignment(ctx, client, event)
						})
					}()
					<-entered
					go advance()
					awaitBlocked(t, advanced)
				}
				close(release)
				require.NoError(t, <-advanced)
				assignmentErr := <-assigned
				if assignmentErr != nil {
					var pgErr *pq.Error
					require.ErrorAs(t, assignmentErr, &pgErr)
					require.Equal(t, pq.ErrorCode("40001"), pgErr.Code)
					require.Zero(t, f.client.TicketWorkflowRecord.Query().Where(ticketworkflowrecord.ActionEQ("assign")).CountX(f.ctx))
					require.Zero(t, f.client.OutboxEvent.Query().Where(outboxevent.EventTypeEQ("work_item.assigned")).CountX(f.ctx))
					require.NoError(t, f.assign(f.ctx, f.next.ID, nil))
				}
				require.Equal(t, "completed", f.client.ProcessCallbackOutbox.GetX(f.ctx, row.ID).Status)
				tasks := f.client.ProcessTask.Query().Where(processtask.ProcessInstanceID(f.task.ProcessInstanceID)).AllX(f.ctx)
				if kind == "user_task_callback" {
					require.Len(t, tasks, 3)
				} else {
					require.Len(t, tasks, 2)
				}
				if first == "callback" {
					audit := f.client.TicketWorkflowRecord.Query().Where(ticketworkflowrecord.ActionEQ("assign")).OnlyX(f.ctx)
					require.Len(t, audit.Metadata["affectedTaskIds"], 2)
				}
				for _, task := range tasks {
					if task.Status == "completed" {
						require.Equal(t, fmt.Sprint(f.owner.ID), task.Assignee)
						continue
					}
					view, err := f.engine.TaskService().GetTaskByID(f.scope(f.next), task.ID)
					require.NoError(t, err)
					require.Equal(t, fmt.Sprint(f.next.ID), view.Assignee)
					require.Empty(t, task.Assignee)
				}
				t.Logf("serial outcome: %s first; callback creates one bound task, current owner consistent", first)
			})
		}
	}
}

func TestPostgresBoundLifecycleReadSnapshotAndIndependentProof(t *testing.T) {
	for _, bound := range []bool{false, true} {
		t.Run(fmt.Sprint(bound), func(t *testing.T) {
			f := newBoundLifecycleFixture(t)
			directory := &countingTaskReadDirectory{DirectorySnapshot: f.runtime.IntakeDirectorySnapshot()}
			f.engine.SetAssignmentDirectory(directory)
			if !bound {
				f.setup.client.ProcessTask.DeleteOne(f.task).ExecX(f.setup.ctx)
				f.task = f.setup.client.ProcessTask.Create().SetTenantID(f.item.TenantID).SetTaskID("independent-snapshot").SetProcessInstanceID(f.task.ProcessInstanceID).SetProcessDefinitionKey(f.definition.Key).SetTaskDefinitionKey("independent").SetTaskName("Independent").SetTaskType("user_task").SetAssignee(fmt.Sprint(f.owner.ID)).SetStatus("created").SaveX(f.setup.ctx)
			}
			changed := false
			f.client.ProcessTask.Intercept(ent.InterceptFunc(func(next ent.Querier) ent.Querier {
				return ent.QuerierFunc(func(ctx context.Context, q ent.Query) (ent.Value, error) {
					value, err := next.Query(ctx, q)
					if err == nil && !changed {
						if qc := entcore.QueryFromContext(ctx); qc != nil && qc.Op == entcore.OpQueryExist {
							changed = true
							if bound {
								require.NoError(t, f.assign(f.scope(f.admin), f.next.ID, nil))
							} else {
								f.setup.client.ProcessTask.Create().SetTenantID(f.item.TenantID).SetTaskID("concurrent-bound").SetProcessInstanceID(f.task.ProcessInstanceID).SetProcessDefinitionKey(f.definition.Key).SetTaskDefinitionKey("fulfill").SetTaskName("Concurrent").SetTaskType("user_task").SetAssigneeSource("work_item_assignee").SetStatus("created").SaveX(f.setup.ctx)
							}
						}
					}
					return value, err
				})
			}))
			rows, total, err := f.engine.TaskService().ListUserTaskViews(f.scope(f.admin), &service.ListUserTasksRequest{Page: 1, PageSize: 10})
			require.NoError(t, err)
			require.True(t, changed)
			if bound {
				require.Equal(t, 1, directory.opens, "selection and DTO/actions share one imported directory snapshot")
			}
			require.Equal(t, 1, total)
			require.Len(t, rows, 1)
			require.Equal(t, fmt.Sprint(f.owner.ID), rows[0].Assignee, "DTO projection stays on the same snapshot as selection/count")
			rows, total, err = f.engine.TaskService().ListUserTaskViews(f.scope(f.admin), &service.ListUserTasksRequest{Page: 1, PageSize: 10})
			require.NoError(t, err)
			if bound {
				require.Equal(t, 1, total)
				require.Equal(t, fmt.Sprint(f.next.ID), rows[0].Assignee)
			} else {
				require.Equal(t, 2, total)
			}
		})
	}
}

type countingTaskReadDirectory struct {
	database.DirectorySnapshot
	opens int
}

func (d *countingTaskReadDirectory) Open(ctx context.Context, tx *ent.Tx, tenantID int) (*ent.Client, func() error, error) {
	d.opens++
	return d.DirectorySnapshot.Open(ctx, tx, tenantID)
}

func TestPostgresBoundLifecycleTerminalBatchAuditMetadata(t *testing.T) {
	f := newBoundLifecycleFixture(t)
	task := f.setup.client.ProcessTask.UpdateOne(f.task).SetStatus("completed").SetAggregationVersion(2).SaveX(f.setup.ctx)
	instance := f.setup.client.ProcessInstance.GetX(f.setup.ctx, task.ProcessInstanceID)
	for _, id := range []any{fmt.Sprint(task.ID), "malformed-audit-task-id"} {
		f.setup.client.ProcessAuditLog.Create().SetProcessInstanceID(instance.ID).SetProcessInstanceKey(instance.ProcessInstanceID).SetProcessDefinitionID(instance.ProcessDefinitionID).SetProcessDefinitionKey(instance.ProcessDefinitionKey).SetActivityID(task.TaskDefinitionKey).SetActivityType(service.ActivityTypeUserTask).SetAction(service.AuditActionTaskCompleted).SetTenantID(task.TenantID).SetAssigneeID(f.owner.ID).SetUserID(f.admin.ID).SetMetadata(map[string]interface{}{"taskId": id, "taskVersion": "2", "terminalStatus": "completed", "assigneeSource": "work_item_assignee"}).SaveX(f.setup.ctx)
	}
	f.setup.client.Ticket.UpdateOne(f.item).SetAssigneeID(f.next.ID).ExecX(f.setup.ctx)
	rows, total, err := f.engine.TaskService().ListUserTaskViews(f.scope(f.admin), &service.ListUserTasksRequest{Page: 1, PageSize: 10})
	require.NoError(t, err)
	require.Equal(t, 1, total)
	require.Len(t, rows, 1)
	require.Equal(t, "terminal", rows[0].AssignmentState)
	require.Equal(t, f.owner.ID, rows[0].ResponsibleUserID)
	require.Equal(t, f.admin.ID, rows[0].ActorID)
}
