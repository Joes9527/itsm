-- 050: 组织节点自带类型。
--
-- "公司/分公司/部门/组"写在节点上，而不是靠"处在第几层"推断：实测旧 ITIL 最深
-- 14 层、eHR 最深 11 层，且"公司下面直接是部门"是常态（旧 ITIL 94 个公司节点里
-- 58 个、eHR 79 个里 67 个如此），层级无法确定类型。
--
-- 空串表示"尚未分类"：既有节点需要业务分流表确认后才能赋值，所以列先允许空。
ALTER TABLE departments
    ADD COLUMN IF NOT EXISTS node_type varchar NOT NULL DEFAULT '';

DO $$
BEGIN
    IF NOT EXISTS (
        SELECT 1 FROM pg_constraint
        WHERE conname = 'departments_node_type_value_check'
          AND conrelid = 'departments'::regclass
    ) THEN
        ALTER TABLE departments
            ADD CONSTRAINT departments_node_type_value_check
            CHECK (node_type IN ('', 'company', 'branch', 'department', 'team'));
    END IF;
END $$;
