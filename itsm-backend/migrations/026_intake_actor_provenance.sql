DO $prerequisites$
DECLARE target text;
BEGIN
 FOR target IN SELECT unnest(ARRAY['intake_requests','audit_logs','users','tenants','tickets','incident_rule_executions','incident_rule_action_receipts','outbox_events','incidents']) LOOP
  IF to_regclass(format('%I.%I',current_schema(),target)) IS NULL THEN RAISE EXCEPTION 'required local % table missing; restore Intake provenance prerequisites',target; END IF;
 END LOOP;
END $prerequisites$;
ALTER TABLE intake_requests ADD COLUMN IF NOT EXISTS actor_tenant_id bigint;
DO $history$
DECLARE invalid_ids text;
BEGIN
 SELECT string_agg(id::text,',') INTO invalid_ids FROM (
  SELECT r.id FROM intake_requests r LEFT JOIN users u ON u.id=r.actor_id LEFT JOIN tenants n ON n.id=u.tenant_id LEFT JOIN tickets t ON t.id=r.work_item_id
  WHERE u.id IS NULL OR n.id IS NULL OR (r.actor_tenant_id IS NULL AND u.tenant_id<>r.tenant_id)
   OR (r.actor_tenant_id IS NOT NULL AND r.actor_tenant_id<>u.tenant_id)
   OR (r.work_item_id IS NOT NULL AND (t.id IS NULL OR t.tenant_id<>r.tenant_id OR t.requester_id<>r.requester_id OR t.opened_by_id<>r.actor_id))
  ORDER BY r.id LIMIT 20
 ) invalid;
 IF invalid_ids IS NOT NULL THEN RAISE EXCEPTION 'unprovable Intake actor provenance for receipt IDs %; reconcile actor/native tenant and WorkItem history before retry',invalid_ids; END IF;
END $history$;
-- Historical absence is provable only for an existing native target actor.
UPDATE intake_requests r SET actor_tenant_id=u.tenant_id FROM users u WHERE u.id=r.actor_id AND r.actor_tenant_id IS NULL AND u.tenant_id=r.tenant_id;
ALTER TABLE intake_requests ALTER COLUMN actor_tenant_id SET NOT NULL;
DO $audit_history$
DECLARE a record;r record;body jsonb;item_id bigint;matches bigint;
BEGIN
 FOR a IN SELECT * FROM audit_logs WHERE action IN ('intake.created','convert_to_problem','incident_rule.action_completed') LOOP
  BEGIN body:=COALESCE(NULLIF(a.request_body,''),'{}')::jsonb;
  EXCEPTION WHEN OTHERS THEN RAISE EXCEPTION 'invalid Intake audit JSON at audit ID %; reconcile audit history before retry',a.id; END;
  IF jsonb_typeof(body)<>'object' THEN RAISE EXCEPTION 'invalid Intake audit object at audit ID %',a.id; END IF;
  item_id:=NULL;
  -- The original action association remains authoritative even when the body
  -- already names a receipt. Never overwrite contradictory historical evidence.
  IF a.action='intake.created' AND a.resource ~ '^work_item:[0-9]+$' THEN item_id:=split_part(a.resource,':',2)::bigint;
  ELSIF a.action='convert_to_problem' AND body->>'targetWorkItemId' ~ '^[0-9]+$' THEN item_id:=(body->>'targetWorkItemId')::bigint;
  ELSIF a.action='incident_rule.action_completed' THEN
   SELECT i.work_item_id INTO item_id FROM incident_rule_executions e JOIN incidents i ON i.id=e.incident_id WHERE e.tenant_id=a.tenant_id AND e.actor_id=a.user_id AND a.request_id ~ ( '^' || e.execution_key || ':action:[0-9]+$');
  END IF;
  IF body->>'intakeRequestId' IS NOT NULL THEN
   SELECT count(*) INTO matches FROM intake_requests i WHERE i.id::text=body->>'intakeRequestId' AND i.actor_id=a.user_id AND i.tenant_id=a.tenant_id AND i.work_item_id=item_id AND i.status='completed';
   SELECT * INTO r FROM intake_requests i WHERE i.id::text=body->>'intakeRequestId' AND i.actor_id=a.user_id AND i.tenant_id=a.tenant_id AND i.work_item_id=item_id AND i.status='completed';
  ELSE
   SELECT count(*) INTO matches FROM intake_requests i WHERE i.work_item_id=item_id AND i.actor_id=a.user_id AND i.tenant_id=a.tenant_id AND i.status='completed';
   SELECT * INTO r FROM intake_requests i WHERE i.work_item_id=item_id AND i.actor_id=a.user_id AND i.tenant_id=a.tenant_id AND i.status='completed';
  END IF;
  IF matches<>1 THEN RAISE EXCEPTION 'unprovable Intake provenance for audit ID %; reconcile unique receipt association before retry',a.id; END IF;
  IF (body ? 'actorTenantId' AND body->>'actorTenantId' IS DISTINCT FROM r.actor_tenant_id::text)
   OR (body ? 'actorUserId' AND body->>'actorUserId' IS DISTINCT FROM r.actor_id::text)
   OR (body ? 'targetTenantId' AND body->>'targetTenantId' IS DISTINCT FROM r.tenant_id::text)
   OR (body ? 'workItemId' AND body->>'workItemId' IS DISTINCT FROM r.work_item_id::text)
  THEN RAISE EXCEPTION 'conflicting Intake provenance for audit ID %; reconcile history before retry',a.id; END IF;
  IF NOT body ?& ARRAY['actorUserId','actorTenantId','targetTenantId','intakeRequestId','workItemId'] THEN
   UPDATE audit_logs SET request_body=(body || jsonb_build_object('actorUserId',r.actor_id,'actorTenantId',r.actor_tenant_id,'targetTenantId',r.tenant_id,'intakeRequestId',r.id,'workItemId',r.work_item_id))::text WHERE id=a.id;
  END IF;
 END LOOP;
