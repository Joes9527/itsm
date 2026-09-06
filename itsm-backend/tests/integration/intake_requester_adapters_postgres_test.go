//go:build integration_postgres

package integration

import (
	"fmt"
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"testing"

	"itsm-backend/authorization"
	changedomain "itsm-backend/handlers/change"
	creation "itsm-backend/handlers/common/workitemcreation"
	"itsm-backend/handlers/intake"
	problemdomain "itsm-backend/handlers/problem"
	catalogdomain "itsm-backend/handlers/service_catalog"
	standarddomain "itsm-backend/handlers/standard_change"
	"itsm-backend/middleware"
	"itsm-backend/migration"
	"itsm-backend/repository/workitemnumber"
	"itsm-backend/service"

	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/require"
	"go.uber.org/zap"
)

// Reuses the reviewed conversion fixture, signed tenant switching and restricted
// Runtime/System snapshot. Standard Change remains unreachable to msp_tech.
func TestPostgresRequesterAdaptersSignedMSPHTTP(t *testing.T) {
	gin.SetMode(gin.TestMode)
	f := newIncidentEffectsFixture(t)
	authorization.InvalidateAllPermissionCaches()
	t.Cleanup(authorization.InvalidateAllPermissionCaches)
	logger := zap.NewNop().Sugar()
	migrator := migration.NewMigrator(f.db, logger)
	require.NoError(t, migrator.EnsureMigrationsTable(f.ctx))
	applied, err := migrator.RunMigrations(f.ctx, migration.PostSchemaMigrations())
	require.NoError(t, err)
	require.Equal(t, len(migration.PostSchemaMigrations()), applied)
	require.NoError(t, f.client.Schema.Create(f.ctx))
	f.client.Tenant.UpdateOneID(f.tenant.ID).SetType("msp_customer").SetStatus("active").ExecX(f.ctx)
	provider := f.client.Tenant.Create().SetCode("adapter-provider").SetName("Adapter Provider").SetType("msp_provider").SetStatus("active").SaveX(f.ctx)
	actor := f.client.User.Create().SetTenantID(provider.ID).SetUsername("adapter-operator").SetName("Adapter Operator").SetEmail("adapter@example.test").SetPasswordHash("test").SetRole("admin").SetMspRole("provider_agent").SetActive(true).SaveX(f.ctx)
	f.client.MSPAllocation.Create().SetMspUserID(actor.ID).SetCustomerTenantID(f.tenant.ID).SetRole("primary").SaveX(f.ctx)
	second := f.client.User.Create().SetTenantID(f.tenant.ID).SetUsername("second").SetName("Second requester").SetEmail("second@example.test").SetPasswordHash("test").SetRole("requester").SetActive(true).SaveX(f.ctx)
	role := f.client.Role.Create().SetTenantID(f.tenant.ID).SetCode("msp_tech").SetName("Customer operator").SetIsActive(true).SaveX(f.ctx)
	for _, resource := range []string{"incident", "problem", "change"} {
		for _, action := range []string{"read", "write", "create_on_behalf"} {
			permission := f.client.Permission.Create().SetTenantID(f.tenant.ID).SetCode(resource + ":" + action).SetName(resource + ":" + action).SetResource(resource).SetAction(action).SaveX(f.ctx)
			f.client.RolePermission.Create().SetTenantID(f.tenant.ID).SetRoleID(role.ID).SetPermissionID(permission.ID).SaveX(f.ctx)
		}
		f.client.ProcessBinding.Create().SetTenantID(f.tenant.ID).SetBusinessType(resource).SetIsDefault(true).SetIsActive(true).SetProcessDefinitionKey("none").SetConditions(map[string]any{"no_process": true}).SaveX(f.ctx)
	}
	clients, cfg := runtimeClients(t, f)
	for _, table := range []string{"problems", "changes", "standard_changes", "work_item_relations", "work_item_number_sequences", "intake_resolution_snapshots", "process_bindings", "sla_definitions", "field_definitions", "field_values", "service_catalogs", "configuration_items", "groups"} {
		_, err = f.db.ExecContext(f.ctx, "GRANT SELECT,INSERT,UPDATE,DELETE ON "+table+" TO "+cfg.User)
		require.NoError(t, err)
		var sequence *string
		require.NoError(t, f.db.QueryRowContext(f.ctx, "SELECT pg_get_serial_sequence($1,'id')", table).Scan(&sequence))
		if sequence != nil {
			_, err = f.db.ExecContext(f.ctx, "GRANT USAGE ON SEQUENCE "+*sequence+" TO "+cfg.User)
			require.NoError(t, err)
		}
	}
	registry := intake.NewCreatorRegistry()
	for _, owner := range []creation.ProfessionalCreator{service.NewIncidentService(clients.Tenant, logger), problemdomain.NewService(problemdomain.NewEntRepository(clients.Tenant), logger), changedomain.NewService(nil, clients.Tenant, logger)} {
		require.NoError(t, registry.Register(owner))
	}
	resolver := intake.NewResolver(catalogdomain.NewService(nil, clients.Tenant, logger, nil), service.NewProcessBindingService(clients.Tenant), service.NewConfigurationItemService(clients.Tenant, logger, nil, nil), service.NewTicketCategoryService(clients.Tenant))
	app := intake.NewService(clients.Tenant, resolver, registry, intake.NewWorkItemCreator(workitemnumber.NewPostgreSQLAllocator()), clients.IntakeDirectorySnapshot())
	adapters := requesterAdapters(t, f.client, app, f.tenant.ID, f.actor.ID)
	const secret = "isolated-requester-adapter-signing-key"
	auth := service.NewAuthService(clients.Tenant, clients.System, secret, logger)
	session, err := auth.SwitchTenant(f.ctx, actor.ID, f.tenant.ID)
	require.NoError(t, err)
	require.Equal(t, "msp_tech", session.User.Role)
	router := gin.New()
	router.Use(middleware.AuthMiddleware(secret), middleware.RBACMiddleware(clients.Tenant, clients.System))
	api := router.Group("/api/v1")
	api.Use(middleware.TenantMiddleware(clients.System))
	paths := map[string]string{"incident": "/incidents", "problem": "/problems", "change": "/changes"}
	for _, a := range adapters {
		if a.name != "standard_change" {
			api.POST(paths[a.name], middleware.RequirePermission(a.resource, "write"), a.handle)
		}
	}
	standard := standarddomain.NewHandler(clients.Tenant, logger)
	standard.SetCreationApplication(app)
	standard.RegisterRoutes(api)
	request := func(path, key, body string) *httptest.ResponseRecorder {
		req := httptest.NewRequest(http.MethodPost, "/api/v1"+path, strings.NewReader(body))
		req.Header.Set("Authorization", "Bearer "+session.AccessToken)
		req.Header.Set("Content-Type", "application/json")
		req.Header.Set("Idempotency-Key", key)
		w := httptest.NewRecorder()
		router.ServeHTTP(w, req)
		return w
	}
	for _, adapter := range adapters {
		t.Run(adapter.name, func(t *testing.T) {
			body := withRequester(adapter.body, strconv.Itoa(f.actor.ID))
			key := "signed-" + adapter.name
			path := paths[adapter.name]
			if adapter.name == "standard_change" {
				stable := requesterGraph(t, f.client)
				path = fmt.Sprintf("/standard-changes/%s/instantiate", adapter.params.ByName("id"))
				w := request(path, key, body)
				require.Equal(t, 403, w.Code, w.Body.String())
				require.Equal(t, stable, requesterGraph(t, f.client))
				return
			}
			w := request(path, key, body)
			require.Equal(t, 201, w.Code, w.Body.String())
			first := assertRequesterReceipt(t, w, false)
			require.Equal(t, adapter.class, first.RecordClass)
			assertRequesterProvenance(t, f.client, first, f.tenant.ID, provider.ID, actor.ID, f.actor.ID)
			stable := requesterGraph(t, f.client)
			w = request(path, key, body)
			require.Equal(t, 200, w.Code, w.Body.String())
			replay := assertRequesterReceipt(t, w, true)
			require.Equal(t, first.WorkItemID, replay.WorkItemID)
			require.Equal(t, first.Number, replay.Number)
			require.Equal(t, stable, requesterGraph(t, f.client))
			w = request(path, key, withRequester(adapter.body, strconv.Itoa(second.ID)))
			require.Equal(t, 409, w.Code, w.Body.String())
			require.Equal(t, stable, requesterGraph(t, f.client))
			// An omitted requester cannot silently use the template/source customer or native MSP actor.
			w = request(path, key+"-omitted", adapter.body)
			require.Equal(t, 400, w.Code, w.Body.String())
			require.Contains(t, w.Body.String(), "requesterId")
			require.Equal(t, stable, requesterGraph(t, f.client))
			f.actor.Update().SetActive(false).ExecX(f.ctx)
			w = request(path, key, body)
			require.Equal(t, 403, w.Code, w.Body.String())
			require.Equal(t, stable, requesterGraph(t, f.client))
			f.actor.Update().SetActive(true).ExecX(f.ctx)
		})
	}
}
