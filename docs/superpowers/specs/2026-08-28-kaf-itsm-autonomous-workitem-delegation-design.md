# KAF 自主 WorkItem 受理与 BPMN 委派设计

> 状态：Implementation in progress
> 日期：2026-08-28
> 范围：KAF 与 ITSM 的统一受理、终端用户/坐席/管理员 UI、WorkItem 创建、BPMN 委派和自动化处理主路径
> 驱动验收：SSLVPN 权限申请与 SSLVPN 连接故障
> 修订：2026-08-29 — 对照 ITSM/KAF 代码库现状核查后补充第 11 节「前置工程条件」，并在 §2.1、§2.3、§3.1、§4.3、§5 就地标注当前代码尚不具备、需先行落地的基础设施缺口。架构决策本身未变，本次修订标注的是「本设计可以实现之前必须先完成什么」。
> 实施更新：2026-08-31 — 核心 BPMN → KAF 委派、事务性 Outbox、任务范围 API、action ledger、完成回执和 KAF 恢复链路已在 feature worktree 实现；统一 Intake、Incident typed actions、UI 和完整产品验收仍未完成。详见第 12 节。
> 基线更新：2026-08-31 — ITSM 委派实现已通过 `dc1233c8` 合并到 `main`；执行完整性 plan 不再是待执行计划，而是保留的历史 TDD 配方。下一实施增量确定为“执行完整性发布收口”，其后再单独设计统一 Intake。详见 §12.6–§12.8。

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

> **现状核查（2026-08-29）**：KAF 代码库中已存在 `src/acp/itsm/`（`client.py`、`tickets.py`）及配套 MCP 工具、webhook、轮询爬虫，对接的是另一套遗留系统（紫羚/Gazellio/KEAS ITSM，cookie+JWT 登录、`orderkey`/`orderno` 字段），与本设计要对接的 Go/BPMN/WorkItem 版 ITSM 无关。本设计新增的委派客户端**必须使用独立命名空间**（例如 `acp/itsm_delegate/`），不得复用或混入现有 `acp/itsm/` 目录，也不得沿用其 cookie+JWT 认证模式——否则会违反本节「KAF 适配 ITSM 的认证协议，不以请求体提供的用户/租户/角色为授权依据」的约束，也会造成同一代码库中两套「ITSM 集成」并存却语义不同的混淆，触犯 KAF AGENTS.md 第 3 条「禁止对同一问题存在多种并行实现」。
>
> 另外，本文档通篇使用「租户」，KAF 侧的对应隔离单位是 `Workspace`（`ProcedureManifest.workspace_id`、`ToolMetadata.workspace_scope` 等）而非 `Tenant`；两边约定 `tenantId ⇔ workspace_id` 一一映射，接口契约中的 `tenantId` 在 KAF 侧解析为 `workspace_id`，不新造第三个身份概念。

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

> **现状核查（2026-08-29）**：ITSM 自定义引擎（`service/bpmn_process_engine.go`）已增加 `AsyncServiceTaskHandler`：`kaf_delegate` 可创建 `ProcessTask`、挂起，并在外部完成任务后恢复流程。`ServiceTaskHandlerInterface`/`CallbackRegistry` 的按 `taskType` 分发机制可复用，无需 fork 引擎。当前不足是 ProcessTask 创建、审计与事件投递尚未处于同一可靠事务边界，且没有 Outbox 保障 KAF 收到委派消息；详见第 11 节 P0-1。

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

> **现状核查（2026-08-29）**：`ent/schema/process_task.go` 已有 `correlation_id`，但创建委派任务时尚未写入或暴露；仍缺 `version`（乐观锁）和结构化 `allowed_actions`，且与 WorkItem 的关联是经 `ProcessInstance.business_id/business_type` 间接引用，不是直接外键——详见第 11 节 P1-1。`CustomProcessEngine.authorizeKafAutomationActor` 已在 `complete_bpmn_task` 路径对具体 `ProcessTask` 执行 `kaf_automation` 角色、任务租户和 `delegated` 状态三重校验。`kaf-context` 与 `actions` 端点应提取并复用同一 per-task 授权策略，而非新建 scoped-token 或平行权限体系，详见第 11 节 P0-2。

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
type ActorRef = {
  id: string
  kind: "system"
  displayName: string
}

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

