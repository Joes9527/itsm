DO $verify$
DECLARE target text;columns text[];present boolean;
BEGIN
 FOR target IN SELECT unnest(ARRAY['incident_rule_executions','incident_rule_action_receipts']) LOOP
  IF to_regclass(format('%I.%I',current_schema(),target)) IS NULL THEN RAISE EXCEPTION 'required local % table missing',target; END IF;
  IF NOT EXISTS(SELECT 1 FROM pg_class t JOIN pg_namespace n ON n.oid=t.relnamespace WHERE n.nspname=current_schema() AND t.relname=target AND t.relrowsecurity AND t.relforcerowsecurity) THEN RAISE EXCEPTION '% requires forced tenant RLS',target; END IF;
 END LOOP;
 IF NOT EXISTS(SELECT 1 FROM pg_attribute a WHERE a.attrelid=format('%I.incident_rule_executions',current_schema())::regclass AND a.attname='rule_id' AND NOT a.attnotnull AND NOT a.attisdropped) THEN RAISE EXCEPTION 'creation event decisions require nullable rule_id'; END IF;
 FOR target,columns IN SELECT * FROM (VALUES('incident_rule_executions',ARRAY['tenant_id','execution_key']),('incident_rule_action_receipts',ARRAY['tenant_id','execution_id','action_index'])) AS required(target,columns) LOOP
  SELECT EXISTS(SELECT 1 FROM pg_index x JOIN pg_class t ON t.oid=x.indrelid JOIN pg_namespace n ON n.oid=t.relnamespace
   WHERE n.nspname=current_schema() AND t.relname=target AND x.indisunique AND x.indisvalid AND x.indisready AND x.indpred IS NULL AND x.indexprs IS NULL
   AND ARRAY(SELECT a.attname::text FROM unnest(x.indkey) WITH ORDINALITY k(num,pos) JOIN pg_attribute a ON a.attrelid=t.oid AND a.attnum=k.num ORDER BY k.pos)=columns) INTO present;
  IF NOT present THEN RAISE EXCEPTION '% requires complete unique action/event identity index',target; END IF;
 END LOOP;
 IF NOT EXISTS(SELECT 1 FROM pg_trigger WHERE tgrelid=format('%I.incident_rule_executions',current_schema())::regclass AND tgname='incident_rule_execution_owner' AND tgenabled<>'D') THEN RAISE EXCEPTION 'Incident execution ownership guard missing'; END IF;
 IF NOT EXISTS(SELECT 1 FROM pg_trigger WHERE tgrelid=format('%I.incident_rule_action_receipts',current_schema())::regclass AND tgname='incident_rule_action_receipt_owner' AND tgenabled<>'D') THEN RAISE EXCEPTION 'action receipt immutable tenant owner guard missing'; END IF;
 IF NOT EXISTS(SELECT 1 FROM pg_constraint WHERE conrelid=format('%I.incident_rule_action_receipts',current_schema())::regclass AND conname='incident_rule_action_receipt_execution_fk' AND contype='f' AND convalidated) THEN RAISE EXCEPTION 'action receipt execution FK missing'; END IF;
END $verify$;
