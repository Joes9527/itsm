package controller

import (
	"context"
	"net/http"
	"path/filepath"
	"strconv"
	"testing"

	"itsm-backend/common"
	"itsm-backend/ent"
	"itsm-backend/ent/enttest"
	"itsm-backend/service"

	"github.com/gin-gonic/gin"
	_ "github.com/mattn/go-sqlite3"
	"github.com/stretchr/testify/require"
	"go.uber.org/zap/zaptest"
)

func setupTicketNotificationController(t *testing.T) (*gin.Engine, *ent.Client, int, int) {
	t.Helper()
	gin.SetMode(gin.TestMode)
	client := enttest.Open(t, "sqlite3", "file:"+filepath.Join(t.TempDir(), "ticket_notification_controller.db")+"?_fk=1")
	tenantID, userID := seedTenantUser(t, client)
	controller := NewTicketNotificationController(service.NewTicketNotificationService(client, zaptest.NewLogger(t).Sugar()), zaptest.NewLogger(t).Sugar())

	router := gin.New()
	router.Use(gin.Recovery(), withTestAuth(tenantID, userID))
	router.PUT("/api/v1/ticket-notifications/:id/read", controller.MarkNotificationRead)
	router.PUT("/api/v1/ticket-notifications/read-all", controller.MarkAllNotificationsRead)
	return router, client, tenantID, userID
}

func createTicketNotificationForController(t *testing.T, client *ent.Client, tenantID, userID int, status string) *ent.TicketNotification {
	t.Helper()
	ctx := context.Background()
	ticket, err := client.Ticket.Create().
		SetTicketNumber("TKT-NOTIFICATION-" + uniqueTestID()).
		SetTitle("Notification contract ticket").
		SetDescription("Ticket notification read contract").
		SetStatus("open").
		SetPriority("medium").
		SetRequesterID(userID).
		SetTenantID(tenantID).
		Save(ctx)
	require.NoError(t, err)

	notification, err := client.TicketNotification.Create().
		SetTicketID(ticket.ID).
		SetUserID(userID).
		SetType("assigned").
		SetChannel("in_app").
		SetContent("You have a ticket update").
		SetStatus(status).
		SetTenantID(tenantID).
		Save(ctx)
	require.NoError(t, err)
	return notification
}

func TestTicketNotificationController_MarkReadUsesAuthenticatedTenantAndUser(t *testing.T) {
	router, client, tenantID, userID := setupTicketNotificationController(t)
	notification := createTicketNotificationForController(t, client, tenantID, userID, "pending")

	response := doReq(t, router, http.MethodPut, "/api/v1/ticket-notifications/"+strconv.Itoa(notification.ID)+"/read", nil, false)
	require.Equal(t, common.SuccessCode, response.Code, "body=%s", mustString(response))

	updated := client.TicketNotification.GetX(context.Background(), notification.ID)
	require.False(t, updated.ReadAt.IsZero())
	require.Equal(t, "pending", updated.Status)
}

func TestTicketNotificationController_MarkAllReadDoesNotCrossTenantOrDeliveryState(t *testing.T) {
	router, client, tenantID, userID := setupTicketNotificationController(t)
	owned := createTicketNotificationForController(t, client, tenantID, userID, "sent")
	otherTenantID, otherUserID := seedTenantUser(t, client)
	other := createTicketNotificationForController(t, client, otherTenantID, otherUserID, "processing")

	response := doReq(t, router, http.MethodPut, "/api/v1/ticket-notifications/read-all", nil, false)
	require.Equal(t, common.SuccessCode, response.Code, "body=%s", mustString(response))

	owned = client.TicketNotification.GetX(context.Background(), owned.ID)
	other = client.TicketNotification.GetX(context.Background(), other.ID)
	require.False(t, owned.ReadAt.IsZero())
	require.Equal(t, "sent", owned.Status)
	require.True(t, other.ReadAt.IsZero())
	require.Equal(t, "processing", other.Status)
}
