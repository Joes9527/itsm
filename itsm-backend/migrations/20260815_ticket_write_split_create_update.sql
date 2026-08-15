-- Migration: 20260815_ticket_write_split_create_update
-- Description: 给所有有 ticket:write 的角色补 ticket:create 和 ticket:update。
--
-- 根因：路由已把 ticket 权限细分为 create/update/assign/escalate/delete 等，
-- 但 seeder 给业务角色的是笼统的 ticket:write（description「创建、编辑工单」）。
-- 导致 end_user 等角色虽然语义上能「创建、编辑工单」，但 POST /tickets 要求
-- ticket:create、PUT /tickets/:id 要求 ticket:update，二者都返回 403。
--
-- 本迁移把 ticket:write 拆成 ticket:create + ticket:update 补授给相关角色。

INSERT INTO role_permissions (role_id, permission_id, tenant_id)
SELECT rp.role_id, pc.id, rp.tenant_id
FROM role_permissions rp
JOIN permissions pw ON pw.id = rp.permission_id AND pw.code = 'ticket:write'
JOIN permissions pc ON pc.code IN ('ticket:create', 'ticket:update')
WHERE NOT EXISTS (
    SELECT 1
    FROM role_permissions rp2
    WHERE rp2.role_id = rp.role_id
      AND rp2.permission_id = pc.id
      AND rp2.tenant_id = rp.tenant_id
);
