package connector_test

import (
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"

	"github.com/stretchr/testify/require"
	"itsm-backend/common/executionscope"
	"itsm-backend/common/tenantctx"
	"itsm-backend/config"
	"itsm-backend/connector"
	"itsm-backend/connector/builtin/feishu"
	"itsm-backend/database"
)

func TestFeishuDeclaredActivationBindsActualTaskClient(t *testing.T) {
	for _, scenario := range []string{"enabled", "disabled", "missing secret", "wrong digest", "caller mutation"} {
		t.Run(scenario, func(t *testing.T) {
			var requests atomic.Int32
			var tasks atomic.Int32
			receiver := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				requests.Add(1)
				w.Header().Set("Content-Type", "application/json")
				if r.URL.Path == "/open-apis/auth/v3/tenant_access_token/internal" {
					fmt.Fprint(w, `{"code":0,"tenant_access_token":"local-token","expire":3600}`)
					return
				}
				if r.URL.Path != "/open-apis/task/v2/tasks" && r.URL.Path != "/open-apis/task/v2/tasks/local-task" {
					http.Error(w, "unexpected route", http.StatusBadRequest)
					return
				}
				tasks.Add(1)
				fmt.Fprint(w, `{"code":0,"data":{"task":{"guid":"local-task"}}}`)
			}))
			defer receiver.Close()
			cfg := connector.Config{Name: "feishu", Provider: "feishu", Credentials: map[string]string{"app_id": "local-app", "app_secret": "local-only"}, Settings: map[string]interface{}{"base_url": receiver.URL, "callbackInstanceId": "original-callback"}}
			digest, err := feishu.New().DescribeDeliveryDestination(cfg)
			require.NoError(t, err)
			if scenario == "missing secret" {
				delete(cfg.Credentials, "app_secret")
			}
			scope := "149ff1af-a27c-47c7-827f-103271130bb9"
			target := config.ConnectorTargetConfig{TenantID: 1, ScopeID: scope, Name: cfg.Name, Provider: cfg.Provider, DestinationDigest: digest, Credentials: cfg.Credentials, Settings: cfg.Settings, Capabilities: []string{"outbox"}}
			if scenario == "wrong digest" {
				target.DestinationDigest = strings.Repeat("f", 64)
			}
			mode := "scoped"
			if scenario == "disabled" {
				mode = "disabled"
			}
			policy, err := database.NewExecutionPolicy(config.ExecutionConfig{Mode: "candidate", DeploymentID: "feishu-local-test", Scopes: []config.ExecutionScopeConfig{{TenantID: 1, ScopeID: scope}}, Capabilities: map[string]string{"outbox": mode}, ConnectorTargets: []config.ConnectorTargetConfig{target}})
			require.NoError(t, err)
			if scenario == "caller mutation" {
				cfg.Credentials["app_id"] = "changed-app"
				cfg.Settings["callbackInstanceId"] = "changed-callback"
				cfg.Settings["base_url"] = "https://unreachable.example.invalid"
			}
			registry := connector.NewRegistry()
			registry.Register(func() connector.Connector { return feishu.New() })
			manager := connector.NewManager(registry, nil, policy)
			defer manager.CloseAll()
			tenantCtx := tenantctx.WithTenantID(context.Background(), 1)
			ref, err := policy.EventRef(1)
			require.NoError(t, err)
			if scenario != "wrong digest" {
				described, err := manager.DescribeDeclaredDeliveryTarget(tenantCtx, ref, "outbox", "feishu", "feishu")
				require.NoError(t, err)
				require.Equal(t, digest, described)
			}
			err = manager.ActivateStartupTargets(tenantctx.SystemContext(context.Background(), "test:feishu-startup", "private local activation"))
			require.Zero(t, requests.Load(), "activation and description must not contact any provider")
			if scenario == "missing secret" || scenario == "wrong digest" {
				require.Error(t, err)
				require.Empty(t, manager.ListByTenant(1))
				return
			}
			require.NoError(t, err)
			bound, generation, actual, err := manager.ResolveDeliveryTarget(tenantCtx, ref, "outbox", "feishu", "feishu")
			if scenario == "disabled" {
				require.ErrorIs(t, err, executionscope.ErrDenied)
				require.Empty(t, manager.ListByTenant(1))
				return
			}
			require.NoError(t, err)
			require.NotZero(t, generation)
			require.Equal(t, digest, actual)
			sender, ok := bound.(*feishu.Feishu)
			require.True(t, ok)
			require.Equal(t, "original-callback", sender.CallbackInstanceID())
			created, err := sender.CreateTask(tenantCtx, &feishu.FeishuTask{Name: "private task"})
			require.NoError(t, err)
			require.NotNil(t, created)
			require.Equal(t, "local-task", created.GUID)
			updated, err := sender.UpdateTask(tenantCtx, created.GUID, &feishu.FeishuTask{Name: "updated private task"})
			require.NoError(t, err)
			require.Equal(t, created.GUID, updated.GUID)
			require.Equal(t, int32(2), tasks.Load())
			_, _, _, err = manager.ResolveDeliveryTarget(tenantCtx, ref, "notification", "feishu", "feishu")
			require.ErrorIs(t, err, executionscope.ErrDenied)
			require.Equal(t, int32(3), requests.Load())
		})
	}
}
