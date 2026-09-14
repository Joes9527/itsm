package service

import (
	"fmt"
	"testing"

	"github.com/stretchr/testify/require"
	"itsm-backend/dto"
	"itsm-backend/handlers/shared/workitemmutation"
	ticketrepo "itsm-backend/repository/ticket"
	executionfixture "itsm-backend/tests/fixtures/execution"
)

func TestTicketCoreMutationsRejectProfessionalClasses(t *testing.T) {
	for _, class := range []string{"incident", "problem", "change_request"} {
		for _, action := range []string{"title", "description", "priority", "category", "resolution", "status", "service_status", "workflow_status", "service_resolve", "service_close", "lifecycle_status", "lifecycle_resolve", "lifecycle_close", "workflow_resolve", "workflow_close", "workflow_reopen", "workflow_withdraw", "repo_update", "repo_status", "repo_assign", "service_sync", "lifecycle_sync"} {
			t.Run(class+"/"+action, func(t *testing.T) {
				client, owner, ctx := setupIncidentTest(t)
				defer client.Close()
				tenant, err := createIncidentTestTenant(ctx, client, "core-boundary")
				require.NoError(t, err)
				actor, err := createIncidentTestUser(ctx, client, tenant.ID, "core-actor")
				require.NoError(t, err)
				status := "in_progress"
				if action == "service_close" || action == "lifecycle_close" || action == "workflow_close" || action == "workflow_reopen" {
					status = "resolved"
				}
				before := client.Ticket.Create().SetTitle("professional title").SetDescription("original description").SetTicketNumber("CORE-BOUNDARY").SetRequesterID(actor.ID).SetTenantID(tenant.ID).SetRecordClass(class).SetStatus(status).SetPriority("medium").SaveX(ctx)
				switch class {
				case "incident":
					client.Incident.Create().SetWorkItemID(before.ID).SaveX(ctx)
				case "problem":
					client.Problem.Create().SetWorkItemID(before.ID).SaveX(ctx)
				case "change_request":
					client.Change.Create().SetWorkItemID(before.ID).SaveX(ctx)
				}
				svc := NewTicketService(&TicketServiceConfig{Client: client, Repository: ticketrepo.NewEntRepository(client, owner.logger), Logger: owner.logger, Execution: executionfixture.Standard()})
				lifecycle := NewTicketLifecycleService(client, owner.logger)
				workflow := NewTicketWorkflowService(client, owner.logger)
				patch := &dto.TicketEditCommand{Fields: dto.TicketEditFields{}, Meta: workitemmutation.Meta{ActorID: actor.ID, ExpectedVersion: before.Version}}
				switch action {
				case "repo_update":
					title := "repository bypass"
					_, err = svc.repo.Update(ctx, before.ID, &ticketrepo.UpdateParams{Title: &title, Version: before.Version}, tenant.ID)
				case "repo_status":
					_, err = svc.repo.UpdateStatus(ctx, before.ID, ticketrepo.StatusPending, tenant.ID)
				case "repo_assign":
					_, err = svc.repo.AssignTicket(ctx, before.ID, actor.ID, tenant.ID)
				case "service_sync":
					err = svc.SyncTicketStatusWithWorkflow(ctx, before.ID, tenant.ID)
				case "lifecycle_sync":
					err = lifecycle.SyncTicketStatusWithWorkflow(ctx, before.ID, tenant.ID)
				case "title":
					patch.Fields.Title = "bypassed title"
				case "description":
					patch.Fields.Description = "bypassed description"
				case "priority":
					patch.Fields.Priority = "high"
				case "category":
					zero := 0
					patch.Fields.CategoryID = &zero
				case "resolution":
					patch.Fields.Resolution = "bypassed evidence"
				case "status":
					patch.Fields.Status = "pending"
				case "service_status":
					_, err = svc.UpdateTicketStatus(ctx, before.ID, "pending", tenant.ID, actor.ID)
				case "workflow_status":
					err = svc.UpdateTicketStatusForWorkflow(ctx, before.ID, "pending", tenant.ID, actor.ID)
				case "service_resolve":
					_, err = svc.ResolveTicket(ctx, before.ID, "bypassed resolution", tenant.ID)
				case "service_close":
					_, err = svc.CloseTicket(ctx, before.ID, tenant.ID, "closed")
				case "lifecycle_status":
					_, err = lifecycle.UpdateTicketStatus(ctx, before.ID, "pending", tenant.ID, actor.ID)
				case "lifecycle_resolve":
					_, err = lifecycle.ResolveTicket(ctx, before.ID, "bypassed resolution", tenant.ID, actor.ID)
				case "lifecycle_close":
					_, err = lifecycle.CloseTicket(ctx, before.ID, "closed", tenant.ID, actor.ID)
				case "workflow_resolve":
					err = workflow.ResolveTicket(ctx, &dto.ResolveTicketRequest{TicketID: before.ID, Resolution: "bypassed resolution"}, actor.ID, tenant.ID)
				case "workflow_close":
					err = workflow.CloseTicket(ctx, &dto.CloseTicketRequest{TicketID: before.ID}, actor.ID, tenant.ID)
				case "workflow_reopen":
					err = workflow.ReopenTicket(ctx, &dto.ReopenTicketRequest{TicketID: before.ID, Reason: "reopened"}, actor.ID, tenant.ID)
				case "workflow_withdraw":
					err = workflow.WithdrawTicket(ctx, &dto.WithdrawTicketRequest{TicketID: before.ID, Reason: "withdrawn"}, actor.ID, tenant.ID)
				}
				switch action {
				case "title", "description", "priority", "category", "resolution", "status":
					role := client.Role.Create().SetTenantID(tenant.ID).SetCode(actor.Role).SetName("Edit boundary fixture").SetIsActive(true).SaveX(ctx)
					permission := client.Permission.Create().SetTenantID(tenant.ID).SetCode("edit-boundary").SetName("Edit boundary").SetResource("*").SetAction("*").SaveX(ctx)
					client.RolePermission.Create().SetTenantID(tenant.ID).SetRoleID(role.ID).SetPermissionID(permission.ID).ExecX(ctx)
					_, err = svc.UpdateTicket(ctx, editCommandForTest(before.ID, patch, tenant.ID))
				}
				require.ErrorContains(t, err, "owning domain command")
				after := client.Ticket.GetX(ctx, before.ID)
				require.Equal(t, before.Version, after.Version)
				require.Equal(t, before.Status, after.Status)
				require.Equal(t, before.Title, after.Title)
				require.Equal(t, before.Description, after.Description)
				require.Equal(t, before.Priority, after.Priority)
				require.Equal(t, before.Resolution, after.Resolution)
				require.Zero(t, client.AuditLog.Query().CountX(ctx))
				require.Zero(t, client.TicketWorkflowRecord.Query().CountX(ctx))
				require.Zero(t, client.Notification.Query().CountX(ctx))
			})
		}
	}
}

