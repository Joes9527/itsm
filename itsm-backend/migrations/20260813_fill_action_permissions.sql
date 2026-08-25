-- Migration: 20260813_fill_action_permissions
-- Description: 为一线角色补充分派关键动作权限 (ticket:escalate, change:rollback, release:approve/rollback)
-- 否则非 super_admin 用户点击升级/回滚/审批按钮全部 403

INSERT INTO role_permissions (role_id, permission_id, tenant_id)
SELECT r.id, p.id, r.tenant_id
FROM roles r
CROSS JOIN permissions p
WHERE r.code IN ('ops_manager', 'sd_manager', 'l1_support', 'dept_manager')
  AND p.code IN ('ticket:escalate', 'change:rollback', 'release:approve', 'release:rollback')
  AND p.tenant_id = 1
  AND NOT EXISTS (
    SELECT 1 FROM role_permissions rp
    WHERE rp.role_id = r.id AND rp.permission_id = p.id
  );
