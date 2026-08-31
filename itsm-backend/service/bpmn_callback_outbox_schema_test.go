package service

import (
	"context"
	"database/sql"
	"fmt"
	"reflect"
	"testing"
	"time"

	"itsm-backend/ent"
	"itsm-backend/ent/enttest"
	"itsm-backend/ent/migrate"
	entschema "itsm-backend/ent/schema"

	sqlschema "entgo.io/ent/dialect/sql/schema"
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

func TestTicketCCSchemaUsesNullableCallbackDeliveryKey(t *testing.T) {
	var deliveryKeyFound bool
	for _, schemaField := range (entschema.TicketCC{}).Fields() {
		descriptor := schemaField.Descriptor()
		if descriptor.Name != "delivery_key" {
			continue
		}
		deliveryKeyFound = true
		require.True(t, descriptor.Optional)
		require.True(t, descriptor.Nillable)
		require.True(t, descriptor.Sensitive)
	}
	require.True(t, deliveryKeyFound, "TicketCC must define callback delivery_key")

	var callbackIndexFound bool
	for _, schemaIndex := range (entschema.TicketCC{}).Indexes() {
		descriptor := schemaIndex.Descriptor()
		require.False(t,
			reflect.DeepEqual(descriptor.Fields, []string{"tenant_id", "ticket_id", "user_id"}),
			"ordinary TicketCC relations must not have an unconditional natural unique index",
		)
		if reflect.DeepEqual(descriptor.Fields, []string{"tenant_id", "delivery_key", "user_id"}) {
			callbackIndexFound = descriptor.Unique
		}
	}
	require.True(t, callbackIndexFound, "TicketCC must have callback-only tenant delivery uniqueness")
}

func TestTicketCCMigrationCompatibilitySQLite(t *testing.T) {
	dsn := "file:ticket_cc_migration_compatibility?mode=memory&cache=shared&_fk=1"
	db, err := sql.Open("sqlite3", dsn)
	require.NoError(t, err)
	t.Cleanup(func() { require.NoError(t, db.Close()) })

	ctx := context.Background()
	_, err = db.ExecContext(ctx, `
		CREATE TABLE tickets (id integer NOT NULL PRIMARY KEY AUTOINCREMENT);
		CREATE TABLE ticket_ccs (
			id integer NOT NULL PRIMARY KEY AUTOINCREMENT,
			user_id integer NOT NULL,
			added_by integer NOT NULL,
			tenant_id integer NOT NULL,
			added_at datetime NOT NULL,
			is_active bool NOT NULL DEFAULT true,
			ticket_id integer NOT NULL,
			FOREIGN KEY (ticket_id) REFERENCES tickets(id)
		);
		INSERT INTO tickets (id) VALUES (41);
	`)
	require.NoError(t, err)

	now := time.Date(2026, 8, 31, 12, 0, 0, 0, time.UTC)
	for _, active := range []bool{true, false, false} {
		_, err = db.ExecContext(ctx, `
			INSERT INTO ticket_ccs (user_id, added_by, tenant_id, added_at, is_active, ticket_id)
			VALUES (?, ?, ?, ?, ?, ?)
		`, 73, 11, 29, now, active, 41)
		require.NoError(t, err)
	}

	client, err := ent.Open("sqlite3", dsn)
	require.NoError(t, err)
	t.Cleanup(func() { require.NoError(t, client.Close()) })
	require.NoError(t, migrate.Create(
		ctx,
		client.Schema,
		[]*sqlschema.Table{ticketCCMigrationTableWithoutForeignKeys()},
	))

	var rowCount, nullDeliveryKeys int
	require.NoError(t, db.QueryRowContext(ctx, "SELECT COUNT(*) FROM ticket_ccs").Scan(&rowCount))
	require.NoError(t, db.QueryRowContext(ctx, "SELECT COUNT(*) FROM ticket_ccs WHERE delivery_key IS NULL").Scan(&nullDeliveryKeys))
	require.Equal(t, 3, rowCount)
	require.Equal(t, rowCount, nullDeliveryKeys)
	requireSQLiteUniqueIndex(t, db, []string{"tenant_id", "delivery_key", "user_id"})
	requireSQLiteIndexAbsent(t, db, []string{"tenant_id", "ticket_id", "user_id"})
}

func ticketCCMigrationTableWithoutForeignKeys() *sqlschema.Table {
	table := *migrate.TicketCcsTable
	table.ForeignKeys = nil
	return &table
}

func requireSQLiteUniqueIndex(t *testing.T, db *sql.DB, wantColumns []string) {
	t.Helper()
	found, unique := findSQLiteIndex(t, db, wantColumns)
	require.True(t, found, "missing SQLite index on %v", wantColumns)
	require.True(t, unique, "SQLite index on %v must be unique", wantColumns)
}

func requireSQLiteIndexAbsent(t *testing.T, db *sql.DB, wantColumns []string) {
	t.Helper()
	found, _ := findSQLiteIndex(t, db, wantColumns)
	require.False(t, found, "unexpected SQLite index on %v", wantColumns)
}

func findSQLiteIndex(t *testing.T, db *sql.DB, wantColumns []string) (bool, bool) {
	t.Helper()
	indexes, err := db.Query("PRAGMA index_list('ticket_ccs')")
	require.NoError(t, err)
	defer indexes.Close()

	for indexes.Next() {
		var sequence, unique, partial int
		var name, origin string
		require.NoError(t, indexes.Scan(&sequence, &name, &unique, &origin, &partial))
		columns, err := sqliteIndexColumns(db, name)
		require.NoError(t, err)
		if reflect.DeepEqual(columns, wantColumns) {
			return true, unique == 1
		}
	}
	require.NoError(t, indexes.Err())
	return false, false
}
