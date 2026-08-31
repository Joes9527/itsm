# Unified Intake 设计

> 状态：整体设计已批准，待书面审阅
> 日期：2026-08-31
> 上位设计：`2026-08-28-kaf-itsm-autonomous-workitem-delegation-design.md`
> 前置门禁：`2026-08-31-kaf-delegation-release-closeout-design.md` 已完成，Live Dev 结论为 Dev PASS
> 首期范围：`service_request_item` 与 `incident` 的统一创建入口

## 1. 背景

KAF 委派、事务性 Outbox、任务范围 API、action ledger、完成回执和恢复链路已经形成稳定 Dev 基线。下一项独立增量是统一 WorkItem 的创建入口，使 ITSM Web、现有专业 API 和 KAF 渠道不再各自维护身份、校验、事务、幂等和 BPMN 启动语义。

当前实现已经做到部分原子性，但尚不能作为统一入口：

- Service Request 的 WorkItem 与专业扩展在同一事务创建，但 CI 准备发生在事务前，动态 `FieldValue` 在提交后以 warning-only 方式写入，BPMN 通过提交后 goroutine 启动。
- Incident 的 WorkItem、Incident 和创建事件在同一事务创建，但专业扩展仍复制标题、描述、状态、优先级、请求人、租户等 WorkItem 公共字段，并在提交后异步触发规则或 BPMN。
- Catalog 的 `target_class` 仍受遗留 `itsm_type` 同步逻辑影响，尚未完全成为唯一持久化语义。
- ITSM 没有通用创建幂等收据；现有 KAF action ledger 只适用于已委派任务动作，不能复用于建单。
- KAF 遗留 `ticket_create` 仍面向 Gazellio/KEAS，通过调用方提供的员工标识、Web Form 或邮件降级建单，不能证明 Go/WorkItem 版 ITSM 的真实 actor。

本设计只统一创建边界，不重做已经验收通过的 KAF 委派与完成协议，也不建立第二套 BPMN、审批或专业生命周期。

## 2. 目标与非目标

### 2.1 目标

1. 建立唯一 `CreateWorkItemCommand` Application Service。
2. 从认证上下文派生 tenant、actor、requester 和 channel，禁止请求体自报权威身份。
3. 在服务端验证 Catalog、CTI、CI、动态表单、权限、租户归属和配置版本。
4. 以调用方稳定 `idempotencyKey` 保证并发和网络重试只创建一个 WorkItem。
5. 原子创建 WorkItem、一个专业扩展、动态字段、Intake 决策快照、审计和 BPMN 启动 Outbox。
6. 让 ITSM Web、现有 Incident/Service Request API 和 KAF typed client 进入同一应用服务。
7. 保持 WorkItem 与 Incident、Service Request 的专业职责边界，未知类型和未知工作流行为 fail closed。
8. 为创建、重放、冲突、Outbox 积压和人工干预提供可观测证据。

### 2.2 非目标

- 不实现新的统一建单 UI、Intake Session、对话草稿、分阶段附件或知识分流。
- 不实现 Incident `assign`、`resolve`、`close` typed actions。
- 不改变 KAF 委派 action ledger、completion receipt、delivery lease 或 replay-only 合同。
- 不把 KAF Procedure、Tool policy 或执行状态复制到 ITSM Intake。
- 不允许首期代理他人创建；需要时另立带显式权限、原因和审计的命令。
- 不扩展到 Problem、Change、Catalog Task、Known Error 或通用 Ticket 创建。
- 不引入第二个审批引擎、工作流引擎、Saga 或长期兼容双写。

## 3. 已确认的架构选择

采用新的 `handlers/intake` 垂直切片和事务感知的专业 Creator Registry：

```text
ITSM Web / existing professional API / KAF typed client
  -> POST /api/v1/intake/work-items
  -> authenticated Intake Actor
  -> handlers/intake Application Service
       -> normalize command and digest
       -> claim idempotency
       -> validate Catalog / CTI / CI / form / permission
       -> resolve authoritative recordClass and workflow binding
       -> lookup ProfessionalCreator
       -> one PostgreSQL transaction
            WorkItem base
            professional extension
            FieldValue and CI relation
            Intake resolution snapshot
            audit
            workflow-start Outbox
            completed idempotency receipt
  -> commit
  -> existing Outbox worker starts the one authoritative BPMN process
```

