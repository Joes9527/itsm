# BPMN 流程实例/任务的实例级授权修复 — 设计文档

- 日期：2026-08-25
- 状态：待实现
- 关联 backlog：[[project-rbac-dual-declaration-convergence-complete]] 记录的"新发现，建议优先级高于以上几项"条目；`docs/superpowers/specs/2026-08-24-rbac-dual-declaration-convergence-design.md` 第 101-102 行

## 问题描述

BPMN 相关的读写接口目前只做租户级隔离，没有做实例级（谁是发起人/负责人/候选人）授权：

1. `GET /api/v1/bpmn/process-instances`（`ListProcessInstances`）——任何通过粗粒度角色门槛的用户（`RequireLegacyBPMNRoles()` 包含 `end_user`，事实上覆盖几乎所有登录用户）都能看到**整个租户**当前所有流程实例，不管是不是本人发起、是不是本人参与审批。
2. `GET /api/v1/bpmn/tasks/:id`（`GetTask`）——同样零过滤，任何人可查看任意任务详情。
3. `GET /api/v1/bpmn/tasks`（`ListUserTasks`，"我的待办"）——不传参数时正确默认到调用者自己身份，但调用方显式传 `user_id`/`assignee`/`candidate_users`/`candidate_groups` 时，服务层直接按传入值过滤，没有校验是否等于调用者本人——可以查询**他人**的待办列表。
4. `/api/v1/bpmn/tasks/*` 下的 `assign`/`cancel`/`variables`（setVariables）/`counter-sign`/`counter-sign-status` 这组动作路由，路由层被有意去掉了粗粒度角色门槛（`candidate_groups` 可以是任意角色名，粗门槛会误伤合法候选人，见 commit `7b0e2cb4`），但也**没有任何替代的实例级检查**——任何租户内认证用户可以重新指派/取消/改变量任意任务。相比之下 `claim`/`complete`/`decisions`/`vote` 已经有 `authorizeTaskActor`/`isTaskCandidate` 做真正候选人级别校验。

前端侧核实：唯一真实需要"看全部"的场景是 `/workflow/instances` 运维监控台（+ admin 仪表盘的运行中实例计数），且这两处目前都没有被限制为管理/运维角色。其余所有真实调用（"我的待办"、"我的审批"、从待办进入的任务详情）从不传递越权参数，只用自己的身份查询——也就是说当前能"看别人数据"的路径，只有直接调 API 才能触发，前端没有任何合法用途在用它。

## 设计决策

### 1. "看全部" vs "只看自己参与的"：复用已有权限点位

`process_instance:read`、`task:read`、`task:update` 三个权限码已经存在于 `pkg/seeder/seeder.go` 的权限目录，且已经在一组平行的简化别名路由（`router/router.go:1398-1407` 的 `/api/v1/workflow/instances`、`/api/v1/workflow/tasks`）上使用，只是主路由（`/api/v1/bpmn/*`）没有接上。

不新建角色/权限概念。改为：

- 拥有 `process_instance:read` 权限的调用者 → `ListProcessInstances` 保持现状（租户内全部可见，供运维台使用）。
- 拥有 `task:read` 权限的调用者 → `GetTask`/`ListUserTasks` 显式覆盖参数保持现状（可查任意任务/按任意条件过滤）。
- 拥有 `task:update` 权限的调用者 → `assign`/`cancel`/`variables`/`counter-sign` 可以绕过候选人检查（管理员改派卡住的任务是合理需求）。
- 没有对应权限的调用者 → 一律收窄到"调用者参与的范围"（见下）。

路由层现有的 `RequireLegacyBPMNRoles()` 粗门槛**不变**——它决定"能不能调用这个接口"，本次修复只补上"调用后能看到/操作什么数据"这一层，两者是不同关注点的正交检查（类比已有的 `authorizeTaskActor` 模式：粗粒度"是认证用户" + 细粒度"是真正的候选人"）。不属于本次修复范围的、`RequireLegacyBPMNRoles()` 本身是否该重新设计的问题，留给以后单独评估。

### 2. "参与" 的定义与唯一实现

"调用者参与某任务" = 调用者是该任务的 `assignee`，或在 `candidate_users` 中（按用户 ID 或用户名匹配，两种格式都存在），或调用者所属的角色/组出现在 `candidate_groups` 中。

**必须只有一份实现**，避免这次会话里刚踩过的坑（`isValidChangeStatusTransitionForBPMN` 与 canonical 状态机各写一份、悄悄分叉）：新增一个共享的判定/查询构造函数（例如 `service/bpmn/participation.go` 里的 `resolveCallerParticipation(ctx, client, userID) (candidateUsersCSVMatch, groupNames []string, err error)` 或等价形式），下列位置全部复用它，不允许独立实现：

- `ListUserTasks` 的过滤逻辑（`service/bpmn_process_engine.go:2265-2319`，目前已经有一份组解析逻辑，本次把它提取成共享函数，而不是新增一份）
- `authorizeTaskActor`（`service/bpmn_process_engine.go:514-539`）—— 目前只查 `Assignee`/`CandidateUsers`，本次补上 `CandidateGroups` 检查
- `isTaskCandidate`（`service/bpmn_process_engine.go:2414-2432`）—— 同上
- `GetTask` 的授权判断（新增）
- `ListProcessInstances` 的任务侧过滤（新增，见下）

"调用者参与某流程实例" = 调用者是该实例的 `initiator`（见下），或该实例下存在任意任务满足上面的"参与"条件。

### 3. 查询策略：两步查询，不用复杂子查询

