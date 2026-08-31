package rls

import (
	"context"
	"database/sql"
	"database/sql/driver"
	"errors"
	"regexp"
	"sync"
	"testing"

	"github.com/DATA-DOG/go-sqlmock"
)

var errInjectedSessionCleanup = errors.New("injected session cleanup failure")

type invalidationTestConnector struct {
	mu          sync.Mutex
	connections []*invalidationTestConn
	failSet     bool
	failDiscard bool
}

func (c *invalidationTestConnector) Connect(context.Context) (driver.Conn, error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	conn := &invalidationTestConn{id: len(c.connections) + 1, connector: c}
	c.connections = append(c.connections, conn)
	return conn, nil
}

func (*invalidationTestConnector) Driver() driver.Driver { return invalidationTestDriver{} }

func (c *invalidationTestConnector) connection(index int) *invalidationTestConn {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.connections[index]
}

func (c *invalidationTestConnector) connectionCount() int {
	c.mu.Lock()
	defer c.mu.Unlock()
	return len(c.connections)
}

type invalidationTestDriver struct{}

func (invalidationTestDriver) Open(string) (driver.Conn, error) {
	return nil, errors.New("invalidation test driver requires OpenDB")
}

type invalidationTestConn struct {
	id        int
	connector *invalidationTestConnector
	mu        sync.Mutex
	closed    bool
}

func (*invalidationTestConn) Prepare(string) (driver.Stmt, error) {
	return nil, errors.New("prepare is not supported by invalidation test driver")
}

func (*invalidationTestConn) Begin() (driver.Tx, error) {
	return nil, errors.New("transactions are not supported by invalidation test driver")
}

func (c *invalidationTestConn) Close() error {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.closed = true
	return nil
}

func (c *invalidationTestConn) isClosed() bool {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.closed
}

func (c *invalidationTestConn) ExecContext(_ context.Context, query string, _ []driver.NamedValue) (driver.Result, error) {
	c.connector.mu.Lock()
	defer c.connector.mu.Unlock()
	if query == "SELECT set_config('app.current_tenant', $1, false)" && c.connector.failSet {
		c.connector.failSet = false
		return nil, errInjectedSessionCleanup
	}
	if query == "DISCARD ALL" && c.connector.failDiscard {
		c.connector.failDiscard = false
		return nil, errInjectedSessionCleanup
	}
	return driver.RowsAffected(1), nil
}

func assertNextBorrowUsesNewPhysicalConnection(t *testing.T, db *sql.DB, connector *invalidationTestConnector) {
	t.Helper()
	conn, err := AcquireConn(WithSystemBypass(context.Background()), db)
	if err != nil {
		t.Fatalf("borrow connection after invalidation: %v", err)
	}
	defer conn.Close()
	if connector.connectionCount() != 2 {
		t.Fatalf("expected a second physical connection, got %d", connector.connectionCount())
	}
	var borrowedID int
	if err := conn.Raw(func(raw any) error {
		borrowedID = raw.(*invalidationTestConn).id
		return nil
	}); err != nil {
		t.Fatalf("inspect replacement connection: %v", err)
	}
	if borrowedID != 2 {
		t.Fatalf("reused dirty physical connection %d; want replacement connection 2", borrowedID)
	}
}

func TestAcquireConn_InvalidatesPhysicalConnectionWhenSetConfigFails(t *testing.T) {
	connector := &invalidationTestConnector{failSet: true}
	db := sql.OpenDB(connector)
	db.SetMaxOpenConns(1)
	db.SetMaxIdleConns(1)
	defer db.Close()

	_, err := AcquireConn(WithTenant(context.Background(), 42), db)
	if !errors.Is(err, errInjectedSessionCleanup) {
		t.Fatalf("expected set_config failure, got %v", err)
	}
	if !connector.connection(0).isClosed() {
		t.Fatal("set_config failure returned the dirty physical connection to the pool")
	}
	assertNextBorrowUsesNewPhysicalConnection(t, db, connector)
}

func TestReleaseConn_InvalidatesPhysicalConnectionWhenDiscardFails(t *testing.T) {
	connector := &invalidationTestConnector{}
	db := sql.OpenDB(connector)
	db.SetMaxOpenConns(1)
	db.SetMaxIdleConns(1)
	defer db.Close()

	ctx := WithTenant(context.Background(), 42)
	conn, err := AcquireConn(ctx, db)
	if err != nil {
		t.Fatalf("acquire tenant-scoped connection: %v", err)
	}
	connector.mu.Lock()
	connector.failDiscard = true
	connector.mu.Unlock()
	if err := ReleaseConn(ctx, conn); !errors.Is(err, errInjectedSessionCleanup) {
		t.Fatalf("expected DISCARD failure, got %v", err)
	}
	if !connector.connection(0).isClosed() {
		t.Fatal("DISCARD failure returned the dirty physical connection to the pool")
	}
	assertNextBorrowUsesNewPhysicalConnection(t, db, connector)
}

func TestAcquireConn_SetsTenantWithParameterizedSetConfig(t *testing.T) {
	db, mock, err := sqlmock.New()
	if err != nil {
		t.Fatalf("create sqlmock database: %v", err)
	}
	defer db.Close()

	mock.ExpectExec(regexp.QuoteMeta("SELECT set_config('app.current_tenant', $1, false)")).
		WithArgs(int64(42)).
		WillReturnResult(sqlmock.NewResult(0, 1))

	conn, err := AcquireConn(WithTenant(context.Background(), 42), db)
	if err != nil {
		t.Fatalf("acquire tenant-scoped connection: %v", err)
	}
	if err := conn.Close(); err != nil {
		t.Fatalf("close connection: %v", err)
	}
	if err := mock.ExpectationsWereMet(); err != nil {
		t.Fatalf("tenant session statement mismatch: %v", err)
	}
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
