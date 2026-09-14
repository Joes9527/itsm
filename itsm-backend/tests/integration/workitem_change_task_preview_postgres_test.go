//go:build integration_postgres

package integration

import (
	"database/sql"
	"fmt"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
	"itsm-backend/authorization"
	"itsm-backend/ent"
)

func TestWorkItemChangeTaskPreviewDoesNotWrite(t *testing.T) {
	f := newChangeLifecycleFixture(t, "normal")
	f.apply(t, f.command("submit", "submit"))
	task := f.client.ProcessTask.Query().OnlyX(f.ctx)
	instance := f.client.ProcessInstance.Query().OnlyX(f.ctx)
	before := f.client.ProcessAuditLog.Query().CountX(f.ctx)
	tx, err := f.runtime.BeginTx(f.ctx, &sql.TxOptions{Isolation: sql.LevelRepeatableRead, ReadOnly: true})
	require.NoError(t, err)
	defer tx.Rollback()
	require.NoError(t, f.engine.CheckTaskCompletionTx(changeCallbackContext(f, f.actor), tx, task.TaskID))
	require.NoError(t, tx.Commit())
	require.Equal(t, task.Status, f.client.ProcessTask.GetX(f.ctx, task.ID).Status)
	require.Equal(t, instance.Version, f.client.ProcessInstance.GetX(f.ctx, instance.ID).Version)
	require.Equal(t, before, f.client.ProcessAuditLog.Query().CountX(f.ctx))
	require.Zero(t, f.client.ProcessCallbackOutbox.Query().CountX(f.ctx))
}

func seedChangeHTTPMSP(t *testing.T, f *changeLifecycleFixture) (*ent.User, *ent.MSPAllocation, *ent.Role) {
	t.Helper()
	f.client.Tenant.UpdateOneID(f.tenant.ID).SetType("msp_customer").ExecX(f.ctx)
	provider := f.client.Tenant.Create().SetCode("http-provider").SetName("Provider").SetType("msp_provider").SaveX(f.ctx)
	actor := f.client.User.Create().SetTenantID(provider.ID).SetUsername("http-provider").SetName("Provider agent").SetEmail("http-provider@example.test").SetPasswordHash("test").SetRole("admin").SetMspRole("provider_agent").SetActive(true).SaveX(f.ctx)
	allocation := f.client.MSPAllocation.Create().SetMspUserID(actor.ID).SetCustomerTenantID(f.tenant.ID).SetRole("primary").SaveX(f.ctx)
	role := f.client.Role.Create().SetTenantID(f.tenant.ID).SetCode("msp_tech").SetName("MSP technician").SetIsActive(true).SaveX(f.ctx)
	for _, action := range []string{"read", "write"} {
		permission := f.client.Permission.Create().SetTenantID(f.tenant.ID).SetCode("change:" + action).SetName("Change " + action).SetResource("change").SetAction(action).SaveX(f.ctx)
		f.client.RolePermission.Create().SetTenantID(f.tenant.ID).SetRoleID(role.ID).SetPermissionID(permission.ID).SaveX(f.ctx)
	}
	authorization.InvalidateAllPermissionCaches()
	t.Cleanup(authorization.InvalidateAllPermissionCaches)
	return actor, allocation, role
}

func TestWorkItemChangeTaskPreviewActorParity(t *testing.T) {
	for _, mode := range []string{"native", "msp", "revoked", "nonparticipant"} {
		t.Run(mode, func(t *testing.T) {
			f := newChangeLifecycleFixture(t, "normal")
			var allocation *ent.MSPAllocation
			if mode == "msp" || mode == "revoked" {
				f.actor, allocation, _ = seedChangeHTTPMSP(t, f)
			}
			task := prepareChangeDefaultTask(t, f)
			if mode == "revoked" {
				allocation.Update().SetDeassignedAt(time.Now()).ExecX(f.ctx)
			}
			if mode == "nonparticipant" {
				f.actor = f.client.User.Create().SetTenantID(f.tenant.ID).SetUsername("preview-outsider").SetName("Outsider").SetEmail("preview-outsider@example.test").SetPasswordHash("test").SetRole("super_admin").SetActive(true).SaveX(f.ctx)
			}
			before := f.client.Ticket.GetX(f.ctx, f.c.WorkItemID)
			audits := f.client.ProcessAuditLog.Query().CountX(f.ctx)
			instance := f.client.ProcessInstance.Query().OnlyX(f.ctx)
			tx, err := f.runtime.BeginTx(f.ctx, &sql.TxOptions{Isolation: sql.LevelRepeatableRead, ReadOnly: true})
			require.NoError(t, err)
			previewErr := f.engine.CheckTaskCompletionTx(changeCallbackContext(f, f.actor), tx, task.TaskID)
			require.NoError(t, tx.Rollback())
			allowed := mode == "native" || mode == "msp"
			require.Equal(t, allowed, previewErr == nil, fmt.Sprint(previewErr))
			require.Equal(t, before.Version, f.client.Ticket.GetX(f.ctx, before.ID).Version)
			require.Equal(t, task.Status, f.client.ProcessTask.GetX(f.ctx, task.ID).Status)
			require.Equal(t, instance.Version, f.client.ProcessInstance.GetX(f.ctx, instance.ID).Version)
			require.Equal(t, audits, f.client.ProcessAuditLog.Query().CountX(f.ctx))
			require.Zero(t, f.client.ProcessCallbackOutbox.Query().CountX(f.ctx))
			r, h := changeHTTPFixture(f)
			r.GET("/changes/:id", h.GetChange)
			r.POST("/changes/:id/assess", h.ExecuteAction)
			view := changeHTTPCall(r, "GET", fmt.Sprintf("/changes/%d", f.c.ID), "")
			if mode == "revoked" {
				require.Equal(t, 403, view.Code, view.Body.String())
			} else {
				require.Equal(t, 200, view.Code, view.Body.String())
				_, actions, _, err := f.owner.GetChangeActionView(f.ctx, f.c.ID, f.command("read", "read").Meta)
				require.NoError(t, err)
				require.Equal(t, allowed, actions["assess"].Allowed)
			}
			response := changeHTTPCall(r, "POST", fmt.Sprintf("/changes/%d/assess", f.c.ID), fmt.Sprintf(`{"expectedVersion":%d,"operationId":"preview-execution","taskId":%q,"evidence":"observed assessment"}`, before.Version, task.TaskID))
			if allowed {
				require.Equal(t, 200, response.Code, response.Body.String())
				require.Equal(t, f.actor.ID, f.client.Change.GetX(f.ctx, f.c.ID).AssessedBy)
			} else {
				require.Equal(t, 403, response.Code, response.Body.String())
				require.Equal(t, before.Version, f.client.Ticket.GetX(f.ctx, before.ID).Version)
				require.Zero(t, f.client.ProcessCallbackOutbox.Query().CountX(f.ctx))
			}
		})
	}
}
