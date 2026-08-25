-- Migration: 20260814_end_user_missing_permissions
-- Description: 补全 end_user 角色缺失的权限（对齐硬编码 RolePermissions[end_user]）。
--
-- 根因：部分环境的权限 seed 不完整，end_user 角色在数据库中只有 8 条权限，
-- 缺少 user:read 等 15 个权限。由于 Fallback 模式下数据库权限集非空时不会
-- 回退到硬编码权限，导致工单详情"加载用户失败"（GET /api/v1/users 返回 403）
-- 等一系列权限不足问题。

INSERT INTO role_permissions (role_id, permission_id, tenant_id)
SELECT r.id, p.id, r.tenant_id
FROM roles r
CROSS JOIN permissions p
WHERE r.code = 'end_user'
  AND p.code IN (
    'ai:read',
    'ai:write',
    'asset:read',
    'change:read',
    'cmdb:read',
    'incident:read',
    'license:read',
    'org:read',
    'problem:read',
    'release:read',
    'service_request:read',
    'service_request:write',
    'sla:read',
    'system_config:read',
    'user:read'
  )
  AND NOT EXISTS (
    SELECT 1
    FROM role_permissions rp
    WHERE rp.role_id = r.id
      AND rp.permission_id = p.id
      AND rp.tenant_id = r.tenant_id
  );
