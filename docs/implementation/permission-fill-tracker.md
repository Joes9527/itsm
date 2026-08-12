# 第2+3项：补齐权限定义 + ResourceActionMap 实施追踪

> 分支: `fix/fill-permission-definitions`
> 日期: 2026-08-12
> 前置: `fix/unify-role-system` (角色体系已统一)

## 命名统一

| 统一后 | Router | ResourceActionMap | Seeder |
|--------|--------|-------------------|--------|
| `audit_log` | `audit_log` ✅ | `audit_logs`→`audit_log` | `audit`→`audit_log` |
| `system_config` | `config`→`system_config` | `system_config` ✅ | 新增 |

## 新增权限定义 (seeder)

- [ ] approval_workflow: create, read, update, delete
- [ ] assignment_rule: create, read, update, delete
- [ ] audit_log: read
- [ ] automation_rule: create, read, update, delete
- [ ] cloud_account: read, write, delete
- [ ] cloud_resource: read, write, delete
- [ ] cloud_service: read, write, delete
- [ ] system_config: read, update
- [ ] investigation: create, read, update
- [ ] menu: create, read, update, delete
- [ ] permission: create, read
- [ ] process_instance: create, read, update
- [ ] root_cause: create
- [ ] solution: create
- [ ] step: create, update
- [ ] tag: read
- [ ] task: read, update
- [ ] tenant: create, read, update, delete
- [ ] view: create, read, update, delete
- [ ] widget: create, update, delete

## 角色-权限分配

- [ ] sysadmin: 全部
- [ ] it_director: 管理类 + 审批/流程类
- [ ] service_catalog_admin: 工单相关 + 流程查看
- [ ] end_user: tag:read
- [ ] allPermissionCodes() 补齐

## Router 命名修正

- [ ] `config:read` → `system_config:read`
- [ ] `config:update` → `system_config:update`

## ResourceActionMap 补齐

- [ ] audit_logs → audit_log 统一
- [ ] 新增 /api/v1/approval-workflows*
- [ ] 新增 /api/v1/approval-chains*
- [ ] 新增 /api/v1/my-approvals*
- [ ] 新增 /api/v1/notification-preferences*
- [ ] 新增 /api/v1/system-configs*
- [ ] 新增 /api/v1/menus*
- [ ] 新增 /api/v1/tenants*
- [ ] 新增 /api/v1/permissions*
- [ ] 新增 /api/v1/bpmn/tasks*
- [ ] 新增 /api/v1/bpmn/instances*
- [ ] 新增 cloud, investigation, cmdb 等路径

## 迁移 SQL

- [ ] 为 tenant 1 创建新权限定义
- [ ] 分配权限给角色
