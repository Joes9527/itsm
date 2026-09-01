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
          AND table_relation.relname = 'process_callback_outboxes'
          AND table_relation.relkind IN ('r', 'p')
          AND column_attribute.attname = 'optional_declared'
          AND column_attribute.atttypid = 'boolean'::regtype
          AND column_attribute.attnotnull
          AND NOT column_attribute.attisdropped
          AND pg_get_expr(column_default.adbin, column_default.adrelid) = 'false'
    ) THEN
        RAISE EXCEPTION
            'process_callback_outboxes.optional_declared must be boolean NOT NULL DEFAULT false';
    END IF;
END $$;
