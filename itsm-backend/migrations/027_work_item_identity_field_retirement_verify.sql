
DO $verify$
BEGIN
 IF EXISTS(SELECT 1 FROM information_schema.columns WHERE table_schema=current_schema() AND ((table_name='tickets' AND column_name='type') OR (table_name='incidents' AND column_name='incident_number'))) THEN RAISE EXCEPTION 'retired WorkItem identity columns must be absent'; END IF;
 IF NOT EXISTS(SELECT 1 FROM pg_attribute WHERE attrelid='incidents'::regclass AND attname='work_item_id' AND attnotnull AND NOT attisdropped) THEN RAISE EXCEPTION 'Incident WorkItem ownership must be required'; END IF;
 IF NOT EXISTS(SELECT 1 FROM pg_constraint c JOIN pg_attribute a ON a.attrelid=c.conrelid AND a.attnum=c.conkey[1] JOIN pg_attribute b ON b.attrelid=c.confrelid AND b.attnum=c.confkey[1]
 WHERE c.conrelid='incidents'::regclass AND c.confrelid='tickets'::regclass AND c.contype='f' AND c.convalidated AND NOT c.condeferrable AND cardinality(c.conkey)=1 AND cardinality(c.confkey)=1 AND a.attname='work_item_id' AND b.attname='id' AND c.confdeltype='a' AND c.confupdtype='a') THEN RAISE EXCEPTION 'Incident WorkItem foreign key must be exact'; END IF;
 IF NOT EXISTS(SELECT 1 FROM pg_index i JOIN pg_attribute a ON a.attrelid=i.indrelid AND a.attnum=i.indkey[0] WHERE i.indrelid='incidents'::regclass AND i.indisunique AND i.indisvalid AND i.indisready AND i.indnkeyatts=1 AND i.indnatts=1 AND i.indpred IS NULL AND i.indexprs IS NULL AND a.attname='work_item_id') THEN RAISE EXCEPTION 'Incident WorkItem ownership must be unique'; END IF;
END $verify$;
