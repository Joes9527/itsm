# KAF 自主 WorkItem 受理与 BPMN 委派设计

> 状态：Draft for review
> 日期：2026-08-28
> 范围：KAF 与 ITSM 的统一受理、WorkItem 创建、BPMN 委派和自动化处理主路径
> 驱动验收：SSLVPN 权限申请与 SSLVPN 连接故障

## 1. 目标

KAF 是终端用户的主要体验层，负责多渠道对话、自然语言理解、CTI/Catalog/`recordClass` 识别、Procedure 选择和受治理 Tool 执行。ITSM 是 WorkItem、专业生命周期、BPMN、SLA、权限和审计的权威系统。

在不禁用 ITSM 直接建单的前提下，统一链路为：

```text
KAF 或 ITSM 直接入口
  -> ITSM Intake 创建 WorkItem
  -> ITSM BPMN 创建 AutomationTask 并委派 KAF
  -> KAF 获取 WorkItem 并自主选择 Procedure/Tools
  -> KAF 回报进度和完成结果
  -> ITSM 校验动作、更新 WorkItem 并推进 BPMN
```

首期覆盖 `service_request_item` 与 `incident`。Problem、Change、人工接管、用户追问恢复、审批恢复和复杂补偿编排不在本次范围。

## 2. 决策与边界

### 2.1 统一认证、身份和入口

KAF 与 ITSM 复用同一认证、身份、租户解析和 RBAC 体系；KAF 适配 ITSM 的认证协议。KAF 不是独立租户，也不以请求体提供的用户、租户或角色作为授权依据。

| 入口 | 行为 | ITSM 认证主体 |
|---|---|---|
| KAF Web / Teams / WeCom | KAF 对话式收集需求并调用 ITSM Intake；Web 必须经用户确认，Teams/WeCom 可按渠道策略自动创建。 | 当前终端用户 |
| ITSM 终端用户页面 | 用户留在 ITSM。可手动选择 Catalog/填写表单，或在当前页面调用 KAF 分析能力；页面不推导领域语义。 | 当前终端用户 |
| ITSM 坐席/管理员页面 | 允许专业人员明确创建 WorkItem；创建后如 BPMN 到达 KAF 委派节点，仍进入同一 KAF 处理链路。 | 当前操作人员 |
| KAF 自动执行 | KAF 领取 ITSM 委派任务并调用 ITSM 自动化 API。 | ITSM 内置 `KAF automation` 技术账号 |

创建时，当前认证用户是 requester/opener 的权威来源；`source` 仅记录渠道事实。自动执行时，`KAF automation` 是执行 actor，原 requester 保留在 WorkItem 上。审计必须同时记录技术账号、KAF agent、Procedure、run 和步骤，不能将自动执行伪装为终端用户操作。

所有入口使用同一 ITSM Intake 应用服务。前端不得使用正则、预设名称或用户优先级来推断 `recordClass`。

### 2.2 智能决策与领域裁定

KAF 负责智能决策：

- 从自然语言识别 CTI、Catalog 和 `recordClass`；
- 收集和标准化字段；
- 在自身按租户隔离的 Procedure/Tool Registry 中选择实际执行的 Procedure；
- 在已有 Tool 治理边界内调用 Tools；
- 决定要请求 ITSM 执行的 WorkItem 动作。

ITSM 不重新使用关键词或 AI 分类器推理同一语义。ITSM 负责确定性裁定：

- 从认证上下文解析 tenant、actor 和 RBAC，不信任外部提交的身份或租户；
- 验证 CTI、Catalog、CI 的租户归属、可见性、表单与并发版本；
- Catalog 存在时，要求其 `targetClass` 与 KAF 提交的 `recordClass` 一致；Catalog 缺失时，按 KAF 指定的 `recordClass` 创建；
- 按既有 `recordClass` 路由到对应专业领域服务；
- 验证 BPMN 当前任务和专业生命周期是否允许请求的动作；
- 原子写入 WorkItem、专业扩展、流程状态、SLA 和审计。

KAF 可以更新、推进、解决和关闭 WorkItem，但不能直接写 ITSM 数据库或绕过 BPMN、IncidentService、ServiceRequestService 等领域规则。

### 2.3 BPMN、Procedure 与自动化资格

