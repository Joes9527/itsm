# 工单/服务请求动作权限统一化设计

Status: Draft (awaiting user review)
Date: 2026-08-24

## 背景

在多角色权限测试中发现：普通员工（`end_user`，测试账号 D05578）从服务目录提交服务请求后，在工单详情页能看到并点击"开始交付"和"分配"——这两个动作按设计应仅供服务台/运维人员操作。

深入排查后确认这不是单个按钮遗漏隐藏的问题，而是权限模型在多处设计得不够精细：

1. `service_request:write` 这一个权限点同时覆盖了"提交自己的申请"和"对任意申请执行交付"两个语义完全不同的动作，且真正该执行交付的运维/服务台角色反而没有这项权限。
2. 工单详情页的动作按钮（批准/拒绝/分配/交付/删除/编辑/抄送）门控逻辑分散在前端各自实现，只有"批准/拒绝"认真做了"不能操作自己提交的工单"判断，其余按钮均无该判断，是一类会反复出现的、非蓄意的遗漏。
3. 历史数据库重置导致工单 ID 被复用，`field_values` 表里的孤儿数据"继承"给了新工单，导致工单详情页显示了不属于自己的字段。
4. 排查过程中额外发现两处更大范围的既有技术债（见"拆分立项"一节），本轮不处理，仅记录边界。

## 范围

**本轮处理**：
- Item 1：`service_request:provision` 权限拆分 + 角色矩阵重新分配
- Item 2：工单/服务请求动作权限统一由后端计算（`actions` 契约）
- Item 3：`field_values` 孤儿数据一次性清理 + 根因防范

**拆分立项，本轮不做**（见文末）：
- 审批机制收口（轻量状态变更 vs BPMN 审批引擎）
- RBAC 全局表（`ResourceActionMap`）与路由独立声明（`RequirePermission`）双轨制收敛

---

## Item 1：`service_request:provision` 权限拆分与职责分离

### 现状与根因

- 路由层：`POST /api/v1/service-requests/:id/provision`（`ProvisioningController.StartProvisioning`，router.go:870）与 `POST /api/v1/provisioning-tasks/:id/execute`（`ExecuteProvisioningTask`，router.go:877）均使用 `RequirePermission("service_request", "write")`。
- 全局兜底表：`middleware/rbac.go` 的 `ResourceActionMap` 中，POST `/api/v1/service-requests/*` 与 `/api/v1/provisioning-tasks/*` 均映射为 `{service_request, write}`。
- 角色层：`end_user` 为能自助提单（`POST /api/v1/service-requests` 需要 `service_request:write`，pkg/seeder/seeder.go:1878）被授予该权限，顺带解锁了对任意服务请求执行交付的能力。
- `l1_support`、`l2_support`、`l3_expert`、`ops_engineer`、`dba`、`network_eng`、`sd_manager`、`ops_manager`、`service_catalog_admin` 这些真正该执行交付的角色，目前**没有任何 `service_request:*` 权限**（已用活数据库核实，非仅代码推断）。
- `ProvisioningService.CreateTaskFromServiceRequest`/`ExecuteTask` 接收 `actorUserID` 参数但从未使用它做归属校验——只检查了租户隔离和关联工单是否已审批通过。

### 设计

1. **新增权限点**：`service_request:provision`（resource=`service_request`, action=`provision`），仿照 `ticket:assign`、`release:approve` 的既有拆分先例。
2. **路由改用新权限**（两处，且需同步改 `ResourceActionMap` 对应条目，否则全局层仍按旧的 `write` 放行，见 Item 4 的双轨制说明）：
   - `router.go:870`：`RequirePermission("service_request", "write")` → `RequirePermission("service_request", "provision")`
   - `router.go:877`：同上
   - `middleware/rbac.go` `ResourceActionMap["POST"]`：新增更具体的路径条目 `"/api/v1/service-requests/*/provision"` → `{service_request, provision}`、`"/api/v1/provisioning-tasks/*/execute"` → `{service_request, provision}`（利用现有的"最长前缀优先"匹配逻辑，不影响 `/api/v1/service-requests` 本身的 create 语义）。
3. **角色权限矩阵调整**（pkg/seeder/seeder.go `rolePermissionMap`）：
   - `end_user`：保留 `service_request:read`/`write`（提单），不授予 `provision`。
   - 新增 `service_request:provision`：`l1_support`、`l2_support`、`l3_expert`、`ops_engineer`、`dba`、`network_eng`、`sd_manager`、`ops_manager`、`service_catalog_admin`。
   - `sysadmin`/`it_director`/`ops_director`：已通过 `allPermissionCodes()`/`allExcept(...)` 覆盖，无需改动。
