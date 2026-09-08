//go:build integration_postgres

package integration

import (
	"encoding/json"
	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"go.uber.org/zap"
	"itsm-backend/authentication"
	"itsm-backend/authorization"
	"itsm-backend/common/tenantctx"
	"itsm-backend/controller"
	"itsm-backend/database"
	"itsm-backend/ent"
	"itsm-backend/ent/role"
	authcommon "itsm-backend/handlers/common"
	approuter "itsm-backend/router"
	"itsm-backend/service"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"
)

func TestPostgresBrowserSessionProjection(t *testing.T) {
	gin.SetMode(gin.TestMode)
	f := newIncidentEffectsFixture(t)
	authorization.InvalidateAllPermissionCaches()
	t.Cleanup(authorization.InvalidateAllPermissionCaches)
	f.client.Tenant.UpdateOneID(f.tenant.ID).SetType("msp_customer").ExecX(f.ctx)
	provider := f.client.Tenant.Create().SetCode("session-provider").SetName("Provider").SetType("msp_provider").SaveX(f.ctx)
	actor := f.client.User.Create().SetTenantID(provider.ID).SetUsername("session-operator").SetName("Operator").SetEmail("operator@example.test").SetPasswordHash("private-password-hash").SetRole("admin").SetMspRole("provider_agent").SaveX(f.ctx)
	allocation := f.client.MSPAllocation.Create().SetMspUserID(actor.ID).SetCustomerTenantID(f.tenant.ID).SetRole("primary").SaveX(f.ctx)
	f.client.Menu.Create().SetTenantID(f.tenant.ID).SetName("Customer home").SetPath("/dashboard").SaveX(f.ctx)
	for _, code := range []string{f.actor.Role, "msp_tech"} {
		f.client.Role.Create().SetTenantID(f.tenant.ID).SetCode(code).SetName(code).SaveX(f.ctx)
	}
	customer2 := f.client.Tenant.Create().SetCode("customer-two").SetName("Customer Two").SetType("msp_customer").SaveX(f.ctx)
	foreign := f.client.Tenant.Create().SetCode("foreign-secret").SetName("Foreign secret").SetType("msp_customer").SaveX(f.ctx)
	expired := f.client.Tenant.Create().SetCode("expired-customer").SetName("Expired").SetType("msp_customer").SetExpiresAt(time.Now().Add(-time.Hour)).SaveX(f.ctx)
	inactive := f.client.Tenant.Create().SetCode("inactive-customer").SetName("Inactive").SetType("msp_customer").SetStatus("suspended").SaveX(f.ctx)
	f.client.Role.Create().SetTenantID(customer2.ID).SetCode("msp_tech").SetName("Customer Two Role").SaveX(f.ctx)
	for _, target := range []*ent.Tenant{customer2, expired, inactive} {
		f.client.MSPAllocation.Create().SetMspUserID(actor.ID).SetCustomerTenantID(target.ID).SetRole("primary").SaveX(f.ctx)
	}
	targetRole := f.client.Role.Query().Where(role.CodeEQ("msp_tech"), role.TenantIDEQ(f.tenant.ID)).OnlyX(f.ctx)
	permission := f.client.Permission.Create().SetTenantID(f.tenant.ID).SetCode("ticket:read").SetName("Read tickets").SetResource("ticket").SetAction("read").SaveX(f.ctx)
	link := f.client.RolePermission.Create().SetTenantID(f.tenant.ID).SetRoleID(targetRole.ID).SetPermissionID(permission.ID).SaveX(f.ctx)
	f.client.Menu.Create().SetTenantID(f.tenant.ID).SetName("Customer tickets").SetPath("/tickets").SetPermissionCode("ticket:read").SaveX(f.ctx)
	f.client.Menu.Create().SetTenantID(f.tenant.ID).SetName("Unpermitted users").SetPath("/users").SetPermissionCode("user:read").SaveX(f.ctx)
	f.client.Menu.Create().SetTenantID(provider.ID).SetName("Provider secret menu").SetPath("/provider").SaveX(f.ctx)
	clients, cfg := runtimeClients(t, f)
	for _, table := range []string{"menus", "user_roles"} {
		_, err := f.db.ExecContext(f.ctx, "GRANT SELECT ON "+table+" TO "+cfg.User)
		require.NoError(t, err)
	}
	_, err := clients.Tenant.User.Get(tenantctx.WithTenantID(f.ctx, f.tenant.ID), actor.ID)
	require.True(t, ent.IsNotFound(err))
	logger := zap.NewNop().Sugar()
	const secret = "isolated-browser-session-key"
	auth := service.NewAuthService(clients.Tenant, clients.System, secret, logger)
	snapshots := &catalogReaderDirectory{directory: clients.IntakeDirectorySnapshot()}
	sessions := authorization.NewSessionReader(clients.Tenant, snapshots)
	owner := authcommon.NewService(authcommon.NewEntRepository(clients.Tenant), secret, logger, clients.System, nil, sessions)
	menuOwner := service.NewMenuService(clients.Tenant, logger, sessions)
	engine := gin.New()
	approuter.SetupRoutes(engine, &approuter.RouterConfig{JWTSecret: secret, Logger: logger, Client: clients.Tenant, TenantDirectoryClient: clients.System, CommonHandler: authcommon.NewHandler(owner), MenuController: controller.NewMenuController(menuOwner)})
	for _, subject := range []*ent.User{f.actor, actor} {
		session, err := auth.SwitchTenant(f.ctx, subject.ID, f.tenant.ID)
		require.NoError(t, err)
		for _, path := range []string{"/me", "/tenants", "/menus"} {
			t.Run(subject.Username+path, func(t *testing.T) {
				req := httptest.NewRequest(http.MethodGet, "/api/v1/auth"+path, nil)
				req.Header.Set("Authorization", "Bearer "+session.AccessToken)
				recorder := httptest.NewRecorder()
				engine.ServeHTTP(recorder, req)
				var response conversionHTTPEnvelope
				require.NoError(t, json.Unmarshal(recorder.Body.Bytes(), &response))
				assert.Equal(t, http.StatusOK, recorder.Code, recorder.Body.String())
				assert.Zero(t, response.Code)
				assert.NotContains(t, recorder.Body.String(), "private-password-hash")
				if path == "/me" {
					var user map[string]interface{}
					require.NoError(t, json.Unmarshal(response.Data, &user))
					assert.Equal(t, float64(subject.ID), user["id"])
					assert.Equal(t, float64(f.tenant.ID), user["tenantId"])
					assert.Equal(t, float64(subject.TenantID), user["actorTenantId"])
					assert.Equal(t, authorization.EffectiveSessionRole(subject), user["role"])
				}
				if path == "/tenants" {
					assert.Contains(t, string(response.Data), f.tenant.Code)
				}
				if path == "/menus" {
					assert.Contains(t, string(response.Data), "Customer home")
				}
				require.Zero(t, database.GetRawDB().Stats().InUse)
				require.Zero(t, clients.SystemDB.Stats().InUse)
			})
		}
	}
	session, err := auth.SwitchTenant(f.ctx, actor.ID, f.tenant.ID)
	require.NoError(t, err)
	read := func(token, path string) (int, conversionHTTPEnvelope) {
		t.Helper()
		req := httptest.NewRequest(http.MethodGet, "/api/v1/auth"+path, nil)
		req.Header.Set("Authorization", "Bearer "+token)
		recorder := httptest.NewRecorder()
		engine.ServeHTTP(recorder, req)
		var response conversionHTTPEnvelope
		require.NoError(t, json.Unmarshal(recorder.Body.Bytes(), &response))
		require.Zero(t, database.GetRawDB().Stats().InUse)
		require.Zero(t, clients.SystemDB.Stats().InUse)
		return recorder.Code, response
	}
	status, response := read(session.AccessToken, "/tenants")
	require.Equal(t, http.StatusOK, status)
	require.Contains(t, string(response.Data), customer2.Code)
	for _, hidden := range []*ent.Tenant{foreign, expired, inactive} {
		require.NotContains(t, string(response.Data), hidden.Code)
	}
	status, response = read(session.AccessToken, "/menus")
	require.Equal(t, http.StatusOK, status)
	require.Contains(t, string(response.Data), "Customer tickets")
	require.NotContains(t, string(response.Data), "Unpermitted users")
	require.NotContains(t, string(response.Data), "Provider secret menu")
	for _, test := range []struct {
		name            string
		revoke, restore func()
		status          int
	}{
		{"allocation", func() { f.client.MSPAllocation.UpdateOneID(allocation.ID).SetDeassignedAt(time.Now()).ExecX(f.ctx) }, func() { f.client.MSPAllocation.UpdateOneID(allocation.ID).ClearDeassignedAt().ExecX(f.ctx) }, 403},
		{"inactive_customer", func() { f.client.Tenant.UpdateOneID(f.tenant.ID).SetStatus("suspended").ExecX(f.ctx) }, func() { f.client.Tenant.UpdateOneID(f.tenant.ID).SetStatus("active").ExecX(f.ctx) }, 403},
		{"expired_customer", func() { f.client.Tenant.UpdateOneID(f.tenant.ID).SetExpiresAt(time.Now().Add(-time.Hour)).ExecX(f.ctx) }, func() { f.client.Tenant.UpdateOneID(f.tenant.ID).ClearExpiresAt().ExecX(f.ctx) }, 403},
		{"inactive_actor", func() { f.client.User.UpdateOneID(actor.ID).SetActive(false).ExecX(f.ctx) }, func() { f.client.User.UpdateOneID(actor.ID).SetActive(true).ExecX(f.ctx) }, 401},
		{"actor_role", func() { f.client.User.UpdateOneID(actor.ID).SetMspRole("provider_admin").ExecX(f.ctx) }, func() { f.client.User.UpdateOneID(actor.ID).SetMspRole("provider_agent").ExecX(f.ctx) }, 401},
		{"target_role", func() { f.client.Role.UpdateOneID(targetRole.ID).SetIsActive(false).ExecX(f.ctx) }, func() { f.client.Role.UpdateOneID(targetRole.ID).SetIsActive(true).ExecX(f.ctx) }, 403},
	} {
		t.Run(test.name, func(t *testing.T) {
			test.revoke()
			defer test.restore()
			for _, path := range []string{"/me", "/tenants", "/menus"} {
				status, response := read(session.AccessToken, path)
				require.Equal(t, test.status, status, response.Message)
				require.NotContains(t, string(response.Data), "actorTenantId")
			}
		})
	}
	t.Run("permission_revocation_does_not_block_hydration", func(t *testing.T) {
		f.client.RolePermission.DeleteOneID(link.ID).ExecX(f.ctx)
		for _, path := range []string{"/me", "/tenants", "/menus"} {
			status, response := read(session.AccessToken, path)
			require.Equal(t, 200, status, response.Message)
			require.NotContains(t, string(response.Data), "ticket:read")
			require.NotContains(t, string(response.Data), "Customer tickets")
		}
	})
	for _, targetID := range []int{foreign.ID, expired.ID, inactive.ID} {
		forged, err := authentication.IssueSessionTokens(authentication.SessionIdentity{UserID: actor.ID, Username: actor.Username, Role: "msp_tech", TenantID: targetID}, secret)
		require.NoError(t, err)
		for _, path := range []string{"/me", "/tenants", "/menus"} {
			status, response := read(forged.AccessToken, path)
			require.Equal(t, 403, status, response.Message)
			require.NotContains(t, string(response.Data), "foreign-secret")
		}
	}
	for _, stage := range []string{"export", "import", "lookup", "close"} {
		t.Run(stage, func(t *testing.T) {
			snapshots.failure = stage
			defer func() { snapshots.failure = "" }()
			for _, path := range []string{"/me", "/tenants", "/menus"} {
				status, response := read(session.AccessToken, path)
				require.Equal(t, 503, status, response.Message)
				require.Contains(t, string(response.Data), "InfrastructureUnavailable")
				require.NotContains(t, string(response.Data), "actorTenantId")
			}
		})
	}
	// A snapshot captures both current actor authorization and target permissions.
	snapshots.afterImport = func() { f.client.MSPAllocation.UpdateOneID(allocation.ID).SetDeassignedAt(time.Now()).ExecX(f.ctx) }
	status, response = read(session.AccessToken, "/me")
	require.Equal(t, 200, status, response.Message)
	status, _ = read(session.AccessToken, "/me")
	require.Equal(t, 403, status)
	f.client.MSPAllocation.UpdateOneID(allocation.ID).ClearDeassignedAt().ExecX(f.ctx)
	t.Run("switch_then_reload", func(t *testing.T) {
		switched, err := auth.SwitchTenant(f.ctx, actor.ID, customer2.ID)
		require.NoError(t, err)
		status, response := read(switched.AccessToken, "/me")
		require.Equal(t, 200, status, response.Message)
		var projection struct{ ID, TenantID, ActorTenantID int }
		// Match the public camelCase projection explicitly.
		var fields map[string]interface{}
		require.NoError(t, json.Unmarshal(response.Data, &fields))
		projection.ID = int(fields["id"].(float64))
		projection.TenantID = int(fields["tenantId"].(float64))
		projection.ActorTenantID = int(fields["actorTenantId"].(float64))
		require.Equal(t, actor.ID, projection.ID)
		require.Equal(t, customer2.ID, projection.TenantID)
		require.Equal(t, provider.ID, projection.ActorTenantID)
		status, response = read(switched.AccessToken, "/tenants")
		require.Equal(t, 200, status)
		require.Contains(t, string(response.Data), customer2.Code)
	})
	t.Run("missing_actor", func(t *testing.T) {
		token, err := authentication.IssueSessionTokens(authentication.SessionIdentity{UserID: 999999, Username: "missing", Role: "msp_tech", TenantID: f.tenant.ID}, secret)
		require.NoError(t, err)
		for _, path := range []string{"/me", "/tenants", "/menus"} {
			status, _ := read(token.AccessToken, path)
			require.Equal(t, 401, status)
		}
	})
	t.Run("required_directory_missing", func(t *testing.T) {
		missing := authorization.NewSessionReader(clients.Tenant, nil)
		original := engine
		engine = gin.New()
		approuter.SetupRoutes(engine, &approuter.RouterConfig{JWTSecret: secret, Logger: logger, Client: clients.Tenant, TenantDirectoryClient: clients.System, CommonHandler: authcommon.NewHandler(authcommon.NewService(authcommon.NewEntRepository(clients.Tenant), secret, logger, clients.System, nil, missing)), MenuController: controller.NewMenuController(service.NewMenuService(clients.Tenant, logger, missing))})
		defer func() { engine = original }()
		for _, path := range []string{"/me", "/tenants", "/menus"} {
			status, response := read(session.AccessToken, path)
			require.Equal(t, 503, status)
			require.NotContains(t, string(response.Data), "actorTenantId")
		}
	})
	// No System business-table grants were introduced.
	_, err = clients.System.Menu.Query().All(f.ctx)
	require.ErrorContains(t, err, "permission denied")
	t.Log("signed registered HTTP proves native actor/selected tenant projection, live revocations, bounded authorized tenant candidates, target-only menus, snapshot consistency and directory failure cleanup")

}
