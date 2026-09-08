// Package rls applies tenant scope at the physical SQL connection boundary.
// Off passes through; shadow observes context only; enforce requires a positive
// tenant or an explicit server-owned system scope. Tenant transactions use SET
// LOCAL; ordinary statements use the established connection checkout/cleanup
// lifecycle. System scope never grants database privileges or changes roles.
package rls

import (
	"context"
	"database/sql"
	"fmt"
	"sync/atomic"

	"itsm-backend/common/tenantctx"

	"entgo.io/ent/dialect"
	"go.uber.org/zap"
)

// Mode is the RLS enforcement level. Keep string values stable — they are
// serialized in config and logs.
type Mode string

const (
	ModeOff     Mode = "off"
	ModeShadow  Mode = "shadow"
	ModeEnforce Mode = "enforce"
)

// ParseMode retains unknown values so they fail closed at configuration/execution.
func ParseMode(s string) Mode {
	switch Mode(s) {
	case "", ModeOff:
		return ModeOff
	case ModeShadow:
		return ModeShadow
	case ModeEnforce:
		return ModeEnforce
	default:
		return Mode(s)
	}
}

// Driver wraps a dialect.Driver and applies RLS behavior per Mode.
// It implements dialect.Driver so callers can drop it in place of the
// underlying entsql driver.
type Driver struct {
	inner dialect.Driver
	mode  Mode
	log   *zap.SugaredLogger

	// stats: atomic counters exposed via Stats(). Cheap to update on hot path.
	nQueriesOff     atomic.Uint64
	nQueriesShadow  atomic.Uint64
	nMissingTenant  atomic.Uint64
	nSystemBypass   atomic.Uint64
	nEnforceApplied atomic.Uint64
}

// NewDriver wraps drv with the given mode. If log is nil, zap's global
// logger is used.
func NewDriver(drv dialect.Driver, mode Mode, log *zap.SugaredLogger) *Driver {
	if log == nil {
		log = zap.S()
	}
	return &Driver{
		inner: drv,
		mode:  mode,
		log:   log,
	}
}

// Mode returns the currently active enforcement mode.
func (d *Driver) Mode() Mode { return d.mode }

// Stats returns runtime counters. Intended for /internal/rls debug endpoint.
type Stats struct {
	Mode           Mode   `json:"mode"`
	QueriesOff     uint64 `json:"queries_off"`
	QueriesShadow  uint64 `json:"queries_shadow"`
	MissingTenant  uint64 `json:"missing_tenant"`
	SystemBypass   uint64 `json:"system_bypass"`
	EnforceApplied uint64 `json:"enforce_applied"`
}

// Stats snapshots the current counters.
func (d *Driver) Stats() Stats {
	return Stats{
		Mode:           d.mode,
		QueriesOff:     d.nQueriesOff.Load(),
		QueriesShadow:  d.nQueriesShadow.Load(),
		MissingTenant:  d.nMissingTenant.Load(),
		SystemBypass:   d.nSystemBypass.Load(),
		EnforceApplied: d.nEnforceApplied.Load(),
	}
}

// -----------------------------------------------------------------------
// dialect.Driver implementation
// -----------------------------------------------------------------------

// Dialect passes through the underlying dialect (e.g. "postgres").
func (d *Driver) Dialect() string { return d.inner.Dialect() }

// Close closes the underlying driver.
func (d *Driver) Close() error { return d.inner.Close() }

