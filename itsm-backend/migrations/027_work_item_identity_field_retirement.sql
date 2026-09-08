
DO $retire$
DECLARE bad_ids text;
BEGIN
 LOCK TABLE tickets, incidents IN ACCESS EXCLUSIVE MODE;
 IF EXISTS(SELECT 1 FROM information_schema.columns WHERE table_schema=current_schema() AND table_name='tickets' AND column_name='type') THEN
  EXECUTE $check$SELECT string_agg(id::text, ',' ORDER BY id) FROM tickets WHERE COALESCE(type,'')<>'' AND NOT (
   (record_class='generic' AND type=COALESCE(generic_subtype,'') AND type NOT IN ('incident','problem','change','change_request','service_request','service_request_item','catalog_task'))
   OR (record_class='incident' AND type='incident') OR (record_class='problem' AND type='problem')
   OR (record_class='change_request' AND type IN ('change','change_request'))
   OR (record_class='service_request_item' AND type IN ('service_request','service_request_item'))
   OR (record_class='catalog_task' AND type='catalog_task'))$check$ INTO bad_ids;
  IF bad_ids IS NOT NULL THEN RAISE EXCEPTION 'conflicting legacy Ticket type at WorkItem IDs %; reconcile identity before retry',bad_ids; END IF;
 END IF;
 SELECT string_agg(i.id::text,',' ORDER BY i.id) INTO bad_ids FROM incidents i LEFT JOIN tickets t ON t.id=i.work_item_id
 WHERE t.id IS NULL OR t.record_class IS DISTINCT FROM 'incident' OR (SELECT count(*) FROM incidents d WHERE d.work_item_id=i.work_item_id)<>1;
 IF bad_ids IS NOT NULL THEN RAISE EXCEPTION 'invalid Incident WorkItem ownership at Incident IDs %; reconcile identity before retry',bad_ids; END IF;
 IF EXISTS(SELECT 1 FROM information_schema.columns WHERE table_schema=current_schema() AND table_name='incidents' AND column_name='incident_number') THEN
  EXECUTE $check$SELECT string_agg(i.id::text,',' ORDER BY i.id) FROM incidents i JOIN tickets t ON t.id=i.work_item_id WHERE COALESCE(i.incident_number,'')<>'' AND i.incident_number IS DISTINCT FROM t.ticket_number$check$ INTO bad_ids;
  IF bad_ids IS NOT NULL THEN RAISE EXCEPTION 'conflicting legacy Incident number at Incident IDs %; preserve WorkItem number and reconcile before retry',bad_ids; END IF;
 END IF;
 ALTER TABLE tickets DROP COLUMN IF EXISTS type;
 ALTER TABLE incidents DROP COLUMN IF EXISTS incident_number;
END $retire$;

DO $verify$
BEGIN
 IF EXISTS(SELECT 1 FROM information_schema.columns WHERE table_schema=current_schema() AND ((table_name='tickets' AND column_name='type') OR (table_name='incidents' AND column_name='incident_number'))) THEN RAISE EXCEPTION 'retired WorkItem identity columns must be absent'; END IF;
 IF NOT EXISTS(SELECT 1 FROM pg_attribute WHERE attrelid='incidents'::regclass AND attname='work_item_id' AND attnotnull AND NOT attisdropped) THEN RAISE EXCEPTION 'Incident WorkItem ownership must be required'; END IF;
 IF NOT EXISTS(SELECT 1 FROM pg_constraint c JOIN pg_attribute a ON a.attrelid=c.conrelid AND a.attnum=c.conkey[1] JOIN pg_attribute b ON b.attrelid=c.confrelid AND b.attnum=c.confkey[1]
 WHERE c.conrelid='incidents'::regclass AND c.confrelid='tickets'::regclass AND c.contype='f' AND c.convalidated AND NOT c.condeferrable AND cardinality(c.conkey)=1 AND cardinality(c.confkey)=1 AND a.attname='work_item_id' AND b.attname='id' AND c.confdeltype='a' AND c.confupdtype='a') THEN RAISE EXCEPTION 'Incident WorkItem foreign key must be exact'; END IF;
 IF NOT EXISTS(SELECT 1 FROM pg_index i JOIN pg_attribute a ON a.attrelid=i.indrelid AND a.attnum=i.indkey[0] WHERE i.indrelid='incidents'::regclass AND i.indisunique AND i.indisvalid AND i.indisready AND i.indnkeyatts=1 AND i.indnatts=1 AND i.indpred IS NULL AND i.indexprs IS NULL AND a.attname='work_item_id') THEN RAISE EXCEPTION 'Incident WorkItem ownership must be unique'; END IF;
END $verify$;
