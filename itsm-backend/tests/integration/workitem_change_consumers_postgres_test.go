//go:build integration_postgres

package integration

import (
	"context"
	"errors"
	"fmt"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
	"itsm-backend/common/tenantctx"
	"itsm-backend/ent"
	"itsm-backend/ent/auditlog"
	"itsm-backend/ent/processcallbackoutbox"
	"itsm-backend/ent/processdefinition"
	"itsm-backend/ent/processtask"
	changedomain "itsm-backend/handlers/change"
	creation "itsm-backend/handlers/common/workitemcreation"
	"itsm-backend/service"
	"itsm-backend/service/bpmn"
)

// These cross-domain tests moved from package service to exercise the actual
// owning Change service without an upward import or simulated business effect.
func prepareChangeMetadataTask(t *testing.T, f *changeLifecycleFixture) *ent.ProcessTask {
	t.Helper()
	f.engine.CallbackRegistry().GetHandler("change_service_handler").(*bpmn.ChangeServiceTaskHandler).SetChangeService(f.owner)
	f.client.ProcessDefinition.Update().Where(processdefinition.Key("change_normal_flow")).SetBpmnXML([]byte(`<bpmn:definitions xmlns:bpmn="http://www.omg.org/spec/BPMN/20100524/MODEL" targetNamespace="test"><bpmn:process id="change" isExecutable="true"><bpmn:startEvent id="start"/><bpmn:userTask id="metadata" name="Edit"><bpmn:extensionElements><bpmn:metaData name="service_task_type">change_task</bpmn:metaData><bpmn:metaData name="action">update_change</bpmn:metaData></bpmn:extensionElements></bpmn:userTask><bpmn:endEvent id="end"/><bpmn:sequenceFlow id="a" sourceRef="start" targetRef="metadata"/><bpmn:sequenceFlow id="b" sourceRef="metadata" targetRef="end"/></bpmn:process></bpmn:definitions>`)).ExecX(f.ctx)
	f.apply(t, f.command("submit", "submit"))
	return f.client.ProcessTask.Query().OnlyX(f.ctx)
}

func seedChangeCABActor(t *testing.T, f *changeLifecycleFixture) *ent.User {
	t.Helper()
	actor := f.client.User.Create().SetTenantID(f.tenant.ID).SetUsername("consumer-cab").SetName("CAB").SetEmail("consumer-cab@example.test").SetPasswordHash("test").SetRole("super_admin").SetActive(true).SaveX(f.ctx)
	role := f.client.Role.Create().SetTenantID(f.tenant.ID).SetCode("change_manager").SetName("CAB").SetIsActive(true).SaveX(f.ctx)
	actor.Update().AddRoleIDs(role.ID).ExecX(f.ctx)
	return actor
}

func completeDefaultChangeAction(t *testing.T, f *changeLifecycleFixture, action, key string) changedomain.TaskProgress {
	t.Helper()
	task := f.client.ProcessTask.Query().Where(processtask.TaskDefinitionKey(key)).OnlyX(f.ctx)
	result, err := f.owner.CompleteChangeTask(f.ctx, changedomain.TaskCommand{Command: f.command(action, "consumer-"+action), TaskID: task.TaskID})
	require.NoError(t, err)
	require.Equal(t, "completed", result.Progress)
	require.NotNil(t, result.Result)
	return result
}

func TestCompleteTask_TypedScope_CallbackUsesAuthoritativeBusinessIdentity(t *testing.T) {
	f := newChangeLifecycleFixture(t, "normal")
	task := prepareChangeMetadataTask(t, f)
	before := f.client.Ticket.GetX(f.ctx, f.c.WorkItemID)
	ctx := service.WithBPMNAccessScope(tenantctx.WithTenantID(context.Background(), f.tenant.ID), service.BPMNAccessScope{UserID: f.actor.ID, TenantID: f.tenant.ID})
	require.NoError(t, f.engine.CompleteTask(ctx, task.TaskID, map[string]any{"title": "typed scope title", "version": before.Version}))
	row := f.client.ProcessCallbackOutbox.Query().OnlyX(f.ctx)
	require.Equal(t, f.tenant.ID, row.TenantID)
	require.Equal(t, f.actor.ID, row.ActorID)
	require.Equal(t, "completed", row.Status)
	require.Equal(t, "typed scope title", f.client.Ticket.GetX(f.ctx, before.ID).Title)
	require.Equal(t, "completed", f.client.ProcessInstance.Query().OnlyX(f.ctx).Status)
	require.Equal(t, 1, f.client.AuditLog.Query().Where(auditlog.Action("change.metadata")).CountX(f.ctx))
}

