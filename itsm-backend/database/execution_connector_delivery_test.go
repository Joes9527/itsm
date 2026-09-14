package database

import (
	"context"
	"testing"

	"github.com/stretchr/testify/require"
	"itsm-backend/common/executionscope"
	"itsm-backend/common/tenantctx"
	"itsm-backend/config"
)

func TestConnectorDeliveryRequiresExactFrozenIdentity(t *testing.T) {
	ctx := tenantctx.WithTenantID(context.Background(), 1)
	for _, mode := range []string{"standard", "candidate"} {
		t.Run(mode, func(t *testing.T) {
			cfg := config.ExecutionConfig{Mode: mode, DeploymentID: "delivery-test", Capabilities: map[string]string{"webhook": "enabled"}}
			if mode == "candidate" {
				cfg.Capabilities["webhook"] = "scoped"
				cfg.Scopes = []config.ExecutionScopeConfig{{TenantID: 1, ScopeID: "149ff1af-a27c-47c7-827f-103271130bb9"}}
			}
			p, err := NewExecutionPolicy(cfg)
			require.NoError(t, err)
			ref, err := p.EventRef(1)
			require.NoError(t, err)
			cfg.DeploymentID = "changed"
			cfg.Capabilities["webhook"] = "disabled"
			require.NoError(t, p.RequireConnectorDelivery(ctx, ref, "webhook"))
			for _, cap := range []string{"notification", "outbox", "connector_poll", "unknown", ""} {
				require.ErrorIs(t, p.RequireConnectorDelivery(ctx, ref, cap), executionscope.ErrDenied)
			}
			for _, badCtx := range []context.Context{nil, context.Background(), tenantctx.WithTenantID(ctx, 2), tenantctx.SystemContext(ctx, "test", "no delivery authority")} {
				require.ErrorIs(t, p.RequireConnectorDelivery(badCtx, ref, "webhook"), executionscope.ErrDenied)
			}
			canceled, cancel := context.WithCancel(ctx)
			cancel()
			require.ErrorIs(t, p.RequireConnectorDelivery(canceled, ref, "webhook"), context.Canceled)
			deployment, scope, tenant := ref, ref, ref
			deployment.DeploymentID = "other"
			scope.ScopeID = "other"
			tenant.TenantID = 2
			for _, badRef := range []executionscope.Ref{{}, deployment, scope, tenant} {
				require.ErrorIs(t, p.RequireConnectorDelivery(ctx, badRef, "webhook"), executionscope.ErrDenied)
			}
		})
	}
	var absent *ExecutionPolicy
	require.ErrorIs(t, absent.RequireConnectorDelivery(ctx, executionscope.Ref{TenantID: 1}, "webhook"), executionscope.ErrDenied)
}

func TestPersistedConnectorDescriptionRequiresStandardIdentity(t *testing.T) {
	ctx := tenantctx.WithTenantID(context.Background(), 1)
	for _, mode := range []string{"standard", "candidate"} {
		cfg := config.ExecutionConfig{Mode: mode, DeploymentID: "description-test"}
		if mode == "candidate" {
			cfg.Scopes = []config.ExecutionScopeConfig{{TenantID: 1, ScopeID: "149ff1af-a27c-47c7-827f-103271130bb9"}}
		}
		policy, err := NewExecutionPolicy(cfg)
		require.NoError(t, err)
		ref, err := policy.EventRef(1)
		require.NoError(t, err)
		err = policy.RequirePersistedConnectorDescription(ctx, ref, "notification")
		if mode == "standard" {
			require.NoError(t, err)
		} else {
			require.ErrorIs(t, err, executionscope.ErrDenied)
		}
		require.ErrorIs(t, policy.RequireConnectorDelivery(ctx, ref, "notification"), executionscope.ErrDenied)
		for _, badCtx := range []context.Context{nil, context.Background(), tenantctx.WithTenantID(ctx, 2), tenantctx.SystemContext(ctx, "test", "reject bypass")} {
			require.ErrorIs(t, policy.RequirePersistedConnectorDescription(badCtx, ref, "notification"), executionscope.ErrDenied)
		}
		canceled, cancel := context.WithCancel(ctx)
		cancel()
		require.ErrorIs(t, policy.RequirePersistedConnectorDescription(canceled, ref, "notification"), context.Canceled)
		wrong := ref
		wrong.DeploymentID = "other"
		require.ErrorIs(t, policy.RequirePersistedConnectorDescription(ctx, wrong, "notification"), executionscope.ErrDenied)
		require.ErrorIs(t, policy.RequirePersistedConnectorDescription(ctx, ref, "unknown"), executionscope.ErrDenied)
	}
}
