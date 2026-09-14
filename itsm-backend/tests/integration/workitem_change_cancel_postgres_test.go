//go:build integration_postgres

package integration

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"github.com/stretchr/testify/require"
	"itsm-backend/authorization"
	"itsm-backend/dto"
	"itsm-backend/ent"
	"itsm-backend/ent/auditlog"
	"itsm-backend/ent/processauditlog"
	"itsm-backend/ent/processdefinition"
	"itsm-backend/ent/processtask"
	changedomain "itsm-backend/handlers/change"
	"itsm-backend/service"
	"os"
	"testing"
)

func TestWorkItemChangeCancellationOwner(t *testing.T) {
	for _, mode := range []string{"cancel", "audit_rollback"} {
		t.Run(mode, func(t *testing.T) {
			f := newChangeLifecycleFixture(t, "normal")
			f.apply(t, f.command("submit", "submit"))
			instance := f.client.ProcessInstance.Query().OnlyX(f.ctx)
			item := f.client.Ticket.GetX(f.ctx, f.c.WorkItemID)
			if mode == "audit_rollback" {
				f.runtime.Use(func(next ent.Mutator) ent.Mutator {
					return ent.MutateFunc(func(ctx context.Context, m ent.Mutation) (ent.Value, error) {
						if _, ok := m.(*ent.AuditLogMutation); ok {
							return nil, errors.New("cancel audit unavailable")
						}
						return next.Mutate(ctx, m)
					})
				})
			}
			cmd := f.command("cancel", "cancel")
			result, err := f.owner.ApplyCommand(f.ctx, cmd)
			if mode == "audit_rollback" {
				require.ErrorContains(t, err, "cancel audit unavailable")
				require.Equal(t, item.Version, f.client.Ticket.GetX(f.ctx, item.ID).Version)
				require.Equal(t, "running", f.client.ProcessInstance.GetX(f.ctx, instance.ID).Status)
				require.Equal(t, "created", f.client.ProcessTask.Query().OnlyX(f.ctx).Status)
				return
			}
			require.NoError(t, err)
			require.Equal(t, "cancelled", result.Status)
			require.Equal(t, "terminated", f.client.ProcessInstance.GetX(f.ctx, instance.ID).Status)
			require.Equal(t, "cancelled", f.client.ProcessTask.Query().OnlyX(f.ctx).Status)
			replay, err := f.owner.ApplyCommand(f.ctx, cmd)
			require.NoError(t, err)
			require.True(t, replay.Replayed)
			require.Equal(t, 1, f.client.AuditLog.Query().Where(auditlog.Action("change.cancel")).CountX(f.ctx))
		})
	}
}

