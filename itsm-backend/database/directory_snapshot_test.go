package database

import (
	"context"
	"database/sql"
	"database/sql/driver"
	"errors"
	"fmt"
	"io"
	"strings"
	"testing"
	"time"

	entsql "entgo.io/ent/dialect/sql"
	"github.com/stretchr/testify/require"
	"itsm-backend/common/tenantctx"
	"itsm-backend/ent"
)

type snapshotSQLState struct {
	failExport, failImport, failClose bool
	snapshot                          string
	directory                         bool
	begins, ends, imports, queries    int
	readOnly                          bool
}
type snapshotSQLDriver struct{ state *snapshotSQLState }

func (d snapshotSQLDriver) Open(string) (driver.Conn, error) {
	return &snapshotSQLConn{state: d.state}, nil
}

type snapshotSQLConn struct{ state *snapshotSQLState }

func (c *snapshotSQLConn) Prepare(string) (driver.Stmt, error) { return nil, driver.ErrSkip }
func (c *snapshotSQLConn) Close() error                        { return nil }
func (c *snapshotSQLConn) Begin() (driver.Tx, error) {
	return c.BeginTx(context.Background(), driver.TxOptions{})
}
func (c *snapshotSQLConn) BeginTx(_ context.Context, options driver.TxOptions) (driver.Tx, error) {
	c.state.begins++
	c.state.readOnly = options.ReadOnly
	return snapshotSQLTx{c.state}, nil
}
func (c *snapshotSQLConn) ExecContext(_ context.Context, q string, _ []driver.NamedValue) (driver.Result, error) {
	if strings.HasPrefix(q, "SET TRANSACTION SNAPSHOT ") {
		c.state.imports++
		if c.state.queries != 0 {
			return nil, errors.New("import after query")
		}
		if c.state.failImport {
			return nil, errors.New("injected import failure")
		}
	}
	return driver.RowsAffected(1), nil
}
func (c *snapshotSQLConn) QueryContext(_ context.Context, _ string, _ []driver.NamedValue) (driver.Rows, error) {
	c.state.queries++
	if c.state.failExport {
		return nil, errors.New("injected export failure")
	}
	return &snapshotSQLRows{value: c.state.snapshot}, nil
}

type snapshotSQLTx struct{ state *snapshotSQLState }

func (tx snapshotSQLTx) Commit() error { tx.state.ends++; return nil }
func (tx snapshotSQLTx) Rollback() error {
	tx.state.ends++
	if tx.state.failClose {
		return errors.New("injected close failure")
	}
	return nil
}

type snapshotSQLRows struct {
	value string
	done  bool
}

func (*snapshotSQLRows) Columns() []string { return []string{"snapshot"} }
func (*snapshotSQLRows) Close() error      { return nil }
func (r *snapshotSQLRows) Next(values []driver.Value) error {
	if r.done {
		return io.EOF
	}
	r.done = true
	values[0] = r.value
	return nil
}

func snapshotTestClient(t *testing.T, state *snapshotSQLState) (*ent.Client, *sql.DB) {
	t.Helper()
	name := fmt.Sprintf("snapshot_%d", time.Now().UnixNano())
	sql.Register(name, snapshotSQLDriver{state})
	db, err := sql.Open(name, "")
	require.NoError(t, err)
	client := ent.NewClient(ent.Driver(entsql.OpenDB("postgres", db)))
	t.Cleanup(func() { require.NoError(t, client.Close()) })
	return client, db
}

func TestDirectorySnapshotCleanupAndImportOrder(t *testing.T) {
	for _, scenario := range []string{"success", "export", "import", "close", "unsafe_identifier", "wrong_scope"} {
		t.Run(scenario, func(t *testing.T) {
			source := &snapshotSQLState{snapshot: "00000003-0000001B-1"}
			directory := &snapshotSQLState{directory: true}
			switch scenario {
			case "export":
				source.failExport = true
			case "import":
				directory.failImport = true
			case "close":
				directory.failClose = true
			case "unsafe_identifier":
				source.snapshot = "'; SELECT 1; --"
			}
			client, db := snapshotTestClient(t, source)
			system, systemDB := snapshotTestClient(t, directory)
			ctx := tenantctx.WithTenantID(context.Background(), 1)
			tx, err := client.BeginTx(ctx, &sql.TxOptions{Isolation: sql.LevelRepeatableRead})
			require.NoError(t, err)
			scope := 1
			if scenario == "wrong_scope" {
				scope = 2
			}
			view, close, err := (&directorySnapshot{system: system}).Open(ctx, tx, scope)
			if scenario == "success" || scenario == "close" {
				require.NoError(t, err)
				require.NotNil(t, view)
				err = close()
				if scenario == "close" {
					require.ErrorContains(t, err, "close failure")
				} else {
					require.NoError(t, err)
				}
			} else {
				require.Error(t, err)
				require.Nil(t, view)
				require.Nil(t, close)
			}
			require.NoError(t, tx.Rollback())
			require.Equal(t, source.begins, source.ends)
			require.Equal(t, directory.begins, directory.ends)
			require.Zero(t, db.Stats().InUse)
			require.Zero(t, systemDB.Stats().InUse)
			if directory.begins > 0 {
				require.True(t, directory.readOnly)
				require.Equal(t, 1, directory.imports)
				require.Zero(t, directory.queries)
			}
		})
	}
}
