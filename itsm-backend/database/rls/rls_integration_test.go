//go:build integration_rls

// Package rls integration tests.
//
// Run with:
//
//	RLS_TEST_DSN='host=<pg_host> port=5432 user=itsm_user dbname=itsm sslmode=disable password=<pwd>' \
//	  go test -tags integration_rls -v ./database/rls/...
//
// Prerequisites:
//   - itsm_app / itsm_admin roles exist (see migrations/001_roles.sql)
//   - `changes` table has rows for at least tenant_id=1
//   - The connecting DB is the SAME one you migrated (see caveat below)
//
// Caveat on host environments:
//
//	If your macOS/Linux host runs a local PostgreSQL (Homebrew, apt) that
//	binds :5432, it may hijack `localhost` connections and route them to
//	the wrong server. Use one of:
//	  - RLS_TEST_DSN with `host=<container_ip>` when Docker network is
//	    reachable from host (Linux, or Docker Desktop w/ direct routes)
//	  - Run the test INSIDE the container (docker exec ... go test ...)
//	  - Stop the local PostgreSQL service and rely on Docker-published :5432
//
// The test enables policy at setup and disables at teardown, so running
// it repeatedly is safe. It does NOT mutate business data other than
// its own probes, which are cleaned up.
package rls

import (
	"context"
	"database/sql"
	"fmt"
	"os"
	"testing"
	"time"

	_ "github.com/lib/pq"
)

func openTestDB(t *testing.T) *sql.DB {
	t.Helper()
	dsn := os.Getenv("RLS_TEST_DSN")
	if dsn == "" {
		t.Skip("RLS_TEST_DSN not set, skipping RLS integration test")
	}
	db, err := sql.Open("postgres", dsn)
	if err != nil {
		t.Fatalf("open db: %v", err)
	}
	if err := db.Ping(); err != nil {
		t.Fatalf("ping db: %v", err)
	}
	return db
}

// setupPolicy enables RLS + policy on `changes`, using the NULLIF-safe form.
// Returns a teardown func that restores the table to no-RLS state.
func setupPolicy(t *testing.T, db *sql.DB) func() {
	t.Helper()
	ctx := context.Background()
	stmts := []string{
		`ALTER TABLE changes ENABLE ROW LEVEL SECURITY`,
		`ALTER TABLE changes FORCE ROW LEVEL SECURITY`,
		`DROP POLICY IF EXISTS tenant_isolation ON changes`,
		`CREATE POLICY tenant_isolation ON changes
			USING       (tenant_id = NULLIF(current_setting('app.current_tenant', true), '')::bigint)
			WITH CHECK  (tenant_id = NULLIF(current_setting('app.current_tenant', true), '')::bigint)`,
	}
	for _, s := range stmts {
		if _, err := db.ExecContext(ctx, s); err != nil {
			t.Fatalf("setup policy (%q): %v", s, err)
		}
	}
	return func() {
		teardown := []string{
			`DROP POLICY IF EXISTS tenant_isolation ON changes`,
			`ALTER TABLE changes NO FORCE ROW LEVEL SECURITY`,
			`ALTER TABLE changes DISABLE ROW LEVEL SECURITY`,
		}
		for _, s := range teardown {
			_, _ = db.ExecContext(ctx, s)
		}
	}
}

// countChangesAs runs SELECT COUNT(*) as itsm_app role after AcquireConn.
// This is the real end-to-end path: no manual SET, package handles it.
func countChangesAs(t *testing.T, db *sql.DB, ctx context.Context) int {
	t.Helper()
	conn, err := AcquireConn(ctx, db)
	if err != nil {
		t.Fatalf("acquire conn: %v", err)
	}
	defer func() {
		if err := ReleaseConn(ctx, conn); err != nil {
			t.Logf("release: %v (non-fatal)", err)
		}
	}()

	// Switch to non-superuser role so policy actually applies.
	if _, err := conn.ExecContext(ctx, "SET ROLE itsm_app"); err != nil {
		t.Fatalf("set role: %v", err)
	}

	var n int
	if err := conn.QueryRowContext(ctx, "SELECT COUNT(*) FROM changes").Scan(&n); err != nil {
		t.Fatalf("count: %v", err)
	}
	return n
}

// TestAcquireConn_TenantScopeIsolation verifies that a request-scoped
// AcquireConn correctly limits visible rows to the given tenant.
func TestAcquireConn_TenantScopeIsolation(t *testing.T) {
	db := openTestDB(t)
	defer db.Close()
	teardown := setupPolicy(t, db)
	defer teardown()

	// tenant=1 must see > 0 rows (assumes dev DB has changes for tenant 1)
	ctx1 := WithTenant(context.Background(), 1)
	n1 := countChangesAs(t, db, ctx1)
	if n1 == 0 {
		t.Fatalf("tenant 1 saw 0 rows; dev DB may be missing seed data")
	}

	// tenant=999 must see 0 rows (unless someone seeded it, which is a bug)
	ctx999 := WithTenant(context.Background(), 999)
	n999 := countChangesAs(t, db, ctx999)
	if n999 != 0 {
		t.Fatalf("tenant 999 saw %d rows; expected 0 (RLS bypassed?)", n999)
	}

	t.Logf("tenant=1 visible=%d, tenant=999 visible=%d ✓", n1, n999)
}

