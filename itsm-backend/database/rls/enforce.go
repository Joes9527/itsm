package rls

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"strconv"
	"sync"

	"entgo.io/ent/dialect"
	entsql "entgo.io/ent/dialect/sql"
	"itsm-backend/common/tenantctx"
)

// scope is fixed for the lifetime of an explicit transaction. A system scope
// remains server-owned; it does not grant database privileges or select a role.
type scope struct {
	tenant int
	system bool
}

func scopeFrom(ctx context.Context) (scope, error) {
	if tenantctx.IsSystemBypass(ctx) {
		return scope{system: true}, nil
	}
	id, ok := tenantctx.TenantID(ctx)
	if !ok || id <= 0 {
		return scope{}, ErrNoTenant
	}
	return scope{tenant: id}, nil
}
func (d *Driver) beginEnforced(ctx context.Context, opts *sql.TxOptions) (dialect.Tx, error) {
	scoped, err := scopeFrom(ctx)
	if err != nil {
		return nil, err
	}
	tx, err := d.beginInner(ctx, opts)
	if err != nil {
		return nil, err
	}
	if !scoped.system {
		if err = requireTenantRole(ctx, tx); err != nil {
			_ = tx.Rollback()
			return nil, err
		}
		if err = tx.Exec(ctx, "SELECT set_config('app.current_tenant', $1, true)", []any{strconv.Itoa(scoped.tenant)}, nil); err != nil {
			_ = tx.Rollback()
			return nil, fmt.Errorf("rls: set transaction tenant: %w", err)
		}
	}
	if !scoped.system {
		d.nEnforceApplied.Add(1)
	}
	return &scopedTx{Tx: tx, scope: scoped}, nil
}

type scopedTx struct {
	dialect.Tx
	scope scope
}

func (tx *scopedTx) check(ctx context.Context) error {
	current, err := scopeFrom(ctx)
	if err != nil {
		return err
	}
	if current != tx.scope {
		return fmt.Errorf("rls: transaction scope cannot change")
	}
	return nil
}
func (tx *scopedTx) Exec(ctx context.Context, q string, args, v any) error {
	if err := tx.check(ctx); err != nil {
		return err
	}
	return tx.Tx.Exec(ctx, q, args, v)
}
func (tx *scopedTx) Query(ctx context.Context, q string, args, v any) error {
	if err := tx.check(ctx); err != nil {
		return err
	}
	return tx.Tx.Query(ctx, q, args, v)
}

// Autocommit uses a checked-out physical connection. It retains PostgreSQL's
// ordinary statement/RETURNING semantics without introducing a hidden commit
// after Ent has already reported success. ReleaseConn cleans or evicts it even
// if the request was cancelled; Query retains ownership until rows close.
func (d *Driver) acquire(ctx context.Context) (*sql.Conn, error) {
	if _, err := scopeFrom(ctx); err != nil {
		return nil, err
	}
	pool, ok := d.inner.(interface{ DB() *sql.DB })
	if !ok {
		return nil, fmt.Errorf("rls: enforced autocommit requires a SQL connection pool")
	}
	conn, err := AcquireConn(ctx, pool.DB())
	if err != nil {
		return nil, err
	}
	if err = requireTenantRole(ctx, entsql.Conn{ExecQuerier: conn}); err != nil {
		return nil, errors.Join(err, ReleaseConn(ctx, conn))
	}
	d.nEnforceApplied.Add(1)
	return conn, nil
}
func (d *Driver) execEnforced(ctx context.Context, q string, args, v any) (err error) {
	if _, err = scopeFrom(ctx); err != nil {
		return err
	}
	if tenantctx.IsSystemBypass(ctx) {
		return d.inner.Exec(ctx, q, args, v)
	}
	conn, err := d.acquire(ctx)
	if err != nil {
		return err
	}
	defer func() { err = errors.Join(err, ReleaseConn(ctx, conn)) }()
	return (entsql.Conn{ExecQuerier: conn}).Exec(ctx, q, args, v)
}
func (d *Driver) queryEnforced(ctx context.Context, q string, args, v any) error {
	if _, err := scopeFrom(ctx); err != nil {
		return err
	}
	if tenantctx.IsSystemBypass(ctx) {
		return d.inner.Query(ctx, q, args, v)
	}
	rows, ok := v.(*entsql.Rows)
	if !ok {
		return fmt.Errorf("rls: unsupported rows destination %T", v)
	}
	conn, err := d.acquire(ctx)
	if err != nil {
		return err
	}
	if err = (entsql.Conn{ExecQuerier: conn}).Query(ctx, q, args, rows); err != nil {
		return errors.Join(err, ReleaseConn(ctx, conn))
	}
	rows.ColumnScanner = &scopedRows{ColumnScanner: rows.ColumnScanner, release: func() error { return ReleaseConn(ctx, conn) }}
	return nil
}

type scopedRows struct {
	entsql.ColumnScanner
	once     sync.Once
	release  func() error
	closeErr error
}

func (rows *scopedRows) Close() error {
	rows.once.Do(func() { rows.closeErr = errors.Join(rows.ColumnScanner.Close(), rows.release()) })
	return rows.closeErr
}
func (rows *scopedRows) Err() error { return errors.Join(rows.ColumnScanner.Err(), rows.closeErr) }

// Setting a GUC cannot constrain a superuser or BYPASSRLS role. Reject that
// configuration at the same physical connection used for tenant operations.
func requireTenantRole(ctx context.Context, db dialect.ExecQuerier) error {
	rows := &entsql.Rows{}
	if err := db.Query(ctx, "SELECT rolsuper OR rolbypassrls FROM pg_roles WHERE rolname=current_user", []any{}, rows); err != nil {
		return err
	}
	defer rows.Close()
	if !rows.Next() {
		return errors.Join(fmt.Errorf("rls: database role could not be verified"), rows.Err())
	}
	var privileged bool
	if err := rows.Scan(&privileged); err != nil {
		return err
	}
	if privileged {
		return fmt.Errorf("rls: tenant operations require a non-superuser, non-bypass database role")
	}
	return nil
}