BPMN 不绑定 KAF Procedure，也不复制 KAF 的 Tool 权限、风险策略或审批治理。BPMN 只表达：当前流程任务委派给 KAF 处理。

ITSM 通过配置化流程绑定决定哪些 WorkItem 产生 KAF 委派任务。例如 `recordClass=incident` 且 CTI 为 SSLVPN 连通性故障时，绑定 SSLVPN 自动诊断流程；未匹配该绑定的 Incident 维持既有人工流程。KAF 不能自行启动、跳过或完成未经委派的 BPMN 自动化任务。

KAF 收到委派后读取 WorkItem，结合当前 CTI、Catalog、表单、CI、历史、BPMN 上下文和租户知识自主选择 Procedure。KAF 的 Tool Registry 与治理机制是 Procedure/Tool 可选范围、风险控制和 Tool 调用的唯一权威来源。ITSM 仅保存最终的 `procedureRef`、版本和脱敏执行证据，不维护第二套 Procedure 或 Tool policy。

ITSM BPMN 仍是流程状态、审批、超时和 WorkItem 生命周期的唯一权威来源。KAF 完成工作后请求 ITSM 落地动作；ITSM 返回最新状态和结构化拒绝原因。

## 3. 状态与数据归属

```text
创建前：KAF Intake Session
  对话、候选、待补字段、确认状态、AI 决策草稿

创建事务：ITSM Intake
  WorkItem、专业扩展、受理快照、SLA、BPMN、审计

委派事务：ITSM AutomationTask
  BPMN 服务任务关联、领取状态、租约、run、幂等和 Outbox 事件

执行中：KAF Execution Context
  taskId、runId、stepId、Procedure/Tool 执行遥测与幂等键
```

ITSM 在 Intake 创建时冻结受理快照：CTI、Catalog 版本、已确认表单、`recordClass`、来源渠道和 KAF 的受理决策。KAF 在开始自动化执行时冻结执行快照：实际选中的 Procedure、版本、输入上下文摘要、模型/提示词版本、置信度、`runId` 和 `stepId`。

KAF 不保存 WorkItem 状态副本；每次展示或执行前读取 ITSM 返回的当前版本和可执行动作。Procedure 选择不在 Intake 时冻结，因为它由 KAF 在 BPMN 委派时基于最新的 WorkItem 上下文决定。

### 3.1 AutomationTask

`AutomationTask` 是 ITSM 持久化的 BPMN 服务任务执行记录，也是 KAF 自动化动作的唯一授权上下文。每条任务至少关联 tenant、WorkItem、BPMN process/task、状态、`correlationId`、当前 `runId`、lease 持有者和到期时间、尝试次数、版本、创建/完成时间。

首期状态机为：

```text
ready -> running -> completed
  ^        |
  |        v
  +--- retryable
```

ITSM 创建任务、写入审计和 Outbox 必须在同一事务中完成。KAF 以 `runId` 领取 `ready` 或 `retryable` 任务并持有可续租 lease；同一任务同一时间只能有一个有效 run。lease 过期后任务转为 `retryable`，可被新 `runId` 重领。人工接管、长期失败编排和补偿流程不在首期状态机范围。

每个实际 ITSM 动作以 `tenantId + taskId + runId + stepId` 为幂等边界。重复提交返回已应用结果而非重复写入；语义动作不得在未刷新上下文时盲重试。

## 4. 接口契约

### 4.1 创建 WorkItem

`CreateWorkItemCommand` 由当前认证主体调用。requester、tenant、actor 和权限由 ITSM 认证上下文派生，不接受调用方任意指定；渠道适配层只提交渠道事实、受理内容与 KAF 的结构化决策。

```ts
type CreateWorkItemCommand = {
  source: "itsm_web" | "teams" | "wecom"
  confirmation: "confirmed" | "channel_auto_create"
  intent: {
    recordClass: WorkItemRecordClass
    cti: { categoryId?: string; typeId?: string; itemId?: string }
    catalogItemId?: string
    ciIds?: string[]
  }
  content: {
    title: string
    description: string
    formData: Record<string, unknown>
    attachmentRefs?: AttachmentRef[]
  }
  aiDecision?: {
    confidence: number
    model: string
    promptVersion: string
    rationale: string
    conversationRef?: string
  }
}
```

