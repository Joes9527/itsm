package connector_test

import (
	"context"
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"testing"

	"github.com/stretchr/testify/require"
	"itsm-backend/common/executionscope"
	"itsm-backend/common/tenantctx"
	"itsm-backend/config"
	"itsm-backend/connector"
	"itsm-backend/connector/builtin/msgraph"
	"itsm-backend/database"
)

func TestManagerDescribesFrozenDeclaredGraphWithoutActivation(t *testing.T) {
	var requests atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(http.ResponseWriter, *http.Request) { requests.Add(1) }))
	defer server.Close()
	graphConfig := connector.Config{Name: "msgraph-email", Provider: "microsoft", Settings: map[string]interface{}{"azure_tenant_id": "test", "mailbox": "mailbox@example.invalid", "aad_base_url": server.URL, "graph_base_url": server.URL}, Credentials: map[string]string{"azure_client_id": "app"}}
	digest, err := msgraph.New().DescribeDeliveryDestination(graphConfig)
	require.NoError(t, err)
	scope := "149ff1af-a27c-47c7-827f-103271130bb9"
	for _, scenario := range []string{"valid", "digest mismatch", "wrong tenant", "wrong scope", "wrong deployment", "wrong owner", "wrong provider", "no tenant", "system context", "canceled", "closed manager", "missing name"} {
		t.Run(scenario, func(t *testing.T) {
			target := config.ConnectorTargetConfig{TenantID: 1, ScopeID: scope, Name: graphConfig.Name, Provider: graphConfig.Provider, Settings: graphConfig.Settings, Credentials: graphConfig.Credentials, DestinationDigest: digest, Capabilities: []string{"notification"}}
			if scenario == "digest mismatch" {
				target.DestinationDigest = "0123456789abcdef0123456789abcdef0123456789abcdef0123456789abcdef"
			}
			policy, err := database.NewExecutionPolicy(config.ExecutionConfig{Mode: "candidate", DeploymentID: "describe-test", Scopes: []config.ExecutionScopeConfig{{TenantID: 1, ScopeID: scope}}, ConnectorTargets: []config.ConnectorTargetConfig{target}})
			require.NoError(t, err)
			factoryCalls := 0
			registry := connector.NewRegistry()
			registry.Register(func() connector.Connector { factoryCalls++; return msgraph.New() })
			manager := connector.NewManager(registry, nil, policy)
			defer manager.CloseAll()
			describe, ok := any(manager).(interface {
				DescribeDeclaredDeliveryTarget(context.Context, executionscope.Ref, string, string, string) (string, error)
			})
			require.True(t, ok, "manager requires a trusted declaration description path")
			ctx := tenantctx.WithTenantID(context.Background(), 1)
			ref, err := policy.EventRef(1)
			require.NoError(t, err)
			owner, provider := "notification", "microsoft"
			name := "msgraph-email"
			switch scenario {
			case "canceled":
				canceled, cancel := context.WithCancel(ctx)
				cancel()
				ctx = canceled
			case "closed manager":
				manager.CloseAll()
			case "missing name":
				name = "missing"
			case "wrong tenant":
				ref.TenantID = 2
			case "wrong scope":
				ref.ScopeID = "249ff1af-a27c-47c7-827f-103271130bb9"
			case "wrong deployment":
				ref.DeploymentID = "other"
			case "wrong owner":
				owner = "outbox"
			case "wrong provider":
				provider = "other"
			case "no tenant":
				ctx = context.Background()
			case "system context":
				ctx = tenantctx.SystemContext(ctx, "test", "must reject bypass")
			}
			actual, err := describe.DescribeDeclaredDeliveryTarget(ctx, ref, owner, name, provider)
			if scenario == "valid" {
				require.NoError(t, err)
				require.Equal(t, digest, actual)
			} else {
				if scenario == "canceled" {
					require.ErrorIs(t, err, context.Canceled)
				} else {
					require.ErrorIs(t, err, executionscope.ErrDenied)
				}
				require.Empty(t, actual)
			}
			require.Equal(t, 1, factoryCalls)
			require.Empty(t, manager.ListByTenant(1))
			require.Zero(t, requests.Load())
		})
	}
}