END $audit_history$;
CREATE OR REPLACE FUNCTION validate_intake_receipt_provenance() RETURNS trigger LANGUAGE plpgsql AS $receipt$
BEGIN
 IF TG_OP='DELETE' THEN RAISE EXCEPTION 'Intake receipt provenance is immutable'; END IF;
 IF NEW.actor_tenant_id IS NULL OR NEW.actor_tenant_id<=0 THEN RAISE EXCEPTION 'Intake receipt native provenance required'; END IF;
 IF TG_OP='UPDATE' AND (NEW.tenant_id,NEW.actor_id,NEW.actor_tenant_id,NEW.requester_id,NEW.channel,NEW.operation,NEW.idempotency_key,NEW.request_digest,NEW.digest_version,NEW.created_at)
 IS DISTINCT FROM (OLD.tenant_id,OLD.actor_id,OLD.actor_tenant_id,OLD.requester_id,OLD.channel,OLD.operation,OLD.idempotency_key,OLD.request_digest,OLD.digest_version,OLD.created_at)
 THEN RAISE EXCEPTION 'Intake receipt provenance is immutable'; END IF;
 IF TG_OP='UPDATE' AND OLD.status='completed' AND (NEW.status,NEW.work_item_id,NEW.completed_at) IS DISTINCT FROM (OLD.status,OLD.work_item_id,OLD.completed_at) THEN RAISE EXCEPTION 'completed Intake receipt is immutable'; END IF;
 IF NEW.work_item_id IS NOT NULL AND NOT EXISTS(SELECT 1 FROM tickets t WHERE t.id=NEW.work_item_id AND t.tenant_id=NEW.tenant_id AND t.opened_by_id=NEW.actor_id AND t.requester_id=NEW.requester_id) THEN RAISE EXCEPTION 'Intake receipt WorkItem provenance mismatch'; END IF;
 RETURN NEW;