ITSM 返回 `WorkItemResult`，至少包括 `id`、`number`、`recordClass`、`status`、CTI、Catalog、当前 BPMN/SLA 摘要、版本和详情链接。Catalog/CTI/CI 不可见、Catalog `targetClass` 冲突、表单无效或确认策略不满足时，返回机器可读的校验错误；这不是 ITSM 的二次分类。

### 4.2 委派事件、任务领取与补偿拉取

当 BPMN 到达 KAF 委派节点时，ITSM Outbox 发布：

```ts
type KafDelegateRequested = {
  eventId: string
  ticketId: string
  taskId: string
  correlationId: string
}
```

事件不携带完整工单正文。KAF 使用 `taskId` 以 `KAF automation` 身份获取任务上下文，得到当前 WorkItem、冻结受理快照、当前 BPMN 等待点、允许动作、版本和 lease 状态。事件推送是主路径；KAF 重启或事件遗漏时，通过 `GET /automation-tasks?status=ready|retryable` 补拉自身可领取的未完成任务。

领取、续租和查询接口都以 `taskId` 为对象。ITSM 必须验证该技术账号对任务所属 tenant 的自动化权限；返回的上下文仅限完成该任务所需的 WorkItem 数据。

### 4.3 任务绑定的 typed action contract

除 Intake 创建外，KAF 对既有 WorkItem 的 `update_progress`、`assign`、`resolve`、`close`、`complete_bpmn_task` 和 `record_execution_failure` 必须关联一个有效 `taskId`。每个动作都包含 `expectedVersion`、`runId`、`stepId`、幂等键、`correlationId`、Procedure/version 和任务专属的 typed payload；不使用无约束的 `Record<string, unknown>` 作为领域动作负载。

```ts
type AutomationActionBase = {
  taskId: string
  workItemId: string
  expectedVersion: number
  execution: {
    procedureRef: string
    procedureVersion: string
    runId: string
    stepId: string
    idempotencyKey: string
    correlationId: string
  }
}

type CompleteAutomationTask = AutomationActionBase & {
  action: "complete_bpmn_task"
  payload: { resultSummary: string; evidenceRefs: string[] }
}

type ResolveIncident = AutomationActionBase & {
  action: "resolve"
  payload: {
    resolutionCode: string
    resolutionSummary: string
    evidenceRefs: string[]
  }
}
```

`complete_bpmn_task` 必须明确完成 `taskId` 所关联的 BPMN 服务任务。`resolve` 和 `close` 先由 IncidentService 或对应专业领域服务校验，再按任务定义决定是否完成 BPMN 任务；该关联在同一事务内落地，不能由 KAF 假设隐式推进。

ITSM 按 WorkItem 的既有 `recordClass` 调用对应专业领域服务。这是确定性路由，不是重新智能分类。动作结果必须区分 `applied`、`already_applied`、`stale_version`、`task_not_active`、`lease_lost`、`forbidden` 与 `domain_rejected`；KAF 刷新上下文并由自身治理策略决定后续行为。

## 5. 数据治理与审计

ITSM WorkItem 时间线只保存脱敏的执行摘要、Procedure/版本、Tool 审计引用、动作结果和 `correlationId`。原始模型输入输出、敏感工具输出和完整 Langfuse trace 留在 KAF；KAF 按同租户 RBAC、脱敏和保留期策略管理它们。

ITSM 不复制原始 prompt、完整对话或 Tool 敏感输出。KAF 返回的证据引用必须可审计但不应泄露凭据、令牌、密码或受保护内容。 `correlationId` 用于关联 KAF session、Langfuse trace、WorkItem、BPMN、AutomationTask、KAF run/step 和 ITSM 审计，而不是绕过权限读取数据的凭据。

## 6. 首期专业域行为

| 专业域 | 委派资格与 KAF 行为 | ITSM 动作落点 |
|---|---|---|
| `service_request_item` | 已获批且 BPMN 到达 KAF 委派节点后，KAF 读取服务申请，自主选择开通 Procedure，调用受治理 Tool 并回报进度。 | `complete_bpmn_task` 完成委派任务；BPMN 和 ServiceRequestService 判断 Requested Item 是否完成。 |
| `incident` | 仅命中配置化自动诊断流程绑定的 Incident 才委派。KAF 诊断、检索知识、选择修复 Procedure、调用受治理 Tool 并形成解决证据。 | `update_progress` 与 `resolve` 由 IncidentService 校验；关闭遵循既有关闭规则。 |

