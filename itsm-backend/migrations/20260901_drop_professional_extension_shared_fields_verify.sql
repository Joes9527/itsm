DO $verification$
DECLARE
    extension_table TEXT;
    extension_index TEXT;
    extension_constraint TEXT;
    expected_record_class TEXT;
    shared_column TEXT;
    shared_columns TEXT[];
    invalid_link_exists BOOLEAN;
    policy_using TEXT;
    policy_check TEXT;
BEGIN
    FOR extension_table, extension_index, extension_constraint, expected_record_class IN
        SELECT * FROM (VALUES
            ('incidents', 'incident_work_item_id', 'incidents_tickets_work_item', 'incident'),
            ('problems', 'problem_work_item_id', 'problems_tickets_work_item', 'problem'),
            ('changes', 'change_work_item_id', 'changes_tickets_work_item', 'change_request')
        ) AS extensions(table_name, index_name, constraint_name, record_class)
    LOOP
        IF to_regclass(format('%I.%I', current_schema(), extension_table)) IS NULL THEN
            RAISE EXCEPTION 'required professional extension table % is missing from schema %',
                extension_table, current_schema();
        END IF;

        shared_columns := CASE extension_table
            WHEN 'incidents' THEN ARRAY[
                'title', 'description', 'status', 'priority', 'reporter_id', 'assignee_id',
                'category', 'subcategory', 'source', 'tenant_id', 'version', 'created_at',
                'updated_at', 'resolved_at', 'closed_at', 'deleted_at'
            ]
            WHEN 'problems' THEN ARRAY[
                'title', 'description', 'status', 'priority', 'category', 'assignee_id',
                'created_by', 'tenant_id', 'created_at', 'updated_at', 'resolved_at',
                'closed_at', 'deleted_at'
            ]
            ELSE ARRAY[
                'title', 'description', 'status', 'priority', 'assignee_id', 'created_by',
                'tenant_id', 'related_tickets', 'created_at', 'updated_at'
            ]
        END;
        FOREACH shared_column IN ARRAY shared_columns LOOP
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
			WHERE constraint_relation.conname = extension_constraint
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
			RAISE EXCEPTION '%.% must be an exact validated foreign key from %.work_item_id to tickets.id',
				current_schema(), extension_constraint, extension_table;
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
            'WHERE work_item.id IS NULL OR work_item.record_class <> %L)',
            current_schema(), extension_table, current_schema(), expected_record_class
        ) INTO invalid_link_exists;
        IF invalid_link_exists THEN
            RAISE EXCEPTION '% extension has an invalid WorkItem record-class link',
                extension_table;
        END IF;
    END LOOP;

	SELECT pg_get_expr(policy.polqual, policy.polrelid),
	       pg_get_expr(policy.polwithcheck, policy.polrelid)
	INTO policy_using, policy_check
	FROM pg_policy policy
	JOIN pg_class relation ON relation.oid = policy.polrelid
	JOIN pg_namespace namespace ON namespace.oid = relation.relnamespace
	WHERE namespace.nspname = current_schema()
	  AND relation.relname = 'changes'
	  AND policy.polname = 'tenant_isolation_changes';

	IF policy_using IS NULL OR policy_check IS NULL
	   OR position('work_item.tenant_id' IN policy_using) = 0
	   OR position('work_item.deleted_at IS NULL' IN policy_using) = 0
	   OR position('app.current_tenant' IN policy_using) = 0
	   OR position('app.current_tenant_id' IN policy_using) > 0
	   OR position('work_item.tenant_id' IN policy_check) = 0
	   OR position('work_item.deleted_at IS NULL' IN policy_check) = 0
	   OR position('app.current_tenant' IN policy_check) = 0
	   OR position('app.current_tenant_id' IN policy_check) > 0 THEN
		RAISE EXCEPTION 'changes.tenant_isolation_changes must use authoritative WorkItem tenant and soft-delete scope';
	END IF;

	IF (SELECT COUNT(*)
		FROM pg_policy policy
		JOIN pg_class relation ON relation.oid = policy.polrelid
		JOIN pg_namespace namespace ON namespace.oid = relation.relnamespace
		WHERE namespace.nspname = current_schema()
		  AND relation.relname = 'changes') <> 1 THEN
		RAISE EXCEPTION 'changes must have exactly one canonical RLS policy';
	END IF;

	IF NOT EXISTS (
		SELECT 1
		FROM pg_class relation
		JOIN pg_namespace namespace ON namespace.oid = relation.relnamespace
		WHERE namespace.nspname = current_schema()
		  AND relation.relname = 'changes'
		  AND relation.relrowsecurity
		  AND NOT relation.relforcerowsecurity
	) THEN
		RAISE EXCEPTION 'changes RLS must be enabled without FORCE';
	END IF;
END $verification$;