不采用以下方案：

- **包装现有创建 API 的 Facade**：现有服务包含独立事务、提交后写入和 goroutine，Facade 无法提供完整原子性，并会保留重复规则。
- **异步命令总线或 Saga**：首期权威写入均在同一 PostgreSQL 中，Saga 会制造不必要的部分状态和补偿协议。
- **服务账号加调用方 `requesterId`**：无法证明真实用户，允许身份伪造，也会把审计主体错误地记录为技术账号。

## 4. 权威边界

- Intake 只拥有创建编排，不拥有 Incident 或 Service Request 的后续状态机。
- WorkItem 是标题、描述、`recordClass`、状态、优先级、requester、opener、assignee、tenant、公共时间戳、SLA、工作流引用和审计关联的唯一权威来源。
- Incident 与 Service Request 扩展只保存专业字段。Intake 切换所涉及的遗留公共字段必须在同一实施增量中迁移读取并停止复制；不得用长期双写维持两套权威数据。
- `recordClass` 在专业扩展存在后不可变。分类不是把 Incident 转成 Problem 或 Change。
- Catalog Item 是服务定义，不是 WorkItem。Catalog 请求的 `recordClass` 必须从 Catalog Item 的权威 `target_class` 取得，调用方不能覆盖。
- BPMN 是审批、履约、SLA 升级和自动化编排的唯一权威引擎。Intake 只可靠请求启动，不自行解释或推进流程。
- KAF 负责意图理解、字段收集与用户确认；ITSM 负责确定性校验、权限、事务、持久化和工作流绑定。
- KAF 的 task-scoped automation token 只用于已委派任务，不能用于 Intake 创建。

## 5. 命令与响应契约

### 5.1 创建命令

统一入口为：

```http
POST /api/v1/intake/work-items
```

概念契约：

```ts
type CreateWorkItemCommand = {
  idempotencyKey: string
  intakeKind: "catalog_item" | "incident"
  title: string
  description?: string
  catalogItemId?: number
  cti?: {
    categoryId?: number
    typeId?: number
    itemId?: number
  }
  ciIds?: number[]
  formValues?: Record<string, unknown>
  sourceReference?: {
    provider: string
    eventId: string
    conversationId?: string
  }
  incident?: {
    severity?: "low" | "medium" | "high" | "critical"
    impact?: "low" | "medium" | "high" | "critical"
    urgency?: "low" | "medium" | "high" | "critical"
    detectedAt?: string
  }
}
```

约束：

- `idempotencyKey` 必填、长度受限，并在调用方首次确认创建时生成；网络重试不得更换。
- JSON object key 按字典序、时间按 UTC、字符串按合同规定的 trim 规则规范化；语义为集合的 CI ID 去重排序，表单数组保持用户顺序。digest 算法和规范化版本必须固定并进入测试，不能依赖 Go map 遍历顺序。
- `catalog_item` 必须提供 `catalogItemId`；最终 `recordClass` 由该 Catalog Item 决定。
- `incident` 只能产生 `incident`；Catalog 指向 Incident 时也通过同一个 `IncidentCreator` 创建。
- 命令不能包含 `tenantId`、`actorId`、`requesterId`、role 或授权结论；出现这些字段时返回明确错误，不能静默采用。
- `sourceReference` 只记录经过清理的渠道事实，不能作为身份来源，也不能包含聊天正文、token 或 secret。
- `sourceReference.provider` 必须与认证 Identity 中的 channel/provider 一致；不一致时拒绝，不能用于改写 channel。
- 渠道适配器在调用前完成用户确认或受配置治理的自动创建判断。ITSM 根据认证/渠道策略记录该事实，不信任普通 payload 中的自报确认。
- 动态字段只通过权威 FieldValue 边界持久化；不再同时把同一字段保存在专业扩展 JSON 中。

### 5.2 响应

