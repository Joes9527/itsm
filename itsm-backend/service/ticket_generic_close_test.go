package service

import (
	"github.com/stretchr/testify/require"
	"go.uber.org/zap"
	"itsm-backend/authorization"
	"itsm-backend/database"
	"itsm-backend/dto"
	"itsm-backend/ent"
	"itsm-backend/handlers/shared/workitemmutation"
	ticketrepo "itsm-backend/repository/ticket"
	executionfixture "itsm-backend/tests/fixtures/execution"
	"testing"
	"time"
)

func TestGenericCloseUsesVersionedEditAuthority(t *testing.T) {
	for _, scenario := range []string{"allowed", "no-permission", "foreign-tenant", "mixed-title", "mixed-resolution", "mixed-tags", "mixed-category", "mixed-assignee", "mixed-form", "stale-version", "missing-operation", "professional", "already-closed", "cancelled"} {
		t.Run(scenario, func(t *testing.T) {
			f := newBPMNAuthorizationFixture(t)
			if scenario != "no-permission" {
				genericCloseGrantPermission(t, f)
			}
			recordClass := "generic"
			if scenario == "professional" {
				recordClass = "incident"
			}
			item := f.client.Ticket.Create().SetTenantID(f.tenant.ID).SetRequesterID(f.actor.ID).SetAssigneeID(f.actor.ID).SetTitle("Resolved generic").SetTicketNumber("CLOSE-AUTH").SetRecordClass(recordClass).SetStatus("resolved").SetResolvedAt(time.Now().Add(-time.Hour)).SetResolution("Verified solution").SaveX(f.userCtx)
			if scenario == "foreign-tenant" {
				item = f.client.Ticket.UpdateOne(item).SetTenantID(f.otherTenant.ID).SaveX(f.userCtx)
			}

			if scenario == "already-closed" {
				item = f.client.Ticket.UpdateOne(item).SetStatus("closed").SaveX(f.userCtx)
			}
			if scenario == "cancelled" {
				item = f.client.Ticket.UpdateOne(item).SetStatus("cancelled").SaveX(f.userCtx)
			}
			repo := ticketrepo.NewEntRepository(f.client, f.engine.logger)
			svc := NewTicketService(&TicketServiceConfig{Client: f.client, Repository: repo, Logger: f.engine.logger, Execution: executionfixture.Standard()})
			svc.SetNotificationService(genericCloseNotificationService(f.client, f.engine.logger, executionfixture.Standard()))
			actor := f.client.User.GetX(f.userCtx, f.actor.ID)
			snapshot, err := repo.GetByID(f.userCtx, item.ID, item.TenantID)
			require.NoError(t, err)
			projected := BuildTicketActions(f.userCtx, ActionActor{Client: f.client, TenantID: f.tenant.ID, UserID: f.actor.ID, Role: actor.Role}, snapshot)
			if scenario == "allowed" {
				require.True(t, projected["close"].Allowed)
				require.False(t, projected["edit"].Allowed)
			}
			if scenario == "no-permission" || scenario == "foreign-tenant" || scenario == "professional" || scenario == "already-closed" || scenario == "cancelled" {
				require.False(t, projected["close"].Allowed)
			}
			cmd := dto.TicketEditCommand{WorkItemID: item.ID, Fields: dto.TicketEditFields{Status: "closed"}, Meta: workitemmutation.Meta{TenantID: f.tenant.ID, ActorID: f.actor.ID, ExpectedVersion: item.Version, Source: "http", OperationID: "generic-close"}}
			switch scenario {
			case "mixed-title":
				cmd.Fields.Title = "Overwrite"
			case "mixed-resolution":
				cmd.Fields.Resolution = "Overwrite"
			case "mixed-tags":
				cmd.Fields.Tags = []string{}
			case "mixed-category":
				zero := 0
				cmd.Fields.CategoryID = &zero
			case "mixed-assignee":
				zero := 0
				cmd.Fields.AssigneeID = &zero
			case "mixed-form":
				cmd.Fields.FormFields = map[string]interface{}{}
			case "stale-version":
				cmd.Meta.ExpectedVersion++
			case "missing-operation":
				cmd.Meta.OperationID = ""
			}
			result, err := svc.UpdateTicket(f.userCtx, cmd)
			after := f.client.Ticket.GetX(f.userCtx, item.ID)
			if scenario != "allowed" {
				require.Error(t, err)
				require.Equal(t, item.Status, after.Status)
				require.Equal(t, item.Version, after.Version)
				require.Zero(t, f.client.OutboxEvent.Query().CountX(f.userCtx))
				return
			}
			require.NoError(t, err)
			require.Equal(t, "closed", after.Status)
			require.Equal(t, item.Version+1, after.Version)
			require.Equal(t, "Verified solution", after.Resolution)
			require.False(t, after.ClosedAt.IsZero())
			require.True(t, item.ResolvedAt.Equal(after.ResolvedAt))
			require.Equal(t, item.Title, after.Title)
			auditCount := f.client.AuditLog.Query().CountX(f.userCtx)
			outboxCount := f.client.OutboxEvent.Query().CountX(f.userCtx)
			notificationCount := f.client.TicketNotification.Query().CountX(f.userCtx)
			require.Equal(t, 1, auditCount)
			replay, err := svc.UpdateTicket(f.userCtx, cmd)
			require.NoError(t, err)
			require.True(t, replay.Replayed)
			require.Equal(t, result.Version, replay.Version)
			require.Equal(t, outboxCount, f.client.OutboxEvent.Query().CountX(f.userCtx))
			require.Equal(t, auditCount, f.client.AuditLog.Query().CountX(f.userCtx))
			require.Equal(t, notificationCount, f.client.TicketNotification.Query().CountX(f.userCtx))
			cmd.Fields.Title = "changed operation content"
			_, err = svc.UpdateTicket(f.userCtx, cmd)
			require.Error(t, err)
			snapshot, err = repo.GetByID(f.userCtx, item.ID, item.TenantID)
			require.NoError(t, err)
			require.False(t, BuildTicketActions(f.userCtx, ActionActor{Client: f.client, TenantID: f.tenant.ID, UserID: f.actor.ID, Role: actor.Role}, snapshot)["close"].Allowed)
		})
	}
}

