package service

import (
	"context"
	"testing"

	"github.com/stretchr/testify/require"
	"itsm-backend/authorization"
	"itsm-backend/common/tenantctx"
	"itsm-backend/dto"
	"itsm-backend/ent"
	creation "itsm-backend/handlers/common/workitemcreation"
	"itsm-backend/handlers/shared/workitemmutation"
	ticketrepo "itsm-backend/repository/ticket"
	executionfixture "itsm-backend/tests/fixtures/execution"
)

func TestTicketAssignmentRejectsProfessionalClasses(t *testing.T) {
	for _, class := range []string{"incident", "problem", "change_request"} {
		t.Run(class, func(t *testing.T) {
			for _, action := range []string{"msp", "edit", "service_escalate", "assign", "batch", "smart", "accept", "forward"} {
				t.Run(action, func(t *testing.T) {
					client, owner, ctx := setupIncidentTest(t)
					defer client.Close()
					tenant, err := createIncidentTestTenant(ctx, client, "boundary")
					require.NoError(t, err)
					actor, err := createIncidentTestUser(ctx, client, tenant.ID, "owner")
					require.NoError(t, err)
					next, err := createIncidentTestUser(ctx, client, tenant.ID, "next")
					require.NoError(t, err)
					grantAssignmentBoundaryRole(t, client, tenant.ID, actor.Role)
					before := client.Ticket.Create().SetTitle("professional boundary").SetTicketNumber("PRO-B").SetRequesterID(actor.ID).SetTenantID(tenant.ID).SetRecordClass(class).SetStatus("new").SetAssigneeID(actor.ID).SaveX(ctx)
					switch class {
					case "incident":
						client.Incident.Create().SetWorkItemID(before.ID).SaveX(ctx)
					case "problem":
						client.Problem.Create().SetWorkItemID(before.ID).SaveX(ctx)
					case "change_request":
						client.Change.Create().SetWorkItemID(before.ID).SaveX(ctx)
					}
					ticketSvc := NewTicketService(&TicketServiceConfig{Client: client, Repository: ticketrepo.NewEntRepository(client, owner.logger), Logger: owner.logger, Execution: executionfixture.Standard(), SessionReader: authorization.NewSessionReader(client, callbackFixtureDirectory{})})
					ctx = tenantctx.WithTenantID(ctx, tenant.ID)
					ticketSvc.execution = executionfixture.Standard()
					assignment := NewTicketAssignmentService(client, owner.logger)
					workflow := NewTicketWorkflowService(client, owner.logger)
					workflow.SetSessionReader(authorization.NewSessionReader(client, callbackFixtureDirectory{}))
					workflow.SetExecutionPolicy(executionfixture.Standard())
					smart := NewTicketAssignmentSmartService(client, owner.logger, assignment, NewTicketAssignmentRuleService(client, owner.logger))
					smart.SetSessionReader(authorization.NewSessionReader(client, callbackFixtureDirectory{}))
					smart.SetExecutionPolicy(executionfixture.Standard())
					switch action {
					case "msp":
						_, err = ticketSvc.AssignMSPTechnician(ctx, before.ID, tenant.ID, next.ID, creation.Identity{ActorID: actor.ID, TenantID: tenant.ID, Role: actor.Role})
					case "edit":
						_, err = ticketSvc.UpdateTicket(ctx, editCommandForTest(before.ID, &dto.TicketEditCommand{Fields: dto.TicketEditFields{AssigneeID: &next.ID}, Meta: workitemmutation.Meta{ActorID: actor.ID, ExpectedVersion: before.Version}}, tenant.ID))
					case "service_escalate":
						_, err = ticketSvc.EscalateTicket(ctx, dto.TicketEscalationCommand{WorkItemID: before.ID, Reason: "handover", Meta: workitemmutation.Meta{TenantID: tenant.ID, ActorID: actor.ID, ExpectedVersion: before.Version, Source: "http", OperationID: "test"}})
					case "assign":
						_, err = ticketSvc.AssignTicket(ctx, before.ID, next.ID, tenant.ID, creation.Identity{ActorID: actor.ID, TenantID: tenant.ID, Role: actor.Role})
					case "batch":
						err = ticketSvc.AssignTickets(ctx, tenant.ID, []int{before.ID}, next.ID, creation.Identity{ActorID: actor.ID, TenantID: tenant.ID, Role: actor.Role})
					case "smart":
						_, err = smart.AutoAssign(ctx, before.ID, tenant.ID, creation.Identity{ActorID: actor.ID, TenantID: tenant.ID, Role: actor.Role})
					case "accept":
						err = workflow.AcceptTicket(ctx, &dto.AcceptTicketRequest{TicketID: before.ID}, actor.ID, tenant.ID, creation.Identity{ActorID: actor.ID, TenantID: tenant.ID, Role: actor.Role})
					case "forward":
						err = workflow.ForwardTicket(ctx, &dto.ForwardTicketRequest{TicketID: before.ID, ToUserID: next.ID, TransferOwnership: true}, actor.ID, tenant.ID, creation.Identity{ActorID: actor.ID, TenantID: tenant.ID, Role: actor.Role})
					}
					if action == "accept" {
						require.ErrorContains(t, err, "does not permit acceptance")
					} else if action == "smart" || action == "forward" {
						require.ErrorContains(t, err, "eligible")
					} else {
						require.ErrorContains(t, err, "owning domain command")
					}
					after := client.Ticket.GetX(ctx, before.ID)
					require.Equal(t, before.AssigneeID, after.AssigneeID)
					require.Equal(t, before.Version, after.Version)
					require.Equal(t, before.Status, after.Status)
					require.Zero(t, client.AuditLog.Query().CountX(ctx))
					require.Zero(t, client.TicketWorkflowRecord.Query().CountX(ctx))
				})
			}
		})
	}
}

