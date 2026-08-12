-- Migration: 20260812_fill_missing_permissions
-- Description: Create missing permission definitions for 20 resources and
--              assign them to appropriate roles. Also renames audit:read →
--              audit_log:read.
--
-- Context: After role system unification (fix/unify-role-system), the
--          permissions table was missing definitions for 20 resources
--          used in router RequirePermission calls, causing 403 for all
--          non-super_admin users on those routes.

-- Step 1: Rename audit:read → audit_log:read if it exists
UPDATE permissions SET code = 'audit_log:read', resource = 'audit_log'
WHERE code = 'audit:read' AND NOT EXISTS (
  SELECT 1 FROM permissions WHERE code = 'audit_log:read'
);

-- Step 2: Create new permissions (idempotent via NOT EXISTS)
INSERT INTO permissions (code, name, resource, action, description, tenant_id, created_at, updated_at)
SELECT v.code, v.name, v.resource, v.action, v.description, 1, NOW(), NOW()
FROM (VALUES
  -- 审批工作流
  ('approval_workflow:read',   '查看审批工作流', 'approval_workflow', 'read',   '查看审批工作流'),
  ('approval_workflow:create', '创建审批工作流', 'approval_workflow', 'create', '创建审批工作流'),
  ('approval_workflow:update', '更新审批工作流', 'approval_workflow', 'update', '更新审批工作流'),
  ('approval_workflow:delete', '删除审批工作流', 'approval_workflow', 'delete', '删除审批工作流'),
  -- 分派规则
  ('assignment_rule:read',   '查看分派规则', 'assignment_rule', 'read',   '查看分派规则'),
  ('assignment_rule:create', '创建分派规则', 'assignment_rule', 'create', '创建分派规则'),
  ('assignment_rule:update', '更新分派规则', 'assignment_rule', 'update', '更新分派规则'),
  ('assignment_rule:delete', '删除分派规则', 'assignment_rule', 'delete', '删除分派规则'),
  -- 自动化规则
  ('automation_rule:read',   '查看自动化规则', 'automation_rule', 'read',   '查看自动化规则'),
  ('automation_rule:create', '创建自动化规则', 'automation_rule', 'create', '创建自动化规则'),
  ('automation_rule:update', '更新自动化规则', 'automation_rule', 'update', '更新自动化规则'),
  ('automation_rule:delete', '删除自动化规则', 'automation_rule', 'delete', '删除自动化规则'),
  -- 云管理
  ('cloud_account:read',   '查看云账号', 'cloud_account', 'read',   '查看云账号'),
  ('cloud_account:write',  '管理云账号', 'cloud_account', 'write',  '管理云账号'),
  ('cloud_account:delete', '删除云账号', 'cloud_account', 'delete', '删除云账号'),
  ('cloud_resource:read',   '查看云资源', 'cloud_resource', 'read',   '查看云资源'),
  ('cloud_resource:write',  '管理云资源', 'cloud_resource', 'write',  '管理云资源'),
  ('cloud_resource:delete', '删除云资源', 'cloud_resource', 'delete', '删除云资源'),
  ('cloud_service:read',   '查看云服务', 'cloud_service', 'read',   '查看云服务'),
  ('cloud_service:write',  '管理云服务', 'cloud_service', 'write',  '管理云服务'),
  ('cloud_service:delete', '删除云服务', 'cloud_service', 'delete', '删除云服务'),
  -- 系统配置
  ('system_config:read',   '查看系统配置', 'system_config', 'read',   '查看系统配置'),
  ('system_config:update', '更新系统配置', 'system_config', 'update', '更新系统配置'),
  -- 问题调查
  ('investigation:read',   '查看调查', 'investigation', 'read',   '查看问题调查'),
  ('investigation:create', '创建调查', 'investigation', 'create', '创建问题调查'),
  ('investigation:update', '更新调查', 'investigation', 'update', '更新问题调查'),
  -- 菜单
  ('menu:read',   '查看菜单', 'menu', 'read',   '查看菜单'),
  ('menu:create', '创建菜单', 'menu', 'create', '创建菜单'),
  ('menu:update', '更新菜单', 'menu', 'update', '更新菜单'),
  ('menu:delete', '删除菜单', 'menu', 'delete', '删除菜单'),
  -- 权限管理
  ('permission:read',   '查看权限', 'permission', 'read',   '查看权限定义'),
  ('permission:create', '创建权限', 'permission', 'create', '创建权限'),
  -- 流程实例
  ('process_instance:read',   '查看流程实例', 'process_instance', 'read',   '查看流程实例'),
  ('process_instance:create', '启动流程',     'process_instance', 'create', '启动流程实例'),
  ('process_instance:update', '管理流程',     'process_instance', 'update', '暂停/恢复/取消流程'),
  -- 根因/方案/步骤
  ('root_cause:create', '设置根因', 'root_cause', 'create', '设置问题根因'),
  ('solution:create',   '创建方案', 'solution',   'create', '创建解决方案'),
  ('step:create', '创建步骤', 'step', 'create', '创建调查步骤'),
  ('step:update', '更新步骤', 'step', 'update', '更新调查步骤'),
  -- 标签
  ('tag:read', '查看标签', 'tag', 'read', '查看工单标签'),
  -- 任务
  ('task:read',   '查看任务', 'task', 'read',   '查看审批/流程任务'),
  ('task:update', '处理任务', 'task', 'update', '审批/处理流程任务'),
  -- 租户
  ('tenant:read',   '查看租户', 'tenant', 'read',   '查看租户信息'),
  ('tenant:create', '创建租户', 'tenant', 'create', '创建新租户'),
  ('tenant:update', '更新租户', 'tenant', 'update', '更新租户配置'),
  ('tenant:delete', '删除租户', 'tenant', 'delete', '删除租户'),
  -- 视图
  ('view:read',   '查看视图', 'view', 'read',   '查看自定义视图'),
  ('view:create', '创建视图', 'view', 'create', '创建自定义视图'),
  ('view:update', '更新视图', 'view', 'update', '更新自定义视图'),
  ('view:delete', '删除视图', 'view', 'delete', '删除自定义视图'),
  -- 部件
  ('widget:create', '创建部件', 'widget', 'create', '创建仪表盘部件'),
  ('widget:update', '更新部件', 'widget', 'update', '更新仪表盘部件'),
  ('widget:delete', '删除部件', 'widget', 'delete', '删除仪表盘部件')
) AS v(code, name, resource, action, description)
WHERE NOT EXISTS (
  SELECT 1 FROM permissions WHERE code = v.code AND tenant_id = 1
);

