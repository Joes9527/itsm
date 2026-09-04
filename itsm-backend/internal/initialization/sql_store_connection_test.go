package initialization

import (
	"context"
	"database/sql"
	"testing"

	_ "github.com/mattn/go-sqlite3"
	"github.com/stretchr/testify/require"
)

func TestSQLStoreRunsOnDedicatedDatabaseConnection(t *testing.T) {
	db, err := sql.Open("sqlite3", ":memory:")
	require.NoError(t, err)
	t.Cleanup(func() { require.NoError(t, db.Close()) })
	conn, err := db.Conn(context.Background())
	require.NoError(t, err)
	t.Cleanup(func() { require.NoError(t, conn.Close()) })

	store, err := NewSQLStoreOnConnection(conn)
	require.NoError(t, err)
	require.NotNil(t, store)
}