`actor` 是创建该委派的 ITSM BPMN 系统主体，不是请求人，也不是 KAF。

事件推送是主路径；KAF 重启或事件遗漏时，通过 `GET /bpmn/process-tasks/kaf-delegated?status=delegated` 补拉其有权处理的未完成任务。MVP 不提供 claim 或 lease API，KAF 自身负责执行协调。

任务范围 API 为：

- `GET /bpmn/process-tasks/{taskId}/kaf-context`：返回该任务关联的 WorkItem、冻结受理快照、当前 BPMN 等待点、允许动作和当前版本；
- `POST /bpmn/process-tasks/{taskId}/actions`：提交 typed action；`complete_bpmn_task` 成功时完成关联 `ProcessTask` 并推进 BPMN。

ITSM 必须复用 `authorizeKafAutomationActor` 的 per-task 校验策略：先按 `taskId` 读取 `ProcessTask`，再校验 `kaf_automation` 角色、任务租户、`taskType="kaf_delegate"` 与 `delegated` 状态；上下文仅限完成该任务所需的数据。动作结果返回最新 WorkItem 版本与结构化结果。不能仅依赖通用 `RequirePermission`，也不引入 scoped-token 体系。

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

> **现状核查（2026-08-29）**：`ResolveIncident`/`CloseIncident` 内部确实有版本校验和状态机守卫（`incident_service.go`），但版本是方法内部重查得到的，方法签名今天不接受调用方传入 `expectedVersion`——需要先扩展这些方法的签名才能支撑本节的 `stale_version` 语义。更需要注意的是 `AssignIncident` 今天**没有任何版本检查也没有状态机守卫**（已关闭的 Incident 也能被重新指派），本节假设「`assign` 必须通过 ITSM 对目标组、人员和可见性的校验」在这一点上不成立，需要在 KAF typed action 的领域服务改造中显式补齐 `AssignIncident` 的状态/版本守卫。WorkItem parity Phase 4 只负责面向人类坐席 UI 的 `ActionPermission` 投影，不能作为自动化动作合法性或并发守卫的权威来源；详见第 11 节 P1-2。

## 5. 数据治理与审计

ITSM WorkItem 时间线只保存脱敏的执行摘要、Procedure/版本、Tool 审计引用、动作结果和 `correlationId`。原始模型输入输出、敏感工具输出和完整 Langfuse trace 留在 KAF；KAF 按同租户 RBAC、脱敏和保留期策略管理它们。

ITSM 不复制原始 prompt、完整对话或 Tool 敏感输出。KAF 返回的证据引用必须可审计但不应泄露凭据、令牌、密码或受保护内容。 `correlationId` 用于关联 KAF session、Langfuse trace、WorkItem、BPMN、ProcessTask、KAF run/step 和 ITSM 审计，而不是绕过权限读取数据的凭据。

> **现状核查（2026-08-29）**：`middleware/audit.go` 已定义 `AuditMiddleware`，但从未在 router 中挂载（`router.go`/`main.go` 均无 `r.Use(...)` 调用）。本链路不以挂载全局中间件为前置条件：参照 `handlers/known_error/handler.go` 的既有模式，在 `kaf-context`/`actions`/`complete_bpmn_task` 等高风险落点由应用服务显式写入 `AuditLog`，记录技术账号、KAF agent、Procedure、run、step 和结果。全局 AuditMiddleware 是否接线是独立的平台治理议题，不阻塞本设计，详见第 11 节 P0-3。

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