## 7. SSLVPN 驱动场景与 BPMN 迁移

### 7.1 SSLVPN 权限申请：Service Request

1. 用户通过 KAF 或 ITSM 直接入口提交 SSLVPN 远程访问权限申请，包含既有 8 个动态字段。
2. ITSM 创建 `service_request_item`，保存受理快照，启动 SSLVPN 双级审批 BPMN 和 SLA。
3. 部门领导与 L2 网络运维完成既有审批。
4. 新版 BPMN 随后创建 KAF Delegate `AutomationTask`，并发布 `KafDelegateRequested`。
5. KAF 领取任务、获取 WorkItem，自主选择 VPN 开通 Procedure，执行受治理 Tool，并将步骤进度和审计引用写回 ITSM。
6. KAF 提交 `CompleteAutomationTask`；ITSM 原子完成任务、记录审计，并按 BPMN/ServiceRequestService 规则推进 Requested Item。

### 7.2 SSLVPN 无法连接：Incident

1. 用户通过 KAF 或 ITSM 直接入口描述 SSLVPN 无法连接。
2. KAF 识别 `recordClass=incident`；ITSM Intake 校验引用后创建 Incident WorkItem 和专业扩展。
3. ITSM 的 SSLVPN 连通性 Incident 流程绑定命中后，BPMN 创建并委派 `AutomationTask`。
4. KAF 领取任务，执行知识检索、诊断和受治理修复 Tool，并回报进度与脱敏证据。
5. 满足 IncidentService 规则时，KAF 提交 `ResolveIncident`；ITSM 记录实际动作、审计和最新 Incident 状态。

### 7.3 流程版本策略

现有 SSLVPN 双级审批 BPMN 在二级审批后直接结束。首期应发布包含 KAF 委派节点的新版本；新创建的 SSLVPN 请求绑定新版本，已运行实例继续按旧版本结束，不做运行中迁移。新版本回滚时只影响后续创建实例，既有实例按其启动版本执行。

## 8. 验收与测试边界

1. SSLVPN 权限申请可从 KAF 与 ITSM 直接入口创建，并形成同样的 `service_request_item`、8 项字段、SLA 和审批 BPMN。
2. SSLVPN 双级审批完成后，BPMN 原子创建 `AutomationTask`、审计与 Outbox；KAF 能以 `taskId` 读取对应 WorkItem，而无需旧 ITSM 全文轮询。
3. KAF 选中的 Procedure、版本、Tool 审计引用、进度和最终动作均可在 KAF 执行记录与 ITSM WorkItem 时间线中追溯，且 ITSM 不保存敏感原始内容。
4. 同一 `tenantId + taskId + runId + stepId` 的重放不会重复落地动作；lease 到期后的新 run 可安全重领任务。
5. SSLVPN 无法连接可创建 `incident`；只有命中流程绑定时才委派 KAF，且 IncidentService 是 `resolve` 的唯一状态校验入口。
6. 每条验收链路均可用同一 `correlationId` 关联 KAF session、Langfuse trace、WorkItem、BPMN 任务、AutomationTask、KAF run/step 和审计记录。
7. 自动化 API 测试必须覆盖 tenant/RBAC 拒绝、任务非活跃、lease 丢失、版本冲突、重复事件与重复动作；KAF/ITSM 合同测试使用确定性 KAF stub，真实 Tool 仅在隔离 SSLVPN 沙箱做端到端验证。
8. KAF 仅通过受控 ITSM API 读取受委派 ticket 和写入动作；不存在直接数据库访问或前端领域语义推导。

## 9. 迁移与非目标

现有 `sr_batch` 为旧 ITSM 缺少可靠 CRUD API 时形成的适配链路。首期以 SSLVPN 服务请求验证新链路后，逐步将 `sr_batch` 的执行台账、幂等和 Tool 审计能力迁入 KAF Execution Context 与 ITSM WorkItem 审计；不保留旧工单全文重分类、Web 表单提交、`cticode` 映射或轮询恢复作为长期兼容路径。

首期不实现人工接管、用户追问恢复、审批恢复、长期失败补偿、Problem、Change 或复杂失败编排。这些作为后续 backlog 单独设计，避免将人工协作状态机混入 KAF 自主执行主路径。
