package migration

import (
	"context"
	"database/sql"
	"fmt"

	"github.com/lib/pq"
)

// Every destructive historical operation is allowed only when its exact removal
// targets are absent. SQL and checksums stay unchanged; no receipt is invented.
func blockHistoricalDestruction(ctx context.Context, q migrationQuery, version string) error {
	tables := []string{}
	columns := map[string][]string{}
	switch version {
	case "012_drop_service_catalog_item":
		tables = []string{"service_catalog_items"}
		columns["service_catalogs"] = []string{"form_schema"}
	case "013_service_request_delegates_to_ticket":
		tables = []string{"service_request_approvals"}
		columns["service_requests"] = []string{"status", "title", "reason", "current_level", "total_levels", "current_approver", "approved_at", "approver_comment", "approval_history"}
	case "014_drop_legacy_approval_workflow":
		tables = []string{"approval_records", "approval_workflows"}
	case "017_drop_ticket_type_legacy_approval_fields":
		columns["ticket_types"] = []string{"approval_workflow_id", "approval_chain"}
	case "028_service_request_work_item_authority":
		columns["service_requests"] = []string{"tenant_id", "requester_id", "processor_id", "version", "created_at", "updated_at", "deleted_at"}
	case "029_catalog_target_class_authority":
		columns["service_catalogs"] = []string{"itsm_type"}
	default:
		return nil
	}
	for _, table := range tables {
		var found bool
		if err := q.QueryRowContext(ctx, "SELECT EXISTS(SELECT 1 FROM pg_class WHERE relnamespace=current_schema()::regnamespace AND relname=$1)", table).Scan(&found); err != nil {
			return err
		}
		if found {
			return fmt.Errorf("historical destruction blocked: %s would remove %s", version, table)
		}
	}
	for table, cols := range columns {
		for _, col := range cols {
			var found bool
			if err := q.QueryRowContext(ctx, "SELECT EXISTS(SELECT 1 FROM pg_attribute a JOIN pg_class c ON c.oid=a.attrelid WHERE c.relnamespace=current_schema()::regnamespace AND c.relname=$1 AND a.attname=$2 AND a.attnum>0 AND NOT a.attisdropped)", table, col).Scan(&found); err != nil {
				return err
			}
			if found {
				return fmt.Errorf("historical destruction blocked: %s would remove %s.%s", version, table, col)
			}
		}
	}
	if version == "013_service_request_delegates_to_ticket" {
		var exists bool
		if err := q.QueryRowContext(ctx, "SELECT EXISTS(SELECT 1 FROM pg_class WHERE relnamespace=current_schema()::regnamespace AND relname='field_values')").Scan(&exists); err != nil {
			return err
		}
		if exists {
			var rows bool
			if err := q.QueryRowContext(ctx, "SELECT EXISTS(SELECT 1 FROM field_values WHERE entity_type='service_request')").Scan(&rows); err != nil {
				return err
			}
			if rows {
				return fmt.Errorf("historical destruction blocked: 013 would delete service_request field_values")
			}
		}
	}
	return nil
}

// Lock existing parents before the final no-effect check. This prevents concurrent
// business inserts or ALTER COLUMN from turning a no-op into a historical deletion.
func lockHistoricalDestructionParents(ctx context.Context, tx *sql.Tx, version string) error {
	var parents []string
	switch version {
	case "012_drop_service_catalog_item":
		parents = []string{"service_catalogs", "service_catalog_items"}
	case "013_service_request_delegates_to_ticket":
		parents = []string{"service_requests", "service_request_approvals", "field_values"}
	case "014_drop_legacy_approval_workflow":
		parents = []string{"approval_records", "approval_workflows"}
	case "017_drop_ticket_type_legacy_approval_fields":
		parents = []string{"ticket_types"}
	case "028_service_request_work_item_authority":
		parents = []string{"service_requests"}
	case "029_catalog_target_class_authority":
		parents = []string{"service_catalogs"}
	}
	schema, err := migrationTargetSchema(ctx, tx)
	if err != nil {
		return err
	}
	for _, table := range parents {
		var exists bool
		if err = tx.QueryRowContext(ctx, "SELECT EXISTS(SELECT 1 FROM pg_class WHERE relnamespace=$1::regnamespace AND relname=$2 AND relkind IN ('r','p'))", schema, table).Scan(&exists); err != nil {
			return err
		}
		if exists {
			if _, err = tx.ExecContext(ctx, "LOCK TABLE "+pq.QuoteIdentifier(schema)+"."+pq.QuoteIdentifier(table)+" IN ACCESS EXCLUSIVE MODE"); err != nil {
				return err
			}
		}
	}
	return nil
}
