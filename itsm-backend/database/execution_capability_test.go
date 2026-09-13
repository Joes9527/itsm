package database

import (
	"context"
	"github.com/stretchr/testify/require"
	"itsm-backend/common/executionscope"
	"itsm-backend/common/tenantctx"
	"itsm-backend/config"
	"testing"
)

func TestExecutionCapabilityRequiresFrozenExplicitPermission(t *testing.T) {
	ctx := tenantctx.WithTenantID(context.Background(), 1)
	cfg := config.ExecutionConfig{Mode: "standard", DeploymentID: "capability-test", Capabilities: map[string]string{"cloud_discovery": "enabled"}}
	enabled, err := NewExecutionPolicy(cfg)
	require.NoError(t, err)
	cfg.Capabilities["cloud_discovery"] = "disabled"
	disabled, err := NewExecutionPolicy(cfg)
	require.NoError(t, err)
	cfg.Capabilities["cloud_discovery"] = "enabled"
	require.NoError(t, enabled.RequireCapability(ctx, 1, "cloud_discovery"), "later config revocation cannot change frozen policy")
	require.ErrorIs(t, disabled.RequireCapability(ctx, 1, "cloud_discovery"), executionscope.ErrDenied, "later config mutation cannot grant authority")
	candidate, err := NewExecutionPolicy(config.ExecutionConfig{Mode: "candidate", DeploymentID: "capability-test", Scopes: []config.ExecutionScopeConfig{{TenantID: 1, ScopeID: "149ff1af-a27c-47c7-827f-103271130bb9"}}, Capabilities: map[string]string{"cloud_discovery": "disabled"}})
	require.NoError(t, err)
	require.ErrorIs(t, candidate.RequireCapability(ctx, 1, "cloud_discovery"), executionscope.ErrDenied)
	require.ErrorIs(t, candidate.RequireCapability(context.Background(), 2, "cloud_discovery"), executionscope.ErrDenied)
	var absent *ExecutionPolicy
	require.ErrorIs(t, absent.RequireCapability(ctx, 1, "cloud_discovery"), executionscope.ErrDenied)
	require.ErrorIs(t, enabled.RequireCapability(nil, 1, "cloud_discovery"), executionscope.ErrDenied)
	require.ErrorIs(t, enabled.RequireCapability(context.Background(), 1, "cloud_discovery"), executionscope.ErrDenied)
	require.ErrorIs(t, enabled.RequireCapability(ctx, 2, "cloud_discovery"), executionscope.ErrDenied)
	require.ErrorIs(t, enabled.RequireCapability(ctx, 0, "cloud_discovery"), executionscope.ErrDenied)
	require.ErrorIs(t, enabled.RequireCapability(ctx, 1, "unknown"), executionscope.ErrDenied)
	require.ErrorIs(t, enabled.RequireCapability(ctx, 1, "embedding"), executionscope.ErrDenied)
	require.ErrorIs(t, enabled.RequireCapability(tenantctx.SystemContext(ctx, "test", "reject bypass"), 1, "cloud_discovery"), executionscope.ErrDenied)
	canceled, cancel := context.WithCancel(ctx)
	cancel()
	require.ErrorIs(t, enabled.RequireCapability(canceled, 1, "cloud_discovery"), context.Canceled)
}

