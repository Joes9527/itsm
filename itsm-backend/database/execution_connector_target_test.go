package database

import (
	"context"
	"testing"

	"github.com/stretchr/testify/require"
	"itsm-backend/common/executionscope"
	"itsm-backend/common/tenantctx"
	"itsm-backend/config"
)

func TestConnectorStartupTargetsRetainFrozenAuthority(t *testing.T) {
	cfg := config.ExecutionConfig{Mode: "candidate", DeploymentID: "target-test",
		Scopes:       []config.ExecutionScopeConfig{{TenantID: 1, ScopeID: "149ff1af-a27c-47c7-827f-103271130bb9"}},
		Capabilities: map[string]string{"webhook": "scoped"},
		ConnectorTargets: []config.ConnectorTargetConfig{{TenantID: 1, ScopeID: "149ff1af-a27c-47c7-827f-103271130bb9", Name: "webhook", Provider: "local-test",
			DestinationDigest: "0123456789abcdef0123456789abcdef0123456789abcdef0123456789abcdef",
			Capabilities:      []string{"webhook"}, Credentials: map[string]string{"secret": "synthetic-original"},
			Settings: map[string]interface{}{"url": "http://127.0.0.1:12345", "nested": map[string]interface{}{"values": []interface{}{"original"}}}}},
	}
	policy, err := NewExecutionPolicy(cfg)
	require.NoError(t, err)
	cfg.ConnectorTargets[0].Credentials["secret"] = "synthetic-replaced"
	cfg.ConnectorTargets[0].Settings["nested"].(map[string]interface{})["values"].([]interface{})[0] = "replaced"
	cfg.ConnectorTargets[0].Capabilities[0] = "notification"
	cfg.ConnectorTargets[0].TenantID = 2
	cfg.ConnectorTargets = append(cfg.ConnectorTargets, config.ConnectorTargetConfig{Name: "injected"})
	ctx := tenantctx.SystemContext(context.Background(), "test:targets", "read trusted startup declarations")
	for i := 0; i < 2; i++ {
		targets, err := policy.ConnectorStartupTargets(ctx)
		require.NoError(t, err)
		require.Len(t, targets, 1)
		require.Equal(t, 1, targets[0].TenantID)
		require.Equal(t, "synthetic-original", targets[0].Credentials["secret"])
		require.Equal(t, []string{"webhook"}, targets[0].Capabilities)
		require.Equal(t, "original", targets[0].Settings["nested"].(map[string]interface{})["values"].([]interface{})[0])
		targets[0].Credentials["secret"] = "synthetic-return-mutation"
		targets[0].Settings["nested"].(map[string]interface{})["values"].([]interface{})[0] = "returned-mutation"
		targets[0].Capabilities[0] = "notification"
	}
	for _, denied := range []context.Context{nil, context.Background(), tenantctx.WithTenantID(ctx, 1)} {
		targets, err := policy.ConnectorStartupTargets(denied)
		require.ErrorIs(t, err, executionscope.ErrDenied)
		require.Empty(t, targets)
	}
	canceled, cancel := context.WithCancel(ctx)
	cancel()
	targets, err := policy.ConnectorStartupTargets(canceled)
	require.ErrorIs(t, err, context.Canceled)
	require.Empty(t, targets)
	standard, err := NewExecutionPolicy(config.ExecutionConfig{Mode: "standard", DeploymentID: "target-standard"})
	require.NoError(t, err)
	targets, err = standard.ConnectorStartupTargets(ctx)
	require.ErrorIs(t, err, executionscope.ErrDenied)
	require.Empty(t, targets)
	var absent *ExecutionPolicy
	targets, err = absent.ConnectorStartupTargets(ctx)
	require.ErrorIs(t, err, executionscope.ErrDenied)
	require.Empty(t, targets)
}
