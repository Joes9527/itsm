//go:build integration_c2

package integration

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
	"go.uber.org/zap"
	"itsm-backend/config"
	"itsm-backend/database"
	"itsm-backend/ent"
	"itsm-backend/handlers/common/accessgrant"
	"itsm-backend/internal/bootstrap"
	"itsm-backend/service"
)

// This deliberately reconstructs a legacy ledger column shape in a new,
// explicitly guarded owned database; never run on a reused deployment DB.
func TestC2AccessInitializeStorageUpgradeAndRestart(t *testing.T) {
	require.Equal(t, "sslvpn_c2_storage_20260907", os.Getenv("C2_BOOTSTRAP_DATABASE"))
	cfg := &config.Config{Database: config.DatabaseConfig{Host: "127.0.0.1", Port: 36446, User: "postgres", DBName: os.Getenv("C2_BOOTSTRAP_DATABASE"), SSLMode: "disable"}, Deployment: config.DeploymentConfig{Mode: "development", AutoMigrate: true, AutoSeed: false}}
	client, err := database.InitDatabase(&cfg.Database)
	require.NoError(t, err)
	defer client.Close()
	db := database.GetRawDB()
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Minute)
	defer cancel()
	var tables int
	require.NoError(t, db.QueryRowContext(ctx, "SELECT count(*) FROM information_schema.tables WHERE table_schema='public'").Scan(&tables))
	require.Zero(t, tables, "test requires an entirely new owned database")
	require.NoError(t, bootstrap.InitializeStorage(cfg, client, zap.NewNop().Sugar()))
	legacy := client.KafTaskActionLedger.Create().SetTenantID(1).SetTaskID("legacy-access").
		SetRunID("run").SetStepID("finish").SetAction("complete_bpmn_task").SetIdempotencyKey("1:legacy-access:run:finish").
		SetCorrelationID("corr").SetProcedureRef("graph_vpn_access_grant").SetProcedureVersion("1").SetResultStatus("applied").SaveX(ctx)
	var before string
	require.NoError(t, db.QueryRowContext(ctx, "SELECT (to_jsonb(l)-'request_digest')::text FROM kaf_task_action_ledgers l WHERE id=$1", legacy.ID).Scan(&before))
	_, err = db.ExecContext(ctx, "ALTER TABLE kaf_task_action_ledgers DROP COLUMN request_digest; DELETE FROM schema_migrations WHERE version='031_kaf_action_request_digest'")
	require.NoError(t, err)
	var columns int
	require.NoError(t, db.QueryRowContext(ctx, "SELECT count(*) FROM information_schema.columns WHERE table_name='kaf_task_action_ledgers' AND column_name='request_digest'").Scan(&columns))
	require.Zero(t, columns)
	for pass := 0; pass < 2; pass++ {
		require.NoError(t, bootstrap.InitializeStorage(cfg, client, zap.NewNop().Sugar()))
		var after, digest string
		require.NoError(t, db.QueryRowContext(ctx, "SELECT (to_jsonb(l)-'request_digest')::text,request_digest FROM kaf_task_action_ledgers l WHERE id=$1", legacy.ID).Scan(&after, &digest))
		require.Equal(t, before, after, "existing execution identity and receipt survive upgrade")
		require.Empty(t, digest, "migration must not invent authorization evidence")
		var count int
		var head string
		require.NoError(t, db.QueryRowContext(ctx, "SELECT count(*),max(version) FROM schema_migrations").Scan(&count, &head))
		require.Equal(t, 24, count)
		require.Equal(t, "031_kaf_action_request_digest", head)
		for _, table := range []string{"catalog_access_policies", "service_request_access_snapshots", "service_request_access_results"} {
			var checks, policies, fks, uniqueIndexes int
			var rls bool
			require.NoError(t, db.QueryRowContext(ctx, `SELECT c.relrowsecurity,(SELECT count(*) FROM pg_constraint k WHERE k.conrelid=c.oid AND k.contype='c'),(SELECT count(*) FROM pg_policy p WHERE p.polrelid=c.oid),(SELECT count(*) FROM pg_constraint k WHERE k.conrelid=c.oid AND k.contype='f'),(SELECT count(*) FROM pg_index i WHERE i.indrelid=c.oid AND i.indisunique AND i.indisvalid) FROM pg_class c WHERE c.oid=$1::regclass`, table).Scan(&rls, &checks, &policies, &fks, &uniqueIndexes))
			require.True(t, rls)
			require.Positive(t, checks)
			require.Positive(t, policies)
			require.Positive(t, fks)
			require.Positive(t, uniqueIndexes)
		}
		var guards int
		require.NoError(t, db.QueryRowContext(ctx, "SELECT count(*) FROM pg_trigger WHERE NOT tgisinternal AND tgenabled<>'D' AND tgname IN ('access_snapshot_guard','access_result_guard')").Scan(&guards))
		require.Equal(t, 2, guards)
		t.Logf("InitializeStorage pass %d: 031 applied once, original ledger unchanged, C1 RLS/CHECK/FK/unique/immutable guards present", pass+1)
	}
	task := &ent.ProcessTask{TaskID: "legacy-access", TenantID: 1, CallbackAction: accessgrant.Capability}
	req := service.KafActionRequest{Action: "complete_bpmn_task", ExpectedVersion: 1,
		Execution: service.KafActionExecution{RunID: "run", StepID: "finish", IdempotencyKey: "1:legacy-access:run:finish", CorrelationID: "corr", ProcedureRef: "graph_vpn_access_grant", ProcedureVersion: "1"},
		Payload:   service.KafActionPayload{ResultSummary: "granted", AccessResult: json.RawMessage(`{"outcome":"granted"}`)}}
	_, _, err = service.NewKafDelegationService(client).ClaimKafAction(ctx, task, req)
	require.ErrorIs(t, err, service.ErrKafActionConflict)
	t.Log(fmt.Sprintf("legacy applied access action %d with empty digest rejected for verified replay", legacy.ID))
}
