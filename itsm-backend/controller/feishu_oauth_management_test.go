package controller

import (
	"context"
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"testing"

	"github.com/gin-gonic/gin"
	_ "github.com/mattn/go-sqlite3"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"go.uber.org/zap"
	"itsm-backend/common/tenantctx"
	"itsm-backend/config"
	"itsm-backend/connector"
	"itsm-backend/database"
	"itsm-backend/ent/enttest"
	"itsm-backend/ent/marketplaceitem"
	market "itsm-backend/service/marketplace"
)

func TestFeishuOAuthManagementDenialPrecedesExchange(t *testing.T) {
	for _, mode := range []string{"candidate", "missing-policy", "missing-service", "canceled"} {
		t.Run(mode, func(t *testing.T) {
			var calls atomic.Int32
			receiver := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				calls.Add(1)
				w.WriteHeader(http.StatusServiceUnavailable)
				_, _ = w.Write([]byte(`{"code":1,"msg":"local denied probe"}`))
			}))
			defer receiver.Close()
			manager := connector.NewManager(nil, zap.NewNop().Sugar(), standardConnectorManagementPolicy(t))
			defer manager.CloseAll()
			require.NoError(t, manager.Provision(tenantctx.WithTenantID(context.Background(), 17), connector.Config{Name: "feishu", TenantID: 17, Enabled: true, Credentials: map[string]string{"app_id": "local-app", "app_secret": "synthetic-secret"}, Settings: map[string]interface{}{"base_url": receiver.URL, "callbackInstanceId": "c83503e86cc5468aaab482cd204f30fa"}}))
			var service *market.Service
			if mode == "candidate" {
				policy, err := database.NewExecutionPolicy(config.ExecutionConfig{Mode: "candidate", DeploymentID: "oauth-test", Scopes: []config.ExecutionScopeConfig{{TenantID: 17, ScopeID: "149ff1af-a27c-47c7-827f-103271130bb9"}}})
				require.NoError(t, err)
				service = market.NewService(nil, nil, policy)
			} else if mode == "canceled" {
				policy, err := database.NewExecutionPolicy(config.ExecutionConfig{Mode: "standard", DeploymentID: "oauth-canceled-test"})
				require.NoError(t, err)
				service = market.NewService(nil, nil, policy)
			} else if mode == "missing-policy" {
				service = market.NewService(nil, nil, nil)
			}
			ctrl := NewFeishuController(manager, nil, service, zap.NewNop().Sugar())
			router := gin.New()
			router.GET("/callback/:instance_id", ctrl.OAuthCallback)
			response := httptest.NewRecorder()
			request := httptest.NewRequest(http.MethodGet, "/callback/c83503e86cc5468aaab482cd204f30fa?code=local-probe", nil)
			wantStatus := http.StatusForbidden
			if mode == "canceled" {
				canceled, cancel := context.WithCancel(request.Context())
				cancel()
				request = request.WithContext(canceled)
				wantStatus = http.StatusInternalServerError
			}
			router.ServeHTTP(response, request)
			assert.Equal(t, wantStatus, response.Code)
			assert.Zero(t, calls.Load(), "denied configuration callback must not exchange a token")
			assert.NotContains(t, response.Body.String(), "synthetic-secret")
		})
	}
}

func TestFeishuOAuthAmbiguousInstanceFailsClosed(t *testing.T) {
	manager := connector.NewManager(nil, zap.NewNop().Sugar(), standardConnectorManagementPolicy(t))
	defer manager.CloseAll()
	for _, tenantID := range []int{17, 18} {
		require.NoError(t, manager.Provision(tenantctx.WithTenantID(context.Background(), tenantID), connector.Config{Name: "feishu", TenantID: tenantID, Enabled: true, Credentials: map[string]string{"app_id": "local-app", "app_secret": "synthetic-secret"}, Settings: map[string]interface{}{"callbackInstanceId": "duplicate-instance"}}))
	}
	found, tenantID, ok := manager.GetByCallbackInstanceID("feishu", "duplicate-instance")
	assert.False(t, ok)
	assert.Nil(t, found)
	assert.Zero(t, tenantID)
}

// This exercises the production callback and persistence with a local provider
// and SQLite; it does not verify OAuth state/actor authorization or PG admission.
func TestFeishuOAuthStandardCallbackPersistsResolvedTenant(t *testing.T) {
	var calls atomic.Int32
	receiver := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls.Add(1)
		w.Header().Set("Content-Type", "application/json")
		if r.URL.Path == "/open-apis/auth/v3/tenant_access_token/internal" {
			_, _ = w.Write([]byte(`{"code":0,"tenant_access_token":"local-token","expire":7200}`))
		} else if r.URL.Path == "/open-apis/authen/v1/access_token" {
			_, _ = w.Write([]byte(`{"code":0,"data":{"access_token":"local-oauth","refresh_token":"local-refresh","expires_in":3600}}`))
		} else {
			w.WriteHeader(http.StatusNotFound)
		}
	}))
	defer receiver.Close()
	policy, err := database.NewExecutionPolicy(config.ExecutionConfig{Mode: "standard", DeploymentID: "oauth-standard-test"})
	require.NoError(t, err)
	client := enttest.Open(t, "sqlite3", "file:oauth_standard?mode=memory&cache=shared&_fk=1")
	defer client.Close()
	ctx := tenantctx.WithTenantID(context.Background(), 17)
	item := client.MarketplaceItem.Create().SetName("feishu-connector").SetType(marketplaceitem.TypeConnector).SetTitle("Local Feishu").SetProvider("local").SetLatestVersion("1").SetStatus(marketplaceitem.StatusPublished).SaveX(ctx)
	service := market.NewService(client, zap.NewNop().Sugar(), policy)
	installed, err := service.InstallItem(ctx, 17, item.ID, "local-user")
	require.NoError(t, err)
	_, err = service.UpdateInstallationConfig(ctx, 17, item.ID, map[string]interface{}{"preserved": "value"})
	require.NoError(t, err)
	manager := connector.NewManager(nil, zap.NewNop().Sugar(), policy)
	defer manager.CloseAll()
	require.NoError(t, manager.Provision(ctx, connector.Config{Name: "feishu", TenantID: 17, Enabled: true, Credentials: map[string]string{"app_id": "local-app", "app_secret": "synthetic-secret"}, Settings: map[string]interface{}{"base_url": receiver.URL, "callbackInstanceId": "c83503e86cc5468aaab482cd204f30fa"}}))
	router := gin.New()
	router.GET("/callback/:instance_id", NewFeishuController(manager, nil, service, zap.NewNop().Sugar()).OAuthCallback)
	response := httptest.NewRecorder()
	// Foreign query data must never determine persistence tenant identity.
	router.ServeHTTP(response, httptest.NewRequest(http.MethodGet, "/callback/c83503e86cc5468aaab482cd204f30fa?code=local-code&tenant_id=18&state=tenant_id%3D18", nil))
	require.Equal(t, http.StatusOK, response.Code, response.Body.String())
	require.EqualValues(t, 2, calls.Load())
	updated := client.TenantInstallation.GetX(ctx, installed.ID)
	require.Equal(t, 17, updated.TenantID)
	require.Equal(t, "value", updated.Config["preserved"])
	require.Equal(t, "local-oauth", updated.Config["oauth"].(map[string]interface{})["access_token"])
	require.NotContains(t, response.Body.String(), "local-oauth")
	require.NotContains(t, response.Body.String(), "local-refresh")
}
