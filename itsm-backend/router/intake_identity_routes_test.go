package router

import (
	"bytes"
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"itsm-backend/ent/enttest"
	"itsm-backend/handlers/intake"
	"itsm-backend/middleware"

	"github.com/gin-gonic/gin"
	_ "github.com/mattn/go-sqlite3"
	"github.com/stretchr/testify/require"
	"go.uber.org/zap/zaptest"
)

type routeIntakeService struct{}

type denyExchangeLimiter struct{ calls int }

func (l *denyExchangeLimiter) Allow(context.Context, string) (bool, error) {
	l.calls++
	return false, nil
}

func (routeIntakeService) Create(context.Context, intake.Identity, intake.CreateWorkItemCommand) (*intake.CreateWorkItemResult, error) {
	return &intake.CreateWorkItemResult{
		WorkItemID: 41, Number: "TKT-ROUTE-41", RecordClass: intake.RecordClassIncident,
		ProfessionalReference: intake.ProfessionalReference{Type: "incident", ID: 42},
		WorkflowStartStatus:   "pending",
	}, nil
}

func TestIntakeTokenRouteIsolationAndIdentityAdminPermission(t *testing.T) {
	gin.SetMode(gin.TestMode)
	client := enttest.Open(t, "sqlite3", fmt.Sprintf("file:intake-identity-routes-%d?mode=memory&cache=shared&_fk=1", time.Now().UnixNano()))
	defer client.Close()
	ctx := context.Background()
	tenant, err := client.Tenant.Create().SetName("Route Tenant").SetCode(fmt.Sprintf("ROUTE-%d", time.Now().UnixNano())).SetDomain(fmt.Sprintf("route-%d.test", time.Now().UnixNano())).SetStatus("active").Save(ctx)
	require.NoError(t, err)
	admin, err := client.User.Create().SetUsername("route-super-admin").SetEmail("route-super-admin@test").SetName("Route Admin").SetPasswordHash("x").SetRole("super_admin").SetActive(true).SetTenantID(tenant.ID).Save(ctx)
	require.NoError(t, err)
	ordinary, err := client.User.Create().SetUsername("route-end-user").SetEmail("route-end-user@test").SetName("Route User").SetPasswordHash("x").SetRole("end_user").SetActive(true).SetTenantID(tenant.ID).Save(ctx)
	require.NoError(t, err)

	const jwtSecret = "route-intake-jwt-secret"
	intakeHandler := intake.NewHandler(routeIntakeService{})
	mappingHandler := intake.NewIdentityMappingHandler(client)
	exchangeHandler := intake.NewIdentityExchangeHandler(client, nil, "exchange-secret", jwtSecret, time.Minute, 5*time.Minute)
	router := gin.New()
	SetupRoutes(router, &RouterConfig{
		JWTSecret: jwtSecret, Logger: zaptest.NewLogger(t).Sugar(), Client: client,
		IntakeHandler: intakeHandler, IdentityMappingHandler: mappingHandler, IdentityExchangeHandler: exchangeHandler,
	})

	intakeToken, _, err := middleware.GenerateIntakeToken(middleware.IntakeTokenIdentity{
		UserID: admin.ID, Username: admin.Username, Role: admin.Role, TenantID: tenant.ID,
		Channel: "teams", Provider: "microsoft",
	}, jwtSecret, 5*time.Minute)
	require.NoError(t, err)
	command := []byte(`{"idempotencyKey":"teams:it-support:message-42","intakeKind":"incident","title":"VPN unavailable","incident":{"severity":"high"}}`)

	t.Run("intake token creates only through intake group", func(t *testing.T) {
		request := httptest.NewRequest(http.MethodPost, "/api/v1/intake/work-items", bytes.NewReader(command))
		request.Header.Set("Authorization", "Bearer "+intakeToken)
		response := httptest.NewRecorder()
		router.ServeHTTP(response, request)
		require.Equal(t, http.StatusCreated, response.Code, response.Body.String())

		request = httptest.NewRequest(http.MethodGet, "/api/v1/intake/external-identities", nil)
		request.Header.Set("Authorization", "Bearer "+intakeToken)
		response = httptest.NewRecorder()
		router.ServeHTTP(response, request)
		require.Equal(t, http.StatusUnauthorized, response.Code, response.Body.String())
	})

	t.Run("ordinary access token still reaches intake but lacks identity admin permission", func(t *testing.T) {
		accessToken, err := middleware.GenerateAccessToken(ordinary.ID, ordinary.Username, ordinary.Role, tenant.ID, jwtSecret, time.Hour)
		require.NoError(t, err)
		request := httptest.NewRequest(http.MethodGet, "/api/v1/intake/external-identities", nil)
		request.Header.Set("Authorization", "Bearer "+accessToken)
		response := httptest.NewRecorder()
		router.ServeHTTP(response, request)
		require.Equal(t, http.StatusForbidden, response.Code, response.Body.String())
	})

	t.Run("identity exchange route is outside user jwt auth", func(t *testing.T) {
		request := httptest.NewRequest(http.MethodPost, "/api/v1/intake/identity-exchange", bytes.NewBufferString(`{}`))
		response := httptest.NewRecorder()
		router.ServeHTTP(response, request)
		require.Equal(t, http.StatusBadRequest, response.Code, response.Body.String())
		require.NotContains(t, response.Body.String(), "认证token")
	})

	t.Run("rate limiter runs before assertion verification", func(t *testing.T) {
		limiter := &denyExchangeLimiter{}
		limitedRouter := gin.New()
		SetupRoutes(limitedRouter, &RouterConfig{
			JWTSecret: jwtSecret, Logger: zaptest.NewLogger(t).Sugar(), Client: client,
			RedisRateLimiter: limiter, IdentityExchangeHandler: exchangeHandler,
		})
		request := httptest.NewRequest(http.MethodPost, "/api/v1/intake/identity-exchange", bytes.NewBufferString(`{"signature":"invalid"}`))
		response := httptest.NewRecorder()
		limitedRouter.ServeHTTP(response, request)
		require.Equal(t, http.StatusTooManyRequests, response.Code, response.Body.String())
		require.Equal(t, 1, limiter.calls)
	})
}
