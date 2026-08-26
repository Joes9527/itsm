-- Migration: 20260826_bpmn_permission_model_e2e_fix
-- Description: 收紧 task:read/process_instance:read/task:update 的分发范围，
--              把"我的待办"菜单的可见性门槛从 task:read 改为 bpmn:read。
--
-- 背景：task:read/process_instance:read 此前同时承担三个职责——路由准入
-- （/my-approvals、/workflow/tasks、/workflow/instances 用 RequirePermission
-- 把关）、提权信号（hasElevatedBPMNAccess 用它判断"是否能看到全租户任务/实例"）、
-- 菜单可见性（"我的待办"菜单项）。默认种子数据里这两个权限码几乎发给了所有能
-- 碰到 BPMN 接口的角色，导致提权判断对绝大多数真实用户永远为真，参与者范围限定
-- （2026-08-25-bpmn-task-instance-authorization 分支的工作）在默认配置下基本
-- 不生效。本迁移配合同一 PR 的代码改动（路由准入改为仅需登录，菜单可见性改用
-- bpmn:read），把这两个权限码收窄为纯"提权"信号。
--
-- 列名核实：role_permissions(role_id, permission_id, tenant_id)、
-- roles(id, code)、permissions(id, code)、menus(path, permission_code, tenant_id)
-- 已对照 migrations/20260812_fill_missing_permissions.sql 与
-- migrations/20260628_add_connector_menu.sql 的真实写法核实一致，与本迁移最初
-- 假设的列名相符，未作调整。
--
-- 说明：以下 DELETE 没有显式过滤 tenant_id，这是安全的，但原因不是"role_permissions
-- 的 role_id/permission_id 只会是同租户配对"——这不是数据库层面保证的不变量。
-- service/role_service.go 的 RoleService.AssignPermissions（POST /roles/:id/permissions，
-- 仅受 role:update 权限把关）校验传入的 permissionIDs 时用的是
-- `s.client.Permission.Query().Where(permission.IDIn(permissionIDs...))`——没有按
-- tenant_id 过滤，随后把这些权限 ID 和角色的 tenant_id 一起写进 role_permissions，
-- 理论上可以经这条生产端点创建出 role_id/permission_id 分属不同租户的行。
-- 这条迁移仍然安全，是因为它只做 DELETE（撤销授权）：即使某个角色因为上面那个缺口
-- 意外持有了别的租户的 task:read/process_instance:read 授权，把它删掉的最坏结果也
-- 只是多撤销了一条本就异常的授权，不会跨租户新增任何访问权限——DELETE 不产生新的
-- 越权可能性，所以不需要额外的 tenant_id 过滤。这个理由是这条迁移专属的，不能照搬
-- 到 INSERT/UPDATE 上：换成写入操作时，缺少 tenant_id 过滤就会是真正的跨租户泄漏。
-- （RoleService.AssignPermissions 缺失的 tenant 过滤本身是一个独立、范围较窄
-- 的权限模型缺口——仅管理员可达，不在本次任务范围内，记入 backlog 待后续修复。）
--
-- 改动前状态（供未来撤销参考，撤销方式是照此写一条新的正向迁移，参见
-- migrations/20260814_revert_end_user_overgrant.sql 的先例——这个仓库的
-- migrations/*.sql 是单向 forward-only 迁移，不接入 migration/migrator.go
-- 的 RollbackSQL/RollbackMigration 机制，那套机制服务于另一批 Go 结构体
-- 定义的迁移）：
--   - dept_manager 的 role_permissions 里有 task:read（附带 bpmn:read/bpmn:write，本迁移不动后两个）
--   - end_user 的 role_permissions 里有 task:read（附带 bpmn:read/bpmn:write，本迁移不动后两个）
--   - service_catalog_admin 的 role_permissions 里有 task:read、process_instance:read
--   - menus 表"我的待办"行（path = '/approvals/pending'）的 permission_code 是 'task:read'
--     （某些更早的租户可能压根没有这一行——该菜单项本身是 2026-08-20 才补种进 seeder 的，
--     此前从未被加入过菜单种子；这类租户下面的 UPDATE 不会命中任何行，属预期行为，
--     seedMenus() 在下次应用重启时会以 bpmn:read 自愈补种这一行）。
--
-- 注：下面的 DELETE 只处理 task:read/process_instance:read，没有单独一条撤销
-- task:update 的语句——这不是遗漏。dept_manager/end_user/service_catalog_admin
-- 这三个角色在整改前的种子数据里本来就从未被授予过 task:update（seeder.go 里
-- 这三个角色的权限清单里一直没有这个码），所以没有对应的 role_permissions 行
-- 需要撤销。

-- Step 1: 收紧 dept_manager
DELETE FROM role_permissions
WHERE role_id IN (SELECT id FROM roles WHERE code = 'dept_manager')
  AND permission_id IN (SELECT id FROM permissions WHERE code = 'task:read');

-- Step 2: 收紧 end_user
DELETE FROM role_permissions
WHERE role_id IN (SELECT id FROM roles WHERE code = 'end_user')
  AND permission_id IN (SELECT id FROM permissions WHERE code = 'task:read');

-- Step 3: 收紧 service_catalog_admin
DELETE FROM role_permissions
WHERE role_id IN (SELECT id FROM roles WHERE code = 'service_catalog_admin')
  AND permission_id IN (
    SELECT id FROM permissions WHERE code IN ('task:read', 'process_instance:read')
  );

-- Step 4: "我的待办"菜单可见性门槛从 task:read 改为 bpmn:read
UPDATE menus SET permission_code = 'bpmn:read'
WHERE path = '/approvals/pending' AND permission_code = 'task:read';
