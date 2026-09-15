-- Prepared A3a identity changes. Incorporate this SQL into the canonical
-- A3b/A7 migration stream together with legacy reader/column cutover.
-- It is deliberately not a separately registered production migration.
ALTER TABLE tickets ADD COLUMN IF NOT EXISTS generic_subtype varchar;

-- Preserve the two existing generic product subtypes. Other legacy generic
-- classifications need the A3b reconciliation preflight, not silent conversion.
UPDATE tickets
SET generic_subtype = type
WHERE record_class = 'generic'
  AND type IN ('ticket', 'improvement')
  AND (generic_subtype IS NULL OR generic_subtype = '');

DO $identity$
DECLARE
    incident_table regclass;
    number_attribute smallint;
    candidate record;
BEGIN
    incident_table := to_regclass(format('%I.incidents', current_schema()));
    IF incident_table IS NULL THEN
        RAISE EXCEPTION 'incidents table is required for identity migration';
    END IF;
    SELECT attnum INTO number_attribute FROM pg_attribute
    WHERE attrelid = incident_table AND attname = 'incident_number' AND NOT attisdropped;
    IF number_attribute IS NULL THEN
        RAISE EXCEPTION 'legacy incident_number column is required for identity migration';
    END IF;

    -- Discover real catalog metadata rather than assuming Ent's generated name.
    -- Never remove multi-column identities, primary keys, or dependent objects.
    FOR candidate IN
        SELECT conname FROM pg_constraint
        WHERE conrelid = incident_table AND contype = 'u'
          AND conkey = ARRAY[number_attribute]::smallint[]
    LOOP
        EXECUTE format('ALTER TABLE %s DROP CONSTRAINT %I', incident_table, candidate.conname);
    END LOOP;
    FOR candidate IN
        SELECT n.nspname, c.relname
        FROM pg_index i
        JOIN pg_class c ON c.oid = i.indexrelid
        JOIN pg_namespace n ON n.oid = c.relnamespace
        WHERE i.indrelid = incident_table AND i.indisunique AND NOT i.indisprimary
          AND i.indexprs IS NULL AND i.indnkeyatts = 1
          AND i.indkey[0] = number_attribute
    LOOP
        EXECUTE format('DROP INDEX %I.%I', candidate.nspname, candidate.relname);
    END LOOP;
END $identity$;