func TestCompleteTask_ParticipantBusinessIDCannotRetargetCallback(t *testing.T) {
	f := newChangeLifecycleFixture(t, "normal")
	task := prepareChangeMetadataTask(t, f)
	otherTenant := f.client.Tenant.Create().SetCode("consumer-other").SetName("Other").SetStatus("active").SaveX(f.ctx)
	other := f.client.Ticket.Create().SetTenantID(otherTenant.ID).SetTitle("foreign immutable").SetTicketNumber("FOREIGN-CHG").SetRequesterID(f.actor.ID).SetRecordClass("change_request").SaveX(f.ctx)
	foreign := f.client.Change.Create().SetWorkItemID(other.ID).SaveX(f.ctx)
	before := f.client.Ticket.GetX(f.ctx, f.c.WorkItemID)
	require.NoError(t, f.engine.CompleteTask(changeCallbackContext(f, f.actor), task.TaskID, map[string]any{"change_id": foreign.ID, "business_id": other.ID, "tenant_id": otherTenant.ID, "title": "authoritative target", "version": before.Version}))
	require.Equal(t, "foreign immutable", f.client.Ticket.GetX(f.ctx, other.ID).Title)
	require.Equal(t, "authoritative target", f.client.Ticket.GetX(f.ctx, before.ID).Title)
	row := f.client.ProcessCallbackOutbox.Query().OnlyX(f.ctx)
	require.Equal(t, f.tenant.ID, row.TenantID)
	require.Equal(t, "completed", row.Status)
	require.Equal(t, 1, f.client.AuditLog.Query().Where(auditlog.Action("change.metadata")).CountX(f.ctx))
}

func TestCABApprovalAssignsChangeManagerRole(t *testing.T) {
	f := newChangeLifecycleFixture(t, "normal")
	cab := seedChangeCABActor(t, f)
	prepareChangeDefaultTask(t, f)
	completeDefaultChangeAction(t, f, "assess", "Activity_Assessment")
	task := f.client.ProcessTask.Query().Where(processtask.TaskDefinitionKey("Activity_CABApproval")).OnlyX(f.ctx)
	require.Contains(t, task.CandidateUsers, cab.Username)
	require.NotContains(t, task.CandidateUsers, f.actor.Username)
	require.Zero(t, f.client.ProcessApprovalDecision.Query().CountX(f.ctx))
}

func TestCABApprovalGatewayRoutesToScheduleOnApprove(t *testing.T) {
	f := newChangeLifecycleFixture(t, "normal")
	cab := seedChangeCABActor(t, f)
	prepareChangeDefaultTask(t, f)
	completeDefaultChangeAction(t, f, "assess", "Activity_Assessment")
	f.actor = cab
	result := completeDefaultChangeAction(t, f, "approve", "Activity_CABApproval")
	require.Equal(t, "approved", result.Result.Status)
	require.Equal(t, "Activity_Schedule", f.client.ProcessInstance.Query().OnlyX(f.ctx).CurrentActivityID)
	require.Equal(t, 1, f.client.ProcessApprovalDecision.Query().CountX(f.ctx))
	require.Equal(t, 1, f.client.AuditLog.Query().Where(auditlog.Action("change.authorize")).CountX(f.ctx))
}

func TestCABApprovalGatewayRoutesToRejectOnReject(t *testing.T) {
	f := newChangeLifecycleFixture(t, "normal")
	cab := seedChangeCABActor(t, f)
	prepareChangeDefaultTask(t, f)
	completeDefaultChangeAction(t, f, "assess", "Activity_Assessment")
	f.actor = cab
	result := completeDefaultChangeAction(t, f, "reject", "Activity_CABApproval")
	require.Equal(t, "rejected", result.Result.Status)
	instance := f.client.ProcessInstance.Query().OnlyX(f.ctx)
	require.Equal(t, "completed", instance.Status)
	require.Equal(t, "EndEvent_Rejected", instance.CurrentActivityID)
	require.Equal(t, 1, f.client.ProcessApprovalDecision.Query().CountX(f.ctx))
	require.Zero(t, f.client.ProcessTask.Query().Where(processtask.TaskDefinitionKey("Activity_Schedule")).CountX(f.ctx))
}

