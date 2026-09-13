//go:build candidate_scope

package integration

import (
	"context"
	"database/sql"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	entsql "entgo.io/ent/dialect/sql"
	"github.com/google/uuid"
	_ "github.com/lib/pq"
	"github.com/stretchr/testify/require"
	"itsm-backend/common/executionscope"
	"itsm-backend/common/tenantctx"
	"itsm-backend/config"
	"itsm-backend/database"
	"itsm-backend/database/rls"
	"itsm-backend/ent"
	"itsm-backend/migration"
)

func TestCandidateScopeRegistration(t *testing.T) {
	socket := os.Getenv("CANDIDATE_SCOPE_TEST_SOCKET")
	if socket == "" {
		t.Skip("requires an explicitly isolated candidate test PostgreSQL socket")
	}
	require.True(t, filepath.IsAbs(socket), "require explicit private socket directory")
	marker, err := os.ReadFile(filepath.Join(socket, "candidate-test-instance"))
	require.NoError(t, err, "refuse a database without an explicit isolated-test marker")
	require.Equal(t, "itsm-candidate-isolated-test\n", string(marker))
	id := "candidate_scope_" + strings.ReplaceAll(uuid.NewString(), "-", "")
	dsn := func(db, role string) string {
		return fmt.Sprintf("host=%s port=25439 dbname=%s user=%s sslmode=disable", socket, db, role)
	}
	admin, err := sql.Open("postgres", dsn("postgres", "candidate_test_owner"))
	require.NoError(t, err)
	t.Cleanup(func() { require.NoError(t, admin.Close()) })
	_, err = admin.Exec("CREATE DATABASE " + id)
	require.NoError(t, err)
	runtimeRole, unboundRole := id+"_run", id+"_none"
	_, err = admin.Exec("CREATE ROLE " + runtimeRole + " LOGIN; CREATE ROLE " + unboundRole + " LOGIN")
	require.NoError(t, err)
	t.Cleanup(func() {
		_, err := admin.Exec("DROP DATABASE " + id + " WITH (FORCE)")
		require.NoError(t, err)
		_, err = admin.Exec("DROP ROLE " + runtimeRole + "; DROP ROLE " + unboundRole)
		require.NoError(t, err)
	})
	owner, err := sql.Open("postgres", dsn(id, "candidate_test_owner"))
	require.NoError(t, err)
	t.Cleanup(func() { require.NoError(t, owner.Close()) })
	_, err = owner.Exec(`CREATE TABLE tenants(id bigint PRIMARY KEY); INSERT INTO tenants VALUES (1),(2);
CREATE TABLE users(id bigint PRIMARY KEY); INSERT INTO users VALUES (1);
CREATE TABLE tickets(id bigint GENERATED ALWAYS AS IDENTITY PRIMARY KEY, tenant_id bigint NOT NULL REFERENCES tenants, title text NOT NULL);
INSERT INTO tickets(tenant_id,title) VALUES(1,'protected history');
CREATE TABLE professional_extensions(work_item_id bigint PRIMARY KEY REFERENCES tickets, valid boolean CHECK(valid));
CREATE TABLE outbox_events(id bigint PRIMARY KEY); INSERT INTO outbox_events VALUES(1);
CREATE TABLE process_instances(id bigint PRIMARY KEY); INSERT INTO process_instances VALUES(1);`)
	require.NoError(t, err)
	_, err = owner.Exec(fmt.Sprintf(`ALTER DEFAULT PRIVILEGES GRANT ALL ON TABLES TO %s;
ALTER DEFAULT PRIVILEGES GRANT EXECUTE ON FUNCTIONS TO %s`, runtimeRole, runtimeRole))
	require.NoError(t, err)
	_, err = owner.Exec(migration.GetMigrationSQL("039_candidate_execution_scope"))
	require.NoError(t, err)
	scope := executionscope.Ref{DeploymentID: "candidate-20260912", ScopeID: uuid.NewString(), TenantID: 1}
	_, err = owner.Exec(`INSERT INTO execution_scopes(id,deployment_id,tenant_id,status,created_by) VALUES($1,$2,1,'active',1)`, scope.ScopeID, scope.DeploymentID)
	require.NoError(t, err)
	_, err = owner.Exec(`INSERT INTO execution_runtime_bindings(runtime_role,deployment_id,mode) VALUES($1,$2,'candidate')`, runtimeRole, scope.DeploymentID)
	require.NoError(t, err)
	_, err = owner.Exec(fmt.Sprintf(`GRANT USAGE ON SCHEMA public TO %s,%s;
GRANT SELECT,INSERT ON tickets,professional_extensions TO %s,%s;
GRANT USAGE,SELECT ON ALL SEQUENCES IN SCHEMA public TO %s,%s;
GRANT SELECT ON execution_scopes,execution_scope_members,execution_runtime_bindings TO %s,%s`, runtimeRole, unboundRole, runtimeRole, unboundRole, runtimeRole, unboundRole, runtimeRole, unboundRole))
	require.NoError(t, err)
	run, err := sql.Open("postgres", dsn(id, runtimeRole))
	require.NoError(t, err)
	t.Cleanup(func() { require.NoError(t, run.Close()) })
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	ctx = tenantctx.WithTenantID(ctx, 1)
	t.Run("structured execution references are nullable foreign keys and immutable", func(t *testing.T) {
		for _, table := range []string{"outbox_events", "process_instances"} {
			var reference sql.NullInt64
			require.NoError(t, owner.QueryRow("SELECT execution_work_item_id FROM "+table+" WHERE id=1").Scan(&reference))
			require.False(t, reference.Valid)
			_, err := owner.Exec("INSERT INTO " + table + "(id,execution_work_item_id) VALUES(2,1)")
			require.NoError(t, err)
			_, err = owner.Exec("INSERT INTO " + table + "(id,execution_work_item_id) VALUES(3,999999)")
			require.Error(t, err)
			_, err = owner.Exec("UPDATE " + table + " SET execution_work_item_id=NULL WHERE id=2")
			require.ErrorContains(t, err, "immutable")
			_, err = owner.Exec("UPDATE " + table + " SET execution_work_item_id=1 WHERE id=1")
			require.ErrorContains(t, err, "immutable")
		}
	})
	for _, mode := range []rls.Mode{rls.ModeOff, rls.ModeEnforce} {
		t.Run("Ent scope original transaction and rollback/"+string(mode), func(t *testing.T) {
			client := ent.NewClient(ent.Driver(rls.NewDriver(entsql.OpenDB("postgres", run), mode, nil)))
			tx, err := client.Tx(ctx)
			require.NoError(t, err)
			defer tx.Rollback()
			require.ErrorIs(t, database.BindEntExecutionScope(nil, tx, scope), executionscope.ErrDenied)
			require.NoError(t, database.BindEntExecutionScope(ctx, tx, scope))
			rows, err := tx.Client().QueryContext(ctx, `INSERT INTO tickets(tenant_id,title) VALUES(1,'Ent atomic scope') RETURNING id`)
			require.NoError(t, err)
			require.True(t, rows.Next())
			var workID int
			require.NoError(t, rows.Scan(&workID))
			require.NoError(t, rows.Close())
			require.NoError(t, database.RequireEntExecutionMember(ctx, tx, scope, workID))
			if mode == rls.ModeEnforce {
				otherCtx := tenantctx.WithTenantID(ctx, 2)
				otherScope := scope
				otherScope.TenantID = 2
				require.ErrorContains(t, database.BindEntExecutionScope(otherCtx, tx, otherScope), "transaction scope cannot change")
				require.ErrorContains(t, database.RequireEntExecutionMember(otherCtx, tx, otherScope, workID), "transaction scope cannot change")
				require.NoError(t, database.RequireEntExecutionMember(ctx, tx, scope, workID), "rejected context switch must retain original transaction")
			}
			require.ErrorIs(t, database.RequireEntExecutionMember(ctx, tx, scope, 1), executionscope.ErrDenied)
			var visible int
			require.NoError(t, owner.QueryRow(`SELECT count(*) FROM tickets WHERE id=$1`, workID).Scan(&visible))
			require.Zero(t, visible)
			require.NoError(t, tx.Rollback())
			require.NoError(t, owner.QueryRow(`SELECT count(*) FROM execution_scope_members WHERE work_item_id=$1`, workID).Scan(&visible))
			require.Zero(t, visible)
			require.Error(t, database.BindEntExecutionScope(ctx, tx, scope), "closed transaction must not open another transaction")
			require.Error(t, database.BindEntExecutionScope(ctx, nil, scope))
		})
	}
	t.Run("runtime admission checks role and configured scopes", func(t *testing.T) {
		cfg := config.ExecutionConfig{Mode: "candidate", DeploymentID: scope.DeploymentID, Scopes: []config.ExecutionScopeConfig{{TenantID: scope.TenantID, ScopeID: scope.ScopeID}}}
		require.NoError(t, database.ValidateExecutionRuntime(ctx, run, cfg))
		require.Error(t, database.ValidateExecutionRuntime(ctx, owner, cfg), "owner must not be admitted as runtime")
		cfg.DeploymentID = "other"
		require.Error(t, database.ValidateExecutionRuntime(ctx, run, cfg))
	})
	t.Run("custom default grants do not leak dangerous privileges", func(t *testing.T) {
		var tableWrite, functionExecute bool
		require.NoError(t, run.QueryRow(`SELECT has_table_privilege(current_user,'execution_scope_members','TRUNCATE'),
has_function_privilege(current_user,'public.register_new_execution_member()','EXECUTE')`).Scan(&tableWrite, &functionExecute))
		require.False(t, tableWrite)
		require.False(t, functionExecute)
		require.NoError(t, run.QueryRow(`SELECT has_function_privilege(current_user,'public.preserve_execution_work_item_reference()','EXECUTE')`).Scan(&functionExecute))
		require.False(t, functionExecute)
	})
	t.Run("direct historical enrollment denied", func(t *testing.T) {
		_, err := run.Exec(`INSERT INTO execution_scope_members(scope_id,work_item_id) VALUES($1,1)`, scope.ScopeID)
		require.ErrorContains(t, err, "permission denied")
	})
	t.Run("new record and member commit together", func(t *testing.T) {
		tx, err := run.BeginTx(ctx, nil)
		require.NoError(t, err)
		defer tx.Rollback()
		require.NoError(t, database.BindExecutionScope(ctx, tx, scope))
		var workID int
		require.NoError(t, tx.QueryRowContext(ctx, `INSERT INTO tickets(tenant_id,title) VALUES(1,'new') RETURNING id`).Scan(&workID))
		require.NoError(t, database.RequireExecutionMember(ctx, tx, scope, workID))
		require.Error(t, database.RequireExecutionMember(ctx, tx, scope, 1))
		require.NoError(t, tx.Commit())
	})
	t.Run("extension failure rolls back all new records", func(t *testing.T) {
		var before int
		require.NoError(t, owner.QueryRow(`SELECT count(*) FROM tickets`).Scan(&before))
		tx, err := run.BeginTx(ctx, nil)
		require.NoError(t, err)
		require.NoError(t, database.BindExecutionScope(ctx, tx, scope))
		var workID int
		require.NoError(t, tx.QueryRowContext(ctx, `INSERT INTO tickets(tenant_id,title) VALUES(1,'rollback') RETURNING id`).Scan(&workID))
		_, err = tx.ExecContext(ctx, `INSERT INTO professional_extensions VALUES($1,false)`, workID)
		require.Error(t, err)
		require.NoError(t, tx.Rollback())
		var after, members int
		require.NoError(t, owner.QueryRow(`SELECT count(*) FROM tickets`).Scan(&after))
		require.Equal(t, before, after)
		require.NoError(t, owner.QueryRow(`SELECT count(*) FROM execution_scope_members WHERE work_item_id=$1`, workID).Scan(&members))
		require.Zero(t, members)
	})
	t.Run("missing binding cannot select standard mode", func(t *testing.T) {
		other, err := sql.Open("postgres", dsn(id, unboundRole))
		require.NoError(t, err)
		defer other.Close()
		_, err = other.Exec(`INSERT INTO tickets(tenant_id,title) VALUES(1,'unbound')`)
		require.Error(t, err)
	})
	t.Run("missing local scope denies insert", func(t *testing.T) {
		_, err := run.Exec(`INSERT INTO tickets(tenant_id,title) VALUES(1,'no scope')`)
		require.Error(t, err)
	})
	t.Run("bound scope cannot insert another tenant", func(t *testing.T) {
		tx, err := run.BeginTx(ctx, nil)
		require.NoError(t, err)
		defer tx.Rollback()
		require.NoError(t, database.BindExecutionScope(ctx, tx, scope))
		_, err = tx.ExecContext(ctx, `INSERT INTO tickets(tenant_id,title) VALUES(2,'wrong tenant')`)
		require.Error(t, err)
	})
	t.Run("runtime cannot modify enrollment or configuration", func(t *testing.T) {
		for _, query := range []string{
			`UPDATE execution_scope_members SET registered_by=session_user`,
			`DELETE FROM execution_scope_members`,
			`UPDATE execution_scopes SET status='closed'`,
			`UPDATE execution_runtime_bindings SET mode='standard'`,
			`SELECT public.register_new_execution_member()`,
		} {
			_, err := run.Exec(query)
			require.Error(t, err, query)
		}
	})
	for _, name := range []string{"other tenant", "other deployment", "closed scope", "system context"} {
		t.Run(name, func(t *testing.T) {
			ref := scope
			testCtx := ctx
			if name == "other tenant" {
				ref.TenantID = 2
			}
			if name == "other deployment" {
				ref.DeploymentID = "other"
			}
			if name == "system context" {
				testCtx = tenantctx.WithSystemBypass(ctx)
			}
			if name == "closed scope" {
				_, err := owner.Exec(`UPDATE execution_scopes SET status='closed' WHERE id=$1`, scope.ScopeID)
				require.NoError(t, err)
			}
			tx, err := run.BeginTx(testCtx, nil)
			require.NoError(t, err)
			defer tx.Rollback()
			require.Error(t, database.BindExecutionScope(testCtx, tx, ref))
		})
	}
	var title string
	require.NoError(t, owner.QueryRow(`SELECT title FROM tickets WHERE id=1`).Scan(&title))
	require.Equal(t, "protected history", title)
	var historicalMembers int
	require.NoError(t, owner.QueryRow(`SELECT count(*) FROM execution_scope_members WHERE work_item_id=1`).Scan(&historicalMembers))
	require.Zero(t, historicalMembers)
}
