package migration

import (
	"context"
	"database/sql"
	"fmt"
)

// Existing migration SQL is immutable ledger history. Automatic bootstrap may
// reconcile an already-canonical schema, but cannot retire retained evidence.
// A separately reviewed cutover is required; there is deliberately no bypass flag.
func blockAutomaticWorkItemRetirement(ctx context.Context, tx *sql.Tx, version string) error {
	var objects, requiredTables string
	var requiredCount int
	switch version {
	case "022_drop_professional_extension_shared_fields":
		requiredTables, requiredCount = "('tickets','incidents','problems','changes')", 4
		objects = `
SELECT object_name FROM (
    SELECT c.relname::text AS object_name
    FROM pg_class c JOIN pg_namespace n ON n.oid=c.relnamespace
    WHERE n.nspname=current_schema()
      AND c.relname IN ('ticket_approvals','workflow_tasks','workflow_instances','workflow_versions','workflows')
    UNION ALL
    SELECT c.relname || '.' || a.attname
    FROM pg_attribute a JOIN pg_class c ON c.oid=a.attrelid
    JOIN pg_namespace n ON n.oid=c.relnamespace
    WHERE n.nspname=current_schema() AND a.attnum>0 AND NOT a.attisdropped AND (
      (c.relname='incidents' AND a.attname IN ('title','description','status','priority','reporter_id','assignee_id','category','subcategory','source','tenant_id','version','created_at','updated_at','resolved_at','closed_at','deleted_at')) OR
      (c.relname='problems' AND a.attname IN ('title','description','status','priority','category','assignee_id','created_by','tenant_id','created_at','updated_at','resolved_at','closed_at','deleted_at')) OR
      (c.relname='changes' AND a.attname IN ('title','description','status','priority','assignee_id','created_by','tenant_id','related_tickets','created_at','updated_at')) OR
      (c.relname='releases' AND a.attname='requires_approval') OR
      (c.relname='ticket_categories' AND a.attname='workflow_id')
    )
) retained ORDER BY object_name LIMIT 1`
	case "027_work_item_identity_field_retirement":
		requiredTables, requiredCount = "('tickets','incidents')", 2
		objects = `
SELECT c.relname || '.' || a.attname
FROM pg_attribute a JOIN pg_class c ON c.oid=a.attrelid
JOIN pg_namespace n ON n.oid=c.relnamespace
WHERE n.nspname=current_schema() AND a.attnum>0 AND NOT a.attisdropped AND (
(c.relname='tickets' AND a.attname='type') OR
(c.relname='incidents' AND a.attname='incident_number')
) ORDER BY c.relname,a.attname LIMIT 1`
	default:
		return nil
	}
	// Historical 027 uses unqualified table references. Reject an incomplete
	// selected schema before search_path can resolve them in another namespace.
	var present int
	if err := tx.QueryRowContext(ctx, "SELECT count(*) FROM pg_class c JOIN pg_namespace n ON n.oid=c.relnamespace WHERE n.nspname=current_schema() AND c.relkind IN ('r','p') AND c.relname IN "+requiredTables).Scan(&present); err != nil {
		return fmt.Errorf("inspect canonical WorkItem schema: %w", err)
	}
	if present != requiredCount {
		return fmt.Errorf("automatic WorkItem retirement is blocked: migration %s requires all canonical tables in the selected schema; search_path fallback is forbidden", version)
	}
	var object string
	err := tx.QueryRowContext(ctx, objects).Scan(&object)
	if err == sql.ErrNoRows {
		return nil
	}
	if err != nil {
		return fmt.Errorf("inspect retained WorkItem structure: %w", err)
	}
	return fmt.Errorf("automatic WorkItem retirement is blocked: migration %s would remove %s; preserve the schema and follow docs/deployment/workitem-convergence-cutover.md", version, object)
}
