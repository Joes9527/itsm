package bootstrap

import (
	"context"
	"database/sql"
	"fmt"

	"go.uber.org/zap"
)

// prepareServiceRequestTicketMigration guards the breaking schema change that
// adds a required service_requests.ticket_id column (ServiceRequest now
// delegates to a linked Ticket instead of owning its own status/approval).
// There is no source of truth to backfill ticket_id from for pre-existing
// rows (no ticket existed for them before this refactor), so — unlike
// prepareRolePermissionTenantMigration's backfill approach — this refuses to
// proceed rather than silently deleting data, mirroring
// prepareCMDBModelMigration's validation-gate pattern. Operators on a
// database with real pre-existing service_requests rows must resolve this
// explicitly (this repo's actual deployments at this stage only carry test
// data, which can be cleared manually per the error message below).
func prepareServiceRequestTicketMigration(ctx context.Context, db *sql.DB, logger *zap.SugaredLogger) error {
	if db == nil {
		return nil
	}

	var tableExists bool
	if err := db.QueryRowContext(ctx, `
		SELECT EXISTS (
			SELECT 1
			FROM information_schema.tables
			WHERE table_schema = current_schema()
			  AND table_name = 'service_requests'
		)
	`).Scan(&tableExists); err != nil {
		return fmt.Errorf("inspect service_requests table: %w", err)
	}
	if !tableExists {
		return nil
	}

	var columnExists bool
	if err := db.QueryRowContext(ctx, `
		SELECT EXISTS (
			SELECT 1
			FROM information_schema.columns
			WHERE table_schema = current_schema()
			  AND table_name = 'service_requests'
			  AND column_name = 'ticket_id'
		)
	`).Scan(&columnExists); err != nil {
		return fmt.Errorf("inspect service_requests.ticket_id: %w", err)
	}
	if columnExists {
		// Already migrated (column present from a prior successful bootstrap run).
		return nil
	}

	var rowCount int
	if err := db.QueryRowContext(ctx, `SELECT COUNT(*) FROM service_requests`).Scan(&rowCount); err != nil {
		return fmt.Errorf("count service_requests rows: %w", err)
	}
	if rowCount > 0 {
		return fmt.Errorf(
			"service_requests has %d existing row(s) but the new required ticket_id column has no backfill source "+
				"(ServiceRequest now delegates to a linked Ticket that didn't exist before this migration); "+
				"this is a breaking schema change — clear the table manually (e.g. `TRUNCATE TABLE service_requests CASCADE;`) "+
				"if this data is disposable test/dev data, or contact the team for a proper data-migration path if it is not",
			rowCount,
		)
	}
	logger.Infow("service_requests ticket_id migration preflight passed", "existing_rows", rowCount)
	return nil
}
