package rls

import (
	"context"
	"regexp"
	"testing"

	"github.com/DATA-DOG/go-sqlmock"
)

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
