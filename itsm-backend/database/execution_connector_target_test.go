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

func TestConnectorActivationTargetsUseFrozenCapabilities(t *testing.T) {
	cfg := config.ExecutionConfig{Mode: "candidate", DeploymentID: "activation-test", Scopes: []config.ExecutionScopeConfig{{TenantID: 1, ScopeID: "149ff1af-a27c-47c7-827f-103271130bb9"}}, Capabilities: map[string]string{"notification": "disabled", "webhook": "scoped"}, ConnectorTargets: []config.ConnectorTargetConfig{{TenantID: 1, ScopeID: "149ff1af-a27c-47c7-827f-103271130bb9", Name: "webhook", Provider: "test", DestinationDigest: "0123456789abcdef0123456789abcdef0123456789abcdef0123456789abcdef", Capabilities: []string{"notification", "webhook"}, Settings: map[string]interface{}{"url": "http://127.0.0.1:12345"}}}}
	policy, err := NewExecutionPolicy(cfg)
	require.NoError(t, err)
	cfg.Capabilities["notification"] = "scoped"
	cfg.Capabilities["webhook"] = "disabled"
	ctx := tenantctx.SystemContext(context.Background(), "test:selection", "select frozen active declarations")
	for i := 0; i < 2; i++ {
		active, err := policy.ConnectorActivationTargets(ctx)
		require.NoError(t, err)
		require.Len(t, active, 1)
		require.Equal(t, []string{"webhook"}, active[0].Capabilities)
		require.Equal(t, "http://127.0.0.1:12345", active[0].Settings["url"])
		active[0].Capabilities[0] = "notification"
		active[0].Settings["url"] = "changed"
		all, err := policy.ConnectorStartupTargets(ctx)
		require.NoError(t, err)
		require.Len(t, all, 1)
		require.Equal(t, []string{"notification", "webhook"}, all[0].Capabilities)
	}
	for _, denied := range []context.Context{nil, context.Background(), tenantctx.WithTenantID(ctx, 1)} {
		active, err := policy.ConnectorActivationTargets(denied)
		require.ErrorIs(t, err, executionscope.ErrDenied)
		require.Empty(t, active)
	}
	canceled, cancel := context.WithCancel(ctx)
	cancel()
	active, err := policy.ConnectorActivationTargets(canceled)
	require.ErrorIs(t, err, context.Canceled)
	require.Empty(t, active)
}

func TestDeclaredConnectorTargetReturnsOwnedConfiguration(t *testing.T) {
	scope := "149ff1af-a27c-47c7-827f-103271130bb9"
	cfg := config.ExecutionConfig{Mode: "candidate", DeploymentID: "declared-read", Scopes: []config.ExecutionScopeConfig{{TenantID: 1, ScopeID: scope}}, ConnectorTargets: []config.ConnectorTargetConfig{{TenantID: 1, ScopeID: scope, Name: "msgraph-email", Provider: "microsoft", DestinationDigest: "0123456789abcdef0123456789abcdef0123456789abcdef0123456789abcdef", Capabilities: []string{"notification"}, Credentials: map[string]string{"azure_client_id": "original"}, Settings: map[string]interface{}{"mailbox": "original@example.invalid"}}}}
	policy, err := NewExecutionPolicy(cfg)
	require.NoError(t, err)
	cfg.ConnectorTargets[0].Settings["mailbox"] = "changed@example.invalid"
	ctx := tenantctx.WithTenantID(context.Background(), 1)
	ref, err := policy.EventRef(1)
	require.NoError(t, err)
	for i := 0; i < 2; i++ {
		target, err := policy.DeclaredConnectorTarget(ctx, ref, "notification", "msgraph-email", "microsoft")
		require.NoError(t, err)
		require.Equal(t, "original@example.invalid", target.Settings["mailbox"])
		require.Equal(t, "original", target.Credentials["azure_client_id"])
		target.Settings["mailbox"] = "returned-change"
		target.Credentials["azure_client_id"] = "returned-change"
		target.Capabilities[0] = "outbox"
	}
	require.ErrorIs(t, policy.RequireConnectorDelivery(ctx, ref, "notification"), executionscope.ErrDenied, "describing must not enable execution")
	canceled, cancel := context.WithCancel(ctx)
	cancel()
	target, err := policy.DeclaredConnectorTarget(canceled, ref, "notification", "msgraph-email", "microsoft")
	require.ErrorIs(t, err, context.Canceled)
	require.Empty(t, target)
	standard, err := NewExecutionPolicy(config.ExecutionConfig{Mode: "standard", DeploymentID: "standard"})
	require.NoError(t, err)
	standardRef, err := standard.EventRef(1)
	require.NoError(t, err)
	target, err = standard.DeclaredConnectorTarget(ctx, standardRef, "notification", "msgraph-email", "microsoft")
	require.ErrorIs(t, err, executionscope.ErrDenied)
	require.Empty(t, target)
}

func TestDeclaredConnectorTargetDistinguishesAbsenceFromInvalidAuthority(t *testing.T) {
	scope := "149ff1af-a27c-47c7-827f-103271130bb9"
	policy, err := NewExecutionPolicy(config.ExecutionConfig{Mode: "candidate", DeploymentID: "declared-read", Scopes: []config.ExecutionScopeConfig{{TenantID: 1, ScopeID: scope}}, ConnectorTargets: []config.ConnectorTargetConfig{{TenantID: 1, ScopeID: scope, Name: "feishu", Provider: "feishu", DestinationDigest: "0123456789abcdef0123456789abcdef0123456789abcdef0123456789abcdef", Capabilities: []string{"outbox"}}}})
	require.NoError(t, err)
	ctx := tenantctx.WithTenantID(context.Background(), 1)
	ref, err := policy.EventRef(1)
	require.NoError(t, err)
	_, err = policy.DeclaredConnectorTarget(ctx, ref, "outbox", "absent", "feishu")
	require.ErrorIs(t, err, executionscope.ErrTargetNotConfigured)
	require.ErrorIs(t, err, executionscope.ErrDenied)
	_, err = policy.DeclaredConnectorTarget(ctx, ref, "outbox", "feishu", "wrong-provider")
	require.ErrorIs(t, err, executionscope.ErrDenied)
	require.NotErrorIs(t, err, executionscope.ErrTargetNotConfigured)
	_, err = policy.DeclaredConnectorTarget(tenantctx.WithTenantID(ctx, 2), ref, "outbox", "absent", "feishu")
	require.ErrorIs(t, err, executionscope.ErrDenied)
	require.NotErrorIs(t, err, executionscope.ErrTargetNotConfigured)
	canceled, cancel := context.WithCancel(ctx)
	cancel()
	_, err = policy.DeclaredConnectorTarget(canceled, ref, "outbox", "absent", "feishu")
	require.ErrorIs(t, err, context.Canceled)
	require.NotErrorIs(t, err, executionscope.ErrTargetNotConfigured)
}
