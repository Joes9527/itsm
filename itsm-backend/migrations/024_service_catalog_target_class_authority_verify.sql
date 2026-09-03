DO $$
BEGIN
    IF to_regclass(format('%I.service_catalogs', current_schema())) IS NULL THEN
        RAISE EXCEPTION 'required table service_catalogs is missing from schema %', current_schema();
    END IF;

    IF EXISTS (
        SELECT 1
        FROM pg_attribute column_attribute
        JOIN pg_class table_relation ON table_relation.oid = column_attribute.attrelid
        JOIN pg_namespace table_schema ON table_schema.oid = table_relation.relnamespace
        WHERE table_schema.nspname = current_schema()
          AND table_relation.relname = 'service_catalogs'
          AND column_attribute.attname = 'itsm_type'
          AND NOT column_attribute.attisdropped
    ) THEN
        RAISE EXCEPTION 'service_catalogs.itsm_type must be dropped';
    END IF;

    IF NOT EXISTS (
        SELECT 1
        FROM pg_attribute column_attribute
        JOIN pg_class table_relation ON table_relation.oid = column_attribute.attrelid
        JOIN pg_namespace table_schema ON table_schema.oid = table_relation.relnamespace
        WHERE table_schema.nspname = current_schema()
          AND table_relation.relname = 'service_catalogs'
          AND column_attribute.attname = 'target_class'
          AND column_attribute.attnotnull
          AND NOT column_attribute.attisdropped
    ) THEN
        RAISE EXCEPTION 'service_catalogs.target_class must be NOT NULL';
    END IF;

    IF NOT EXISTS (
        SELECT 1
        FROM pg_constraint constraint_relation
        JOIN pg_class table_relation ON table_relation.oid = constraint_relation.conrelid
        JOIN pg_namespace table_schema ON table_schema.oid = table_relation.relnamespace
        WHERE table_schema.nspname = current_schema()
          AND table_relation.relname = 'service_catalogs'
          AND constraint_relation.conname = 'service_catalogs_target_class_check'
          AND constraint_relation.contype = 'c'
          AND pg_get_constraintdef(constraint_relation.oid) =
              'CHECK (((target_class)::text = ANY ((ARRAY[''service_request_item''::character varying, ''incident''::character varying, ''change_request''::character varying])::text[])))'
    ) THEN
        RAISE EXCEPTION 'service_catalogs_target_class_check constraint is missing or does not match the authoritative definition';
    END IF;

    IF EXISTS (
        SELECT 1 FROM service_catalogs
        WHERE target_class IS NULL
           OR target_class NOT IN ('service_request_item', 'incident', 'change_request')
    ) THEN
        RAISE EXCEPTION 'service_catalogs contains rows with invalid target_class values';
    END IF;
END $$;