func TestUserTaskWithServiceTaskTypeMetadataTriggersCallback(t *testing.T) {
	f := newChangeLifecycleFixture(t, "normal")
	cab := seedChangeCABActor(t, f)
	prepareChangeDefaultTask(t, f)
	completeDefaultChangeAction(t, f, "assess", "Activity_Assessment")
	task := f.client.ProcessTask.Query().Where(processtask.TaskDefinitionKey("Activity_CABApproval")).OnlyX(f.ctx)
	version := f.client.Ticket.GetX(f.ctx, f.c.WorkItemID).Version
	require.NoError(t, f.engine.CompleteTask(changeCallbackContext(f, cab), task.TaskID, map[string]any{}))
	row := f.client.ProcessCallbackOutbox.Query().Where(processcallbackoutbox.ProcessTaskID(task.ID)).OnlyX(f.ctx)
	require.False(t, row.OptionalDeclared)
	require.Equal(t, "blocked", row.Status)
	require.Equal(t, "Activity_CABApproval", f.client.ProcessInstance.Query().OnlyX(f.ctx).CurrentActivityID)
	require.Equal(t, version, f.client.Ticket.GetX(f.ctx, f.c.WorkItemID).Version)
	require.Equal(t, "submitted", f.client.Ticket.GetX(f.ctx, f.c.WorkItemID).Status)
}

func TestUserTaskMetadataPersistsOnlyInImmutableDescriptor(t *testing.T) {
	f := newChangeLifecycleFixture(t, "normal")
	seedChangeCABActor(t, f)
	task := prepareChangeDefaultTask(t, f)
	require.NotContains(t, task.TaskVariables, "service_task_type")
	require.NotContains(t, task.TaskVariables, "action")
	require.Equal(t, "change_service_handler", task.CallbackHandlerID)
	require.Equal(t, "change_task", task.CallbackTaskType)
	require.Equal(t, "assess_risk", task.CallbackAction)
	version := f.client.Ticket.GetX(f.ctx, f.c.WorkItemID).Version
	require.NoError(t, f.engine.CompleteTask(changeCallbackContext(f, f.actor), task.TaskID, map[string]any{"service_task_type": "unknown", "action": "close_change", "version": version, "evidence": "assessed"}))
	row := f.client.ProcessCallbackOutbox.Query().OnlyX(f.ctx)
	require.Equal(t, "change_service_handler", row.HandlerID)
	require.Equal(t, "assess_risk", row.Action)
	require.Equal(t, "completed", row.Status)
	require.Equal(t, "Activity_CABApproval", f.client.ProcessInstance.Query().OnlyX(f.ctx).CurrentActivityID)
}