4. **服务层硬性职责分离规则**（双保险，不完全依赖权限矩阵）：
   通过 Item 2 定义的 `CanProvision` 函数实现（`service_request:provision` 权限 + 排除申请人本人），**不要在这里另写一份内联判断**。`CreateTaskFromServiceRequest`/`ExecuteTask` 加载 `ServiceRequest`/`ProvisioningTask` 后调用 `CanProvision`，返回 `allowed=false` 时用其 `reason` 作为业务错误信息拒绝。`CanProvision` 只实现一次，这里和 Item 2 的 `ServiceRequestResponse.actions.provision` 调的是同一个函数——详见 Item 2 及文末"依赖关系与实施顺序"。
5. **前端**：`ServiceRequestPanel.tsx` 的"开始交付"按钮改为读取 `ServiceRequestResponse.actions.provision`（见 Item 2），不再无条件渲染。
6. **配套数据库 migration（不能只改 `seeder.go`）**：`AutoSeed`/`AutoMigrate` 只在显式的 `ITSM_BOOTSTRAP_ONLY=true` 引导任务里跑（`internal/bootstrap/app.go` `ValidateWebStartupConfig`），长驻的 Web 进程启动时不会重新 seed；改 `seeder.go` 里的 `rolePermissionMap` 只对全新初始化的库生效，已存在的开发/测试/生产库不会自动补齐。本仓库对这类"新增权限定义 + 补发角色授权"的变更已有固定先例（`migrations/20260814_missing_permission_definitions.sql`、`migrations/20260814_end_user_missing_permissions.sql`），新增 `migrations/20260824_add_service_request_provision_permission.sql`，照抄同一个两步模式：
   ```sql
   -- 1. 补全 service_request:provision 权限定义（对所有现有租户，幂等）
   INSERT INTO permissions (code, name, resource, action, tenant_id, created_at, updated_at)
   SELECT 'service_request:provision', '执行服务请求交付', 'service_request', 'provision', t.tenant_id, now(), now()
   FROM (SELECT DISTINCT tenant_id FROM roles) t
   WHERE NOT EXISTS (
       SELECT 1 FROM permissions p
       WHERE p.code = 'service_request:provision' AND p.tenant_id = t.tenant_id
   );

   -- 2. 关联给履约角色（幂等）
   INSERT INTO role_permissions (role_id, permission_id, tenant_id)
   SELECT r.id, p.id, r.tenant_id
   FROM roles r
   JOIN permissions p ON p.tenant_id = r.tenant_id AND p.code = 'service_request:provision'
   WHERE r.code IN ('l1_support','l2_support','l3_expert','ops_engineer','dba','network_eng','sd_manager','ops_manager','service_catalog_admin')
     AND NOT EXISTS (
       SELECT 1 FROM role_permissions rp
       WHERE rp.role_id = r.id AND rp.permission_id = p.id AND rp.tenant_id = r.tenant_id
     );
   ```
   `seeder.go` 的改动仍然要做（保证全新环境从一开始就正确），migration 文件是补给已存在环境的必要配套，两者不是二选一。

---

## Item 2：工单/服务请求动作权限统一由后端计算

### 设计原则

前端不承担业务判断，只做数据展示和格式校验。所有"这个按钮能不能点"的判断，由后端算好、随对应资源的详情响应下发；前端只读取该结果控制 `disabled`/`title`。**同一套判断函数**必须同时用于（a）响应中组装 `actions` 字段，和（b）用户实际发起写请求时的服务层校验——严禁前后端各自维护一份、或读/写两条路径各写一份可能算出不同结果的判断逻辑。

### DTO 契约

`ActionPermission` 通用结构（新增，放在 `dto` 包，供两个 Response 复用）：

```go
type ActionPermission struct {
    Allowed bool   `json:"allowed"`
    Reason  string `json:"reason,omitempty"`
}
```

**`TicketResponse`**（`dto/ticket_dto.go`）新增字段，覆盖工单核心域的 6 个动作：

```go
Actions map[string]ActionPermission `json:"actions"`
// key: "approve", "reject", "assign", "edit", "cc", "delete"
```

**`ServiceRequestResponse`**（`dto` 包内对应服务请求的响应类型）新增字段，覆盖服务请求域的动作：

