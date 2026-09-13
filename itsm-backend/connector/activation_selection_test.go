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
	"itsm-backend/connector/builtin/msgraph"
	"itsm-backend/database"
)

type activationSelectionProbe struct{ initialized *int }

func (*activationSelectionProbe) Manifest() connector.Manifest {
	return connector.Manifest{Name: "selection-probe", Provider: "test", Version: "1", RequiredPermissions: []string{"connector:write"}, InitializationBehavior: connector.InitializationLocalOnly}
}
func (p *activationSelectionProbe) Init(context.Context, connector.Config) error {
	*p.initialized++
	return nil
}
func (*activationSelectionProbe) Send(context.Context, *connector.Message) error { return nil }
func (*activationSelectionProbe) HealthCheck(context.Context) connector.HealthStatus {
	return connector.HealthStatus{}
}
func (*activationSelectionProbe) Close() error                        { return nil }
func (*activationSelectionProbe) DeliveryDestinationIdentity() string { return strings.Repeat("a", 64) }

func TestManagerActivatesOnlyEnabledDeclaredTargets(t *testing.T) {
	for _, scenario := range []string{"disabled", "omitted", "mixed", "enabled"} {
		t.Run(scenario, func(t *testing.T) {
			scope := "149ff1af-a27c-47c7-827f-103271130bb9"
			cfg := config.ExecutionConfig{Mode: "candidate", DeploymentID: "selection-test", Scopes: []config.ExecutionScopeConfig{{TenantID: 1, ScopeID: scope}}, Capabilities: map[string]string{"notification": "disabled"}, ConnectorTargets: []config.ConnectorTargetConfig{{TenantID: 1, ScopeID: scope, Name: "selection-probe", Provider: "test", DestinationDigest: strings.Repeat("a", 64), Capabilities: []string{"notification"}}}}
			switch scenario {
			case "omitted":
				cfg.Capabilities = map[string]string{}
			case "enabled":
				cfg.Capabilities["notification"] = "scoped"
			case "mixed":
				cfg.Capabilities["webhook"] = "scoped"
				cfg.ConnectorTargets[0].Capabilities = append(cfg.ConnectorTargets[0].Capabilities, "webhook")
			}
			policy, err := database.NewExecutionPolicy(cfg)
			require.NoError(t, err, "a complete target declaration must remain readable while execution is disabled")
			ctx := tenantctx.SystemContext(context.Background(), "test:selection", "read declarations and activate permitted targets")
			declarations, err := policy.ConnectorStartupTargets(ctx)
			require.NoError(t, err)
			require.Len(t, declarations, 1)
			initialized := 0
			registry := connector.NewRegistry()
			registry.Register(func() connector.Connector { return &activationSelectionProbe{initialized: &initialized} })
			manager := connector.NewManager(registry, nil, policy)
			defer manager.CloseAll()
			require.NoError(t, manager.ActivateStartupTargets(ctx))
			expected := 0
			if scenario == "mixed" || scenario == "enabled" {
				expected = 1
			}
			require.Equal(t, expected, initialized)
			require.Len(t, manager.ListByTenant(1), expected)
			if scenario == "mixed" {
				ref, err := policy.EventRef(1)
				require.NoError(t, err)
				_, _, _, err = manager.ResolveDeliveryTarget(tenantctx.WithTenantID(context.Background(), 1), ref, "notification", "selection-probe", "test")
				require.ErrorIs(t, err, executionscope.ErrDenied)
				_, _, _, err = manager.ResolveDeliveryTarget(tenantctx.WithTenantID(context.Background(), 1), ref, "webhook", "selection-probe", "test")
				require.NoError(t, err)
			}
		})
	}
}

func TestDisabledGraphDeclarationDoesNotConstructRuntimeInstance(t *testing.T) {
	var requests atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(http.ResponseWriter, *http.Request) { requests.Add(1) }))
	defer server.Close()
	graphConfig := connector.Config{Name: "msgraph-email", Provider: "microsoft", Settings: map[string]interface{}{"azure_tenant_id": "test", "mailbox": "mailbox@example.invalid", "aad_base_url": server.URL, "graph_base_url": server.URL}, Credentials: map[string]string{"azure_client_id": "app"}}
	digest, err := msgraph.New().DescribeDeliveryDestination(graphConfig)
	require.NoError(t, err)
	scope := "149ff1af-a27c-47c7-827f-103271130bb9"
	policy, err := database.NewExecutionPolicy(config.ExecutionConfig{Mode: "candidate", DeploymentID: "graph-disabled", Scopes: []config.ExecutionScopeConfig{{TenantID: 1, ScopeID: scope}}, ConnectorTargets: []config.ConnectorTargetConfig{{TenantID: 1, ScopeID: scope, Name: graphConfig.Name, Provider: graphConfig.Provider, Settings: graphConfig.Settings, Credentials: graphConfig.Credentials, DestinationDigest: digest, Capabilities: []string{"notification"}}}})
	require.NoError(t, err)
	created := 0
	registry := connector.NewRegistry()
	registry.Register(func() connector.Connector { created++; return msgraph.New() })
	require.Equal(t, 1, created, "registration constructs the existing prototype")
	manager := connector.NewManager(registry, nil, policy)
	defer manager.CloseAll()
	require.NoError(t, manager.ActivateStartupTargets(tenantctx.SystemContext(context.Background(), "test:graph-disabled", "verify no disabled activation")))
	require.Equal(t, 1, created)
	require.Empty(t, manager.ListByTenant(1))
	actual, err := registry.DescribeDeliveryDestination(graphConfig)
	require.NoError(t, err)
	require.Equal(t, digest, actual)
	require.Zero(t, requests.Load())
}