受理页的主区域是对话流，KAF 负责理解需求与收集字段。KAF 可先展示高相关知识或自助操作建议；用户可直接结束，或选择“仍需帮助”继续同一受理会话并提交，不形成第二种建单入口。KAF 产出结构化结果后，页面以确认卡片显示标题、推荐 Catalog、已收集字段、预期交付/SLA 和后续审批步骤；用户可返回编辑或确认提交。分类、`recordClass`、优先级与流程不是终端用户字段。Web 入口确认后以 `confirmation="confirmed"` 调用 Intake；Teams/WeCom 保持既有渠道自动创建策略。

### 8.2 请求详情与信息披露

终端用户通过 `/my-requests` 进入简化请求详情，而不是完整的坐席 Ticket 工作台。申请人只能看到服务阶段、当前状态、下一步、SLA、审批结果、面向用户的评论、附件和 KAF 的脱敏进度摘要；不得看到内部 CTI、Procedure、Tool、技术失败详情、BPMN task ID 或内部审计引用。

服务阶段是 ITSM 对专业状态与 BPMN 的配置化用户视图投影，而非前端或 KAF 重新解释流程。首期标准阶段为：`已提交`、`需要你的操作`、`正在处理`、`等待验证` 和 `已完成`。每个 Catalog/Incident 场景可配置其内部状态和节点到服务阶段的映射；页面只显示当前阶段、下一步和更新时间，不显示内部节点名称。

处理中复用现有公开评论与附件能力，供申请人与坐席协作；评论不会改变 KAF 正在执行的任务上下文。KAF 需要补充信息或用户验证时，必须由明确的 ITSM/BPMN 用户操作节点发起；首期不提供自由聊天输入或用户追问恢复。

坐席/管理员继续使用现有 `/tickets/[ticketId]` 与 Incident 详情。除既有 SLA、审批、时间线、附件和关系组件外，增加“自动化执行”信息区，显示 `kaf_delegate` ProcessTask 状态、KAF 进度、Procedure/版本、脱敏 Tool 审计引用和失败摘要。所有可见操作均以后端返回的权限与允许动作决定，前端不从状态文字推导权限。

### 8.3 Workflow Designer

现有 Workflow Designer 的服务任务选择器和节点属性面板增加 `KAF 委派` 类型。选择后写入 `taskType="kaf_delegate"`；管理员只能配置节点名称、说明、允许的 WorkItem 动作和超时/SLA 关联。Procedure、Tool、模型、Prompt 和 KAF 风险策略不得出现在 BPMN 节点属性中。

流程实例与审计页面将该节点展示为“已委派 KAF”“等待 KAF 完成”或“已完成”，并可跳转到关联 WorkItem。节点配置必须经后端校验，与 `kaf_delegate` ServiceTask 注册表一致。

### 8.4 通知

ITSM 的 WorkItem 状态和流程事件是用户通知的权威来源。KAF 将用户可见摘要投递回原始渠道：Web 使用站内通知，Teams/WeCom 使用原会话消息；`/my-requests` 始终保留完整进度作为可追溯入口。通知只包含服务阶段、下一步和可见摘要，不包含内部自动化、Procedure、Tool 或技术失败细节。

### 8.5 UI 状态与验证

受理页必须覆盖 KAF 分析中、补充字段、确认、提交中、创建成功和可恢复失败；用户在确认前始终可以编辑或取消。请求详情必须区分加载、空、错误和无权限状态，并在移动端保持对话、确认卡片和状态信息可读。

UI 验收至少覆盖：入口收敛与旧路由重定向、知识建议后继续提交、Catalog 预填、Web 确认创建、移动端受理、服务阶段投影、申请人与坐席的信息隔离、公开评论不影响 KAF 执行、原渠道通知、`kaf_delegate` 节点配置、KAF 进度展示，以及前端不推导领域语义或权限。

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

现有 `sr_batch` 为旧 ITSM 缺少可靠 CRUD API 时形成的适配链路。此结论来自 KAF 代码库核查，当前 `itsm-backend` 仓库不包含该实现；详细依据见 [终端用户受理体验分析](2026-08-28-end-user-ticketing-experience-analysis.md)。首期以 SSLVPN 服务请求验证新链路后，逐步将 `sr_batch` 的执行台账、幂等和 Tool 审计能力迁入 KAF Execution Context 与 ITSM WorkItem 审计；不保留旧工单全文重分类、Web 表单提交、`cticode` 映射或轮询恢复作为长期兼容路径。

