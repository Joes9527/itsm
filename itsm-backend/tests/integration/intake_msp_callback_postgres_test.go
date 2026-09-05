//go:build integration_postgres

package integration

import (
	"context"
	"errors"
	"fmt"
	"github.com/stretchr/testify/require"
	"go.uber.org/zap"
	"itsm-backend/common/tenantctx"
	"itsm-backend/database"
	"itsm-backend/ent"
	"itsm-backend/ent/intakerequest"
	changedomain "itsm-backend/handlers/change"
	"itsm-backend/handlers/intake"
	catalogdomain "itsm-backend/handlers/service_catalog"
	"itsm-backend/migration"
	"itsm-backend/repository/workitemnumber"
	"itsm-backend/service"
	"itsm-backend/service/bpmn"
	"strings"
	"testing"
	"time"
)

// Uses the real Runtime worker, professional creator and imported System snapshot.
// Source process/callback fixtures represent a process already paused at its declared service task.
func TestPostgresIntakeMSPCreationCallbacks(t *testing.T) {
	for _, kind := range []string{"incident", "change"} {
		t.Run(kind, func(t *testing.T) {
			f := newIncidentEffectsFixture(t)
			migrator := migration.NewMigrator(f.db, zap.NewNop().Sugar())
			require.NoError(t, migrator.EnsureMigrationsTable(f.ctx))
			_, err := migrator.RunMigrations(f.ctx, migration.PostSchemaMigrations())
			require.NoError(t, err)
			provider := f.client.Tenant.Create().SetCode("provider").SetName("Provider").SetType("msp_provider").SaveX(f.ctx)
			f.client.Tenant.UpdateOneID(f.tenant.ID).SetType("msp_customer").ExecX(f.ctx)
			actor := f.client.User.Create().SetTenantID(provider.ID).SetUsername("callback-operator").SetName("Original callback operator").SetEmail("callback@example.test").SetPasswordHash("unused").SetRole("admin").SetMspRole("provider_agent").SaveX(f.ctx)
			allocation := f.client.MSPAllocation.Create().SetMspUserID(actor.ID).SetCustomerTenantID(f.tenant.ID).SetRole("primary").SaveX(f.ctx)
			role := f.client.Role.Create().SetTenantID(f.tenant.ID).SetCode("msp_tech").SetName("Customer creator").SaveX(f.ctx)
			var writeGrant *ent.RolePermission
			for _, action := range []string{"read", "write", "create_on_behalf"} {
				permission := f.client.Permission.Create().SetTenantID(f.tenant.ID).SetCode(kind + ":" + action).SetName(action).SetResource(kind).SetAction(action).SaveX(f.ctx)
				grant := f.client.RolePermission.Create().SetTenantID(f.tenant.ID).SetRoleID(role.ID).SetPermissionID(permission.ID).SaveX(f.ctx)
				if action == "write" {
					writeGrant = grant
				}
			}
			f.client.ProcessBinding.Create().SetTenantID(f.tenant.ID).SetBusinessType(kind).SetIsDefault(true).SetProcessDefinitionKey("none").SetConditions(map[string]any{"no_process": true}).SaveX(f.ctx)
			clients, cfg := runtimeClients(t, f)
			for _, table := range []string{"changes", "intake_resolution_snapshots", "work_item_number_sequences", "process_bindings", "process_definitions", "process_deployments", "process_instances", "process_tasks", "process_audit_logs", "process_callback_outboxes", "sla_definitions", "field_definitions", "field_values", "service_catalogs", "configuration_items", "groups"} {
				_, err := f.db.ExecContext(f.ctx, "GRANT SELECT,INSERT,UPDATE,DELETE ON "+table+" TO "+cfg.User)
				require.NoError(t, err)
				var seq *string
				require.NoError(t, f.db.QueryRowContext(f.ctx, "SELECT pg_get_serial_sequence($1,'id')", table).Scan(&seq))
				if seq != nil {
					_, err = f.db.ExecContext(f.ctx, "GRANT USAGE ON SEQUENCE "+*seq+" TO "+cfg.User)
					require.NoError(t, err)
				}
			}
			ctx := service.WithTrustedBPMNTenantContext(tenantctx.WithTenantID(f.ctx, f.tenant.ID), f.tenant.ID)
			_, err = clients.Tenant.User.Get(ctx, actor.ID)
			require.True(t, ent.IsNotFound(err))
			logger := zap.NewNop().Sugar()
			registry := intake.NewCreatorRegistry()
			require.NoError(t, registry.Register(service.NewIncidentService(clients.Tenant, logger)))
			require.NoError(t, registry.Register(changedomain.NewService(nil, clients.Tenant, logger)))
			resolver := intake.NewResolver(catalogdomain.NewService(nil, clients.Tenant, logger), service.NewProcessBindingService(clients.Tenant), service.NewConfigurationItemService(clients.Tenant, logger, nil, nil), service.NewTicketCategoryService(clients.Tenant))
			app := intake.NewService(clients.Tenant, resolver, registry, intake.NewWorkItemCreator(workitemnumber.NewPostgreSQLAllocator()), clients.IntakeDirectorySnapshot())
			engine := service.NewCustomProcessEngine(clients.Tenant, logger).(*service.CustomProcessEngine)
			setDirectory := func(directory *ent.Client) {
				if kind == "incident" {
					engine.CallbackRegistry().GetHandler("incident_service_handler").(*bpmn.IncidentServiceTaskHandler).SetCreationApplication(app, directory)
				} else {
					engine.CallbackRegistry().GetHandler("change_service_handler").(*bpmn.ChangeServiceTaskHandler).SetCreationApplication(app, directory)
				}
			}
			setDirectory(clients.System)
			deployment := f.client.ProcessDeployment.Create().SetTenantID(f.tenant.ID).SetDeploymentID("callback").SetDeploymentName("Callback").SaveX(f.ctx)
			xml := []byte(fmt.Sprintf(`<bpmn:definitions xmlns:bpmn="http://www.omg.org/spec/BPMN/20100524/MODEL" targetNamespace="test"><bpmn:process id="creation" isExecutable="true"><bpmn:startEvent id="start"/><bpmn:serviceTask id="create"><bpmn:extensionElements><bpmn:metaData name="service_task_type">%s_task</bpmn:metaData><bpmn:metaData name="action">create_%s</bpmn:metaData></bpmn:extensionElements></bpmn:serviceTask><bpmn:endEvent id="end"/><bpmn:sequenceFlow id="a" sourceRef="start" targetRef="create"/><bpmn:sequenceFlow id="b" sourceRef="create" targetRef="end"/></bpmn:process></bpmn:definitions>`, kind, kind))
			definition := f.client.ProcessDefinition.Create().SetTenantID(f.tenant.ID).SetDeploymentID(deployment.ID).SetKey("creation").SetName("Creation").SetBpmnXML(xml).SetIsActive(true).SaveX(f.ctx)
			failAck := false
			clients.Tenant.ProcessCallbackOutbox.Use(func(next ent.Mutator) ent.Mutator {
				return ent.MutateFunc(func(ctx context.Context, m ent.Mutation) (ent.Value, error) {
					if typed, ok := m.(*ent.ProcessCallbackOutboxMutation); ok && failAck {
						if status, ok := typed.Status(); ok && status == "completed" {
							failAck = false
							return nil, errors.New("injected callback acknowledgement failure")
						}
					}
					return next.Mutate(ctx, m)
				})
			})
			for _, stage := range []string{"ack_then_allocation_revoked", "ack_then_permission_revoked", "allocation_revoked", "permission_revoked", "role_changed", "missing_directory", "requester_omitted", "success"} {
				t.Run(stage, func(t *testing.T) {
					values := map[string]any{"title": "Callback child", "description": "Declared callback work", "priority": "high", "reporter_id": f.actor.ID}
					if kind == "change" {
						delete(values, "reporter_id")
						values["created_by"] = f.actor.ID
						values["justification"] = "Reviewed configuration"
						values["impact_scope"] = "low"
						values["risk_level"] = "medium"
						values["implementation_plan"] = "Apply and verify"
						values["rollback_plan"] = "Restore and verify"
					}
					if stage == "requester_omitted" {
						delete(values, "reporter_id")
						delete(values, "created_by")
					}
					instance := f.client.ProcessInstance.Create().SetTenantID(f.tenant.ID).SetProcessDefinitionID(definition.ID).SetProcessDefinitionKey(definition.Key).SetProcessInstanceID(kind + stage).SetStatus("running").SetCurrentActivityID("create").SetInitiator(fmt.Sprint(actor.ID)).SetBusinessType("incident").SetBusinessID(f.inc.WorkItemID).SetVariables(values).SaveX(f.ctx)
					row := f.client.ProcessCallbackOutbox.Create().SetTenantID(f.tenant.ID).SetExecutionKey(kind + ":" + stage).SetProcessInstanceID(instance.ID).SetCallbackKind("service_task").SetHandlerID(kind + "_service_handler").SetTaskType(kind + "_task").SetElementID("create").SetAction("create_" + kind).SetVariables(values).SaveX(f.ctx)
					before := clients.Tenant.Ticket.Query().CountX(ctx)
					beforeReceipts := clients.Tenant.IntakeRequest.Query().CountX(ctx)
					if stage == "allocation_revoked" {
						f.client.MSPAllocation.UpdateOneID(allocation.ID).SetDeassignedAt(time.Now()).ExecX(f.ctx)
					}
					if stage == "permission_revoked" {
						f.client.RolePermission.DeleteOne(writeGrant).ExecX(f.ctx)
					}
					if strings.HasPrefix(stage, "ack_then_") {
						failAck = true
					}
					if stage == "role_changed" {
						f.client.User.UpdateOneID(actor.ID).SetMspRole("provider_admin").ExecX(f.ctx)
					}
					if stage == "missing_directory" {
						setDirectory(nil)
					}
					_, firstErr := engine.ProcessPendingCallbacks(ctx, "msp-callback", 10)
					if strings.HasPrefix(stage, "ack_then_") {
						require.False(t, failAck, "actual callback must reach child commit then acknowledgement failure")
						require.Error(t, firstErr)
						require.Equal(t, before+1, clients.Tenant.Ticket.Query().CountX(ctx))
						row = clients.Tenant.ProcessCallbackOutbox.GetX(ctx, row.ID)
						require.Equal(t, "pending", row.Status)
						if stage == "ack_then_allocation_revoked" {
							f.client.MSPAllocation.UpdateOneID(allocation.ID).SetDeassignedAt(time.Now()).ExecX(f.ctx)
						} else {
							f.client.RolePermission.DeleteOne(writeGrant).ExecX(f.ctx)
						}
						clients.Tenant.ProcessCallbackOutbox.UpdateOneID(row.ID).SetNextAttemptAt(time.Now().Add(-time.Second)).ExecX(ctx)
						_, err = engine.ProcessPendingCallbacks(ctx, "msp-callback", 10)
						require.NoError(t, err)
					} else {
						require.NoError(t, firstErr)
					}
					row = clients.Tenant.ProcessCallbackOutbox.GetX(ctx, row.ID)
					created := stage == "success" || strings.HasPrefix(stage, "ack_then_")
					if created {
						require.Equal(t, before+1, clients.Tenant.Ticket.Query().CountX(ctx))
						require.Equal(t, beforeReceipts+1, clients.Tenant.IntakeRequest.Query().CountX(ctx))
						receipt := clients.Tenant.IntakeRequest.Query().Where(intakerequest.IdempotencyKey("bpmn-create:" + row.ExecutionKey)).OnlyX(ctx)
						require.Equal(t, actor.ID, receipt.ActorID)
						require.Equal(t, provider.ID, receipt.ActorTenantID)
						require.Equal(t, f.actor.ID, receipt.RequesterID)
						require.Equal(t, actor.ID, clients.Tenant.Ticket.GetX(ctx, *receipt.WorkItemID).OpenedByID)
					} else {
						require.Equal(t, before, clients.Tenant.Ticket.Query().CountX(ctx))
						require.Equal(t, beforeReceipts, clients.Tenant.IntakeRequest.Query().CountX(ctx))
					}
					if stage == "success" {
						require.Equal(t, "completed", row.Status)
					} else {
						require.Equal(t, "blocked", row.Status)
						require.Equal(t, "create", clients.Tenant.ProcessInstance.GetX(ctx, instance.ID).CurrentActivityID)
					}
					require.Equal(t, kind+":"+stage, row.ExecutionKey)
					require.Equal(t, fmt.Sprint(actor.ID), clients.Tenant.ProcessInstance.GetX(ctx, instance.ID).Initiator)
					if stage == "allocation_revoked" || stage == "ack_then_allocation_revoked" {
						f.client.MSPAllocation.UpdateOneID(allocation.ID).ClearDeassignedAt().ExecX(f.ctx)
					}
					if stage == "permission_revoked" || stage == "ack_then_permission_revoked" {
						writeGrant = f.client.RolePermission.Create().SetTenantID(f.tenant.ID).SetRoleID(role.ID).SetPermissionID(writeGrant.PermissionID).SaveX(f.ctx)
					}
					if stage == "role_changed" {
						f.client.User.UpdateOneID(actor.ID).SetMspRole("provider_agent").ExecX(f.ctx)
					}
					if stage == "missing_directory" {
						setDirectory(clients.System)
					}
					require.Zero(t, database.GetRawDB().Stats().InUse)
					require.Zero(t, clients.SystemDB.Stats().InUse)
				})
			}
		})
	}
}
