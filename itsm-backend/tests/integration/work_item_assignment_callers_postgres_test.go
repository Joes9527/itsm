//go:build integration_postgres

package integration

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/require"
	"go.uber.org/zap"
	"itsm-backend/authorization"
	"itsm-backend/common/tenantctx"
	"itsm-backend/controller"
	"itsm-backend/dto"
	"itsm-backend/ent"
	"itsm-backend/ent/outboxevent"
	"itsm-backend/ent/ticketnotification"
	change "itsm-backend/handlers/change"
	creation "itsm-backend/handlers/common/workitemcreation"
	problem "itsm-backend/handlers/problem"
	requestowner "itsm-backend/handlers/service_request"
	repository "itsm-backend/repository/ticket"
	"itsm-backend/service"
	"itsm-backend/service/bpmn"
	"net/http/httptest"
	"sync/atomic"
	"testing"
	"time"
)

func TestPostgresAssignmentCallerManualAtomic(t *testing.T) {
	client, cmd := assignmentFixture(t)
	ctx := tenantctx.WithTenantID(context.Background(), cmd.TenantID)
	actor := client.User.UpdateOneID(cmd.ActorID).SetRole("super_admin").SaveX(ctx)
	identity := creation.Identity{ActorID: actor.ID, TenantID: cmd.TenantID, Role: actor.Role, Channel: "http"}
	svc := service.NewTicketService(&service.TicketServiceConfig{SessionReader: authorization.NewSessionReader(client, sameTransactionDirectory{}), Client: client, Repository: repository.NewEntRepository(client, zap.NewNop().Sugar()), Logger: zap.NewNop().Sugar()})
	item, err := svc.AssignTicket(ctx, cmd.WorkItemID, cmd.AssigneeID, cmd.TenantID, identity)
	require.NoError(t, err)
	require.Equal(t, cmd.AssigneeID, *item.AssigneeID)
	require.Equal(t, 1, client.OutboxEvent.Query().CountX(ctx), "owning assignment must persist its durable event")
	require.Equal(t, 1, client.TicketWorkflowRecord.Query().CountX(ctx))
}

func TestPostgresAssignmentCallerBatchAtomic(t *testing.T) {
	client, cmd := assignmentFixture(t)
	ctx := tenantctx.WithTenantID(context.Background(), cmd.TenantID)
	actor := client.User.UpdateOneID(cmd.ActorID).SetRole("super_admin").SaveX(ctx)
	identity := creation.Identity{ActorID: actor.ID, TenantID: cmd.TenantID, Role: actor.Role, Channel: "http"}
	svc := service.NewTicketService(&service.TicketServiceConfig{SessionReader: authorization.NewSessionReader(client, sameTransactionDirectory{}), Client: client, Repository: repository.NewEntRepository(client, zap.NewNop().Sugar()), Logger: zap.NewNop().Sugar()})
	require.NoError(t, svc.AssignTickets(ctx, cmd.TenantID, []int{cmd.WorkItemID}, cmd.AssigneeID, identity))
	require.Equal(t, 1, client.OutboxEvent.Query().CountX(ctx), "batch owning path must record durable assignment")
}
func TestPostgresAssignmentCallerEscalationAtomic(t *testing.T) {
	client, cmd := assignmentFixture(t)
	ctx := tenantctx.WithTenantID(context.Background(), cmd.TenantID)
	actor := client.User.UpdateOneID(cmd.ActorID).SetRole("super_admin").SaveX(ctx)
	identity := creation.Identity{ActorID: actor.ID, TenantID: cmd.TenantID, Role: actor.Role, Channel: "http"}
	svc := service.NewTicketService(&service.TicketServiceConfig{SessionReader: authorization.NewSessionReader(client, sameTransactionDirectory{}), Client: client, Repository: repository.NewEntRepository(client, zap.NewNop().Sugar()), Logger: zap.NewNop().Sugar()})
	client.TicketAssignmentRule.Create().SetName("escalation").SetTenantID(cmd.TenantID).SetConditions([]map[string]interface{}{}).SetActions(map[string]interface{}{"type": "user", "value": cmd.AssigneeID}).SaveX(ctx)
	_, err := svc.EscalateTicket(ctx, cmd.WorkItemID, "needs specialist", cmd.TenantID, cmd.ActorID, identity)
	require.NoError(t, err)
	require.Equal(t, 1, client.OutboxEvent.Query().CountX(ctx), "escalation owning path must record durable assignment")
}

