# KAF 自主 WorkItem 受理与 BPMN 委派设计

> 状态：Draft for review
> 日期：2026-08-28
> 范围：KAF 与 ITSM 的统一受理、WorkItem 创建、BPMN 委派和自动化处理主路径
> 驱动验收：SSLVPN 权限申请与 SSLVPN 连接故障

## 1. 目标

KAF 是终端用户的主要体验层，负责多渠道对话、自然语言理解、CTI/Catalog/`recordClass` 识别、Procedure 选择和受治理 Tool 执行。ITSM 是 WorkItem、专业生命周期、BPMN、SLA、权限和审计的权威系统。

目标是在不禁用用户直接通过 ITSM 创建单据的前提下，实现以下统一链路：

```text
KAF 或 ITSM 直接入口
  -> ITSM Intake 创建 WorkItem
  -> ITSM BPMN 到达 KAF Delegate Task
  -> KAF 获取 WorkItem 并自主选择 Procedure/Tools
  -> KAF 回报进度和完成结果
  -> ITSM 校验动作、更新 WorkItem 并推进 BPMN
```

首期覆盖 `service_request_item` 与 `incident`。Problem、Change、人工接管、用户追问恢复、审批恢复和补偿编排不在本次范围。

## 2. 决策与边界

### 2.1 用户入口

KAF 是终端用户的长期主体验，但 ITSM 直接建单能力保留，且不得要求用户跳转到 KAF。

| 入口 | 行为 |
|---|---|
| KAF Web / Teams / WeCom | KAF 对话式收集需求并调用 ITSM Intake；Web 必须经用户确认，Teams/WeCom 可按渠道策略自动创建。 |
| ITSM 终端用户页面 | 用户留在 ITSM。可手动选择 Catalog/填写表单，或在当前页面调用 KAF 分析能力；页面不推导领域语义。 |
| ITSM 坐席/管理员页面 | 允许专业人员明确创建 WorkItem；创建后如 BPMN 到达 KAF 委派节点，仍进入同一 KAF 处理链路。 |

所有入口使用同一 ITSM Intake 应用服务。前端不得使用正则、预设名称或用户优先级来推断 `recordClass`。

### 2.2 智能决策与领域裁定

KAF 负责智能决策：

- 从自然语言识别 CTI、Catalog 和 `recordClass`；
- 收集和标准化字段；
- 选择实际执行的 Procedure；
- 调用其已有治理边界内的 Tools；
- 决定要请求 ITSM 执行的 WorkItem 动作。

ITSM 不重新使用关键词或 AI 分类器推理同一语义。ITSM 负责确定性领域裁定：

- 验证租户、权限、Catalog/CTI 可见性、表单与并发版本；
- 按已给定的 `recordClass` 路由到对应专业领域服务；
- 验证 BPMN 当前任务和专业生命周期是否允许请求的动作；
- 原子写入 WorkItem、专业扩展、流程状态、SLA 和审计。

KAF 可以更新、推进、解决和关闭 WorkItem，但不能直接写 ITSM 数据库或绕过 BPMN、IncidentService、ServiceRequestService 等领域规则。

### 2.3 BPMN 与 KAF

BPMN 不绑定 KAF Procedure，也不复制 KAF 的 Tool 权限、风险策略或审批治理。BPMN 只表达：当前流程任务委派给 KAF 处理。

KAF 收到委派后读取 WorkItem，结合当前 CTI、Catalog、表单、历史、BPMN 上下文和租户知识自主选择 Procedure。KAF 的 Tool Registry 与治理机制是 Tool 调用和自主执行策略的唯一权威来源。

ITSM BPMN 仍是流程状态、审批、超时和 WorkItem 生命周期的唯一权威来源。KAF 完成工作后请求 ITSM 落地动作；ITSM 返回最新状态和结构化拒绝原因。

## 3. 状态与数据归属

```text
创建前：KAF Intake Session
  对话、候选、待补字段、确认状态、AI 决策草稿

创建事务：ITSM Intake
  WorkItem、专业扩展、受理快照、SLA、BPMN、审计

创建后：ITSM WorkItem + BPMN
  权威状态、流程任务、分派、附件、评论、审批与时间线

创建后：KAF Execution Context
  workItemId、taskId、runId、stepId、Tool 执行遥测与幂等键
```

ITSM 在 Intake 创建时冻结受理快照：CTI、Catalog 版本、已确认表单、`recordClass`、来源渠道和 KAF 的受理决策。KAF 在开始自动化执行时冻结执行快照：实际选中的 Procedure、版本、输入上下文摘要、模型/提示词版本、置信度、`runId` 和 `stepId`。

KAF 不保存 WorkItem 状态副本；每次展示或执行前读取 ITSM 返回的当前版本和可执行动作。Procedure 选择不在 Intake 时冻结，因为它由 KAF 在 BPMN 委派时基于最新的 WorkItem 上下文决定。

## 4. 接口契约

### 4.1 创建 WorkItem

