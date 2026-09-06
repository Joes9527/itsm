DO $verify$
BEGIN
 IF EXISTS(SELECT 1 FROM information_schema.columns WHERE table_schema=current_schema() AND table_name='service_catalogs' AND column_name='itsm_type') THEN RAISE EXCEPTION 'retired Catalog itsm_type must be absent'; END IF;
END $verify$;
