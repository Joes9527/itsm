package connector_test

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"github.com/stretchr/testify/require"
	"itsm-backend/common/executionscope"
	"itsm-backend/common/tenantctx"
	"itsm-backend/config"
	"itsm-backend/connector"
	"itsm-backend/connector/builtin/webhook"
	"itsm-backend/database"
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestManagerDeliveryRequiresDeclaredTargetCapability(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(http.ResponseWriter, *http.Request) { t.Error("resolution must not send") }))
	defer server.Close()
	raw, _ := json.Marshal(server.URL)
	sum := sha256.Sum256(raw)
	digest := hex.EncodeToString(sum[:])
	scope := "149ff1af-a27c-47c7-827f-103271130bb9"
	// Both capabilities are globally admitted, but this exact target is webhook-only.
	p, err := database.NewExecutionPolicy(config.ExecutionConfig{Mode: "candidate", DeploymentID: "delivery-test", Scopes: []config.ExecutionScopeConfig{{TenantID: 1, ScopeID: scope}}, Capabilities: map[string]string{"webhook": "scoped", "notification": "scoped"}, ConnectorTargets: []config.ConnectorTargetConfig{{TenantID: 1, ScopeID: scope, Name: "webhook", Provider: "local", DestinationDigest: digest, Capabilities: []string{"webhook"}, Settings: map[string]interface{}{"url": server.URL}}}})
	require.NoError(t, err)
	registry := connector.NewRegistry()
	registry.Register(func() connector.Connector { return webhook.New() })
	manager := connector.NewManager(registry, nil, p)
	defer manager.CloseAll()
	ctx := tenantctx.WithTenantID(context.Background(), 1)
	require.NoError(t, manager.ActivateStartupTargets(tenantctx.SystemContext(ctx, "test", "activate loopback target")))
	ref, err := p.EventRef(1)
	require.NoError(t, err)
	actual, generation, gotDigest, err := manager.ResolveDeliveryTarget(ctx, ref, "webhook", "webhook", "local")
	require.NoError(t, err)
	expected, wantGeneration, ok := manager.GetInstance(1, "webhook", "local")
	require.True(t, ok)
	require.Same(t, expected, actual)
	require.Equal(t, wantGeneration, generation)
	require.Equal(t, digest, gotDigest)
	for _, tc := range []struct{ cap, name, provider string }{{"notification", "webhook", "local"}, {"webhook", "other", "local"}, {"webhook", "webhook", "other"}} {
		conn, gen, d, err := manager.ResolveDeliveryTarget(ctx, ref, tc.cap, tc.name, tc.provider)
		require.ErrorIs(t, err, executionscope.ErrDenied)
		require.Nil(t, conn)
		require.Zero(t, gen)
		require.Empty(t, d)
	}
	manager.CloseAll()
	_, _, _, err = manager.ResolveDeliveryTarget(ctx, ref, "webhook", "webhook", "local")
	require.ErrorIs(t, err, executionscope.ErrDenied)
}
