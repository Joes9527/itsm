package migration

const incidentStatusEventsSQL = `CREATE OR REPLACE FUNCTION validate_incident_rule_execution_owner() RETURNS trigger LANGUAGE plpgsql AS $owner$
BEGIN
 -- Ent drops checks outside its schema. Keep the complete identity shape in
 -- the same durable ownership trigger as the tenant and frozen-policy guards.
 IF NEW.execution_kind IS NULL OR NEW.execution_kind NOT IN ('rule','creation_event','status_event')
  OR (NEW.execution_kind='rule' AND NEW.rule_id IS NULL)
  OR (NEW.execution_kind IN ('creation_event','status_event') AND (NEW.rule_id IS NOT NULL OR NEW.execution_key IS NULL))
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
  WHERE o.id=NEW.source_event_id AND o.tenant_id=NEW.tenant_id AND o.event_type='incident.created' AND NEW.execution_kind IN ('rule','creation_event')
    AND o.aggregate_type='work_item' AND o.aggregate_id=i.work_item_id::text
    AND o.event_id='incident-created:'||i.work_item_id::text
    AND t.tenant_id=NEW.tenant_id AND t.opened_by_id=r.actor_id AND t.requester_id=r.requester_id
    AND o.payload->>'actorId'=r.actor_id::text
    AND o.payload->>'tenantId'=r.tenant_id::text
    AND o.payload->>'workItemId'=t.id::text
    AND o.payload->>'incidentId'=i.id::text
    AND o.payload->>'channel'=r.channel
    AND NEW.execution_key=CASE WHEN NEW.execution_kind='creation_event' THEN o.event_id ELSE o.event_id||':rule:'||NEW.rule_id::text END
 ) AND NOT EXISTS(
 SELECT 1 FROM outbox_events o JOIN incidents i ON i.id=NEW.incident_id JOIN tickets t ON t.id=i.work_item_id
 JOIN audit_logs a ON a.tenant_id=NEW.tenant_id AND a.user_id=NEW.actor_id
  AND a.operation_id=o.payload->>'operationId' AND a.resource='work_item' AND a.path=t.id::text
  AND a.result_version::text=o.payload->>'version' AND a.result_status=o.payload->>'status'
  AND a.method=NEW.source AND a.action IN ('incident.assign','incident.escalate','incident.acknowledge','incident.start','incident.resolve','incident.close','incident.reopen')
 WHERE o.id=NEW.source_event_id AND o.tenant_id=NEW.tenant_id AND o.event_type='incident.status_changed'
  AND o.aggregate_type='work_item' AND o.aggregate_id=t.id::text AND t.tenant_id=NEW.tenant_id
  AND o.event_id='incident-status:'||t.id::text||':'||a.result_version::text
  AND o.payload->>'actorId'=NEW.actor_id::text AND o.payload->>'source'=NEW.source
  AND o.payload->>'tenantId'=NEW.tenant_id::text AND o.payload->>'incidentId'=i.id::text
  AND o.payload->>'workItemId'=t.id::text AND o.payload=a.request_body::jsonb
  AND NEW.execution_kind IN ('rule','status_event')
  AND NEW.execution_key=CASE WHEN NEW.execution_kind='status_event' THEN o.event_id ELSE o.event_id||':rule:'||NEW.rule_id::text END
 ) THEN RAISE EXCEPTION 'Incident execution source event identity mismatch'; END IF;
 RETURN NEW;
END $owner$;
ALTER TABLE process_callback_outboxes ADD COLUMN IF NOT EXISTS actor_id bigint;
ALTER TABLE process_callback_outboxes ADD COLUMN IF NOT EXISTS actor_source varchar;
CREATE OR REPLACE FUNCTION immutable_callback_actor() RETURNS trigger LANGUAGE plpgsql AS $actor$
BEGIN
 IF (NEW.actor_id,NEW.actor_source) IS DISTINCT FROM (OLD.actor_id,OLD.actor_source) THEN RAISE EXCEPTION 'callback completion actor is immutable'; END IF;
 RETURN NEW;
END $actor$;
DROP TRIGGER IF EXISTS immutable_callback_actor ON process_callback_outboxes;
CREATE TRIGGER immutable_callback_actor BEFORE UPDATE ON process_callback_outboxes FOR EACH ROW EXECUTE FUNCTION immutable_callback_actor();
CREATE OR REPLACE FUNCTION validate_incident_status_action_audit() RETURNS trigger LANGUAGE plpgsql AS $guard$
BEGIN
 IF TG_OP IN ('UPDATE','DELETE') AND OLD.action='incident_rule.status_action_completed' THEN RAISE EXCEPTION 'Incident status action audit is immutable'; END IF;
 IF TG_OP='DELETE' THEN RETURN OLD; END IF;
 IF NEW.action<>'incident_rule.status_action_completed' THEN RETURN NEW; END IF;
 IF NOT EXISTS(SELECT 1 FROM incident_rule_executions e JOIN outbox_events o ON o.id=e.source_event_id
  JOIN incident_rule_action_receipts r ON r.execution_id=e.id AND r.tenant_id=e.tenant_id
  WHERE e.tenant_id=NEW.tenant_id AND e.actor_id=NEW.user_id AND o.event_type='incident.status_changed'
   AND e.execution_kind='rule' AND NEW.request_id=e.execution_key||':action:'||r.action_index::text
   AND NEW.request_body::jsonb->>'sourceEventId'=o.id::text
   AND NEW.request_body::jsonb->>'operationId'=o.payload->>'operationId'
   AND NEW.request_body::jsonb->>'workItemId'=o.aggregate_id)
 THEN RAISE EXCEPTION 'Incident status action audit provenance mismatch'; END IF;
 RETURN NEW;
END $guard$;
DROP TRIGGER IF EXISTS incident_status_action_audit ON audit_logs;
CREATE TRIGGER incident_status_action_audit BEFORE INSERT OR UPDATE OR DELETE ON audit_logs FOR EACH ROW EXECUTE FUNCTION validate_incident_status_action_audit();
`