```ts
type CreateWorkItemResult = {
  workItemId: number
  number: string
  recordClass: "service_request_item" | "incident"
  professionalReference: { type: string; id: number }
  workflowStartStatus: "not_required" | "pending" | "active" | "manual_intervention_required"
  replayed: boolean
}
```

首次创建返回 `201` 和 `replayed=false`。相同 key、相同 digest 的重放返回 `200` 和 `replayed=true`，并从 `work_item_id` 加载权威结果，不保存一份可能漂移的完整响应 JSON。

## 6. 身份交换与授权

### 6.1 ITSM Web 与现有 API

现有 ITSM JWT 继续作为权威认证来源。Intake 从中派生稳定内部 user、tenant、role 和权限；首期 requester 与认证用户相同。现有专业 API Handler 可以保留公共请求形状，但传入 Intake 前必须丢弃或拒绝调用方身份字段，并从认证上下文构造 Identity。ITSM Web 和 API client 在首次提交时生成稳定幂等键；统一入口从 JSON 字段读取，保留的专业 URL 从必填 `Idempotency-Key` header 读取。服务端不得为缺少 key 的请求临时生成随机值。

### 6.2 KAF 渠道

KAF Web、Teams 和 WeCom 使用 ITSM identity exchange，而不是复用 task-scoped KAF automation token：

```text
channel-verified external identity
  -> configured ITSM connector identity mapping
  -> ITSM verifies provider assertion or authenticated connector exchange
  -> short-lived JWT: aud=itsm-intake, scope=intake:create
  -> Unified Intake API
```

交换结果必须绑定 tenant、内部 user、channel、audience、scope、过期时间和可审计的 token ID。映射缺失、停用、歧义、跨租户或 provider assertion 无法验证时拒绝交换。KAF 不能通过 `employeeId`、邮箱、tenant 或请求头覆盖映射结果。

### 6.3 权限检查顺序

1. 验证 token、audience、scope、过期时间和 channel。
2. 建立 tenant/RLS 上下文。
3. 检查 `intake:create`。
4. 检查目标专业创建权限。
5. 检查 Catalog 可见性、CTI/CI 行权限和表单访问权限。
6. 在同一 tenant 下解析工作流和 SLA 配置。

资源不存在与跨租户不可见统一返回不泄露存在性的 `404`。隐藏菜单永远不替代后端授权。

## 7. 组件边界

### 7.1 `handlers/intake`

- `Handler`：绑定命令、读取认证上下文、调用 Application Service、映射稳定 HTTP 错误。
- `ActorResolver`：产生不可伪造的 Intake Identity。
- `ApplicationService`：规范化、幂等、引用解析、事务和 Creator 编排。
- `IdempotencyRepository`：在调用方事务中 claim、比较 digest 并完成收据。
- `ReferenceValidator`：验证 Catalog、CTI、CI、动态表单、版本和 tenant 范围。
- `CreatorRegistry`：按权威 `recordClass` 注册和查找专业 Creator。
- `WorkflowBindingResolver`：按 Catalog/tenant/recordClass/场景解析唯一流程或显式 `no_process`。
- `OutboxRepository`：复用现有 Outbox，在调用方 `*ent.Tx` 中插入启动事件。

Handler 不执行业务规则，Application Service 不调用外部 HTTP，Repository 不反向调用 Handler 或专业 Controller。

### 7.2 Creator Registry

概念接口：

```go
type ProfessionalCreator interface {
    RecordClass() workitem.RecordClass
    Prepare(ctx context.Context, tx *ent.Tx, in ResolvedIntake) (*CreationPlan, error)
    CreateExtension(
        ctx context.Context,
        tx *ent.Tx,
        workItem *ent.Ticket,
        plan *CreationPlan,
    ) (*ProfessionalReference, error)
}
```

