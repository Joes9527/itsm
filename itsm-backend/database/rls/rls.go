// Package rls provides Row-Level Security context propagation for PostgreSQL.
//
// Design (from rls-migration-proposal-2026-07-18.md):
//  1. Middleware extracts tenant_id from JWT and stores it in gin.Context.
//  2. When a DB connection is acquired from the pool, we set the session-scoped
//     app.current_tenant GUC with PostgreSQL's parameter-safe set_config API.
//  3. Before the connection is returned to the pool, we RESET the variable
//     to prevent cross-request tenant leakage.
//
// Threading model: database/sql pool guarantees exclusive ownership of a
// *sql.Conn per checkout, so SESSION-level variables are safe as long as
// the acquire/release lifecycle is respected.
//
// Bypass: background workers, migrations, and MSP cross-tenant ops must
// connect via the dedicated itsm_admin role (BYPASSRLS attribute). This
// package is intentionally NOT used on those paths.
//
// Status: R0.5 skeleton — not wired into main yet. See R1 milestone.
package rls

import (
	"context"
	"database/sql"
	"database/sql/driver"
	"errors"
	"fmt"
	"strconv"
	"time"
)

// tenantCtxKey is the private type used to store tenant_id in context.Context.
type tenantCtxKey struct{}

// WithTenant returns a new context carrying the given tenant_id.
func WithTenant(ctx context.Context, tenantID int64) context.Context {
	return context.WithValue(ctx, tenantCtxKey{}, tenantID)
}

// TenantFromContext returns the tenant_id stored in ctx, or (0, false) if absent.
func TenantFromContext(ctx context.Context) (int64, bool) {
	v, ok := ctx.Value(tenantCtxKey{}).(int64)
	return v, ok
}

// systemBypassKey marks a context as authorized to bypass RLS (server jobs).
type systemBypassKey struct{}

// WithSystemBypass tags the context as system-privileged. The DB layer should
// route such calls through a BYPASSRLS role instead of the tenant-scoped pool.
func WithSystemBypass(ctx context.Context) context.Context {
	return context.WithValue(ctx, systemBypassKey{}, true)
}

// IsSystemBypass reports whether the context is authorized for cross-tenant ops.
func IsSystemBypass(ctx context.Context) bool {
	v, _ := ctx.Value(systemBypassKey{}).(bool)
	return v
}

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

	if IsSystemBypass(ctx) {
		return conn, nil
	}

	tid, ok := TenantFromContext(ctx)
	if !ok {
		_ = conn.Close()
		return nil, ErrNoTenant
	}

	// PostgreSQL SET does not accept bind parameters. set_config is the
	// parameter-safe equivalent and false keeps the value for this session;
	// ReleaseConn's DISCARD ALL clears it before pool reuse.
	if _, err := conn.ExecContext(
		ctx,
		"SELECT set_config('app.current_tenant', $1, false)",
		strconv.FormatInt(tid, 10),
	); err != nil {
		_ = conn.Close()
		return nil, fmt.Errorf("rls: set tenant: %w", err)
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

// --------------------------------------------------------------------------
// Middleware helper (Gin adapter) — pseudo-code, not wired.
// --------------------------------------------------------------------------
//
// func GinMiddleware(db *sql.DB) gin.HandlerFunc {
//     return func(c *gin.Context) {
//         tid, _ := c.Get("tenant_id")
//         ctx := WithTenant(c.Request.Context(), tid.(int64))
//         c.Request = c.Request.WithContext(ctx)
//         c.Next()
//     }
// }
//
// // In service layer:
// conn, err := rls.AcquireConn(ctx, s.rawDB)
// defer rls.ReleaseConn(ctx, conn)
// // Now Ent queries on `conn` are automatically tenant-scoped.