func TestApprovalGatewayReadsApplicationVariableName(t *testing.T) {
	for _, approval := range []bool{true, false} {
		t.Run(fmt.Sprint(approval), func(t *testing.T) {
			f := newChangeLifecycleFixture(t, "normal")
			if !approval {
				template := f.client.StandardChange.Create().SetTenantID(f.tenant.ID).SetCreatedBy(f.actor.ID).SetTitle("standard").SetJustification("routine").SetImplementationPlan("deploy").SetRollbackPlan("restore").SetIsActive(true).SetApprovalRequired(false).SaveX(f.ctx)
				tx, err := f.runtime.Tx(f.ctx)
				require.NoError(t, err)
				defer tx.Rollback()
				plan, err := f.owner.Prepare(f.ctx, tx, creation.ResolvedIntake{Identity: creation.Identity{TenantID: f.tenant.ID, ActorID: f.actor.ID, Role: "super_admin", Channel: "http"}, Command: creation.CreateWorkItemCommand{Title: "standard", Change: &creation.ChangeInput{StandardTemplateID: &template.ID}}})
				require.NoError(t, err)
				item := tx.Ticket.Create().SetTenantID(f.tenant.ID).SetRequesterID(f.actor.ID).SetOpenedByID(f.actor.ID).SetTitle("standard").SetTicketNumber("CHG-STANDARD-CONSUMER").SetRecordClass("change_request").SetStatus("draft").SaveX(f.ctx)
				ref, err := f.owner.CreateExtension(f.ctx, tx, item, plan)
				require.NoError(t, err)
				require.NoError(t, tx.Commit())
				f.c = f.client.Change.GetX(f.ctx, ref.ID)
			}
			prepareChangeDefaultTask(t, f)
			completeDefaultChangeAction(t, f, "assess", "Activity_Assessment")
			_, err := f.engine.ProcessPendingCallbacks(f.ctx, "policy-consumer", 10)
			require.NoError(t, err)
			instance := f.client.ProcessInstance.Query().OnlyX(f.ctx)
			require.Equal(t, approval, instance.Variables["approval_required"])
			if approval {
				require.Equal(t, "Activity_CABApproval", instance.CurrentActivityID)
			} else {
				require.Equal(t, "Activity_Schedule", instance.CurrentActivityID)
				require.Equal(t, "approved", f.client.Ticket.GetX(f.ctx, f.c.WorkItemID).Status)
				require.Zero(t, f.client.ProcessApprovalDecision.Query().CountX(f.ctx))
			}
		})
	}
}

func TestChangeCallbackBusinessEffectSurvivesAdvanceFailureWithoutReplay(t *testing.T) {
	f := newChangeLifecycleFixture(t, "normal")
	initiator := f.actor
	cab := seedChangeCABActor(t, f)
	prepareChangeDefaultTask(t, f)
	completeDefaultChangeAction(t, f, "assess", "Activity_Assessment")
	f.actor = cab
	completeDefaultChangeAction(t, f, "approve", "Activity_CABApproval")
	f.actor = initiator
	enabled := true
	f.runtime.Use(func(next ent.Mutator) ent.Mutator {
		return ent.MutateFunc(func(ctx context.Context, m ent.Mutation) (ent.Value, error) {
			if enabled {
				if p, ok := m.(*ent.ProcessInstanceMutation); ok {
					if node, exists := p.CurrentActivityID(); exists && node == "Activity_Implement" {
						return nil, errors.New("continuation unavailable")
					}
				}
			}
			return next.Mutate(ctx, m)
		})
	})
	task := f.client.ProcessTask.Query().Where(processtask.TaskDefinitionKey("Activity_Schedule")).OnlyX(f.ctx)
	cmd := f.command("schedule", "durable-schedule")
	start, end := time.Now().Add(-time.Hour), time.Now().Add(time.Hour)
	cmd.PlannedStart = &start
	cmd.PlannedEnd = &end
	accepted, err := f.owner.CompleteChangeTask(f.ctx, changedomain.TaskCommand{Command: cmd, TaskID: task.TaskID})
	require.NoError(t, err)
	require.Equal(t, "pending", accepted.Progress)
	require.NotNil(t, accepted.Result)
	require.Equal(t, "scheduled", accepted.Result.Status)
	first := f.client.Change.GetX(f.ctx, f.c.ID)
	require.False(t, first.PlannedStartDate.IsZero())
	require.False(t, first.PlannedEndDate.IsZero())
	row := f.client.ProcessCallbackOutbox.Query().Where(processcallbackoutbox.ProcessTaskID(task.ID)).OnlyX(f.ctx)
	require.Equal(t, "advance_error", row.LastErrorClass)
	enabled = false
	row.Update().SetNextAttemptAt(time.Now().Add(-time.Minute)).ExecX(f.ctx)
	count, err := f.engine.ProcessPendingCallbacks(f.ctx, "consumer-recovery", 10)
	require.NoError(t, err)
	require.Equal(t, 1, count)
	after := f.client.Change.GetX(f.ctx, f.c.ID)
	require.Equal(t, first.PlannedStartDate, after.PlannedStartDate)
	require.Equal(t, first.PlannedEndDate, after.PlannedEndDate)
	require.Equal(t, accepted.Result.Version, f.client.Ticket.GetX(f.ctx, f.c.WorkItemID).Version)
	require.Equal(t, 1, f.client.AuditLog.Query().Where(auditlog.Action("change.schedule")).CountX(f.ctx))
	require.Equal(t, "completed", f.client.ProcessCallbackOutbox.GetX(f.ctx, row.ID).Status)
	require.Equal(t, "Activity_Implement", f.client.ProcessInstance.Query().OnlyX(f.ctx).CurrentActivityID)
}

