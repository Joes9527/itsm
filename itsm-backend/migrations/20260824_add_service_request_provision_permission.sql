-- Migration: 20260824_add_service_request_provision_permission
-- Description: 新增 service_request:provision 权限定义，并授予真正的履约角色；
-- 同时不改动 end_user 的既有 service_request:write/read（自助提单仍然保留）。
--
-- 根因：POST /service-requests/:id/provision 与 /provisioning-tasks/:id/execute
-- 原来复用 service_request:write 校验——这条权限是为了让 end_user 能自助提单
-- （POST /service-requests）才发的，结果顺带解锁了"对任意服务请求发起交付"。
-- 而真正该执行交付的运维/服务台角色反而一条 service_request 权限都没有。见
-- pkg/seeder/seeder.go 里 service_request:provision 的定义与角色矩阵调整。

-- 1. 补全 service_request:provision 权限定义（对所有现有租户，幂等）
INSERT INTO permissions (code, name, resource, action, tenant_id, created_at, updated_at)
SELECT 'service_request:provision', '执行服务请求交付', 'service_request', 'provision', t.tenant_id, now(), now()
FROM (SELECT DISTINCT tenant_id FROM roles) t
WHERE NOT EXISTS (
    SELECT 1 FROM permissions p
    WHERE p.code = 'service_request:provision' AND p.tenant_id = t.tenant_id
);

-- 2. 关联给履约角色（幂等）：一线/二线/三线支持、运维工程师、DBA、网络工程师、
-- 服务台主管、运维经理、服务目录管理员。end_user 不在这个名单里——保留提单权限，
-- 不发交付权限，职责分离由 service.CanProvision 的服务层校验兜底。
INSERT INTO role_permissions (role_id, permission_id, tenant_id)
SELECT r.id, p.id, r.tenant_id
FROM roles r
JOIN permissions p ON p.tenant_id = r.tenant_id AND p.code = 'service_request:provision'
WHERE r.code IN (
    'l1_support', 'l2_support', 'l3_expert',
    'ops_engineer', 'dba', 'network_eng',
    'sd_manager', 'ops_manager', 'service_catalog_admin'
)
AND NOT EXISTS (
    SELECT 1 FROM role_permissions rp
    WHERE rp.role_id = r.id AND rp.permission_id = p.id AND rp.tenant_id = r.tenant_id
);