// Tx and BeginTx retain the caller's real transaction and isolation options.
func (d *Driver) Tx(ctx context.Context) (dialect.Tx, error) { return d.BeginTx(ctx, nil) }
func (d *Driver) BeginTx(ctx context.Context, opts *sql.TxOptions) (dialect.Tx, error) {
	if err := d.validateMode(); err != nil {
		return nil, err
	}
	d.observe(ctx, "BeginTx", "")
	if d.mode == ModeEnforce {
		return d.beginEnforced(ctx, opts)
	}
	return d.beginInner(ctx, opts)
}
func (d *Driver) beginInner(ctx context.Context, opts *sql.TxOptions) (dialect.Tx, error) {
	if beginner, ok := d.inner.(interface {
		BeginTx(context.Context, *sql.TxOptions) (dialect.Tx, error)
	}); ok {
		return beginner.BeginTx(ctx, opts)
	}
	if opts != nil {
		return nil, fmt.Errorf("rls: driver does not support transaction options")
	}
	return d.inner.Tx(ctx)
}
func (d *Driver) Exec(ctx context.Context, query string, args, v any) error {
	if err := d.validateMode(); err != nil {
		return err
	}
	d.observe(ctx, "Exec", firstToken(query))
	if d.mode == ModeEnforce {
		return d.execEnforced(ctx, query, args, v)
	}
	return d.inner.Exec(ctx, query, args, v)
}
func (d *Driver) Query(ctx context.Context, query string, args, v any) error {
	if err := d.validateMode(); err != nil {
		return err
	}
	d.observe(ctx, "Query", firstToken(query))
	if d.mode == ModeEnforce {
		return d.queryEnforced(ctx, query, args, v)
	}
	return d.inner.Query(ctx, query, args, v)
}

// -----------------------------------------------------------------------
// Observation (used by off + shadow)
// -----------------------------------------------------------------------

// observe records a query event according to the current mode. It never
// blocks and never returns an error: the goal is auditing, not enforcement.
// Enforced counters are incremented only after the tenant setting and role
// checks succeed, in the actual execution boundary.
func (d *Driver) observe(ctx context.Context, op, firstTok string) {
	switch d.mode {
	case ModeOff:
		d.nQueriesOff.Add(1)
		return

	case ModeShadow, ModeEnforce:
		if tenantctx.IsSystemBypass(ctx) {
			d.nSystemBypass.Add(1)
			return
		}
		tid, ok := tenantctx.TenantID(ctx)
		if !ok || tid <= 0 {
			d.nMissingTenant.Add(1)
			// Shadow mode observes; enforce rejects before SQL execution.
			d.log.Warnw(
				"rls: query without tenant scope",
				"op", op,
				"stmt", firstTok,
				"mode", string(d.mode),
			)
			return
		}
		if d.mode == ModeShadow {
			d.nQueriesShadow.Add(1)
			d.log.Debugw(
				"rls: shadow query",
				"op", op, "stmt", firstTok, "tenant_id", tid,
			)
		}

	default:
		d.nQueriesOff.Add(1)
	}
}

// firstToken extracts the SQL verb (SELECT / INSERT / UPDATE / DELETE / …)
// for structured logging. Kept intentionally cheap — no full parse.
func firstToken(q string) string {
	for i := 0; i < len(q); i++ {
		c := q[i]
		if c == ' ' || c == '\t' || c == '\n' {
			return q[:i]
		}
		if i > 32 {
			return q[:32]
		}
	}
	return q
}

// -----------------------------------------------------------------------
// Compile-time interface conformance
// -----------------------------------------------------------------------

var _ dialect.Driver = (*Driver)(nil)

// -----------------------------------------------------------------------
// Convenience: build a driver from an *sql.DB and mode string.
// -----------------------------------------------------------------------

// From wraps db as an Ent driver decorated with RLS observability at the
// given mode. This is the recommended one-liner for wiring at bootstrap.
//
// Example:
//
//	drv := rls.From(db, cfg.RLS.Mode, sugar)
//	client := ent.NewClient(ent.Driver(drv))
func From(inner dialect.Driver, modeStr string, log *zap.SugaredLogger) *Driver {
	return NewDriver(inner, ParseMode(modeStr), log)
}

// Describe returns a short human string used in startup logs.
func (d *Driver) Describe() string {
	return fmt.Sprintf("rls-driver(mode=%s)", d.mode)
}

func (d *Driver) validateMode() error {
	switch d.mode {
	case ModeOff, ModeShadow, ModeEnforce:
		return nil
	default:
		return fmt.Errorf("rls: unsupported enforcement mode %q", d.mode)
	}
}
