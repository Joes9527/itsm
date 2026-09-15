package migration

const catalogAccessPolicyResultSQL = `-- Ent creates the typed tables/FKs before the registered migration stream.
-- Cross-owner invariants and immutable evidence are enforced here.
CREATE OR REPLACE FUNCTION itsm_finite_access_options(options jsonb) RETURNS boolean
LANGUAGE plpgsql IMMUTABLE AS $$
DECLARE option jsonb; seen text[] := '{}'; seconds numeric;
BEGIN
 IF jsonb_typeof(options) <> 'array' OR jsonb_array_length(options)=0 THEN RETURN false; END IF;
 FOR option IN SELECT value FROM jsonb_array_elements(options) LOOP
  IF jsonb_typeof(option)<>'object' OR jsonb_typeof(option->'key')<>'string'
   OR jsonb_typeof(option->'label')<>'string' OR jsonb_typeof(option->'seconds')<>'number'
   OR COALESCE(btrim(option->>'key'),'')='' OR COALESCE(btrim(option->>'label'),'')=''
   OR (option->>'key')=ANY(seen) THEN RETURN false; END IF;
  seconds := (option->>'seconds')::numeric;
  IF seconds IS NULL OR seconds<>trunc(seconds) OR seconds<=0 OR seconds>9223372036 THEN RETURN false; END IF;
  seen:=array_append(seen,option->>'key');
 END LOOP;
 RETURN true;
END $$;
DO $$ BEGIN
 IF NOT EXISTS(SELECT 1 FROM pg_constraint WHERE conrelid='catalog_access_policies'::regclass AND conname='catalog_access_policy_finite') THEN
 ALTER TABLE catalog_access_policies ADD CONSTRAINT catalog_access_policy_finite CHECK(version>0 AND provider='graph' AND btrim(external_system)<>'' AND btrim(group_id)<>'' AND btrim(duration_field)<>'' AND itsm_finite_access_options(duration_options));
 END IF;
 IF NOT EXISTS(SELECT 1 FROM pg_constraint WHERE conrelid='service_request_access_snapshots'::regclass AND conname='access_snapshot_finite') THEN
 ALTER TABLE service_request_access_snapshots ADD CONSTRAINT access_snapshot_finite CHECK(policy_version>0 AND provider='graph' AND btrim(external_system)<>'' AND btrim(subject_id)<>'' AND btrim(group_id)<>'' AND btrim(duration_key)<>'' AND duration_seconds>0 AND duration_seconds<=9223372036);
 END IF;
 IF NOT EXISTS(SELECT 1 FROM pg_constraint WHERE conrelid='service_request_access_results'::regclass AND conname='access_result_verified') THEN
 ALTER TABLE service_request_access_results ADD CONSTRAINT access_result_verified CHECK(provider='graph' AND btrim(subject_id)<>'' AND btrim(group_id)<>'' AND btrim(evidence_ref)<>'' AND verified_at>'0001-01-01T00:00:00Z'::timestamptz AND ((outcome='granted' AND baseline='not_member' AND expires_at IS NOT NULL AND expires_at>verified_at) OR(outcome='already_present' AND baseline='member' AND expires_at IS NULL)));
 END IF;
END $$;
CREATE OR REPLACE FUNCTION itsm_access_snapshot_guard() RETURNS trigger LANGUAGE plpgsql AS $$
BEGIN
 IF TG_OP='UPDATE' THEN RAISE EXCEPTION 'requested access snapshot is immutable'; END IF;
 IF NOT EXISTS(SELECT 1 FROM tickets w JOIN service_requests sr ON sr.ticket_id=w.id
   JOIN catalog_access_policies p ON p.catalog_id=sr.catalog_id
   JOIN service_catalogs c ON c.id=p.catalog_id
   JOIN external_identities i ON i.tenant_id=w.tenant_id AND i.user_id=w.requester_id AND i.provider=NEW.provider AND i.workspace=NEW.external_system AND i.subject=NEW.subject_id AND i.active
   WHERE w.id=NEW.work_item_id AND w.record_class='service_request_item' AND w.deleted_at IS NULL
    AND c.tenant_id=w.tenant_id AND p.id=NEW.policy_id AND p.version=NEW.policy_version
    AND p.provider=NEW.provider AND p.external_system=NEW.external_system AND p.group_id=NEW.group_id
    AND EXISTS(SELECT 1 FROM jsonb_array_elements(p.duration_options) o WHERE o->>'key'=NEW.duration_key AND (o->>'seconds')::numeric=NEW.duration_seconds))
 THEN RAISE EXCEPTION 'requested access snapshot must match its WorkItem, policy and requester mapping'; END IF;
 RETURN NEW;
END $$;
DROP TRIGGER IF EXISTS access_snapshot_guard ON service_request_access_snapshots;
CREATE TRIGGER access_snapshot_guard BEFORE INSERT OR UPDATE ON service_request_access_snapshots FOR EACH ROW EXECUTE FUNCTION itsm_access_snapshot_guard();
CREATE OR REPLACE FUNCTION itsm_access_result_guard() RETURNS trigger LANGUAGE plpgsql AS $$
BEGIN
 IF TG_OP='UPDATE' THEN RAISE EXCEPTION 'verified access result is immutable'; END IF;
 IF NOT EXISTS(SELECT 1 FROM service_request_access_snapshots s JOIN tickets w ON w.id=s.work_item_id
   JOIN process_tasks t ON t.id=NEW.process_task_id JOIN process_instances p ON p.id=t.process_instance_id
   WHERE s.work_item_id=NEW.work_item_id AND w.record_class='service_request_item' AND w.deleted_at IS NULL
    AND t.tenant_id=w.tenant_id AND p.tenant_id=w.tenant_id AND p.business_type='service_request' AND p.business_id=w.id
    AND t.task_type='kaf_delegate' AND t.callback_action='external_group_grant' AND t.callback_config_ref=s.policy_id::text
    AND s.provider=NEW.provider AND s.subject_id=NEW.subject_id AND s.group_id=NEW.group_id
    AND (NEW.outcome='already_present' OR NEW.expires_at=NEW.verified_at+make_interval(secs=>s.duration_seconds)))
 THEN RAISE EXCEPTION 'verified access result must match approved snapshot and delegated task'; END IF;
 RETURN NEW;
END $$;
DROP TRIGGER IF EXISTS access_result_guard ON service_request_access_results;
CREATE TRIGGER access_result_guard BEFORE INSERT OR UPDATE ON service_request_access_results FOR EACH ROW EXECUTE FUNCTION itsm_access_result_guard();
DO $$
DECLARE target text; expression text; existing text;
BEGIN
 FOREACH target IN ARRAY ARRAY['catalog_access_policies','service_request_access_snapshots','service_request_access_results'] LOOP
  FOR existing IN SELECT polname FROM pg_policy WHERE polrelid=target::regclass LOOP
   IF existing<>'tenant_isolation_'||target THEN RAISE EXCEPTION 'unexpected access policy on %',target; END IF;
  END LOOP;
  EXECUTE format('ALTER TABLE %I ENABLE ROW LEVEL SECURITY',target);
  EXECUTE format('ALTER TABLE %I FORCE ROW LEVEL SECURITY',target);
  IF target='catalog_access_policies' THEN
   expression:='EXISTS(SELECT 1 FROM service_catalogs c WHERE c.id=catalog_access_policies.catalog_id AND c.tenant_id=NULLIF(current_setting(''app.current_tenant'',true),'''')::bigint)';
  ELSE
   expression:=format('EXISTS(SELECT 1 FROM tickets w WHERE w.id=%I.work_item_id AND w.record_class=''service_request_item'' AND w.tenant_id=NULLIF(current_setting(''app.current_tenant'',true),'''')::bigint)',target);
  END IF;
  EXECUTE format('DROP POLICY IF EXISTS %I ON %I','tenant_isolation_'||target,target);
  EXECUTE format('CREATE POLICY %I ON %I USING (%s) WITH CHECK (%s)','tenant_isolation_'||target,target,expression,expression);
 END LOOP;
END $$;
`
