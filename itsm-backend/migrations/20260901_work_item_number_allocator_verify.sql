DO $$
BEGIN
    IF to_regclass(format('%I.%I', current_schema(), 'work_item_number_sequences')) IS NULL THEN
        RAISE EXCEPTION 'work_item_number_sequences table is missing';
    END IF;

    IF NOT EXISTS (
        SELECT 1
        FROM pg_index i
        JOIN pg_class index_relation ON index_relation.oid = i.indexrelid
        JOIN pg_namespace index_schema ON index_schema.oid = index_relation.relnamespace
        JOIN pg_class table_relation ON table_relation.oid = i.indrelid
        JOIN pg_namespace table_schema ON table_schema.oid = table_relation.relnamespace
        JOIN LATERAL unnest(i.indkey) WITH ORDINALITY AS key_column(attnum, ordinal) ON TRUE
        JOIN pg_attribute attribute
            ON attribute.attrelid = i.indrelid AND attribute.attnum = key_column.attnum
        WHERE index_schema.nspname = current_schema()
          AND table_schema.nspname = current_schema()
          AND table_relation.relname = 'work_item_number_sequences'
          AND index_relation.relname = 'workitemnumbersequence_tenant_id_period'
          AND i.indisunique
          AND i.indisvalid
          AND i.indisready
        GROUP BY i.indexrelid
        HAVING array_agg(attribute.attname ORDER BY key_column.ordinal) = ARRAY['tenant_id', 'period']::name[]
    ) THEN
        RAISE EXCEPTION 'workitemnumbersequence_tenant_id_period unique index is missing';
    END IF;

    IF NOT EXISTS (
        SELECT 1
        FROM pg_index i
        JOIN pg_class index_relation ON index_relation.oid = i.indexrelid
        JOIN pg_namespace index_schema ON index_schema.oid = index_relation.relnamespace
        JOIN pg_class table_relation ON table_relation.oid = i.indrelid
        JOIN pg_namespace table_schema ON table_schema.oid = table_relation.relnamespace
        JOIN LATERAL unnest(i.indkey) WITH ORDINALITY AS key_column(attnum, ordinal) ON TRUE
        JOIN pg_attribute attribute
            ON attribute.attrelid = i.indrelid AND attribute.attnum = key_column.attnum
        WHERE index_schema.nspname = current_schema()
          AND table_schema.nspname = current_schema()
          AND table_relation.relname = 'tickets'
          AND index_relation.relname = 'ticket_tenant_id_ticket_number'
          AND i.indisunique
          AND i.indisvalid
          AND i.indisready
        GROUP BY i.indexrelid
        HAVING array_agg(attribute.attname ORDER BY key_column.ordinal) = ARRAY['tenant_id', 'ticket_number']::name[]
    ) THEN
        RAISE EXCEPTION 'ticket_tenant_id_ticket_number unique index is missing';
    END IF;

    IF NOT EXISTS (
        SELECT 1
        FROM pg_constraint
        WHERE conrelid = to_regclass(format('%I.%I', current_schema(), 'work_item_number_sequences'))
          AND conname = 'work_item_number_sequences_period_check'
          AND contype = 'c'
    ) THEN
        RAISE EXCEPTION 'work_item_number_sequences_period_check is missing';
    END IF;

    IF NOT EXISTS (
        SELECT 1
        FROM pg_constraint
        WHERE conrelid = to_regclass(format('%I.%I', current_schema(), 'work_item_number_sequences'))
          AND conname = 'work_item_number_sequences_last_value_check'
          AND contype = 'c'
    ) THEN
        RAISE EXCEPTION 'work_item_number_sequences_last_value_check is missing';
    END IF;

    IF EXISTS (
        SELECT 1
        FROM tickets
        GROUP BY tenant_id, ticket_number
        HAVING COUNT(*) > 1
    ) THEN
        RAISE EXCEPTION 'duplicate tenant-scoped ticket numbers exist';
    END IF;

    IF EXISTS (
        SELECT 1
        FROM work_item_number_sequences
        WHERE period !~ '^[0-9]{6}$'
           OR last_value NOT BETWEEN 0 AND 999999
    ) THEN
        RAISE EXCEPTION 'work item number sequence period or last_value is invalid';
    END IF;
END $$;