func TestPostgresAssignmentCallerMSPSelfAssignmentRuntime(t *testing.T) {
	f := newIncidentEffectsFixture(t)
	f.client.Tenant.UpdateOneID(f.tenant.ID).SetType("msp_customer").ExecX(f.ctx)
	provider := f.client.Tenant.Create().SetCode("assignment-provider").SetName("Provider").SetType("msp_provider").SaveX(f.ctx)
	actor := f.client.User.Create().SetTenantID(provider.ID).SetUsername("assignment-msp").SetName("MSP").SetEmail("assignment-msp@example.test").SetPasswordHash("unused").SetRole("admin").SetMspRole("provider_agent").SaveX(f.ctx)
	f.client.MSPAllocation.Create().SetMspUserID(actor.ID).SetCustomerTenantID(f.tenant.ID).SetRole("primary").SaveX(f.ctx)
	role := f.client.Role.Create().SetTenantID(f.tenant.ID).SetCode("msp_tech").SetName("MSP tech").SaveX(f.ctx)
	permission := f.client.Permission.Create().SetTenantID(f.tenant.ID).SetCode("ticket:assign").SetName("Assign").SetResource("ticket").SetAction("assign").SaveX(f.ctx)
	f.client.RolePermission.Create().SetTenantID(f.tenant.ID).SetRoleID(role.ID).SetPermissionID(permission.ID).SaveX(f.ctx)
	item := f.client.Ticket.Create().SetTitle("MSP self assign").SetTicketNumber("MSP-ASSIGN-1").SetRequesterID(actor.ID).SetTenantID(f.tenant.ID).SaveX(f.ctx)
	clients, cfg := runtimeClients(t, f)
	for _, grant := range []string{"SELECT,UPDATE ON process_instances,process_tasks", "SELECT,INSERT ON ticket_workflow_records", "USAGE ON SEQUENCE ticket_workflow_records_id_seq", "SELECT ON notification_preferences", "SELECT ON process_audit_logs,process_callback_outboxes,service_requests"} {
		_, err := f.db.ExecContext(f.ctx, "GRANT "+grant+" TO "+cfg.User)
		require.NoError(t, err)
	}
	ctx := tenantctx.WithTenantID(f.ctx, f.tenant.ID)
	_, err := clients.Tenant.User.Get(ctx, actor.ID)
	require.True(t, ent.IsNotFound(err), "target RLS must hide native MSP actor")
	logger := zap.NewNop().Sugar()
	svc := service.NewTicketService(&service.TicketServiceConfig{Client: clients.Tenant, Repository: repository.NewEntRepository(clients.Tenant, logger), Logger: logger, SessionReader: authorization.NewSessionReader(clients.Tenant, clients.IntakeDirectorySnapshot())})
	handler := controller.NewMSPController(nil, svc, logger)
	recorder := httptest.NewRecorder()
	gin.SetMode(gin.TestMode)
	c, _ := gin.CreateTestContext(recorder)
	c.Params = gin.Params{{Key: "id", Value: fmt.Sprint(item.ID)}}
	c.Set("user_id", actor.ID)
	c.Set("tenant_id", f.tenant.ID)
	c.Set("role", "msp_tech")
	c.Request = httptest.NewRequest("POST", "/msp/assign", bytes.NewBufferString(fmt.Sprintf(`{"customerTenantId":%d}`, f.tenant.ID))).WithContext(ctx)
	c.Request.Header.Set("Content-Type", "application/json")
	handler.AssignMSPTechnician(c)
	require.NotContains(t, recorder.Body.String(), `"code":500`, "handler response: %s", recorder.Body.String())
	require.Equal(t, actor.ID, f.client.Ticket.GetX(f.ctx, item.ID).AssigneeID, "verified allocated MSP self-assignment must pass owning HTTP handler")
	f.client.NotificationPreference.Create().SetTenantID(f.tenant.ID).SetUserID(actor.ID).SetEventType("ticket_assigned").SetInAppEnabled(true).SetEmailEnabled(true).SaveX(f.ctx)
	notifications := service.NewTicketNotificationService(clients.Tenant, logger)
	notifications.SetAssignmentDirectory(clients.IntakeDirectorySnapshot())
	notifications.SetDeliveryQueueClient(clients.System)
	event := f.client.OutboxEvent.Query().Where(outboxevent.EventTypeEQ("work_item.assigned")).OnlyX(f.ctx)
	sender := &assignmentMailRecorder{}
	email := service.NewEmailService(service.EmailConfig{}, logger)
	email.SetGraphProvider(func(int) (service.GraphMailSender, string, bool) { return sender, "support@example.test", true })
	notifications.SetEmailService(email)
	require.NoError(t, service.NewWorkItemAssignmentNotificationHandler(clients.Tenant, notifications).Deliver(ctx, event), "durable MSP assignment must materialize its notification")
	count, err := notifications.ProcessPendingDeliveries(ctx, "assignment-msp-test", 10)
	require.NoError(t, err)
	require.Equal(t, 1, count)
	require.Equal(t, 1, sender.calls, "allocated MSP recipient uses fake transport")
	// Produce two more events while actor allocation is valid. One is revoked
	// after materialization, the other before materialization.
	var pending []*ent.OutboxEvent
	for _, number := range []string{"MSP-ASSIGN-2", "MSP-ASSIGN-3"} {
		next := f.client.Ticket.Create().SetTitle(number).SetTicketNumber(number).SetRequesterID(actor.ID).SetTenantID(f.tenant.ID).SaveX(f.ctx)
		_, err := svc.AssignMSPTechnician(ctx, next.ID, f.tenant.ID, actor.ID, creation.Identity{ActorID: actor.ID, TenantID: f.tenant.ID, Role: "msp_tech", Channel: "http"})
		require.NoError(t, err)
		pending = append(pending, f.client.OutboxEvent.Query().Where(outboxevent.AggregateIDEQ(fmt.Sprint(next.ID)), outboxevent.EventTypeEQ("work_item.assigned")).OnlyX(f.ctx))
	}
	require.NoError(t, service.NewWorkItemAssignmentNotificationHandler(clients.Tenant, notifications).Deliver(ctx, pending[0]))
	requestPermission := f.client.Permission.Create().SetTenantID(f.tenant.ID).SetCode("service_request:provision").SetName("Provision").SetResource("service_request").SetAction("provision").SaveX(f.ctx)
	f.client.RolePermission.Create().SetTenantID(f.tenant.ID).SetRoleID(role.ID).SetPermissionID(requestPermission.ID).SaveX(f.ctx)
	authorization.InvalidateAllPermissionCaches()
	require.NoError(t, svc.RegisterAssignmentOwner(requestowner.NewService(nil, clients.Tenant, logger, nil)))
	requested := f.client.Ticket.Create().SetTenantID(f.tenant.ID).SetRequesterID(actor.ID).SetTitle("MSP requested item").SetTicketNumber("MSP-REQUEST-1").SetRecordClass("service_request_item").SetStatus("open").SaveX(f.ctx)
	f.client.ServiceRequest.Create().SetTicketID(requested.ID).SetCatalogID(1).SaveX(f.ctx)
	proRecorder := httptest.NewRecorder()
	proCtx, _ := gin.CreateTestContext(proRecorder)
	proCtx.Params = gin.Params{{Key: "id", Value: fmt.Sprint(requested.ID)}}
	proCtx.Set("user_id", actor.ID)
	proCtx.Set("tenant_id", f.tenant.ID)
	proCtx.Set("role", "msp_tech")
	proCtx.Request = httptest.NewRequest("POST", "/msp/assign", bytes.NewBufferString(fmt.Sprintf(`{"customerTenantId":%d}`, f.tenant.ID))).WithContext(ctx)
	proCtx.Request.Header.Set("Content-Type", "application/json")
	handler.AssignMSPTechnician(proCtx)
	require.Equal(t, actor.ID, f.client.Ticket.GetX(f.ctx, requested.ID).AssigneeID, proRecorder.Body.String())
	// Actor and requested owner are independently checked against current scope.
	customerTarget := f.client.User.Create().SetTenantID(f.tenant.ID).SetUsername("customer-target").SetName("Customer target").SetEmail("customer-target@example.test").SetPasswordHash("unused").SaveX(f.ctx)
	foreign := f.client.User.Create().SetTenantID(provider.ID).SetUsername("ordinary-foreign").SetName("Foreign").SetEmail("foreign@example.test").SetPasswordHash("unused").SaveX(f.ctx)
	inactive := f.client.User.Create().SetTenantID(f.tenant.ID).SetUsername("inactive-target").SetName("Inactive").SetEmail("inactive@example.test").SetPasswordHash("unused").SetActive(false).SaveX(f.ctx)
	unallocated := f.client.User.Create().SetTenantID(provider.ID).SetUsername("unallocated-msp").SetName("Unallocated MSP").SetEmail("unallocated@example.test").SetPasswordHash("unused").SetRole("admin").SetMspRole("provider_agent").SaveX(f.ctx)
	deassigned := f.client.User.Create().SetTenantID(provider.ID).SetUsername("deassigned-msp").SetName("Deassigned MSP").SetEmail("deassigned@example.test").SetPasswordHash("unused").SetRole("admin").SetMspRole("provider_agent").SaveX(f.ctx)
	f.client.MSPAllocation.Create().SetMspUserID(deassigned.ID).SetCustomerTenantID(f.tenant.ID).SetRole("primary").SetDeassignedAt(time.Now()).SaveX(f.ctx)
	before := f.client.Ticket.GetX(f.ctx, item.ID)
	for _, target := range []int{foreign.ID, inactive.ID, unallocated.ID, deassigned.ID} {
		_, err := svc.AssignTicket(ctx, item.ID, target, f.tenant.ID, creation.Identity{ActorID: actor.ID, TenantID: f.tenant.ID, Role: "msp_tech", Channel: "http"})
		require.Error(t, err)
		require.Equal(t, before.Version, f.client.Ticket.GetX(f.ctx, item.ID).Version)
	}
	callbackCtx := assignmentCallbackEvidence(t, f.client, item, actor.ID, provider.ID)
	callbackCtx = tenantctx.WithTenantID(callbackCtx, f.tenant.ID)
	applyCallback := func(target int) error {
		tx, err := clients.Tenant.Tx(callbackCtx)
		if err != nil {
			return err
		}
		defer tx.Rollback()
		writer, cmd, err := service.NewWorkflowAssignmentBoundary(clients.IntakeDirectorySnapshot())(callbackCtx, tx, f.tenant.ID)
		if err != nil {
			return err
		}
		current := tx.Ticket.GetX(callbackCtx, item.ID)
		cmd.WorkItemID = item.ID
		cmd.AssigneeID = target
		cmd.ExpectedVersion = current.Version
		if _, err = writer.Apply(callbackCtx, tx.Client(), cmd); err != nil {
			return err
		}
		return tx.Commit()
	}
	require.NoError(t, applyCallback(customerTarget.ID), "durable callback native MSP actor and customer owner pass runtime directory")
	f.client.MSPAllocation.Update().SetDeassignedAt(time.Now()).ExecX(f.ctx)
	require.Error(t, applyCallback(actor.ID), "recorded callback actor is revalidated after allocation revocation")
	_, err = svc.AssignMSPTechnician(ctx, item.ID, f.tenant.ID, actor.ID, creation.Identity{ActorID: actor.ID, TenantID: f.tenant.ID, Role: "msp_tech", Channel: "http"})
	require.Error(t, err, "revoked MSP self-assignment is denied")
	count, err = notifications.ProcessPendingDeliveries(ctx, "assignment-msp-revoked", 10)
	require.ErrorContains(t, err, "not completed")
	require.Zero(t, count)
	require.Equal(t, 1, sender.calls, "revoked allocation must never send")
	require.Error(t, service.NewWorkItemAssignmentNotificationHandler(clients.Tenant, notifications).Deliver(ctx, pending[1]), "revoked allocation must not materialize")
	row := f.client.TicketNotification.Query().Where(ticketnotification.DeliveryKeyEQ(pending[0].EventID), ticketnotification.ChannelEQ("email")).OnlyX(f.ctx)
	require.Equal(t, "delivery_target_invalid", row.LastErrorClass)

}

