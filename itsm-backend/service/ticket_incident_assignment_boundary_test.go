package service

import (
	"github.com/stretchr/testify/require"
	"itsm-backend/dto"
	"testing"
)

func TestTicketAssignmentRejectsIncident(t *testing.T) {
	for _, action := range []string{"msp", "edit", "service_escalate", "assign", "batch", "policy", "smart", "reassign", "policy_batch", "accept", "forward", "escalate"} {
		t.Run(action, func(t *testing.T) {
			client, owner, ctx := setupIncidentTest(t)
			defer client.Close()
			tenant, err := createIncidentTestTenant(ctx, client, "boundary")
			require.NoError(t, err)
			actor, err := createIncidentTestUser(ctx, client, tenant.ID, "owner")
			require.NoError(t, err)
			next, err := createIncidentTestUser(ctx, client, tenant.ID, "next")
			require.NoError(t, err)
			inc := createAutomationIncident(t, ctx, client, tenant.ID, actor.ID, "boundary")
			before := client.Ticket.UpdateOneID(inc.WorkItemID).SetStatus("new").SetAssigneeID(actor.ID).SaveX(ctx)
			ticketSvc := NewTicketServiceForTest(client, owner.logger)
			assignment := NewTicketAssignmentService(client, owner.logger)
			workflow := NewTicketWorkflowService(client, owner.logger)
			switch action {
			case "msp":
				_, err = ticketSvc.AssignMSPTechnician(ctx, before.ID, tenant.ID, next.ID)
			case "edit":
				_, err = ticketSvc.UpdateTicket(ctx, before.ID, &dto.UpdateTicketRequest{AssigneeID: next.ID, Version: before.Version}, tenant.ID)
			case "service_escalate":
				_, err = ticketSvc.EscalateTicket(ctx, before.ID, "handover", tenant.ID, actor.ID)
			case "assign":
				_, err = ticketSvc.AssignTicket(ctx, before.ID, next.ID, tenant.ID)
			case "batch":
				err = ticketSvc.AssignTickets(ctx, tenant.ID, []int{before.ID}, next.ID)
			case "policy":
				_, err = assignment.AssignTicket(ctx, &AssignmentRequest{TicketID: before.ID, TenantID: tenant.ID, PreferredUser: &next.ID})
			case "smart":
				_, err = NewTicketAssignmentSmartService(client, owner.logger, assignment, NewTicketAssignmentRuleService(client, owner.logger)).AutoAssign(ctx, before.ID, tenant.ID)
			case "reassign":
				err = assignment.ReassignTicket(ctx, before.ID, next.ID, "handover")
			case "policy_batch":
				err = assignment.AssignTickets(ctx, tenant.ID, []int{before.ID}, next.ID)
			case "accept":
				err = workflow.AcceptTicket(ctx, &dto.AcceptTicketRequest{TicketID: before.ID}, next.ID, tenant.ID)
			case "forward":
				err = workflow.ForwardTicket(ctx, &dto.ForwardTicketRequest{TicketID: before.ID, ToUserID: next.ID, TransferOwnership: true}, actor.ID, tenant.ID)
			case "escalate":
				_, err = NewTicketLifecycleService(client, owner.logger).EscalateTicket(ctx, before.ID, "handover", tenant.ID, actor.ID)
			}
			require.ErrorContains(t, err, "Incident assignment requires the Incident command")
			after := client.Ticket.GetX(ctx, before.ID)
			require.Equal(t, before.AssigneeID, after.AssigneeID)
			require.Equal(t, before.Version, after.Version)
			require.Equal(t, before.Status, after.Status)
		})
	}
}

func TestTicketAssignmentPreservesOtherClasses(t *testing.T) {
	for _, class := range []string{"generic", "service_request_item", "catalog_task", "problem", "change_request"} {
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
			_, err = NewTicketAssignmentService(client, owner.logger).AssignTicket(ctx, &AssignmentRequest{TicketID: before.ID, TenantID: tenant.ID, PreferredUser: &next.ID})
			require.NoError(t, err)
			require.Equal(t, next.ID, client.Ticket.GetX(ctx, before.ID).AssigneeID)
		})
	}
}

func TestTicketAssignmentMixedBatchRejectsBeforeWrites(t *testing.T) {
	for _, policy := range []bool{false, true} {
		t.Run(map[bool]string{false: "ticket", true: "policy"}[policy], func(t *testing.T) {
			client, owner, ctx := setupIncidentTest(t)
			defer client.Close()
			tenant, err := createIncidentTestTenant(ctx, client, "mixed")
			require.NoError(t, err)
			actor, err := createIncidentTestUser(ctx, client, tenant.ID, "owner")
			require.NoError(t, err)
			inc := createAutomationIncident(t, ctx, client, tenant.ID, actor.ID, "mixed")
			generic := client.Ticket.Create().SetTitle("generic").SetTicketNumber("GEN-B").SetRequesterID(actor.ID).SetTenantID(tenant.ID).SetRecordClass("generic").SetStatus("new").SetPriority("medium").SaveX(ctx)
			ids := []int{generic.ID, inc.WorkItemID}
			if policy {
				err = NewTicketAssignmentService(client, owner.logger).AssignTickets(ctx, tenant.ID, ids, actor.ID)
			} else {
				err = NewTicketServiceForTest(client, owner.logger).AssignTickets(ctx, tenant.ID, ids, actor.ID)
			}
			require.ErrorContains(t, err, "Incident assignment requires the Incident command")
			after := client.Ticket.GetX(ctx, generic.ID)
			require.Equal(t, generic.AssigneeID, after.AssigneeID)
			require.Equal(t, generic.Version, after.Version)
		})
	}
}
