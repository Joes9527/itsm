# RBAC 双轨制收敛设计

**Status:** Implemented — see docs/superpowers/plans/2026-08-24-rbac-dual-declaration-convergence.md
**Date:** 2026-08-24
**Related:** [`2026-08-24-ticket-action-authorization-design.md`](2026-08-24-ticket-action-authorization-design.md)（Item 4b 是本项目的直接前置发现）、`docs/adr/0001-canonical-rbac-and-initialization.md`

## 背景

在上一轮工单动作权限收敛项目里，我们确认并修复了一处线上漂移 bug：`PUT /api/v1/tickets/:id` 在全局权限表里声明的是 `ticket:write`，但路由自己声明的是 `ticket:update`——两者不一致导致除少数角色外，几乎所有角色都无法编辑工单。这个 bug 已经单独修过了，但它暴露的是一个架构层面的问题，本项目要解决的是这个问题本身，而不是某一处漂移的症状。

## 问题

同一个接口"需要什么权限"，现在由两个**独立实现、需要人工保持一致**的判断路径共同决定：

1. **全局层（下称 OLD）**：`auth`/`msp` 这两个 `gin.RouterGroup` 通过 `.Use(middleware.RBACMiddleware(...))` 挂载的全局中间件。它调用 `hasPermission(client, role, method, path, ...)`，从裸路径字符串反推 resource:action——先查 `ResourceActionMap`（`middleware/rbac.go` 里一个几百行的手工维护字面量），查不到再走 `SmartCheckPermission` 的四层 fallback（L1 auth 白名单 → L2 `endpoint_acls` 数据库表，当前库里未建表/为空，永远返回 false → L3 `ResourceActionMap` URL 精确/通配匹配，查不到再退化为"按 URL 第三段猜 resource + 按 HTTP method 猜 action" → L4 硬编码 `RolePermissions` 兜底，这张表里只有 `super_admin`/`end_user`/5 个 `msp_*` 角色，其余 16 个真实角色完全不在表里）。
2. **路由层（下称 NEW）**：部分路由（610 条）各自显式挂的 `middleware.RequirePermission(resource, action)`。它调用 `hasResourcePermission(client, role, resource, action, tenantID)`，resource:action 已经明确给定，直接查 `role_permissions`（走 `loadPermissionsByMode`，当前运行时固定为 `PermissionConfigModeDBOnly`，即完全以数据库授权为准）。

Gin 中间件链保证两层都跑（AND 逻辑），且 OLD 先跑。这意味着**每个受保护请求要过两遍权限判断，用的是两套独立实现的代码**：一遍是从 URL 字符串"猜"出来的，一遍是路由自己"明确声明"的。只要两边的 resource:action 不一致，或者其中一边压根没声明，就会产生行为漂移。

`docs/adr/0001-canonical-rbac-and-initialization.md` 已经记录了这个问题，并提出以 `endpoint_acls` 数据库表作为唯一权威来源的长期方案，但该迁移停在第一步——`endpoint_acls` 表在当前开发库里没有被建出来/填充数据。

## 目标 / 非目标

**目标**：把"一个接口的权限由两个可能互相漂移的声明共同决定"，收敛成"由一个声明决定"。

**非目标（明确排除，作为独立 backlog）**：
- 不在本项目落地 ADR-0001 的 `endpoint_acls` DB canonical 方案（业务需求存在但优先级不高，见前序讨论）。
- 不新增任何运行时注册表、路由包装层或启动期 fail-fast 校验机制（曾作为设计选项讨论过，确认属于范围蔓延——解决的是一个从未真实发生过的问题，反而正是 AGENTS.md 明确列为反模式的"新增一层路由机制、同时保留旧机制"）。
- `POST /api/v1/ws/ticket`、`POST /api/v1/connectors/feishu/callback`、以及下文"BPMN 集群"和"完全无覆盖"两组约 108 条路由"正确的权限模型应该是什么"，一律作为独立 backlog，本项目只保证不因为这次重构而改变它们当前的实际生效行为，不在本项目里决定"应该给谁用"。
- BPMN 流程实例/任务的**实例级/数据级授权**（谁能看到具体哪条流程实例、哪个任务——而不是"这个角色有没有资格调用接口"）不属于 RBAC 范畴，本项目不处理，见下文"已知遗留"。

