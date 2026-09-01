package rls

import (
	"context"
	"database/sql"
	"database/sql/driver"
	"errors"
	"testing"
)

type recordedRLSExec struct {
	query string
	args  []driver.NamedValue
}

type recordingRLSDriver struct {
	executed chan recordedRLSExec
}

func (d *recordingRLSDriver) Open(string) (driver.Conn, error) {
	return &recordingRLSConn{executed: d.executed}, nil
}

type recordingRLSConn struct {
	executed chan recordedRLSExec
}

func (c *recordingRLSConn) Prepare(string) (driver.Stmt, error) {
	return nil, errors.New("prepare is not supported")
}
func (c *recordingRLSConn) Close() error { return nil }
func (c *recordingRLSConn) Begin() (driver.Tx, error) {
	return nil, errors.New("transactions are not supported")
}
func (c *recordingRLSConn) ExecContext(_ context.Context, query string, args []driver.NamedValue) (driver.Result, error) {
	c.executed <- recordedRLSExec{query: query, args: args}
	return driver.RowsAffected(1), nil
}

func TestWithTenantAndRoundTrip(t *testing.T) {
	ctx := WithTenant(context.Background(), 42)
	tid, ok := TenantFromContext(ctx)
	if !ok || tid != 42 {
		t.Fatalf("expected tenant 42, got %d ok=%v", tid, ok)
	}
}

func TestTenantMissing(t *testing.T) {
	_, ok := TenantFromContext(context.Background())
	if ok {
		t.Fatal("expected missing tenant")
	}
}

func TestSystemBypass(t *testing.T) {
	if IsSystemBypass(context.Background()) {
		t.Fatal("plain context should not be bypass")
	}
	ctx := WithSystemBypass(context.Background())
	if !IsSystemBypass(ctx) {
		t.Fatal("bypass context should be flagged")
	}
	// bypass and tenant can coexist; DB layer decides which to honor
	ctx = WithTenant(ctx, 1)
	if !IsSystemBypass(ctx) {
		t.Fatal("bypass should survive WithTenant chaining")
	}
}

func TestAcquireConnUsesParameterSafeCanonicalTenantSetting(t *testing.T) {
	executed := make(chan recordedRLSExec, 1)
	driverName := "recording-rls-" + t.Name()
	sql.Register(driverName, &recordingRLSDriver{executed: executed})
	db, err := sql.Open(driverName, "")
	if err != nil {
		t.Fatalf("open recording db: %v", err)
	}
	defer db.Close()

	conn, err := AcquireConn(WithTenant(context.Background(), 42), db)
	if err != nil {
		t.Fatalf("acquire conn: %v", err)
	}
	defer conn.Close()

	call := <-executed
	if call.query != "SELECT set_config('app.current_tenant', $1, false)" {
		t.Fatalf("unexpected tenant setting query: %q", call.query)
	}
	if len(call.args) != 1 || call.args[0].Value != "42" {
		t.Fatalf("tenant setting must use one decimal string argument, got %#v", call.args)
	}
}
