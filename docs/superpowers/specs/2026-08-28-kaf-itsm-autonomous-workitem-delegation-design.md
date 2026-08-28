# KAF 自主 WorkItem 受理与 BPMN 委派设计

> 状态：Draft for review
> 日期：2026-08-28
> 范围：KAF 与 ITSM 的统一受理、终端用户/坐席/管理员 UI、WorkItem 创建、BPMN 委派和自动化处理主路径
> 驱动验收：SSLVPN 权限申请与 SSLVPN 连接故障

## 1. 目标

KAF 是终端用户的主要体验层，负责多渠道对话、自然语言理解、CTI/Catalog/`recordClass` 识别、Procedure 选择和受治理 Tool 执行。ITSM 是 WorkItem、专业生命周期、BPMN、SLA、权限和审计的权威系统。

在不禁用 ITSM 直接建单的前提下，统一链路为：

```text
KAF 或 ITSM 直接入口
  -> ITSM Intake 创建 WorkItem
  -> ITSM BPMN 创建 kaf_delegate ProcessTask 并通知 KAF
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
| ITSM 终端用户页面 | 用户留在 ITSM 的统一自然语言受理入口；当前页面调用 KAF 分析能力并展示提交确认，页面不推导领域语义。Catalog 浏览仅是可选捷径。 | 当前终端用户 |
| ITSM 坐席/管理员页面 | 允许专业人员明确创建 WorkItem；创建后如 BPMN 到达 KAF 委派节点，仍进入同一 KAF 处理链路。 | 当前操作人员 |
| KAF 自动执行 | KAF 接收 ITSM 委派任务并调用任务范围自动化 API。 | ITSM 内置 `KAF automation` 技术账号 |

创建时，当前认证用户是 requester/opener 的权威来源；`source` 仅记录渠道事实。自动执行时，`KAF automation` 是执行 actor，原 requester 保留在 WorkItem 上。审计必须同时记录技术账号、KAF agent、Procedure、run 和步骤，不能将自动执行伪装为终端用户操作。

所有入口使用同一 ITSM Intake 应用服务。终端用户不必先选择 Catalog、分类或专业域；KAF 生成结构化受理结果，用户确认后提交。前端不得使用正则、预设名称或用户优先级来推断 `recordClass`。坐席/管理员可在其权限范围内显式选择专业域和分类。

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

ITSM 通过配置化流程绑定决定哪些 WorkItem 产生 KAF 委派任务。绑定优先级为：Catalog Item 明确的 `processDefinitionKey`；`tenant + recordClass + scenario + CTI/category/department` 精确绑定；`tenant + recordClass` 默认绑定；无匹配时执行明确配置的“无需流程”或返回阻断错误。禁止静默选择其他流程。例如 `recordClass=incident` 且 CTI 为 SSLVPN 连通性故障时，绑定 SSLVPN 自动诊断流程；未匹配该绑定的 Incident 维持既有人工流程。KAF 不能自行启动、跳过或完成未经委派的 BPMN 自动化任务。

KAF 委派节点是注册的 BPMN `serviceTask` 类型，`taskType="kaf_delegate"`。节点只声明委派语义和允许的 WorkItem 动作，不声明 Procedure、Tool 或 KAF 风险策略。流程到达节点时，ITSM 创建现有 `ProcessTask`、写入审计与 Outbox，并停在该节点；KAF 最终完成关联 `ProcessTask` 后，BPMN 才沿出边继续。

KAF 收到委派后读取 WorkItem，结合当前 CTI、Catalog、表单、CI、历史、BPMN 上下文和租户知识自主选择 Procedure。KAF 的 Tool Registry 与治理机制是 Procedure/Tool 可选范围、风险控制和 Tool 调用的唯一权威来源。ITSM 仅保存最终的 `procedureRef`、版本和脱敏执行证据，不维护第二套 Procedure 或 Tool policy。

ITSM BPMN 仍是流程状态、审批、超时和 WorkItem 生命周期的唯一权威来源。KAF 完成工作后请求 ITSM 落地动作；ITSM 返回最新状态和结构化拒绝原因。

## 3. 状态与数据归属

```text
创建前：KAF Intake Session
  对话、候选、待补字段、确认状态、AI 决策草稿

