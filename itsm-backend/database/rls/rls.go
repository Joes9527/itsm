// Package rls owns physical SQL connection checkout and safe tenant cleanup.
// Both the Ent decorator and direct SQL callers use common/tenantctx.
// A session setting stays on the checked-out connection only; release resets
// the session or evicts the physical connection if cleanup fails.
package rls

import (
	"context"
	"database/sql"
	"database/sql/driver"
	"errors"
	"fmt"
	"strconv"
	"time"

	"itsm-backend/common/tenantctx"
)

// ErrNoTenant is returned when a query is attempted without a tenant scope
// and without SystemBypass. Callers must handle this before hitting the DB.
var ErrNoTenant = errors.New("rls: no tenant_id in context and system bypass not set")

const releaseCleanupTimeout = 5 * time.Second

// AcquireConn checks out a connection from db and sets the SESSION variable
// app.current_tenant to the tenant_id carried by ctx. The returned *sql.Conn
// MUST be released with ReleaseConn to prevent variable leakage.
//
// If ctx carries SystemBypass, no variable is set. Callers using this branch
// are expected to have already routed through the BYPASSRLS pool.
func AcquireConn(ctx context.Context, db *sql.DB) (*sql.Conn, error) {
	conn, err := db.Conn(ctx)
	if err != nil {
		return nil, fmt.Errorf("rls: acquire conn: %w", err)
	}

	if tenantctx.IsSystemBypass(ctx) {
		return conn, nil
	}

	tid, ok := tenantctx.TenantID(ctx)
	if !ok || tid <= 0 {
		_ = conn.Close()
		return nil, ErrNoTenant
	}

	// PostgreSQL SET does not accept bind parameters. set_config is the
	// parameter-safe equivalent and false keeps the value for this session;
	// ReleaseConn's DISCARD ALL clears it before pool reuse.
	if _, err := conn.ExecContext(
		ctx,
		"SELECT set_config('app.current_tenant', $1, false)",
		strconv.Itoa(tid),
	); err != nil {
		return nil, fmt.Errorf("rls: set tenant: %w", errors.Join(err, ReleaseConn(ctx, conn)))
	}
	return conn, nil
}

// ReleaseConn resets tenant scope and returns the connection to the pool.
// If reset fails, the connection is destroyed rather than returned dirty.
func ReleaseConn(ctx context.Context, conn *sql.Conn) error {
	if conn == nil {
		return nil
	}
	// Request cancellation must not bypass session cleanup. A short independent
	// context bounds DISCARD while still allowing it to run after the request ends.
	cleanupCtx, cancel := context.WithTimeout(context.Background(), releaseCleanupTimeout)
	defer cancel()
	if _, err := conn.ExecContext(cleanupCtx, "DISCARD ALL"); err != nil {
		// Returning driver.ErrBadConn from Raw tells database/sql to destroy the
		// physical connection. Close alone would put a dirty session back in pool.
		evictionErr := conn.Raw(func(any) error { return driver.ErrBadConn })
		closeErr := conn.Close()
		if evictionErr != nil && !errors.Is(evictionErr, driver.ErrBadConn) {
			return fmt.Errorf("rls: discard on release: %w", errors.Join(err, evictionErr, closeErr))
		}
		return fmt.Errorf("rls: discard on release: %w", errors.Join(err, closeErr))
	}
	return conn.Close() // Close on *sql.Conn returns it to the pool
}
