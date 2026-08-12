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

- [x] approval_workflow: create, read, update, delete
- [x] assignment_rule: create, read, update, delete
- [x] audit_log: read
- [x] automation_rule: create, read, update, delete
- [x] cloud_account: read, write, delete
- [x] cloud_resource: read, write, delete
- [x] cloud_service: read, write, delete
- [x] system_config: read, update
- [x] investigation: create, read, update
- [x] menu: create, read, update, delete
- [x] permission: create, read
- [x] process_instance: create, read, update
- [x] root_cause: create
- [x] solution: create
- [x] step: create, update
- [x] tag: read
- [x] task: read, update
- [x] tenant: create, read, update, delete
- [x] view: create, read, update, delete
- [x] widget: create, update, delete

## 角色-权限分配

- [x] sysadmin: 全部
- [x] it_director: 管理类 + 审批/流程类
- [x] service_catalog_admin: 工单相关 + 流程查看
- [x] end_user: tag:read
- [x] allPermissionCodes() 补齐

## Router 命名修正

- [x] `config:read` → `system_config:read`
- [x] `config:update` → `system_config:update`

## ResourceActionMap 补齐

- [x] audit_logs → audit_log 统一
- [x] 新增 /api/v1/approval-workflows*
- [x] 新增 /api/v1/approval-chains*
- [x] 新增 /api/v1/my-approvals*
- [x] 新增 /api/v1/notification-preferences*
- [x] 新增 /api/v1/system-configs*
- [x] 新增 /api/v1/menus*
- [x] 新增 /api/v1/tenants*
- [x] 新增 /api/v1/permissions*
- [x] 新增 /api/v1/bpmn/tasks*
- [x] 新增 /api/v1/bpmn/instances*
- [x] 新增 cloud, investigation, cmdb 等路径

## 迁移 SQL

- [x] 为 tenant 1 创建新权限定义
- [x] 分配权限给角色