首期不实现人工接管、用户追问恢复、审批恢复、长期失败补偿、Problem、Change 或复杂失败编排。这些作为后续 backlog 单独设计，避免将人工协作状态机混入 KAF 自主执行主路径。

## 11. 前置工程条件

本节汇总 §2.1/§2.3/§3.1/§4.3/§5 就地标注的现状核查结论。这份文档描述的是目标态架构；P0-1 必须先形成独立技术设计，P0-2 至 P0-4 必须在随后的实现计划中明确落地与验证。架构决策本身不受这些条目影响——它们都是"今天代码里还没有"，不是"设计错了"。

### P0：阻塞整体链路，必须先落地

| 编号 | 缺口 | 涉及章节 | 说明 |
|---|---|---|---|
| P0-1 | KAF 委派的事务性投递（Outbox）尚未落地 | §2.3/§3.1/§4.2 | 已实现 `AsyncServiceTaskHandler`、`kaf_delegate` 的 `ProcessTask` 暂停/完成恢复和角色校验；仍须将 ProcessTask 创建、显式审计与 Outbox 记录放入同一事务，并实现 `KafDelegateRequested` 的可靠投递、重试和消费去重。原 P0-1 的流程暂停一致性与原 P0-4 的 Outbox 缺口由同一项设计解决，不单独拆分。 |
| P0-2 | 任务范围授权尚未覆盖 KAF 新 API | §3.1/§4.2 | `authorizeKafAutomationActor` 已在 `complete_bpmn_task` 对具体任务完成角色、租户和状态校验。新增 `GET kaf-context` 与 `POST actions` 必须复用/提取同一 per-task 授权策略，并补 `taskType="kaf_delegate"` 校验；不新建 scoped-token 或平行权限模型。 |
| P0-3 | KAF 高风险落点的显式审计尚未实现 | §5 | 不要求挂载全局 `AuditMiddleware`。参照已存在的显式 `AuditLog` 写入模式，在 KAF 上下文读取、typed action 和 BPMN 完成的应用服务落点写入所需审计字段；全局中间件接线另作平台治理。 |
| P0-4 | 幂等键机制不存在 | §4.1/§4.3 | ITSM 没有任何 `idempotencyKey` 的既有实现（现有"幂等"只是局部的唯一索引/状态空操作保护）。`CreateWorkItemCommand.idempotencyKey` 与各 typed action 的幂等键需要新建统一的幂等基础设施，并与 P0-1 的至少一次投递和消费端去重语义配合设计。 |

### P1：需要在对应实现计划中显式排期，不阻塞本文档评审通过

