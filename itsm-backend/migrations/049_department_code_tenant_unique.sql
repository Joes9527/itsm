-- 049: 部门编码在租户内唯一。
--
-- 组织编码是节点的稳定业务键（来自旧 ITIL / eHR 的对齐结果），一旦重复，
-- 组织树就会出现无法区分的同名节点，且导入重跑无法幂等。
-- 软删除的行不占用编码，因此用部分索引：deleted_at IS NULL。
CREATE UNIQUE INDEX IF NOT EXISTS idx_departments_tenant_code
    ON departments (tenant_id, code)
    WHERE deleted_at IS NULL;
