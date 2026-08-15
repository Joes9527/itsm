# 权限体系统一收敛设计（方向 C：分层收敛）

- 日期：2026-08-14
- 状态：已完成
- 相关：`middleware/rbac.go`、`pkg/seeder/seeder.go`、`internal/bootstrap/app.go`、`dto/user_dto.go`、`service/auth_service.go`、`service/menu_service.go`、`handlers/service_request/service.go`

## 1. 背景与问题

当前权限体系存在 **三套角色/权限定义并存**，且两两不一致，导致「加载用户失败」等问题（详见排查过程）：

| 层 | 位置 | 角色数量 | 用途 |
|---|---|---|---|
| 硬编码 `RolePermissions` | `middleware/rbac.go:53` | 13 个通用角色 | 仅 Fallback 模式（开发环境）兜底 |
| seeder `rolePermissionMap` | `pkg/seeder/seeder.go:1746` | 23 个业务角色 | 权威，初始化数据库 |
| 数据库 `roles` + `role_permissions` | 运行时 | 18 个角色 | 运行时权限查询来源 |

### 核心矛盾

1. **角色不对应**：硬编码的 `admin`/`manager`/`agent`/`technician`/`security`/`msp_*` 共 11 个角色，在用户表和数据库里**都不存在**（死代码）。
2. **`super_admin` vs `sysadmin` 二义性**：两者都有「所有权限」但语义不同。
   - `super_admin` = 平台超管（跨租户运维，代码级 `role=="super_admin"` 直接放行，不查数据库）
   - `sysadmin` = 租户系统管理员（单租户所有权限，走数据库 `allPermissionCodes()`）
3. **开发/生产行为不一致**：`configurePermissionMode` 中 `development`→Fallback、`production`→DBOnly。
4. **dto 枚举脱节**：`dto/user_dto.go` 的 role binding 枚举是旧通用角色，与数据库业务角色脱节。
5. **硬编码 end_user 过度授权**：硬编码 `end_user` 有 20+ 条权限（含 `user:read`），而 seeder 权威定义仅 8 条。

## 2. 目标

1. 数据库为**唯一运行时权限权威**。
2. 硬编码 `RolePermissions` 收敛为「最小兜底集」，不再是第二套平行权威。
3. 消除 `super_admin`/`sysadmin` 二义性（明确边界，不合并）。
4. 开发/生产权限行为一致。
5. dto role 枚举对齐数据库角色 code。

## 3. 方案（已确认的决策）

### 决策 1：硬编码 `RolePermissions` 保留 `super_admin` + `end_user`，删除其余 11 个死角色

- 保留 `super_admin` 的 `{Resource: "*", Action: "*"}`（作为显式声明，与 `role=="super_admin"` 代码级放行呼应）。
- 保留 `end_user`，但**精简为与 seeder 一致的 8 条**（`ticket:read`/`ticket:write`/`knowledge:read`/`service_catalog:read`/`ticket_category:read`/`ticket_template:read`/`notification:read`/`tag:read`）。
- 删除：`sysadmin`/`admin`/`manager`/`agent`/`technician`/`security`/`msp_viewer`/`msp_tech`/`msp_specialist`/`msp_manager`/`msp_admin`（11 个）。

> 说明：DBOnly 模式下硬编码 `RolePermissions` 不生效（仅 `super_admin` 代码级放行生效）。保留 `end_user` 兜底是防御性的，以防未来某环境切回 Fallback 时全新数据库不至于完全无权限。

### 决策 2：开发环境也统一 DBOnly

`configurePermissionMode` 改为所有环境统一 `PermissionConfigModeDBOnly`。依赖 seeder 在 bootstrap 时初始化数据库权限（当前本地已初始化，生产 bootstrap job 亦会初始化）。

### 决策 3（补充）：明确 `super_admin` vs `sysadmin` 边界

- `super_admin`：平台超管，跨租户运维，代码级放行，**不可通过数据库收回**。
- `sysadmin`：租户系统管理员，走数据库权限，**可通过数据库收回**。

在 `rbac.go` 和 `seeder.go` 相关位置补充注释说明边界，不改变两者现有行为。

### 决策 4：dto role 枚举对齐

`dto/user_dto.go` 的 role binding 枚举由旧通用角色（`super_admin admin manager agent technician security end_user user`）改为数据库业务角色 code（`super_admin` + `sysadmin`/`it_director`/`ops_director`/.../`end_user`/`guest` 等），或放开为自由字符串由 service 层校验。

## 4. 改动清单

### 4.1 核心改动（已完成）

| 文件 | 改动 |
|---|---|
| `middleware/rbac.go` | `RolePermissions` 删除 6 个死角色（sysadmin/admin/manager/agent/technician/security），保留 super_admin（代码级放行）+ end_user（8 条）+ msp_*（MSP 子系统活跃）；导出 `GetRolePermissions` |
| `internal/bootstrap/app.go` | `configurePermissionMode` 统一 DBOnly |
| `dto/user_dto.go` | role binding 枚举对齐数据库 code |
| `service/auth_service.go` | `getUserPermissions` 改从数据库加载（fallback 硬编码） |

### 4.2 关联清理

**已完成（本次）**：
- 死角色业务引用：`ticket_service.go`（isTicketDataScopeAllRole）、`ticket_workflow_service.go`（ensureCanViewTicketCC）、`incident_alerting_service.go`（RoleIn）、`common/constants.go`（删除 RoleAdmin/RoleManager/RoleAgent/RoleTechnician/RoleSecurity/SuperAdminUser 死常量）
- `getUserPermissions` 改从数据库加载（导出 `GetRolePermissions` 复用 `loadPermissionsFromDB`）
- `handlers/service_request/service.go`：`isServiceRequestAdmin`/`isServiceRequestOperator` 删除，改为 `canManageServiceRequest` 用 `middleware.HasResourcePermission`（service_request:write 权限判断）
- `service/menu_service.go`：菜单构建删除硬编码 `RolePermissions` 读取，改用已有的 `addDatabaseRolePermissions`（数据库加载）
- 前端 `TicketWorkflowActions`/`TicketDetail`：用 `hasPermission('user:read')` 替代硬编码角色名（end_user/guest）

## 5. 风险与回滚

- **风险**：开发环境切 DBOnly 后，若数据库权限未初始化（全新环境未跑 bootstrap），非 super_admin 角色将无权限。
  - **缓解**：确保 bootstrap/seed 流程完整；保留 super_admin 代码级放行作为「总开关」。
- **回滚**：`RolePermissions` 和 `configurePermissionMode` 均为单文件改动，可快速 revert。

## 6. 验证

- `go build ./...` + `go test ./middleware/... ./service/...`。
- 手动验证：end_user（Julian）登录 → 工单详情正常（无「加载用户失败」）、转派/抄送隐藏、仪表盘/审批链可访问（对应权限在数据库已授权）。
- super_admin（admin）登录 → 全功能可用（代码级放行）。
- sysadmin 角色（如有）→ 通过数据库权限校验。

## 7. 待确认项

- 关联清理（4.2）是否与核心改动同 PR，还是拆分为后续独立 PR？
