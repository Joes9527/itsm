DO $$
BEGIN
    IF EXISTS (
        SELECT 1 FROM information_schema.columns
        WHERE table_schema = current_schema()
          AND table_name = 'service_catalogs'
          AND column_name = 'itsm_type'
    ) THEN
        UPDATE service_catalogs
        SET target_class = CASE itsm_type
            WHEN 'Incident' THEN 'incident'
            WHEN 'Change' THEN 'change_request'
            ELSE 'service_request_item'
        END
        WHERE target_class IS NULL
           OR target_class NOT IN ('service_request_item', 'incident', 'change_request');
    END IF;
END $$;

DO $$
BEGIN
    IF EXISTS (
        SELECT 1 FROM service_catalogs
        WHERE target_class IS NULL
           OR target_class NOT IN ('service_request_item', 'incident', 'change_request')
    ) THEN
        RAISE EXCEPTION 'service_catalogs.target_class has invalid or NULL values after backfill';
    END IF;
END $$;

ALTER TABLE service_catalogs ALTER COLUMN target_class SET NOT NULL;
ALTER TABLE service_catalogs DROP CONSTRAINT IF EXISTS service_catalogs_target_class_check;
ALTER TABLE service_catalogs
    ADD CONSTRAINT service_catalogs_target_class_check
    CHECK (target_class IN ('service_request_item', 'incident', 'change_request'));

ALTER TABLE service_catalogs DROP COLUMN IF EXISTS itsm_type;
