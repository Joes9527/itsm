package router

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"itsm-backend/controller"
	"itsm-backend/ent/enttest"
	domainCommon "itsm-backend/handlers/common"
	"itsm-backend/middleware"
	"itsm-backend/service"

	"github.com/gin-gonic/gin"
	_ "github.com/mattn/go-sqlite3"
	"github.com/stretchr/testify/require"
	"go.uber.org/zap/zaptest"
)

func TestTicketNotificationReadRoutesUseDedicatedTenantRBACNamespace(t *testing.T) {
	gin.SetMode(gin.TestMode)
	middleware.InvalidateAllPermissionCaches()
	client := enttest.Open(t, "sqlite3", "file:ticket_notification_routes?mode=memory&cache=shared&_fk=1")
	defer client.Close()
	ctx := context.Background()
	ten, err := client.Tenant.Create().SetName("notification-routes").SetCode("notification-routes").SetDomain("notification-routes.test").SetStatus("active").Save(ctx)
	require.NoError(t, err)
	role, err := client.Role.Create().SetCode("notification_reader").SetName("Notification Reader").SetTenantID(ten.ID).Save(ctx)
	require.NoError(t, err)
	user, err := client.User.Create().SetUsername("notification-reader").SetEmail("notification-reader@example.test").SetName("Notification Reader").SetPasswordHash("x").SetRole(role.Code).SetActive(true).SetTenantID(ten.ID).Save(ctx)
	require.NoError(t, err)

	logger := zaptest.NewLogger(t).Sugar()
	ticketNotifications := controller.NewTicketNotificationController(service.NewTicketNotificationService(client, logger), logger)
	genericNotifications := controller.NewNotificationController(service.NewNotificationService(client))
	router := gin.New()
	SetupRoutes(router, &RouterConfig{JWTSecret: "ticket-notification-routes", Logger: logger, Client: client, CommonHandler: &domainCommon.Handler{}, TicketNotificationController: ticketNotifications, NotificationController: genericNotifications})

	routes := map[string]bool{}
	for _, route := range router.Routes() {
		routes[route.Method+" "+route.Path] = true
	}
	require.True(t, routes["PUT /api/v1/ticket-notifications/:id/read"])
	require.True(t, routes["PUT /api/v1/ticket-notifications/read-all"])
	require.True(t, routes["PUT /api/v1/notifications/:id/read"], "generic notification IDs retain their separate route")

	request := httptest.NewRequest(http.MethodPut, "/api/v1/ticket-notifications/1/read", nil)
	recorder := httptest.NewRecorder()
	router.ServeHTTP(recorder, request)
	require.Equal(t, http.StatusUnauthorized, recorder.Code)

	token, err := middleware.GenerateAccessToken(user.ID, user.Username, role.Code, ten.ID, "ticket-notification-routes", time.Hour)
	require.NoError(t, err)
	request = httptest.NewRequest(http.MethodPut, "/api/v1/ticket-notifications/1/read", nil)
	request.Header.Set("Authorization", "Bearer "+token)
	recorder = httptest.NewRecorder()
	router.ServeHTTP(recorder, request)
	require.Equal(t, http.StatusForbidden, recorder.Code)
}
