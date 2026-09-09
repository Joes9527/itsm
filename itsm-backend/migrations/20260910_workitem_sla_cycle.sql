-- No historical policy reconstruction: legacy bound records require explicit policy application.
ALTER TABLE tickets ADD COLUMN IF NOT EXISTS sla_cycle_number integer NOT NULL DEFAULT 0 CHECK (sla_cycle_number >= 0);
ALTER TABLE tickets ADD COLUMN IF NOT EXISTS sla_cycle_started_at timestamptz;
ALTER TABLE tickets ADD COLUMN IF NOT EXISTS sla_paused_minutes integer NOT NULL DEFAULT 0 CHECK (sla_paused_minutes >= 0);
ALTER TABLE tickets ADD COLUMN IF NOT EXISTS applied_sla_policy jsonb;
ALTER TABLE audit_logs ADD COLUMN IF NOT EXISTS operation_id varchar;
ALTER TABLE audit_logs ADD COLUMN IF NOT EXISTS request_digest varchar;
ALTER TABLE audit_logs ADD COLUMN IF NOT EXISTS result_version integer;
ALTER TABLE audit_logs ADD COLUMN IF NOT EXISTS result_status varchar;
CREATE UNIQUE INDEX IF NOT EXISTS audit_log_operation_receipt ON audit_logs (tenant_id, user_id, operation_id) WHERE operation_id IS NOT NULL;
-- Retain operation receipts for the full supported replay window. Outbox cleanup must never delete receipts.

-- Protect only durable action receipts and historical SLA facts. Ordinary HTTP
-- audit administration retains its existing semantics. No replay retention window
-- has been authorized, so these facts are retained until an explicit policy exists.
CREATE OR REPLACE FUNCTION protect_workitem_audit_fact() RETURNS trigger LANGUAGE plpgsql AS $$
BEGIN
 IF OLD.action = 'sla.cycle.completed' OR OLD.operation_id IS NOT NULL THEN
  RAISE EXCEPTION 'immutable work item audit fact';
 END IF;
 IF TG_OP = 'UPDATE' THEN
  IF NEW.action = 'sla.cycle.completed' OR NEW.operation_id IS NOT NULL THEN
   RAISE EXCEPTION 'immutable work item audit facts must be inserted';
  END IF;
  RETURN NEW;
 END IF;
 RETURN OLD;
END;
$$;
DROP TRIGGER IF EXISTS workitem_audit_fact_immutable ON audit_logs;
CREATE TRIGGER workitem_audit_fact_immutable BEFORE UPDATE OR DELETE ON audit_logs
 FOR EACH ROW EXECUTE FUNCTION protect_workitem_audit_fact();