func TestPostgresAssignmentCallerWorkflowAndAutomatic(t *testing.T) {
	for _, method := range []string{"accept", "forward", "auto"} {
		t.Run(method, func(t *testing.T) {
			client, cmd := assignmentFixture(t)
			ctx := tenantctx.WithTenantID(context.Background(), cmd.TenantID)
			logger := zap.NewNop().Sugar()
			actor := client.User.UpdateOneID(cmd.ActorID).SetRole("super_admin").SaveX(ctx)
			identity := creation.Identity{ActorID: actor.ID, TenantID: cmd.TenantID, Role: actor.Role, Channel: "http"}
			sessions := authorization.NewSessionReader(client, sameTransactionDirectory{})
			workflow := service.NewTicketWorkflowService(client, logger)
			workflow.SetSessionReader(sessions)
			smart := service.NewTicketAssignmentSmartService(client, logger, service.NewTicketAssignmentService(client, logger), service.NewTicketAssignmentRuleService(client, logger))
			smart.SetSessionReader(sessions)
			var err error
			switch method {
			case "accept":
				client.Ticket.UpdateOneID(cmd.WorkItemID).SetStatus("new").ExecX(ctx)
				err = workflow.AcceptTicket(ctx, &dto.AcceptTicketRequest{TicketID: cmd.WorkItemID}, cmd.ActorID, cmd.TenantID, identity)
			case "forward":
				err = workflow.ForwardTicket(ctx, &dto.ForwardTicketRequest{TicketID: cmd.WorkItemID, ToUserID: cmd.AssigneeID, TransferOwnership: true}, cmd.ActorID, cmd.TenantID, identity)
			case "auto":
				client.TicketAssignmentRule.Create().SetName("route").SetTenantID(cmd.TenantID).SetActions(map[string]interface{}{"type": "user", "value": cmd.AssigneeID}).SaveX(ctx)
				_, err = smart.AutoAssign(ctx, cmd.WorkItemID, cmd.TenantID, identity)
			}
			require.NoError(t, err)
			require.Equal(t, 1, client.OutboxEvent.Query().CountX(ctx), "owning path must persist durable assignment")
		})
	}
}

