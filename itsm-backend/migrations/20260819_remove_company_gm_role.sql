-- Migration: 20260819_remove_company_gm_role
-- Description: 清理误加的 company_gm（总经理）角色及其权限映射。
--
-- 背景：曾经短暂把"总经理"建模为一个 BPMN 按角色路由的角色 company_gm，
-- 随 config/seed/default.json 和 pkg/seeder/seeder.go 进入过 main 分支。
-- 总经理审批改为复用组织架构（部门树固定节点 + assigneeDeptId 固定部门审批人），
-- 不再需要这个角色。任何在 company_gm 存在期间跑过种子初始化的环境都会有这行
-- 孤儿角色数据，本迁移清理它。执行前建议先确认没有真实用户持有这个角色：
--   SELECT count(*) FROM users WHERE role = 'company_gm';
-- 如果结果不是 0，先把这些用户的角色改成合适的其他角色，再执行下面的删除。

UPDATE users SET role = 'end_user' WHERE role = 'company_gm';

DELETE FROM role_permissions
WHERE role_id IN (SELECT id FROM roles WHERE code = 'company_gm');

DELETE FROM roles WHERE code = 'company_gm';
