//go:build candidate_scope

package integration

import (
	"context"
	"database/sql"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/stretchr/testify/require"
	"itsm-backend/ent"
)

// Shared admission for task-private PostgreSQL tests; never accepts a general
// connection URL or falls back to a configured application database.
func candidatePrivatePostgresDSN(t *testing.T) (string, func(string, string) string) {
	t.Helper()
	socket := os.Getenv("CANDIDATE_SCOPE_TEST_SOCKET")
	if socket == "" {
		t.Skip("requires an explicitly isolated PostgreSQL socket")
	}
	require.True(t, filepath.IsAbs(socket))
	marker, err := os.ReadFile(filepath.Join(socket, "candidate-test-instance"))
	require.NoError(t, err)
	require.Equal(t, "itsm-candidate-isolated-test\n", string(marker))
	return socket, func(db, role string) string {
		return fmt.Sprintf("host=%s port=25439 dbname=%s user=%s sslmode=disable", socket, db, role)
	}
}

// Owns only its randomly named database. Callers supply their domain schema;
// owner-level tests do not establish restricted-role candidate admission.
func newCandidatePrivatePostgresClient(t *testing.T, ctx context.Context) *ent.Client {
	t.Helper()
	_, dsn := candidatePrivatePostgresDSN(t)
	name := "incident_mail_" + strings.ReplaceAll(uuid.NewString(), "-", "")
	admin, err := sql.Open("postgres", dsn("postgres", "candidate_test_owner"))
	require.NoError(t, err)
	t.Cleanup(func() { require.NoError(t, admin.Close()) })
	_, err = admin.ExecContext(ctx, "CREATE DATABASE "+name)
	require.NoError(t, err)
	t.Cleanup(func() {
		cleanup, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel()
		_, err := admin.ExecContext(cleanup, "DROP DATABASE "+name+" WITH (FORCE)")
		require.NoError(t, err)
	})
	client, err := ent.Open("postgres", dsn(name, "candidate_test_owner"))
	require.NoError(t, err)
	t.Cleanup(func() { require.NoError(t, client.Close()) })
	return client
}