func TestPostgresAssignmentCallerMixedUpdate(t *testing.T) {
	client, cmd := assignmentFixture(t)
	ctx := tenantctx.WithTenantID(context.Background(), cmd.TenantID)
	actor := client.User.UpdateOneID(cmd.ActorID).SetRole("super_admin").SaveX(ctx)
	identity := creation.Identity{ActorID: actor.ID, TenantID: cmd.TenantID, Role: actor.Role, Channel: "http"}
	svc := service.NewTicketService(&service.TicketServiceConfig{SessionReader: authorization.NewSessionReader(client, sameTransactionDirectory{}), Client: client, Repository: repository.NewEntRepository(client, zap.NewNop().Sugar()), Logger: zap.NewNop().Sugar()})
	updated, err := svc.UpdateTicket(ctx, cmd.WorkItemID, &dto.UpdateTicketRequest{Title: "Edited and assigned", AssigneeID: &cmd.AssigneeID, Version: cmd.ExpectedVersion}, cmd.TenantID, identity)
	require.NoError(t, err)
	require.Equal(t, cmd.ExpectedVersion+1, updated.Version)
	require.Equal(t, "Edited and assigned", updated.Title)
	require.Equal(t, 1, client.OutboxEvent.Query().CountX(ctx), "mixed update must record shared assignment")
}