END $receipt$;
DROP TRIGGER IF EXISTS intake_receipt_provenance ON intake_requests;
CREATE TRIGGER intake_receipt_provenance BEFORE INSERT OR UPDATE OR DELETE ON intake_requests FOR EACH ROW EXECUTE FUNCTION validate_intake_receipt_provenance();
CREATE OR REPLACE FUNCTION validate_intake_audit_provenance() RETURNS trigger LANGUAGE plpgsql AS $audit$
DECLARE body jsonb;
BEGIN
 IF TG_OP IN ('UPDATE','DELETE') AND OLD.action IN ('intake.created','convert_to_problem','incident_rule.action_completed') THEN RAISE EXCEPTION 'Intake audit provenance is immutable'; END IF;
 IF TG_OP='DELETE' THEN RETURN OLD; END IF;
 IF NEW.action NOT IN ('intake.created','convert_to_problem','incident_rule.action_completed') THEN RETURN NEW; END IF;
 body:=NEW.request_body::jsonb;
 IF EXISTS(SELECT 1 FROM unnest(ARRAY['actorUserId','actorTenantId','targetTenantId','intakeRequestId','workItemId']) AS key WHERE jsonb_typeof(body->key) IS DISTINCT FROM 'number') THEN RAISE EXCEPTION 'numeric Intake audit provenance required'; END IF;
 IF jsonb_typeof(body)<>'object' OR NOT body ?& ARRAY['actorUserId','actorTenantId','targetTenantId','intakeRequestId','workItemId'] THEN RAISE EXCEPTION 'typed Intake audit provenance required'; END IF;
 IF NOT EXISTS(SELECT 1 FROM intake_requests r JOIN tickets t ON t.id::text=body->>'workItemId'
  WHERE r.id::text=body->>'intakeRequestId' AND r.tenant_id=NEW.tenant_id AND r.actor_id=NEW.user_id
   AND body->>'actorUserId'=r.actor_id::text AND body->>'actorTenantId'=r.actor_tenant_id::text AND body->>'targetTenantId'=r.tenant_id::text
   AND t.tenant_id=r.tenant_id AND t.opened_by_id=r.actor_id AND t.requester_id=r.requester_id
   AND (r.work_item_id IS NULL OR r.work_item_id=t.id))
 THEN RAISE EXCEPTION 'Intake audit receipt provenance mismatch'; END IF;
 RETURN NEW;