func TestTicketCoreBoundaryPreservesSharedOperations(t *testing.T) {
	for _, class := range []string{"incident", "problem", "change_request", "generic", "service_request_item", "catalog_task"} {
		t.Run(class, func(t *testing.T) {
			client, owner, ctx := setupIncidentTest(t)
			defer client.Close()
			tenant, err := createIncidentTestTenant(ctx, client, "shared")
			require.NoError(t, err)
			actor, err := createIncidentTestUser(ctx, client, tenant.ID, "owner")
			require.NoError(t, err)
			role := client.Role.Create().SetTenantID(tenant.ID).SetCode(actor.Role).SetName("Shared edit fixture").SetIsActive(true).SaveX(ctx)
			permission := client.Permission.Create().SetTenantID(tenant.ID).SetCode("shared-edit").SetName("Shared edit").SetResource("*").SetAction("*").SaveX(ctx)
			client.RolePermission.Create().SetTenantID(tenant.ID).SetRoleID(role.ID).SetPermissionID(permission.ID).ExecX(ctx)

			next, err := createIncidentTestUser(ctx, client, tenant.ID, "next")
			require.NoError(t, err)
			item := client.Ticket.Create().SetTitle("original title").SetDescription("description").SetTicketNumber("SHARED").SetRequesterID(actor.ID).SetTenantID(tenant.ID).SetRecordClass(class).SetStatus("new").SaveX(ctx)
			svc := NewTicketService(&TicketServiceConfig{Client: client, Repository: ticketrepo.NewEntRepository(client, owner.logger), Logger: owner.logger, Execution: executionfixture.Standard()})
			_, err = svc.UpdateTicket(ctx, editCommandForTest(item.ID, &dto.TicketEditCommand{Fields: dto.TicketEditFields{Tags: []string{}}, Meta: workitemmutation.Meta{ActorID: actor.ID, ExpectedVersion: item.Version}}, tenant.ID))
			require.NoError(t, err)
			err = NewTicketWorkflowService(client, owner.logger).ForwardTicket(ctx, &dto.ForwardTicketRequest{TicketID: item.ID, ToUserID: next.ID, TransferOwnership: false, Comment: "collaborate"}, actor.ID, tenant.ID)
			require.NoError(t, err)
			after := client.Ticket.GetX(ctx, item.ID)
			require.Equal(t, item.AssigneeID, after.AssigneeID)
			require.Equal(t, item.Status, after.Status)
			if class == "generic" || class == "service_request_item" || class == "catalog_task" {
				_, err = svc.UpdateTicket(ctx, editCommandForTest(item.ID, &dto.TicketEditCommand{Fields: dto.TicketEditFields{Title: "updated title"}, Meta: workitemmutation.Meta{ActorID: actor.ID, ExpectedVersion: after.Version}}, tenant.ID))
				require.NoError(t, err)
				require.Equal(t, "updated title", client.Ticket.GetX(ctx, item.ID).Title)
			}
		})
	}
}

// editCommandForTest assembles a trusted fixture boundary without replacing the observed version.
func editCommandForTest(id int, input *dto.TicketEditCommand, tenantID int) dto.TicketEditCommand {
	cmd := *input
	cmd.WorkItemID = id
	cmd.Meta.TenantID = tenantID
	cmd.Meta.Source = "test"
	if cmd.Meta.OperationID == "" {
		cmd.Meta.OperationID = fmt.Sprintf("test-edit:%d:%d", id, cmd.Meta.ExpectedVersion)
	}
	return cmd
}