func TestPostgresAssignmentCallerProfessionalMixed(t *testing.T) {
	for _, class := range []string{"problem", "change_request"} {
		t.Run(class, func(t *testing.T) {
			client, cmd := assignmentFixture(t)
			ctx := tenantctx.WithTenantID(context.Background(), cmd.TenantID)
			item := client.Ticket.Create().SetTenantID(cmd.TenantID).SetRequesterID(cmd.ActorID).SetOpenedByID(cmd.ActorID).SetTitle("Professional assignment").SetTicketNumber("PRO-ASSIGN").SetRecordClass(class).SetStatus("draft").SetPriority("medium").SaveX(ctx)
			cmd.WorkItemID = item.ID
			logger := zap.NewNop().Sugar()
			actor := client.User.UpdateOneID(cmd.ActorID).SetRole("super_admin").SaveX(ctx)
			identity := creation.Identity{ActorID: actor.ID, TenantID: cmd.TenantID, Role: actor.Role, Channel: "http"}
			sessions := authorization.NewSessionReader(client, sameTransactionDirectory{})
			var err error
			if class == "problem" {
				client.Ticket.UpdateOneID(cmd.WorkItemID).SetStatus("open").ExecX(ctx)
				ext := client.Problem.Create().SetWorkItemID(cmd.WorkItemID).SaveX(ctx)
				owner := problem.NewService(problem.NewEntRepository(client), logger)
				owner.SetSessionReader(sessions)
				_, err = owner.Update(ctx, cmd.TenantID, ext.ID, &problem.Problem{AssigneeID: &cmd.AssigneeID, Workaround: "durable workaround"}, identity)
			} else {
				ext := client.Change.Create().SetWorkItemID(cmd.WorkItemID).SaveX(ctx)
				repo := change.NewEntRepository(client, nil)
				current, loadErr := repo.Get(ctx, ext.ID, cmd.TenantID)
				require.NoError(t, loadErr)
				current.AssigneeID = &cmd.AssigneeID
				current.ImplementationPlan = "durable implementation"
				owner := change.NewService(repo, client, logger)
				owner.SetSessionReader(sessions)
				_, err = owner.UpdateChange(ctx, current, identity)
			}
			require.NoError(t, err)
			require.Equal(t, 1, client.OutboxEvent.Query().CountX(ctx), "professional owner must record shared assignment")
			require.Equal(t, cmd.ExpectedVersion+1, client.Ticket.GetX(ctx, cmd.WorkItemID).Version)
		})
	}
}