```go
Actions map[string]ActionPermission `json:"actions"`
// key: "provision"（预留未来可能的 "cancelProvision" 等）
```

`provision` 不放进 `TicketResponse.actions`：工单是通用核心域，不应感知服务目录履约这类扩展子域的具体动作；`ServiceRequestPanel.tsx` 本来就独立调用 `GET /service-requests/by-ticket/:id`，天然消费 `ServiceRequestResponse.actions`，不需要工单详情接口额外关联查询服务请求表。

### 判断规则（原子化拆分，按动作组合，不用一个大函数套所有场景）

原子谓词（Go 函数，供各 `CanXxx` 组合调用）：
- `hasPermission(client *ent.Client, role, resource, action string, tenantID int) bool` — 直接复用现有 `middleware.HasResourcePermission`，签名照抄，不要另包一层简化版；`PermissionConfigModeDBOnly` 下必须带 `client`+`tenantID` 才能命中按租户缓存的权限查询。
- `isRequester(ticket, actorUserID)` — `ticket.RequesterID == actorUserID`
- `isFinalStatus(ticket)` — 复用现有 `isFinalStatus(status)` 辅助函数

每个 `CanXxx` 函数的入参因此至少需要 `ctx context.Context, client *ent.Client, tenantID int, actorUserID int, actorRole string` 加上具体资源实体（`*ent.Ticket` 或 `*ent.ServiceRequest`）——不要只传 `role string` 三件套，那是简化写法，真正实现要接住 `HasResourcePermission` 的完整签名。

组合规则：

| 动作 | 权限码 | 排除本人 | 排除终态 | 其它条件 |
|---|---|---|---|---|
| `CanApprove`/`CanReject` | `ticket:update` | 是 | 是 | 无（本轮沿用现有"轻量版"语义，不涉及 BPMN 收口） |
| `CanAssign` | `ticket:assign` | 否 | 是 | 分配是路由工单给合适的人，非"对自己的工作做背书"，不属于职责分离场景 |
| `CanEdit` | `ticket:update` | 否 | 是 | — |
| `CanCC` | 无独立权限码 | — | — | **复用既有函数，不新写规则**：`service/ticket_workflow_service.go:982` 的 `ensureCanCCTicket`（需导出为 `EnsureCanCCTicket`）已经实现了完整规则——工单状态为 `closed`/`cancelled` 时拒绝；允许者为申请人、处理人（assignee）、`super_admin`、该工单的审批人（`TicketApproval` 记录）、或已在该工单抄送列表中的人（`TicketCC` 记录）。`CanCC` 内部构造一个 `TicketWorkflowService{client, logger}` 调用该函数，把返回的 `error` 转成 `ActionPermission{allowed:false, reason: err.Error()}`。注意该函数接收的是 `*ent.Ticket`（ent 原生实体），跟其余 5 个 `CanXxx` 接收的 `*ticket.Ticket`（`repository/ticket` 领域模型）不是同一类型，`CanCC` 签名要单独处理，不能套用其它 5 个的统一签名。真实端点是 `POST /api/v1/tickets/workflow/cc`（`TicketWorkflowController.CCTicket`），路由层权限码是 `workflow:update`，与 `ensureCanCCTicket` 的业务规则是两层独立校验，都要保留。 |
| `CanDelete` | `ticket:delete` | 否 | 是 | **加一道安全阀**：若该工单绑定的 BPMN 流程实例仍在运行（`ProcessInstance` 表 `business_key = "ticket:{id}"` 且 `status = "running"`），禁止删除，Reason 提示"工单流程流转中，不可删除"。理由：删除流转中的工单会让 `process_tasks` 变成指向已软删除工单的孤儿任务，是与 Item 3 同一类问题的新来源，此时拦截成本极低。 |
| `CanProvision`（挂在 `ServiceRequestResponse`） | `service_request:provision` | 是 | — | 见 Item 1 |

> 字段核对（已直接读 `ent/schema/process_instance.go` 源码确认）：`ProcessInstance` 表只有单一的 `business_key`（`"ticket:{id}"` 格式）字段，**没有**分开的 `business_type`/`business_id` 列。那对分开的列是另一张表 `ProcessApprovalDecision`（`ent/schema/process_approval_decision.go:22-23`）的字段，两张表用的是两套不同的业务关联约定，写查询时不要混用。`CanDelete` 这里查的是 `ProcessInstance`，必须用 `business_key`。

### 实现落点

