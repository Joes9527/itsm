package migration

import (
	"context"
	"database/sql"
	"database/sql/driver"
	"fmt"
	"io"
	"strings"
	"sync"
	"testing"

	"github.com/stretchr/testify/require"
)

// This deterministic driver models two independent PostgreSQL instances that
// report identical database/schema/address/port and backend/database IDs. Their
// advisory-lock namespaces remain independent. Real PostgreSQL coverage lives
// in TestControlledEntryReadOnlyInspectorNeedsNoBusinessAccess.
type bindingInstance struct {
	mu    sync.Mutex
	locks map[int64]bool
}
type bindingConnector struct{ instance *bindingInstance }

func (c bindingConnector) Connect(context.Context) (driver.Conn, error) {
	return &bindingConn{instance: c.instance}, nil
}
func (c bindingConnector) Driver() driver.Driver { return bindingDriver{} }

type bindingDriver struct{}

func (bindingDriver) Open(string) (driver.Conn, error) { return nil, fmt.Errorf("connector required") }

type bindingConn struct {
	instance *bindingInstance
	held     []int64
}

func (c *bindingConn) Prepare(string) (driver.Stmt, error) {
	return nil, fmt.Errorf("unexpected prepare")
}
func (c *bindingConn) Close() error              { return nil }
func (c *bindingConn) Begin() (driver.Tx, error) { return c, nil }
func (c *bindingConn) BeginTx(_ context.Context, opts driver.TxOptions) (driver.Tx, error) {
	if !opts.ReadOnly {
		return nil, fmt.Errorf("read-only required")
	}
	return c, nil
}
func (c *bindingConn) Commit() error { return c.Rollback() }
func (c *bindingConn) Rollback() error {
	c.instance.mu.Lock()
	defer c.instance.mu.Unlock()
	for _, k := range c.held {
		delete(c.instance.locks, k)
	}
	c.held = nil
	return nil
}
func (c *bindingConn) QueryContext(ctx context.Context, q string, args []driver.NamedValue) (driver.Rows, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	row := func(v ...driver.Value) driver.Rows { return &bindingRows{values: v} }
	switch {
	case strings.Contains(q, "current_setting('search_path')"):
		return row("public", "public"), nil
	case strings.Contains(q, "inet_server_addr"):
		return row("same_database", "127.0.0.1", int64(5432)), nil
	case strings.Contains(q, "pg_backend_pid()"):
		return row(int64(16384), int64(12345)), nil
	case strings.Contains(q, "pg_try_advisory_xact_lock"):
		k := args[0].Value.(int64)
		c.instance.mu.Lock()
		defer c.instance.mu.Unlock()
		if c.instance.locks[k] {
			return row(false), nil
		}
		c.instance.locks[k] = true
		c.held = append(c.held, k)
		return row(true), nil
	case strings.Contains(q, "pg_locks"):
		if args[0].Value.(int64) != 16384 || args[1].Value.(int64) != 12345 {
			return row(false), nil
		}
		c.instance.mu.Lock()
		defer c.instance.mu.Unlock()
		k1 := int64(uint64(args[2].Value.(int64))<<32 | uint64(args[3].Value.(int64)))
		k2 := int64(uint64(args[4].Value.(int64))<<32 | uint64(args[5].Value.(int64)))
		return row(c.instance.locks[k1] && c.instance.locks[k2]), nil
	default:
		return nil, fmt.Errorf("unexpected binding query: %s", q)
	}
}

type bindingRows struct {
	values []driver.Value
	done   bool
}

func (r *bindingRows) Columns() []string { return make([]string, len(r.values)) }
func (r *bindingRows) Close() error      { return nil }
func (r *bindingRows) Next(dest []driver.Value) error {
	if r.done {
		return io.EOF
	}
	copy(dest, r.values)
	r.done = true
	return nil
}

func TestRuntimeConnectionBindingRejectsModeledIdenticalTupleAcrossInstances(t *testing.T) {
	a := &bindingInstance{locks: map[int64]bool{}}
	b := &bindingInstance{locks: map[int64]bool{}}
	business := sql.OpenDB(bindingConnector{a})
	defer business.Close()
	inspector := sql.OpenDB(bindingConnector{b})
	defer inspector.Close()
	ctx := context.Background()
	bt, err := business.BeginTx(ctx, &sql.TxOptions{ReadOnly: true})
	require.NoError(t, err)
	defer bt.Rollback()
	it, err := inspector.BeginTx(ctx, &sql.TxOptions{ReadOnly: true})
	require.NoError(t, err)
	defer it.Rollback()
	bf, err := runtimeConnectionFingerprint(ctx, bt)
	require.NoError(t, err)
	inf, err := runtimeConnectionFingerprint(ctx, it)
	require.NoError(t, err)
	require.Equal(t, bf, inf, "reported tuples deliberately collide")
	require.ErrorContains(t, verifyRuntimeConnectionBinding(ctx, bt, it), "does not match")
	require.NoError(t, bt.Rollback())
	require.Empty(t, a.locks, "transaction rollback must release proof locks")
}

func TestRuntimeConnectionBindingSameInstanceReleasesAndRenewsProof(t *testing.T) {
	instance := &bindingInstance{locks: map[int64]bool{}}
	business := sql.OpenDB(bindingConnector{instance})
	defer business.Close()
	inspector := sql.OpenDB(bindingConnector{instance})
	defer inspector.Close()
	business.SetMaxOpenConns(1)
	inspector.SetMaxOpenConns(1)
	ctx := context.Background()
	for i := 0; i < 2; i++ {
		bt, err := business.BeginTx(ctx, &sql.TxOptions{ReadOnly: true})
		require.NoError(t, err)
		it, err := inspector.BeginTx(ctx, &sql.TxOptions{ReadOnly: true})
		require.NoError(t, err)
		require.NoError(t, verifyRuntimeConnectionBinding(ctx, bt, it))
		require.Len(t, instance.locks, 2)
		require.NoError(t, it.Rollback())
		require.NoError(t, bt.Rollback())
		require.Empty(t, instance.locks, "pooled sessions cannot retain proof")
	}
}
