package migration

const processStartRequestDigestSQL = `DO $prerequisite$
BEGIN
    IF to_regclass(format('%I.process_instances', current_schema())) IS NULL THEN
        RAISE EXCEPTION 'required local process_instances table is missing';
    END IF;
END $prerequisite$;
ALTER TABLE process_instances ADD COLUMN IF NOT EXISTS start_request_digest varchar NULL;
DO $verify$
BEGIN
    IF NOT EXISTS (
        SELECT 1 FROM pg_attribute a
        JOIN pg_class t ON t.oid = a.attrelid
        JOIN pg_namespace n ON n.oid = t.relnamespace
        WHERE n.nspname = current_schema() AND t.relname = 'process_instances'
          AND a.attname = 'start_request_digest' AND NOT a.attisdropped
          AND a.atttypid = 'varchar'::regtype AND NOT a.attnotnull
          AND (a.atttypmod = -1 OR a.atttypmod >= 68)
          AND NOT a.atthasdef
    ) THEN
        RAISE EXCEPTION 'process_instances.start_request_digest must be nullable varchar (capacity >=64) without a default; reconcile schema before retry';
    END IF;
    IF NOT EXISTS (
        SELECT 1 FROM pg_index i
        JOIN pg_class t ON t.oid = i.indrelid
        JOIN pg_namespace n ON n.oid = t.relnamespace
        JOIN pg_attribute a ON a.attrelid = t.oid AND a.attnum = ANY(i.indkey)
        WHERE n.nspname = current_schema() AND t.relname = 'process_instances'
          AND a.attname = 'process_instance_id' AND a.attnotnull
          AND i.indisunique AND i.indisvalid AND i.indisready
          AND i.indnatts = 1 AND i.indpred IS NULL AND i.indexprs IS NULL
    ) THEN
        RAISE EXCEPTION 'process_instances.process_instance_id requires an existing complete unique index; repair the identity prerequisite before retry';
    END IF;
END $verify$;
`

const processStartRequestDigestDevelopmentResetSQL = `DO $reset$
BEGIN
    IF to_regclass(format('%I.process_instances', current_schema())) IS NULL THEN
        RAISE EXCEPTION 'required local process_instances table is missing';
    END IF;
    IF EXISTS (SELECT 1 FROM process_instances LIMIT 1) THEN
        RAISE EXCEPTION 'reset requires an empty process_instances table; durable start receipts cannot be discarded';
    END IF;
END $reset$;
ALTER TABLE process_instances DROP COLUMN IF EXISTS start_request_digest;
`