- 新建 `service/ticket_authorization.go`（或类似命名，遵循 `*_service.go` 文件命名规范的变体，具体命名在实施计划阶段确认），承载 6 个工单域 `CanXxx` 函数与组装 `actions` map 的辅助函数。
- 服务请求域的 `CanProvision` 放在 `ProvisioningService` 或 `service_request` 相关 service 文件内。
- **读路径**：`ToTicketResponseWithCustomFields`（`service/ticket_service.go`）与服务请求详情的 mapper，组装响应时调用上述 `CanXxx` 函数填充 `actions`。
- **写路径**：`TicketController` 的 approve/reject/assign/edit/delete 对应 handler、`TicketWorkflowController.CCTicket`、以及 `ProvisioningController` 的 provision handler，在 `RequirePermission` 粗粒度中间件通过后，加载具体资源实例，调用**同一个** `CanXxx` 函数二次校验；若返回 `false`，返回 403 + 对应 Reason。
- 前端 `TicketDetail.tsx` 全部 7 个按钮的 `disabled`/`title` 改为读 `ticket.actions?.[x]?.allowed`/`.reason`（`provision` 读 `serviceRequest.actions?.provision`），移除现有的 `isRequester`/`isTicketFinal` 等前端本地判断代码。
- 前端类型定义同步：新增共享类型 `ActionPermission { allowed: boolean; reason?: string }`；`src/types/ticket.ts` 的 `Ticket` interface（68行起）新增 `actions?: Record<string, ActionPermission>`；`src/types/biz/service-request.ts` 的 `ServiceRequest` interface（31行起）同样新增该字段。字段名沿用后端 DTO 的 `actions`（已是 camelCase，无需转换）。

---

## Item 3：`field_values` 孤儿数据清理

### 根因

`field_values` 表按 `(tenant_id, entity_type, entity_id)` 存储动态字段值，`entity_id` 与 `tickets.id` 之间**没有外键约束**（ent schema 设计为弱引用，允许字段定义被删除后历史值仍可展示）。历史开发库重置时，`tickets`/`field_definitions` 表被清空重建（ID 从 1 重新分配），但 `field_values` 未同步清理。新工单复用了旧工单的 ID 后，读取时"继承"了不属于自己的历史字段值。查询代码本身（`FieldValueService.ListValues`）逻辑正确，问题在数据侧。

### 清理方案（三阶段）

1. **Dry-run 预览**（先跑 SELECT，不做任何写操作）：
   ```sql
   -- Q1：完全孤儿——entity_id 在 tickets 表里已不存在
   SELECT count(*), entity_id FROM field_values
   WHERE entity_type = 'ticket' AND entity_id NOT IN (SELECT id FROM tickets)
   GROUP BY entity_id;

   -- Q2：时间错乱——字段值的创建时间早于其所属工单的创建时间（逻辑上不可能，即 ID 复用残留）
   SELECT count(*), fv.entity_id, fv.created_at, t.created_at AS ticket_created_at
   FROM field_values fv
   JOIN tickets t ON fv.entity_id = t.id
   WHERE fv.entity_type = 'ticket' AND fv.created_at < t.created_at
   GROUP BY fv.entity_id, fv.created_at, t.created_at;
   ```
   抽样输出命中行的 `field_name`/`field_label`/`value`，人工确认特征符合预期（如 ID 14 上的"账号申请"字段），确认无误伤后再进入下一阶段。此次复现的场景命中的是 Q2，不是 Q1（ID 14 在新库中存在，只是换了主人）。

2. **事务内清理**：带事务执行对应 DELETE，输出受影响行数，仅在此开发库执行，不面向生产环境。

3. **根因防范**：现有 `TicketService.DeleteTicket` 是软删除（仅标记 `deleted_at`，ID 不会被释放复用），本身不会重现这次的 ID 复用场景，因此不需要为软删除路径新增级联清理。真正的防范点在于：若未来新增硬删除/数据清理类功能（会真正释放 ID 供复用），必须在同一事务内级联清理对应 `field_values`，并将这条规则写入该功能的实现约束里，不要等下一次环境重置或数据清理任务上线后再补。

---

## Item 4：拆分立项（本轮不处理，仅记录边界）

### 4a. 审批机制收口

现状：`PUT /tickets/:id/status`（"轻量版"直接改状态）与真正的 BPMN 任务审批系统（`POST /bpmn/tasks/:id/decisions`）是两条独立机制并存，另有第三条已存在但未接入前端的桥接路径 `POST /tickets/workflow/approve`（`TicketWorkflowService.ApproveTicket`，已正确调用 `CompleteBusinessApprovalTask`）。

