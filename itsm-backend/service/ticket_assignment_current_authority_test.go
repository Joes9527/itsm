package service

import (
	"testing"

	"github.com/stretchr/testify/require"
	"itsm-backend/authorization"
	"itsm-backend/dto"
	"itsm-backend/ent/permission"
	"itsm-backend/ent/rolepermission"
	"itsm-backend/handlers/shared/workitemmutation"
	ticketrepo "itsm-backend/repository/ticket"
	executionfixture "itsm-backend/tests/fixtures/execution"
)

func TestTicketMixedAssignmentRequiresCurrentAuthority(t *testing.T) {
	for _, scenario := range []string{"update-only", "foreign-row", "revoked-cached-assign", "allowed", "clear", "replay-after-target-disabled"} {
		t.Run(scenario, func(t *testing.T) {
			f := newBPMNAuthorizationFixture(t)
			grants := []string{"ticket", "update"}
			if scenario != "update-only" {
				grants = append(grants, "ticket", "assign")
			}
			grantBoundPermissions(t, f, f.actor, grants...)
			item := f.client.Ticket.Create().SetTenantID(f.tenant.ID).SetRequesterID(f.actor.ID).SetAssigneeID(f.actor.ID).SetTitle("Before").SetTicketNumber("MIXED-AUTH").SetStatus("open").SaveX(f.userCtx)
			if scenario == "foreign-row" {
				item = f.client.Ticket.UpdateOne(item).SetRequesterID(f.outsider.ID).SetAssigneeID(f.outsider.ID).SaveX(f.userCtx)
			}
			actor := f.client.User.GetX(f.userCtx, f.actor.ID)
			old := authorization.PermissionConfig
			authorization.PermissionConfig.EnableCache = true
			t.Cleanup(func() { authorization.PermissionConfig = old; authorization.InvalidateAllPermissionCaches() })
			if scenario == "revoked-cached-assign" {
				_, err := authorization.LoadPermissionsByModeChecked(f.userCtx, f.client, actor.Role, f.tenant.ID)
				require.NoError(t, err)
				grant := f.client.Permission.Query().Where(permission.TenantID(f.tenant.ID), permission.Resource("ticket"), permission.Action("assign")).OnlyX(f.userCtx)
				f.client.RolePermission.Delete().Where(rolepermission.PermissionID(grant.ID)).ExecX(f.userCtx)
			}
			svc := NewTicketService(&TicketServiceConfig{Client: f.client, Repository: ticketrepo.NewEntRepository(f.client, f.engine.logger), Logger: f.engine.logger, Execution: executionfixture.Standard(), Directory: callbackFixtureDirectory{}})
			target := f.outsider.ID
			if scenario == "clear" {
				target = 0
			}
			cmd := dto.TicketEditCommand{WorkItemID: item.ID, Fields: dto.TicketEditFields{Title: "After", AssigneeID: &target}, Meta: workitemmutation.Meta{TenantID: f.tenant.ID, ActorID: f.actor.ID, ExpectedVersion: item.Version, Source: "http", OperationID: "mixed-auth"}}
			_, err := svc.UpdateTicket(f.userCtx, cmd)
			after := f.client.Ticket.GetX(f.userCtx, item.ID)
			allowed := scenario == "allowed" || scenario == "clear" || scenario == "replay-after-target-disabled"
			if !allowed {
				require.Error(t, err)
				require.Equal(t, item.AssigneeID, after.AssigneeID)
				require.Equal(t, item.Version, after.Version)
				require.Equal(t, item.Title, after.Title)
				require.Zero(t, f.client.TicketWorkflowRecord.Query().CountX(f.userCtx))
				require.Zero(t, f.client.AuditLog.Query().CountX(f.userCtx))
				require.Zero(t, f.client.OutboxEvent.Query().CountX(f.userCtx))
				return
			}
			require.NoError(t, err)
			require.Equal(t, target, after.AssigneeID)
			require.Equal(t, item.Version+1, after.Version)
			require.Equal(t, "After", after.Title)
			if scenario == "replay-after-target-disabled" {
				f.client.User.UpdateOneID(target).SetActive(false).ExecX(f.userCtx)
				_, err = svc.UpdateTicket(f.userCtx, cmd)
				require.NoError(t, err)
				require.Equal(t, after.Version, f.client.Ticket.GetX(f.userCtx, item.ID).Version)
				require.Equal(t, 1, f.client.OutboxEvent.Query().CountX(f.userCtx))
				require.Equal(t, 1, f.client.TicketWorkflowRecord.Query().CountX(f.userCtx))
			}
		})
	}
}