- 首期只注册 `ServiceRequestItemCreator` 与 `IncidentCreator`。
- `Prepare` 只读取当前事务可见的权威配置并生成不可变计划；计划包含共享 WorkItem 草稿、专业输入和版本化解析结果。
- Intake 创建 WorkItem 基础记录；Creator 只创建自己的专业扩展和创建期专业事件。
- Creator 必须使用传入的 `*ent.Tx`，不能嵌套开启事务。
- Creator 不启动 goroutine、不直接调用 BPMN、KAF、Graph、邮件或其他外部系统。
- 重复注册、未知 `recordClass`、不匹配的专业 payload 或不支持的 Catalog target 均显式失败。
- IncidentService 和 ServiceRequestService 继续拥有后续专业转换；Registry 不能演变成覆盖所有生命周期的巨大 Service 或 switch。

## 8. 数据模型

### 8.1 `intake_requests`

`intake_requests` 是创建命令的幂等收据，不是第二个 WorkItem 数据源。

| 字段 | 语义 |
|---|---|
| `id` | Intake 请求 ID |
| `tenant_id` | tenant/RLS 边界 |
| `actor_id` | 经认证的内部操作者 |
| `channel` | `itsm_web`、`kaf_web`、`teams`、`wecom` 等 |
| `operation` | 首期固定 `create_work_item` |
| `idempotency_key` | 调用方稳定重试键 |
| `request_digest` | 规范化命令的 SHA-256 |
| `status` | `processing`、`completed` |
| `work_item_id` | 完成后关联唯一 WorkItem |
| `created_at` / `completed_at` | 审计与指标时间 |

唯一约束：

```text
(tenant_id, actor_id, channel, operation, idempotency_key)
```

`processing` 只存在于尚未提交的创建事务内；提交前必须变成 `completed` 且设置 `work_item_id`，数据库约束禁止提交缺少 WorkItem 的 completed 收据。校验、Creator 或 Outbox 写入失败时整条收据随事务回滚，不保存误导性的失败记录。同 key、同 digest 返回既有 WorkItem；同 key、不同 digest 返回 `409 IdempotencyConflict`。

claim 使用 `INSERT ... ON CONFLICT DO NOTHING RETURNING` 或等价、不会使当前事务进入 aborted 状态的实现。无返回行时读取冲突收据；PostgreSQL 会等待并发持有者提交或回滚，再得到 completed 收据或重新 claim。不能捕获一次普通唯一键异常后继续使用已经失败的事务，也不能依赖进程内锁。

### 8.2 `intake_resolution_snapshots`

Snapshot 是不可变的创建决策证据，不参与后续业务裁定。它至少关联唯一 `intake_request_id`、`work_item_id` 和 `tenant_id`，并记录：

- source/channel 与经过清理的外部事件引用；
- Catalog Item ID、配置版本、权威 `target_class`；
- CTI、Category、CI 引用及解析版本；
- 表单 schema 版本；
- 工作流与 SLA 绑定版本或显式 `no_process`；
- Intake resolver/rules 版本、`request_digest` 和解析时间。

Snapshot 不复制标题、描述、状态、优先级、requester 等 WorkItem 公共字段，不复制完整表单值、聊天记录、token、secret 或附件内容。允许的 JSONB 仅保存非权威、不可变且已脱敏的决策证据，并在代码中禁止业务双读。

### 8.3 既有模型收敛

- WorkItem 与专业扩展保持一对一，并在同一事务创建。
- Service Request 的结构化动态字段必须在该事务写入 FieldValue；提交后 warning-only 写入被删除。
- CI 关联和需要自动创建的 CI 必须通过 tx-aware repository 在该事务内完成；不能在事务前留下孤立 CI。
- Incident/Service Request 遗留公共字段的读取迁移到 WorkItem，创建路径停止双写；若 schema 仍强制要求这些字段，实施迁移必须在 Creator 切换前解除约束或移除列。
- Catalog `target_class` 成为唯一权威持久化字段。遗留 `itsm_type` 只能作为迁移输入，回填验收后删除运行时双向同步和 fallback。
- 适用的 SLA 引用与创建期记录使用同一事务的 tx-aware port；若存在跨进程动作，只能通过同事务 Outbox 驱动。

所有新增表、外键、索引和查询必须带 tenant 边界及 PostgreSQL RLS policy。后台 worker 每次处理前显式建立并在归还连接前清理 tenant context。