收口涉及至少三个需要独立决策的问题：
1. `authorizeTaskActor`（任务完成时的实际授权闸）与 `ListUserTasks`（"我的待办"列表查询）对 `candidate_groups` 的处理不一致，需先统一判断标准。
2. `incident_emergency_flow.bpmn` 的"主管审批"节点缺少 `taskPurpose="approval"` 属性，导致该节点未走 `assigneeRole`/`assigneeGmChain` 等解析逻辑，被错误分配给了工单申请人本人。
3. 产品行为变化需确认：`ticket_general_flow`（多数非服务目录工单走这条）的审批网关默认跳过（`approval_required` 默认 `false`），收口后这类工单将不再出现"批准/拒绝"入口——这是符合逻辑的正确行为，但用户可感知，需要产品侧明确认可。

### 4b. RBAC 双轨制收敛

`middleware/rbac.go` 的 `ResourceActionMap`（全局粗粒度兜底表）与 `router.go` 中每条路由自行声明的 `RequirePermission(resource, action)`（细粒度声明）是两套独立维护的权限声明，Gin 中间件按 AND 逻辑要求两者都通过。已发现至少一处实质性不同步：`PUT /api/v1/tickets/*` 与 `/status` 子路径，路由声明为 `ticket:update`，全局表却写的是 `ticket:write`，导致除 `sysadmin`/`it_director`/`ops_director` 外的所有角色（含 `l1_support`、`sd_manager`、`end_user` 等）实际调用会在全局层被拦截，即使路由自身的声明是正确的。已用活数据库核实。

`docs/adr/0001-canonical-rbac-and-initialization.md` 记录了这本应收敛到 `endpoint_acls` 单一权威表的迁移计划（第2-4步：建表、CI 比对双读差异、切换权威），但该表在当前开发库中尚未建立（查询报 relation 不存在），说明这次迁移停留在早期阶段，双轨制导致的漂移此前未被发现。

本轮策略：仅针对本次改动涉及的接口（`service-requests/*/provision`、`provisioning-tasks/*/execute`、`tickets/*` 的 PUT 系列）手动同步两处声明，不做全库收敛。完成 ADR-0001 迁移作为独立后续项目。

---

## 依赖关系与实施顺序

三个 Item 不是三个先后阶段，实际依赖关系如下：

- **`CanProvision` 只实现一次**，同时满足 Item 1（`ProvisioningService` 里的强制职责分离校验）和 Item 2（`ServiceRequestResponse.actions.provision` 的展示）两边的需求——这是同一份代码的两个消费方，不是先做 Item 1 的"服务层校验"、再做 Item 2 的"actions 字段"两个先后步骤。实施时应先把 `CanProvision` 写好并接入两个消费方，再各自验证。
- **Item 1 的路由权限码切换、角色矩阵调整（seeder.go + migration 文件）不依赖 `actions` 契约**，可以独立先行，跟 Item 2 的其它 6 个 `CanXxx` 函数（approve/reject/assign/edit/cc/delete）没有先后关系，可并行开发。
- **Item 2 的 6 个工单域 `CanXxx` 函数之间也互相独立**，可以按任意顺序或并行实现，共享同一个 `ActionPermission` 类型和 `TicketResponse.actions`/`ServiceRequestResponse.actions` 契约即可。
- **Item 3（`field_values` 清理）与 Item 1/2 完全无依赖关系**，可以并行处理，也可以独立于本次改动单独执行——它是数据修复，不是代码改动。
- **Item 4 的两个拆分项目不在本轮排期内**，无需现在安排顺序。

## 验证计划

- 后端：`service/ticket_authorization_test.go`（或对应文件）覆盖每个 `CanXxx` 函数的组合场景（含"本人+权限具备但仍被拒绝"这类职责分离场景）；`go test ./...`。
- 前端：`TicketDetail.tsx`/`ServiceRequestPanel.tsx` 相关组件测试更新为基于 `actions` 字段的渲染断言；`npm run type-check`。
- 回归验证路径：以 `end_user`（无 `service_request:provision`）与 `l1_support`（有）两个角色，分别真实点击"开始交付"，确认前者按钮置灰且直接调用接口会被 403，后者可正常执行（但对自己提交的申请仍被拒绝）。
- `field_values` 清理脚本：Dry-run 输出需在实施 PR 描述中留痕，供审查。
