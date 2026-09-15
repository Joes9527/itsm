//go:build integration_postgres

package integration

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
	"go.uber.org/zap"
	"itsm-backend/common/tenantctx"
	"itsm-backend/dto"
	"itsm-backend/ent"
	"itsm-backend/ent/ticketnotification"
	"itsm-backend/handlers/shared/workitemmutation"
	repository "itsm-backend/repository/ticket"
	"itsm-backend/service"
	executionfixture "itsm-backend/tests/fixtures/execution"
)

func TestPostgresAssignmentMSPMixedEditNotificationAuthority(t *testing.T) {
	for _, mode := range []string{"owner-and-status", "existing-owner-status", "rollback", "revoked", "foreign-recipient"} {
		t.Run(mode, func(t *testing.T) {
			f := newIncidentEffectsFixture(t)
			f.client.Tenant.UpdateOne(f.tenant).SetType("msp_customer").ExecX(f.ctx)
			editor := f.client.User.UpdateOne(f.actor).SetRole("super_admin").SaveX(f.ctx)
			provider := f.client.Tenant.Create().SetCode("edit-provider").SetName("Provider").SetType("msp_provider").SaveX(f.ctx)
			owner := f.client.User.Create().SetTenantID(provider.ID).SetUsername("edit-msp").SetName("MSP").SetEmail("edit-msp@example.test").SetPasswordHash("unused").SetRole("admin").SetMspRole("provider_agent").SaveX(f.ctx)
			allocation := f.client.MSPAllocation.Create().SetMspUserID(owner.ID).SetCustomerTenantID(f.tenant.ID).SetRole("primary").SaveX(f.ctx)
			item := f.client.Ticket.Create().SetTenantID(f.tenant.ID).SetTitle("Mixed MSP").SetTicketNumber("MSP-MIXED").SetRequesterID(editor.ID).SetOpenedByID(editor.ID).SetStatus("open").SaveX(f.ctx)
			if mode == "existing-owner-status" || mode == "revoked" {
				item = f.client.Ticket.UpdateOne(item).SetAssigneeID(owner.ID).SaveX(f.ctx)
			}
			for _, id := range []int{editor.ID, owner.ID} {
				f.client.NotificationPreference.Create().SetTenantID(f.tenant.ID).SetUserID(id).SetEventType("ticket_updated").SetInAppEnabled(true).SetEmailEnabled(false).SaveX(f.ctx)
			}
			clients, cfg := runtimeClients(t, f)
			for _, grant := range []string{"SELECT,UPDATE ON process_instances,process_tasks", "SELECT,INSERT ON ticket_workflow_records", "USAGE ON SEQUENCE ticket_workflow_records_id_seq", "SELECT ON notification_preferences", "SELECT ON process_audit_logs,process_callback_outboxes,service_requests"} {
				_, err := f.db.ExecContext(f.ctx, "GRANT "+grant+" TO "+cfg.User)
				require.NoError(t, err)
			}
			ctx := tenantctx.WithTenantID(f.ctx, f.tenant.ID)
			_, err := clients.Tenant.User.Get(ctx, owner.ID)
			require.True(t, ent.IsNotFound(err), "MSP identity must remain hidden by tenant RLS")
			logger := zap.NewNop().Sugar()
			notifications := service.NewTicketNotificationService(clients.Tenant, logger, executionfixture.Standard("notification"))
			notifications.SetAssignmentDirectory(clients.IntakeDirectorySnapshot())
			svc := service.NewTicketService(&service.TicketServiceConfig{Client: clients.Tenant, Repository: repository.NewEntRepository(clients.Tenant, logger), Logger: logger, Execution: executionfixture.Standard(), Directory: clients.IntakeDirectorySnapshot(), NotificationService: notifications})
			if mode == "foreign-recipient" {
				tx, err := clients.Tenant.Tx(ctx)
				require.NoError(t, err)
				defer tx.Rollback()
				err = notifications.EnqueueNotificationTx(ctx, tx, item.ID, item.TenantID, &dto.SendTicketNotificationRequest{UserIDs: []int{owner.ID}, EventType: "ticket_updated", Content: "foreign", DeliveryKey: "ticket:edit:foreign"})
				require.ErrorContains(t, err, "notification recipient missing", "allocated MSP user is not an arbitrary notification recipient")
				require.NoError(t, tx.Rollback())
				require.Zero(t, f.client.TicketNotification.Query().CountX(f.ctx))
				return
			}
			if mode == "revoked" {
				allocation.Update().SetDeassignedAt(time.Now()).ExecX(f.ctx)
			}
			if mode == "rollback" {
				clients.Tenant.TicketNotification.Use(func(next ent.Mutator) ent.Mutator {
					return ent.MutateFunc(func(ctx context.Context, m ent.Mutation) (ent.Value, error) {
						return nil, errors.New("mixed notification rollback")
					})
				})
			}
			var target *int
			if mode == "owner-and-status" || mode == "rollback" {
				target = &owner.ID
			}
			command := dto.TicketEditCommand{WorkItemID: item.ID, Fields: dto.TicketEditFields{AssigneeID: target, Status: "in_progress"}, Meta: workitemmutation.Meta{TenantID: item.TenantID, ActorID: editor.ID, ExpectedVersion: item.Version, Source: "http", OperationID: "msp-mixed"}}
			beforeAudits := f.client.AuditLog.Query().CountX(f.ctx)
			beforeOutbox := f.client.OutboxEvent.Query().CountX(f.ctx)
			_, err = svc.UpdateTicket(ctx, command)
			after := f.client.Ticket.GetX(f.ctx, item.ID)
			if mode == "rollback" || mode == "revoked" {
				require.Error(t, err)
				if mode == "rollback" {
					require.ErrorContains(t, err, "mixed notification rollback")
				}
				if mode == "revoked" {
					require.NotContains(t, err.Error(), "execution scope", "revocation must be rejected by current recipient authority")
				}

				require.Equal(t, item.AssigneeID, after.AssigneeID)
				require.Equal(t, item.Version, after.Version)
				require.Equal(t, item.Status, after.Status)
				require.Equal(t, beforeAudits, f.client.AuditLog.Query().CountX(f.ctx))
				require.Equal(t, beforeOutbox, f.client.OutboxEvent.Query().CountX(f.ctx))
				require.Zero(t, f.client.TicketWorkflowRecord.Query().CountX(f.ctx))
				require.Zero(t, f.client.TicketNotification.Query().CountX(f.ctx))
				return
			}
			require.NoError(t, err)
			require.Equal(t, owner.ID, after.AssigneeID)
			require.Equal(t, item.Version+1, after.Version)
			require.Equal(t, "in_progress", after.Status)
			rows := f.client.TicketNotification.Query().Where(ticketnotification.TicketID(item.ID)).AllX(f.ctx)
			require.Len(t, rows, 2)
			ids := []int{}
			for _, row := range rows {
				ids = append(ids, row.UserID)
				require.NotNil(t, row.DeliveryKey)
				require.Contains(t, *row.DeliveryKey, "ticket:edit:")
			}
			require.ElementsMatch(t, []int{editor.ID, owner.ID}, ids)
			_, err = svc.UpdateTicket(ctx, command)
			require.NoError(t, err)
			require.Equal(t, 2, f.client.TicketNotification.Query().Where(ticketnotification.TicketID(item.ID)).CountX(f.ctx))
			require.Equal(t, after.Version, f.client.Ticket.GetX(f.ctx, item.ID).Version)
		})
	}
}
