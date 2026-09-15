-- Deferred C3 only. This file is deliberately NOT registered with migration.RegisteredMigrations.
-- No bootstrap, release migration, or schema generation step may execute it.
-- Run only after observed zero old readers/writers, tenant-by-tenant historical-data
-- review and retained export, explicit observation-gate approval, and verified backup.
-- It does not backfill, translate historical intent, or remove WorkItemRelation history.
-- The approved operator must explicitly SET LOCAL itsm.workitem_relation_retirement_approved = 'true'
-- inside a transaction before this block; default execution fails closed.
DO $retirement$
BEGIN
 IF current_setting('itsm.workitem_relation_retirement_approved', true) IS DISTINCT FROM 'true' THEN
  RAISE EXCEPTION 'C3 observation gate has not approved legacy relation retirement';
 END IF;
 ALTER TABLE tickets DROP CONSTRAINT IF EXISTS tickets_problems_tickets;
 ALTER TABLE tickets DROP COLUMN IF EXISTS problem_tickets;
 DROP TABLE IF EXISTS problem_incidents;
 DROP TABLE IF EXISTS problem_changes;
END $retirement$;