func setChangeWriter(t *testing.T, f *changeLifecycleFixture, msp bool) (*ent.Role, *ent.MSPAllocation) {
	t.Helper()
	code := "agent"
	var allocation *ent.MSPAllocation
	if msp {
		f.client.Tenant.UpdateOneID(f.tenant.ID).SetType("msp_customer").ExecX(f.ctx)
		provider := f.client.Tenant.Create().SetCode("cancel-provider").SetName("Provider").SetType("msp_provider").SaveX(f.ctx)
		actor := f.client.User.Create().SetTenantID(provider.ID).SetUsername("provider").SetName("Actual provider").SetEmail("provider@example.test").SetPasswordHash("test").SetRole("admin").SetMspRole("provider_agent").SetActive(true).SaveX(f.ctx)
		allocation = f.client.MSPAllocation.Create().SetMspUserID(actor.ID).SetCustomerTenantID(f.tenant.ID).SetRole("primary").SaveX(f.ctx)
		f.actor = actor
		code = "msp_tech"
	} else {
		f.actor.Update().SetRole("agent").ExecX(f.ctx)
	}
	role := f.client.Role.Create().SetTenantID(f.tenant.ID).SetCode(code).SetName("Writer").SetIsActive(true).SaveX(f.ctx)
	permission := f.client.Permission.Create().SetTenantID(f.tenant.ID).SetCode("change:write").SetName("Write").SetResource("change").SetAction("write").SaveX(f.ctx)
	f.client.RolePermission.Create().SetTenantID(f.tenant.ID).SetRoleID(role.ID).SetPermissionID(permission.ID).SaveX(f.ctx)
	authorization.InvalidateAllPermissionCaches()
	t.Cleanup(authorization.InvalidateAllPermissionCaches)
	return role, allocation
}
func TestWorkItemChangeCancellationAuthority(t *testing.T) {
	for _, mode := range []string{"native_write_only", "msp", "revoked_permission", "revoked_allocation", "foreign", "foreign_native_actor", "forged", "unrelated", "engine_audit_rollback"} {
		t.Run(mode, func(t *testing.T) {
			f := newChangeLifecycleFixture(t, "normal")
			f.apply(t, f.command("submit", "submit"))
			instance := f.client.ProcessInstance.Query().OnlyX(f.ctx)
			role, allocation := setChangeWriter(t, f, mode != "native_write_only")
			cmd := f.command("cancel", "cancel")
			switch mode {
			case "revoked_permission":
				role.Update().SetIsActive(false).ExecX(f.ctx)
			case "revoked_allocation":
				f.client.MSPAllocation.DeleteOne(allocation).ExecX(f.ctx)
			case "foreign":
				cmd.Meta.TenantID++
			case "foreign_native_actor":
				tenant := f.client.Tenant.Create().SetCode("foreign-native").SetName("Foreign").SaveX(f.ctx)
				actor := f.client.User.Create().SetTenantID(tenant.ID).SetUsername("foreign").SetName("Foreign").SetEmail("foreign@example.test").SetPasswordHash("test").SetRole("admin").SetActive(true).SaveX(f.ctx)
				cmd.Meta.ActorID = actor.ID
			case "forged":
				cmd.Meta.ActorID += 10000
			case "unrelated":
				instance.Update().SetBusinessID(f.c.WorkItemID + 100).ExecX(f.ctx)
			case "engine_audit_rollback":
				f.runtime.Use(func(next ent.Mutator) ent.Mutator {
					return ent.MutateFunc(func(ctx context.Context, m ent.Mutation) (ent.Value, error) {
						if _, ok := m.(*ent.ProcessAuditLogMutation); ok {
							return nil, errors.New("engine audit unavailable")
						}
						return next.Mutate(ctx, m)
					})
				})
			}
			_, err := f.owner.ApplyCommand(f.ctx, cmd)
			if mode != "native_write_only" && mode != "msp" {
				require.Error(t, err)
				require.Equal(t, "running", f.client.ProcessInstance.GetX(f.ctx, instance.ID).Status)
				require.Equal(t, "submitted", f.client.Ticket.GetX(f.ctx, f.c.WorkItemID).Status)
				return
			}
			require.NoError(t, err)
			audit := f.client.ProcessAuditLog.Query().Where(processauditlog.Action(service.AuditActionProcessTerminated)).OnlyX(f.ctx)
			require.Equal(t, f.actor.ID, audit.UserID)
			require.Equal(t, f.actor.Name, audit.UserName)
			role.Update().SetIsActive(false).ExecX(f.ctx)
			_, err = f.owner.ApplyCommand(f.ctx, cmd)
			require.Error(t, err, "revoked actor cannot replay")
		})
	}
}
func prepareChangeDefaultTask(t *testing.T, f *changeLifecycleFixture) *ent.ProcessTask {
	t.Helper()
	xml, err := os.ReadFile("../../service/bpmn/change_normal_flow.bpmn")
	require.NoError(t, err)
	f.client.ProcessDefinition.Update().Where(processdefinition.Key("change_normal_flow")).SetBpmnXML(xml).ExecX(f.ctx)
	f.apply(t, f.command("submit", "submit"))
	return f.client.ProcessTask.Query().Where(processtask.TaskDefinitionKey("Activity_Assessment")).OnlyX(f.ctx)
}
func TestWorkItemChangeCancellationCallbackFence(t *testing.T) {
	f := newChangeLifecycleFixture(t, "normal")
	task := prepareChangeDefaultTask(t, f)
	f.runtime.Use(func(next ent.Mutator) ent.Mutator {
		return ent.MutateFunc(func(ctx context.Context, m ent.Mutation) (ent.Value, error) {
			if a, ok := m.(*ent.AuditLogMutation); ok {
				action, _ := a.Action()
				if action == "change.assess" {
					return nil, errors.New("callback audit unavailable")
				}
			}
			return next.Mutate(ctx, m)
		})
	})
	accepted, err := f.owner.CompleteChangeTask(f.ctx, changedomain.TaskCommand{Command: f.command("assess", "accept"), TaskID: task.TaskID})
	require.NoError(t, err)
	require.Equal(t, "pending", accepted.Progress)
	row := f.client.ProcessCallbackOutbox.Query().OnlyX(f.ctx)
	for _, status := range []string{"pending", "processing", "blocked"} {
		row.Update().SetStatus(status).ExecX(f.ctx)
		_, err = f.owner.ApplyCommand(f.ctx, f.command("cancel", "cancel-"+status))
		require.ErrorContains(t, err, "prior callback")
		require.Equal(t, "running", f.client.ProcessInstance.Query().OnlyX(f.ctx).Status)
		// Public termination uses the same guarded transaction and retains scope authorization.
		scope := service.WithBPMNAccessScope(f.ctx, service.BPMNAccessScope{UserID: f.actor.ID, TenantID: f.tenant.ID, CanUpdateAllInstances: true})
		require.ErrorContains(t, f.engine.TerminateProcess(scope, fmt.Sprint(row.ProcessInstanceID), "cancel"), "prior callback")
		_, err = f.pirOwner.CreatePIR(f.ctx, &dto.CreateChangePIRRequest{ChangeID: f.c.ID, OverallResult: "successful"}, f.command("pir", "pir-"+status).Meta)
		require.ErrorContains(t, err, "prior callback")
	}
	require.Equal(t, 2, f.client.Ticket.GetX(f.ctx, f.c.WorkItemID).Version)
}
func TestWorkItemChangeCancelTaskRace(t *testing.T) {
	f := newChangeLifecycleFixture(t, "normal")
	task := prepareChangeDefaultTask(t, f)
	cancel := f.command("cancel", "cancel-race")
	accept := changedomain.TaskCommand{Command: f.command("assess", "accept-race"), TaskID: task.TaskID}
	synchronizeChangeOwnerReads(t, f)
	results := make(chan error, 2)
	go func() { _, err := f.owner.ApplyCommand(f.ctx, cancel); results <- err }()
	go func() { _, err := f.owner.CompleteChangeTask(f.ctx, accept); results <- err }()
	first, second := <-results, <-results
	require.NotEqual(t, first == nil, second == nil, "exactly one owner must win")
	item := f.client.Ticket.GetX(f.ctx, f.c.WorkItemID)
	require.Equal(t, 3, item.Version)
	instance := f.client.ProcessInstance.Query().OnlyX(f.ctx)
	if item.Status == "cancelled" {
		require.Equal(t, "terminated", instance.Status)
		require.Zero(t, f.client.ProcessCallbackOutbox.Query().CountX(f.ctx))
	} else {
		require.Equal(t, "running", instance.Status)
		require.Equal(t, "completed", f.client.ProcessCallbackOutbox.Query().OnlyX(f.ctx).Status)
	}
}

