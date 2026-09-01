//go:build migrate

package main

import (
	"testing"

	"itsm-backend/config"

	"github.com/stretchr/testify/require"
)

func TestValidateFreshTargetRequiresDevelopmentModeAndExactConfirmation(t *testing.T) {
	cfg := &config.Config{Database: config.DatabaseConfig{Host: "127.0.0.1", Port: 5432, DBName: "itsm_fresh_test"}, Deployment: config.DeploymentConfig{Mode: "development"}}
	t.Setenv("ITSM_ALLOW_DESTRUCTIVE_FRESH", "true")
	t.Setenv("ITSM_FRESH_DATABASE", cfg.Database.DBName)
	t.Setenv("ITSM_FRESH_HOST", cfg.Database.Host)
	t.Setenv("ITSM_FRESH_PORT", "5432")
	require.NoError(t, validateFreshTarget(cfg))

	cfg.Deployment.Mode = "private"
	require.ErrorContains(t, validateFreshTarget(cfg), "development-only")
	cfg.Deployment.Mode = "development"
	t.Setenv("ITSM_FRESH_DATABASE", "different_database")
	require.ErrorContains(t, validateFreshTarget(cfg), "exact configured database")
}

func TestValidateFreshTargetRequiresNormalizedHostPortAndNonSystemDatabase(t *testing.T) {
	cfg := &config.Config{Database: config.DatabaseConfig{Host: "LOCALHOST.", Port: 5432, DBName: "itsm_fresh_test"}, Deployment: config.DeploymentConfig{Mode: "development"}}
	t.Setenv("ITSM_ALLOW_DESTRUCTIVE_FRESH", "true")
	t.Setenv("ITSM_FRESH_DATABASE", "itsm_fresh_test")
	t.Setenv("ITSM_FRESH_HOST", "localhost")
	t.Setenv("ITSM_FRESH_PORT", "5432")
	require.NoError(t, validateFreshTarget(cfg))

	t.Setenv("ITSM_FRESH_PORT", "5433")
	require.ErrorContains(t, validateFreshTarget(cfg), "exact configured port")
	t.Setenv("ITSM_FRESH_PORT", "5432")
	cfg.Database.Host = "192.168.31.66"
	t.Setenv("ITSM_FRESH_HOST", cfg.Database.Host)
	require.ErrorContains(t, validateFreshTarget(cfg), "shared host")

	cfg.Database.Host = "localhost"
	cfg.Database.DBName = "postgres"
	t.Setenv("ITSM_FRESH_HOST", "localhost")
	t.Setenv("ITSM_FRESH_DATABASE", "postgres")
	require.ErrorContains(t, validateFreshTarget(cfg), "system database")

	cfg.Database.DBName = "itsm_fresh_test"
	cfg.Database.Port = 0
	t.Setenv("ITSM_FRESH_DATABASE", cfg.Database.DBName)
	require.ErrorContains(t, validateFreshTarget(cfg), "invalid fresh database port")
}
