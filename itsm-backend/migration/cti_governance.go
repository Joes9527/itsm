package migration

// CTIGovernanceVersion 是 CTI 治理的结构迁移：分类代码唯一范围、目录默认分类结构引用
// 与启用记录保留键的局部唯一约束。
//
// 本迁移只做结构准备，不迁移旧工单、不回填目录默认分类、不改写历史分类、不启用任何
// 门禁。发现的既有异常以预检失败暴露（可读原因），绝不自动删除、截断或迁移父链。
const CTIGovernanceVersion = "048_cti_governance"

const ctiGovernanceSQL = `
DO $cti_preflight$
DECLARE
    offenders bigint;
BEGIN
    IF to_regclass('ticket_categories') IS NULL THEN
        RAISE EXCEPTION 'CTI preflight: ticket_categories is missing; run the Ent schema preparation first';
    END IF;
    IF to_regclass('service_catalogs') IS NULL THEN
        RAISE EXCEPTION 'CTI preflight: service_catalogs is missing; run the Ent schema preparation first';
    END IF;
    IF to_regclass('system_configs') IS NULL THEN
        RAISE EXCEPTION 'CTI preflight: system_configs is missing; run the Ent schema preparation first';
    END IF;

    -- 1) 放宽 code 唯一范围前，租户内重复编码必须为空。
    SELECT count(*) INTO offenders FROM (
        SELECT tenant_id, code FROM ticket_categories GROUP BY tenant_id, code HAVING count(*) > 1
    ) duplicated_codes;
    IF offenders > 0 THEN
        RAISE EXCEPTION 'CTI preflight: % duplicated (tenant_id, code) group(s) in ticket_categories; resolve the classification master data before applying %',
            offenders, '048_cti_governance';
    END IF;

    -- 2) 层级必须落在 1..3。
    SELECT count(*) INTO offenders FROM ticket_categories WHERE level < 1 OR level > 3;
    IF offenders > 0 THEN
        RAISE EXCEPTION 'CTI preflight: % ticket_categories row(s) outside levels 1..3; classification master data must be corrected by the owner (no automatic truncation)', offenders;
    END IF;

    -- 3) 父链必须存在、同租户，且 level 与父级严格连续。
    SELECT count(*) INTO offenders
    FROM ticket_categories child
    LEFT JOIN ticket_categories parent ON parent.id = child.parent_id
    WHERE child.parent_id IS NOT NULL AND child.parent_id <> 0
      AND (parent.id IS NULL OR parent.tenant_id <> child.tenant_id);
    IF offenders > 0 THEN
        RAISE EXCEPTION 'CTI preflight: % ticket_categories row(s) have a missing or cross-tenant parent; resolve before applying', offenders;
    END IF;

    SELECT count(*) INTO offenders
    FROM ticket_categories child
    JOIN ticket_categories parent ON parent.id = child.parent_id
    WHERE child.parent_id IS NOT NULL AND child.parent_id <> 0
      AND child.level <> parent.level + 1;
    IF offenders > 0 THEN
        RAISE EXCEPTION 'CTI preflight: % ticket_categories row(s) have a level inconsistent with their parent; resolve before applying', offenders;
    END IF;

    -- 4) 根节点的 level 必须是 1。
    SELECT count(*) INTO offenders FROM ticket_categories
    WHERE (parent_id IS NULL OR parent_id = 0) AND level <> 1;
    IF offenders > 0 THEN
        RAISE EXCEPTION 'CTI preflight: % root ticket_categories row(s) are not level 1; resolve before applying', offenders;
    END IF;

    -- 5) 禁止环：从每个节点向上追踪父链，超过三级深度仍能继续即视为环或超深链。
    WITH RECURSIVE walk AS (
        SELECT id AS origin, parent_id, level AS depth, ARRAY[id] AS visited
        FROM ticket_categories
        WHERE parent_id IS NOT NULL AND parent_id <> 0
        UNION ALL
        SELECT walk.origin, parent.parent_id, walk.depth + 1, walk.visited || parent.id
        FROM walk
        JOIN ticket_categories parent ON parent.id = walk.parent_id
        WHERE parent.parent_id IS NOT NULL AND parent.parent_id <> 0
          AND NOT parent.id = ANY(walk.visited)
          AND walk.depth < 12
    )
    SELECT count(DISTINCT origin) INTO offenders FROM walk WHERE depth >= 3;
    IF offenders > 0 THEN
        RAISE EXCEPTION 'CTI preflight: % ticket_categories row(s) sit deeper than three levels or form a cycle; resolve before applying', offenders;
    END IF;

    WITH RECURSIVE walk AS (
        SELECT id AS origin, parent_id, ARRAY[id] AS visited
        FROM ticket_categories
        WHERE parent_id IS NOT NULL AND parent_id <> 0
        UNION ALL
        SELECT walk.origin, parent.parent_id, walk.visited || parent.id
        FROM walk
        JOIN ticket_categories parent ON parent.id = walk.parent_id
        WHERE NOT parent.id = ANY(walk.visited)
          AND array_length(walk.visited, 1) < 12
    )
    SELECT count(DISTINCT origin) INTO offenders FROM walk WHERE parent_id = ANY(visited);
    IF offenders > 0 THEN
        RAISE EXCEPTION 'CTI preflight: % ticket_categories row(s) form a parent cycle; resolve before applying', offenders;
    END IF;

    -- 6) 保留键不得有重复的非删除行，否则局部唯一约束无法建立。
    IF to_regclass('system_configs') IS NOT NULL THEN
        SELECT count(*) INTO offenders FROM (
            SELECT tenant_id FROM system_configs
            WHERE key = 'cti_governance_v1' AND deleted_at IS NULL
            GROUP BY tenant_id HAVING count(*) > 1
        ) duplicated_governance_keys;
        IF offenders > 0 THEN
            RAISE EXCEPTION 'CTI preflight: % tenant(s) have duplicated cti_governance_v1 system_configs rows; resolve before applying', offenders;
        END IF;
    END IF;
END $cti_preflight$;

-- 分类代码唯一范围：全表 → 租户内。这是放宽，不会让既有合法数据失效。
DROP INDEX IF EXISTS ticketcategory_code;
CREATE UNIQUE INDEX IF NOT EXISTS ticketcategory_tenant_id_code ON ticket_categories (tenant_id, code);

-- 目录默认分类：可空结构引用，供发布/申请以目录为初始权威。
ALTER TABLE service_catalogs
    ADD COLUMN IF NOT EXISTS default_ticket_category_id integer;

DO $cti_fk$
BEGIN
    IF NOT EXISTS (
        SELECT 1 FROM pg_constraint
        WHERE conname = 'service_catalogs_ticket_categories_default_catalogs'
          AND conrelid = 'service_catalogs'::regclass
    ) THEN
        ALTER TABLE service_catalogs
            ADD CONSTRAINT service_catalogs_ticket_categories_default_catalogs
            FOREIGN KEY (default_ticket_category_id) REFERENCES ticket_categories (id)
            ON DELETE SET NULL;
    END IF;
END $cti_fk$;

CREATE INDEX IF NOT EXISTS servicecatalog_tenant_id_default_ticket_category_id
    ON service_catalogs (tenant_id, default_ticket_category_id);

-- 启用记录的保留键：每租户至多一条未删除行。通用配置写入路径必须在 Go 层拒绝该键，
-- 该索引是最后的并发防线（先查后写在并发下不可靠）。
CREATE UNIQUE INDEX IF NOT EXISTS system_configs_cti_governance_reserved_key_uq
    ON system_configs (tenant_id, key)
    WHERE key = 'cti_governance_v1' AND deleted_at IS NULL;
` + ctiGovernanceVerifySQL

