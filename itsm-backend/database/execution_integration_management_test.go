package database

import (
	"context"
	"testing"

	"github.com/stretchr/testify/require"
	"itsm-backend/common/executionscope"
	"itsm-backend/common/tenantctx"
	"itsm-backend/config"
)

func TestIntegrationManagementRequiresStandardTenantContext(t *testing.T) {
	ctx := tenantctx.WithTenantID(context.Background(), 1)
	standard, err := NewExecutionPolicy(config.ExecutionConfig{Mode: "standard", DeploymentID: "management-test"})
	require.NoError(t, err)
	require.NoError(t, standard.RequireIntegrationManagement(ctx, 1))
	for _, denied := range []context.Context{nil, context.Background(), tenantctx.WithTenantID(ctx, 2), tenantctx.SystemContext(ctx, "test", "not request authority")} {
		require.ErrorIs(t, standard.RequireIntegrationManagement(denied, 1), executionscope.ErrDenied)
	}
	require.ErrorIs(t, standard.RequireIntegrationManagement(ctx, 0), executionscope.ErrDenied)
	canceled, cancel := context.WithCancel(ctx)
	cancel()
	require.ErrorIs(t, standard.RequireIntegrationManagement(canceled, 1), context.Canceled)
	candidate, err := NewExecutionPolicy(config.ExecutionConfig{Mode: "candidate", DeploymentID: "management-test", Scopes: []config.ExecutionScopeConfig{{TenantID: 1, ScopeID: "149ff1af-a27c-47c7-827f-103271130bb9"}}})
	require.NoError(t, err)
	require.ErrorIs(t, candidate.RequireIntegrationManagement(ctx, 1), executionscope.ErrDenied)
	var absent *ExecutionPolicy
	require.ErrorIs(t, absent.RequireIntegrationManagement(ctx, 1), executionscope.ErrDenied)
}