## 现状影响面核查（系统性核实，非抽样）

用真实的线上判断函数（`middleware.SmartCheckPermission` 代表 OLD，`middleware.HasResourcePermission` 代表 NEW）对照当前开发库真实种子数据，跑了一遍全量对照：749 个已登记接口，其中 610 条挂了 `RequirePermission`/`RequireMSPPermission`（29,280 组 tenant×role×route 组合），136 条完全没有路由级权限声明（6,528 组组合）；覆盖两个真实 tenant、18 个 DB 内真实角色 + `super_admin` + 5 个硬编码 `msp_*` 角色。

### 结论 1：610 条已有 `RequirePermission` 的路由——删除 OLD 是净收益，不是风险

`PermissionConfig.Mode` 运行时固定为 `PermissionConfigModeDBOnly`，即 NEW 完全以数据库真实授权为准。OLD 因为 `RolePermissions` 硬编码兜底表只覆盖 `super_admin`/`end_user`/`msp_*`，导致 2,018 组（206 条路由）组合是"OLD 拒绝、NEW 正确允许"——`it_director`、`ops_director`、`sysadmin`、`dept_manager`、`l1_support`、`dba`、`change_manager` 等 16 个真实角色，目前被 OLD 挡在 CMDB 资产、云账号、License、Dashboard、报表、SLA、绝大部分工单操作等一大片功能之外，即便数据库里明明已经授予了对应权限。删除 OLD 会直接修复这批本应可用但目前不可用的功能。反方向（OLD 允许、NEW 拒绝，1,887 组/151 条路由）没有实际影响——今天的门槛是 `OLD AND NEW`，NEW 拒绝的话今天就已经拒绝，删除 OLD 之后仍然拒绝。之前修过的 `PUT /tickets/:id` 的 `ticket:write`/`update` 漂移，现在两边结果一致，不再有差异。

### 结论 2：136 条无路由级声明的路由——需要逐类处理，不能直接删除 OLD

**A 组 — 最初核查发现的 5 处（结构性缺口，各自独立）：**

| 路由 | 当前实际生效判断 | 处理方式 |
|---|---|---|
| `GET /api/v1/msp/status` | `ResourceActionMap` 精确命中 `{msp, read}` | 补 `RequirePermission("msp", "read")` |
| `GET /api/v1/users/profile` | 命中通配符 `/api/v1/users/*` → `{user, read}` | 补 `RequirePermission("user", "read")` |
| `GET /api/v1/users/me` | 同上 | 补 `RequirePermission("user", "read")` |
| `POST /api/v1/ws/ticket` | 无任何匹配，现状对非 super_admin 一律拒绝 | 补 `RequireRole("super_admin")`，注释标注"复刻现状，正确模型见 backlog" |
| `POST /api/v1/connectors/feishu/callback` | 同上 | 补 `RequireRole("super_admin")`，注释标注"复刻现状，正确模型见 backlog" |

**B 组 — `sla/templates/*`（4 条，有真实、合理的 DB 权限模型）：**

命中 `/api/v1/sla/*` 通配符，现状对 `change_manager/it_director/l1_support/l2_support/l3_expert/network_eng/ops_director/ops_engineer/ops_manager/sd_manager/service_catalog_admin/sysadmin` 生效，这是一个合理、现有的权限模型（`sla:read`/`sla:write` 已经是正式的 DB 权限）。处理方式：补 `RequirePermission("sla", "read"/"write")`，视为正确收敛，不进 backlog。

**C 组 — BPMN 引擎集群（74 条：process-definitions/process-instances/tasks/monitoring/dashboard/AI 生成器/process-trigger/process-bindings，注册于 `controller/bpmn_workflow_controller.go` 等 5 个 controller 的 `RegisterRoutes` 方法，`router.go` 里通过 `config.BPMNWorkflowController.RegisterRoutes(tenant.(*gin.RouterGroup))` 等调用挂载）：**

