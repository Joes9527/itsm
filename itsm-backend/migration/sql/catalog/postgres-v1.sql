WITH target_schema AS (
    SELECT current_schema()::text AS name
), catalog_records AS (
    SELECT jsonb_build_array(
        'schema', (SELECT name FROM target_schema)
    )::text AS record

    UNION ALL

    SELECT jsonb_build_array(
        'extension', extension_record.extname, namespace.nspname,
        extension_record.extversion
    )::text
    FROM pg_extension extension_record
    JOIN pg_namespace namespace ON namespace.oid = extension_record.extnamespace

    UNION ALL

    SELECT jsonb_build_array(
        'extension-member', extension_record.extname, identified.type,
        COALESCE(identified.schema, ''), COALESCE(identified.name, ''),
        identified.identity
    )::text
    FROM pg_depend dependency
    JOIN pg_extension extension_record ON extension_record.oid = dependency.refobjid
    CROSS JOIN LATERAL pg_identify_object(
        dependency.classid, dependency.objid, dependency.objsubid
    ) identified
    WHERE dependency.refclassid = 'pg_extension'::regclass
      AND dependency.deptype = 'e'

    UNION ALL

    SELECT jsonb_build_array(
        'relation', relation.relname, relation.relkind::text,
        relation.relpersistence::text, relation.relrowsecurity,
        relation.relforcerowsecurity, relation.relispartition,
        relation.relhassubclass, relation.relreplident::text,
        COALESCE(pg_get_viewdef(relation.oid, true), '')
    )::text
    FROM pg_class relation
    JOIN pg_namespace namespace ON namespace.oid = relation.relnamespace
    WHERE namespace.nspname = (SELECT name FROM target_schema)
      AND relation.relkind IN ('r', 'p', 'v', 'm', 'f', 'c')

    UNION ALL

    SELECT jsonb_build_array(
        'column', relation.relname, attribute.attnum, attribute.attname,
        format_type(attribute.atttypid, attribute.atttypmod),
        attribute.attnotnull, attribute.attidentity::text,
        attribute.attgenerated::text, attribute.attndims,
        COALESCE(collation_namespace.nspname, ''),
        COALESCE(collation_record.collname, ''), attribute.attstorage::text,
        COALESCE(pg_get_expr(default_record.adbin, default_record.adrelid), '')
    )::text
    FROM pg_attribute attribute
    JOIN pg_class relation ON relation.oid = attribute.attrelid
    JOIN pg_namespace namespace ON namespace.oid = relation.relnamespace
    LEFT JOIN pg_attrdef default_record
      ON default_record.adrelid = attribute.attrelid
     AND default_record.adnum = attribute.attnum
    LEFT JOIN pg_collation collation_record ON collation_record.oid = attribute.attcollation
    LEFT JOIN pg_namespace collation_namespace ON collation_namespace.oid = collation_record.collnamespace
    WHERE namespace.nspname = (SELECT name FROM target_schema)
      AND relation.relkind IN ('r', 'p', 'v', 'm', 'f', 'c')
      AND attribute.attnum > 0
      AND NOT attribute.attisdropped

    UNION ALL

    SELECT jsonb_build_array(
        'constraint', constraint_record.conname, constraint_record.contype::text,
        COALESCE(relation.relname, ''), COALESCE(type_record.typname, ''),
        COALESCE(referenced_namespace.nspname, ''),
        COALESCE(referenced_relation.relname, ''),
        constraint_record.condeferrable, constraint_record.condeferred,
        constraint_record.convalidated, constraint_record.connoinherit,
        constraint_record.confupdtype::text, constraint_record.confdeltype::text,
        constraint_record.confmatchtype::text,
        pg_get_constraintdef(constraint_record.oid, true)
    )::text
    FROM pg_constraint constraint_record
    LEFT JOIN pg_class relation ON relation.oid = constraint_record.conrelid
    LEFT JOIN pg_namespace relation_namespace ON relation_namespace.oid = relation.relnamespace
    LEFT JOIN pg_type type_record ON type_record.oid = constraint_record.contypid
    LEFT JOIN pg_namespace type_namespace ON type_namespace.oid = type_record.typnamespace
    LEFT JOIN pg_class referenced_relation ON referenced_relation.oid = constraint_record.confrelid
    LEFT JOIN pg_namespace referenced_namespace ON referenced_namespace.oid = referenced_relation.relnamespace
    WHERE relation_namespace.nspname = (SELECT name FROM target_schema)
       OR type_namespace.nspname = (SELECT name FROM target_schema)

    UNION ALL

    SELECT jsonb_build_array(
        'index', table_relation.relname, index_relation.relname,
        access_method.amname, index_record.indisunique,
        index_record.indisprimary, index_record.indisexclusion,
        index_record.indimmediate, index_record.indisclustered,
        index_record.indisvalid, index_record.indisready,
        index_record.indisreplident, index_record.indkey::text,
        index_record.indoption::text,
        COALESCE(index_relation.reloptions::text, ''),
        pg_get_indexdef(index_record.indexrelid),
        COALESCE(pg_get_expr(index_record.indpred, index_record.indrelid), '')
    )::text
    FROM pg_index index_record
    JOIN pg_class index_relation ON index_relation.oid = index_record.indexrelid
    JOIN pg_class table_relation ON table_relation.oid = index_record.indrelid
    JOIN pg_namespace namespace ON namespace.oid = table_relation.relnamespace
    JOIN pg_am access_method ON access_method.oid = index_relation.relam
    WHERE namespace.nspname = (SELECT name FROM target_schema)

    UNION ALL

    SELECT jsonb_build_array(
        'policy', relation.relname, policy.polname, policy.polcmd::text,
        policy.polpermissive,
        ARRAY(
            SELECT CASE WHEN role_id = 0 THEN 'PUBLIC' ELSE role_record.rolname END
            FROM unnest(policy.polroles) role_id
            LEFT JOIN pg_roles role_record ON role_record.oid = role_id
            ORDER BY CASE WHEN role_id = 0 THEN 'PUBLIC' ELSE role_record.rolname END
        )::text,
        COALESCE(pg_get_expr(policy.polqual, policy.polrelid), ''),
        COALESCE(pg_get_expr(policy.polwithcheck, policy.polrelid), '')
    )::text
    FROM pg_policy policy
    JOIN pg_class relation ON relation.oid = policy.polrelid
    JOIN pg_namespace namespace ON namespace.oid = relation.relnamespace
    WHERE namespace.nspname = (SELECT name FROM target_schema)

    UNION ALL

    SELECT jsonb_build_array(
        'trigger', relation.relname, trigger_record.tgname,
        trigger_record.tgenabled::text, trigger_record.tgdeferrable,
        trigger_record.tginitdeferred,
        pg_get_triggerdef(trigger_record.oid, true)
    )::text
    FROM pg_trigger trigger_record
    JOIN pg_class relation ON relation.oid = trigger_record.tgrelid
    JOIN pg_namespace namespace ON namespace.oid = relation.relnamespace
    WHERE namespace.nspname = (SELECT name FROM target_schema)
      AND NOT trigger_record.tgisinternal

    UNION ALL

    SELECT jsonb_build_array(
        'sequence', sequence_relation.relname,
        format_type(sequence_record.seqtypid, NULL),
        sequence_record.seqstart, sequence_record.seqincrement,
        sequence_record.seqmax, sequence_record.seqmin,
        sequence_record.seqcache, sequence_record.seqcycle,
        COALESCE(owner_relation.relname, ''),
        COALESCE(owner_attribute.attname, '')
    )::text
    FROM pg_sequence sequence_record
    JOIN pg_class sequence_relation ON sequence_relation.oid = sequence_record.seqrelid
    JOIN pg_namespace namespace ON namespace.oid = sequence_relation.relnamespace
    LEFT JOIN pg_depend ownership
      ON ownership.classid = 'pg_class'::regclass
     AND ownership.objid = sequence_relation.oid
     AND ownership.refclassid = 'pg_class'::regclass
     AND ownership.refobjsubid > 0
     AND ownership.deptype IN ('a', 'i')
    LEFT JOIN pg_class owner_relation ON owner_relation.oid = ownership.refobjid
    LEFT JOIN pg_attribute owner_attribute
      ON owner_attribute.attrelid = ownership.refobjid
     AND owner_attribute.attnum = ownership.refobjsubid
    WHERE namespace.nspname = (SELECT name FROM target_schema)

    UNION ALL

    SELECT jsonb_build_array(
        'routine', routine.proname, routine.prokind::text,
        pg_get_function_identity_arguments(routine.oid),
        pg_get_function_result(routine.oid), language.lanname,
        routine.provolatile::text, routine.proparallel::text,
        routine.proisstrict, routine.prosecdef, routine.proleakproof,
        COALESCE(routine.proconfig::text, ''),
        CASE
          WHEN routine.prokind IN ('f', 'p', 'w')
            THEN pg_get_functiondef(routine.oid)
          ELSE routine.prosrc
        END
    )::text
    FROM pg_proc routine
    JOIN pg_namespace namespace ON namespace.oid = routine.pronamespace
    JOIN pg_language language ON language.oid = routine.prolang
    WHERE namespace.nspname = (SELECT name FROM target_schema)

    UNION ALL

    SELECT jsonb_build_array(
        'type', type_record.typname, type_record.typtype::text,
        type_record.typcategory::text, type_record.typispreferred,
        type_record.typisdefined, type_record.typnotnull,
        type_record.typdelim::text, type_record.typlen,
        type_record.typbyval, type_record.typalign::text,
        type_record.typstorage::text,
        CASE WHEN type_record.typelem = 0 THEN '' ELSE format_type(type_record.typelem, NULL) END,
        CASE WHEN type_record.typbasetype = 0 THEN '' ELSE format_type(type_record.typbasetype, type_record.typtypmod) END,
        COALESCE(relation.relname, ''), COALESCE(type_record.typdefault, ''),
        COALESCE((
            SELECT jsonb_agg(enum_record.enumlabel ORDER BY enum_record.enumsortorder)::text
            FROM pg_enum enum_record
            WHERE enum_record.enumtypid = type_record.oid
        ), '')
    )::text
    FROM pg_type type_record
    JOIN pg_namespace namespace ON namespace.oid = type_record.typnamespace
    LEFT JOIN pg_class relation ON relation.oid = type_record.typrelid
    WHERE namespace.nspname = (SELECT name FROM target_schema)

    UNION ALL

    SELECT jsonb_build_array(
        'operator', operator_record.oprname,
        CASE WHEN operator_record.oprleft = 0 THEN '' ELSE format_type(operator_record.oprleft, NULL) END,
        CASE WHEN operator_record.oprright = 0 THEN '' ELSE format_type(operator_record.oprright, NULL) END,
        format_type(operator_record.oprresult, NULL),
        operator_record.oprcanmerge, operator_record.oprcanhash,
        operator_record.oprcode::regprocedure::text,
        CASE WHEN operator_record.oprcom = 0 THEN '' ELSE operator_record.oprcom::regoperator::text END,
        CASE WHEN operator_record.oprnegate = 0 THEN '' ELSE operator_record.oprnegate::regoperator::text END
    )::text
    FROM pg_operator operator_record
    JOIN pg_namespace namespace ON namespace.oid = operator_record.oprnamespace
    WHERE namespace.nspname = (SELECT name FROM target_schema)

    UNION ALL

    SELECT jsonb_build_array(
        'operator-class', operator_class.opcname, access_method.amname,
        operator_family.opfname, format_type(operator_class.opcintype, NULL),
        CASE WHEN operator_class.opckeytype = 0 THEN '' ELSE format_type(operator_class.opckeytype, NULL) END,
        operator_class.opcdefault
    )::text
    FROM pg_opclass operator_class
    JOIN pg_namespace namespace ON namespace.oid = operator_class.opcnamespace
    JOIN pg_am access_method ON access_method.oid = operator_class.opcmethod
    JOIN pg_opfamily operator_family ON operator_family.oid = operator_class.opcfamily
    WHERE namespace.nspname = (SELECT name FROM target_schema)

    UNION ALL

    SELECT jsonb_build_array(
        'inheritance', child_namespace.nspname, child.relname,
        parent_namespace.nspname, parent.relname,
        inheritance.inhseqno, inheritance.inhdetachpending
    )::text
    FROM pg_inherits inheritance
    JOIN pg_class child ON child.oid = inheritance.inhrelid
    JOIN pg_namespace child_namespace ON child_namespace.oid = child.relnamespace
    JOIN pg_class parent ON parent.oid = inheritance.inhparent
    JOIN pg_namespace parent_namespace ON parent_namespace.oid = parent.relnamespace
    WHERE child_namespace.nspname = (SELECT name FROM target_schema)
       OR parent_namespace.nspname = (SELECT name FROM target_schema)
)
SELECT COALESCE(string_agg(record, E'\n' ORDER BY record), '')
FROM catalog_records