func TestPostgresAssignmentCallerIncident(t *testing.T) {
	for _, method := range []string{"assign", "update"} {
		t.Run(method, func(t *testing.T) {
			client, cmd := assignmentFixture(t)
			ctx := tenantctx.WithTenantID(context.Background(), cmd.TenantID)
			item := client.Ticket.Create().SetTenantID(cmd.TenantID).SetRequesterID(cmd.ActorID).SetOpenedByID(cmd.ActorID).SetTitle("Incident assignment").SetTicketNumber("INC-ASSIGN").SetRecordClass("incident").SetStatus("new").SetPriority("medium").SaveX(ctx)
			ext := client.Incident.Create().SetWorkItemID(item.ID).SaveX(ctx)
			owner := service.NewIncidentService(client, zap.NewNop().Sugar())
			owner.SetSessionReader(authorization.NewSessionReader(client, sameTransactionDirectory{}))
			actor := client.User.UpdateOneID(cmd.ActorID).SetRole("super_admin").SaveX(ctx)
			identity := creation.Identity{ActorID: actor.ID, TenantID: cmd.TenantID, Role: actor.Role, Channel: "http"}
			var err error
			if method == "assign" {
				_, err = owner.AssignIncident(ctx, ext.ID, cmd.AssigneeID, cmd.TenantID, identity)
			} else {
				title := "updated incident"
				_, err = owner.UpdateIncident(ctx, ext.ID, &dto.UpdateIncidentRequest{AssigneeID: &cmd.AssigneeID, Title: &title}, cmd.TenantID, identity)
			}
			require.NoError(t, err)
			require.Equal(t, 1, client.OutboxEvent.Query().CountX(ctx), "Incident owner must record shared assignment")
		})
	}
}