## 9. 事务与幂等流程

1. Handler 验证认证材料，ActorResolver 产生 Identity。
2. Application Service 规范化命令，拒绝未知字段和身份字段，计算 `requestDigest`。
3. 开启一个 Ent/PostgreSQL 事务。
4. 插入 `intake_requests(processing)` 以 claim 幂等键。
5. 唯一冲突时读取既有收据：digest 相同且 completed 则返回原 WorkItem；digest 不同则返回 `409`；并发中的事务等待对方提交或回滚后重新判断。
6. 在事务内验证用户状态、Catalog/CTI/CI/表单、tenant、权限和配置版本。
7. 解析权威 `recordClass`、工作流绑定、SLA 和不可变 CreationPlan。
8. 从 Registry 取得 Creator；未知类型 fail closed。
9. 创建 WorkItem 基础记录及适用的共享关系。
10. Creator 创建且只创建一个专业扩展及专业创建事件。
11. 写入 FieldValue、CI 关系、Intake Snapshot、审计和 workflow-start Outbox。
12. 将幂等收据更新为 completed 并关联 WorkItem。
13. 提交事务，返回 `201`。
14. Outbox worker 在提交后启动 BPMN；请求线程不等待外部引擎，也不启动 goroutine。

任一步失败都回滚第 4–12 步。编号生成允许出现未使用的号段，但不能因此产生缺少专业扩展的 WorkItem。

## 10. BPMN 启动与恢复

创建事务向既有 `outbox_events` 写入：

```text
eventType: workflow.start.requested
aggregateType: work_item
aggregateId: <workItemID>
dedupeKey: workflow-start:<workItemID>:<bindingVersion>
```

Payload 只包含 tenant、WorkItem、`recordClass`、冻结的流程定义/绑定版本、actor/source、`intakeRequestID` 和启动所需的最小非敏感变量。

可靠性合同：

1. Outbox 与 WorkItem 同事务提交。
2. Worker 复用现有 lease、指数退避、重试和故障恢复能力。
3. BPMN 以 `dedupeKey` 作为幂等 business key；同一 WorkItem/binding 只能得到一个流程实例。
4. BPMN 已启动但 ITSM 回写失败时，重试必须返回同一流程实例，再写权威 workflow execution/reference。
5. 未注册 event、handler、流程定义或不支持的 binding fail closed，不能标记投递成功。
6. 超过重试上限后进入 `manual_intervention_required`，记录脱敏错误、尝试次数、审计和告警。
7. 人工重试继续使用原 dedupe key，不能创建第二个流程。

`pending`、`retrying`、`active` 和 `manual_intervention_required` 是根据 Outbox 与权威 workflow reference 投影出的运行状态，不写入或替代 Incident/Service Request 的专业业务状态。配置明确为 `no_process` 时不创建 Outbox，并返回 `not_required`；缺少配置不能被推断为可选。

## 11. 错误契约

所有错误包含稳定 `code`、用户可读 `message`、`requestId`、`retryable`，表单错误另带 `fieldErrors`。响应不得泄露 SQL、表名、栈、跨租户资源、token 或 secret。

| HTTP | 示例 |
|---|---|
| `400` | 命令格式、未知字段、身份字段、幂等键或字段类型非法 |
| `401` | token 缺失、过期、签名或 audience 错误 |
| `403` | 缺少 Intake 或专业创建权限 |
| `404` | Catalog、CTI、CI 不存在或不可见 |
| `409` | 相同幂等键对应不同 digest、配置版本冲突 |
| `422` | 表单或专业领域规则校验失败 |
| `503` | 可重试的数据库/基础设施故障 |
| `500` | 未分类内部错误 |

事务提交后 BPMN 暂时不可用不改变建单响应为失败；结果返回 `workflowStartStatus=pending`，由 Outbox 可靠恢复。只有 Intake 事务本身未提交时，客户端才应以同 key、同 payload 重试创建。

## 12. 安全、审计与可观测性

