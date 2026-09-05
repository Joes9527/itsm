package rls

import (
	"context"
	"database/sql"
	"database/sql/driver"
	"fmt"
	"io"
	"testing"
	"time"

	entsql "entgo.io/ent/dialect/sql"
	"github.com/stretchr/testify/require"
	"go.uber.org/zap"
	"itsm-backend/common/tenantctx"
)

type variableDriver struct{}

func (variableDriver) Open(string) (driver.Conn, error) { return variableConn{}, nil }

type variableConn struct{}

func (variableConn) Prepare(string) (driver.Stmt, error) { return nil, driver.ErrSkip }
func (variableConn) Close() error                        { return nil }
func (variableConn) Begin() (driver.Tx, error)           { return variableTx{}, nil }
func (variableConn) ExecContext(context.Context, string, []driver.NamedValue) (driver.Result, error) {
	return driver.RowsAffected(1), nil
}
func (variableConn) QueryContext(context.Context, string, []driver.NamedValue) (driver.Rows, error) {
	return &variableRows{}, nil
}

type variableTx struct{}

func (variableTx) Commit() error   { return nil }
func (variableTx) Rollback() error { return nil }

type variableRows struct{ done bool }

func (*variableRows) Columns() []string { return []string{"privileged"} }
func (*variableRows) Close() error      { return nil }
func (r *variableRows) Next(out []driver.Value) error {
	if r.done {
		return io.EOF
	}
	r.done = true
	out[0] = false
	return nil
}

func TestEnforcedEntVariablesNeverPanicOrLeak(t *testing.T) {
	name := fmt.Sprintf("%s_%d", t.Name(), time.Now().UnixNano())
	sql.Register(name, variableDriver{})
	db, err := sql.Open(name, "")
	require.NoError(t, err)
	db.SetMaxOpenConns(1)
	t.Cleanup(func() {
		if db.Stats().InUse == 0 {
			require.NoError(t, db.Close())
		}
	})
	d := From(entsql.OpenDB("postgres", db), "enforce", zap.NewNop().Sugar())
	ctx := entsql.WithVar(tenantctx.WithTenantID(context.Background(), 1), "application_name", "test")
	require.NotPanics(t, func() { require.NoError(t, d.Exec(ctx, "SELECT 1", []any{}, nil)) })
	require.Equal(t, 0, db.Stats().InUse)
}

func TestTransactionRawSQLPreservesScopeAndVariables(t *testing.T) {
	name := fmt.Sprintf("%s_%d", t.Name(), time.Now().UnixNano())
	sql.Register(name, variableDriver{})
	db, err := sql.Open(name, "")
	require.NoError(t, err)
	defer db.Close()
	d := From(entsql.OpenDB("postgres", db), "enforce", zap.NewNop().Sugar())
	ctx := tenantctx.WithTenantID(context.Background(), 1)
	tx, err := d.BeginTx(ctx, nil)
	require.NoError(t, err)
	defer tx.Rollback()
	raw, ok := tx.(interface {
		ExecContext(context.Context, string, ...any) (sql.Result, error)
		QueryContext(context.Context, string, ...any) (*sql.Rows, error)
	})
	require.True(t, ok, "transaction must support guarded Ent raw SQL")
	_, err = raw.ExecContext(tenantctx.WithTenantID(ctx, 2), "SELECT 1")
	require.ErrorContains(t, err, "scope cannot change")
	for _, key := range []string{"role", "app.current_tenant", "row_security", "session_authorization"} {
		_, err = raw.QueryContext(entsql.WithVar(ctx, key, "unsafe"), "SELECT 1")
		require.ErrorContains(t, err, "reserved session variable")
	}
	rows, err := raw.QueryContext(ctx, "SELECT 1")
	require.NoError(t, err)
	require.NoError(t, rows.Close())
	require.NoError(t, tx.Rollback())
	_, err = raw.ExecContext(ctx, "SELECT 1")
	require.ErrorIs(t, err, sql.ErrTxDone)
	require.Zero(t, db.Stats().InUse)
}
