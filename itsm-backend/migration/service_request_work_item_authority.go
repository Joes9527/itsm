package migration

import (
	"context"
	"database/sql"
	"fmt"
)

// PrepareServiceRequestWorkItemAuthority validates historical ownership before Ent adds its FK.
func PrepareServiceRequestWorkItemAuthority(ctx context.Context, db *sql.DB) error {
	if db == nil {
		return fmt.Errorf("ServiceRequest authority preflight requires database")
	}
	var exists bool
	if err := db.QueryRowContext(ctx, `SELECT EXISTS(SELECT 1 FROM information_schema.columns WHERE table_schema=current_schema() AND table_name='service_requests' AND column_name='ticket_id')`).Scan(&exists); err != nil {
		return err
	}
	if !exists {
		return nil
	}
	_, err := db.ExecContext(ctx, serviceRequestWorkItemAuthorityPreflightSQL)
	return err
}

const serviceRequestWorkItemAuthorityPreflightSQL = `
DO $preflight$
DECLARE bad text; col text; target text;
BEGIN
 SELECT string_agg(sr.id::text, ', ' ORDER BY sr.id) INTO bad FROM service_requests sr LEFT JOIN tickets w ON w.id=sr.ticket_id WHERE w.id IS NULL OR w.record_class IS DISTINCT FROM 'service_request_item';
 IF bad IS NOT NULL THEN RAISE EXCEPTION 'ServiceRequest IDs % have missing or invalid WorkItem ownership', bad; END IF;
 SELECT string_agg(id::text, ', ' ORDER BY id) INTO bad FROM service_requests WHERE ticket_id IN (SELECT ticket_id FROM service_requests GROUP BY ticket_id HAVING count(*)>1);
 IF bad IS NOT NULL THEN RAISE EXCEPTION 'ServiceRequest IDs % share duplicate WorkItem ownership', bad; END IF;
 FOREACH col IN ARRAY ARRAY['tenant_id','requester_id','processor_id','version','created_at','updated_at','deleted_at'] LOOP
  IF EXISTS(SELECT 1 FROM information_schema.columns WHERE table_schema=current_schema() AND table_name='service_requests' AND column_name=col) THEN
   target := CASE WHEN col='processor_id' THEN 'assignee_id' ELSE col END;
   EXECUTE format('SELECT string_agg(sr.id::text, '', '' ORDER BY sr.id) FROM service_requests sr JOIN tickets w ON w.id=sr.ticket_id WHERE sr.%I IS DISTINCT FROM w.%I', col,target) INTO bad;
   IF bad IS NOT NULL THEN RAISE EXCEPTION 'ServiceRequest IDs % conflict with WorkItem field %; explicit data remediation required',bad,col; END IF;
  END IF;
 END LOOP;
END $preflight$;
`