func TestStartupCapabilityRequiresExplicitStandardSystemContext(t *testing.T) {
	ctx := tenantctx.SystemContext(context.Background(), "test:startup", "test frozen capability")
	cfg := config.ExecutionConfig{Mode: "standard", DeploymentID: "startup-test", Capabilities: map[string]string{"connector_poll": "enabled"}}
	enabled, err := NewExecutionPolicy(cfg)
	require.NoError(t, err)
	cfg.Capabilities["connector_poll"] = "disabled"
	disabled, err := NewExecutionPolicy(cfg)
	require.NoError(t, err)
	cfg.Capabilities["connector_poll"] = "enabled"
	require.NoError(t, enabled.RequireStartupCapability(ctx, "connector_poll"))
	require.ErrorIs(t, disabled.RequireStartupCapability(ctx, "connector_poll"), executionscope.ErrDenied)
	require.ErrorIs(t, enabled.RequireStartupCapability(context.Background(), "connector_poll"), executionscope.ErrDenied)
	require.ErrorIs(t, enabled.RequireStartupCapability(tenantctx.WithTenantID(ctx, 1), "connector_poll"), executionscope.ErrDenied)
	require.ErrorIs(t, enabled.RequireStartupCapability(nil, "connector_poll"), executionscope.ErrDenied)
	require.ErrorIs(t, enabled.RequireStartupCapability(ctx, "unknown"), executionscope.ErrDenied)
	require.ErrorIs(t, enabled.RequireStartupCapability(ctx, "embedding"), executionscope.ErrDenied)
	var absent *ExecutionPolicy
	require.ErrorIs(t, absent.RequireStartupCapability(ctx, "connector_poll"), executionscope.ErrDenied)
	candidate, err := NewExecutionPolicy(config.ExecutionConfig{Mode: "candidate", DeploymentID: "startup-test", Scopes: []config.ExecutionScopeConfig{{TenantID: 1, ScopeID: "149ff1af-a27c-47c7-827f-103271130bb9"}}, Capabilities: map[string]string{"connector_poll": "disabled"}})
	require.NoError(t, err)
	require.ErrorIs(t, candidate.RequireStartupCapability(ctx, "connector_poll"), executionscope.ErrDenied)
	canceled, cancel := context.WithCancel(ctx)
	cancel()
	require.ErrorIs(t, enabled.RequireStartupCapability(canceled, "connector_poll"), context.Canceled)
}

func TestExecutionWorkerCapabilityRequiresExplicitFrozenPermission(t *testing.T) {
	for _, mode := range []string{"standard", "candidate"} {
		t.Run(mode, func(t *testing.T) {
			cfg := config.ExecutionConfig{Mode: mode, DeploymentID: "worker-gate", Capabilities: map[string]string{"outbox": "enabled"}}
			if mode == "candidate" {
				cfg.Scopes = []config.ExecutionScopeConfig{{TenantID: 1, ScopeID: "149ff1af-a27c-47c7-827f-103271130bb9"}}
				cfg.Capabilities["outbox"] = "scoped"
			}
			enabled, err := NewExecutionPolicy(cfg)
			require.NoError(t, err)
			cfg.Capabilities["outbox"] = "disabled"
			disabled, err := NewExecutionPolicy(cfg)
			require.NoError(t, err)
			ctx := tenantctx.SystemContext(context.Background(), "outbox:poll", "worker gate test")
			require.NoError(t, enabled.RequireWorkerCapability(ctx, "outbox"))
			require.ErrorIs(t, disabled.RequireWorkerCapability(ctx, "outbox"), executionscope.ErrDenied)
			require.ErrorIs(t, enabled.RequireWorkerCapability(ctx, "unknown"), executionscope.ErrDenied)
			require.ErrorIs(t, enabled.RequireWorkerCapability(context.Background(), "outbox"), executionscope.ErrDenied)
			require.ErrorIs(t, enabled.RequireWorkerCapability(tenantctx.WithTenantID(ctx, 1), "outbox"), executionscope.ErrDenied)
			require.ErrorIs(t, enabled.RequireWorkerCapability(nil, "outbox"), executionscope.ErrDenied)
			cancelled, cancel := context.WithCancel(ctx)
			cancel()
			require.ErrorIs(t, enabled.RequireWorkerCapability(cancelled, "outbox"), context.Canceled)
			var absent *ExecutionPolicy
			require.ErrorIs(t, absent.RequireWorkerCapability(ctx, "outbox"), executionscope.ErrDenied)
		})
	}
}