const ctiGovernanceVerifySQL = `
DO $cti_verify$
DECLARE
    offenders bigint;
BEGIN
    IF NOT EXISTS (
        SELECT 1 FROM information_schema.columns
        WHERE table_name = 'service_catalogs' AND column_name = 'default_ticket_category_id'
    ) THEN
        RAISE EXCEPTION 'CTI verify: service_catalogs.default_ticket_category_id is missing in schema %', current_schema();
    END IF;
    IF NOT EXISTS (
        SELECT 1 FROM pg_constraint
        WHERE conname = 'service_catalogs_ticket_categories_default_catalogs'
          AND conrelid = 'service_catalogs'::regclass
    ) THEN
        RAISE EXCEPTION 'CTI verify: default CTI foreign key is missing in schema %', current_schema();
    END IF;
    IF NOT EXISTS (
        SELECT 1 FROM pg_indexes
        WHERE schemaname = current_schema() AND indexname = 'ticketcategory_tenant_id_code'
    ) THEN
        RAISE EXCEPTION 'CTI verify: tenant-scoped ticket category code index is missing in schema %', current_schema();
    END IF;
    IF EXISTS (
        SELECT 1 FROM pg_indexes
        WHERE schemaname = current_schema() AND indexname = 'ticketcategory_code'
    ) THEN
        RAISE EXCEPTION 'CTI verify: the table-global ticket category code index still exists in schema %', current_schema();
    END IF;
    IF NOT EXISTS (
        SELECT 1 FROM pg_indexes
        WHERE schemaname = current_schema() AND indexname = 'system_configs_cti_governance_reserved_key_uq'
    ) THEN
        RAISE EXCEPTION 'CTI verify: the cti_governance_v1 reserved key index is missing in schema %', current_schema();
    END IF;
    SELECT count(*) INTO offenders FROM ticket_categories WHERE level < 1 OR level > 3;
    IF offenders > 0 THEN
        RAISE EXCEPTION 'CTI verify: % ticket_categories row(s) outside levels 1..3 after migration', offenders;
    END IF;
END $cti_verify$;
`

// ctiGovernanceDevelopmentResetSQL 仅用于可丢弃的开发/测试库。恢复全表唯一索引前必须
// 确认不存在跨租户重复编码，否则明确失败而不是静默丢失分类。
const ctiGovernanceDevelopmentResetSQL = `
DO $cti_reset$
DECLARE
    offenders bigint;
BEGIN
    IF to_regclass('ticket_categories') IS NOT NULL THEN
        SELECT count(*) INTO offenders FROM (
            SELECT code FROM ticket_categories GROUP BY code HAVING count(*) > 1
        ) duplicated_codes;
        IF offenders > 0 THEN
            RAISE EXCEPTION 'CTI reset: % code value(s) are shared across tenants; resolve before restoring table-global uniqueness', offenders;
        END IF;
        DROP INDEX IF EXISTS ticketcategory_tenant_id_code;
        CREATE UNIQUE INDEX IF NOT EXISTS ticketcategory_code ON ticket_categories (code);
    END IF;
    IF to_regclass('service_catalogs') IS NOT NULL THEN
        ALTER TABLE service_catalogs DROP CONSTRAINT IF EXISTS service_catalogs_ticket_categories_default_catalogs;
        DROP INDEX IF EXISTS servicecatalog_tenant_id_default_ticket_category_id;
        ALTER TABLE service_catalogs DROP COLUMN IF EXISTS default_ticket_category_id;
    END IF;
    DROP INDEX IF EXISTS system_configs_cti_governance_reserved_key_uq;
END $cti_reset$;
`