命中 `/api/v1/bpmn/*` 通配符，现状对 `change_manager/dept_manager/end_user/it_director/ops_director/sysadmin`（+`super_admin`）生效。**这组不按"补对应 resource:action 即视为正确"处理**——核实后发现这套 `bpmn:read`/`write` 门槛是历史意外形成的（`ResourceActionMap` 通配符恰好命中），不是有意设计的权限模型，而且即便通过了这层粗粒度门槛，底层 handler（`ListProcessInstances`、`GetTask`）完全没有做实例级过滤——任何能调用接口的角色能看到**整个租户**所有流程实例和任意任务详情，不区分是否为发起人/审批人。处理方式：补 `RequireRole("super_admin", "change_manager", "dept_manager", "end_user", "it_director", "ops_director", "sysadmin")` 精确复刻现状可达角色集（`RequireRole` 是纯 allowlist，没有 `super_admin` 隐式放行——必须显式列入，否则会意外收窄 super_admin 现有权限，这是自查时发现的真实风险点）。不声称这套角色集是"正确模型"，只是保证删除 OLD 不改变现状，归入 backlog，与 D 组一起交给"审批机制收口"项目或独立评估。

**D 组 — 完全无覆盖、现状仅 `super_admin` 可达（约 34 条：known-errors、marketplace、a2ui、global-search、standard-changes、escalation-matrices、domain-configs、部门流程初始化、process-bindings 的 PUT/DELETE）：**

`ResourceActionMap` 无任何匹配，`RolePermissions` 硬编码表也没有对应条目，现状对非 super_admin 一律拒绝。处理方式：补 `RequireRole("super_admin")`，精确复刻现状行为，归入同一条 backlog（"这些模块目前没有真正的 RBAC 权限模型，需要产品侧决定该给哪些角色开放"）。

### 结构上/逻辑上不受影响（供完整性参考，无需处理）

- `public.*`、`/api/v1/auth/me`、`/tenants`、`/menus`、`/logout`、`/switch-tenant`、`/api/v1/ws/notifications`、`/metrics`——不在 `RBACMiddleware` 挂载的分组下，从未经过 OLD。
- 所有走 `RequireMSPPermission` 的 MSP 路由——底层同样调用 `hasResourcePermission()`，与 NEW 共享判断核心。（旁注：5 个 `msp_*` 角色目前在 `roles` 表里完全没有记录，所有 MSP 写/读接口现状对它们一律 403——这是一个已经存在、与本次删除无关的独立缺口，不在本项目处理。）
- `hasPermission()` 里 `GET /api/v1/auth/menus` 的特判分支——死代码，该路由注册在独立的 `authGrp` 下，从未经过 `RBACMiddleware`/`hasPermission`。

## 目标架构

