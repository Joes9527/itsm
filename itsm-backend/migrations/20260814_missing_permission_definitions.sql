-- Migration: 20260814_missing_permission_definitions
-- Description: 补全 permissions 表缺失的权限定义，并给 end_user 关联其需要的 3 个。
--
-- 根因：permissions 表 seed 不完整，硬编码 RolePermissions 中有 21 个权限定义
-- 在数据库里缺失。由于 Fallback 模式下数据库权限集非空时不回退硬编码，
-- 需要这些权限的角色将永远无法获得它们。
-- 其中 end_user 需要 notification:write / dashboard:read / bpmn:read。

-- 1. 补全缺失的权限定义（对所有现有租户）
INSERT INTO permissions (code, name, resource, action, tenant_id, created_at, updated_at)
SELECT v.code, v.name, v.resource, v.action, t.tenant_id, now(), now()
FROM (
    VALUES
      ('audit:read',           '查看审计日志',   'audit',           'read'),
      ('bpmn:read',            '查看流程',       'bpmn',            'read'),
      ('bpmn:write',           '编辑流程',       'bpmn',            'write'),
      ('bpmn:delete',          '删除流程',       'bpmn',            'delete'),
      ('change:list',          '查看变更列表',   'change',          'list'),
      ('dashboard:read',       '查看仪表盘',     'dashboard',       'read'),
      ('dashboard:admin',      '管理仪表盘',     'dashboard',       'admin'),
      ('incident:admin',       '管理事件',       'incident',        'admin'),
      ('incident:list',        '查看事件列表',   'incident',        'list'),
      ('knowledge:admin',      '管理知识库',     'knowledge',       'admin'),
      ('knowledge:list',       '查看知识列表',   'knowledge',       'list'),
      ('notification:list',    '查看通知列表',   'notification',    'list'),
      ('notification:write',   '发送通知',       'notification',    'write'),
      ('problem:list',         '查看问题列表',   'problem',         'list'),
      ('project:delete',       '删除项目',       'project',         'delete'),
      ('role:delete',          '删除角色',       'role',            'delete'),
      ('system_config:write',  '修改系统配置',   'system_config',   'write'),
      ('ticket:list',          '查看工单列表',   'ticket',          'list'),
      ('ticket_category:write','管理工单分类',   'ticket_category', 'write'),
      ('ticket_tag:write',     '管理工单标签',   'ticket_tag',      'write'),
      ('ticket_template:write','管理工单模板',   'ticket_template', 'write')
) AS v(code, name, resource, action)
CROSS JOIN (SELECT DISTINCT tenant_id FROM roles) t
WHERE NOT EXISTS (
    SELECT 1
    FROM permissions p
    WHERE p.code = v.code AND p.tenant_id = t.tenant_id
);

-- 2. 给 end_user 角色关联其需要的 3 个权限（notification:write / dashboard:read / bpmn:read）
INSERT INTO role_permissions (role_id, permission_id, tenant_id)
SELECT r.id, p.id, r.tenant_id
FROM roles r
JOIN permissions p ON p.tenant_id = r.tenant_id
WHERE r.code = 'end_user'
  AND p.code IN ('notification:write', 'dashboard:read', 'bpmn:read')
  AND NOT EXISTS (
    SELECT 1
    FROM role_permissions rp
    WHERE rp.role_id = r.id
      AND rp.permission_id = p.id
      AND rp.tenant_id = r.tenant_id
  );
