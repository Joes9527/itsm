DO $$
BEGIN
    IF EXISTS (SELECT 1 FROM service_catalogs LIMIT 1) THEN
        RAISE EXCEPTION 'reset requires an empty service_catalogs table; run development reset first';
    END IF;
END $$;

ALTER TABLE service_catalogs ADD COLUMN IF NOT EXISTS itsm_type character varying NOT NULL DEFAULT 'Request';
ALTER TABLE service_catalogs DROP CONSTRAINT IF EXISTS service_catalogs_target_class_check;
ALTER TABLE service_catalogs ALTER COLUMN target_class DROP NOT NULL;