**`middleware/rbac.go` 改动**：
- 删除 `ResourceActionMap`（含 GET/POST/PUT/DELETE/PATCH 各方法子表）。
- 删除 `hasPermission()` 函数、`getPermissionFromPath()`、`matchPath()`（仅被 `hasPermission()`/`SmartCheckPermission` 路径使用，删除后无调用方）。
- `RBACMiddleware` 移除对 `hasPermission(...)` 的调用，保留其余职责：认证态存在性、用户禁用检查、从数据库刷新最新角色、租户ID 解析与校验、写 context、`c.Next()`。
- `RequirePermission(resource, action)`、`hasResourcePermission()`、`loadPermissionsByMode()`、`checkPermissionMatch()` 保持不变。
- A/B 组按上表补齐 `RequirePermission`；C 组补 `RequireRole("super_admin", ...)`（含 6 个现状可达角色，见下文）占位；D 组补 `RequireRole("super_admin")` 占位。
- `RequireRole` 当前响应格式是裸 `c.JSON(http.StatusForbidden, gin.H{"code": 2003, ...})`（`middleware/rbac.go:766-789`），不是项目规范的 `common.Fail`。这次要大量新增 `RequireRole` 调用点，顺手把 `RequireRole` 内部改成 `common.Fail`，消除这个已存在的响应格式偏差——这是对本次改动直接涉及的函数做的对齐，不算范围蔓延。
- `RBACMiddleware` 移除权限判断后，职责收窄为"认证态校验 + 用户禁用检查 + 角色刷新 + 租户ID校验 + 上下文注入"，不再是"挂了就有权限保护"。改动时在函数上补一条 doc comment 说明这一点，避免后续开发者误以为只要路由挂在 `RBACMiddleware` 保护的分组下就自动有细粒度权限保护——细粒度保护现在完全由紧随其后的 `RequirePermission`/`RequireRole`/`RequireMSPPermission` 承担。
- **C 组挂载方式**：不在 74 处路由调用上逐条重复声明，改为在对应 controller 的 `RegisterRoutes` 方法里、对应 `gin.RouterGroup` 变量上做**分组级** `.Use(middleware.RequireRole(...))`。5 个 controller 里 4 个（`BPMNWorkflowController`、`BPMNMonitoringController`、`BPMNDashboardController`、`BPMNAIGeneratorController`）各自只有一个顶层分组，直接在分组创建后加一行 `.Use()` 即可覆盖全部路由。`BPMNProcessTriggerController` 例外：它的 `/process-bindings` 子分组内部本身就是 C/D 混合——`POST ""`、`GET ""`、`GET "/by-type/:business_type"`、`GET "/:id"` 命中 `bpmn:*` 通配符（C 组），但 `PUT "/:id"`、`DELETE "/:id"` 没有被通配符覆盖（D 组，现状仅 super_admin）。处理方式：`bindings` 分组整体先挂 C 组的 `.Use(RequireRole(...))`，再单独给 `PUT "/:id"`/`DELETE "/:id"` 这两条路由**追加**一层 `RequireRole("super_admin")`（Gin 中间件链式 AND，两层叠加后这两条的有效要求就精确收敛成"仅 super_admin"，与现状一致）；该 controller 其余三个子分组（`trigger`、`departments`、`domain-configs`）内部角色集统一，可以直接分组级挂载（`trigger` 是 C 组，`departments`/`domain-configs` 是 D 组）。

**`middleware/smart_permission.go` 改动**：
- 整个文件删除：`SmartCheckPermission`、`checkDatabaseACL`、`checkURLInference`、`checkRoleBasedPermission`、`getCachedACLs`、`loadACLsFromDB`、`isKnownWhitelistPath`、`isAuthWhitelist`、`authWhitelist`、ACL 缓存相关包级状态（`aclCache`、`aclCacheLock`、`aclConfig`）、`EndpointACL`/`DBQuerier` 类型定义（这是 `middleware` 包内的本地类型，与 `ent.EndpointACL`/`ent/schema/endpoint_acl.go` 是两个完全独立的东西——后者是 Ent 生成的 schema，当前没有任何 controller/handler/service 引用它，属于 ADR-0001 的既有脚手架，**不在本项目删除范围内**——复核确认：`ent.EndpointACL` 与 `middleware.EndpointACL` 是两个完全独立的类型，删除后者不影响前者，边界清晰，无需额外处理）。

**不改动**：`router.go` 里已有的 `RequirePermission(...)` 调用、`hasResourcePermission()`、`loadPermissionsByMode()`、`RequireMSPPermission`、`RequireRole`、`PermissionConfig`/`PermissionConfigMode*`、`RolePermissions`（仍被 `loadPermissionsByMode` 的 `HardcodeOnly`/`Merge` 模式使用）、`ent/schema/endpoint_acl.go` 及其生成代码。

## 测试策略

