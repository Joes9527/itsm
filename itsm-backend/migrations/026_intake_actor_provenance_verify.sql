DO $verify$
DECLARE target text;
BEGIN
 IF NOT EXISTS(SELECT 1 FROM pg_attribute WHERE attrelid=format('%I.intake_requests',current_schema())::regclass AND attname='actor_tenant_id' AND attnotnull AND NOT attisdropped AND atttypid IN ('bigint'::regtype,'integer'::regtype) AND NOT atthasdef) THEN RAISE EXCEPTION 'Intake immutable native actor tenant must be required with no default'; END IF;
 FOR target IN SELECT unnest(ARRAY['intake_receipt_provenance','intake_audit_provenance','incident_rule_execution_owner','incident_rule_action_receipt_owner']) LOOP
  IF NOT EXISTS(SELECT 1 FROM pg_trigger t JOIN pg_class c ON c.oid=t.tgrelid JOIN pg_namespace n ON n.oid=c.relnamespace WHERE n.nspname=current_schema() AND t.tgname=target AND t.tgenabled='O' AND NOT t.tgisinternal) THEN RAISE EXCEPTION 'required Intake provenance guard % missing',target; END IF;
 END LOOP;
 FOR target IN SELECT unnest(ARRAY['intake_requests','audit_logs','incident_rule_executions','incident_rule_action_receipts']) LOOP
  IF NOT EXISTS(SELECT 1 FROM pg_class c JOIN pg_namespace n ON n.oid=c.relnamespace WHERE n.nspname=current_schema() AND c.relname=target AND c.relrowsecurity AND c.relforcerowsecurity) THEN RAISE EXCEPTION '% requires forced tenant RLS',target; END IF;
 END LOOP;
END $verify$;