func genericCloseGrantPermission(t *testing.T, f *bpmnAuthorizationFixture) {
	t.Helper()
	role := "generic-close-role"
	f.client.User.UpdateOne(f.actor).SetRole(role).ExecX(f.userCtx)
	r := f.client.Role.Create().SetCode(role).SetName(role).SetTenantID(f.tenant.ID).SaveX(f.userCtx)
	p := f.client.Permission.Create().SetCode("generic-close-update").SetName("close").SetResource("ticket").SetAction("update").SetTenantID(f.tenant.ID).SaveX(f.userCtx)
	f.client.RolePermission.Create().SetRoleID(r.ID).SetPermissionID(p.ID).SetTenantID(f.tenant.ID).SaveX(f.userCtx)
	authorization.InvalidateRolePermissionCache(role, f.tenant.ID)
	t.Cleanup(func() { authorization.InvalidateRolePermissionCache(role, f.tenant.ID) })
}

func genericCloseNotificationService(client *ent.Client, logger *zap.SugaredLogger, policy *database.ExecutionPolicy) *TicketNotificationService {
	notifications := NewTicketNotificationService(client, logger, policy)
	mail := NewEmailService(EmailConfig{DeliveryTransport: "smtp", Host: "smtp.example.invalid", Port: 2525, Username: "fixture", From: "fixture@example.invalid"}, logger)
	mail.SetDeliveryTargetDependencies(nil, policy)
	notifications.SetEmailService(mail)
	return notifications
}