| 编号 | 缺口 | 涉及章节 | 说明 |
|---|---|---|---|
| P1-1 | `ProcessTask` 执行字段仍未完全落地 | §3.1 | `correlation_id` 已增加，但创建委派任务时尚未写入或暴露；仍缺任务级 version/乐观锁和结构化 `allowed_actions`，且与 WorkItem 是经 `ProcessInstance.business_id/business_type` 间接关联而非直接外键。 |
| P1-2 | 领域动作方法不支持调用方传入 `expectedVersion`；`AssignIncident` 缺状态/版本守卫 | §4.3 | `ResolveIncident`/`CloseIncident` 的版本校验是方法内部重查，签名不接受外部 `expectedVersion`，需要扩展签名。`AssignIncident` 今天完全没有版本检查或状态机守卫，需要在 KAF typed action 落地时先补齐。WorkItem parity Phase 4 仅定义面向人类 UI 的 `ActionPermission`，不承接自动化动作的合法性或并发规则。 |
| P1-3 | KAF `ProcedureManifest` 缺 version 字段 | §4.3 | `execution.procedureVersion` 是硬性字段，但 KAF 的 `ProcedureManifest`（`src/acp/models/procedure_manifest.py`）今天没有 version 列，需要新增。 |
| P1-4 | KAF 侧命名空间隔离 | §2.1 | KAF 已有 `src/acp/itsm/` 对接另一套遗留系统（紫羚/Gazellio），新的委派客户端必须使用独立命名空间（如 `acp/itsm_delegate/`），不得复用其认证模式或与之混淆。 |
| P1-5 | `runId`/`stepId` 与既有 `WorkflowStepLedger` 的关系未定义 | §3（KAF Execution Context） | KAF 已有 `WorkflowStepLedger`（`workflow_id`/`step_index`/`idempotency_key`）承担同样职责，字段命名不同。需要在实现前决定：扩展复用该台账，还是引入平行概念——后者会违反双方 AGENTS.md「不为已有能力再建一套」的原则。 |
| P1-6 | KAF 新入口在 Layer Map 中的归属未定义 | 全文 | KAF 现有调度是 `TurnPipeline` 对话轮次驱动，`kaf_delegate` 委派由 ITSM 事件/轮询触发，不是对话轮次。需要明确新入口（新 Worker？独立后台服务？）挂在哪一层，以符合 KAF AGENTS.md「downward only」的依赖方向。 |

以上条目中 P0-1 应作为一份「KAF 委派事务性投递（Outbox）」技术设计统一处理；P0-2、P0-3、P0-4 可作为该链路实现计划中的明确工作包，并在设计评审中确认复用边界。P1 类条目可以在对应领域的实现计划中作为前置 task 处理，不需要单独拦截本文档的评审通过。

## 12. 实施状态与 Agent 交接（2026-08-31）

### 12.1 状态口径与代码基线

本节是当前实施状态的权威说明。第 11 节保留的是 2026-08-29 设计评审时识别的前置条件，不能继续被理解为当前代码状态。

- ITSM 主工作区：`/home/administrator/project/itsm`，`main` 当前基线为 `dc1233c8`；该提交已经合并完整的 ITSM 委派、Outbox、action ledger、completion receipt、RLS 迁移和配套测试/文档。
- ITSM 历史实现 worktree：`/home/administrator/project/itsm/.worktrees/kaf-delegation-transactional-delivery`，分支 `feat/kaf-delegation-transactional-delivery`，当前提交为 `41b24068`。它只用于追溯，不再是后续实现基线；后续工作从 `main` 开始。
- KAF 实现 worktree：`/mnt/d/SynologyDrive/kerry/KAF_Migration_Pack/kaf-worktrees/kaf-delegation-transactional-delivery`，当前提交为 `b533daea`；核心实现提交 `afbc1645` 之后还有格式化提交。
- ITSM feature worktree 中存在未跟踪的历史 review/approval Markdown 文件；它们不属于产品实现，不得在未确认来源时删除、覆盖或加入提交。
- `docs/superpowers/specs/2026-08-30-kaf-delegation-execution-integrity-design.md`、`docs/superpowers/plans/2026-08-30-kaf-delegation-execution-integrity.md` 和 `docs/reports/2026-08-30-kaf-delegation-execution-integrity-report.md` 均已进入 `main`，不得再要求从 ITSM feature worktree 读取。
- 下表中的“已完成”表示已合并到 ITSM `main` 且有针对性自动化证据，不表示已部署到 PROD、取得真实跨进程 Dev 证据或完成真实 SSLVPN Tool 验收。

### 12.2 第 11 节工程条件状态

