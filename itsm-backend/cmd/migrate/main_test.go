//go:build migrate

package main

import (
	"context"
	"database/sql"
	"database/sql/driver"
	"errors"
	"net"
	"testing"

	"itsm-backend/config"

	"github.com/stretchr/testify/require"
)

func TestNormalizeMigrationCommandRequiresExactlyOneAction(t *testing.T) {
	for name, command := range map[string]migrationCommand{
		"up and status":        {up: true, status: true},
		"down and rollback-to": {down: true, rollbackVersion: "022_prior"},
		"dry-run and seed":     {dryRun: true, seed: true},
		"fresh and reset":      {fresh: true, reset: true},
	} {
		t.Run(name, func(t *testing.T) {
			_, err := normalizeMigrationCommand(command)
			require.ErrorContains(t, err, "exactly one command")
			require.NotContains(t, err.Error(), "publication gate")
		})
	}

	action, err := normalizeMigrationCommand(migrationCommand{rollbackVersion: " 022_prior "})
	require.NoError(t, err)
	require.Equal(t, migrationActionRollbackTo, action)
}

func TestValidateCommandPublicationNormalizesBeforeGate(t *testing.T) {
	err := validateCommandPublication(migrationCommand{status: true, up: true})
	require.ErrorContains(t, err, "exactly one command")
	require.NotContains(t, err.Error(), "publication gate")
}

func TestValidateCommandPublicationGatesEveryMutatingFrontDoor(t *testing.T) {
	mutating := map[string]migrationCommand{
		"up":          {up: true},
		"down":        {down: true},
		"rollback-to": {rollbackVersion: "022_prior"},
		"fresh":       {fresh: true},
		"reset":       {reset: true},
		"seed":        {seed: true},
		"seed-only":   {seedOnly: true},
	}
	for name, command := range mutating {
		t.Run(name, func(t *testing.T) {
			err := validateCommandPublication(command)
			require.ErrorContains(t, err, "release publication gate is closed")
		})
	}
}

func TestValidateCommandPublicationLeavesReadOnlyFrontDoorsAvailable(t *testing.T) {
	for name, command := range map[string]migrationCommand{
		"status":  {status: true},
		"list":    {list: true},
		"dry-run": {dryRun: true},
		"version": {version: true},
		"help":    {},
	} {
		t.Run(name, func(t *testing.T) {
			require.NoError(t, validateCommandPublication(command))
		})
	}
}

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
	previousLookup := lookupFreshHostIPs
	lookupFreshHostIPs = func(string) ([]net.IP, error) { return []net.IP{net.ParseIP("127.0.0.1")}, nil }
	t.Cleanup(func() { lookupFreshHostIPs = previousLookup })
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

func TestValidateFreshTargetRejectsIPv4MappedSharedHost(t *testing.T) {
	cfg := &config.Config{Database: config.DatabaseConfig{Host: "::ffff:192.168.31.66", Port: 5432, DBName: "itsm_fresh_test"}, Deployment: config.DeploymentConfig{Mode: "development"}}
	t.Setenv("ITSM_ALLOW_DESTRUCTIVE_FRESH", "true")
	t.Setenv("ITSM_FRESH_DATABASE", cfg.Database.DBName)
	t.Setenv("ITSM_FRESH_HOST", cfg.Database.Host)
	t.Setenv("ITSM_FRESH_PORT", "5432")
	require.ErrorContains(t, validateFreshTarget(cfg), "shared host")
}

func TestValidateFreshTargetRejectsHostnameResolvingToSharedHost(t *testing.T) {
	previousLookup := lookupFreshHostIPs
	lookupFreshHostIPs = func(host string) ([]net.IP, error) {
		require.Equal(t, "fresh-alias.internal", host)
		return []net.IP{net.ParseIP("127.0.0.1"), net.ParseIP("192.168.31.66")}, nil
	}
	t.Cleanup(func() { lookupFreshHostIPs = previousLookup })

	cfg := &config.Config{Database: config.DatabaseConfig{Host: "fresh-alias.internal", Port: 5432, DBName: "itsm_fresh_test"}, Deployment: config.DeploymentConfig{Mode: "development"}}
	t.Setenv("ITSM_ALLOW_DESTRUCTIVE_FRESH", "true")
	t.Setenv("ITSM_FRESH_DATABASE", cfg.Database.DBName)
	t.Setenv("ITSM_FRESH_HOST", cfg.Database.Host)
	t.Setenv("ITSM_FRESH_PORT", "5432")
	require.ErrorContains(t, validateFreshTarget(cfg), "resolved as")
}

type recordingFreshAdmin struct {
	statements []string
}

func (database *recordingFreshAdmin) ExecContext(_ context.Context, statement string, _ ...any) (sql.Result, error) {
	database.statements = append(database.statements, statement)
	return driver.RowsAffected(1), nil
}

func TestRecreateFreshDatabaseRunsReadOnlyPreflightBeforeDrop(t *testing.T) {
	admin := &recordingFreshAdmin{}
	preflightFailure := errors.New("platform preflight failed")
	err := recreateFreshDatabase(context.Background(), admin, "itsm_fresh_test", func() error {
		return preflightFailure
	})
	require.ErrorIs(t, err, preflightFailure)
	require.Empty(t, admin.statements, "failed preflight must not issue DROP or CREATE")

	err = recreateFreshDatabase(context.Background(), admin, "itsm_fresh_test", func() error { return nil })
	require.NoError(t, err)
	require.Len(t, admin.statements, 2)
	require.Contains(t, admin.statements[0], "DROP DATABASE")
	require.Contains(t, admin.statements[1], "CREATE DATABASE")
}