func TestKafExecutionIntegrityTablesRejectCrossTenantRows(t *testing.T) {
	db := openTestDB(t)
	defer db.Close()
	for _, table := range []string{"kaf_task_action_ledgers", "kaf_task_completion_receipts"} {
		ctx := WithTenant(context.Background(), 999999)
		conn, err := AcquireConn(ctx, db)
		if err != nil {
			t.Fatalf("acquire %s connection: %v", table, err)
		}
		if _, err := conn.ExecContext(ctx, "SET ROLE itsm_app"); err != nil {
			_ = ReleaseConn(ctx, conn)
			t.Fatalf("set role for %s: %v", table, err)
		}
		var crossTenantRows int
		query := "SELECT COUNT(*) FROM " + table + " WHERE tenant_id <> 999999"
		if err := conn.QueryRowContext(ctx, query).Scan(&crossTenantRows); err != nil {
			_ = ReleaseConn(ctx, conn)
			t.Fatalf("query %s through RLS: %v", table, err)
		}
		if err := ReleaseConn(ctx, conn); err != nil {
			t.Fatalf("release %s connection: %v", table, err)
		}
		if crossTenantRows != 0 {
			t.Fatalf("%s exposed %d cross-tenant rows", table, crossTenantRows)
		}
	}
}

func execIntakeProbeAsTenant(t *testing.T, db *sql.DB, tenantID int64, query string, args ...any) {
	t.Helper()
	ctx := WithTenant(context.Background(), tenantID)
	conn, err := AcquireConn(ctx, db)
	if err != nil {
		t.Fatalf("acquire tenant %d connection: %v", tenantID, err)
	}
	defer func() {
		if err := ReleaseConn(ctx, conn); err != nil {
			t.Fatalf("release tenant %d connection: %v", tenantID, err)
		}
	}()
	if _, err := conn.ExecContext(ctx, "SET ROLE itsm_app"); err != nil {
		t.Fatalf("set tenant %d role: %v", tenantID, err)
	}
	if _, err := conn.ExecContext(ctx, query, args...); err != nil {
		t.Fatalf("execute tenant %d intake probe: %v", tenantID, err)
	}
}

func countIntakeProbeAsTenant(t *testing.T, db *sql.DB, tenantID int64, query string, args ...any) int {
	t.Helper()
	ctx := WithTenant(context.Background(), tenantID)
	conn, err := AcquireConn(ctx, db)
	if err != nil {
		t.Fatalf("acquire tenant %d connection: %v", tenantID, err)
	}
	defer func() {
		if err := ReleaseConn(ctx, conn); err != nil {
			t.Fatalf("release tenant %d connection: %v", tenantID, err)
		}
	}()
	if _, err := conn.ExecContext(ctx, "SET ROLE itsm_app"); err != nil {
		t.Fatalf("set tenant %d role: %v", tenantID, err)
	}
	var count int
	if err := conn.QueryRowContext(ctx, query, args...).Scan(&count); err != nil {
		t.Fatalf("count tenant %d intake probe: %v", tenantID, err)
	}
	return count
}