```ts
type CreateWorkItemCommand = {
  requester: ActorRef
  source: "itsm_web" | "teams" | "wecom"
  confirmation: "confirmed" | "channel_auto_create"
  intent: {
    recordClass: WorkItemRecordClass
    cti: { categoryId?: string; typeId?: string; itemId?: string }
    catalogItemId?: string
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

ITSM 返回 `WorkItemResult`，至少包括 `id`、`number`、`recordClass`、`status`、CTI、Catalog、当前 BPMN/SLA 摘要和详情链接。

### 4.2 委派事件与补偿拉取

当 BPMN 到达 KAF Delegate Task 时，ITSM Outbox 发布：

```ts
type KafDelegateRequested = {
  eventId: string
  ticketId: string
  taskId: string
  correlationId: string
}
```

事件不携带完整工单正文。KAF 使用 `taskId` 调用 ITSM 查询任务上下文，得到当前 WorkItem、冻结受理快照、当前 BPMN 等待点和版本。事件推送是主路径；KAF 重启或事件遗漏时，通过 `GET /automation-tasks?status=ready|retryable` 补拉自己的未完成委派任务。

`taskId + runId` 是委派执行的幂等边界，`correlationId` 是端到端追踪边界。

### 4.3 KAF 处理与动作提交

KAF 可持续回报进度，但进度不会推进 BPMN。只有完成请求经 ITSM 验证成功后，BPMN 才继续。

```ts
type ExecuteWorkItemActionCommand = {
  workItemId: string
  expectedVersion: number
  action:
    | "add_comment"
    | "update_progress"
    | "assign"
    | "complete_bpmn_task"
    | "resolve"
    | "close"
    | "record_execution_failure"
  payload: Record<string, unknown>
  execution: {
    actorType: "kaf_agent"
    actorId: string
    source: "kaf_procedure"
    procedureRef: string
    procedureVersion: string
    runId: string
    stepId: string
    idempotencyKey: string
    correlationId: string
  }
}
```

ITSM 按 WorkItem 的既有 `recordClass` 调用对应专业领域服务。这是确定性路由，不是重新智能分类。版本冲突、BPMN 任务已变化或领域规则拒绝时，ITSM 返回结构化拒绝结果；KAF 刷新上下文并由自身治理策略决定后续行为。语义动作不得盲重试。

## 5. 首期专业域行为

| 专业域 | KAF 行为 | ITSM 动作落点 |
|---|---|---|
| `service_request_item` | 读取获批服务申请，自主选择开通 Procedure，调用受治理 Tool 并持续回报执行进度。 | `complete_bpmn_task` 完成 KAF 委派任务；BPMN 和 ServiceRequestService 判断 Requested Item 是否完成。 |
| `incident` | 诊断、知识检索、选择修复 Procedure、调用受治理 Tool 并形成解决证据。 | `update_progress` 与 `resolve` 由 IncidentService 校验，随后由既有关闭规则处理。 |

## 6. SSLVPN 驱动场景

### 6.1 SSLVPN 权限申请：Service Request

1. 用户通过 KAF 或 ITSM 直接入口提交 SSLVPN 远程访问权限申请，包含既有 8 个动态字段。
2. ITSM 创建 `service_request_item`，保存受理快照，启动现有 SSLVPN 双级审批 BPMN 和 SLA。
3. 部门领导与 L2 网络运维完成既有审批。审批逻辑复用现有能力，不在本设计中重建。
4. BPMN 随后到达 KAF Delegate Task，发布 `KafDelegateRequested`。
5. KAF 获取 WorkItem，自主选择 VPN 开通 Procedure，执行受治理 Tool，并将步骤进度和审计引用写回 ITSM。
6. KAF 提交 `complete_bpmn_task`；ITSM 完成流程任务，记录审计并按 BPMN/ServiceRequestService 规则推进 Requested Item。

### 6.2 SSLVPN 无法连接：Incident

1. 用户通过 KAF 或 ITSM 直接入口描述 SSLVPN 无法连接。
2. KAF 识别 `recordClass=incident`，ITSM Intake 创建 Incident WorkItem 和专业扩展。
3. BPMN 委派 KAF 诊断；KAF 获取 Incident，执行知识检索、诊断和受治理修复 Tool。
4. KAF 将诊断证据和进度写回 ITSM；满足 IncidentService 规则时请求 `resolve`。
5. ITSM 记录实际动作与审计，并返回最新 Incident 状态。

## 7. 验收标准

1. SSLVPN 权限申请可从 KAF 与 ITSM 直接入口创建，并形成同样的 `service_request_item`、8 项字段、SLA 和审批 BPMN。
2. SSLVPN 双级审批完成后，BPMN 成功创建 KAF Delegate Task，KAF 能以 `taskId` 读取对应 WorkItem，而无需旧 ITSM 全文轮询。
3. KAF 选中的 Procedure、版本、Tool 审计引用、进度和最终动作均同时可在 KAF 执行记录与 ITSM WorkItem 时间线中追溯。
4. KAF 完成 SSLVPN 服务请求委派任务后，ITSM 更新 BPMN/WorkItem；同一 `taskId + runId + stepId` 的重放不会重复落地动作。
5. SSLVPN 无法连接可创建 `incident`，KAF 可回报诊断并请求 `resolve`，且 IncidentService 是唯一的状态校验入口。
6. 每条验收链路均可用同一 `correlationId` 关联 KAF session、Langfuse trace、WorkItem、BPMN 任务、KAF run/step 和审计记录。
7. KAF 仅通过受控 ITSM API 读取受委派 ticket 和写入动作；不存在直接数据库访问或前端领域语义推导。

## 8. 迁移与非目标

现有 `sr_batch` 为旧 ITSM 缺少可靠 CRUD API 时形成的适配链路。首期以 SSLVPN 服务请求验证新链路后，逐步将 `sr_batch` 的执行台账、幂等和工具审计能力迁入 KAF Execution Context 与 ITSM WorkItem 审计；不保留旧工单全文重分类、Web 表单提交、`cticode` 映射或轮询恢复作为长期兼容路径。

本设计不实现人工接管、用户追问恢复、审批恢复、补偿流程、Problem、Change 或复杂失败编排。这些作为后续 backlog 单独设计，避免在首期将人工协作状态机混入 KAF 自主执行主路径。
