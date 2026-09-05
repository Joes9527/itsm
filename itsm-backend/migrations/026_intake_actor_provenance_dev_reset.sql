DO $reset$
BEGIN
 IF to_regclass(format('%I.intake_requests',current_schema())) IS NULL THEN RAISE EXCEPTION 'required local intake_requests missing'; END IF;
 IF EXISTS(SELECT 1 FROM intake_requests) OR EXISTS(SELECT 1 FROM audit_logs WHERE action IN ('intake.created','convert_to_problem','incident_rule.action_completed')) OR EXISTS(SELECT 1 FROM incident_rule_executions) THEN RAISE EXCEPTION 'development reset requires empty Intake and Incident provenance history'; END IF;
END $reset$;
DROP TRIGGER IF EXISTS intake_receipt_provenance ON intake_requests;
DROP TRIGGER IF EXISTS intake_audit_provenance ON audit_logs;
DROP FUNCTION IF EXISTS validate_intake_receipt_provenance();
DROP FUNCTION IF EXISTS validate_intake_audit_provenance();
ALTER TABLE intake_requests DROP COLUMN IF EXISTS actor_tenant_id;
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
 IF NEW.actor_id IS NOT NULL AND NOT EXISTS(SELECT 1 FROM users WHERE id=NEW.actor_id AND tenant_id=NEW.tenant_id) THEN RAISE EXCEPTION 'Incident execution actor tenant mismatch'; END IF;
 IF NEW.source_event_id IS NOT NULL AND NOT EXISTS(
  SELECT 1 FROM outbox_events o JOIN incidents i ON i.id=NEW.incident_id
  WHERE o.id=NEW.source_event_id AND o.tenant_id=NEW.tenant_id AND o.event_type='incident.created'
    AND o.aggregate_type='work_item' AND o.aggregate_id=i.work_item_id::text
    AND o.event_id='incident-created:'||i.work_item_id::text
    AND NEW.execution_key=CASE WHEN NEW.execution_kind='creation_event' THEN o.event_id ELSE o.event_id||':rule:'||NEW.rule_id::text END
 ) THEN RAISE EXCEPTION 'Incident execution source event identity mismatch'; END IF;
 RETURN NEW;
END $owner$;