- 对命令体、字符串、动态字段、CI 数量和 source reference 设置确定性上限。
- 审计至少记录真实 actor、tenant、requester、channel、认证客户端、Intake request、WorkItem、Creator、配置版本、结果和 correlation/request ID。
- KAF/AI 参与时额外记录自动化来源与版本，但不能把真实用户伪装成技术账号，也不能记录 prompt secret 或完整敏感内容。
- 人工重试、幂等结果查询、identity exchange 和 workflow intervention 都需要单独权限和审计。
- 日志只记录 ID、状态、耗时和脱敏错误摘要；不记录原始请求体、表单秘密、聊天正文或 JWT。
- 关键指标包括：
  - `intake_requests_total{channel,record_class,result}`；
  - `intake_duration_seconds`；
  - `intake_idempotency_replay_total` 与 `intake_idempotency_conflict_total`；
  - `workflow_start_outbox_pending`、最老事件年龄、重试与 dead/manual intervention 数；
  - identity exchange 成功、拒绝和映射失败数。
- 告警至少覆盖 Outbox 最老事件超阈值、连续 workflow start 失败、人工干预增长、identity mapping 异常和跨租户/RLS 拒绝激增。

## 13. 入口迁移

### 13.1 ITSM

1. 建立 Intake schema、Application Service、Registry、两个 Creator 和事务测试。
2. 提取 Incident/Service Request 创建规则到 Creator，并将公共字段、FieldValue、CI、SLA、Snapshot、审计和 Outbox 纳入同一事务。
3. 现有 `/incidents`、`/service-requests` Handler 保留必要的公共 API 形状，但只做 DTO 到 `CreateWorkItemCommand` 的薄转换，并要求 `Idempotency-Key` header。
4. 先更新仓库内 ITSM Web/API client 生成并在重试中复用 key，再对专业 URL 启用必填校验。ITSM Web 可继续调用专业 URL，也可改用 `/intake/work-items`；两者必须进入同一 Application Service。
5. 在每个入口切换的同一增量删除被替代的独立事务、提交后 FieldValue、BPMN goroutine、重复公共字段写入和运行时 Catalog fallback。

保留稳定公共 URL 是协议边界，不是保留第二套业务实现。不得长期 feature-flag 双写或让新旧 Service 各自创建 WorkItem。

### 13.2 KAF

KAF 新建与 Go/WorkItem 版 ITSM 对应的 typed Intake client，并保持与遗留 Gazellio `src/acp/itsm/` 命名空间隔离。它执行：

```text
verified channel identity
  -> ITSM identity exchange
  -> short-lived intake:create token
  -> CreateWorkItemCommand
  -> persist WorkItem result and replay state
```

- 稳定渠道消息或用户确认事件生成 `idempotencyKey`。
- 重试发送完全相同的 key 和规范化 payload。
- 不发送权威 `employeeId`、tenant、requester 或 role。
- 迁移 E2E 通过后，删除被本产品创建能力替代的旧 `ticket_create` Gazellio 直连、`cticode` Web Form 和“邮件已发即建单成功”降级路径；不删除与本次 Intake 无关的 Gazellio 只读或其他集成能力。
- 邮件通知可以作为通知，但不能作为权威 WorkItem 创建结果。
- KAF 已验收的 delegated task ledger、receipt、outbox 和 completion replay 不做迁移或复用。

## 14. 测试设计

### 14.1 单元测试

- 命令规范化顺序、空值语义和 digest 稳定性。
- ActorResolver、audience/scope/channel 校验及禁止身份注入。
- Catalog/CTI/CI/form/schema/version 校验。
- Creator 注册、重复注册、未知类型和 payload 不匹配 fail closed。
- 错误码与 `retryable` 映射。
- Snapshot 脱敏和禁止复制权威字段。

### 14.2 PostgreSQL 集成测试