-- Step 3: Assign new permissions to sysadmin (all) and end_user (tag:read)
INSERT INTO role_permissions (role_id, permission_id, tenant_id)
SELECT r.id, p.id, r.tenant_id
FROM roles r
CROSS JOIN permissions p
WHERE r.code = 'sysadmin'
  AND p.tenant_id = 1
  AND p.code IN (
    'approval_workflow:read','approval_workflow:create','approval_workflow:update','approval_workflow:delete',
    'assignment_rule:read','assignment_rule:create','assignment_rule:update','assignment_rule:delete',
    'automation_rule:read','automation_rule:create','automation_rule:update','automation_rule:delete',
    'cloud_account:read','cloud_account:write','cloud_account:delete',
    'cloud_resource:read','cloud_resource:write','cloud_resource:delete',
    'cloud_service:read','cloud_service:write','cloud_service:delete',
    'system_config:read','system_config:update',
    'investigation:read','investigation:create','investigation:update',
    'menu:read','menu:create','menu:update','menu:delete',
    'permission:read','permission:create',
    'process_instance:read','process_instance:create','process_instance:update',
    'root_cause:create','solution:create','step:create','step:update',
    'tag:read','task:read','task:update',
    'tenant:read','tenant:create','tenant:update','tenant:delete',
    'view:read','view:create','view:update','view:delete',
    'widget:create','widget:update','widget:delete'
  )
  AND NOT EXISTS (
    SELECT 1 FROM role_permissions rp
    WHERE rp.role_id = r.id AND rp.permission_id = p.id
  );

-- Step 4: Assign tag:read to end_user
INSERT INTO role_permissions (role_id, permission_id, tenant_id)
SELECT r.id, p.id, r.tenant_id
FROM roles r
CROSS JOIN permissions p
WHERE r.code = 'end_user'
  AND p.code = 'tag:read'
  AND p.tenant_id = 1
  AND NOT EXISTS (
    SELECT 1 FROM role_permissions rp
    WHERE rp.role_id = r.id AND rp.permission_id = p.id
  );
