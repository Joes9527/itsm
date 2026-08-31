package bootstrap

import (
	"context"
	"database/sql"
	"fmt"

	"go.uber.org/zap"
)

type ticketNotificationMigrationDialect string

const (
	ticketNotificationSQLite   ticketNotificationMigrationDialect = "sqlite"
	ticketNotificationPostgres ticketNotificationMigrationDialect = "postgres"
)

type ticketNotificationMigrationColumn struct {
	name       string
	sqliteType string
	pgType     string
}

var ticketNotificationDeliveryColumns = []ticketNotificationMigrationColumn{
	{name: "delivery_key", sqliteType: "TEXT", pgType: "TEXT"},
	{name: "attempt_count", sqliteType: "INTEGER", pgType: "BIGINT"},
	{name: "next_attempt_at", sqliteType: "DATETIME", pgType: "TIMESTAMPTZ"},
	{name: "lease_owner", sqliteType: "TEXT", pgType: "TEXT"},
	{name: "lease_expires_at", sqliteType: "DATETIME", pgType: "TIMESTAMPTZ"},
	{name: "last_error_class", sqliteType: "TEXT", pgType: "VARCHAR(128)"},
}

// prepareTicketNotificationMigration makes populated pre-delivery-worker tables
// compatible with Ent's final required schema without discarding delivery work.
func prepareTicketNotificationMigration(ctx context.Context, db *sql.DB, logger *zap.SugaredLogger) error {
	if db == nil {
		return nil
	}
	dialect, tableExists, columns, err := inspectTicketNotificationTable(ctx, db)
	if err != nil {
		return err
	}
	if !tableExists {
		return nil
	}

	tx, err := db.BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("begin ticket notification compatibility migration: %w", err)
	}
	defer tx.Rollback()
	for _, column := range ticketNotificationDeliveryColumns {
		if columns[column.name] {
			continue
		}
		columnType := column.pgType
		if dialect == ticketNotificationSQLite {
			columnType = column.sqliteType
		}
		if _, err := tx.ExecContext(ctx, fmt.Sprintf(
			"ALTER TABLE ticket_notifications ADD COLUMN %s %s",
			column.name,
			columnType,
		)); err != nil {
			return fmt.Errorf("add ticket_notifications.%s: %w", column.name, err)
		}
	}

	if _, err := tx.ExecContext(ctx, `
		UPDATE ticket_notifications
		SET delivery_key = 'ticket-notification-legacy-' || CAST(id AS TEXT),
		    status = 'pending',
		    attempt_count = 0,
		    next_attempt_at = COALESCE(created_at, CURRENT_TIMESTAMP),
		    lease_owner = NULL,
		    lease_expires_at = NULL,
		    last_error_class = NULL
		WHERE channel <> 'in_app'
		  AND status IN ('pending', 'failed')
		  AND delivery_key IS NULL
	`); err != nil {
		return fmt.Errorf("reconcile legacy ticket notification delivery work: %w", err)
	}
	if _, err := tx.ExecContext(ctx, `
		UPDATE ticket_notifications
		SET attempt_count = COALESCE(attempt_count, 0),
		    next_attempt_at = COALESCE(next_attempt_at, created_at, CURRENT_TIMESTAMP)
	`); err != nil {
		return fmt.Errorf("backfill ticket notification delivery state: %w", err)
	}
	if err := tx.Commit(); err != nil {
		return fmt.Errorf("commit ticket notification compatibility migration: %w", err)
	}
	if logger != nil {
		logger.Debugw("ticket notification delivery schema prepared")
	}
	return nil
}

func inspectTicketNotificationTable(
	ctx context.Context,
	db *sql.DB,
) (ticketNotificationMigrationDialect, bool, map[string]bool, error) {
	rows, err := db.QueryContext(ctx, `PRAGMA table_info(ticket_notifications)`)
	if err == nil {
		defer rows.Close()
		columns := make(map[string]bool)
		for rows.Next() {
			var cid, notNull, primaryKey int
			var name, columnType string
			var defaultValue sql.NullString
			if err := rows.Scan(&cid, &name, &columnType, &notNull, &defaultValue, &primaryKey); err != nil {
				return "", false, nil, fmt.Errorf("inspect SQLite ticket_notifications columns: %w", err)
			}
			columns[name] = true
		}
		if err := rows.Err(); err != nil {
			return "", false, nil, fmt.Errorf("inspect SQLite ticket_notifications table: %w", err)
		}
		return ticketNotificationSQLite, len(columns) > 0, columns, nil
	}

	var tableExists bool
	if err := db.QueryRowContext(ctx, `
		SELECT EXISTS (
			SELECT 1 FROM information_schema.tables
			WHERE table_schema = current_schema() AND table_name = 'ticket_notifications'
		)
	`).Scan(&tableExists); err != nil {
		return "", false, nil, fmt.Errorf("inspect ticket_notifications table: %w", err)
	}
	columns := make(map[string]bool)
	if !tableExists {
		return ticketNotificationPostgres, false, columns, nil
	}
	columnRows, err := db.QueryContext(ctx, `
		SELECT column_name FROM information_schema.columns
		WHERE table_schema = current_schema() AND table_name = 'ticket_notifications'
	`)
	if err != nil {
		return "", false, nil, fmt.Errorf("inspect ticket_notifications columns: %w", err)
	}
	defer columnRows.Close()
	for columnRows.Next() {
		var name string
		if err := columnRows.Scan(&name); err != nil {
			return "", false, nil, fmt.Errorf("scan ticket_notifications column: %w", err)
		}
		columns[name] = true
	}
	if err := columnRows.Err(); err != nil {
		return "", false, nil, fmt.Errorf("inspect ticket_notifications columns: %w", err)
	}
	return ticketNotificationPostgres, true, columns, nil
}
