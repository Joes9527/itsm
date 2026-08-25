-- Migration: 20260814_revert_end_user_overgrant
-- Description: 撤销给 end_user 过度授权的权限，恢复到 seeder 权威定义（8 条）。
--
-- 背景：此前误判 end_user 缺 user:read 等权限为 seed 不完整，通过
-- 20260814_end_user_missing_permissions.sql 和
-- 20260814_missing_permission_definitions.sql 给 end_user 补了 18 个权限关联。
-- 实际上 seeder 的 rolePermissionMap 里 end_user 有意只授予 8 条权限
-- （不含 user:read），以避免 end_user 访问完整用户列表（含所有用户邮箱/角色）。
-- 本迁移撤销这些过度授权，恢复安全隔离；前端已改为按角色隐藏转派/抄送操作。

DELETE FROM role_permissions
WHERE role_id IN (SELECT id FROM roles WHERE code = 'end_user')
  AND permission_id IN (
    SELECT id FROM permissions WHERE code IN (
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
      'user:read',
      'notification:write',
      'dashboard:read',
      'bpmn:read'
    )
  );
