package service

import (
	"context"
	"database/sql"
	"fmt"
	"testing"

	"itsm-backend/ent"
	"itsm-backend/ent/enttest"

	_ "github.com/mattn/go-sqlite3"
	"github.com/stretchr/testify/require"
)

func openBPMNSchemaTestClient(t *testing.T) *ent.Client {
	t.Helper()

	client := enttest.Open(t, "sqlite3", "file:bpmn_callback_outbox_schema?mode=memory&cache=shared&_fk=1")
	t.Cleanup(func() { _ = client.Close() })
	return client
}

func TestProcessCallbackOutboxSchemaEnforcesExecutionKeyAndTenant(t *testing.T) {
	client := openBPMNSchemaTestClient(t)
	ctx := context.Background()
	require.NoError(t, client.Schema.Create(ctx))

	row := client.ProcessCallbackOutbox.Create().
		SetExecutionKey("bpmn-callback-00000001").
		SetTenantID(7).
		SetProcessInstanceID(101).
		SetProcessTaskID(202).
		SetCallbackKind("service_task").
		SetHandlerID("webhook_handler").
		SetTaskType("webhook").
		SetElementID("Activity_Notify").
		SetVariables(map[string]interface{}{"ticket_id": 42}).
		SetStatus("pending").
		SaveX(ctx)
	require.Equal(t, 7, row.TenantID)
	require.Equal(t, 0, row.AttemptCount)
	require.False(t, row.NextAttemptAt.IsZero())

	_, err := client.ProcessCallbackOutbox.Create().
		SetExecutionKey(row.ExecutionKey).
		SetTenantID(7).
		SetProcessInstanceID(101).
		SetCallbackKind("service_task").
		SetHandlerID("webhook_handler").
		SetTaskType("webhook").
		SetElementID("Activity_Notify").
		SetStatus("pending").
		Save(ctx)
	require.Error(t, err, "execution_key must be globally unique and opaque")
}

func TestProcessCallbackOutboxSchemaUsesTenantScopedOperationalIndexes(t *testing.T) {
	client := openBPMNSchemaTestClient(t)
	require.NoError(t, client.Schema.Create(context.Background()))

	db, err := sql.Open("sqlite3", "file:bpmn_callback_outbox_schema?mode=memory&cache=shared&_fk=1")
	require.NoError(t, err)
	t.Cleanup(func() { _ = db.Close() })

	for _, columns := range [][]string{
		{"tenant_id", "status", "next_attempt_at"},
		{"tenant_id", "status", "lease_expires_at"},
		{"tenant_id", "process_instance_id", "status"},
		{"tenant_id", "process_task_id"},
	} {
		requireProcessCallbackOutboxIndex(t, db, columns)
	}
}

func requireProcessCallbackOutboxIndex(t *testing.T, db *sql.DB, wantColumns []string) {
	t.Helper()

	indexes, err := db.Query("PRAGMA index_list('process_callback_outboxes')")
	require.NoError(t, err)
	defer indexes.Close()

	for indexes.Next() {
		var sequence, unique, partial int
		var name, origin string
		require.NoError(t, indexes.Scan(&sequence, &name, &unique, &origin, &partial))

		columns, err := sqliteIndexColumns(db, name)
		require.NoError(t, err)
		if len(columns) != len(wantColumns) {
			continue
		}

		matches := true
		for i := range columns {
			if columns[i] != wantColumns[i] {
				matches = false
				break
			}
		}
		if matches {
			return
		}
	}
	require.NoError(t, indexes.Err())
	t.Fatalf("process_callback_outboxes is missing tenant-scoped index on %v", wantColumns)
}

func sqliteIndexColumns(db *sql.DB, indexName string) ([]string, error) {
	rows, err := db.Query(fmt.Sprintf("PRAGMA index_info(%q)", indexName))
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var columns []string
	for rows.Next() {
		var sequence, columnID int
		var name string
		if err := rows.Scan(&sequence, &columnID, &name); err != nil {
			return nil, err
		}
		columns = append(columns, name)
	}
	return columns, rows.Err()
}
