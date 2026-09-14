package marketplace

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/require"
	"itsm-backend/common/tenantctx"
	"itsm-backend/config"
	"itsm-backend/database"
	"itsm-backend/ent"
	"itsm-backend/ent/marketplaceitem"
	"itsm-backend/middleware"
	marketplaceservice "itsm-backend/service/marketplace"
)

func TestConnectorInstallationRequiresRuntimeDependency(t *testing.T) {
	ctrl := NewController(nil)
	ctx, _ := gin.CreateTestContext(httptest.NewRecorder())
	installation := &ent.TenantInstallation{TenantID: 1, Edges: ent.TenantInstallationEdges{Item: &ent.MarketplaceItem{Type: marketplaceitem.TypeConnector, Name: "webhook-connector"}}}
	require.Error(t, ctrl.provisionConnectorInstallation(ctx, installation), "missing runtime cannot be reported as successful activation")
}

func TestMarketplaceManagementDenialPrecedesDatabaseAndActivation(t *testing.T) {
	policy, err := database.NewExecutionPolicy(config.ExecutionConfig{Mode: "candidate", DeploymentID: "marketplace-test", Scopes: []config.ExecutionScopeConfig{{TenantID: 1, ScopeID: "149ff1af-a27c-47c7-827f-103271130bb9"}}})
	require.NoError(t, err)
	ctrl := NewController(marketplaceservice.NewService(nil, nil, policy))
	router := gin.New()
	router.Use(func(c *gin.Context) {
		c.Set(middleware.TenantContextKey, &middleware.TenantContext{TenantID: 1})
		c.Set("tenant_id", 1)
		c.Set("user_id", 1)
		c.Next()
	})
	router.POST("/items/:id/install", ctrl.InstallItem)
	router.POST("/items/:id/uninstall", ctrl.UninstallItem)
	router.PUT("/installations/:id/config", ctrl.UpdateInstallationConfig)
	for _, endpoint := range []struct{ method, path string }{{http.MethodPost, "/items/1/install"}, {http.MethodPost, "/items/1/uninstall"}, {http.MethodPut, "/installations/1/config"}} {
		t.Run(endpoint.path, func(t *testing.T) {
			req := httptest.NewRequest(endpoint.method, endpoint.path, strings.NewReader(`{"credentials":{"secret":"synthetic-private"}}`)).WithContext(tenantctx.WithTenantID(context.Background(), 1))
			req.Header.Set("Content-Type", "application/json")
			response := httptest.NewRecorder()
			router.ServeHTTP(response, req)
			require.Equal(t, http.StatusForbidden, response.Code)
			require.NotContains(t, response.Body.String(), "synthetic-private")
		})
	}
}
