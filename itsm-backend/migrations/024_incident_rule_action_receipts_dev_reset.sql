DO $reset$
DECLARE has_receipts boolean;
BEGIN
 IF to_regclass(format('%I.incident_rule_executions',current_schema())) IS NULL THEN RAISE EXCEPTION 'required local execution table missing'; END IF;
 IF EXISTS(SELECT 1 FROM incident_rule_executions LIMIT 1) THEN RAISE EXCEPTION 'reset requires empty execution history; durable receipts cannot be discarded'; END IF;
 IF to_regclass(format('%I.incident_rule_action_receipts',current_schema())) IS NOT NULL THEN
  EXECUTE format('SELECT EXISTS(SELECT 1 FROM %I.incident_rule_action_receipts LIMIT 1)',current_schema()) INTO has_receipts;
  IF has_receipts THEN RAISE EXCEPTION 'reset requires empty action receipts'; END IF;
  EXECUTE format('DROP TABLE %I.incident_rule_action_receipts',current_schema());
 END IF;
 EXECUTE format('DROP FUNCTION IF EXISTS %I.validate_incident_rule_action_receipt_owner()',current_schema());
END $reset$;
DROP TRIGGER IF EXISTS incident_rule_execution_owner ON incident_rule_executions;
DO $functions$
BEGIN
 EXECUTE format('DROP FUNCTION IF EXISTS %I.validate_incident_rule_execution_owner()',current_schema());
END $functions$;
ALTER TABLE incident_rule_executions DROP CONSTRAINT IF EXISTS incident_rule_execution_identity;
ALTER TABLE incident_rule_executions DROP COLUMN IF EXISTS execution_kind,DROP COLUMN IF EXISTS execution_key,DROP COLUMN IF EXISTS source_event_id,DROP COLUMN IF EXISTS actor_id,DROP COLUMN IF EXISTS source,DROP COLUMN IF EXISTS frozen_actions;
ALTER TABLE incident_rule_executions ALTER COLUMN rule_id SET NOT NULL;
