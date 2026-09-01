package controller

import (
	"context"
	"fmt"
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
	"go.uber.org/zap"
	"go.uber.org/zap/zaptest/observer"
)

func setupTicketNotificationController(t *testing.T) (*gin.Engine, *ent.Client, int, int, *observer.ObservedLogs) {
	t.Helper()
	gin.SetMode(gin.TestMode)
	client := enttest.Open(t, "sqlite3", "file:"+filepath.Join(t.TempDir(), "ticket_notification_controller.db")+"?_fk=1")
	tenantID, userID := seedTenantUser(t, client)
	core, logs := observer.New(zap.DebugLevel)
	logger := zap.New(core).Sugar()
	controller := NewTicketNotificationController(service.NewTicketNotificationService(client, logger), logger)

	router := gin.New()
	router.Use(gin.Recovery(), withTestAuth(tenantID, userID))
	router.POST("/api/v1/tickets/:id/notifications", controller.SendTicketNotification)
	router.PUT("/api/v1/ticket-notifications/:id/read", controller.MarkNotificationRead)
	router.PUT("/api/v1/ticket-notifications/read-all", controller.MarkAllNotificationsRead)
	return router, client, tenantID, userID, logs
}

func TestTicketNotificationController_SendRejectsLegacyFields(t *testing.T) {
	router, client, tenantID, userID, _ := setupTicketNotificationController(t)
	ticketEntity, err := client.Ticket.Create().
		SetTicketNumber("TKT-NOTIFICATION-SEND-" + uniqueTestID()).
		SetTitle("Notification send contract").SetDescription("d").SetStatus("open").SetPriority("medium").
		SetRequesterID(userID).SetTenantID(tenantID).Save(context.Background())
	require.NoError(t, err)

	response := doReq(t, router, http.MethodPost, "/api/v1/tickets/"+strconv.Itoa(ticketEntity.ID)+"/notifications", map[string]interface{}{
		"userIds": []int{userID}, "eventType": "ticket_updated", "content": "x", "channel": "in_app",
	}, false)
	require.Equal(t, common.ParamErrorCode, response.Code, "body=%s", mustString(response))
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
	router, client, tenantID, userID, _ := setupTicketNotificationController(t)
	notification := createTicketNotificationForController(t, client, tenantID, userID, "pending")

	response := doReq(t, router, http.MethodPut, "/api/v1/ticket-notifications/"+strconv.Itoa(notification.ID)+"/read", nil, false)
	require.Equal(t, common.SuccessCode, response.Code, "body=%s", mustString(response))

	updated := client.TicketNotification.GetX(context.Background(), notification.ID)
	require.False(t, updated.ReadAt.IsZero())
	require.Equal(t, "pending", updated.Status)
}

func TestTicketNotificationController_MarkAllReadDoesNotCrossTenantOrDeliveryState(t *testing.T) {
	router, client, tenantID, userID, _ := setupTicketNotificationController(t)
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

func TestTicketNotificationController_MarkReadHidesMissingAndForeignOwnership(t *testing.T) {
	router, client, _, _, logs := setupTicketNotificationController(t)
	otherTenantID, otherUserID := seedTenantUser(t, client)
	foreign := createTicketNotificationForController(t, client, otherTenantID, otherUserID, "sent")

	for _, notificationID := range []int{foreign.ID, foreign.ID + 100000} {
		response := doReq(t, router, http.MethodPut, "/api/v1/ticket-notifications/"+strconv.Itoa(notificationID)+"/read", nil, false)
		require.Equal(t, common.NotFoundCode, response.Code, "body=%s", mustString(response))
		require.Equal(t, "通知不存在或无权限", response.Message)
	}

	require.Empty(t, logs.All(), "ownership misses must not emit storage-error logs")
}

func TestTicketNotificationController_MarkReadSanitizesStorageFailure(t *testing.T) {
	router, client, _, _, logs := setupTicketNotificationController(t)
	require.NoError(t, client.Close())

	response := doReq(t, router, http.MethodPut, "/api/v1/ticket-notifications/1/read", nil, false)
	require.Equal(t, common.InternalErrorCode, response.Code, "body=%s", mustString(response))
	require.Equal(t, "通知读取状态更新失败", response.Message)

	entries := logs.All()
	require.Len(t, entries, 1)
	fields := entries[0].ContextMap()
	require.Equal(t, "ticket_notification_read_storage", fields["error_class"])
	require.NotContains(t, fields, "error")
	require.NotContains(t, fmt.Sprint(entries), "sql: database is closed")
}
