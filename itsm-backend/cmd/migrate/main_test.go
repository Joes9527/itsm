//go:build migrate

package main

import (
	"context"
	"database/sql"
	"errors"
	"net"
	"testing"

	"itsm-backend/config"
	"itsm-backend/migration"

	"github.com/stretchr/testify/require"
)

func TestCompleteSchemaReleaseProvisionsPrivilegesBeforePromotion(t *testing.T) {
	var events []string
	getenv := func(name string) string {
		events = append(events, "load:"+name)
		switch name {
		case "ITSM_MIGRATION_DB_USER":
			return "migration_role"
		case "ITSM_RUNTIME_DB_USER":
			return "runtime_role"
		default:
			return ""
		}
	}
	apply := func(_ context.Context, _ *sql.DB, roles migration.SchemaStateRoles) error {
		require.Equal(t, "migration_role", roles.MigrationRole)
		require.Equal(t, "runtime_role", roles.RuntimeRole)
		events = append(events, "privileges")
		return nil
	}
	promote := func(_ context.Context, _ migration.DBTX, release migration.ReleaseManifest) error {
		require.Equal(t, "028_schema_release_state", release.SchemaVersion)
		events = append(events, "promote")
		return nil
	}

	require.NoError(t, completeSchemaRelease(context.Background(), nil, getenv, apply, promote))
	require.Equal(t, []string{
		"load:ITSM_MIGRATION_DB_USER",
		"load:ITSM_RUNTIME_DB_USER",
		"privileges",
		"promote",
	}, events)
}

func TestCompleteSchemaReleaseFailsClosedBeforePromotion(t *testing.T) {
	promoted := false
	apply := func(context.Context, *sql.DB, migration.SchemaStateRoles) error {
		return errors.New("controlled privilege failure")
	}
	promote := func(context.Context, migration.DBTX, migration.ReleaseManifest) error {
		promoted = true
		return nil
	}

	err := completeSchemaRelease(context.Background(), nil, func(name string) string {
		if name == "ITSM_MIGRATION_DB_USER" {
			return "migration_role"
		}
		return "runtime_role"
	}, apply, promote)
	require.ErrorContains(t, err, "provision schema state privileges")
	require.False(t, promoted)

	err = completeSchemaRelease(context.Background(), nil, func(string) string { return "same_role" }, apply, promote)
	require.ErrorContains(t, err, "role")
	require.False(t, promoted)
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