type assignmentMailRecorder struct{ calls int }

func (s *assignmentMailRecorder) SendMail(context.Context, string, string, string, string, string) error {
	s.calls++
	return nil
}

func assignmentCallbackEvidence(t *testing.T, client *ent.Client, item *ent.Ticket, actor, native int) context.Context {
	t.Helper()
	ctx := context.Background()
	target := item.TenantID
	deployment := client.ProcessDeployment.Create().SetDeploymentID("assignment-proof").SetDeploymentName("Assignment proof").SetTenantID(target).SaveX(ctx)
	definition := client.ProcessDefinition.Create().SetKey("assignment-proof").SetName("Assignment proof").SetBpmnXML([]byte("<definitions/>")).SetDeploymentID(deployment.ID).SetTenantID(target).SaveX(ctx)
	instance := client.ProcessInstance.Create().SetProcessInstanceID("assignment-proof").SetProcessDefinitionKey(definition.Key).SetProcessDefinitionID(definition.ID).SetBusinessID(item.ID).SetBusinessType("ticket").SetTenantID(target).SaveX(ctx)
	row := client.ProcessCallbackOutbox.Create().SetExecutionKey("assignment-proof").SetProcessInstanceID(instance.ID).SetTenantID(target).SetCallbackKind("service_task").SetHandlerID("ticket_service_handler").SetTaskType("ticket_task").SetElementID("assign").SetAction("assign").SaveX(ctx)
	client.ProcessAuditLog.Create().SetProcessInstanceID(instance.ID).SetProcessInstanceKey(instance.ProcessInstanceID).SetProcessDefinitionID(definition.ID).SetProcessDefinitionKey(definition.Key).SetActivityID("assign").SetActivityType("service_task").SetAction("callback_execution_provenance").SetTenantID(target).SetUserID(actor).SetMetadata(map[string]interface{}{"execution_key": row.ExecutionKey, "outbox_id": row.ID, "process_task_id": 0, "task_id": "", "native_tenant_id": native, "target_tenant_id": target, "source": "bpmn_start"}).SaveX(ctx)
	return bpmn.WithBPMNCallbackExecutionKey(ctx, row.ExecutionKey)
}

func TestPostgresAssignmentCallerIncidentRuleReceiptAtomic(t *testing.T) {
	f := newIncidentEffectsFixture(t)
	f.engine.SetAssignmentDirectory(sameTransactionDirectory{})
	f.rule(map[string]interface{}{"type": "assign", "assignee_id": f.actor.ID, "reason": "configured incident policy"})
	var fail atomic.Bool
	fail.Store(true)
	f.client.IncidentRuleActionReceipt.Use(func(next ent.Mutator) ent.Mutator {
		return ent.MutateFunc(func(ctx context.Context, m ent.Mutation) (ent.Value, error) {
			if fail.Load() {
				return nil, errors.New("assignment receipt rollback")
			}
			return next.Mutate(ctx, m)
		})
	})
	require.ErrorContains(t, f.engine.Deliver(f.ctx, f.event), "assignment receipt rollback")
	require.Zero(t, f.client.Ticket.GetX(f.ctx, f.inc.WorkItemID).AssigneeID)
	require.Zero(t, f.client.OutboxEvent.Query().Where(outboxevent.EventTypeEQ("work_item.assigned")).CountX(f.ctx))
	require.Zero(t, f.client.TicketWorkflowRecord.Query().CountX(f.ctx))
	fail.Store(false)
	require.NoError(t, f.engine.Deliver(f.ctx, f.event))
	require.NoError(t, f.engine.Deliver(f.ctx, f.event))
	require.Equal(t, f.actor.ID, f.client.Ticket.GetX(f.ctx, f.inc.WorkItemID).AssigneeID)
	require.Equal(t, 1, f.client.OutboxEvent.Query().Where(outboxevent.EventTypeEQ("work_item.assigned")).CountX(f.ctx))
	audit := f.client.TicketWorkflowRecord.Query().OnlyX(f.ctx)
	require.Equal(t, f.actor.ID, audit.OperatorID)
	require.Equal(t, "incident_rule", audit.Metadata["source"])
}