// A matching current title is insufficient for replay. Only the same durable
// callback receipt can replay; a stale independent callback remains blocked.
func TestChangeServiceTaskHandler_UpdateChangeCASLoserClassifiesExactEffect(t *testing.T) {
	for _, title := range []string{"same title", "different title"} {
		t.Run(title, func(t *testing.T) {
			f := newChangeLifecycleFixture(t, "normal")
			task := prepareChangeMetadataTask(t, f)
			before := f.client.Ticket.GetX(f.ctx, f.c.WorkItemID)
			f.client.Ticket.UpdateOneID(before.ID).SetTitle("same title").AddVersion(1).ExecX(f.ctx)
			require.NoError(t, f.engine.CompleteTask(changeCallbackContext(f, f.actor), task.TaskID, map[string]any{"title": title, "version": before.Version}))
			require.Equal(t, "same title", f.client.Ticket.GetX(f.ctx, before.ID).Title)
			require.Equal(t, before.Version+1, f.client.Ticket.GetX(f.ctx, before.ID).Version)
			row := f.client.ProcessCallbackOutbox.Query().OnlyX(f.ctx)
			require.NotEqual(t, "completed", row.Status)
			require.Zero(t, f.client.AuditLog.Query().Where(auditlog.Action("change.metadata")).CountX(f.ctx))
			require.Equal(t, "metadata", f.client.ProcessInstance.Query().OnlyX(f.ctx).CurrentActivityID)
		})
	}
}

func TestChangeServiceTaskHandler_UnchangedUpdateRequiresReceipt(t *testing.T) {
	f := newChangeLifecycleFixture(t, "normal")
	task := prepareChangeMetadataTask(t, f)
	before := f.client.Ticket.GetX(f.ctx, f.c.WorkItemID)
	require.NoError(t, f.engine.CompleteTask(changeCallbackContext(f, f.actor), task.TaskID, map[string]any{"title": before.Title, "version": before.Version}))
	row := f.client.ProcessCallbackOutbox.Query().OnlyX(f.ctx)
	require.NotEqual(t, "completed", row.Status)
	require.Equal(t, before.Version, f.client.Ticket.GetX(f.ctx, before.ID).Version)
	require.Zero(t, f.client.AuditLog.Query().Where(auditlog.Action("change.metadata")).CountX(f.ctx))
	require.Equal(t, "metadata", f.client.ProcessInstance.Query().OnlyX(f.ctx).CurrentActivityID)
}

func TestChangeMetadataCallbackPreservesTenantBoundary(t *testing.T) {
	for _, scope := range []string{"own", "foreign", "missing"} {
		t.Run(scope, func(t *testing.T) {
			f := newChangeLifecycleFixture(t, "normal")
			task := prepareChangeMetadataTask(t, f)
			before := f.client.Ticket.GetX(f.ctx, f.c.WorkItemID)
			ctx := changeCallbackContext(f, f.actor)
			if scope == "foreign" {
				ctx = service.WithBPMNAccessScope(f.ctx, service.BPMNAccessScope{UserID: f.actor.ID, TenantID: f.tenant.ID + 999})
			}
			if scope == "missing" {
				ctx = context.Background()
			}
			err := f.engine.CompleteTask(ctx, task.TaskID, map[string]any{"title": "scoped metadata", "version": before.Version})
			if scope == "own" {
				require.NoError(t, err)
				require.Equal(t, "scoped metadata", f.client.Ticket.GetX(f.ctx, before.ID).Title)
				require.Equal(t, 1, f.client.ProcessCallbackOutbox.Query().CountX(f.ctx))
			} else {
				require.Error(t, err)
				require.Equal(t, before.Title, f.client.Ticket.GetX(f.ctx, before.ID).Title)
				require.Zero(t, f.client.ProcessCallbackOutbox.Query().CountX(f.ctx))
			}
		})
	}
}
