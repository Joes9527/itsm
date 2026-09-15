package database

import (
	"testing"

	"github.com/stretchr/testify/require"
	"itsm-backend/config"
)

func TestExecutionPolicyFreezesAdmittedTenants(t *testing.T) {
	cfg := config.ExecutionConfig{Mode: "candidate", DeploymentID: "candidate-test", Scopes: []config.ExecutionScopeConfig{{TenantID: 1, ScopeID: "11111111-1111-4111-8111-111111111111"}}}
	policy, err := NewExecutionPolicy(cfg)
	require.NoError(t, err)
	ids := policy.CandidateTenantIDs()
	require.Equal(t, []int{1}, ids)
	ids[0] = 99
	require.Equal(t, []int{1}, policy.CandidateTenantIDs())
	cfg.Scopes[0].TenantID = 2
	cfg.Scopes[0].ScopeID = "22222222-2222-4222-8222-222222222222"
	ref, scoped, err := policy.scopeFor(1)
	require.NoError(t, err)
	require.True(t, scoped)
	require.Equal(t, "11111111-1111-4111-8111-111111111111", ref.ScopeID)
	_, _, err = policy.scopeFor(2)
	require.Error(t, err, "a valid but unadmitted tenant must not be discovered from the database")
	var missing *ExecutionPolicy
	_, _, err = missing.scopeFor(1)
	require.Error(t, err)
	_, _, err = (&ExecutionPolicy{}).scopeFor(1)
	require.Error(t, err)
}

func TestExecutionPolicyRequiresExplicitMode(t *testing.T) {
	_, err := NewExecutionPolicy(config.ExecutionConfig{})
	require.Error(t, err)
	policy, err := NewExecutionPolicy(config.ExecutionConfig{Mode: "standard", DeploymentID: "standard-test"})
	require.NoError(t, err)
	_, scoped, err := policy.scopeFor(1)
	require.NoError(t, err)
	require.False(t, scoped)
	_, _, err = policy.scopeFor(0)
	require.Error(t, err)
}

func TestExecutionPolicyEventRefFreezesModeAndDeployment(t *testing.T) {
	cfg := config.ExecutionConfig{Mode: "standard", DeploymentID: "standard-events"}
	policy, err := NewExecutionPolicy(cfg)
	require.NoError(t, err)
	cfg.DeploymentID = "changed"
	ref, err := policy.EventRef(17)
	require.NoError(t, err)
	require.Equal(t, "standard-events", ref.DeploymentID)
	require.Equal(t, 17, ref.TenantID)
	require.Empty(t, ref.ScopeID)
	_, err = policy.CandidateRef(17)
	require.Error(t, err)
	_, err = policy.EventRef(0)
	require.Error(t, err)
	var absent *ExecutionPolicy
	_, err = absent.EventRef(17)
	require.Error(t, err)
	_, err = (&ExecutionPolicy{mode: "standard"}).EventRef(17)
	require.Error(t, err)
	candidate, err := NewExecutionPolicy(config.ExecutionConfig{Mode: "candidate", DeploymentID: "candidate-events", Scopes: []config.ExecutionScopeConfig{{TenantID: 17, ScopeID: "11111111-1111-4111-8111-111111111111"}}})
	require.NoError(t, err)
	ref, err = candidate.EventRef(17)
	require.NoError(t, err)
	expected, err := candidate.CandidateRef(17)
	require.NoError(t, err)
	require.Equal(t, expected, ref)
	_, err = candidate.EventRef(18)
	require.Error(t, err)
}
