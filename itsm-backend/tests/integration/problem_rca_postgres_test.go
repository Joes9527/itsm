//go:build integration_postgres

package integration

import (
	"context"
	"database/sql"
	_ "github.com/lib/pq"
	"github.com/stretchr/testify/require"
	"go.uber.org/zap"
	"go.uber.org/zap/zaptest/observer"
	"itsm-backend/common/tenantctx"
	"itsm-backend/dto"
	"itsm-backend/ent"
	problem "itsm-backend/handlers/problem"
	"itsm-backend/service"
	"os"
	"testing"
)

// RCA_POSTGRES_TEST_DSN must point to a disposable, isolated PostgreSQL database.
// This test owns only its transactionally-created fixture schema, never public tables.
func TestRCAAuthorityPostgres(t *testing.T) {
	f := newProblemLifecycleFixture(t)
	driver, runtimeDB := runtimeRLSDriver(t, f.incidentEffectsFixture)
	var role, schema string
	require.NoError(t, runtimeDB.QueryRowContext(f.ctx, "SELECT current_user,current_schema()").Scan(&role, &schema))
	_, err := f.db.ExecContext(f.ctx, "GRANT SELECT,INSERT,UPDATE,DELETE ON ALL TABLES IN SCHEMA "+schema+" TO "+role)
	require.NoError(t, err)
	_, err = f.db.ExecContext(f.ctx, "GRANT USAGE ON ALL SEQUENCES IN SCHEMA "+schema+" TO "+role)
	require.NoError(t, err)
	scoped := ent.NewClient(ent.Driver(driver))
	f.owner = problem.NewService(problem.NewEntRepository(scoped), zap.NewNop().Sugar())
	f.ctx = tenantctx.WithTenantID(f.ctx, f.tenant.ID)
	errorCore, errorLogs := observer.New(zap.ErrorLevel)
	reader := service.NewTenantScopedProblemInvestigationService(runtimeDB, zap.New(errorCore).Sugar())
	created, err := f.createRCA(&dto.CreateRootCauseAnalysisRequest{ProblemID: f.p.ID, AnalystID: f.actor.ID, AnalysisMethod: "5_whys", RootCauseDescription: "postgres root", ConfidenceLevel: dto.ConfidenceHigh}, f.tenant.ID)
	require.NoError(t, err)
	root := "updated postgres root"
	updated, err := f.updateRCA(created.ID, &dto.UpdateRootCauseAnalysisRequest{RootCauseDescription: &root}, f.tenant.ID)
	require.NoError(t, err)
	require.Equal(t, root, updated.RootCauseDescription)
	read, err := reader.GetRootCauseAnalysis(f.ctx, created.ID, f.tenant.ID)
	require.NoError(t, err)
	require.Equal(t, root, read.RootCauseDescription)
	_, err = reader.GetRootCauseAnalysis(context.Background(), created.ID, f.tenant.ID+1000)
	require.Error(t, err)
	cmd := problem.MetadataCommand{Meta: f.command("metadata", "foreign-rca").Meta, ProblemID: f.p.ID, RootCauseAnalysis: &problem.RootCauseMetadata{ID: created.ID, Update: &dto.UpdateRootCauseAnalysisRequest{RootCauseDescription: &root}}}
	cmd.Meta.TenantID += 1000
	_, err = f.owner.ApplyMetadata(context.Background(), cmd)
	require.Error(t, err)
	summary, err := reader.GetProblemInvestigationSummary(f.ctx, f.p.ID, f.tenant.ID)
	require.NoError(t, err)
	require.NotNil(t, summary.RootCauseAnalysis)
	require.Equal(t, root, summary.RootCauseAnalysis.RootCauseDescription)
	var leaked string
	require.NoError(t, runtimeDB.QueryRow("SELECT COALESCE(current_setting('app.current_tenant',true),'')").Scan(&leaked))
	require.Empty(t, leaked)
	require.Empty(t, errorLogs.All())
	var unscoped int
	require.NoError(t, runtimeDB.QueryRow("SELECT count(*) FROM problem_root_cause_analyses").Scan(&unscoped))
	require.Zero(t, unscoped)
	_, err = f.owner.ApplyMetadata(f.ctx, problem.MetadataCommand{Meta: f.command("metadata", "delete-rca").Meta, ProblemID: f.p.ID, RootCauseAnalysis: &problem.RootCauseMetadata{ID: created.ID, Delete: true}})
	require.NoError(t, err)
	require.Equal(t, root, f.client.Problem.GetX(f.ctx, f.p.ID).RootCause)
}

