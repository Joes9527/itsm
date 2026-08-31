package router

import (
	"context"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"

	"itsm-backend/controller"
	"itsm-backend/ent"
	"itsm-backend/ent/enttest"
	domainCommon "itsm-backend/handlers/common"
	"itsm-backend/middleware"
	"itsm-backend/service"

	"github.com/gin-gonic/gin"
	_ "github.com/mattn/go-sqlite3"
	"github.com/stretchr/testify/require"
	"go.uber.org/zap/zaptest"
)

type ticketNotificationRouteEnv struct {
	router         *gin.Engine
	client         *ent.Client
	token          string
	notificationID int
}

func setupTicketNotificationRouteEnv(t *testing.T, roleCode string, permissions ...string) *ticketNotificationRouteEnv {
	t.Helper()
	gin.SetMode(gin.TestMode)
	middleware.InvalidateAllPermissionCaches()
	client := enttest.Open(t, "sqlite3", "file:"+filepath.Join(t.TempDir(), "ticket_notification_routes.db")+"?_fk=1")
	t.Cleanup(func() {
		middleware.InvalidateAllPermissionCaches()
		client.Close()
	})

	ctx := context.Background()
	tenant, err := client.Tenant.Create().
		SetName("notification-routes").
		SetCode("notification-routes").
		SetDomain("notification-routes.test").
		SetStatus("active").
		Save(ctx)
	require.NoError(t, err)
	role, err := client.Role.Create().
		SetCode(roleCode).
		SetName(roleCode).
		SetTenantID(tenant.ID).
		Save(ctx)
	require.NoError(t, err)
	for _, code := range permissions {
		parts := strings.SplitN(code, ":", 2)
		require.Len(t, parts, 2)
		permission, createErr := client.Permission.Create().
			SetCode(code).
			SetName(code).
			SetResource(parts[0]).
			SetAction(parts[1]).
			SetTenantID(tenant.ID).
			Save(ctx)
		require.NoError(t, createErr)
		_, createErr = client.RolePermission.Create().
			SetRoleID(role.ID).
			SetPermissionID(permission.ID).
			SetTenantID(tenant.ID).
			Save(ctx)
		require.NoError(t, createErr)
	}
	user, err := client.User.Create().
		SetUsername(roleCode + "-reader").
		SetEmail(roleCode + "-reader@example.test").
		SetName(roleCode + " reader").
		SetPasswordHash("x").
		SetRole(role.Code).
		SetActive(true).
		SetTenantID(tenant.ID).
		Save(ctx)
	require.NoError(t, err)
	ticket, err := client.Ticket.Create().
		SetTicketNumber("TKT-ROUTE-" + roleCode).
		SetTitle("Notification route contract").
		SetDescription("Notification route contract").
		SetStatus("open").
		SetPriority("medium").
		SetRequesterID(user.ID).
		SetTenantID(tenant.ID).
		Save(ctx)
	require.NoError(t, err)
	notification, err := client.TicketNotification.Create().
		SetTicketID(ticket.ID).
		SetUserID(user.ID).
		SetType("assigned").
		SetChannel("in_app").
		SetContent("You have a ticket update").
		SetStatus("sent").
		SetTenantID(tenant.ID).
		Save(ctx)
	require.NoError(t, err)

	logger := zaptest.NewLogger(t).Sugar()
	ticketNotifications := controller.NewTicketNotificationController(service.NewTicketNotificationService(client, logger), logger)
	genericNotifications := controller.NewNotificationController(service.NewNotificationService(client))
	router := gin.New()
	SetupRoutes(router, &RouterConfig{
		JWTSecret:                    "ticket-notification-routes",
		Logger:                       logger,
		Client:                       client,
		CommonHandler:                &domainCommon.Handler{},
		TicketNotificationController: ticketNotifications,
		NotificationController:       genericNotifications,
	})
	token, err := middleware.GenerateAccessToken(user.ID, user.Username, role.Code, tenant.ID, "ticket-notification-routes", time.Hour)
	require.NoError(t, err)

	return &ticketNotificationRouteEnv{
		router:         router,
		client:         client,
		token:          token,
		notificationID: notification.ID,
	}
}

func executeTicketNotificationReadRoute(env *ticketNotificationRouteEnv, path string) *httptest.ResponseRecorder {
	request := httptest.NewRequest(http.MethodPut, path, nil)
	request.Header.Set("Authorization", "Bearer "+env.token)
	recorder := httptest.NewRecorder()
	env.router.ServeHTTP(recorder, request)
	return recorder
}

func TestTicketNotificationReadRoutesUseDedicatedTenantRBACNamespace(t *testing.T) {
	env := setupTicketNotificationRouteEnv(t, "notification_reader")
	routes := map[string]bool{}
	for _, route := range env.router.Routes() {
		routes[route.Method+" "+route.Path] = true
	}
	require.True(t, routes["PUT /api/v1/ticket-notifications/:id/read"])
	require.True(t, routes["PUT /api/v1/ticket-notifications/read-all"])
	require.True(t, routes["PUT /api/v1/notifications/:id/read"], "generic notification IDs retain their separate route")

	request := httptest.NewRequest(http.MethodPut, "/api/v1/ticket-notifications/1/read", nil)
	recorder := httptest.NewRecorder()
	env.router.ServeHTTP(recorder, request)
	require.Equal(t, http.StatusUnauthorized, recorder.Code)
}

func TestTicketNotificationReadRoutesAllowDefaultReaderRoles(t *testing.T) {
	paths := []struct {
		name string
		path func(int) string
	}{
		{name: "mark one", path: func(id int) string { return "/api/v1/ticket-notifications/" + strconv.Itoa(id) + "/read" }},
		{name: "mark all", path: func(int) string { return "/api/v1/ticket-notifications/read-all" }},
	}
	for _, roleCode := range []string{"end_user", "security"} {
		for _, route := range paths {
			t.Run(roleCode+"/"+route.name, func(t *testing.T) {
				env := setupTicketNotificationRouteEnv(t, roleCode, "notification:read")
				recorder := executeTicketNotificationReadRoute(env, route.path(env.notificationID))
				require.Equal(t, http.StatusOK, recorder.Code, "body=%s", recorder.Body.String())

				updated := env.client.TicketNotification.GetX(context.Background(), env.notificationID)
				require.False(t, updated.ReadAt.IsZero())
				require.Equal(t, "sent", updated.Status)
			})
		}
	}
}

func TestTicketNotificationReadRoutesForbidRoleWithoutReadPermission(t *testing.T) {
	paths := []func(int) string{
		func(id int) string { return "/api/v1/ticket-notifications/" + strconv.Itoa(id) + "/read" },
		func(int) string { return "/api/v1/ticket-notifications/read-all" },
	}
	for index, path := range paths {
		t.Run(strconv.Itoa(index), func(t *testing.T) {
			env := setupTicketNotificationRouteEnv(t, "notification_updater", "notification:update")
			recorder := executeTicketNotificationReadRoute(env, path(env.notificationID))
			require.Equal(t, http.StatusForbidden, recorder.Code, "body=%s", recorder.Body.String())

			stored := env.client.TicketNotification.GetX(context.Background(), env.notificationID)
			require.True(t, stored.ReadAt.IsZero())
		})
	}
}
