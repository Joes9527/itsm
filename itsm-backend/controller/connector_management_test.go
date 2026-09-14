package controller

import (
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/require"
	"itsm-backend/common/tenantctx"
	"itsm-backend/config"
	"itsm-backend/connector"
	"itsm-backend/database"
)

func TestConnectorManagementRejectsBeforeDependencies(t *testing.T) {
	for _, scenario := range []string{"nil-manager", "nil-policy", "candidate", "missing-tenant", "foreign-tenant", "system", "canceled"} {
		t.Run(scenario, func(t *testing.T) {
			policy := standardConnectorManagementPolicy(t)
			if scenario == "candidate" {
				var err error
				policy, err = database.NewExecutionPolicy(config.ExecutionConfig{Mode: "candidate", DeploymentID: "connector-http-test", Scopes: []config.ExecutionScopeConfig{{TenantID: 1, ScopeID: "149ff1af-a27c-47c7-827f-103271130bb9"}}})
				require.NoError(t, err)
			}
			manager := connector.NewManager(nil, nil, policy)
			if scenario == "nil-policy" {
				manager = connector.NewManager(nil, nil, nil)
			}
			if scenario == "nil-manager" {
				manager = nil
			}
			ctrl := &ConnectorController{manager: manager}
			r := gin.New()
			r.Use(func(c *gin.Context) { c.Set("tenant_id", 1); c.Next() })
			r.POST("/configs", ctrl.Provision)
			r.DELETE("/configs/:name", ctrl.Revoke)
			for _, method := range []string{http.MethodPost, http.MethodDelete} {
				ctx := tenantctx.WithTenantID(context.Background(), 1)
				switch scenario {
				case "missing-tenant":
					ctx = context.Background()
				case "foreign-tenant":
					ctx = tenantctx.WithTenantID(context.Background(), 2)
				case "system":
					ctx = tenantctx.WithSystemBypass(ctx)
				case "canceled":
					canceled, cancel := context.WithCancel(ctx)
					cancel()
					ctx = canceled
				}
				route := "/configs"
				if method == http.MethodDelete {
					route += "/webhook"
				}
				request := httptest.NewRequest(method, route, strings.NewReader(`{"name":"webhook","enabled":false,"credentials":{"secret":"synthetic-secret"}}`)).WithContext(ctx)
				request.Header.Set("Content-Type", "application/json")
				response := httptest.NewRecorder()
				r.ServeHTTP(response, request)
				expected := http.StatusForbidden
				if scenario == "canceled" {
					expected = http.StatusInternalServerError
				}
				require.Equal(t, expected, response.Code)
				require.NotContains(t, response.Body.String(), "synthetic-secret")
			}
		})
	}
}

type managementCloseProbe struct {
	cfg    connector.Config
	closed map[string]int
}

func (*managementCloseProbe) Manifest() connector.Manifest {
	return connector.Manifest{Name: "management-probe", Version: "1", Title: "Local close probe", Type: connector.TypeCustom, RequiredPermissions: []string{"connector:write"}}
}

func (p *managementCloseProbe) Init(_ context.Context, cfg connector.Config) error {
	p.cfg = cfg
	return nil
}
func (*managementCloseProbe) Send(context.Context, *connector.Message) error { return nil }
func (*managementCloseProbe) HealthCheck(context.Context) connector.HealthStatus {
	return connector.HealthStatus{}
}

func (p *managementCloseProbe) Close() error {
	p.closed[fmt.Sprintf("%d/%s", p.cfg.TenantID, p.cfg.Provider)]++
	return nil
}

func TestConnectorManagementRevokeClosesAllNamedProvidersOnlyInTenant(t *testing.T) {
	closed := map[string]int{}
	registry := connector.NewRegistry()
	registry.Register(func() connector.Connector { return &managementCloseProbe{closed: closed} })
	manager := connector.NewManager(registry, nil, standardConnectorManagementPolicy(t))
	defer manager.CloseAll()
	for _, cfg := range []connector.Config{
		{Name: "management-probe", TenantID: 1, Provider: "first", Enabled: true},
		{Name: "management-probe", TenantID: 1, Provider: "second", Enabled: true},
		{Name: "management-probe", TenantID: 2, Provider: "first", Enabled: true},
	} {
		require.NoError(t, manager.Provision(tenantctx.WithTenantID(context.Background(), cfg.TenantID), cfg))
	}
	ctrl := &ConnectorController{manager: manager}
	router := gin.New()
	router.Use(func(c *gin.Context) { c.Set("tenant_id", 1); c.Next() })
	router.DELETE("/configs/:name", ctrl.Revoke)
	response := httptest.NewRecorder()
	router.ServeHTTP(response, httptest.NewRequest(http.MethodDelete, "/configs/management-probe", nil).WithContext(tenantctx.WithTenantID(context.Background(), 1)))
	require.Equal(t, http.StatusOK, response.Code)
	require.Empty(t, manager.ListByTenant(1))
	require.Len(t, manager.ListByTenant(2), 1)
	require.Equal(t, 1, closed["1/first"])
	require.Equal(t, 1, closed["1/second"])
	require.Zero(t, closed["2/first"])
}