创建事务：ITSM Intake
  WorkItem、专业扩展、受理快照、SLA、BPMN、审计

委派事务：ITSM kaf_delegate ProcessTask
  BPMN 等待点、允许动作、审计和 Outbox 事件

执行中：KAF Execution Context
  taskId、runId、stepId、Procedure/Tool 执行、重试遥测与幂等键
```

ITSM 在 Intake 创建时冻结受理快照：CTI、Catalog 版本、已确认表单、`recordClass`、来源渠道和 KAF 的受理决策。KAF 在开始自动化执行时冻结执行快照：实际选中的 Procedure、版本、输入上下文摘要、模型/提示词版本、置信度、`runId` 和 `stepId`。

KAF 不保存 WorkItem 状态副本；每次展示或执行前读取 ITSM 返回的当前版本和可执行动作。Procedure 选择不在 Intake 时冻结，因为它由 KAF 在 BPMN 委派时基于最新的 WorkItem 上下文决定。

### 3.1 KAF 委派 ProcessTask

`kaf_delegate` 复用现有 `ProcessTask`，而不是引入第二套 `AutomationTask` 实体或状态机。`ProcessTask` 是 ITSM/BPMN 的等待点，至少关联 tenant、WorkItem、BPMN process/task、`delegated` 状态、允许动作、`correlationId`、创建/完成时间和版本。

ITSM 在同一事务内创建 `ProcessTask`、审计和 Outbox。KAF 的 `runId`、重试、Tool 执行和内部并发协调属于 KAF Execution Context，不由 ITSM 以 lease 或独立自动化任务状态管理。KAF 重复提交 ITSM 动作时，以 `tenantId + taskId + runId + stepId` 为幂等边界；重复提交返回已应用结果而非重复写入。

`KAF automation` 是全局 ITSM 内置自动化主体，但只拥有任务范围权限：可补拉被委派的 `kaf_delegate` 任务、通过 `taskId` 读取其 ticket 上下文，以及提交该任务声明允许的动作。ITSM 始终以关联 `ProcessTask.tenantId` 和 `taskType` 强制租户及任务类型隔离；KAF 不能调用通用 WorkItem 查询或修改 API。

## 4. 接口契约

### 4.1 创建 WorkItem

`CreateWorkItemCommand` 由当前认证主体调用。requester、tenant、actor 和权限由 ITSM 认证上下文派生，不接受调用方任意指定；渠道适配层只提交渠道事实、受理内容与 KAF 的结构化决策和创建幂等键。KAF Web 与 ITSM Web 只在用户点击确认后以 `confirmation="confirmed"` 调用 ITSM；ITSM 信任已认证的调用，不保存 KAF draft 或额外确认凭据。Teams/WeCom 的自动创建由 ITSM 已配置的渠道策略决定。

```ts
type CreateWorkItemCommand = {
  idempotencyKey: string
  source: "kaf_web" | "itsm_web" | "teams" | "wecom"
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

### 4.2 委派事件与任务范围 API

当 BPMN 到达 KAF 委派节点时，ITSM Outbox 发布：

事件不携带完整工单正文，但必须包含统一事件元数据：

```ts
type KafDelegateRequested = {
  eventId: string
  tenantId: string
  workItemId: string
  ticketId: string
  taskId: string
  recordClass: WorkItemRecordClass
  actor: ActorRef
  timestamp: string
  version: number
  correlationId: string
}
```

事件推送是主路径；KAF 重启或事件遗漏时，通过 `GET /bpmn/process-tasks/kaf-delegated?status=delegated` 补拉其有权处理的未完成任务。MVP 不提供 claim 或 lease API，KAF 自身负责执行协调。

任务范围 API 为：

- `GET /bpmn/process-tasks/{taskId}/kaf-context`：返回该任务关联的 WorkItem、冻结受理快照、当前 BPMN 等待点、允许动作和当前版本；
- `POST /bpmn/process-tasks/{taskId}/actions`：提交 typed action；`complete_bpmn_task` 成功时完成关联 `ProcessTask` 并推进 BPMN。

ITSM 必须验证 `KAF automation` 对关联 `ProcessTask` 的 tenant、类型和状态具有权限；上下文仅限完成该任务所需的数据。动作结果返回最新 WorkItem 版本与结构化结果。

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

type CompleteKafDelegateTask = AutomationActionBase & {
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

type UpdateProgress = AutomationActionBase & {
  action: "update_progress"
  payload: { stage: string; summary: string; evidenceRefs: string[] }
}

type AssignWorkItem = AutomationActionBase & {
  action: "assign"
  payload: { assignmentGroupId?: string; assigneeId?: string; reason: string }
}

type CloseWorkItem = AutomationActionBase & {
  action: "close"
  payload: { closureCode: string; closureSummary: string; evidenceRefs: string[] }
}

type RecordExecutionFailure = AutomationActionBase & {
  action: "record_execution_failure"
  payload: {
    failureClass: string
    summary: string
    evidenceRefs: string[]
  }
}
```

`complete_bpmn_task` 必须明确完成 `taskId` 所关联的 BPMN 服务任务。`resolve` 和 `close` 先由 IncidentService 或对应专业领域服务校验，再按任务定义决定是否完成 BPMN 任务；该关联在同一事务内落地，不能由 KAF 假设隐式推进。所有动作必须存在于 `kaf_delegate ProcessTask` 声明的允许动作；`assign` 还必须通过 ITSM 对目标组、人员和可见性的校验。`record_execution_failure` 只记录失败证据，不改变 BPMN 等待点；KAF 按自身治理策略决定是否重试。

ITSM 按 WorkItem 的既有 `recordClass` 调用对应专业领域服务。这是确定性路由，不是重新智能分类。动作结果必须区分 `applied`、`already_applied`、`stale_version`、`task_not_active`、`forbidden` 与 `domain_rejected`；KAF 刷新上下文并由自身治理策略决定后续行为。

## 5. 数据治理与审计

ITSM WorkItem 时间线只保存脱敏的执行摘要、Procedure/版本、Tool 审计引用、动作结果和 `correlationId`。原始模型输入输出、敏感工具输出和完整 Langfuse trace 留在 KAF；KAF 按同租户 RBAC、脱敏和保留期策略管理它们。

ITSM 不复制原始 prompt、完整对话或 Tool 敏感输出。KAF 返回的证据引用必须可审计但不应泄露凭据、令牌、密码或受保护内容。 `correlationId` 用于关联 KAF session、Langfuse trace、WorkItem、BPMN、ProcessTask、KAF run/step 和 ITSM 审计，而不是绕过权限读取数据的凭据。

## 6. 首期专业域行为

| 专业域 | 委派资格与 KAF 行为 | ITSM 动作落点 |
|---|---|---|
| `service_request_item` | 已获批且 BPMN 到达 KAF 委派节点后，KAF 读取服务申请，自主选择开通 Procedure，调用受治理 Tool 并回报进度。 | `complete_bpmn_task` 完成 `kaf_delegate ProcessTask`；BPMN 和 ServiceRequestService 判断 Requested Item 是否完成。 |
| `incident` | 仅命中配置化自动诊断流程绑定的 Incident 才委派。KAF 诊断、检索知识、选择修复 Procedure、调用受治理 Tool 并形成解决证据。 | `update_progress` 与 `resolve` 由 IncidentService 校验；关闭遵循既有关闭规则。 |

## 7. SSLVPN 驱动场景与 BPMN 迁移

### 7.1 SSLVPN 权限申请：Service Request

1. 用户通过 KAF 或 ITSM 直接入口提交 SSLVPN 远程访问权限申请，包含既有 8 个动态字段。
2. ITSM 创建 `service_request_item`，保存受理快照，启动 SSLVPN 双级审批 BPMN 和 SLA。
3. 部门领导与 L2 网络运维完成既有审批。
4. 新版 BPMN 随后创建 `kaf_delegate ProcessTask`，并发布 `KafDelegateRequested`。
5. KAF 接收任务、获取 WorkItem，自主选择 VPN 开通 Procedure，执行受治理 Tool，并将步骤进度和审计引用写回 ITSM。
6. KAF 提交 `CompleteKafDelegateTask`；ITSM 原子完成任务、记录审计，并按 BPMN/ServiceRequestService 规则推进 Requested Item。

### 7.2 SSLVPN 无法连接：Incident

1. 用户通过 KAF 或 ITSM 直接入口描述 SSLVPN 无法连接。
2. KAF 识别 `recordClass=incident`；ITSM Intake 校验引用后创建 Incident WorkItem 和专业扩展。
3. ITSM 的 SSLVPN 连通性 Incident 流程绑定命中后，BPMN 创建并委派 `kaf_delegate ProcessTask`。
4. KAF 接收任务，执行知识检索、诊断和受治理修复 Tool，并回报进度与脱敏证据。
5. 满足 IncidentService 规则时，KAF 提交 `ResolveIncident`；ITSM 记录实际动作、审计和最新 Incident 状态。

### 7.3 流程版本策略

现有 SSLVPN 双级审批 BPMN 在二级审批后直接结束。首期应发布包含 KAF 委派节点的新版本；新创建的 SSLVPN 请求绑定新版本，已运行实例继续按旧版本结束，不做运行中迁移。新版本回滚时只影响后续创建实例，既有实例按其启动版本执行。

## 8. UI 与体验设计

### 8.1 终端用户入口

终端用户面对单一的“获取帮助”受理体验，而不在“标准申请”“自由报障”、Catalog、Incident 或优先级之间作前置选择。页面沿用现有 Ticket 页的 Ant Design `Card`、`Tag`、状态信息区和响应式布局，不引入平行视觉体系。

| 路由 | 目标形态 | 迁移规则 |
|---|---|---|
| `/my-requests` | 我的请求列表与主要“获取帮助”按钮。 | 保留并扩展为终端用户的请求总览。 |
| `/my-requests/new` | ITSM 内唯一的终端用户自然语言受理页：KAF 对话、追问、结构化预览与确认提交。 | 新增权威入口。 |
| `/tickets/ai-create` | 旧 AI 建单入口。 | 重定向至 `/my-requests/new`。 |
| `/service-catalog/request/[id]` | Catalog 详情的预填入口。 | 将选中的 Catalog 作为提示进入 `/my-requests/new`，不单独创建 WorkItem。 |
| `/tickets/create` | 坐席/管理员快速建单工作台。 | 从终端用户导航移除，保留显式专业建单能力。 |

受理页的主区域是对话流，KAF 负责理解需求与收集字段。KAF 产出结构化结果后，页面以确认卡片显示标题、推荐 Catalog、已收集字段、预期交付/SLA 和后续审批步骤；用户可返回编辑或确认提交。分类、`recordClass`、优先级与流程不是终端用户字段。Web 入口确认后以 `confirmation="confirmed"` 调用 Intake；Teams/WeCom 保持既有渠道自动创建策略。

### 8.2 请求详情与信息披露

终端用户通过 `/my-requests` 进入简化请求详情，而不是完整的坐席 Ticket 工作台。申请人只能看到当前状态、下一步、SLA、审批结果、面向用户的评论、附件和 KAF 的脱敏进度摘要；不得看到内部 CTI、Procedure、Tool、技术失败详情、BPMN task ID 或内部审计引用。

坐席/管理员继续使用现有 `/tickets/[ticketId]` 与 Incident 详情。除既有 SLA、审批、时间线、附件和关系组件外，增加“自动化执行”信息区，显示 `kaf_delegate` ProcessTask 状态、KAF 进度、Procedure/版本、脱敏 Tool 审计引用和失败摘要。所有可见操作均以后端返回的权限与允许动作决定，前端不从状态文字推导权限。

### 8.3 Workflow Designer

现有 Workflow Designer 的服务任务选择器和节点属性面板增加 `KAF 委派` 类型。选择后写入 `taskType="kaf_delegate"`；管理员只能配置节点名称、说明、允许的 WorkItem 动作和超时/SLA 关联。Procedure、Tool、模型、Prompt 和 KAF 风险策略不得出现在 BPMN 节点属性中。

流程实例与审计页面将该节点展示为“已委派 KAF”“等待 KAF 完成”或“已完成”，并可跳转到关联 WorkItem。节点配置必须经后端校验，与 `kaf_delegate` ServiceTask 注册表一致。

### 8.4 UI 状态与验证

受理页必须覆盖 KAF 分析中、补充字段、确认、提交中、创建成功和可恢复失败；用户在确认前始终可以编辑或取消。请求详情必须区分加载、空、错误和无权限状态，并在移动端保持对话、确认卡片和状态信息可读。

UI 验收至少覆盖：入口收敛与旧路由重定向、Catalog 预填、Web 确认创建、移动端受理、申请人与坐席的信息隔离、`kaf_delegate` 节点配置、KAF 进度展示，以及前端不推导领域语义或权限。

## 9. 验收与测试边界

1. SSLVPN 权限申请可从 KAF 与 ITSM 直接入口创建，并形成同样的 `service_request_item`、8 项字段、SLA 和审批 BPMN。
2. SSLVPN 双级审批完成后，BPMN 原子创建 `kaf_delegate ProcessTask`、审计与 Outbox；KAF 能以 `taskId` 读取对应 WorkItem，而无需旧 ITSM 全文轮询。
3. KAF 选中的 Procedure、版本、Tool 审计引用、进度和最终动作均可在 KAF 执行记录与 ITSM WorkItem 时间线中追溯，且 ITSM 不保存敏感原始内容。
4. 同一 `tenantId + taskId + runId + stepId` 的重放不会重复落地动作；重复事件由 KAF 幂等处理，重复动作由 ITSM 返回已应用结果。
5. SSLVPN 无法连接可创建 `incident`；只有命中流程绑定时才委派 KAF，且 IncidentService 是 `resolve` 的唯一状态校验入口。
6. 每条验收链路均可用同一 `correlationId` 关联 KAF session、Langfuse trace、WorkItem、BPMN 任务、ProcessTask、KAF run/step 和审计记录。
7. 自动化 API 测试必须覆盖 tenant/RBAC 拒绝、任务非活跃、版本冲突、重复事件与重复动作；KAF/ITSM 合同测试使用确定性 KAF stub，真实 Tool 仅在隔离 SSLVPN 沙箱做端到端验证。
8. KAF 仅通过受控 ITSM API 读取受委派 ticket 和写入动作；不存在直接数据库访问或前端领域语义推导。

## 10. 迁移与非目标

现有 `sr_batch` 为旧 ITSM 缺少可靠 CRUD API 时形成的适配链路。首期以 SSLVPN 服务请求验证新链路后，逐步将 `sr_batch` 的执行台账、幂等和 Tool 审计能力迁入 KAF Execution Context 与 ITSM WorkItem 审计；不保留旧工单全文重分类、Web 表单提交、`cticode` 映射或轮询恢复作为长期兼容路径。

首期不实现人工接管、用户追问恢复、审批恢复、长期失败补偿、Problem、Change 或复杂失败编排。这些作为后续 backlog 单独设计，避免将人工协作状态机混入 KAF 自主执行主路径。
