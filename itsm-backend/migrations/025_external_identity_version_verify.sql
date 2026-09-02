DO $$
BEGIN
    IF NOT EXISTS (
        SELECT 1
        FROM pg_attribute column_attribute
        JOIN pg_class table_relation
            ON table_relation.oid = column_attribute.attrelid
        JOIN pg_namespace table_schema
            ON table_schema.oid = table_relation.relnamespace
        JOIN pg_attrdef column_default
            ON column_default.adrelid = table_relation.oid
           AND column_default.adnum = column_attribute.attnum
        WHERE table_schema.nspname = current_schema()
          AND table_relation.relname = 'external_identities'
          AND table_relation.relkind IN ('r', 'p')
          AND column_attribute.attname = 'version'
          AND column_attribute.atttypid = 'bigint'::regtype
          AND column_attribute.attnotnull
          AND NOT column_attribute.attisdropped
          AND pg_get_expr(column_default.adbin, column_default.adrelid) = '1'
    ) THEN
        RAISE EXCEPTION
            'external_identities.version must be bigint NOT NULL DEFAULT 1';
    END IF;

    IF NOT EXISTS (
        SELECT 1
        FROM pg_constraint constraint_relation
        JOIN pg_class table_relation ON table_relation.oid = constraint_relation.conrelid
        JOIN pg_namespace table_schema ON table_schema.oid = table_relation.relnamespace
        WHERE table_schema.nspname = current_schema()
          AND table_relation.relname = 'external_identities'
          AND constraint_relation.conname = 'external_identities_version_positive'
          AND constraint_relation.contype = 'c'
    ) THEN
        RAISE EXCEPTION
            'external_identities_version_positive constraint is missing from schema %', current_schema();
    END IF;
END $$;
