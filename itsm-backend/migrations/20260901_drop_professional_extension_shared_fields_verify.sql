DO $verification$
DECLARE
    extension_table TEXT;
    extension_index TEXT;
    expected_record_class TEXT;
    shared_column TEXT;
    invalid_link_exists BOOLEAN;
BEGIN
    FOR extension_table, extension_index, expected_record_class IN
        SELECT * FROM (VALUES
            ('incidents', 'incident_work_item_id', 'incident'),
            ('problems', 'problem_work_item_id', 'problem'),
            ('changes', 'change_work_item_id', 'change_request')
        ) AS extensions(table_name, index_name, record_class)
    LOOP
        IF to_regclass(format('%I.%I', current_schema(), extension_table)) IS NULL THEN
            RAISE EXCEPTION 'required professional extension table % is missing from schema %',
                extension_table, current_schema();
        END IF;

        FOREACH shared_column IN ARRAY ARRAY['title', 'description', 'status', 'priority'] LOOP
            IF EXISTS (
                SELECT 1
                FROM information_schema.columns
                WHERE table_schema = current_schema()
                  AND table_name = extension_table
                  AND column_name = shared_column
            ) THEN
                RAISE EXCEPTION 'WorkItem-owned column %.% still exists', extension_table, shared_column;
            END IF;
        END LOOP;

        IF NOT EXISTS (
            SELECT 1
            FROM information_schema.columns
            WHERE table_schema = current_schema()
              AND table_name = extension_table
              AND column_name = 'work_item_id'
              AND is_nullable = 'NO'
        ) THEN
            RAISE EXCEPTION '%.work_item_id must exist and be NOT NULL', extension_table;
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
              ON attribute.attrelid = i.indrelid
             AND attribute.attnum = key_column.attnum
            WHERE index_schema.nspname = current_schema()
              AND index_relation.relname = extension_index
              AND table_schema.nspname = current_schema()
              AND table_relation.relname = extension_table
              AND i.indisunique
              AND i.indisvalid
              AND i.indisready
              AND i.indnkeyatts = 1
              AND i.indnatts = 1
              AND i.indexprs IS NULL
              AND i.indpred IS NULL
            GROUP BY i.indexrelid
            HAVING array_agg(attribute.attname ORDER BY key_column.ordinal) = ARRAY['work_item_id']::name[]
        ) THEN
            RAISE EXCEPTION '%.% must be a ready, valid, one-column unique index on %.work_item_id',
                current_schema(), extension_index, extension_table;
        END IF;

        EXECUTE format(
            'SELECT EXISTS ('
            'SELECT 1 FROM %I.%I extension '
            'LEFT JOIN %I.tickets work_item ON work_item.id = extension.work_item_id '
            'WHERE work_item.id IS NULL '
            'OR work_item.tenant_id <> extension.tenant_id '
            'OR work_item.record_class <> %L)',
            current_schema(), extension_table, current_schema(), expected_record_class
        ) INTO invalid_link_exists;
        IF invalid_link_exists THEN
            RAISE EXCEPTION '% extension has an invalid WorkItem tenant or record-class link',
                extension_table;
        END IF;
    END LOOP;
END $verification$;