func TestWorkItemChangeTerminationTxPort(t *testing.T) {
	f := newChangeLifecycleFixture(t, "normal")
	f.apply(t, f.command("submit", "submit"))
	instance := f.client.ProcessInstance.Query().OnlyX(f.ctx)
	scope := service.WithBPMNAccessScope(f.ctx, service.BPMNAccessScope{UserID: f.actor.ID, TenantID: f.tenant.ID})
	require.Error(t, f.engine.TerminateProcess(scope, instance.ProcessInstanceID, "denied"))
	scope = service.WithBPMNAccessScope(f.ctx, service.BPMNAccessScope{UserID: f.actor.ID, TenantID: f.tenant.ID, CanUpdateAllInstances: true})
	weak, err := f.runtime.BeginTx(f.ctx, &sql.TxOptions{})
	require.NoError(t, err)
	require.ErrorContains(t, f.engine.TerminateProcessTx(scope, weak, instance.ProcessInstanceID, "weak"), "repeatable read")
	require.NoError(t, weak.Rollback())
	tx, err := f.runtime.BeginTx(f.ctx, &sql.TxOptions{Isolation: sql.LevelRepeatableRead})
	require.NoError(t, err)
	require.NoError(t, f.engine.TerminateProcessTx(scope, tx, instance.ProcessInstanceID, "rollback"))
	require.Equal(t, "terminated", tx.ProcessInstance.GetX(f.ctx, instance.ID).Status)
	require.NoError(t, tx.Rollback())
	require.Equal(t, "running", f.client.ProcessInstance.GetX(f.ctx, instance.ID).Status)
	require.Zero(t, f.client.ProcessAuditLog.Query().Where(processauditlog.Action(service.AuditActionProcessTerminated)).CountX(f.ctx))
}