func TestPostgresAssignmentCallerFailureAtomic(t *testing.T) {
	for _, mode := range []string{"assignment_outbox_failure", "escalation_no_policy", "escalation_foreign_policy", "clear_owner", "negative_owner"} {
		t.Run(mode, func(t *testing.T) {
			client, cmd := assignmentFixture(t)
			ctx := tenantctx.WithTenantID(context.Background(), cmd.TenantID)
			actor := client.User.UpdateOneID(cmd.ActorID).SetRole("super_admin").SaveX(ctx)
			identity := creation.Identity{ActorID: actor.ID, TenantID: cmd.TenantID, Role: actor.Role, Channel: "http"}
			svc := service.NewTicketService(&service.TicketServiceConfig{SessionReader: authorization.NewSessionReader(client, sameTransactionDirectory{}), Client: client, Repository: repository.NewEntRepository(client, zap.NewNop().Sugar()), Logger: zap.NewNop().Sugar()})
			if mode == "clear_owner" {
				client.Ticket.UpdateOneID(cmd.WorkItemID).SetAssigneeID(cmd.AssigneeID).ExecX(ctx)
			}
			before := client.Ticket.GetX(ctx, cmd.WorkItemID)
			if mode == "assignment_outbox_failure" {
				client.OutboxEvent.Use(func(next ent.Mutator) ent.Mutator {
					return ent.MutateFunc(func(context.Context, ent.Mutation) (ent.Value, error) {
						return nil, errors.New("assignment outbox rejected")
					})
				})
			}
			if mode == "escalation_foreign_policy" {
				foreign := client.Tenant.Create().SetName("Foreign policy").SetCode("foreign-policy").SaveX(ctx)
				client.TicketAssignmentRule.Create().SetTenantID(foreign.ID).SetName("foreign").SetConditions([]map[string]interface{}{}).SetActions(map[string]interface{}{"type": "user", "value": cmd.AssigneeID}).SaveX(ctx)
			}
			var err error
			switch mode {
			case "escalation_no_policy", "escalation_foreign_policy":
				_, err = svc.EscalateTicket(ctx, cmd.WorkItemID, "escalation", cmd.TenantID, actor.ID, identity)
			case "clear_owner":
				_, err = svc.UpdateTicket(ctx, cmd.WorkItemID, &dto.UpdateTicketRequest{AssigneeID: new(int), Title: "cleared"}, cmd.TenantID, identity)
			case "negative_owner":
				_, err = svc.AssignTicket(ctx, cmd.WorkItemID, -1, cmd.TenantID, identity)
			default:
				_, err = svc.AssignTicket(ctx, cmd.WorkItemID, cmd.AssigneeID, cmd.TenantID, identity)
			}
			after := client.Ticket.GetX(ctx, cmd.WorkItemID)
			if mode == "clear_owner" {
				require.NoError(t, err)
				require.Zero(t, after.AssigneeID)
				require.Equal(t, before.Version+1, after.Version)
				require.Equal(t, "cleared", after.Title)
				require.Equal(t, 1, client.OutboxEvent.Query().CountX(ctx))
				return
			}
			require.Error(t, err)
			require.Equal(t, before.AssigneeID, after.AssigneeID)
			require.Equal(t, before.Version, after.Version)
			require.Equal(t, before.Priority, after.Priority)
			require.Equal(t, before.Status, after.Status)
			require.Zero(t, client.OutboxEvent.Query().CountX(ctx))
			require.Zero(t, client.TicketWorkflowRecord.Query().CountX(ctx))
		})
	}
}