func TestTicketAssignmentPreservesOtherClasses(t *testing.T) {
	for _, class := range []string{"generic", "service_request_item", "catalog_task"} {
		t.Run(class, func(t *testing.T) {
			client, owner, ctx := setupIncidentTest(t)
			defer client.Close()
			tenant, err := createIncidentTestTenant(ctx, client, "preserve")
			require.NoError(t, err)
			actor, err := createIncidentTestUser(ctx, client, tenant.ID, "owner")
			require.NoError(t, err)
			next, err := createIncidentTestUser(ctx, client, tenant.ID, "next")
			require.NoError(t, err)
			before := client.Ticket.Create().SetTitle("unchanged class behavior").SetTicketNumber("WI-B").SetRequesterID(actor.ID).SetTenantID(tenant.ID).SetRecordClass(class).SetStatus("new").SetPriority("medium").SaveX(ctx)
			grantAssignmentBoundaryRole(t, client, tenant.ID, actor.Role)
			ctx = tenantctx.WithTenantID(ctx, tenant.ID)
			svc := NewTicketService(&TicketServiceConfig{Client: client, Repository: ticketrepo.NewEntRepository(client, owner.logger), Logger: owner.logger, Execution: executionfixture.Standard(), SessionReader: authorization.NewSessionReader(client, callbackFixtureDirectory{})})
			_, err = svc.AssignTicket(ctx, before.ID, next.ID, tenant.ID, creation.Identity{ActorID: actor.ID, TenantID: tenant.ID, Role: actor.Role})
			require.NoError(t, err)
			require.Equal(t, next.ID, client.Ticket.GetX(ctx, before.ID).AssigneeID)
		})
	}
}

func TestTicketAssignmentMixedBatchRejectsBeforeWrites(t *testing.T) {
	for _, class := range []string{"incident", "problem", "change_request"} {
		for _, action := range []string{"assignment", "close", "priority"} {
			t.Run(class+"/"+action, func(t *testing.T) {
				client, owner, ctx := setupIncidentTest(t)
				defer client.Close()
				tenant, err := createIncidentTestTenant(ctx, client, "mixed")
				require.NoError(t, err)
				actor, err := createIncidentTestUser(ctx, client, tenant.ID, "owner")
				require.NoError(t, err)
				grantAssignmentBoundaryRole(t, client, tenant.ID, actor.Role)
				professional := client.Ticket.Create().SetTitle("professional").SetTicketNumber("PRO-B").SetRequesterID(actor.ID).SetTenantID(tenant.ID).SetRecordClass(class).SetStatus("resolved").SaveX(ctx)
				generic := client.Ticket.Create().SetTitle("generic").SetTicketNumber("GEN-B").SetRequesterID(actor.ID).SetTenantID(tenant.ID).SetRecordClass("generic").SetStatus("resolved").SetPriority("medium").SaveX(ctx)
				ids := []int{generic.ID, professional.ID}
				svc := NewTicketService(&TicketServiceConfig{Client: client, Repository: ticketrepo.NewEntRepository(client, owner.logger), Logger: owner.logger, Execution: executionfixture.Standard(), SessionReader: authorization.NewSessionReader(client, callbackFixtureDirectory{})})
				ctx = tenantctx.WithTenantID(ctx, tenant.ID)
				switch action {
				case "assignment":
					err = svc.AssignTickets(ctx, tenant.ID, ids, actor.ID, creation.Identity{ActorID: actor.ID, TenantID: tenant.ID, Role: actor.Role})
				case "close":
					err = svc.BatchCloseTickets(ctx, ids, tenant.ID, "closed")
				case "priority":
					err = svc.BatchUpdatePriority(ctx, ids, "high", tenant.ID)
				}
				require.ErrorContains(t, err, "owning domain command")
				for _, before := range []*ent.Ticket{generic, professional} {
					after := client.Ticket.GetX(ctx, before.ID)
					require.Equal(t, before.AssigneeID, after.AssigneeID)
					require.Equal(t, before.Version, after.Version)
					require.Equal(t, before.Status, after.Status)
					require.Equal(t, before.Priority, after.Priority)
				}
				require.Zero(t, client.AuditLog.Query().CountX(ctx))
				require.Zero(t, client.TicketWorkflowRecord.Query().CountX(ctx))
				require.Zero(t, client.Notification.Query().CountX(ctx))
			})
		}
	}
}

func grantAssignmentBoundaryRole(t *testing.T, client *ent.Client, tenantID int, code string) {
	t.Helper()
	ctx := context.Background()
	role := client.Role.Create().SetTenantID(tenantID).SetCode(code).SetName("Boundary fixture").SetIsActive(true).SaveX(ctx)
	permission := client.Permission.Create().SetTenantID(tenantID).SetCode("assignment-boundary").SetName("Boundary fixture").SetResource("*").SetAction("*").SaveX(ctx)
	client.RolePermission.Create().SetTenantID(tenantID).SetRoleID(role.ID).SetPermissionID(permission.ID).ExecX(ctx)
	authorization.InvalidateAllPermissionCaches()
}