- 删除 `middleware/rbac.go`/`middleware/smart_permission.go` 中随函数一起失效的现有单测。
- A/B 组（9 条）：各加一条 controller/router 层测试，验证持有对应权限的角色可达（200），不持有的角色被拒（403）。
- C 组（74 条）：批量加测试验证 `RequireRole("super_admin", "change_manager", "dept_manager", "end_user", "it_director", "ops_director", "sysadmin")` 占位后，这 7 个角色仍然可达（尤其显式验证 super_admin——`RequireRole` 无隐式放行，容易漏），其余角色仍然被拒——即占位不能收窄也不能放宽现状。
- D 组（约 34 条）：批量加测试验证 `RequireRole("super_admin")` 占位后，仅 super_admin 可达，其余角色仍然被拒。
- `go build ./...` + `go test ./...` 全量跑一遍。
- 手工核对：用非 super_admin 测试账号实际调用 A/B 组路由，确认响应码符合预期。

## 已知遗留（backlog，不在本项目处理）

- ADR-0001 `endpoint_acls` DB canonical 方案的完整落地。
- C 组（BPMN 引擎集群）、D 组（约 34 条无覆盖路由）"正确的权限模型应该是什么"——需要产品侧决定。
- `POST /api/v1/ws/ticket`、`POST /api/v1/connectors/feishu/callback` 当前实际权限行为的正确性核实。
- **（新发现，建议优先级高于以上几项）BPMN 流程实例/任务的实例级授权缺失**：`GET /api/v1/bpmn/process-instances`、`GET /api/v1/bpmn/tasks/:id` 目前只做 tenant 级隔离，没有按调用者是否为发起人/审批人/候选人做数据过滤——任何能通过粗粒度角色门槛的用户可以看到整个租户所有流程实例和任意任务详情；`GET /api/v1/bpmn/tasks`（"我的待办"）在调用方显式传 `user_id`/`assignee`/`candidate_users` 时会返回**他人**的待办列表，没有校验调用者是否有权查询别人的任务。这是数据越权问题，不是 RBAC 声明问题，建议与"审批机制收口"项目一并评估，或作为独立安全修复优先处理。
  - **补充发现（收尾合并阶段实测证伪）**：`/api/v1/bpmn/tasks/*` 这组接口最初也被套了 C 组的粗粒度角色门槛（`RequireLegacyBPMNRoles()`），但与另一条并行分支新增的 SSL-VPN 审批链路 E2E 测试合并时，实测发现这会把合法的任务候选人挡在外面——BPMN `candidate_groups` 的取值可以是任意角色名（例如 `network_eng`），并不受限于那 7 个角色。已改为不给 `/tasks/*` 套粗粒度角色门槛，还原为收敛前的行为。其中 `claim`/`complete`（含 `decisions`）/`vote` 在 service 层已有 `authorizeTaskActor`/`isTaskCandidate` 做真正的候选人级别校验；`assign`/`cancel`/`variables`/`counter-sign`/`counter-sign-status`/`GetTask`/`ListUserTasks` 目前没有对应校验——这正是本条目上面已经记录的"实例级授权缺失"的一部分，不是这次修复引入的新问题，只是在这次合并过程中被更精确地定位了范围。
- 5 个 `msp_*` 角色在 `roles` 表里完全没有记录，导致所有 MSP 接口现状对其一律 403——与本项目无关的既有缺口。
- `GET /api/v1/msp/status` 相对旧版全局推断层有一次真实的行为收窄：该路由用 `RequirePermission("msp","read")`（纯 DB 驱动），5 个硬编码 `msp_*` 角色因为在 `roles` 表里没有对应行、拿不到任何 DB 授权而被拒绝；旧版是靠硬编码兜底表放行这 5 个角色，删除该兜底表后此路由对它们从"可用"变成"403"。已确认 `/msp/*` 下其它路由本来就全部要求 `RequireMSPPermission`，同样因这 5 个角色零记录而 403，所以这不影响它们对 MSP 控制台的实际可用功能，故本项目内不改动该路由的中间件调用。彻底修复需要给 `msp_*` 角色补上真实的 `roles` 表记录（MSP 上线数据模型任务），或引入新的 OR 逻辑权限判定机制（超出本项目范围，属于新机制）。
