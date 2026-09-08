DO $reset$
BEGIN
    IF to_regclass(format('%I.process_instances', current_schema())) IS NULL THEN
        RAISE EXCEPTION 'required local process_instances table is missing';
    END IF;
    IF EXISTS (SELECT 1 FROM process_instances LIMIT 1) THEN
        RAISE EXCEPTION 'reset requires an empty process_instances table; durable start receipts cannot be discarded';
    END IF;
END $reset$;
ALTER TABLE process_instances DROP COLUMN IF EXISTS start_request_digest;
