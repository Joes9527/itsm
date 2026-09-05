package rls

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
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
	if scoped.system {
		tx, err := d.beginInner(ctx, opts)
		if err != nil {
			return nil, err
		}
		return &scopedTx{Tx: tx, scope: scoped}, nil
	}
	conn, err := d.acquire(ctx)
	if err != nil {
		return nil, err
	}
	raw, err := conn.BeginTx(ctx, opts)
	if err != nil {
		return nil, errors.Join(err, ReleaseConn(ctx, conn))
	}
	tx := &entsql.Tx{Conn: entsql.Conn{ExecQuerier: raw}, Tx: raw}

	return &scopedTx{Tx: tx, scope: scoped, raw: raw, release: func() error { return ReleaseConn(ctx, conn) }}, nil
}

type scopedTx struct {
	dialect.Tx
	scope    scope
	raw      entsql.ExecQuerier
	release  func() error
	once     sync.Once
	closeErr error
}

func (tx *scopedTx) finish(commit bool) error {
	finished := false
	tx.once.Do(func() {
		finished = true
		if commit {
			tx.closeErr = tx.Tx.Commit()
		} else {
			tx.closeErr = tx.Tx.Rollback()
		}
		if tx.release != nil {
			tx.closeErr = errors.Join(tx.closeErr, tx.release())
		}
	})
	if !finished {
		return sql.ErrTxDone
	}
	return tx.closeErr
}
func (tx *scopedTx) Commit() error   { return tx.finish(true) }
func (tx *scopedTx) Rollback() error { return tx.finish(false) }

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
	if tx.scope.system {
		return tx.Tx.Exec(ctx, q, args, v)
	}
	if err := prepareVariables(ctx, tx.raw, tx.scope); err != nil {
		return err
	}
	return (rawConnection{tx.raw}).Exec(ctx, q, args, v)
}
func (tx *scopedTx) Query(ctx context.Context, q string, args, v any) error {
	if err := tx.check(ctx); err != nil {
		return err
	}
	if tx.scope.system {
		return tx.Tx.Query(ctx, q, args, v)
	}
	if err := prepareVariables(ctx, tx.raw, tx.scope); err != nil {
		return err
	}
	return (rawConnection{tx.raw}).Query(ctx, q, args, v)
}

// Autocommit uses a checked-out physical connection. It retains PostgreSQL's
// ordinary statement/RETURNING semantics without introducing a hidden commit
// after Ent has already reported success. ReleaseConn cleans or evicts it even
// if the request was cancelled; Query retains ownership until rows close.
func (d *Driver) acquire(ctx context.Context) (conn *sql.Conn, err error) {
	scoped, err := scopeFrom(ctx)
	if err != nil {
		return nil, err
	}
	pool, ok := d.inner.(interface{ DB() *sql.DB })
	if !ok {
		return nil, fmt.Errorf("rls: enforced autocommit requires a SQL connection pool")
	}
	conn, err = AcquireConn(ctx, pool.DB())
	if err != nil {
		return nil, err
	}
	checkedOut := conn
	owned := false
	defer func() {
		if !owned {
			err = errors.Join(err, ReleaseConn(ctx, checkedOut))
			conn = nil
		}
	}()
	setup, err := conn.BeginTx(ctx, nil)
	if err != nil {
		return nil, err
	}
	defer setup.Rollback()
	if err = prepareVariables(ctx, setup, scoped); err != nil {
		return nil, err
	}
	if err = setup.Commit(); err != nil {
		return nil, err
	}
	d.nEnforceApplied.Add(1)
	owned = true
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
	return (rawConnection{conn}).Exec(ctx, q, args, v)
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
	if err = (rawConnection{conn}).Query(ctx, q, args, rows); err != nil {
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