| 编号 | 状态 | 已落地能力 | 剩余工作 |
|---|---|---|---|
| P0-1 | 已完成（ITSM `main`） | `ProcessTask`、显式创建审计和 Outbox 在同一事务落地；具备可靠投递、重试、KAF 持久化 delivery、lease、去重与完成恢复。 | 在 Dev 复核迁移顺序和三项 `ITSM_KAF_*` 配置；完成真实跨进程链路验收。 |
| P0-2 | 已完成（ITSM `main`） | `kaf-context`、委派任务补拉和 `actions` 复用任务级 actor/tenant/`taskType`/状态校验；未引入 scoped token 或第二套权限模型。 | 在真实 Dev `kaf_automation` 主体下验证 token、租户和 RBAC 配置。 |
| P0-3 | 部分完成 | typed action、任务创建及完成路径已有显式审计，action 审计关联 immutable ledger、Procedure、run/step 和结果。 | `GET kaf-context` 当前仅执行授权与读取，尚未按 §5 写入显式上下文读取审计；需要补测试并确认高频补拉是否单独审计或聚合审计。 |
| P0-4 | 部分完成 | KAF 动作已使用 `tenantId:taskId:runId:stepId`、`KafTaskActionLedger` 和 `applied/already_applied`；delivery 消费具备持久化去重。 | §4.1 的统一 `CreateWorkItemCommand.idempotencyKey` 及 Intake 创建去重尚未实现。不得用 action ledger 代替 Intake 幂等。 |
| P1-1 | 部分完成 | 委派任务已写入并暴露 `correlationId`；上下文返回当前 `expectedVersion`；运行时能够约束允许动作。 | `allowedActions` 仍来自 `TaskVariables` 字符串，尚非结构化字段；任务级 version/直接 WorkItem 关联仍未按目标模型完全落地。 |
| P1-2 | 部分完成 | Incident 既有领域服务仍是计划中的唯一动作入口；`AssignIncident` 已具备终态拒绝、目标用户租户/有效性校验和内部乐观并发守卫，`ResolveIncident`/`CloseIncident` 也已有状态机与内部版本条件。 | KAF API 当前仍不支持 `assign`、`resolve`、`close`；三个领域动作的方法签名都没有接受 KAF 提交的显式 `expectedVersion`。接入 typed action 前须定义调用方版本合同、稳定的 domain rejection 映射以及动作与 BPMN task completion 的原子关系。 |
| P1-3 | 未完成 | KAF 执行时暂以 Procedure 检索结果的 `content_hash` 作为 `procedureVersion`。 | `ProcedureManifest` 仍没有权威 `version` 列。需新增 schema/migration/摄取更新规则，并让执行记录引用持久化版本；不能长期把临时 hash 约定当成领域字段。 |
| P1-4 | 部分完成 | 新链路采用独立的 `ITSM_KAF_URL`、`ITSM_KAF_AUTOMATION_TOKEN`、`ITSM_KAF_WEBHOOK_SECRET`，未复用遗留 Gazellio 凭据；委派运行在独立 headless pipeline。 | 实现位于 `orchestration/headless_tasks/kaf_delegation_pipeline.py`，未采用原建议的 `acp/itsm_delegate/` 命名空间。后续应基于职责拆分客户端与 pipeline，避免形成新的超大模块。 |
| P1-5 | 部分完成 | ITSM action ledger 已以 run/step 作为跨系统动作幂等边界；KAF pipeline 会传递 run/step。 | KAF 既有 `WorkflowStepLedger` 与 ITSM action ledger 的职责映射尚未形成明确实现契约。应定义“Tool/步骤尝试归 KAF ledger，ITSM 副作用归 action ledger”，并补关联字段/测试，禁止双写同一事实。 |
| P1-6 | 已完成（KAF feature 分支） | `KafDelegationPipeline` 作为非对话 headless 入口，由 webhook 与恢复循环驱动，不依赖 `TurnPipeline`。 | 保持该依赖方向；后续拆文件不能回退到对话轮次入口。 |

### 12.3 当前 action contract

当前已合并的 ITSM action API 只接受以下动作：

- `complete_bpmn_task`
- `update_progress`
- `record_execution_failure`

`assign`、`resolve`、`close` 尚未实现，不能因为 §4.3 已定义目标 DTO 就视为可调用能力。新增动作必须调用对应专业领域服务并维持 WorkItem 的单一生命周期权威；不得在 KAF delegation service 内复制 Incident/Service Request 状态机。

