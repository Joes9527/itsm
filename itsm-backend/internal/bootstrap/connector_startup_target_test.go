package bootstrap

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"testing"

	"github.com/stretchr/testify/require"
	"go.uber.org/zap"
	"itsm-backend/config"
	"itsm-backend/connector"
	webhook "itsm-backend/connector/builtin/webhook"
	"itsm-backend/database"
)

func TestCandidateRuntimeActivatesOnlyFrozenDeclaredTargets(t *testing.T) {
	var requests atomic.Int32
	receiver := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { requests.Add(1); w.WriteHeader(http.StatusNoContent) }))
	defer receiver.Close()
	cfg := connectorStartupExecution(t, receiver.URL)
	policy, err := database.NewExecutionPolicy(cfg)
	require.NoError(t, err)
	reg := connector.NewRegistry()
	reg.Register(func() connector.Connector { return webhook.New() })
	manager := connector.NewManager(reg, zap.NewNop().Sugar(), policy)
	defer manager.CloseAll()
	// Later mutable config must not substitute the frozen startup destination.
	cfg.ConnectorTargets[0].Settings["url"] = receiver.URL + "/substituted"
	var background atomic.Int32
	app := &Application{Cfg: &config.Config{Execution: cfg}, executionPolicy: policy, connectorManager: manager,
		notificationWorker: &recordingTicketNotificationWorker{},
		startBackgroundTasksFunc: func(context.Context) {
			background.Add(1)
			_, exists := manager.Get(1, "webhook")
			if !exists {
				t.Error("declared targets must be ready before consumers start")
			}
		},
	}
	stop, err := app.startAPIRuntime(context.Background())
	require.NoError(t, err)
	defer stop()
	conn, _, ok := manager.GetInstance(1, "webhook", "local-startup")
	require.True(t, ok)
	require.Equal(t, connectorStartupDigest(t, receiver.URL), conn.(*webhook.Webhook).DeliveryDestinationIdentity())
	require.Len(t, manager.ListByTenant(1), 1)
	require.Empty(t, manager.ListByTenant(2))
	require.EqualValues(t, 1, background.Load())
	require.Zero(t, requests.Load(), "initialization must perform no network operation")
	stop()
	require.Empty(t, manager.ListByTenant(1))
}

func TestCandidateTargetFailurePreventsConsumersAndCleansBatch(t *testing.T) {
	for _, failure := range []string{"missing registry entry", "invalid destination", "destination mismatch", "undeclared initialization"} {
		t.Run(failure, func(t *testing.T) {
			cfg := connectorStartupExecution(t, "http://127.0.0.1:12345")
			second := cfg.ConnectorTargets[0]
			second.Provider = "failing-target"
			second.Settings = map[string]interface{}{"url": "http://127.0.0.1:12345"}
			var inits atomic.Int32
			reg := connector.NewRegistry()
			reg.Register(func() connector.Connector { return webhook.New() })
			reg.Register(func() connector.Connector {
				return &unclassifiedStartupConnector{Webhook: webhook.New(), inits: &inits}
			})
			switch failure {
			case "missing registry entry":
				second.Name = "not-registered"
			case "invalid destination":
				second.Settings["url"] = "invalid"
			case "destination mismatch":
				second.Settings["url"] = "http://127.0.0.1:12345/other"
			case "undeclared initialization":
				second.Name = "unclassified-startup"
			}
			cfg.ConnectorTargets = append(cfg.ConnectorTargets, second)
			policy, err := database.NewExecutionPolicy(cfg)
			require.NoError(t, err)
			manager := connector.NewManager(reg, zap.NewNop().Sugar(), policy)
			defer manager.CloseAll()
			var background atomic.Int32
			app := &Application{Cfg: &config.Config{Execution: cfg}, executionPolicy: policy, connectorManager: manager,
				notificationWorker: &recordingTicketNotificationWorker{}, startBackgroundTasksFunc: func(context.Context) { background.Add(1) }}
			stop, err := app.startAPIRuntime(context.Background())
			if stop != nil {
				defer stop()
			}
			require.Error(t, err)
			require.Zero(t, background.Load())
			require.Zero(t, inits.Load(), "unknown initialization behavior must be rejected before Init")
			require.Empty(t, manager.ListByTenant(1), "failed startup must not publish a partial target batch")
		})
	}
}

func connectorStartupDigest(t *testing.T, endpoint string) string {
	t.Helper()
	raw, err := json.Marshal(endpoint)
	require.NoError(t, err)
	digest := sha256.Sum256(raw)
	return hex.EncodeToString(digest[:])
}

func connectorStartupExecution(t *testing.T, endpoint string) config.ExecutionConfig {
	t.Helper()
	return config.ExecutionConfig{Mode: "candidate", DeploymentID: "startup-target-test",
		Scopes:       []config.ExecutionScopeConfig{{TenantID: 1, ScopeID: "149ff1af-a27c-47c7-827f-103271130bb9"}},
		Capabilities: map[string]string{"notification": "scoped"},
		ConnectorTargets: []config.ConnectorTargetConfig{{TenantID: 1, ScopeID: "149ff1af-a27c-47c7-827f-103271130bb9", Name: "webhook", Provider: "local-startup",
			DestinationDigest: connectorStartupDigest(t, endpoint), Capabilities: []string{"notification"}, Settings: map[string]interface{}{"url": endpoint}}},
	}
}

type unclassifiedStartupConnector struct {
	*webhook.Webhook
	inits *atomic.Int32
}

func (*unclassifiedStartupConnector) Manifest() connector.Manifest {
	return connector.Manifest{Name: "unclassified-startup", Version: "1", RequiredPermissions: []string{"connector:write"}}
}
func (c *unclassifiedStartupConnector) Init(ctx context.Context, cfg connector.Config) error {
	c.inits.Add(1)
	return c.Webhook.Init(ctx, cfg)
}
