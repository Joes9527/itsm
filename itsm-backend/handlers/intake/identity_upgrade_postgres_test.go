//go:build integration_postgres

package intake

import (
	"context"
	"database/sql"
	"fmt"
	"os"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

func TestPostgresPreparedIdentityUpgradeFindsActualUniqueObjects(t *testing.T) {
	dsn := os.Getenv("INTAKE_POSTGRES_TEST_DSN")
	if dsn == "" {
		t.Skip("disposable INTAKE_POSTGRES_TEST_DSN is required")
	}
	db, err := sql.Open("postgres", dsn)
	require.NoError(t, err)
	defer db.Close()
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	conn, err := db.Conn(ctx)
	require.NoError(t, err)
	defer conn.Close()
	schema := fmt.Sprintf("a3_identity_upgrade_%d", time.Now().UnixNano())
	_, err = conn.ExecContext(ctx, `CREATE SCHEMA `+schema)
	require.NoError(t, err)
	defer func() {
		_, err := conn.ExecContext(context.Background(), `DROP SCHEMA `+schema+` CASCADE`)
		require.NoError(t, err)
	}()
	_, err = conn.ExecContext(ctx, `SET search_path TO `+schema)
	require.NoError(t, err)
	_, err = conn.ExecContext(ctx, `
 CREATE TABLE tickets(id bigint PRIMARY KEY, tenant_id bigint NOT NULL, record_class text NOT NULL, type text NOT NULL, ticket_number text NOT NULL, UNIQUE(tenant_id,ticket_number));
 CREATE TABLE incidents(id bigint PRIMARY KEY, work_item_id bigint NOT NULL UNIQUE REFERENCES tickets(id), incident_number text NOT NULL, CONSTRAINT historical_vendor_inc_number UNIQUE(incident_number));
 CREATE UNIQUE INDEX independently_named_legacy_incident_number ON incidents(incident_number);
 INSERT INTO tickets VALUES (1,1,'incident','incident','TKT-202609-000001'),(2,2,'incident','incident','TKT-202609-000001'),(3,1,'generic','improvement','TKT-202609-000002');
 INSERT INTO incidents VALUES(1,1,'TKT-202609-000001');
 `)
	require.NoError(t, err)
	_, err = conn.ExecContext(ctx, `INSERT INTO incidents VALUES(2,2,'TKT-202609-000001')`)
	require.Error(t, err, "legacy global uniqueness must reproduce the tenant collision")
	prepared, err := os.ReadFile("../../migrations/workitem_creation_identity_prepare.sql")
	require.NoError(t, err)
	_, err = conn.ExecContext(ctx, string(prepared))
	require.NoError(t, err)
	_, err = conn.ExecContext(ctx, `INSERT INTO incidents VALUES(2,2,'TKT-202609-000001')`)
	require.NoError(t, err)
	var subtype string
	require.NoError(t, conn.QueryRowContext(ctx, `SELECT generic_subtype FROM tickets WHERE id=3`).Scan(&subtype))
	require.Equal(t, "improvement", subtype)
	_, err = conn.ExecContext(ctx, `INSERT INTO incidents VALUES(3,2,'different')`)
	require.Error(t, err, "WorkItem one-to-one uniqueness must remain")
	_, err = conn.ExecContext(ctx, `INSERT INTO tickets VALUES(4,1,'incident','incident','TKT-202609-000001',NULL)`)
	require.Error(t, err, "WorkItem tenant-number uniqueness must remain")
	_, err = conn.ExecContext(ctx, string(prepared))
	require.NoError(t, err, "preparation must be repeatable")
}
