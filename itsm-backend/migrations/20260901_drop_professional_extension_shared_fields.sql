DO $migration$
DECLARE
    extension_table TEXT;
    extension_index TEXT;
    extension_constraint TEXT;
    duplicate_work_item_id BIGINT;
    existing_index_oid OID;
    existing_index_unique BOOLEAN;
    existing_constraint_oid OID;
    work_item_foreign_key_count INTEGER;
    orphan_work_item_id BIGINT;
BEGIN
    -- Development cutover: BPMN ProcessTask/ProcessApprovalDecision are the
    -- only ticket approval runtime. No legacy row migration is supported.
    EXECUTE format('DROP TABLE IF EXISTS %I.ticket_approvals CASCADE', current_schema());
	EXECUTE format('DROP TABLE IF EXISTS %I.workflow_tasks CASCADE', current_schema());
	EXECUTE format('DROP TABLE IF EXISTS %I.workflow_instances CASCADE', current_schema());
	EXECUTE format('DROP TABLE IF EXISTS %I.workflow_versions CASCADE', current_schema());
	EXECUTE format('DROP TABLE IF EXISTS %I.workflows CASCADE', current_schema());
	IF to_regclass(format('%I.ticket_categories', current_schema())) IS NOT NULL THEN
		EXECUTE format('ALTER TABLE %I.ticket_categories DROP COLUMN IF EXISTS workflow_id', current_schema());
	END IF;

    IF to_regclass(format('%I.tickets', current_schema())) IS NULL THEN
        RAISE EXCEPTION 'required WorkItem table tickets is missing from schema %', current_schema();
    END IF;

    -- Legacy direct tenant policies depend on columns removed below. Replace every
    -- professional extension policy in this same authoritative cutover.
    FOR extension_table IN SELECT unnest(ARRAY['incidents', 'problems', 'changes']) LOOP
        IF to_regclass(format('%I.%I', current_schema(), extension_table)) IS NOT NULL THEN
            EXECUTE format('DROP POLICY IF EXISTS tenant_isolation ON %I.%I', current_schema(), extension_table);
            EXECUTE format('DROP POLICY IF EXISTS %I ON %I.%I',
                'tenant_isolation_' || extension_table, current_schema(), extension_table);
        END IF;
    END LOOP;

    FOR extension_table, extension_index, extension_constraint IN
        SELECT * FROM (VALUES
            ('incidents', 'incident_work_item_id', 'incidents_tickets_work_item'),
            ('problems', 'problem_work_item_id', 'problems_tickets_work_item'),
            ('changes', 'change_work_item_id', 'changes_tickets_work_item')
        ) AS extensions(table_name, index_name, constraint_name)
    LOOP
        IF to_regclass(format('%I.%I', current_schema(), extension_table)) IS NULL THEN
            RAISE EXCEPTION 'required professional extension table % is missing from schema %',
                extension_table, current_schema();
        END IF;

        EXECUTE format(
            'SELECT extension.work_item_id FROM %I.%I extension '
            'LEFT JOIN %I.tickets work_item ON work_item.id = extension.work_item_id '
            'WHERE work_item.id IS NULL LIMIT 1',
            current_schema(), extension_table, current_schema()
        ) INTO orphan_work_item_id;
        IF orphan_work_item_id IS NOT NULL THEN
            RAISE EXCEPTION '%.work_item_id has orphan WorkItem reference %', extension_table, orphan_work_item_id;
        END IF;

        SELECT constraint_relation.oid
        INTO existing_constraint_oid
        FROM pg_constraint constraint_relation
        JOIN pg_class extension_relation ON extension_relation.oid = constraint_relation.conrelid
        JOIN pg_namespace extension_schema ON extension_schema.oid = extension_relation.relnamespace
        WHERE extension_schema.nspname = current_schema()
          AND extension_relation.relname = extension_table
          AND constraint_relation.conname = extension_constraint;

        SELECT COUNT(*)
        INTO work_item_foreign_key_count
        FROM pg_constraint constraint_relation
        JOIN pg_class extension_relation ON extension_relation.oid = constraint_relation.conrelid
        JOIN pg_namespace extension_schema ON extension_schema.oid = extension_relation.relnamespace
        JOIN pg_attribute extension_column
          ON extension_column.attrelid = extension_relation.oid
         AND extension_column.attnum = ANY (constraint_relation.conkey)
        WHERE extension_schema.nspname = current_schema()
          AND extension_relation.relname = extension_table
          AND constraint_relation.contype = 'f'
          AND extension_column.attname = 'work_item_id';

        IF existing_constraint_oid IS NOT NULL AND work_item_foreign_key_count <> 1 THEN
            RAISE EXCEPTION '%.work_item_id has an additional foreign key constraint', extension_table;
        END IF;

        IF existing_constraint_oid IS NOT NULL AND NOT EXISTS (
            SELECT 1
            FROM pg_constraint constraint_relation
            JOIN pg_class extension_relation ON extension_relation.oid = constraint_relation.conrelid
            JOIN pg_namespace extension_schema ON extension_schema.oid = extension_relation.relnamespace
            JOIN pg_class work_item_relation ON work_item_relation.oid = constraint_relation.confrelid
            JOIN pg_namespace work_item_schema ON work_item_schema.oid = work_item_relation.relnamespace
            JOIN pg_attribute extension_column
              ON extension_column.attrelid = extension_relation.oid
             AND extension_column.attnum = constraint_relation.conkey[1]
            JOIN pg_attribute work_item_column
              ON work_item_column.attrelid = work_item_relation.oid
             AND work_item_column.attnum = constraint_relation.confkey[1]
            WHERE constraint_relation.oid = existing_constraint_oid
              AND constraint_relation.contype = 'f'
              AND constraint_relation.convalidated
              AND NOT constraint_relation.condeferrable
              AND constraint_relation.confdeltype = 'a'
              AND constraint_relation.confupdtype = 'a'
              AND cardinality(constraint_relation.conkey) = 1
              AND cardinality(constraint_relation.confkey) = 1
              AND extension_schema.nspname = current_schema()
              AND extension_relation.relname = extension_table
              AND extension_column.attname = 'work_item_id'
              AND work_item_schema.nspname = current_schema()
              AND work_item_relation.relname = 'tickets'
              AND work_item_column.attname = 'id'
        ) THEN
            RAISE EXCEPTION 'constraint %.% conflicts with required %.work_item_id foreign key',
                current_schema(), extension_constraint, extension_table;
        END IF;

        IF existing_constraint_oid IS NULL THEN
            IF EXISTS (
                SELECT 1
                FROM pg_constraint constraint_relation
                JOIN pg_class extension_relation ON extension_relation.oid = constraint_relation.conrelid
                JOIN pg_namespace extension_schema ON extension_schema.oid = extension_relation.relnamespace
                JOIN pg_attribute extension_column
                  ON extension_column.attrelid = extension_relation.oid
                 AND extension_column.attnum = ANY (constraint_relation.conkey)
                WHERE extension_schema.nspname = current_schema()
                  AND extension_relation.relname = extension_table
                  AND constraint_relation.contype = 'f'
                  AND extension_column.attname = 'work_item_id'
            ) THEN
                RAISE EXCEPTION '%.work_item_id has a non-authoritative foreign key constraint', extension_table;
            END IF;
            EXECUTE format(
                'ALTER TABLE %I.%I ADD CONSTRAINT %I FOREIGN KEY (work_item_id) REFERENCES %I.tickets(id)',
                current_schema(), extension_table, extension_constraint, current_schema()
            );
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
        existing_constraint_oid := NULL;
        work_item_foreign_key_count := NULL;
        orphan_work_item_id := NULL;
    END LOOP;

    FOR extension_table IN SELECT unnest(ARRAY['incidents', 'problems', 'changes']) LOOP
        EXECUTE format(
            'CREATE POLICY %I ON %I.%I AS PERMISSIVE FOR ALL TO PUBLIC '
            'USING (EXISTS (SELECT 1 FROM %I.tickets work_item '
            'WHERE work_item.id = %I.work_item_id '
            'AND work_item.tenant_id = NULLIF(current_setting(''app.current_tenant'', true), '''')::bigint '
            'AND work_item.deleted_at IS NULL)) '
            'WITH CHECK (EXISTS (SELECT 1 FROM %I.tickets work_item '
            'WHERE work_item.id = %I.work_item_id '
            'AND work_item.tenant_id = NULLIF(current_setting(''app.current_tenant'', true), '''')::bigint '
            'AND work_item.deleted_at IS NULL))',
            'tenant_isolation_' || extension_table, current_schema(), extension_table,
            current_schema(), extension_table, current_schema(), extension_table
        );
        EXECUTE format('ALTER TABLE %I.%I ENABLE ROW LEVEL SECURITY', current_schema(), extension_table);
        EXECUTE format('ALTER TABLE %I.%I NO FORCE ROW LEVEL SECURITY', current_schema(), extension_table);
    END LOOP;
END $migration$;