const serviceRequestWorkItemAuthoritySQL = serviceRequestWorkItemAuthorityPreflightSQL + `
ALTER TABLE service_requests ALTER COLUMN ticket_id SET NOT NULL;
CREATE UNIQUE INDEX IF NOT EXISTS servicerequest_ticket_id ON service_requests(ticket_id);
DO $fk$
BEGIN
 IF NOT EXISTS(SELECT 1 FROM pg_constraint WHERE conrelid='service_requests'::regclass AND conname='service_requests_work_item_fk') THEN
  ALTER TABLE service_requests ADD CONSTRAINT service_requests_work_item_fk FOREIGN KEY(ticket_id) REFERENCES tickets(id) ON DELETE NO ACTION ON UPDATE NO ACTION;
 END IF;
END $fk$;
ALTER TABLE service_requests ENABLE ROW LEVEL SECURITY;
ALTER TABLE service_requests FORCE ROW LEVEL SECURITY;
DO $policies$
DECLARE p record;
BEGIN
 FOR p IN SELECT polname FROM pg_policy WHERE polrelid='service_requests'::regclass LOOP
  IF p.polname <> 'tenant_isolation_service_requests' THEN RAISE EXCEPTION 'unexpected ServiceRequest RLS policy %', p.polname; END IF;
 END LOOP;
END $policies$;
DROP POLICY IF EXISTS tenant_isolation_service_requests ON service_requests;
CREATE POLICY tenant_isolation_service_requests ON service_requests USING (EXISTS(SELECT 1 FROM tickets w WHERE w.id=service_requests.ticket_id AND w.record_class='service_request_item' AND w.tenant_id=NULLIF(current_setting('app.current_tenant',true),'')::bigint)) WITH CHECK (EXISTS(SELECT 1 FROM tickets w WHERE w.id=service_requests.ticket_id AND w.record_class='service_request_item' AND w.tenant_id=NULLIF(current_setting('app.current_tenant',true),'')::bigint));
ALTER TABLE service_requests DROP COLUMN IF EXISTS tenant_id, DROP COLUMN IF EXISTS requester_id, DROP COLUMN IF EXISTS processor_id, DROP COLUMN IF EXISTS version, DROP COLUMN IF EXISTS created_at, DROP COLUMN IF EXISTS updated_at, DROP COLUMN IF EXISTS deleted_at;
` + serviceRequestWorkItemAuthorityVerifySQL

const serviceRequestWorkItemAuthorityVerifySQL = `
DO $verify$
BEGIN
 IF EXISTS(SELECT 1 FROM information_schema.columns WHERE table_schema=current_schema() AND table_name='service_requests' AND column_name=ANY(ARRAY['tenant_id','requester_id','processor_id','version','created_at','updated_at','deleted_at'])) THEN RAISE EXCEPTION 'ServiceRequest shared columns must be absent'; END IF;
 IF NOT EXISTS(SELECT 1 FROM pg_attribute WHERE attrelid='service_requests'::regclass AND attname='ticket_id' AND attnotnull AND NOT attisdropped) THEN RAISE EXCEPTION 'ServiceRequest WorkItem must be required'; END IF;
 IF (SELECT count(*) FROM pg_constraint c JOIN pg_attribute a ON a.attrelid=c.conrelid AND a.attnum=c.conkey[1] WHERE c.conrelid='service_requests'::regclass AND c.contype='f' AND a.attname='ticket_id') <> 1 OR NOT EXISTS(SELECT 1 FROM pg_constraint c JOIN pg_attribute a ON a.attrelid=c.conrelid AND a.attnum=c.conkey[1] JOIN pg_attribute b ON b.attrelid=c.confrelid AND b.attnum=c.confkey[1] WHERE c.conrelid='service_requests'::regclass AND c.confrelid='tickets'::regclass AND c.conname='service_requests_work_item_fk' AND c.contype='f' AND c.convalidated AND NOT c.condeferrable AND cardinality(c.conkey)=1 AND cardinality(c.confkey)=1 AND a.attname='ticket_id' AND b.attname='id' AND c.confdeltype='a' AND c.confupdtype='a') THEN RAISE EXCEPTION 'ServiceRequest WorkItem foreign key must be exact'; END IF;
 IF NOT EXISTS(SELECT 1 FROM pg_index i JOIN pg_attribute a ON a.attrelid=i.indrelid AND a.attnum=i.indkey[0] WHERE i.indrelid='service_requests'::regclass AND i.indisunique AND i.indisvalid AND i.indisready AND i.indnkeyatts=1 AND i.indnatts=1 AND i.indpred IS NULL AND i.indexprs IS NULL AND a.attname='ticket_id') THEN RAISE EXCEPTION 'ServiceRequest WorkItem must be unique'; END IF;
 IF NOT EXISTS(SELECT 1 FROM pg_class WHERE oid='service_requests'::regclass AND relrowsecurity AND relforcerowsecurity) OR (SELECT count(*) FROM pg_policy WHERE polrelid='service_requests'::regclass) <> 1 THEN RAISE EXCEPTION 'ServiceRequest RLS must be enforced'; END IF;
END $verify$;
`
