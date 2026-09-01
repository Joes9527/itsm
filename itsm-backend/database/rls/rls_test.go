package rls

import (
	"context"
	"database/sql"
	"database/sql/driver"
	"errors"
	"strings"
	"sync/atomic"
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
func (c *recordingRLSConn) ExecContext(ctx context.Context, query string, args []driver.NamedValue) (driver.Result, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	c.executed <- recordedRLSExec{query: query, args: args}
	return driver.RowsAffected(1), nil
}

type failingDiscardDriver struct {
	openCount  atomic.Int32
	closeCount atomic.Int32
}

func (d *failingDiscardDriver) Open(string) (driver.Conn, error) {
	d.openCount.Add(1)
	return &failingDiscardConn{driver: d}, nil
}

type failingDiscardConn struct {
	driver *failingDiscardDriver
}

func (c *failingDiscardConn) Prepare(string) (driver.Stmt, error) {
	return nil, errors.New("prepare is not supported")
}
func (c *failingDiscardConn) Close() error {
	c.driver.closeCount.Add(1)
	return nil
}
func (c *failingDiscardConn) Begin() (driver.Tx, error) {
	return nil, errors.New("transactions are not supported")
}
func (c *failingDiscardConn) ExecContext(ctx context.Context, query string, _ []driver.NamedValue) (driver.Result, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	if strings.TrimSpace(query) == "DISCARD ALL" {
		return nil, errors.New("forced discard failure")
	}
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

func TestReleaseConnUsesIndependentCleanupContext(t *testing.T) {
	executed := make(chan recordedRLSExec, 2)
	driverName := "recording-rls-release-" + t.Name()
	sql.Register(driverName, &recordingRLSDriver{executed: executed})
	db, err := sql.Open(driverName, "")
	if err != nil {
		t.Fatalf("open recording db: %v", err)
	}
	defer db.Close()

	requestCtx, cancel := context.WithCancel(WithTenant(context.Background(), 42))
	conn, err := AcquireConn(requestCtx, db)
	if err != nil {
		t.Fatalf("acquire conn: %v", err)
	}
	<-executed // tenant set
	cancel()

	if err := ReleaseConn(requestCtx, conn); err != nil {
		t.Fatalf("release with canceled request context: %v", err)
	}
	call := <-executed
	if call.query != "DISCARD ALL" {
		t.Fatalf("unexpected cleanup query: %q", call.query)
	}
}

func TestReleaseConnEvictsPhysicalConnectionWhenCleanupFails(t *testing.T) {
	testDriver := &failingDiscardDriver{}
	driverName := "failing-discard-" + t.Name()
	sql.Register(driverName, testDriver)
	db, err := sql.Open(driverName, "")
	if err != nil {
		t.Fatalf("open failing-discard db: %v", err)
	}
	defer db.Close()
	db.SetMaxOpenConns(1)
	db.SetMaxIdleConns(1)

	conn, err := AcquireConn(WithTenant(context.Background(), 42), db)
	if err != nil {
		t.Fatalf("acquire conn: %v", err)
	}
	if err := ReleaseConn(context.Background(), conn); err == nil {
		t.Fatal("expected forced cleanup failure")
	}

	next, err := db.Conn(context.Background())
	if err != nil {
		t.Fatalf("acquire replacement conn: %v", err)
	}
	if err := next.Close(); err != nil {
		t.Fatalf("close replacement conn: %v", err)
	}
	if got := testDriver.openCount.Load(); got != 2 {
		t.Fatalf("dirty physical connection was reused: opened %d connections, want 2", got)
	}
	if got := testDriver.closeCount.Load(); got < 1 {
		t.Fatalf("dirty physical connection was not closed: close count %d", got)
	}
}