### 12.4 验收状态

| §9 验收项 | 状态 | 说明 |
|---|---|---|
| 1. KAF 与 ITSM 入口创建一致的 SSLVPN Service Request | 未完成 | 尚无统一 `CreateWorkItemCommand`/Intake 应用服务和创建幂等；ITSM 新建页与 KAF 确认提交也未收敛到同一契约。 |
| 2. 双级审批后原子委派并由 KAF 按 taskId 获取上下文 | 核心链路已完成 | 已有事务性 Outbox、补拉、任务上下文 API 和 in-process SSLVPN Service Request 验收测试；尚未完成真实跨进程环境验收。 |
| 3. Procedure/版本/Tool 引用/进度/最终动作可追溯 | 部分完成 | run/step、Procedure 引用、action/timeline/audit 主链路已具备；Procedure 权威版本、完整 Tool 审计关联和产品 UI 展示仍缺。 |
| 4. 事件与动作重放不产生重复副作用 | 已完成（核心链路） | KAF delivery 去重、ITSM action ledger、completion receipt 和 `applied/already_applied` 已覆盖并发及恢复路径。 |
| 5. SSLVPN Incident 创建、委派并经 IncidentService resolve | 未完成 | Incident Intake、流程绑定和 `resolve` typed action 尚未形成 E2E。 |
| 6. 同一 correlationId 串联全部系统证据 | 部分完成 | ITSM/KAF 委派与 action 已传播 correlationId；尚未证明 KAF session、Langfuse trace、真实 Tool 和 UI 时间线的完整跨进程关联。 |
| 7. tenant/RBAC/状态/版本/重放及真实沙箱测试 | 部分完成 | 核心 API 的确定性测试已覆盖主要拒绝与重放场景；PostgreSQL RLS/并发探针因凭据未配置而跳过，真实 SSLVPN 沙箱未执行。 |
| 8. KAF 只通过 ITSM API、前端不推导领域语义 | 部分完成 | 新委派链路没有直连 ITSM 数据库；目标 Intake/UI 尚未完成，因此前端约束未形成完整验收证据。 |

### 12.5 UI 与用户体验状态

第 8 节不是“不需要调整 UI”，而是尚未实现：

- 已有 `/my-requests` 列表与详情，但没有目标 `/my-requests/new` 统一自然语言受理页。
- `/tickets/ai-create` 页面和侧边栏入口仍然存在，尚未迁移或重定向到统一受理入口。
- 请求详情尚未完整展示服务阶段投影、KAF 自动化执行摘要、Procedure 版本、进度、失败原因和可审计证据引用。
- 坐席页尚未形成面向 `kaf_delegate` 的等待点、允许动作、执行状态和恢复信息面板。
- BPMN 设计器尚未完成 `kaf_delegate` 节点配置与校验体验。
- 通知策略及 Web/Teams/WeCom 的状态反馈尚未形成端到端实现。

公开评论与附件能力应复用现有 ticket 页面能力，不新建第二套评论系统。UI 只展示后端返回的状态、允许动作和结构化原因，不自行推导 `recordClass`、权限或领域状态机。

### 12.6 下一阶段建议工作包

新 Agent 应从 ITSM `main` 读取本设计、`docs/testing/kaf-delegation-release-closeout-fixture.md`、执行完整性 design、历史 plan 和验收 report。不得把历史 plan 的未勾选 TDD 步骤当成待实施清单，也不得从 feature worktree 重做已经合并的 action ledger、completion receipt 或 Outbox。原 `BPMN 整改遗留项 E2E 测试计划.md` 已归档，只保留历史参考价值，不再作为发布收口权威入口。

后续工作按依赖关系拆成独立 spec → plan → implementation 周期，禁止合并为一份横跨 ITSM、KAF、前端和真实环境的大计划：

