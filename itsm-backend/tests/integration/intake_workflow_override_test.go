package integration

import (
	"context"
	"errors"
	"fmt"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
	"go.uber.org/zap"
	"itsm-backend/controller"
	"itsm-backend/ent"
	"itsm-backend/ent/processbinding"
	"itsm-backend/service"
	"itsm-backend/service/bpmn"
)

// Replace the general fixture grant once, before requests; replay never changes RBAC.
func restrictEntryPermissions(t *testing.T, f *unifiedIntakeFixture) {
	t.Helper()
	ctx := context.Background()
	f.client.RolePermission.Delete().ExecX(ctx)
	for _, resource := range []string{"ticket", "incident", "service_request", "service_catalog"} {
		for _, action := range []string{"read", "write", "create_on_behalf"} {
			grantEntryPermission(t, f, resource, action)
		}
	}
}
func grantEntryPermission(t *testing.T, f *unifiedIntakeFixture, resource, action string) *ent.RolePermission {
	t.Helper()
	ctx := context.Background()
	p := f.client.Permission.Create().SetTenantID(f.identity.TenantID).SetCode(resource + ":" + action).SetName(resource + action).SetResource(resource).SetAction(action).SaveX(ctx)
	return f.client.RolePermission.Create().SetTenantID(f.identity.TenantID).SetRoleID(f.client.Role.Query().OnlyX(ctx).ID).SetPermissionID(p.ID).SaveX(ctx)
}
func entryDefinition(t *testing.T, f *unifiedIntakeFixture, key string, tenantID int, xml string) *ent.ProcessDefinition {
	t.Helper()
	ctx := context.Background()
	if xml == "" {
		xml = fmt.Sprintf(`<definitions xmlns="http://www.omg.org/spec/BPMN/20100524/MODEL"><process id="%s" isExecutable="true"><startEvent id="start"/><userTask id="approval" name="Approval"/><endEvent id="end"/><sequenceFlow id="a" sourceRef="start" targetRef="approval"/><sequenceFlow id="b" sourceRef="approval" targetRef="end"/></process></definitions>`, key)
	}
	d := f.client.ProcessDeployment.Create().SetTenantID(tenantID).SetDeploymentID(key).SetDeploymentName(key).SaveX(ctx)
	return f.client.ProcessDefinition.Create().SetTenantID(tenantID).SetDeploymentID(d.ID).SetKey(key).SetName(key).SetVersion("1").SetIsActive(true).SetIsLatest(true).SetBpmnXML([]byte(xml)).SaveX(ctx)
}
func bindEntryDefinition(t *testing.T, f *unifiedIntakeFixture, business, key string) {
	t.Helper()
	ctx := context.Background()
	f.client.ProcessBinding.Delete().Where(processbinding.TenantIDEQ(f.identity.TenantID), processbinding.BusinessTypeEQ(business)).ExecX(ctx)
	f.client.ProcessBinding.Create().SetTenantID(f.identity.TenantID).SetBusinessType(business).SetIsDefault(true).SetProcessDefinitionKey(key).SaveX(ctx)
}
func TestIntakeWorkflowOverrideCurrentPermission(t *testing.T) {
	f := newUnifiedIntakeFixture(t)
	restrictEntryPermissions(t, f)
	ctx := context.Background()
	entryDefinition(t, f, "override", f.identity.TenantID, "")
	command := f.command
	command.WorkflowDefinitionKey = "override"
	// Public source labels are never delegation authority.
	for _, channel := range []string{"itsm_web", "bpmn"} {
		identity := f.identity
		identity.Channel = channel
		_, err := f.app.Create(ctx, identity, command)
		require.ErrorContains(t, err, "workflow:write")
		assertNoEntryGraph(t, f.client)
	}
	grant := grantEntryPermission(t, f, "workflow", "write")
	first, err := f.app.Create(ctx, f.identity, command)
	require.NoError(t, err)
	replay, err := f.app.Create(ctx, f.identity, command)
	require.NoError(t, err)
	require.True(t, replay.Replayed)
	require.Equal(t, first.WorkItemID, replay.WorkItemID)
	f.client.RolePermission.DeleteOne(grant).ExecX(ctx)
	_, err = f.app.Create(ctx, f.identity, command)
	require.ErrorContains(t, err, "workflow:write")
	require.Equal(t, 1, f.client.Ticket.Query().CountX(ctx))
	require.Equal(t, 1, f.client.IntakeRequest.Query().CountX(ctx))
}
func TestIntakeHTTPWorkflowOverridePermissionAndTenant(t *testing.T) {
	f := newUnifiedIntakeFixture(t)
	restrictEntryPermissions(t, f)
	entryDefinition(t, f, "override", f.identity.TenantID, "")
	h := controller.NewTicketController(nil, nil, nil, f.client, zap.NewNop().Sugar())
	h.SetCreationApplication(f.app)
	body := `{"title":"Override process","description":"Explicit process","priority":"high","workflowDefinitionKey":"override"}`
	w, _ := intakeHTTP(t, f, h.CreateTicket, body, "override-http", nil)
	require.Equal(t, 403, w.Code, w.Body.String())
	assertNoEntryGraph(t, f.client)
	grant := grantEntryPermission(t, f, "workflow", "write")
	w, first := intakeHTTP(t, f, h.CreateTicket, body, "override-http", nil)
	require.Equal(t, 201, w.Code, w.Body.String())
	w, replay := intakeHTTP(t, f, h.CreateTicket, body, "override-http", nil)
	require.Equal(t, 200, w.Code, w.Body.String())
	require.Equal(t, first.WorkItemID, replay.WorkItemID)
	f.client.RolePermission.DeleteOne(grant).ExecX(context.Background())
	w, _ = intakeHTTP(t, f, h.CreateTicket, body, "override-http", nil)
	require.Equal(t, 403, w.Code, w.Body.String())
}
func TestIntakeWorkflowOverrideCrossTenantGrantAndDefinition(t *testing.T) {
	f := newUnifiedIntakeFixture(t)
	restrictEntryPermissions(t, f)
	ctx := context.Background()
	other := f.client.Tenant.Create().SetCode("other").SetName("Other").SaveX(ctx)
	definition := entryDefinition(t, f, "foreign", other.ID, "")
	command := f.command
	command.WorkflowDefinitionKey = definition.Key
	p := f.client.Permission.Create().SetTenantID(other.ID).SetCode("workflow:write").SetName("Workflow").SetResource("workflow").SetAction("write").SaveX(ctx)
	link := f.client.RolePermission.Create().SetTenantID(f.identity.TenantID).SetRoleID(f.client.Role.Query().OnlyX(ctx).ID).SetPermissionID(p.ID).SaveX(ctx)
	_, err := f.app.Create(ctx, f.identity, command)
	require.ErrorContains(t, err, "workflow:write")
	assertNoEntryGraph(t, f.client)
	f.client.RolePermission.DeleteOne(link).ExecX(ctx)
	grantEntryPermission(t, f, "workflow", "write")
	_, err = f.app.Create(ctx, f.identity, command)
	require.Error(t, err)
	assertNoEntryGraph(t, f.client)
}
func TestIntakeBPMNRuntimeWorkflowOverrideRequiresCurrentPermission(t *testing.T) {
	for _, mode := range []string{"denied", "permitted", "revoked_on_replay"} {
		t.Run(mode, func(t *testing.T) {
			f := newUnifiedIntakeFixture(t)
			restrictEntryPermissions(t, f)
			ctx := context.Background()
			source, err := f.app.Create(ctx, f.identity, f.command)
			require.NoError(t, err)
			entryDefinition(t, f, "child", f.identity.TenantID, "")
			var grant *ent.RolePermission
			if mode != "denied" {
				grant = grantEntryPermission(t, f, "workflow", "write")
			}
			failed := false
			if mode == "revoked_on_replay" {
				f.client.ProcessCallbackOutbox.Use(func(next ent.Mutator) ent.Mutator {
					return ent.MutateFunc(func(ctx context.Context, m ent.Mutation) (ent.Value, error) {
						if typed, ok := m.(*ent.ProcessCallbackOutboxMutation); ok && !failed {
							if status, ok := typed.Status(); ok && status == "completed" {
								failed = true
								return nil, errors.New("injected callback acknowledgement failure")
							}
						}
						return next.Mutate(ctx, m)
					})
				})
			}
			xml := `<definitions xmlns="http://www.omg.org/spec/BPMN/20100524/MODEL"><process id="source" isExecutable="true"><startEvent id="start"/><serviceTask id="create"><extensionElements><metaData name="service_task_type">incident_task</metaData><metaData name="action">create_incident</metaData></extensionElements></serviceTask><endEvent id="end"/><sequenceFlow id="a" sourceRef="start" targetRef="create"/><sequenceFlow id="b" sourceRef="create" targetRef="end"/></process></definitions>`
			definition := entryDefinition(t, f, "source", f.identity.TenantID, xml)
			engine := service.NewCustomProcessEngine(f.client, zap.NewNop().Sugar()).(*service.CustomProcessEngine)
			engine.CallbackRegistry().GetHandler("incident_service_handler").(*bpmn.IncidentServiceTaskHandler).SetCreationApplication(f.app, f.client)
			ctx = service.WithTrustedBPMNTenantContext(ctx, f.identity.TenantID)
			ctx = context.WithValue(ctx, bpmn.BPMNUserIDContextKey, f.identity.ActorID)
			_, err = engine.StartProcessByDefinitionID(ctx, service.FreezeProcessDefinition(definition), fmt.Sprintf("ticket:%d", source.WorkItemID), "generic", source.WorkItemID, map[string]any{"title": "Callback incident", "workflow_definition_key": "child", "priority": "high"}, "source-start")
			require.NoError(t, err)
			callback := f.client.ProcessCallbackOutbox.Query().OnlyX(ctx)
			if mode == "revoked_on_replay" {
				require.True(t, failed)
				require.NotEqual(t, "completed", callback.Status)
				require.Equal(t, 2, f.client.Ticket.Query().CountX(ctx))
				require.Equal(t, 2, f.client.IntakeRequest.Query().CountX(ctx))
				f.client.RolePermission.DeleteOne(grant).ExecX(ctx)
				callback.Update().SetNextAttemptAt(time.Now().Add(-time.Second)).ExecX(ctx)
				_, err = engine.ProcessPendingCallbacks(ctx, "revoked-override", 10)
				require.NoError(t, err)
				callback = f.client.ProcessCallbackOutbox.GetX(ctx, callback.ID)
				require.Equal(t, "blocked", callback.Status)
				require.Equal(t, 2, f.client.Ticket.Query().CountX(ctx))
				require.Equal(t, 2, f.client.IntakeRequest.Query().CountX(ctx))
				require.Equal(t, 1, f.client.Incident.Query().CountX(ctx))
			} else if mode == "permitted" {
				require.Equal(t, "completed", callback.Status)
				require.Equal(t, 2, f.client.Ticket.Query().CountX(ctx))
			} else {
				require.Equal(t, "blocked", callback.Status)
				require.Zero(t, f.client.Incident.Query().CountX(ctx))
				require.Equal(t, 1, f.client.IntakeRequest.Query().CountX(ctx))
				require.Zero(t, f.client.OutboxEvent.Query().CountX(ctx))
			}
		})
	}
}

func TestIntakeWorkflowOverridePreservesSuperAdminSemantics(t *testing.T) {
	f := newUnifiedIntakeFixture(t)
	ctx := context.Background()
	f.client.RolePermission.Delete().ExecX(ctx)
	f.client.User.UpdateOneID(f.identity.ActorID).SetRole("super_admin").ExecX(ctx)
	f.identity.Role = "super_admin"
	entryDefinition(t, f, "override", f.identity.TenantID, "")
	command := f.command
	command.WorkflowDefinitionKey = "override"
	result, err := f.app.Create(ctx, f.identity, command)
	require.NoError(t, err)
	require.Equal(t, "pending", result.WorkflowStartStatus)
	// The existing super_admin permission bypass does not bypass actor tenant validation.
	other := f.client.Tenant.Create().SetName("Other").SetCode("other").SaveX(ctx)
	f.identity.TenantID = other.ID
	_, err = f.app.Create(ctx, f.identity, command)
	require.Error(t, err)
	require.Equal(t, 1, f.client.IntakeRequest.Query().CountX(ctx))
}