func TestCandidateGraphActivationIsLocalAndTargetBound(t *testing.T) {
	for _, scenario := range []string{"valid", "missing secret", "digest mismatch", "disabled", "cancelled"} {
		t.Run(scenario, func(t *testing.T) {
			var requests atomic.Int32
			type observedRequest struct{ path, clientID, secret string }
			observed := make(chan observedRequest, 4)
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				requests.Add(1)
				if strings.HasSuffix(r.URL.Path, "/token") {
					if err := r.ParseForm(); err != nil {
						http.Error(w, "invalid form", http.StatusBadRequest)
						return
					}
					observed <- observedRequest{r.URL.Path, r.Form.Get("client_id"), r.Form.Get("client_secret")}
					w.Header().Set("Content-Type", "application/json")
					fmt.Fprint(w, `{"access_token":"private-token","expires_in":3600}`)
					return
				}
				observed <- observedRequest{path: r.URL.Path}
				w.WriteHeader(http.StatusAccepted)
			}))
			defer server.Close()
			cfg := connector.Config{TenantID: 1, Name: "msgraph-email", Provider: "microsoft", Enabled: true, Settings: map[string]interface{}{"azure_tenant_id": "private-tenant", "mailbox": "original@example.invalid", "aad_base_url": server.URL, "graph_base_url": server.URL + "/graph"}, Credentials: map[string]string{"azure_client_id": "original-app", "azure_client_secret": "private-secret"}}
			digest, err := msgraph.New().DescribeDeliveryDestination(cfg)
			require.NoError(t, err)
			if scenario == "missing secret" {
				delete(cfg.Credentials, "azure_client_secret")
			}
			if scenario == "digest mismatch" {
				digest = strings.Repeat("b", 64)
			}
			scope := "149ff1af-a27c-47c7-827f-103271130bb9"
			execution := config.ExecutionConfig{Mode: "candidate", DeploymentID: "graph-local-init", Scopes: []config.ExecutionScopeConfig{{TenantID: 1, ScopeID: scope}}, Capabilities: map[string]string{"outbox": "scoped"}, ConnectorTargets: []config.ConnectorTargetConfig{{TenantID: 1, ScopeID: scope, Name: cfg.Name, Provider: cfg.Provider, Settings: cfg.Settings, Credentials: cfg.Credentials, DestinationDigest: digest, Capabilities: []string{"outbox"}}}}
			if scenario == "disabled" {
				execution.Capabilities["outbox"] = "disabled"
			}
			policy, err := database.NewExecutionPolicy(execution)
			require.NoError(t, err)
			// Caller changes after construction cannot change the frozen activation.
			cfg.Settings["mailbox"] = "changed@example.invalid"
			cfg.Credentials["azure_client_id"] = "changed-app"
			created := 0
			registry := connector.NewRegistry()
			registry.Register(func() connector.Connector { created++; return msgraph.New() })
			manager := connector.NewManager(registry, nil, policy)
			defer manager.CloseAll()
			ctx, cancel := context.WithCancel(tenantctx.SystemContext(context.Background(), "test:graph-startup", "private activation"))
			defer cancel()
			if scenario == "cancelled" {
				cancel()
			}
			err = manager.ActivateStartupTargets(ctx)
			require.Zero(t, requests.Load(), "activation must not acquire tokens, check health or poll")
			if scenario == "valid" {
				require.NoError(t, err)
				require.Len(t, manager.ListByTenant(1), 1)
				ref, err := policy.EventRef(1)
				require.NoError(t, err)
				tenantCtx := tenantctx.WithTenantID(context.Background(), 1)
				bound, _, actual, err := manager.ResolveDeliveryTarget(tenantCtx, ref, "outbox", cfg.Name, cfg.Provider)
				require.NoError(t, err)
				require.Equal(t, digest, actual)
				graph, ok := bound.(*msgraph.GraphConnector)
				require.True(t, ok)
				require.Equal(t, "original@example.invalid", graph.Mailbox())
				_, _, _, err = manager.ResolveDeliveryTarget(tenantCtx, ref, "notification", cfg.Name, cfg.Provider)
				require.ErrorIs(t, err, executionscope.ErrDenied)
				require.Zero(t, requests.Load())
				require.NoError(t, graph.SendMail(tenantCtx, graph.Mailbox(), "recipient@example.invalid", "private subject", "private body", "activation-test"))
				require.Equal(t, int32(2), requests.Load())
				tokenRequest, mailRequest := <-observed, <-observed
				require.Equal(t, "/private-tenant/oauth2/v2.0/token", tokenRequest.path)
				require.Equal(t, "original-app", tokenRequest.clientID)
				require.Equal(t, "private-secret", tokenRequest.secret)
				require.Equal(t, "/graph/users/original@example.invalid/sendMail", mailRequest.path)
			} else if scenario == "disabled" {
				require.NoError(t, err)
				require.Equal(t, 1, created)
				require.Empty(t, manager.ListByTenant(1))
			} else {
				require.Error(t, err)
				switch scenario {
				case "missing secret":
					require.ErrorContains(t, err, "client secret is required")
				case "digest mismatch":
					require.ErrorContains(t, err, "destination mismatch")
				case "cancelled":
					require.ErrorIs(t, err, context.Canceled)
				}
				require.Empty(t, manager.ListByTenant(1))
			}
		})
	}
}
