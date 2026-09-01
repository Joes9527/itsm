-- Development-only reset. There is no historical-data compatibility path;
-- reassert the authoritative target schema, scoped only to current_schema().
DO $migration$
DECLARE
    extension_table TEXT;
    extension_index TEXT;
    duplicate_work_item_id BIGINT;
    existing_index_oid OID;
    existing_index_unique BOOLEAN;
BEGIN
	IF to_regclass(format('%I.changes', current_schema())) IS NOT NULL THEN
		EXECUTE format('DROP POLICY IF EXISTS tenant_isolation ON %I.changes', current_schema());
		EXECUTE format('DROP POLICY IF EXISTS tenant_isolation_changes ON %I.changes', current_schema());
	END IF;

    FOR extension_table, extension_index IN
        SELECT * FROM (VALUES
            ('incidents', 'incident_work_item_id'),
            ('problems', 'problem_work_item_id'),
            ('changes', 'change_work_item_id')
        ) AS extensions(table_name, index_name)
    LOOP
        IF to_regclass(format('%I.%I', current_schema(), extension_table)) IS NULL THEN
            RAISE EXCEPTION 'required professional extension table % is missing from schema %',
                extension_table, current_schema();
        END IF;

        EXECUTE format(
            'SELECT work_item_id FROM %I.%I WHERE work_item_id IS NOT NULL '
            'GROUP BY work_item_id HAVING COUNT(*) > 1 LIMIT 1',
            current_schema(), extension_table
        ) INTO duplicate_work_item_id;
        IF duplicate_work_item_id IS NOT NULL THEN
            RAISE EXCEPTION '%.work_item_id has duplicate work_item_id %',
                extension_table, duplicate_work_item_id;
        END IF;

        -- Validate a pre-existing named index before dropping shared columns. PostgreSQL
        -- may otherwise remove a conflicting index as a dependency of a dropped column,
        -- silently turning an invalid catalog shape into an apparently valid migration.
        SELECT index_relation.oid
        INTO existing_index_oid
        FROM pg_class index_relation
        JOIN pg_namespace index_schema ON index_schema.oid = index_relation.relnamespace
        JOIN pg_index i ON i.indexrelid = index_relation.oid
        WHERE index_schema.nspname = current_schema()
          AND index_relation.relname = extension_index;

        IF existing_index_oid IS NOT NULL AND NOT EXISTS (
            SELECT 1
            FROM pg_index i
            JOIN pg_class table_relation ON table_relation.oid = i.indrelid
            JOIN pg_namespace table_schema ON table_schema.oid = table_relation.relnamespace
            JOIN LATERAL unnest(i.indkey) WITH ORDINALITY AS key_column(attnum, ordinal) ON TRUE
            JOIN pg_attribute attribute
              ON attribute.attrelid = i.indrelid
             AND attribute.attnum = key_column.attnum
            WHERE i.indexrelid = existing_index_oid
              AND table_schema.nspname = current_schema()
              AND table_relation.relname = extension_table
              AND i.indisvalid
              AND i.indisready
              AND i.indnkeyatts = 1
              AND i.indnatts = 1
              AND i.indexprs IS NULL
              AND i.indpred IS NULL
            GROUP BY i.indexrelid
            HAVING array_agg(attribute.attname ORDER BY key_column.ordinal) = ARRAY['work_item_id']::name[]
        ) THEN
            RAISE EXCEPTION 'index %.% conflicts with required %.work_item_id index',
                current_schema(), extension_index, extension_table;
        END IF;

        IF extension_table = 'incidents' THEN
            EXECUTE format(
                'ALTER TABLE %I.incidents '
                'DROP COLUMN IF EXISTS title, DROP COLUMN IF EXISTS description, '
                'DROP COLUMN IF EXISTS status, DROP COLUMN IF EXISTS priority, '
                'DROP COLUMN IF EXISTS reporter_id, DROP COLUMN IF EXISTS assignee_id, '
                'DROP COLUMN IF EXISTS category, DROP COLUMN IF EXISTS subcategory, '
                'DROP COLUMN IF EXISTS source, DROP COLUMN IF EXISTS tenant_id, '
                'DROP COLUMN IF EXISTS version, DROP COLUMN IF EXISTS created_at, '
                'DROP COLUMN IF EXISTS updated_at, DROP COLUMN IF EXISTS resolved_at, '
                'DROP COLUMN IF EXISTS closed_at, DROP COLUMN IF EXISTS deleted_at, '
                'ALTER COLUMN work_item_id SET NOT NULL', current_schema());
        ELSIF extension_table = 'problems' THEN
            EXECUTE format(
                'ALTER TABLE %I.problems '
                'DROP COLUMN IF EXISTS title, DROP COLUMN IF EXISTS description, '
                'DROP COLUMN IF EXISTS status, DROP COLUMN IF EXISTS priority, '
                'DROP COLUMN IF EXISTS category, DROP COLUMN IF EXISTS assignee_id, '
                'DROP COLUMN IF EXISTS created_by, DROP COLUMN IF EXISTS tenant_id, '
                'DROP COLUMN IF EXISTS created_at, DROP COLUMN IF EXISTS updated_at, '
                'DROP COLUMN IF EXISTS resolved_at, DROP COLUMN IF EXISTS closed_at, '
                'DROP COLUMN IF EXISTS deleted_at, ALTER COLUMN work_item_id SET NOT NULL',
                current_schema());
        ELSE
            EXECUTE format(
                'ALTER TABLE %I.changes '
                'DROP COLUMN IF EXISTS title, DROP COLUMN IF EXISTS description, '
                'DROP COLUMN IF EXISTS status, DROP COLUMN IF EXISTS priority, '
                'DROP COLUMN IF EXISTS assignee_id, DROP COLUMN IF EXISTS created_by, '
                'DROP COLUMN IF EXISTS tenant_id, DROP COLUMN IF EXISTS related_tickets, '
                'DROP COLUMN IF EXISTS created_at, DROP COLUMN IF EXISTS updated_at, '
                'ALTER COLUMN work_item_id SET NOT NULL', current_schema());
        END IF;

        SELECT index_relation.oid, i.indisunique
        INTO existing_index_oid, existing_index_unique
        FROM pg_class index_relation
        JOIN pg_namespace index_schema ON index_schema.oid = index_relation.relnamespace
        JOIN pg_index i ON i.indexrelid = index_relation.oid
        WHERE index_schema.nspname = current_schema()
          AND index_relation.relname = extension_index;

        IF existing_index_oid IS NOT NULL AND NOT EXISTS (
            SELECT 1
            FROM pg_index i
            JOIN pg_class table_relation ON table_relation.oid = i.indrelid
            JOIN pg_namespace table_schema ON table_schema.oid = table_relation.relnamespace
            JOIN LATERAL unnest(i.indkey) WITH ORDINALITY AS key_column(attnum, ordinal) ON TRUE
            JOIN pg_attribute attribute
              ON attribute.attrelid = i.indrelid
             AND attribute.attnum = key_column.attnum
            WHERE i.indexrelid = existing_index_oid
              AND table_schema.nspname = current_schema()
              AND table_relation.relname = extension_table
              AND i.indisvalid
              AND i.indisready
              AND i.indnkeyatts = 1
              AND i.indnatts = 1
              AND i.indexprs IS NULL
              AND i.indpred IS NULL
            GROUP BY i.indexrelid
            HAVING array_agg(attribute.attname ORDER BY key_column.ordinal) = ARRAY['work_item_id']::name[]
        ) THEN
            RAISE EXCEPTION 'index %.% conflicts with required %.work_item_id index',
                current_schema(), extension_index, extension_table;
        END IF;

        IF existing_index_oid IS NOT NULL AND NOT existing_index_unique THEN
            EXECUTE format('DROP INDEX %I.%I', current_schema(), extension_index);
            existing_index_oid := NULL;
        END IF;

        IF existing_index_oid IS NULL THEN
            EXECUTE format(
                'CREATE UNIQUE INDEX %I ON %I.%I (work_item_id)',
                extension_index, current_schema(), extension_table
            );
        END IF;

        duplicate_work_item_id := NULL;
        existing_index_oid := NULL;
        existing_index_unique := NULL;
    END LOOP;

	EXECUTE format(
		'CREATE POLICY tenant_isolation_changes ON %I.changes '
		'USING (EXISTS (SELECT 1 FROM %I.tickets work_item '
		'WHERE work_item.id = changes.work_item_id '
		'AND work_item.tenant_id = NULLIF(current_setting(''app.current_tenant'', true), '''')::bigint '
		'AND work_item.deleted_at IS NULL)) '
		'WITH CHECK (EXISTS (SELECT 1 FROM %I.tickets work_item '
		'WHERE work_item.id = changes.work_item_id '
		'AND work_item.tenant_id = NULLIF(current_setting(''app.current_tenant'', true), '''')::bigint '
		'AND work_item.deleted_at IS NULL))',
		current_schema(), current_schema(), current_schema()
	);
	EXECUTE format('ALTER TABLE %I.changes ENABLE ROW LEVEL SECURITY', current_schema());
	EXECUTE format('ALTER TABLE %I.changes NO FORCE ROW LEVEL SECURITY', current_schema());
END $migration$;
