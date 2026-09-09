//go:build integration_postgres

package service

import (
	"context"
	"database/sql"
	"fmt"
	_ "github.com/lib/pq"
	"github.com/stretchr/testify/require"
	"itsm-backend/ent"
	"itsm-backend/migration"
	"net/url"
	"os"
	"testing"
	"time"
)

func TestIncidentCallbackWorkerPostgresContinuation(t *testing.T) {
	testIncidentCallbackWorkerContinuation(t, func(t *testing.T) *bpmnAuthorizationFixture {
		parsed, err := url.Parse(os.Getenv("INTAKE_POSTGRES_TEST_DSN"))
		require.NoError(t, err)
		require.Equal(t, "127.0.0.1:36444", parsed.Host)
		require.Equal(t, "/sslvpn_test", parsed.Path)
		db, err := sql.Open("postgres", parsed.String())
		require.NoError(t, err)
		t.Cleanup(func() { require.NoError(t, db.Close()) })
		schema := fmt.Sprintf("a2_callback_%d", time.Now().UnixNano())
		_, err = db.Exec("CREATE SCHEMA " + schema)
		require.NoError(t, err)
		t.Cleanup(func() { _, err := db.Exec("DROP SCHEMA " + schema + " CASCADE"); require.NoError(t, err) })
		params := parsed.Query()
		params.Set("search_path", schema)
		parsed.RawQuery = params.Encode()
		client, err := ent.Open("postgres", parsed.String())
		require.NoError(t, err)
		require.NoError(t, client.Schema.Create(context.Background()))
		scoped, err := sql.Open("postgres", parsed.String())
		require.NoError(t, err)
		defer scoped.Close()
		for _, name := range []string{"009_enable_rls_tenant_isolation", "024_incident_rule_action_receipts", "032_workitem_sla_cycle", "033_incident_status_events"} {
			_, err = scoped.Exec(migration.GetMigrationSQL(name))
			require.NoError(t, err)
		}
		return newBPMNAuthorizationFixtureWithClient(t, client)
	})
}
