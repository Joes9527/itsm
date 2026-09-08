package migration

import (
	"context"
	"database/sql"
	"fmt"
)

// Run before Ent on an existing catalog, retaining legacy conflict evidence.
// Ent does not drop columns; registered 029 repeats this check and owns retirement.
func PrepareCatalogTargetClassAuthority(ctx context.Context, db *sql.DB) error {
	if db == nil {
		return fmt.Errorf("catalog authority database is required")
	}
	var exists bool
	if err := db.QueryRowContext(ctx, `SELECT to_regclass(format('%I.service_catalogs',current_schema())) IS NOT NULL`).Scan(&exists); err != nil {
		return err
	}
	if !exists {
		return nil
	}
	_, err := db.ExecContext(ctx, catalogTargetClassPreflightSQL)
	return err
}

const catalogTargetClassPreflightSQL = `DO $catalog$
DECLARE bad_ids text;
BEGIN
 LOCK TABLE service_catalogs IN ACCESS EXCLUSIVE MODE;
 IF EXISTS(SELECT 1 FROM information_schema.columns WHERE table_schema=current_schema() AND table_name='service_catalogs' AND column_name='itsm_type') THEN
  EXECUTE $check$SELECT string_agg(id::text,',' ORDER BY id) FROM service_catalogs
  WHERE itsm_type IS NULL OR itsm_type NOT IN ('Request','Incident','Change')
   OR (COALESCE(target_class,'')<>'' AND target_class IS DISTINCT FROM CASE itsm_type WHEN 'Request' THEN 'service_request_item' WHEN 'Incident' THEN 'incident' WHEN 'Change' THEN 'change_request' END)$check$ INTO bad_ids;
  IF bad_ids IS NOT NULL THEN RAISE EXCEPTION 'conflicting or unknown legacy class at Catalog IDs %; reconcile catalog identity before retry',bad_ids; END IF;
 END IF;
END $catalog$;
`

const catalogTargetClassAuthoritySQL = catalogTargetClassPreflightSQL + `DO $retire$
BEGIN
 IF EXISTS(SELECT 1 FROM information_schema.columns WHERE table_schema=current_schema() AND table_name='service_catalogs' AND column_name='itsm_type') THEN
  EXECUTE $backfill$UPDATE service_catalogs SET target_class=CASE itsm_type WHEN 'Request' THEN 'service_request_item' WHEN 'Incident' THEN 'incident' WHEN 'Change' THEN 'change_request' END WHERE COALESCE(target_class,'')=''$backfill$;
  ALTER TABLE service_catalogs DROP COLUMN itsm_type;
 END IF;
END $retire$;
` + catalogTargetClassAuthorityVerifySQL

const catalogTargetClassAuthorityVerifySQL = `DO $verify$
BEGIN
 IF EXISTS(SELECT 1 FROM information_schema.columns WHERE table_schema=current_schema() AND table_name='service_catalogs' AND column_name='itsm_type') THEN RAISE EXCEPTION 'retired Catalog itsm_type must be absent'; END IF;
END $verify$;
`
