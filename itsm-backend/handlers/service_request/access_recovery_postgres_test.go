//go:build integration_c3

package service_request_test

import (
	"context"
	"os"
	"testing"

	"github.com/stretchr/testify/require"
	"go.uber.org/zap"
	"itsm-backend/config"
	"itsm-backend/database"
	"itsm-backend/ent"
	"itsm-backend/internal/bootstrap"
)

// A fresh named database is created/removed by the owned runner; no shared reset.
func TestC3AccessPostgresConcurrentReplay(t *testing.T) {
	client := initializeC3Postgres(t)
	fx, task, itemID, req := verifiedAccessFixture(t, client)
	assertC3AccessReplay(t, fx, task, itemID, req, true)
}

func TestC3AccessPostgresUnknownReceipt(t *testing.T) {
	client := initializeC3Postgres(t)
	fx, task, itemID, req := verifiedAccessFixture(t, client)
	assertC3UnknownFailure(t, fx, task, itemID, req)
}

func initializeC3Postgres(t *testing.T) *ent.Client {
	require.Equal(t, "sslvpn_c3_recovery_20260907", os.Getenv("C3_BOOTSTRAP_DATABASE"))
	cfg := &config.Config{Database: config.DatabaseConfig{Host: "127.0.0.1", Port: 36446, User: "postgres", DBName: os.Getenv("C3_BOOTSTRAP_DATABASE"), SSLMode: "disable"}, Deployment: config.DeploymentConfig{Mode: "development", AutoMigrate: true, AutoSeed: false}}
	client, err := database.InitDatabase(&cfg.Database)
	require.NoError(t, err)
	t.Cleanup(func() { client.Close() })
	var count int
	require.NoError(t, database.GetRawDB().QueryRowContext(context.Background(), "SELECT count(*) FROM information_schema.tables WHERE table_schema='public'").Scan(&count))
	require.Zero(t, count, "owned database must be empty")
	require.NoError(t, bootstrap.InitializeStorage(cfg, client, zap.NewNop().Sugar()))
	return client
}

func TestC3PostgresPreclaimedFailureRetry(t *testing.T) {
	client := initializeC3Postgres(t)
	fx, task, _, req := verifiedAccessFixture(t, client)
	assertC3PreclaimedFailure(t, fx, task, req, "retry")
}

func TestC3PostgresPreclaimedFailureResume(t *testing.T) {
	client := initializeC3Postgres(t)
	fx, task, _, req := verifiedAccessFixture(t, client)
	assertC3PreclaimedFailure(t, fx, task, req, "resume")
}

func TestC3PostgresPreclaimedFailureAfterSuccess(t *testing.T) {
	client := initializeC3Postgres(t)
	fx, task, _, req := verifiedAccessFixture(t, client)
	assertC3PreclaimedFailure(t, fx, task, req, "success")
}
