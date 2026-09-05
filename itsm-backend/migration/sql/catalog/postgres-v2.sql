WITH RECURSIVE role_identity AS (
    SELECT $1::text AS migration_name,
           $2::text AS runtime_name,
           $3::text AS bootstrap_name,
           (SELECT oid FROM pg_roles WHERE rolname = $1) AS migration_oid,
           (SELECT oid FROM pg_roles WHERE rolname = $2) AS runtime_oid,
           (SELECT oid FROM pg_roles WHERE rolname = $3) AS bootstrap_oid
), role_labels AS (
    SELECT role_record.oid,
           CASE
             WHEN role_record.rolname = identity.migration_name THEN '$migration'
             WHEN role_record.rolname = identity.runtime_name THEN '$runtime'
             WHEN role_record.rolname = identity.bootstrap_name THEN '$bootstrap'
             WHEN role_record.rolname ~ '^pg_' THEN '$system:' || role_record.rolname
             ELSE '$unknown'
           END AS label
    FROM pg_roles role_record
    CROSS JOIN role_identity identity
), target_schema AS (
    SELECT namespace.oid, namespace.nspname::text AS name,
           namespace.nspowner, namespace.nspacl
    FROM pg_namespace namespace
    WHERE namespace.nspname = current_schema()
), schema_state_relation AS (
    SELECT relation.oid, relation.relowner, relation.relacl
    FROM pg_class relation
    JOIN target_schema namespace ON namespace.oid = relation.relnamespace
    WHERE relation.relname = 'schema_state'
      AND relation.relkind IN ('r', 'p')
), schema_state_direct_writer_roles AS (
    SELECT migration_oid AS oid FROM role_identity WHERE migration_oid IS NOT NULL
    UNION
    SELECT bootstrap_oid FROM role_identity WHERE bootstrap_oid IS NOT NULL
    UNION
    SELECT oid FROM pg_roles WHERE rolname = 'pg_write_all_data'
    UNION
    SELECT acl.grantee
    FROM schema_state_relation relation
    CROSS JOIN LATERAL aclexplode(COALESCE(relation.relacl, acldefault('r', relation.relowner))) acl
    WHERE acl.grantee <> 0
      AND acl.privilege_type IN ('INSERT', 'UPDATE', 'DELETE', 'TRUNCATE')
    UNION
    SELECT acl.grantee
    FROM pg_attribute attribute
    JOIN schema_state_relation relation ON relation.oid = attribute.attrelid
    CROSS JOIN LATERAL aclexplode(attribute.attacl) acl
    WHERE attribute.attnum > 0
      AND NOT attribute.attisdropped
      AND acl.grantee <> 0
      AND acl.privilege_type IN ('INSERT', 'UPDATE')
), role_reachability AS (
    SELECT membership.member, membership.roleid AS reachable
    FROM pg_auth_members membership
    WHERE membership.inherit_option OR membership.set_option
    UNION
    SELECT reachability.member, membership.roleid
    FROM role_reachability reachability
    JOIN pg_auth_members membership ON membership.member = reachability.reachable
    WHERE membership.inherit_option OR membership.set_option
), unauthorized_schema_state_writers AS (
    SELECT DISTINCT role_record.oid
    FROM pg_roles role_record
    CROSS JOIN role_identity identity
    WHERE role_record.oid NOT IN (identity.migration_oid, identity.bootstrap_oid)
      AND role_record.rolname !~ '^pg_'
      AND (
          role_record.rolsuper
          OR role_record.oid IN (SELECT oid FROM schema_state_direct_writer_roles)
          OR EXISTS (
              SELECT 1
              FROM role_reachability reachability
              WHERE reachability.member = role_record.oid
                AND reachability.reachable IN (SELECT oid FROM schema_state_direct_writer_roles)
          )
      )
), schema_state_public_write_grants AS (
    SELECT acl.privilege_type
    FROM schema_state_relation relation
    CROSS JOIN LATERAL aclexplode(COALESCE(relation.relacl, acldefault('r', relation.relowner))) acl
    WHERE acl.grantee = 0
      AND acl.privilege_type IN ('INSERT', 'UPDATE', 'DELETE', 'TRUNCATE')
    UNION ALL
    SELECT acl.privilege_type
    FROM pg_attribute attribute
    JOIN schema_state_relation relation ON relation.oid = attribute.attrelid
    CROSS JOIN LATERAL aclexplode(attribute.attacl) acl
    WHERE attribute.attnum > 0
      AND NOT attribute.attisdropped
      AND acl.grantee = 0
      AND acl.privilege_type IN ('INSERT', 'UPDATE')
), catalog_records AS (
    SELECT jsonb_build_array(
        'schema', (SELECT name FROM target_schema)
    )::text AS record

    UNION ALL

    SELECT jsonb_build_array(
        'schema-security', namespace.name,
        owner_label.label,
        COALESCE((
            SELECT jsonb_agg(
                jsonb_build_array(
                    CASE WHEN acl.grantee = 0 THEN 'PUBLIC' ELSE grantee_label.label END,
                    acl.privilege_type,
                    acl.is_grantable,
                    grantor_label.label
                )
                ORDER BY
                    CASE WHEN acl.grantee = 0 THEN 'PUBLIC' ELSE grantee_label.label END,
                    acl.privilege_type, acl.is_grantable
            )
            FROM aclexplode(COALESCE(namespace.nspacl, acldefault('n', namespace.nspowner))) acl
            LEFT JOIN role_labels grantee_label ON grantee_label.oid = acl.grantee
            LEFT JOIN role_labels grantor_label ON grantor_label.oid = acl.grantor
        ), '[]'::jsonb)
    )::text
    FROM target_schema namespace
    JOIN role_labels owner_label ON owner_label.oid = namespace.nspowner

    UNION ALL

    SELECT jsonb_build_array(
        'relation-security', relation.relname, relation.relkind::text,
        owner_label.label,
        COALESCE((
            SELECT jsonb_agg(
                jsonb_build_array(
                    CASE WHEN acl.grantee = 0 THEN 'PUBLIC' ELSE grantee_label.label END,
                    acl.privilege_type,
                    acl.is_grantable,
                    grantor_label.label
                )
                ORDER BY
                    CASE WHEN acl.grantee = 0 THEN 'PUBLIC' ELSE grantee_label.label END,
                    acl.privilege_type, acl.is_grantable
            )
            FROM aclexplode(COALESCE(relation.relacl, acldefault(
                CASE WHEN relation.relkind = 'S' THEN 'S'::"char" ELSE 'r'::"char" END,
                relation.relowner
            ))) acl
            LEFT JOIN role_labels grantee_label ON grantee_label.oid = acl.grantee
            LEFT JOIN role_labels grantor_label ON grantor_label.oid = acl.grantor
        ), '[]'::jsonb)
    )::text
    FROM pg_class relation
    JOIN pg_namespace namespace ON namespace.oid = relation.relnamespace
    JOIN role_labels owner_label ON owner_label.oid = relation.relowner
    WHERE namespace.nspname = (SELECT name FROM target_schema)
      AND relation.relkind IN ('r', 'p', 'v', 'm', 'f', 'S')

    UNION ALL

    SELECT jsonb_build_array(
        'column-security', relation.relname,
        row_number() OVER (PARTITION BY relation.oid ORDER BY attribute.attnum),
        attribute.attname,
        COALESCE((
            SELECT jsonb_agg(
                jsonb_build_array(
                    CASE WHEN acl.grantee = 0 THEN 'PUBLIC' ELSE grantee_label.label END,
                    acl.privilege_type,
                    acl.is_grantable,
                    grantor_label.label
                )
                ORDER BY
                    CASE WHEN acl.grantee = 0 THEN 'PUBLIC' ELSE grantee_label.label END,
                    acl.privilege_type, acl.is_grantable
            )
            FROM aclexplode(attribute.attacl) acl
            LEFT JOIN role_labels grantee_label ON grantee_label.oid = acl.grantee
            LEFT JOIN role_labels grantor_label ON grantor_label.oid = acl.grantor
        ), '[]'::jsonb)
    )::text
    FROM pg_attribute attribute
    JOIN pg_class relation ON relation.oid = attribute.attrelid
    JOIN pg_namespace namespace ON namespace.oid = relation.relnamespace
    WHERE namespace.nspname = (SELECT name FROM target_schema)
      AND relation.relkind IN ('r', 'p', 'v', 'm', 'f', 'c')
      AND attribute.attnum > 0
      AND NOT attribute.attisdropped

    UNION ALL

    SELECT jsonb_build_array(
        'default-acl',
        owner_label.label,
        COALESCE(namespace.nspname, '<all-schemas>'),
        default_acl.defaclobjtype::text,
        COALESCE((
            SELECT jsonb_agg(
                jsonb_build_array(
                    CASE WHEN acl.grantee = 0 THEN 'PUBLIC' ELSE grantee_label.label END,
                    acl.privilege_type,
                    acl.is_grantable,
                    grantor_label.label
                )
                ORDER BY
                    CASE WHEN acl.grantee = 0 THEN 'PUBLIC' ELSE grantee_label.label END,
                    acl.privilege_type, acl.is_grantable
            )
            FROM aclexplode(default_acl.defaclacl) acl
            LEFT JOIN role_labels grantee_label ON grantee_label.oid = acl.grantee
            LEFT JOIN role_labels grantor_label ON grantor_label.oid = acl.grantor
        ), '[]'::jsonb)
    )::text
    FROM pg_default_acl default_acl
    JOIN role_labels owner_label ON owner_label.oid = default_acl.defaclrole
    LEFT JOIN pg_namespace namespace ON namespace.oid = default_acl.defaclnamespace
    WHERE default_acl.defaclnamespace = 0
       OR default_acl.defaclnamespace = (SELECT oid FROM target_schema)

    UNION ALL

    SELECT jsonb_build_array(
        'schema-state-effective-writer-boundary',
        (SELECT count(*) FROM unauthorized_schema_state_writers),
        (SELECT count(*) FROM schema_state_public_write_grants)
    )::text

    UNION ALL

    SELECT jsonb_build_array(
        'event-trigger', event_trigger.evtname, event_trigger.evtevent,
        event_trigger.evtenabled::text,
        COALESCE(event_trigger.evttags::text, ''),
        routine_namespace.nspname, routine.proname,
        pg_get_function_identity_arguments(routine.oid),
        owner_label.label
    )::text
    FROM pg_event_trigger event_trigger
    JOIN pg_proc routine ON routine.oid = event_trigger.evtfoid
    JOIN pg_namespace routine_namespace ON routine_namespace.oid = routine.pronamespace
    JOIN role_labels owner_label ON owner_label.oid = routine.proowner

    UNION ALL

    SELECT jsonb_build_array(
        'publication', publication.pubname,
        owner_label.label,
        publication.puballtables, publication.pubinsert,
        publication.pubupdate, publication.pubdelete,
        publication.pubtruncate, publication.pubviaroot
    )::text
    FROM pg_publication publication
    JOIN role_labels owner_label ON owner_label.oid = publication.pubowner
    WHERE publication.puballtables
       OR EXISTS (
           SELECT 1
           FROM pg_publication_rel publication_relation
           JOIN pg_class relation ON relation.oid = publication_relation.prrelid
           JOIN pg_namespace namespace ON namespace.oid = relation.relnamespace
           WHERE publication_relation.prpubid = publication.oid
             AND namespace.nspname = (SELECT name FROM target_schema)
       )
       OR EXISTS (
           SELECT 1
           FROM pg_publication_namespace publication_namespace
           JOIN pg_namespace namespace ON namespace.oid = publication_namespace.pnnspid
           WHERE publication_namespace.pnpubid = publication.oid
             AND namespace.nspname = (SELECT name FROM target_schema)
       )

    UNION ALL

    SELECT jsonb_build_array(
        'publication-namespace', publication.pubname, namespace.nspname
    )::text
    FROM pg_publication_namespace publication_namespace
    JOIN pg_publication publication ON publication.oid = publication_namespace.pnpubid
    JOIN pg_namespace namespace ON namespace.oid = publication_namespace.pnnspid
    WHERE namespace.nspname = (SELECT name FROM target_schema)

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
        relation.relreplident::text,
        COALESCE(access_method.amname, ''),
        COALESCE(tablespace.spcname, ''),
        COALESCE((
            SELECT array_agg(option ORDER BY option)::text
            FROM unnest(relation.reloptions) option
        ), ''),
        COALESCE(pg_get_viewdef(relation.oid, true), '')
    )::text
    FROM pg_class relation
    JOIN pg_namespace namespace ON namespace.oid = relation.relnamespace
    LEFT JOIN pg_am access_method ON access_method.oid = relation.relam
    LEFT JOIN pg_tablespace tablespace ON tablespace.oid = relation.reltablespace
    WHERE namespace.nspname = (SELECT name FROM target_schema)
      AND relation.relkind IN ('r', 'p', 'v', 'm', 'f', 'c')

    UNION ALL

    SELECT jsonb_build_array(
        'foreign-table', relation.relname, foreign_server.srvname,
        COALESCE((
            SELECT array_agg(option ORDER BY option)::text
            FROM unnest(foreign_table.ftoptions) option
        ), '')
    )::text
    FROM pg_foreign_table foreign_table
    JOIN pg_class relation ON relation.oid = foreign_table.ftrelid
    JOIN pg_namespace namespace ON namespace.oid = relation.relnamespace
    JOIN pg_foreign_server foreign_server ON foreign_server.oid = foreign_table.ftserver
    WHERE namespace.nspname = (SELECT name FROM target_schema)

    UNION ALL

    SELECT jsonb_build_array(
        'partition-key', relation.relname,
        partitioned_table.partstrat::text, partitioned_table.partnatts,
        COALESCE(pg_get_expr(partitioned_table.partexprs, partitioned_table.partrelid), ''),
        pg_get_partkeydef(partitioned_table.partrelid)
    )::text
    FROM pg_partitioned_table partitioned_table
    JOIN pg_class relation ON relation.oid = partitioned_table.partrelid
    JOIN pg_namespace namespace ON namespace.oid = relation.relnamespace
    WHERE namespace.nspname = (SELECT name FROM target_schema)

    UNION ALL

    SELECT jsonb_build_array(
        'partition-bound', relation.relname,
        pg_get_expr(relation.relpartbound, relation.oid, true)
    )::text
    FROM pg_class relation
    JOIN pg_namespace namespace ON namespace.oid = relation.relnamespace
    WHERE namespace.nspname = (SELECT name FROM target_schema)
      AND relation.relispartition

    UNION ALL

    SELECT jsonb_build_array(
        'publication-relation', publication.pubname, relation.relname,
        COALESCE(pg_get_expr(publication_relation.prqual, publication_relation.prrelid), ''),
        COALESCE((
            SELECT array_agg(attribute.attname ORDER BY attribute_number)::text
            FROM unnest(publication_relation.prattrs) attribute_number
            JOIN pg_attribute attribute
              ON attribute.attrelid = publication_relation.prrelid
             AND attribute.attnum = attribute_number
        ), '')
    )::text
    FROM pg_publication_rel publication_relation
    JOIN pg_publication publication ON publication.oid = publication_relation.prpubid
    JOIN pg_class relation ON relation.oid = publication_relation.prrelid
    JOIN pg_namespace namespace ON namespace.oid = relation.relnamespace
    WHERE namespace.nspname = (SELECT name FROM target_schema)

    UNION ALL

    SELECT jsonb_build_array(
        'column', relation.relname,
        row_number() OVER (PARTITION BY relation.oid ORDER BY attribute.attnum),
        attribute.attname,
        format_type(attribute.atttypid, attribute.atttypmod),
        attribute.attnotnull, attribute.attidentity::text,
        attribute.attgenerated::text, attribute.attndims,
        COALESCE(collation_namespace.nspname, ''),
        COALESCE(collation_record.collname, ''), attribute.attstorage::text,
        attribute.attcompression::text, attribute.attstattarget,
        attribute.attislocal, attribute.attinhcount,
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
        index_record.indisreplident,
        ARRAY(
            SELECT pg_get_indexdef(index_record.indexrelid, position, true)
            FROM generate_series(1, index_record.indnatts) position
            ORDER BY position
        )::text,
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
            SELECT CASE WHEN role_id = 0 THEN 'PUBLIC' ELSE role_label.label END
            FROM unnest(policy.polroles) role_id
            LEFT JOIN role_labels role_label ON role_label.oid = role_id
            ORDER BY CASE WHEN role_id = 0 THEN 'PUBLIC' ELSE role_label.label END
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
        'rule', relation.relname, rewrite_record.rulename,
        rewrite_record.ev_type::text, rewrite_record.ev_enabled::text,
        rewrite_record.is_instead,
        pg_get_ruledef(rewrite_record.oid, true)
    )::text
    FROM pg_rewrite rewrite_record
    JOIN pg_class relation ON relation.oid = rewrite_record.ev_class
    JOIN pg_namespace namespace ON namespace.oid = relation.relnamespace
    WHERE namespace.nspname = (SELECT name FROM target_schema)
      AND rewrite_record.rulename <> '_RETURN'

    UNION ALL

    SELECT jsonb_build_array(
        'sequence', sequence_relation.relname,
        format_type(sequence_record.seqtypid, NULL),
        sequence_record.seqstart, sequence_record.seqincrement,
        sequence_record.seqmax, sequence_record.seqmin,
        sequence_record.seqcache, sequence_record.seqcycle,
        sequence_relation.relpersistence::text,
        COALESCE(tablespace.spcname, ''),
        COALESCE((
            SELECT array_agg(option ORDER BY option)::text
            FROM unnest(sequence_relation.reloptions) option
        ), ''),
        COALESCE(owner_relation.relname, ''),
        COALESCE(owner_attribute.attname, '')
    )::text
    FROM pg_sequence sequence_record
    JOIN pg_class sequence_relation ON sequence_relation.oid = sequence_record.seqrelid
    JOIN pg_namespace namespace ON namespace.oid = sequence_relation.relnamespace
    LEFT JOIN pg_tablespace tablespace ON tablespace.oid = sequence_relation.reltablespace
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
        'aggregate', routine.proname,
        pg_get_function_identity_arguments(routine.oid),
        aggregate_record.aggkind::text,
        aggregate_record.aggnumdirectargs,
        aggregate_record.aggtransfn::regprocedure::text,
        CASE WHEN aggregate_record.aggfinalfn = 0 THEN '' ELSE aggregate_record.aggfinalfn::regprocedure::text END,
        CASE WHEN aggregate_record.aggcombinefn = 0 THEN '' ELSE aggregate_record.aggcombinefn::regprocedure::text END,
        CASE WHEN aggregate_record.aggserialfn = 0 THEN '' ELSE aggregate_record.aggserialfn::regprocedure::text END,
        CASE WHEN aggregate_record.aggdeserialfn = 0 THEN '' ELSE aggregate_record.aggdeserialfn::regprocedure::text END,
        CASE WHEN aggregate_record.aggmtransfn = 0 THEN '' ELSE aggregate_record.aggmtransfn::regprocedure::text END,
        CASE WHEN aggregate_record.aggminvtransfn = 0 THEN '' ELSE aggregate_record.aggminvtransfn::regprocedure::text END,
        CASE WHEN aggregate_record.aggmfinalfn = 0 THEN '' ELSE aggregate_record.aggmfinalfn::regprocedure::text END,
        aggregate_record.aggfinalextra, aggregate_record.aggmfinalextra,
        aggregate_record.aggfinalmodify::text, aggregate_record.aggmfinalmodify::text,
        CASE WHEN aggregate_record.aggsortop = 0 THEN '' ELSE aggregate_record.aggsortop::regoperator::text END,
        format_type(aggregate_record.aggtranstype, NULL), aggregate_record.aggtransspace,
        CASE WHEN aggregate_record.aggmtranstype = 0 THEN '' ELSE format_type(aggregate_record.aggmtranstype, NULL) END,
        aggregate_record.aggmtransspace,
        COALESCE(aggregate_record.agginitval, ''), COALESCE(aggregate_record.aggminitval, '')
    )::text
    FROM pg_aggregate aggregate_record
    JOIN pg_proc routine ON routine.oid = aggregate_record.aggfnoid
    JOIN pg_namespace namespace ON namespace.oid = routine.pronamespace
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
        'range', range_type.typname,
        format_type(range_record.rngsubtype, NULL),
        CASE WHEN range_record.rngmultitypid = 0 THEN '' ELSE format_type(range_record.rngmultitypid, NULL) END,
        COALESCE(collation_namespace.nspname, ''), COALESCE(collation_record.collname, ''),
        operator_namespace.nspname, operator_class.opcname, access_method.amname,
        CASE WHEN range_record.rngcanonical = 0 THEN '' ELSE range_record.rngcanonical::regprocedure::text END,
        CASE WHEN range_record.rngsubdiff = 0 THEN '' ELSE range_record.rngsubdiff::regprocedure::text END
    )::text
    FROM pg_range range_record
    JOIN pg_type range_type ON range_type.oid = range_record.rngtypid
    JOIN pg_namespace namespace ON namespace.oid = range_type.typnamespace
    JOIN pg_opclass operator_class ON operator_class.oid = range_record.rngsubopc
    JOIN pg_namespace operator_namespace ON operator_namespace.oid = operator_class.opcnamespace
    JOIN pg_am access_method ON access_method.oid = operator_class.opcmethod
    LEFT JOIN pg_collation collation_record ON collation_record.oid = range_record.rngcollation
    LEFT JOIN pg_namespace collation_namespace ON collation_namespace.oid = collation_record.collnamespace
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
        'operator-family', operator_family.opfname, access_method.amname
    )::text
    FROM pg_opfamily operator_family
    JOIN pg_namespace namespace ON namespace.oid = operator_family.opfnamespace
    JOIN pg_am access_method ON access_method.oid = operator_family.opfmethod
    WHERE namespace.nspname = (SELECT name FROM target_schema)

    UNION ALL

    SELECT jsonb_build_array(
        'operator-family-operator', operator_family.opfname,
        access_method.amname, family_operator.amopstrategy,
        family_operator.amoppurpose::text,
        format_type(family_operator.amoplefttype, NULL),
        format_type(family_operator.amoprighttype, NULL),
        family_operator.amopopr::regoperator::text,
        COALESCE(sort_namespace.nspname, ''), COALESCE(sort_family.opfname, '')
    )::text
    FROM pg_amop family_operator
    JOIN pg_opfamily operator_family ON operator_family.oid = family_operator.amopfamily
    JOIN pg_namespace namespace ON namespace.oid = operator_family.opfnamespace
    JOIN pg_am access_method ON access_method.oid = operator_family.opfmethod
    LEFT JOIN pg_opfamily sort_family ON sort_family.oid = family_operator.amopsortfamily
    LEFT JOIN pg_namespace sort_namespace ON sort_namespace.oid = sort_family.opfnamespace
    WHERE namespace.nspname = (SELECT name FROM target_schema)

    UNION ALL

    SELECT jsonb_build_array(
        'operator-family-function', operator_family.opfname,
        access_method.amname, family_function.amprocnum,
        format_type(family_function.amproclefttype, NULL),
        format_type(family_function.amprocrighttype, NULL),
        family_function.amproc::regprocedure::text
    )::text
    FROM pg_amproc family_function
    JOIN pg_opfamily operator_family ON operator_family.oid = family_function.amprocfamily
    JOIN pg_namespace namespace ON namespace.oid = operator_family.opfnamespace
    JOIN pg_am access_method ON access_method.oid = operator_family.opfmethod
    WHERE namespace.nspname = (SELECT name FROM target_schema)

    UNION ALL

    SELECT jsonb_build_array(
        'conversion', conversion_record.conname,
        pg_encoding_to_char(conversion_record.conforencoding),
        pg_encoding_to_char(conversion_record.contoencoding),
        conversion_record.conproc::regprocedure::text,
        conversion_record.condefault
    )::text
    FROM pg_conversion conversion_record
    JOIN pg_namespace namespace ON namespace.oid = conversion_record.connamespace
    WHERE namespace.nspname = (SELECT name FROM target_schema)

    UNION ALL

    SELECT jsonb_build_array(
        'cast', format_type(cast_record.castsource, NULL),
        format_type(cast_record.casttarget, NULL),
        CASE WHEN cast_record.castfunc = 0 THEN '' ELSE cast_record.castfunc::regprocedure::text END,
        cast_record.castcontext::text, cast_record.castmethod::text
    )::text
    FROM pg_cast cast_record
    JOIN pg_type source_type ON source_type.oid = cast_record.castsource
    JOIN pg_namespace source_namespace ON source_namespace.oid = source_type.typnamespace
    JOIN pg_type target_type ON target_type.oid = cast_record.casttarget
    JOIN pg_namespace target_namespace ON target_namespace.oid = target_type.typnamespace
    WHERE source_namespace.nspname = (SELECT name FROM target_schema)
       OR target_namespace.nspname = (SELECT name FROM target_schema)

    UNION ALL

    SELECT jsonb_build_array(
        'transform', format_type(transform_record.trftype, NULL),
        language.lanname,
        CASE WHEN transform_record.trffromsql = 0 THEN '' ELSE transform_record.trffromsql::regprocedure::text END,
        CASE WHEN transform_record.trftosql = 0 THEN '' ELSE transform_record.trftosql::regprocedure::text END
    )::text
    FROM pg_transform transform_record
    JOIN pg_type type_record ON type_record.oid = transform_record.trftype
    JOIN pg_namespace namespace ON namespace.oid = type_record.typnamespace
    JOIN pg_language language ON language.oid = transform_record.trflang
    WHERE namespace.nspname = (SELECT name FROM target_schema)

    UNION ALL

    SELECT jsonb_build_array(
        'text-search-parser', parser.prsname,
        parser.prsstart::regprocedure::text,
        parser.prstoken::regprocedure::text,
        parser.prsend::regprocedure::text,
        CASE WHEN parser.prsheadline = 0 THEN '' ELSE parser.prsheadline::regprocedure::text END,
        parser.prslextype::regprocedure::text
    )::text
    FROM pg_ts_parser parser
    JOIN pg_namespace namespace ON namespace.oid = parser.prsnamespace
    WHERE namespace.nspname = (SELECT name FROM target_schema)

    UNION ALL

    SELECT jsonb_build_array(
        'text-search-template', template.tmplname,
        CASE WHEN template.tmplinit = 0 THEN '' ELSE template.tmplinit::regprocedure::text END,
        template.tmpllexize::regprocedure::text
    )::text
    FROM pg_ts_template template
    JOIN pg_namespace namespace ON namespace.oid = template.tmplnamespace
    WHERE namespace.nspname = (SELECT name FROM target_schema)

    UNION ALL

    SELECT jsonb_build_array(
        'text-search-dictionary', dictionary.dictname,
        template_namespace.nspname, template.tmplname,
        COALESCE(dictionary.dictinitoption, '')
    )::text
    FROM pg_ts_dict dictionary
    JOIN pg_namespace namespace ON namespace.oid = dictionary.dictnamespace
    JOIN pg_ts_template template ON template.oid = dictionary.dicttemplate
    JOIN pg_namespace template_namespace ON template_namespace.oid = template.tmplnamespace
    WHERE namespace.nspname = (SELECT name FROM target_schema)

    UNION ALL

    SELECT jsonb_build_array(
        'text-search-configuration', configuration.cfgname,
        parser_namespace.nspname, parser.prsname
    )::text
    FROM pg_ts_config configuration
    JOIN pg_namespace namespace ON namespace.oid = configuration.cfgnamespace
    JOIN pg_ts_parser parser ON parser.oid = configuration.cfgparser
    JOIN pg_namespace parser_namespace ON parser_namespace.oid = parser.prsnamespace
    WHERE namespace.nspname = (SELECT name FROM target_schema)

    UNION ALL

    SELECT jsonb_build_array(
        'text-search-configuration-map', configuration.cfgname,
        configuration_map.maptokentype, configuration_map.mapseqno,
        dictionary_namespace.nspname, dictionary.dictname
    )::text
    FROM pg_ts_config_map configuration_map
    JOIN pg_ts_config configuration ON configuration.oid = configuration_map.mapcfg
    JOIN pg_namespace namespace ON namespace.oid = configuration.cfgnamespace
    JOIN pg_ts_dict dictionary ON dictionary.oid = configuration_map.mapdict
    JOIN pg_namespace dictionary_namespace ON dictionary_namespace.oid = dictionary.dictnamespace
    WHERE namespace.nspname = (SELECT name FROM target_schema)

    UNION ALL

    SELECT jsonb_build_array(
        'collation', collation_record.collname,
        collation_record.collprovider::text,
        collation_record.collisdeterministic,
        collation_record.collencoding,
        collation_record.collcollate,
        collation_record.collctype,
        COALESCE(collation_record.colllocale, ''),
        COALESCE(collation_record.collicurules, ''),
        COALESCE(collation_record.collversion, '')
    )::text
    FROM pg_collation collation_record
    JOIN pg_namespace namespace ON namespace.oid = collation_record.collnamespace
    WHERE namespace.nspname = (SELECT name FROM target_schema)

    UNION ALL

    SELECT jsonb_build_array(
        'extended-statistics', statistics_record.stxname,
        relation.relname,
        statistics_record.stxstattarget,
        statistics_record.stxkind::text,
        COALESCE(pg_get_expr(statistics_record.stxexprs, statistics_record.stxrelid), ''),
        pg_get_statisticsobjdef(statistics_record.oid)
    )::text
    FROM pg_statistic_ext statistics_record
    JOIN pg_namespace namespace ON namespace.oid = statistics_record.stxnamespace
    JOIN pg_class relation ON relation.oid = statistics_record.stxrelid
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
