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
	"itsm-backend/authentication"
	"itsm-backend/authorization"
	"itsm-backend/common/tenantctx"
	"itsm-backend/controller"
	"itsm-backend/dto"
	"itsm-backend/ent"
	"itsm-backend/ent/ticketnotification"
	change "itsm-backend/handlers/change"
	assignment "itsm-backend/handlers/common/workitemassignment"
	creation "itsm-backend/handlers/common/workitemcreation"
	problem "itsm-backend/handlers/problem"
	request "itsm-backend/handlers/service_request"
	repository "itsm-backend/repository/ticket"
	"itsm-backend/router"
	"itsm-backend/service"
	"net/http/httptest"
	"testing"
	"time"
)

type assignmentReviewDirectory struct{ failClose bool }

func (d assignmentReviewDirectory) Open(_ context.Context, tx *ent.Tx, _ int) (*ent.Client, func() error, error) {
	return tx.Client(), func() error {
		if d.failClose {
			return errors.New("forced outer directory close failure")
		}
		return nil
	}, nil
}
func TestPostgresAssignmentReviewMixedStatusAtomic(t *testing.T) {
	for _, mode := range []struct{ fail, inapp bool }{{false, true}, {true, true}, {true, false}} {
		fail := mode.fail
		t.Run(fmt.Sprintf("outer_failure_%v_inapp_%v", fail, mode.inapp), func(t *testing.T) {
			client, cmd := assignmentFixture(t)
			base := tenantctx.WithTenantID(context.Background(), cmd.TenantID)
			actor := client.User.UpdateOneID(cmd.ActorID).SetRole("super_admin").SaveX(base)
			previous := client.User.Create().SetTenantID(cmd.TenantID).SetUsername("previous-owner").SetName("Previous").SetEmail("previous@example.test").SetPasswordHash("unused").SaveX(base)
			client.Ticket.UpdateOneID(cmd.WorkItemID).SetAssigneeID(previous.ID).SetStatus("open").ExecX(base)
			before := client.Ticket.GetX(base, cmd.WorkItemID)
			for _, id := range []int{actor.ID, previous.ID, cmd.AssigneeID} {
				client.NotificationPreference.Create().SetTenantID(cmd.TenantID).SetUserID(id).SetEventType("ticket_updated").SetInAppEnabled(mode.inapp).SetEmailEnabled(true).SaveX(base)
			}
			logger := zap.NewNop().Sugar()
			notifications := service.NewTicketNotificationService(client, logger)
			notifications.SetAssignmentDirectory(sameTransactionDirectory{})
			sender := &assignmentMailRecorder{}
			email := service.NewEmailService(service.EmailConfig{}, logger)
			email.SetGraphProvider(func(int) (service.GraphMailSender, string, bool) { return sender, "support@example.test", true })
			notifications.SetEmailService(email)
			svc := service.NewTicketService(&service.TicketServiceConfig{Client: client, Repository: repository.NewEntRepository(client, logger), Logger: logger, NotificationService: notifications, SessionReader: authorization.NewSessionReader(client, assignmentReviewDirectory{failClose: fail})})
			ctx, cancel := context.WithTimeout(base, 2*time.Second)
			defer cancel()
			_, err := svc.UpdateTicket(ctx, cmd.WorkItemID, &dto.UpdateTicketRequest{AssigneeID: &cmd.AssigneeID, Status: "in_progress"}, cmd.TenantID, creation.Identity{ActorID: actor.ID, TenantID: cmd.TenantID, Role: actor.Role, Channel: "http"})
			require.NoError(t, ctx.Err(), "notification must not exhaust outer deadline")
			require.Zero(t, sender.calls, "no external send before owning transaction commit")
			after := client.Ticket.GetX(base, cmd.WorkItemID)
			if fail {
				require.ErrorContains(t, err, "directory")
				require.Equal(t, before.AssigneeID, after.AssigneeID)
				require.Equal(t, before.Version, after.Version)
				require.Equal(t, before.Status, after.Status)
				require.Zero(t, client.Notification.Query().CountX(base))
				require.Zero(t, client.TicketNotification.Query().CountX(base))
				require.Zero(t, client.OutboxEvent.Query().CountX(base))
				return
			}
			require.NoError(t, err, "mixed update must not wait on its own uncommitted FK")
			require.Equal(t, cmd.AssigneeID, after.AssigneeID)
			require.Equal(t, "in_progress", after.Status)
			require.Equal(t, before.Version+1, after.Version)
			rows := client.TicketNotification.Query().Where(ticketnotification.TypeEQ("ticket_updated")).AllX(base)
			require.Len(t, rows, 4)
			for _, row := range rows {
				require.NotEqual(t, previous.ID, row.UserID)
				require.Contains(t, []int{actor.ID, cmd.AssigneeID}, row.UserID)
			}
		})
	}
}
func TestPostgresAssignmentReviewForwardWithoutTransfer(t *testing.T) {
	client, cmd := assignmentFixture(t)
	ctx := tenantctx.WithTenantID(context.Background(), cmd.TenantID)
	role := client.Role.Create().SetTenantID(cmd.TenantID).SetCode("workflow_only").SetName("Workflow only").SaveX(ctx)
	permission := client.Permission.Create().SetTenantID(cmd.TenantID).SetCode("workflow:update").SetName("Forward").SetResource("workflow").SetAction("update").SaveX(ctx)
	client.RolePermission.Create().SetTenantID(cmd.TenantID).SetRoleID(role.ID).SetPermissionID(permission.ID).SaveX(ctx)
	actor := client.User.UpdateOneID(cmd.ActorID).SetRole(role.Code).SaveX(ctx)
	client.Ticket.UpdateOneID(cmd.WorkItemID).SetRequesterID(actor.ID).ExecX(ctx)
	svc := service.NewTicketWorkflowService(client, zap.NewNop().Sugar())
	svc.SetSessionReader(authorization.NewSessionReader(client, sameTransactionDirectory{}))
	identity := creation.Identity{ActorID: actor.ID, TenantID: cmd.TenantID, Role: role.Code, Channel: "http"}
	require.NoError(t, svc.ForwardTicket(ctx, &dto.ForwardTicketRequest{TicketID: cmd.WorkItemID, ToUserID: cmd.AssigneeID, TransferOwnership: false}, actor.ID, cmd.TenantID, identity))
	require.Zero(t, client.OutboxEvent.Query().CountX(ctx))
	require.Zero(t, client.Ticket.GetX(ctx, cmd.WorkItemID).AssigneeID)
	require.Error(t, svc.ForwardTicket(ctx, &dto.ForwardTicketRequest{TicketID: cmd.WorkItemID, ToUserID: cmd.AssigneeID, TransferOwnership: true}, actor.ID, cmd.TenantID, identity))
}
func TestPostgresAssignmentReviewProfessionalRegisteredRoute(t *testing.T) {
	for _, class := range []string{"service_request_item", "incident", "problem", "change_request"} {
		t.Run(class, func(t *testing.T) {
			client, cmd := assignmentFixture(t)
			ctx := context.Background()
			actor := client.User.UpdateOneID(cmd.ActorID).SetRole("super_admin").SaveX(ctx)
			item := client.Ticket.Create().SetTenantID(cmd.TenantID).SetRequesterID(actor.ID).SetOpenedByID(actor.ID).SetTitle("Professional assign").SetTicketNumber("PRO-ASSIGN").SetRecordClass(class).SetStatus("open").SaveX(ctx)
			switch class {
			case "service_request_item":
				client.ServiceRequest.Create().SetTicketID(item.ID).SetCatalogID(1).SaveX(ctx)
			case "incident":
				client.Incident.Create().SetWorkItemID(item.ID).SaveX(ctx)
			case "problem":
				client.Problem.Create().SetWorkItemID(item.ID).SaveX(ctx)
			case "change_request":
				client.Ticket.UpdateOneID(item.ID).SetStatus("draft").ExecX(ctx)
				client.Change.Create().SetWorkItemID(item.ID).SaveX(ctx)
			}
			logger := zap.NewNop().Sugar()
			svc := service.NewTicketService(&service.TicketServiceConfig{Client: client, Repository: repository.NewEntRepository(client, logger), Logger: logger, SessionReader: authorization.NewSessionReader(client, sameTransactionDirectory{})})
			for _, owner := range []assignment.Owner{service.NewIncidentService(client, logger), problem.NewService(problem.NewEntRepository(client), logger), change.NewService(change.NewEntRepository(client, nil), client, logger), request.NewService(nil, client, logger, nil)} {
				require.NoError(t, svc.RegisterAssignmentOwner(owner))
			}
			engine := gin.New()
			router.SetupRoutes(engine, &router.RouterConfig{JWTSecret: "assignment-review-secret", Client: client, TenantDirectoryClient: client, Logger: logger, TicketController: controller.NewTicketController(svc, nil, nil, client, logger)})
			token, err := authentication.GenerateAccessToken(actor.ID, actor.Username, actor.Role, cmd.TenantID, "assignment-review-secret", time.Hour)
			require.NoError(t, err)
			request := httptest.NewRequest("POST", fmt.Sprintf("/api/v1/tickets/%d/assign", item.ID), bytes.NewBufferString(fmt.Sprintf(`{"assigneeId":%d}`, cmd.AssigneeID)))
			request.Header.Set("Authorization", "Bearer "+token)
			request.Header.Set("Content-Type", "application/json")
			recorder := httptest.NewRecorder()
			engine.ServeHTTP(recorder, request)
			require.Equal(t, cmd.AssigneeID, client.Ticket.GetX(ctx, item.ID).AssigneeID, recorder.Body.String())
			require.Equal(t, 1, client.OutboxEvent.Query().CountX(ctx))
			first := client.Ticket.GetX(ctx, item.ID)
			retry := httptest.NewRequest("POST", fmt.Sprintf("/api/v1/tickets/%d/assign", item.ID), bytes.NewBufferString(fmt.Sprintf(`{"assigneeId":%d}`, cmd.AssigneeID)))
			retry.Header.Set("Authorization", "Bearer "+token)
			retry.Header.Set("Content-Type", "application/json")
			replay := httptest.NewRecorder()
			engine.ServeHTTP(replay, retry)
			require.Equal(t, first.Version, client.Ticket.GetX(ctx, item.ID).Version, replay.Body.String())
			require.Equal(t, 1, client.OutboxEvent.Query().CountX(ctx))
			if class == "incident" {
				require.Equal(t, 1, client.IncidentEvent.Query().CountX(ctx), "owning Incident audit must remain once")
			}
			client.Ticket.UpdateOneID(item.ID).SetStatus("closed").ExecX(ctx)
			terminal := httptest.NewRequest("POST", fmt.Sprintf("/api/v1/tickets/%d/assign", item.ID), bytes.NewBufferString(fmt.Sprintf(`{"assigneeId":%d}`, actor.ID)))
			terminal.Header.Set("Authorization", "Bearer "+token)
			terminal.Header.Set("Content-Type", "application/json")
			denied := httptest.NewRecorder()
			engine.ServeHTTP(denied, terminal)
			require.Equal(t, cmd.AssigneeID, client.Ticket.GetX(ctx, item.ID).AssigneeID)
			require.Equal(t, 1, client.OutboxEvent.Query().CountX(ctx))

		})
	}
}
