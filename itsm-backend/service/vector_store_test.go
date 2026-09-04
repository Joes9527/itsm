package service

import (
	"context"
	"database/sql"
	"database/sql/driver"
	"errors"
	"fmt"
	"io"
	"strings"
	"sync"
	"sync/atomic"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestVectorStoreAvailabilityCheckNeverExecutesDDL(t *testing.T) {
	db, statements := openVectorAvailabilityRecorder(t, true)
	store := NewVectorStore(db)

	require.NoError(t, store.CheckAvailability(context.Background()))
	require.NotEmpty(t, *statements)
	for _, statement := range *statements {
		normalized := strings.ToUpper(strings.TrimSpace(statement))
		require.True(t, strings.HasPrefix(normalized, "SELECT"), statement)
		for _, verb := range []string{"CREATE", "ALTER", "DROP", "GRANT", "REVOKE", "INSERT", "UPDATE", "DELETE"} {
			require.NotContains(t, normalized, verb, statement)
		}
	}
}

func TestVectorStoreAvailabilityCheckFailsClosedWhenCapabilityIsMissing(t *testing.T) {
	db, _ := openVectorAvailabilityRecorder(t, false)
	err := NewVectorStore(db).CheckAvailability(context.Background())
	require.ErrorContains(t, err, "pgvector runtime capability is unavailable")
}

var vectorAvailabilityDriverSequence atomic.Uint64

func openVectorAvailabilityRecorder(t *testing.T, available bool) (*sql.DB, *[]string) {
	t.Helper()
	statements := &[]string{}
	driverName := fmt.Sprintf("vector_availability_%d", vectorAvailabilityDriverSequence.Add(1))
	sql.Register(driverName, &vectorAvailabilityDriver{available: available, statements: statements})
	db, err := sql.Open(driverName, "")
	require.NoError(t, err)
	t.Cleanup(func() { require.NoError(t, db.Close()) })
	return db, statements
}

type vectorAvailabilityDriver struct {
	available  bool
	statements *[]string
	mu         sync.Mutex
}

func (d *vectorAvailabilityDriver) Open(string) (driver.Conn, error) {
	return &vectorAvailabilityConn{driver: d}, nil
}

type vectorAvailabilityConn struct {
	driver *vectorAvailabilityDriver
}

func (*vectorAvailabilityConn) Prepare(string) (driver.Stmt, error) {
	return nil, errors.New("prepare is unsupported")
}

func (*vectorAvailabilityConn) Close() error { return nil }

func (*vectorAvailabilityConn) Begin() (driver.Tx, error) {
	return nil, errors.New("transaction is unsupported")
}

func (c *vectorAvailabilityConn) QueryContext(_ context.Context, query string, _ []driver.NamedValue) (driver.Rows, error) {
	c.driver.mu.Lock()
	*c.driver.statements = append(*c.driver.statements, query)
	c.driver.mu.Unlock()
	return &vectorAvailabilityRows{available: c.driver.available}, nil
}

type vectorAvailabilityRows struct {
	available bool
	read      bool
}

func (*vectorAvailabilityRows) Columns() []string { return []string{"available"} }
func (*vectorAvailabilityRows) Close() error      { return nil }

func (r *vectorAvailabilityRows) Next(values []driver.Value) error {
	if r.read {
		return io.EOF
	}
	r.read = true
	values[0] = r.available
	return nil
}
