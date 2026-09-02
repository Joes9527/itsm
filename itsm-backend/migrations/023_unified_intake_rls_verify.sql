DO $verification$
DECLARE
    rls_table TEXT;
    canonical_policy_name TEXT;
    canonical_policy_expression TEXT;
    policy_using TEXT;
    policy_check TEXT;
    policy_roles OID[];
    policy_command "char";
    policy_permissive BOOLEAN;
BEGIN
    canonical_policy_expression := '(tenant_id = (NULLIF(current_setting(''app.current_tenant''::text, true), ''''::text))::bigint)';

    FOR rls_table IN SELECT unnest(ARRAY['intake_requests', 'intake_resolution_snapshots', 'external_identities']) LOOP
        IF to_regclass(format('%I.%I', current_schema(), rls_table)) IS NULL THEN
            RAISE EXCEPTION 'required table % is missing from schema %', rls_table, current_schema();
        END IF;

        IF NOT EXISTS (
            SELECT 1
            FROM pg_class relation
            JOIN pg_namespace namespace ON namespace.oid = relation.relnamespace
            WHERE namespace.nspname = current_schema()
              AND relation.relname = rls_table
              AND relation.relrowsecurity
              AND relation.relforcerowsecurity
        ) THEN
            RAISE EXCEPTION '%.% must have row level security enabled and forced', current_schema(), rls_table;
        END IF;

        canonical_policy_name := rls_table || '_tenant_isolation';
        policy_using := NULL;
        policy_check := NULL;
        policy_roles := NULL;
        policy_command := NULL;
        policy_permissive := NULL;

        SELECT pg_get_expr(policy.polqual, policy.polrelid),
               pg_get_expr(policy.polwithcheck, policy.polrelid),
               policy.polroles,
               policy.polcmd,
               policy.polpermissive
        INTO policy_using, policy_check, policy_roles, policy_command, policy_permissive
        FROM pg_policy policy
        JOIN pg_class relation ON relation.oid = policy.polrelid
        JOIN pg_namespace namespace ON namespace.oid = relation.relnamespace
        WHERE namespace.nspname = current_schema()
          AND relation.relname = rls_table
          AND policy.polname = canonical_policy_name;

        IF policy_using IS NULL OR policy_check IS NULL
           OR policy_using <> canonical_policy_expression
           OR policy_check <> canonical_policy_expression THEN
            RAISE EXCEPTION '%.% must use the authoritative tenant isolation predicate exactly',
                rls_table, canonical_policy_name;
        END IF;

        IF policy_roles <> ARRAY[0::OID] OR policy_command <> '*' OR NOT policy_permissive THEN
            RAISE EXCEPTION '%.% must have canonical PUBLIC/ALL/PERMISSIVE policy attributes',
                rls_table, canonical_policy_name;
        END IF;

        IF (SELECT COUNT(*)
            FROM pg_policy policy
            JOIN pg_class relation ON relation.oid = policy.polrelid
            JOIN pg_namespace namespace ON namespace.oid = relation.relnamespace
            WHERE namespace.nspname = current_schema()
              AND relation.relname = rls_table) <> 1 THEN
            RAISE EXCEPTION '% must have exactly one RLS policy', rls_table;
        END IF;
    END LOOP;

    IF NOT EXISTS (
        SELECT 1
        FROM pg_constraint constraint_relation
        JOIN pg_class table_relation ON table_relation.oid = constraint_relation.conrelid
        JOIN pg_namespace table_schema ON table_schema.oid = table_relation.relnamespace
        WHERE table_schema.nspname = current_schema()
          AND table_relation.relname = 'intake_requests'
          AND constraint_relation.conname = 'intake_requests_completed_work_item_check'
          AND constraint_relation.contype = 'c'
    ) THEN
        RAISE EXCEPTION 'intake_requests_completed_work_item_check constraint is missing from schema %', current_schema();
    END IF;
END $verification$;