1. **执行完整性发布收口**：补 `kaf-context` 显式读取审计及测试；验证 ITSM/KAF 迁移与三项 `ITSM_KAF_*` 配置；以真实 Dev `kaf_automation` 主体执行跨进程 SSLVPN Service Request 主路径、重放/恢复与租户/RBAC 拒绝；配置 `RLS_TEST_DSN` 执行 PostgreSQL RLS 探针；形成可复现证据报告。该增量不新增 Intake、Incident typed actions、UI 或 PROD 写入。
2. **统一 Intake**：在发布收口通过后单独 brainstorm，设计唯一 `CreateWorkItemCommand` 应用服务、认证主体派生、Catalog/CTI/CI 校验、`idempotencyKey`、Service Request/Incident 原子创建与直接 ITSM/KAF 入口复用。
3. **Incident typed actions**：扩展 IncidentService 的调用方 `expectedVersion` 合同，再接入 `assign`、`resolve`、`close`，补 tenant/RBAC/domain rejection/并发和 BPMN completion 一致性测试。
4. **KAF 执行模型收敛**：为 `ProcedureManifest` 增加权威 version；明确 `WorkflowStepLedger` 与 ITSM action ledger 的职责和关联；在不改变 headless 边界的前提下拆分当前超大委派 pipeline。
5. **UI 与完整产品验收**：新增统一受理页并迁移 `/tickets/ai-create`，在 requester/agent 页面加入自动化状态投影，补 BPMN 设计器节点体验、通知，以及 SSLVPN 权限申请/无法连接两个驱动场景。

### 12.7 已有验证与限制

- ITSM feature 分支已通过 `go test ./... -count=1`、`go build ./...` 和 `git diff --check`。
- KAF 委派相关 focused suite 已通过；本地 Dev PostgreSQL 已升级到 `036_kaf_completion_replay`，当前源码启动后 `/health` 正常。
- KAF repository-wide suite **未全绿**：最近一次结果为 `2457 passed, 13 skipped, 1 xfailed, 90 failed, 32 errors`。主要失败涉及测试全局 settings/数据库隔离和委派范围外模块，但在清理前不能宣称 KAF 全量回归通过。
- 未对 `10.128.35.195` KAF PROD 发起写请求或验收；PROD 只可在明确发布窗口、凭据与回滚方案就绪后使用。
- 当前通过的是 in-process SSLVPN Service Request 权威场景，不等同于真实跨进程、真实 Tool 或 PROD E2E。

### 12.8 已确认的实施顺序与门禁

2026-08-31 已确认先执行“执行完整性发布收口”，把已合并能力验证为稳定 Dev 基线；该门通过后，再单独 brainstorm 和设计统一 Intake。这样可以避免在跨进程认证、迁移、RLS 和恢复证据尚未闭合时，同时叠加 Intake、Incident actions 和 UI。

发布收口的完成条件是：

1. `GET .../kaf-context` 的成功读取形成显式、租户范围、无敏感内容的审计证据；高频 delegated-list 补拉不按每一条上下文读取制造不可控审计量。
2. 当前 ITSM `main` 与 KAF feature worktree 在 Dev 完成迁移、健康检查、专用配置和合同核对。
3. 使用真实 Dev `kaf_automation` 主体跑通一次跨进程 SSLVPN Service Request 委派；同一 payload 重放返回 `already_applied`，且 Procedure、Tool、BPMN、timeline、audit 和 action ledger 的副作用基数保持为一。
4. 至少覆盖重复 webhook/恢复竞争、持久化 completion payload 后崩溃恢复、错误主体/租户拒绝和附件最小披露。
5. PostgreSQL RLS 探针以配置的 `RLS_TEST_DSN` 实际执行，不能以 deterministic SQL、SQLite 或 skip 结果替代。
6. 证据写回执行完整性验收报告；未配置凭据、Dev 环境不可用或真实 Tool 不可用时明确记为阻断，禁止推断通过。
7. 不向 `10.128.35.195` KAF PROD 发起写请求，不使用生产凭据；如需 PROD 验收，必须另行取得发布窗口、回滚方案和明确授权。
