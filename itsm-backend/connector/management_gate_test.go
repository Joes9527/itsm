package connector_test

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"testing"

	executionfixture "itsm-backend/tests/fixtures/execution"

	"github.com/stretchr/testify/require"
	"itsm-backend/common/executionscope"
	"itsm-backend/common/tenantctx"
	"itsm-backend/config"
	"itsm-backend/connector"
	webhook "itsm-backend/connector/builtin/webhook"
	"itsm-backend/database"
)

func TestManagerCandidateRevokePreservesDeclaredInstance(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { t.Error("unexpected request") }))
	defer server.Close()
	encoded, _ := json.Marshal(server.URL)
	digest := sha256.Sum256(encoded)
	scopeID := "149ff1af-a27c-47c7-827f-103271130bb9"
	policy, err := database.NewExecutionPolicy(config.ExecutionConfig{Mode: "candidate", DeploymentID: "manager-gate-test", Scopes: []config.ExecutionScopeConfig{{TenantID: 1, ScopeID: scopeID}}, Capabilities: map[string]string{"webhook": "scoped"}, ConnectorTargets: []config.ConnectorTargetConfig{{TenantID: 1, ScopeID: scopeID, Name: "webhook", Provider: "local", DestinationDigest: hex.EncodeToString(digest[:]), Capabilities: []string{"webhook"}, Settings: map[string]interface{}{"url": server.URL}}}})
	require.NoError(t, err)
	registry := connector.NewRegistry()
	registry.Register(func() connector.Connector { return webhook.New() })
	manager := connector.NewManager(registry, nil, policy)
	defer manager.CloseAll()
	ctx := tenantctx.WithTenantID(context.Background(), 1)
	require.NoError(t, manager.ActivateStartupTargets(tenantctx.SystemContext(ctx, "test:manager-revoke", "activate frozen local target")))
	before, generation, ok := manager.GetInstance(1, "webhook", "local")
	require.True(t, ok)
	require.ErrorIs(t, manager.Revoke(ctx, connector.Config{TenantID: 1, Name: "webhook", Provider: "local"}), executionscope.ErrDenied)
	after, afterGeneration, ok := manager.GetInstance(1, "webhook", "local")
	require.True(t, ok, "candidate request must not revoke declared runtime authority")
	require.Same(t, before, after)
	require.Equal(t, generation, afterGeneration)
}

type managementLifecycleProbe struct {
	init  func(context.Context) error
	close func() error
}

func (*managementLifecycleProbe) Manifest() connector.Manifest {
	return connector.Manifest{Name: "management-lifecycle", Version: "1", Title: "Local lifecycle", Type: connector.TypeCustom, RequiredPermissions: []string{"connector:write"}}
}

func (p *managementLifecycleProbe) Init(ctx context.Context, _ connector.Config) error {
	return p.init(ctx)
}

func (p *managementLifecycleProbe) Close() error                                 { return p.close() }
func (*managementLifecycleProbe) Send(context.Context, *connector.Message) error { return nil }
func (*managementLifecycleProbe) HealthCheck(context.Context) connector.HealthStatus {
	return connector.HealthStatus{}
}

func TestManagerCanceledInitializationNeverPublishes(t *testing.T) {
	entered, release := make(chan struct{}), make(chan struct{})
	var closes atomic.Int32
	closeFailure := errors.New("close fixture failure")
	registry := connector.NewRegistry()
	registry.Register(func() connector.Connector {
		return &managementLifecycleProbe{init: func(context.Context) error { close(entered); <-release; return nil }, close: func() error { closes.Add(1); return closeFailure }}
	})
	manager := connector.NewManager(registry, nil, executionfixture.Standard())
	defer manager.CloseAll()
	ctx, cancel := context.WithCancel(tenantctx.WithTenantID(context.Background(), 1))
	defer cancel()
	result := make(chan error, 1)
	go func() {
		result <- manager.Provision(ctx, connector.Config{TenantID: 1, Name: "management-lifecycle", Provider: "local", Enabled: true})
	}()
	<-entered
	cancel()
	close(release)
	err := <-result
	require.ErrorIs(t, err, context.Canceled)
	require.ErrorIs(t, err, closeFailure)
	require.Empty(t, manager.ListByTenant(1))
	require.EqualValues(t, 1, closes.Load())
}

func TestManagerRevokeFailurePreservesInstance(t *testing.T) {
	closeFailure := errors.New("close fixture failure")
	registry := connector.NewRegistry()
	registry.Register(func() connector.Connector {
		return &managementLifecycleProbe{init: func(context.Context) error { return nil }, close: func() error { return closeFailure }}
	})
	manager := connector.NewManager(registry, nil, executionfixture.Standard())
	defer manager.CloseAll()
	ctx := tenantctx.WithTenantID(context.Background(), 1)
	cfg := connector.Config{TenantID: 1, Name: "management-lifecycle", Provider: "local", Enabled: true}
	require.NoError(t, manager.Provision(ctx, cfg))
	before, generation, ok := manager.GetInstance(1, cfg.Name, cfg.Provider)
	require.True(t, ok)
	require.ErrorIs(t, manager.Revoke(ctx, cfg), closeFailure)
	cfg.Enabled = false
	require.ErrorIs(t, manager.Provision(ctx, cfg), closeFailure)
	after, current, ok := manager.GetInstance(1, cfg.Name, cfg.Provider)
	require.True(t, ok)
	require.Same(t, before, after)
	require.Equal(t, generation, current)
}

func TestManagerManagementGatePrecedesInitAndDisabledRevoke(t *testing.T) {
	for _, scenario := range []string{"nil-manager", "nil-policy", "missing-tenant", "foreign-tenant", "system", "canceled", "candidate"} {
		t.Run(scenario, func(t *testing.T) {
			var inits atomic.Int32
			registry := connector.NewRegistry()
			registry.Register(func() connector.Connector {
				return &managementLifecycleProbe{init: func(context.Context) error { inits.Add(1); return nil }, close: func() error { return nil }}
			})
			policy := executionfixture.Standard()
			if scenario == "candidate" {
				var err error
				policy, err = database.NewExecutionPolicy(config.ExecutionConfig{Mode: "candidate", DeploymentID: "manager-negative", Scopes: []config.ExecutionScopeConfig{{TenantID: 1, ScopeID: "149ff1af-a27c-47c7-827f-103271130bb9"}}})
				require.NoError(t, err)
			}
			manager := connector.NewManager(registry, nil, policy)
			if scenario == "nil-policy" {
				manager = connector.NewManager(registry, nil, nil)
			}
			if scenario == "nil-manager" {
				manager = nil
			} else {
				defer manager.CloseAll()
			}
			ctx := tenantctx.WithTenantID(context.Background(), 1)
			switch scenario {
			case "missing-tenant":
				ctx = context.Background()
			case "foreign-tenant":
				ctx = tenantctx.WithTenantID(ctx, 2)
			case "system":
				ctx = tenantctx.WithSystemBypass(ctx)
			case "canceled":
				canceled, cancel := context.WithCancel(ctx)
				cancel()
				ctx = canceled
			}
			expected := executionscope.ErrDenied
			if scenario == "canceled" {
				expected = context.Canceled
			}
			cfg := connector.Config{TenantID: 1, Name: "management-lifecycle", Provider: "local", Enabled: true}
			require.ErrorIs(t, manager.Provision(ctx, cfg), expected)
			cfg.Enabled = false
			require.ErrorIs(t, manager.Provision(ctx, cfg), expected)
			require.ErrorIs(t, manager.Revoke(ctx, cfg), expected)
			require.Zero(t, inits.Load())
		})
	}
}