`ListProcessInstances` 无 elevated 权限时的过滤，采用两步查询而不是在 ent 里拼一个"存在满足条件的关联任务"的子查询/JOIN（ent 的子查询写法复杂、调试成本高，收益对这个场景不成比例）：

1. 按共享的"参与"判定查 `ProcessTask` 表（`TenantID` + assignee/candidate_users/candidate_groups 匹配），取出这批任务所属的 `process_instance_id` 去重集合。
2. 查 `ProcessInstance`：`TenantID(tenantID) AND (Initiator(callerIDStr) OR IDIn(第一步的instanceID集合))`。

`GetTask`/`ListUserTasks`/task 动作路由的过滤更简单，直接用共享判定函数对单个任务做布尔检查即可，不需要两步查询。

**租户隔离是硬约束**：上面每一步查询都必须显式带 `TenantID` 谓词，不能只依赖上层中间件已经做过租户上下文注入。按 AGENTS.md"Cross-tenant access must fail closed"要求，每个新查询都要配跨租户测试用例（同租户能看到 vs 跨租户看不到，即使 ID 猜得到）。

### 4. `initiator` 字段回填

`ent/schema/process_instance.go` 已有 `initiator` 字段（索引、可选字符串），但从未被写入过——`StartProcess`（`service/bpmn_process_engine.go:206-266`）建 `ProcessInstance` 时没有调 `.SetInitiator(...)`。

本次修复：`StartProcess` 补上 `.SetInitiator(fmt.Sprint(callerUserID))`，取自 `ctx.Value(bpmn.BPMNUserIDContextKey)`；系统触发（无认证用户上下文，例如工单自动触发流程）时，沿用现有 `requester_id` 变量的值作为兜底。

**已知局限，本次不处理**：修复上线之前创建的历史流程实例，`initiator` 字段是空的。这些旧实例的原发起人如果自己不是任何任务的候选人/负责人，修复后会看不到自己过去发起的流程，只能看到修复上线之后新建的流程。不做一次性回填脚本——历史数据里 `requester_id` 变量的存在性和格式不保证干净，回填脚本本身的正确性难以验证，风险收益不成比例。如果这个过渡期影响后续被证明是真实痛点，再评估补救方案。

### 5. 审计记录（本次一并处理，非仅新增的 elevated 绕过路径）

现状核实：`service/bpmn_audit_service.go`（`BPMNAuditService`）已经有完整的审计写入方法：`RecordTaskAssigned`、`RecordTaskClaimed`、`RecordTaskCompleted`、`RecordVariableChanged` 等，但 `AssignTask`（`service/bpmn_process_engine.go:2399`）等写路径目前**完全没有调用**它们——这是一个独立于本次授权修复、原本就存在的审计缺口，覆盖所有调用者（不分是否 elevated）。

按用户确认，这次一并处理：

- `AssignTask` → 调用现有 `RecordTaskAssigned`
- `CancelTask` → 检查是否有对应的 `RecordTaskCancelled`（若无，按现有方法的模式新增一个，复用 `BPMNAuditService`，不新建另一套审计机制）
- `SetTaskVariables` → 复用已有的 `RecordVariableChanged`
- `CreateCounterSignTasks`/`counter-sign` 相关 → 检查是否有对应记录方法，若无，同上按现有模式补齐

不新增审计存储/表结构，全部复用 `ProcessAuditLog` + `BPMNAuditService` 现有基础设施（`ent/schema/process_audit_log.go`）。

## 逐接口行为总表

| 接口 | 有对应 elevated 权限 | 无对应 elevated 权限 |
|---|---|---|
| `GET /bpmn/process-instances` | 租户内全部可见（现状不变） | 只返回 `initiator=我` 或存在参与任务的实例（两步查询） |
| `GET /bpmn/tasks/:id` | 任意任务可查看 | 仅当我是该任务参与者，或我是所属实例的发起人（只读） |
| `GET /bpmn/tasks`（我的待办） | 显式覆盖参数（`user_id`/`assignee`/`candidate_users`/`candidate_groups`）保持现状 | 忽略调用方传入的覆盖参数，强制按调用者自身身份过滤 |
| `assign`/`cancel`/`setVariables`/`counter-sign` | 可对任意任务操作，**必须写审计记录** | 沿用/扩展后的 `authorizeTaskActor`：非参与者拒绝（403） |

## 非目标（本次不做）

- 不重新设计 `RequireLegacyBPMNRoles()` 或路由层粗粒度角色门槛。
- 不为历史流程实例做 `initiator` 回填脚本。
- 不新建角色/权限概念，不改 `permissions` 表结构。
- 不为只读查看（`ListProcessInstances`/`GetTask` 的 elevated 全量查看）单独加审计记录——`审计优先` 规则针对的是高风险动作/变更，不是常规管理员只读浏览。

## 测试计划

- 每个受影响接口至少覆盖：参与者能看到/操作自己参与的数据；非参与者（同租户内其他用户）看不到/不能操作；跨租户（不同 `tenant_id`）即使 ID 猜得到也拒绝；拥有对应 elevated 权限的角色不受限制。
- `candidate_groups` 命中场景的回归测试（当前 `authorizeTaskActor`/`isTaskCandidate` 缺失这一档，需要补）。
- `initiator` 回填：`StartProcess` 后查库确认字段真的被写入；系统触发路径（无用户上下文）走 `requester_id` 兜底的用例。
- 审计记录：`assign`/`cancel`/`setVariables`/`counter-sign` 分别断言对应 `ProcessAuditLog` 行被创建，字段（actor、目标任务、动作类型）正确。
- 沿用仓库现有约定：`go test ./service/... ./controller/...`，改动前后跑一遍 `go test ./...` 全绿。
