//go:build integration_postgres

package integration

import (
	"context"
	"database/sql"
	_ "github.com/lib/pq"
	"github.com/stretchr/testify/require"
	"go.uber.org/zap"
	"go.uber.org/zap/zaptest/observer"
	"itsm-backend/dto"
	"itsm-backend/service"
	"net/url"
	"os"
	"testing"
)

// RCA_POSTGRES_TEST_DSN must point to a disposable, isolated PostgreSQL database.
// This test owns only its transactionally-created fixture schema, never public tables.
func TestRCAAuthorityPostgres(t *testing.T) {
	dsn := os.Getenv("RCA_POSTGRES_TEST_DSN")
	if dsn == "" {
		t.Skip("requires isolated RCA_POSTGRES_TEST_DSN")
	}
	db, err := sql.Open("postgres", dsn)
	require.NoError(t, err)
	defer db.Close()
	db.SetMaxOpenConns(1)
	_, err = db.Exec(`CREATE SCHEMA rca_service_test; SET search_path TO rca_service_test;
 CREATE TABLE tickets(id bigint PRIMARY KEY,tenant_id bigint,title text,deleted_at timestamptz,version int DEFAULT 1,updated_at timestamptz);
 CREATE TABLE users(id bigint PRIMARY KEY,tenant_id bigint,name text);
 CREATE TABLE problems(id bigint PRIMARY KEY,work_item_id bigint,root_cause text);
 INSERT INTO tickets(id,tenant_id,title) VALUES(1,10,'source');
 INSERT INTO users VALUES(1,10,'analyst'); INSERT INTO problems VALUES(1,1,'old');`)
	require.NoError(t, err)
	defer db.Exec(`DROP SCHEMA rca_service_test CASCADE`)
	migration, err := os.ReadFile("../../migrations/20260909_problem_rca_authority.sql")
	require.NoError(t, err)
	_, err = db.Exec(string(migration))
	require.NoError(t, err)
	_, err = db.Exec(`CREATE ROLE rca_runtime_test LOGIN; GRANT USAGE ON SCHEMA rca_service_test TO rca_runtime_test;
 GRANT SELECT,INSERT,UPDATE,DELETE ON ALL TABLES IN SCHEMA rca_service_test TO rca_runtime_test;
 GRANT USAGE,SELECT ON ALL SEQUENCES IN SCHEMA rca_service_test TO rca_runtime_test;
 ALTER TABLE tickets ENABLE ROW LEVEL SECURITY;
 CREATE POLICY tenant ON tickets USING(tenant_id=NULLIF(current_setting('app.current_tenant',true),'')::bigint);
 ALTER TABLE users ENABLE ROW LEVEL SECURITY;
 CREATE POLICY tenant ON users USING(tenant_id=NULLIF(current_setting('app.current_tenant',true),'')::bigint);
 ALTER TABLE problems ENABLE ROW LEVEL SECURITY;
 CREATE POLICY tenant ON problems USING(EXISTS(SELECT 1 FROM tickets wi WHERE wi.id=problems.work_item_id));`)
	require.NoError(t, err)
	defer db.Exec(`DROP OWNED BY rca_runtime_test; DROP ROLE rca_runtime_test`)
	runtimeURL, err := url.Parse(dsn)
	require.NoError(t, err)
	runtimeURL.User = url.User("rca_runtime_test")
	params := runtimeURL.Query()
	params.Set("search_path", "rca_service_test")
	runtimeURL.RawQuery = params.Encode()
	runtimeDB, err := sql.Open("postgres", runtimeURL.String())
	require.NoError(t, err)
	defer runtimeDB.Close()
	runtimeDB.SetMaxOpenConns(1)
	errorCore, errorLogs := observer.New(zap.ErrorLevel)
	svc := service.NewTenantScopedProblemInvestigationService(runtimeDB, zap.New(errorCore).Sugar())
	created, err := svc.CreateRootCauseAnalysis(context.Background(), &dto.CreateRootCauseAnalysisRequest{ProblemID: 1, AnalystID: 1, AnalysisMethod: "5_whys", RootCauseDescription: "postgres root", ConfidenceLevel: dto.ConfidenceHigh}, 10)
	require.NoError(t, err)
	require.Equal(t, "postgres root", created.RootCauseDescription)
	root := "updated postgres root"
	updated, err := svc.UpdateRootCauseAnalysis(context.Background(), created.ID, &dto.UpdateRootCauseAnalysisRequest{RootCauseDescription: &root}, 10)
	require.NoError(t, err)
	require.Equal(t, root, updated.RootCauseDescription)
	var stored string
	require.NoError(t, db.QueryRow(`SELECT root_cause FROM problems WHERE id=1`).Scan(&stored))
	require.Equal(t, root, stored)
	read, err := svc.GetRootCauseAnalysis(context.Background(), created.ID, 10)
	require.NoError(t, err)
	require.Equal(t, root, read.RootCauseDescription)
	_, err = svc.GetRootCauseAnalysis(context.Background(), created.ID, 20)
	require.Error(t, err)
	_, err = svc.UpdateRootCauseAnalysis(context.Background(), created.ID, &dto.UpdateRootCauseAnalysisRequest{RootCauseDescription: &root}, 20)
	require.Error(t, err)
	summary, err := svc.GetProblemInvestigationSummary(context.Background(), 1, 10)
	require.NoError(t, err)
	require.NotNil(t, summary.RootCauseAnalysis)
	require.Equal(t, root, summary.RootCauseAnalysis.RootCauseDescription)
	var leaked string
	require.NoError(t, runtimeDB.QueryRow(`SELECT COALESCE(current_setting('app.current_tenant',true),'')`).Scan(&leaked))
	require.Empty(t, leaked, "connection release must clear tenant state")
	require.Empty(t, errorLogs.All(), "scoped reads must release a usable connection")
	var unscopedRows int
	require.NoError(t, runtimeDB.QueryRow(`SELECT count(*) FROM problem_root_cause_analyses`).Scan(&unscopedRows))
	require.Zero(t, unscopedRows)
	require.NoError(t, svc.DeleteRootCauseAnalysis(context.Background(), created.ID, 10))
	require.NoError(t, db.QueryRow(`SELECT root_cause FROM problems WHERE id=1`).Scan(&stored))
	require.Equal(t, root, stored)
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
