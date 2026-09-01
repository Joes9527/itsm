//go:build migrate

package main

import (
	"testing"

	"itsm-backend/config"

	"github.com/stretchr/testify/require"
)

func TestValidateFreshTargetRequiresDevelopmentModeAndExactConfirmation(t *testing.T) {
	cfg := &config.Config{Database: config.DatabaseConfig{DBName: "itsm_fresh_test"}, Deployment: config.DeploymentConfig{Mode: "development"}}
	t.Setenv("ITSM_ALLOW_DESTRUCTIVE_FRESH", "true")
	t.Setenv("ITSM_FRESH_DATABASE", cfg.Database.DBName)
	require.NoError(t, validateFreshTarget(cfg))

	cfg.Deployment.Mode = "private"
	require.ErrorContains(t, validateFreshTarget(cfg), "development-only")
	cfg.Deployment.Mode = "development"
	t.Setenv("ITSM_FRESH_DATABASE", "different_database")
	require.ErrorContains(t, validateFreshTarget(cfg), "exact configured database")
}