func TestRCAMigrationPostgres(t *testing.T) {
	dsn := os.Getenv("RCA_POSTGRES_TEST_DSN")
	if dsn == "" {
		t.Skip("requires isolated RCA_POSTGRES_TEST_DSN")
	}
	db, err := sql.Open("postgres", dsn)
	require.NoError(t, err)
	defer db.Close()
	db.SetMaxOpenConns(1)
	asset, err := os.ReadFile("../../migrations/20260909_problem_rca_authority.sql")
	require.NoError(t, err)
	for _, tc := range []struct{ name, change, failure string }{
		{"backfill", "", ""},
		{"conflict", `UPDATE problems SET root_cause='conflict' WHERE id=2`, "Conflicting"},
		{"analyst", `UPDATE problem_root_cause_analyses SET analyst_id=2 WHERE id=1`, "ownership"},
		{"orphan", `UPDATE problem_root_cause_analyses SET problem_id=999 WHERE id=1`, "ownership"},
		{"duplicate", `INSERT INTO problem_root_cause_analyses VALUES(3,1,1,NULL,'historical')`, "Multiple"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			tx, err := db.Begin()
			require.NoError(t, err)
			defer tx.Rollback()
			_, err = tx.Exec(`CREATE SCHEMA rca_migration_test; SET LOCAL search_path TO rca_migration_test;
    CREATE TABLE tickets(id bigint PRIMARY KEY,tenant_id bigint,deleted_at timestamptz); CREATE TABLE users(id bigint PRIMARY KEY,tenant_id bigint);
    CREATE TABLE problems(id bigint PRIMARY KEY,work_item_id bigint,root_cause text);
    INSERT INTO tickets(id,tenant_id) VALUES(1,10),(2,20); INSERT INTO users VALUES(1,10),(2,20);
    INSERT INTO problems VALUES(1,1,''),(2,2,'canonical');
    CREATE TABLE problem_root_cause_analyses(id bigint PRIMARY KEY,problem_id bigint,analyst_id bigint,reviewed_by bigint,root_cause_description text);
    INSERT INTO problem_root_cause_analyses VALUES(1,1,1,NULL,'historical'),(2,2,2,NULL,'canonical');`)
			require.NoError(t, err)
			if tc.change != "" {
				_, err = tx.Exec(tc.change)
				require.NoError(t, err)
			}
			_, err = tx.Exec(`SAVEPOINT migration_attempt`)
			require.NoError(t, err)
			_, err = tx.Exec(string(asset))
			if tc.failure != "" {
				require.ErrorContains(t, err, tc.failure)
				_, err = tx.Exec(`ROLLBACK TO SAVEPOINT migration_attempt`)
				require.NoError(t, err)
				var root string
				require.NoError(t, tx.QueryRow(`SELECT root_cause FROM problems WHERE id=1`).Scan(&root))
				require.Empty(t, root)
				var legacy string
				require.NoError(t, tx.QueryRow(`SELECT root_cause_description FROM problem_root_cause_analyses WHERE id=1`).Scan(&legacy))
				require.Equal(t, "historical", legacy)
			} else {
				require.NoError(t, err)
				_, err = tx.Exec(string(asset))
				require.NoError(t, err)
				var root string
				require.NoError(t, tx.QueryRow(`SELECT root_cause FROM problems WHERE id=1`).Scan(&root))
				require.Equal(t, "historical", root)
				var oldColumns int
				require.NoError(t, tx.QueryRow(`SELECT count(*) FROM information_schema.columns WHERE table_schema=current_schema() AND table_name='problem_root_cause_analyses' AND column_name='root_cause_description'`).Scan(&oldColumns))
				require.Zero(t, oldColumns)
				// Built-in read-only role has no BYPASSRLS; verify policies independently of Go filters.
				_, err = tx.Exec(`SET LOCAL row_security=on; SET LOCAL ROLE pg_read_all_data; SELECT set_config('app.current_tenant','10',true)`)
				require.NoError(t, err)
				var visible int
				require.NoError(t, tx.QueryRow(`SELECT count(*) FROM problem_root_cause_analyses`).Scan(&visible))
				require.Equal(t, 1, visible)
				_, err = tx.Exec(`SELECT set_config('app.current_tenant','',true)`)
				require.NoError(t, err)
				require.NoError(t, tx.QueryRow(`SELECT count(*) FROM problem_root_cause_analyses`).Scan(&visible))
				require.Zero(t, visible)
			}
		})
	}
}