END $audit$;
DROP TRIGGER IF EXISTS intake_audit_provenance ON audit_logs;
CREATE TRIGGER intake_audit_provenance BEFORE INSERT OR UPDATE OR DELETE ON audit_logs FOR EACH ROW EXECUTE FUNCTION validate_intake_audit_provenance();
CREATE OR REPLACE FUNCTION validate_incident_rule_execution_owner() RETURNS trigger LANGUAGE plpgsql AS $owner$
BEGIN
 -- Ent drops checks outside its schema. Keep the complete identity shape in
 -- the same durable ownership trigger as the tenant and frozen-policy guards.
 IF NEW.execution_kind IS NULL OR NEW.execution_kind NOT IN ('rule','creation_event')
  OR (NEW.execution_kind='rule' AND NEW.rule_id IS NULL)
  OR (NEW.execution_kind='creation_event' AND (NEW.rule_id IS NOT NULL OR NEW.execution_key IS NULL))
 THEN RAISE EXCEPTION 'Incident execution kind and rule identity mismatch'; END IF;
 IF NEW.execution_key IS NULL THEN
  IF NEW.source_event_id IS NOT NULL THEN RAISE EXCEPTION 'Incident execution source requires stable identity'; END IF;
 ELSE
  IF length(NEW.execution_key)=0 OR NEW.source_event_id IS NULL OR NEW.actor_id IS NULL OR NEW.actor_id<=0
   OR NEW.incident_id IS NULL OR NEW.source IS NULL OR length(NEW.source)=0
  THEN RAISE EXCEPTION 'Incident execution stable source and actor identity required'; END IF;
 END IF;
 IF TG_OP='UPDATE' AND OLD.execution_key IS NOT NULL AND
 (NEW.tenant_id,NEW.execution_kind,NEW.execution_key,NEW.source_event_id,NEW.rule_id,NEW.incident_id,NEW.actor_id,NEW.source,NEW.frozen_actions,NEW.input_data)
 IS DISTINCT FROM
 (OLD.tenant_id,OLD.execution_kind,OLD.execution_key,OLD.source_event_id,OLD.rule_id,OLD.incident_id,OLD.actor_id,OLD.source,OLD.frozen_actions,OLD.input_data)
 THEN RAISE EXCEPTION 'frozen Incident rule execution identity and policy are immutable'; END IF;
 IF NEW.rule_id IS NOT NULL AND NOT EXISTS(SELECT 1 FROM incident_rules WHERE id=NEW.rule_id AND tenant_id=NEW.tenant_id) THEN RAISE EXCEPTION 'Incident rule owner tenant mismatch'; END IF;
 IF NEW.incident_id IS NOT NULL AND NOT EXISTS(SELECT 1 FROM incidents i JOIN tickets t ON t.id=i.work_item_id WHERE i.id=NEW.incident_id AND t.tenant_id=NEW.tenant_id) THEN RAISE EXCEPTION 'Incident execution WorkItem tenant mismatch'; END IF;
 IF NEW.source_event_id IS NULL AND NEW.actor_id IS NOT NULL AND NOT EXISTS(SELECT 1 FROM users WHERE id=NEW.actor_id AND tenant_id=NEW.tenant_id) THEN RAISE EXCEPTION 'Incident execution actor tenant mismatch'; END IF;
 IF NEW.source_event_id IS NOT NULL AND NOT EXISTS(
  SELECT 1 FROM outbox_events o JOIN incidents i ON i.id=NEW.incident_id JOIN tickets t ON t.id=i.work_item_id
  JOIN intake_requests r ON r.work_item_id=t.id AND r.tenant_id=NEW.tenant_id AND r.actor_id=NEW.actor_id AND r.channel=NEW.source AND r.status='completed' AND r.actor_tenant_id>0
  WHERE o.id=NEW.source_event_id AND o.tenant_id=NEW.tenant_id AND o.event_type='incident.created'
    AND o.aggregate_type='work_item' AND o.aggregate_id=i.work_item_id::text
    AND o.event_id='incident-created:'||i.work_item_id::text
    AND t.tenant_id=NEW.tenant_id AND t.opened_by_id=r.actor_id AND t.requester_id=r.requester_id
    AND o.payload->>'actorId'=r.actor_id::text
    AND o.payload->>'tenantId'=r.tenant_id::text
    AND o.payload->>'workItemId'=t.id::text
    AND o.payload->>'incidentId'=i.id::text
    AND o.payload->>'channel'=r.channel
    AND NEW.execution_key=CASE WHEN NEW.execution_kind='creation_event' THEN o.event_id ELSE o.event_id||':rule:'||NEW.rule_id::text END
 ) THEN RAISE EXCEPTION 'Incident execution source event identity mismatch'; END IF;
 RETURN NEW;
END $owner$;
DO $execution_history$
DECLARE entry record;
BEGIN
 FOR entry IN SELECT id FROM incident_rule_executions LOOP
  BEGIN UPDATE incident_rule_executions SET status=status WHERE id=entry.id;
  EXCEPTION WHEN OTHERS THEN RAISE EXCEPTION 'invalid Incident execution provenance ID %: %; reconcile history before retry',entry.id,SQLERRM; END;
 END LOOP;
END $execution_history$;
ALTER TABLE intake_requests ENABLE ROW LEVEL SECURITY;
ALTER TABLE intake_requests FORCE ROW LEVEL SECURITY;
DROP POLICY IF EXISTS tenant_isolation ON intake_requests;
DROP POLICY IF EXISTS tenant_isolation_intake_requests ON intake_requests;
CREATE POLICY tenant_isolation_intake_requests ON intake_requests USING(tenant_id=NULLIF(current_setting('app.current_tenant',true),'')::bigint) WITH CHECK(tenant_id=NULLIF(current_setting('app.current_tenant',true),'')::bigint);
ALTER TABLE audit_logs ENABLE ROW LEVEL SECURITY;
ALTER TABLE audit_logs FORCE ROW LEVEL SECURITY;
DROP POLICY IF EXISTS tenant_isolation ON audit_logs;
DROP POLICY IF EXISTS tenant_isolation_audit_logs ON audit_logs;
CREATE POLICY tenant_isolation_audit_logs ON audit_logs USING(tenant_id=NULLIF(current_setting('app.current_tenant',true),'')::bigint) WITH CHECK(tenant_id=NULLIF(current_setting('app.current_tenant',true),'')::bigint);
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
