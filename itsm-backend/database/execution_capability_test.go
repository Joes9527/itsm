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
