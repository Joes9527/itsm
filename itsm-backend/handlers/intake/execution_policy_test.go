package intake

import (
	"context"
	"testing"

	"github.com/stretchr/testify/require"
	"itsm-backend/common/tenantctx"
	"itsm-backend/config"
	"itsm-backend/database"
	"itsm-backend/handlers/common/workitemcreation"
)

func TestApplicationExecutionQueryFailureIsInfrastructureFailure(t *testing.T) {
	client, s, i, c, _, _ := intakeFixture(t)
	// The SQLite fixture deliberately lacks PostgreSQL set_config. A database
	// execution error must remain unavailable/retryable, not become a denial.
	policy, err := database.NewExecutionPolicy(config.ExecutionConfig{Mode: "candidate", DeploymentID: "test-query-failure", Scopes: []config.ExecutionScopeConfig{{TenantID: i.TenantID, ScopeID: "11111111-1111-4111-8111-111111111111"}}})
	require.NoError(t, err)
	s.execution = policy
	_, err = s.Create(tenantctx.WithTenantID(context.Background(), i.TenantID), i, c)
	require.ErrorIs(t, err, workitemcreation.ErrInfrastructureUnavailable)
	require.NotErrorIs(t, err, workitemcreation.ErrPermissionDenied)
	var failure *workitemcreation.IntakeError
	require.ErrorAs(t, err, &failure)
	require.True(t, failure.Retryable)
	require.Zero(t, client.IntakeRequest.Query().CountX(context.Background()))
	require.Zero(t, client.Ticket.Query().CountX(context.Background()))
}