func TestUnifiedIntakeTablesIsolateTenantRows(t *testing.T) {
	db := openTestDB(t)
	defer db.Close()

	var tenantA, tenantB int64 = 910001, 910002
	nonce := time.Now().UnixNano()
	marker := fmt.Sprintf("rls-intake-%d", nonce)
	baseID := 1_500_000_000 + nonce%100_000_000

	for offset, tenantID := range []int64{tenantA, tenantB} {
		rowID := baseID + int64(offset*10)
		execIntakeProbeAsTenant(t, db, tenantID, `
			INSERT INTO intake_requests
				(id, tenant_id, actor_id, channel, operation, idempotency_key, request_digest, digest_version, status)
			VALUES ($1, $2, 1, 'rls_test', 'create', $3, $4, 'v1', 'pending')`,
			rowID, tenantID, fmt.Sprintf("%s-%d", marker, tenantID), marker)
		execIntakeProbeAsTenant(t, db, tenantID, `
			INSERT INTO intake_resolution_snapshots
				(id, tenant_id, intake_request_id, work_item_id, channel, source_provider, record_class, ci_ids, no_process, resolver_version, request_digest)
			VALUES ($1, $2, $3, $4, 'rls_test', 'integration', 'incident', '[]'::jsonb, true, 'v1', $5)`,
			rowID+1, tenantID, rowID, rowID, marker)
		execIntakeProbeAsTenant(t, db, tenantID, `
			INSERT INTO external_identities
				(id, tenant_id, provider, workspace, subject, user_id, active)
			VALUES ($1, $2, $3, 'rls-test', $4, 1, true)`,
			rowID+2, tenantID, marker, fmt.Sprintf("subject-%d", tenantID))
	}

	defer func() {
		for _, tenantID := range []int64{tenantA, tenantB} {
			execIntakeProbeAsTenant(t, db, tenantID, "DELETE FROM external_identities WHERE provider = $1", marker)
			execIntakeProbeAsTenant(t, db, tenantID, "DELETE FROM intake_resolution_snapshots WHERE request_digest = $1", marker)
			execIntakeProbeAsTenant(t, db, tenantID, "DELETE FROM intake_requests WHERE request_digest = $1", marker)
		}
	}()

	for _, probe := range []struct {
		name  string
		query string
	}{
		{name: "intake_requests", query: "SELECT COUNT(*) FROM intake_requests WHERE request_digest = $1"},
		{name: "intake_resolution_snapshots", query: "SELECT COUNT(*) FROM intake_resolution_snapshots WHERE request_digest = $1"},
		{name: "external_identities", query: "SELECT COUNT(*) FROM external_identities WHERE provider = $1"},
	} {
		t.Run(probe.name, func(t *testing.T) {
			if got := countIntakeProbeAsTenant(t, db, tenantA, probe.query, marker); got != 1 {
				t.Fatalf("tenant A saw %d %s rows, want exactly its own row", got, probe.name)
			}
			if got := countIntakeProbeAsTenant(t, db, tenantB, probe.query, marker); got != 1 {
				t.Fatalf("tenant B saw %d %s rows, want exactly its own row", got, probe.name)
			}
		})
	}
}

// TestAcquireConn_NoTenantRejected ensures ErrNoTenant surfaces when
// caller forgets to set tenant scope AND bypass is not asserted.
func TestAcquireConn_NoTenantRejected(t *testing.T) {
	db := openTestDB(t)
	defer db.Close()

	_, err := AcquireConn(context.Background(), db)
	if err != ErrNoTenant {
		t.Fatalf("expected ErrNoTenant, got %v", err)
	}
}

// TestAcquireConn_SystemBypassSkipsSet verifies that WithSystemBypass
// grants access without setting the tenant variable. Because BYPASSRLS
// on the connecting role is required for actual data visibility, this
// test only proves the SET is skipped (no error) — real bypass depends
// on connecting as itsm_admin, which is out of scope here.
func TestAcquireConn_SystemBypassSkipsSet(t *testing.T) {
	db := openTestDB(t)
	defer db.Close()

	ctx := WithSystemBypass(context.Background())
	conn, err := AcquireConn(ctx, db)
	if err != nil {
		t.Fatalf("bypass acquire should not error: %v", err)
	}
	if err := ReleaseConn(ctx, conn); err != nil {
		t.Fatalf("release: %v", err)
	}
}

// TestReleaseConn_DiscardsSessionState verifies that ReleaseConn actually
// wipes the tenant variable, so the next borrower of the pooled connection
// cannot inherit the previous tenant.
func TestReleaseConn_DiscardsSessionState(t *testing.T) {
	db := openTestDB(t)
	defer db.Close()
	teardown := setupPolicy(t, db)
	defer teardown()

	// Force a tiny pool so we deterministically reuse the same underlying
	// physical connection across two borrows.
	db.SetMaxOpenConns(1)
	db.SetMaxIdleConns(1)

	// First borrow: tenant=1
	ctx1 := WithTenant(context.Background(), 1)
	conn1, err := AcquireConn(ctx1, db)
	if err != nil {
		t.Fatalf("acquire 1: %v", err)
	}
	// Read within same borrow to make sure SET took effect
	var tid1 sql.NullString
	if err := conn1.QueryRowContext(ctx1,
		"SELECT current_setting('app.current_tenant', true)").Scan(&tid1); err != nil {
		t.Fatalf("read var: %v", err)
	}
	if tid1.String != "1" {
		t.Fatalf("expected tenant var '1', got %q", tid1.String)
	}
	if err := ReleaseConn(ctx1, conn1); err != nil {
		t.Fatalf("release 1: %v", err)
	}

	// Second borrow: on the SAME physical conn (pool size = 1). If DISCARD
	// ALL worked, the variable must be empty now. We bypass RLS setup
	// deliberately (use db.Conn directly) to inspect raw state.
	conn2, err := db.Conn(context.Background())
	if err != nil {
		t.Fatalf("raw conn: %v", err)
	}
	defer conn2.Close()

	var tid2 sql.NullString
	if err := conn2.QueryRowContext(context.Background(),
		"SELECT current_setting('app.current_tenant', true)").Scan(&tid2); err != nil {
		t.Fatalf("read var 2: %v", err)
	}
	if tid2.String != "" {
		t.Fatalf("session state leaked across borrows: got %q, want empty", tid2.String)
	}
	t.Logf("session state correctly cleared after ReleaseConn ✓")
}
