package router

import (
	"context"
	"database/sql"
	"database/sql/driver"
	"errors"
	"io"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"

	"itsm-backend/pkg/seeder"
)

// The fake SQL transport exercises the real baseline query and its refusal paths.
// Any attempted schema-ledger query through the business pool fails.
type (
	readinessDriver struct{}
	readinessConn   struct{ mode string }
	readinessRows   struct {
		values []driver.Value
		read   bool
	}
)

func (readinessDriver) Open(name string) (driver.Conn, error) { return &readinessConn{name}, nil }
func (*readinessConn) Prepare(string) (driver.Stmt, error) {
	return nil, errors.New("unexpected prepare")
}
func (*readinessConn) Close() error {
	return nil
}
func (*readinessConn) Begin() (driver.Tx, error) { return nil, errors.New("unexpected transaction") }

func (c *readinessConn) QueryContext(_ context.Context, q string, _ []driver.NamedValue) (driver.Rows, error) {
	if !strings.Contains(q, "FROM initialization_installations") {
		return nil, errors.New("business pool must not inspect migration evidence")
	}
	if c.mode == "denied" {
		return nil, errors.New("permission denied")
	}
	count := int64(len(seeder.ProductionComponentNames))
	version := seeder.CurrentTenantTemplateVersion
	if c.mode == "partial" {
		count--
	}
	if c.mode == "old" {
		version = "old"
	}
	return &readinessRows{values: []driver.Value{count, version}}, nil
}
func (*readinessRows) Columns() []string { return []string{"count", "version"} }
func (*readinessRows) Close() error      { return nil }
func (r *readinessRows) Next(v []driver.Value) error {
	if r.read {
		return io.EOF
	}
	copy(v, r.values)
	r.read = true
	return nil
}
func init() { sql.Register("readiness-test", readinessDriver{}) }
func TestInitializationReadinessKeepsBaselineAndAdmissionGates(t *testing.T) {
	for _, mode := range []string{"ready", "partial", "old", "denied"} {
		t.Run(mode, func(t *testing.T) {
			db, err := sql.Open("readiness-test", mode)
			require.NoError(t, err)
			defer db.Close()
			called := false
			result := checkInitializationReadiness(context.Background(), db, func(context.Context) error { called = true; return nil })
			require.True(t, called)
			require.Equal(t, mode == "ready", result.Ready)
		})
	}
	db, err := sql.Open("readiness-test", "ready")
	require.NoError(t, err)
	defer db.Close()
	for _, admission := range []func(context.Context) error{nil, func(context.Context) error { return errors.New("inspection denied") }} {
		result := checkInitializationReadiness(context.Background(), db, admission)
		require.False(t, result.Ready)
		require.Empty(t, result.BaselineVersion)
	}
}