- 在 WorkItem、扩展、专业事件、FieldValue、CI、Snapshot、audit、Outbox、receipt 每一步注入失败，证明全部回滚。
- 相同 key 的并发请求只生成一个 WorkItem、一个专业扩展、一个 Snapshot 和一个 Outbox。
- 相同 key/相同 digest 精确重放；相同 key/不同 digest 冲突。
- Catalog 版本变化、无效 target、未知 workflow binding 明确失败。
- 跨租户 Catalog/CTI/CI、幂等收据和 WorkItem 不可见。
- 连接池复用后 tenant context 不泄漏；真实 `RLS_TEST_DSN` 套件零 skip。
- WorkItem 与每种专业扩展的一对一约束和不可变 `recordClass`。

### 14.3 Outbox/BPMN

- 重复投递只启动一个 BPMN 实例。
- BPMN 成功、ITSM 回写失败后的同实例恢复。
- lease 超时、worker 崩溃和多 worker 竞争。
- 未注册 event/handler/process 不被确认。
- 重试耗尽进入人工干预，人工重试保持同 dedupe key。
- 显式 `no_process` 不发事件；配置缺失不静默跳过。

### 14.4 API 与 E2E

- ITSM Web 创建 Catalog Service Request。
- ITSM Web 创建 Incident。
- 现有专业 API 与新 Intake API 产生相同权威领域结果。
- KAF 身份交换、用户确认、创建和精确重放。
- KAF 伪造 employee/tenant/requester 被拒绝。
- BPMN 暂停期间 WorkItem 可见且显示 pending，恢复后自动 active。
- PostgreSQL RLS、RBAC、配置版本、payload 上限和日志脱敏。
- ITSM 全量 Go test/build、相关前端测试/build，以及 KAF typed client focused suite。

## 15. 分阶段交付与门禁

1. **Foundation**：schema、RLS、identity/command、幂等、Snapshot、Registry 和两个 Creator；完成原子性与并发测试。
2. **ITSM cutover**：现有 Incident/Service Request API 切入 Intake；同阶段删除旧创建逻辑和公共字段双写；完成全量回归。
3. **KAF cutover**：identity exchange、typed client 和真实 Dev E2E；通过后删除旧直连/Web Form/邮件成功降级。
4. **Operational hardening**：Outbox 告警、人工干预入口、运行指标和操作手册。

这些阶段是验收关口，不是长期并行实现。每一阶段只有在本阶段替代路径已经删除、迁移可回滚、测试证据可复现时才完成。任何实施中发现需要第二套审批/BPMN、请求体身份、跨事务业务 goroutine、无处理器静默成功或长期双写的情况，都必须停止并重新评审。

## 16. 完成标准

1. ITSM Web、现有 Incident/Service Request API 和 KAF 创建最终调用唯一 `CreateWorkItemCommand` Application Service。
2. 并发精确重放只产生一个 WorkItem、一个专业扩展、一个 Snapshot、一个启动 Outbox 和一个 BPMN 实例。
3. 任意事务内故障不留下孤立 WorkItem、扩展、CI、FieldValue、审计或幂等收据。
4. tenant、actor、requester、channel 和权限不能由请求体伪造。
5. Catalog `target_class` 是唯一权威；未知 recordClass、事件或 workflow binding 明确失败。
6. Incident/Service Request 公共字段只由 WorkItem 持有，不保留长期双写。
7. 无提交后业务 goroutine，无 warning-only 关键写入，无第二套专业生命周期。
8. Outbox/BPMN 在所有重试与崩溃窗口保持单实例，并可见地进入 pending、active 或人工干预。
9. 指标与审计能关联 channel、Intake request、WorkItem、workflow 和真实 actor，且不泄露敏感内容。
10. PostgreSQL RLS 零 skip，ITSM 全量测试/build、相关前端验证和 KAF typed client focused suite 通过。
11. 既有 KAF 委派 ledger、receipt、Outbox 和 replay-only 合同保持不变。

## 17. 后续独立增量

本设计完成后，下一批工作仍按独立 spec → plan → implementation 周期推进：

1. Incident typed actions 与 `expectedVersion` 合同。
2. KAF Procedure 权威版本和执行模型收敛。
3. Unified Intake UI、Intake Session、附件 staging、知识分流和产品级多渠道状态反馈。
4. Problem、Change、Catalog Task 等新的 ProfessionalCreator。

这些后续项不得倒灌到首期 Unified Intake 实施计划。
