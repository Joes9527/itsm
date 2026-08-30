# 终端用户受理与建单体验分析及重构蓝图

- 日期：2026-08-28
- 范围：Web、Teams、WeCom 等终端用户入口，KAF AI 平台与 ITSM 的受理、建单及服务请求履约边界
- 性质：基于现有代码的现状分析与重构设计；不包含实施代码
- 关联：`AGENTS.md`、`docs/superpowers/specs/2026-08-26-unified-work-item-model-design.md`、KAF `AGENTS.md` 与 `src/acp/domains/it_support/sr_batch_*`

---

## 1. 结论

原分析把问题概括为“Portal 没有接上现代建单能力”，这个判断不完整。当前真正的结构性问题是：

1. ITSM 同时存在 Portal/目录申请、通用 `/tickets/create`、AI 建单页三类用户路径，领域语义被分散在不同模型和前端规则中；
2. KAF 已经是面向 Web、Teams、WeCom 的多渠道 AI 对话与服务请求编排平台。`sr_batch` 是为适配旧 ITSM 缺少可靠 CRUD API、只能围绕既有工单读取、Web 表单提交、webhook/轮询而形成的**创建后履约引擎**，不是用户 Intake；
3. ITSM 正在重建为 WorkItem 领域模型，继续把 AI 的结构化结论塞入旧 `POST /tickets` 的 `type/category/priority` 字段，会固化旧模型；
4. `ServiceCatalog.target_class` 已被读路径用于路由，但其写路径仍由历史 `itsm_type` 间接计算，创建 DTO 又未暴露两者，尚未成为可由目录管理员直接维护的完整权威模型。

重构目标不是再做一个 Portal 表单，也不是把 `sr_batch` 搬到前台，而是建立一条唯一的领域创建链路：

```text
Web / Teams / WeCom
  -> KAF AI Agent：对话、CTI/目录识别、字段收集、确认或追问
  -> ITSM Intake Application Service / API：验证并原子创建 WorkItem
  -> ITSM 流程/事件：绑定 SLA、BPMN、审计与通知
  -> KAF sr_batch 或其演进后的履约引擎：消费已创建的服务请求并执行 Procedure
```

KAF 与 ITSM 是同一产品能力的两个子系统，不是互不信任的外部平台。KAF Agent 可以读取当前租户的 ITSM CTI、Service Catalog、表单与流程定义，并产出符合 ITSM 领域模型的结构化创建命令；ITSM Intake 不二次用关键词或分类器重算意图，而是负责领域不变量、权限、租户和原子落库。

---

## 2. 已确认的目标边界

### 2.1 用户只有一个受理入口

用户不需要在“标准化申请”和“自由报障”之间选入口，也不应选择 Incident、Requested Item、Change 或优先级。用户从 AI 平台发起请求，描述问题、回答追问、必要时从推荐的服务目录中确认即可。

ITSM 自有 Web 可以直接进入相同的 AI 受理体验，也可以在 AI 不可用时提供目录搜索和人工受理兜底；它们必须调用同一 Intake 应用服务，不能重新使用 `/tickets/create` 的前端推导逻辑。

### 2.2 渠道策略

| 渠道 | 创建前行为 | 创建策略 |
|---|---|---|
| Web | KAF 识别意图、展示 CTI/目录/字段预览 | 用户确认后提交 |
| Teams / WeCom | KAF 在会话中识别意图、补齐必填信息 | 按渠道策略直接提交并回传结构化 Ticket；不完整时继续追问 |
| ITSM 直接访问 | 同一受理体验或目录兜底 | 与 Web 相同，必须确认 |

“聊天渠道自动创建”只适用于 WorkItem 的创建。涉及帐号、权限、资源变更等外部副作用时，仍由 Service Catalog、BPMN、KAF Procedure 和风险策略决定是否需要审批或操作员审核；建单自动化不能绕过履约审批。

### 2.3 专业领域的权威边界

| 概念 | 权威职责 |
|---|---|
| KAF AI Agent | 理解自然语言、读取当前 ITSM CTI/目录数据、收集字段、生成结构化领域命令、对话追问 |
| Service Catalog | 服务定义、用户表单、`targetClass`、流程、SLA、审批和履约 Procedure 绑定 |
| Ticket Category / CTI | 运营分类、路由、报表、知识关联、自动化条件；不拥有专业生命周期 |
| Intake Application Service | 校验命令在当前租户、权限、渠道和目录策略下可执行，并原子创建 WorkItem 与专业扩展 |
| Incident / ServiceRequest / Change 等领域服务 | 各自的专业状态机、领域校验与副作用 |
| KAF `sr_batch` | 对已创建的服务请求进行字段处理、审批门控、自动化执行、执行台账和结果回写 |

`recordClass` 是 WorkItem 不可变的专业身份；目录路径中由 Catalog 的 `targetClass` 决定。AI 选择 CTI、目录和目标 WorkItem，但不得直接写数据库或绕过专业服务。

---

## 3. 现有实现的证据与差距

### 3.1 ITSM Web 路径并存，且语义不一致

| 路径 | 现状 | 结论 |
|---|---|---|
| `/portal` | 有常用目录、近期请求和知识搜索。`HeroSearchBar` 调用的是 `/api/v1/knowledge/search`，目录推荐仍为前端模拟 | 有可复用的体验基础，但不是统一受理入口 |
| `/service-catalog/request/[id]` | 真实创建 `ServiceRequest` 的终端用户目录申请页 | 是正确方向的一部分，但只覆盖已选目录 |
| `/tickets/create` | 加载 TicketTemplate 和分类；`ticketTypePresets`、`inferTicketType()` 和用户优先级驱动旧 `POST /tickets` | 实际是坐席/工作台式快速建单，不应继续作为终端用户主路径 |
| `/tickets/ai-create` | 独立 AI 页面 | “AI 是另一个功能”，没有形成唯一入口 |

`/tickets/create` 的问题不是视觉层面：前端以模板名和预设名称的正则推断 `type`，要求终端用户提交 `priority`，并会把结构化自定义字段重新拼进 `description`。这直接违反 ITSM `AGENTS.md` 中“后端拥有领域规则、前端不推导领域语义”和“一个业务字段只有一个权威写入位置”的约束。

### 3.2 Service Catalog 尚未完成目标模型收敛

当前 `handlers/service_request/entity.go` 已只读取 `target_class` 决定目录申请的 Ticket 类型；但 `handlers/service_catalog/entity.go` 的 `ComputeTargetClass()` 仍以历史 `ITSMType` 为输入。更重要的是，`dto/service_dto.go` 的创建/更新请求没有暴露 `targetClass` 或 `itsmType`，而 repository 创建时的 `ITSMType` 实际是零值，因此新目录默认落为 `service_request_item`。

因此，不能把现状描述为“ServiceCatalog 已是完整的 Record Producer”。准确说法是：读路径已经开始向 `target_class` 收敛，写路径与管理契约尚未重建；这正适合与 WorkItem 模型重建一并完成，而不是再加兼容映射。

### 3.3 当前 AI triage 与 KB 的成熟度被高估

`/tickets/create` 的 AI triage 是表单底部按钮：先 `validateFields()`，再将建议写回表单。它没有形成 Web 的对话式确认，也没有可审计的接受/拒绝闭环。现有 `TriageService` 仍包含关键词兜底和默认策略，不能作为专业 `recordClass` 的唯一决定机制。

知识偏转不是“完全缺失”：Portal 搜索栏已有知识查询；但它未接入统一受理会话，也没有把目录、相似请求和“仍需创建”的决定组织成一次连续交互。

### 3.4 现有详情组件不能简单前移

`KBRecommendCard`、`CIContextCard`、`TicketAttachmentGrid` 多依赖已创建的 `ticketId` 或详情上下文，不能机械搬到提交前。新 Intake 需要：

- 面向请求者的知识/目录推荐数据；
- “我的 CI”选择与上下文查询；
- 暂存附件 API（租户隔离、MIME/大小校验、TTL、审计），在创建事务中关联到 WorkItem。

### 3.5 KAF 的实际能力与 `sr_batch` 的正确定位

KAF `ServiceRequestWorker` 已有“意图/策略 -> 用户确认预览 -> 执行工作流”的交互式前置能力。它会保存确认时的 Procedure 快照，避免用户预览后再次检索造成版本漂移。

`sr_batch` 则从已有 ITSM 工单开始：读取工单、对照 Procedure 识别 intent、RAG 获取模板、抽取/校验字段、要求缺失信息、暂停等待操作员审核、执行步骤、写 step ledger、回写并关闭工单。它出现的历史原因不是 Intake 的产品设计，而是旧 ITSM 的 CRUD API 不可用或不足以承载完整生命周期：KAF 曾以 Web 表单方式创建工单，再依赖 webhook/轮询和工单内容变化恢复处理。它的稳定线程 ID、审计记录、幂等处理和人工审核是履约层资产。

新模型不应让 `sr_batch` 再从标题和备注重新识别已由 KAF 确认的目录/CTI，也不应让它再次从 RAG 推导用户已确认的表单。Intake 创建时必须保存 Catalog、CTI、表单与 Procedure 版本快照；履约引擎消费该结构化快照。`sr_batch` 中读取旧工单文本、`cticode` 字符串、Web 表单创建和通用 `create_ticket()` 负载的部分都是旧 ITSM 适配层，应随新的 ITSM CRUD/API 契约落地而退役，而非进入新入口契约。

---

## 4. 方案 3：以统一领域契约重建受理链路

### 4.1 命令与结果契约

ITSM 应提供受控的 Intake API（或同等的内部应用服务），替代面向终端用户/AI 的通用 `POST /tickets`。KAF 使用它作为内部工具调用边界。

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
  aiDecision: {
    confidence: number
    model: string
    promptVersion: string
    rationale: string
    conversationRef?: string
  }
}
```

```ts
type WorkItemResult = {
  id: string
  number: string
  recordClass: WorkItemRecordClass
  status: string
  cti: CTIRef
  catalogItem?: CatalogItemRef
  assignee?: ActorRef
  assignmentGroup?: GroupRef
  workflow?: WorkflowSummary
  sla?: SLASummary
  actions: { view: string; correctClassification?: string }
}
```

此 API 的校验是领域不变量，不是第二个 AI 分类器：验证当前租户中目录/CTI 的可见性、`targetClass` 一致性、必填字段、请求者权限、渠道自动创建策略和附件归属；随后在同一事务中创建 WorkItem 与其专业扩展，并写审计和事件。

### 4.2 决策规则

| 情形 | 领域决策 |
|---|---|
| AI 确认一个 Catalog Item | 目录的 `targetClass`、表单、流程、SLA、审批与 Procedure 绑定为准 |
| 多个目录候选 | Web 展示选择；聊天追问或等待明确指令，不暗中猜测 |
| 无目录但 AI 已明确专业类别 | Intake 调用对应专业领域服务创建 WorkItem |
| 无目录且专业类别不足以确定 | 创建 `generic` WorkItem，标记待分类；不伪造 Incident 或 Requested Item |
| 必填字段缺失 | Web 不允许确认；聊天继续追问。只有明确策略允许时才创建“待补充”记录 |
| AI 不可用 | 目录搜索/人工 CTI 选择/`generic` 受理兜底；不回退到前端正则或关键词分类 |

目录优先不等于“所有请求都必须有目录”。Catalog 是可配置的服务生产定义；`generic` 是真实的不确定性状态，不是把未知请求错误塞进 Service Request 的兼容桶。

### 4.3 审计、反馈与更正

每次创建必须区分“AI 建议”和“最终领域决定”，记录：来源渠道、会话/消息引用、请求者、CTI、目录、`recordClass`、置信度、模型与提示词版本、用户确认或渠道自动创建策略、创建结果和后续更正。

用户或服务台修正分类时，必须调用专业领域服务执行合法的分类/扩展操作；不得直接改写已有专业记录的 `recordClass`。这些修正记录成为 KAF 评估 AI 识别质量的可追溯反馈，而不是覆盖原始审计。

### 4.4 创建前后状态归属

KAF 是会话与执行编排控制面，ITSM 是 WorkItem 与 BPMN 的权威状态系统。两者不得维护平行的工单状态机。

```text
创建前：KAF Intake Session
  对话、识别候选、待补字段、确认状态、AI 决策草稿

提交：ITSM Intake
  原子创建 WorkItem、专业扩展、Catalog/Procedure 快照与审计

创建后：ITSM WorkItem + BPMN
  权威状态、流程任务、SLA、分派、附件、评论、审批与时间线

创建后：KAF Execution Context
  workItemId、会话关联、Procedure run/step、工具执行遥测与幂等键
```

KAF 创建成功后不保存 WorkItem 状态副本。它在每次展示或执行前读取 ITSM 的当前版本与允许动作；ITSM 在创建事务中保存最终 CTI、Catalog 版本、表单、`recordClass`、来源渠道和 KAF 决策的不可变受理快照。

### 4.5 KAF 的受控处理与推进能力

KAF 负责自然语言到 CTI/Catalog/`recordClass` 的智能识别，并负责 Procedure、工具和多步骤执行编排。ITSM 不重复推理这些语义；当收到 KAF 给定的 `recordClass` 或动作请求时，只执行确定性的领域路由与校验。

KAF 可以更新、推进、解决和关闭 WorkItem，但不能直接写库或绕过 BPMN/专业状态机。它必须以可审计执行主体调用 ITSM 的领域动作 API，而非获得一个无约束的 `PATCH /tickets/:id`：

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
    | "request_information"
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

ITSM 按现有 WorkItem 的 `recordClass` 将动作交给对应专业领域服务，并由 BPMN/领域规则检查该动作是否允许；这是确定性路由，不是再次分类。KAF 则根据结构化拒绝原因刷新上下文、重试技术步骤、继续追问或转人工。语义动作不可盲重试；`idempotencyKey` 必须由 `workItemId + runId + stepId` 稳定构成，防止网络重试导致重复外部副作用或重复关闭。

### 4.6 BPMN 分配给 KAF 的代表性场景

以“为新员工开通 AD/O365 帐号”为例：

1. 用户在 WeCom 向 KAF 提出请求；KAF 匹配“新员工帐号开通”Catalog，收集字段后以 `channel_auto_create` 调用 ITSM Intake。
2. ITSM 创建 Requested Item WorkItem、固定 Catalog/CTI/Procedure 快照并启动 BPMN；当前 WorkItem 为“等待经理审批”，KAF 回传工单号与状态。
3. 经理在 ITSM 审批通过。BPMN 到达“自动开通帐号”服务任务时，创建一项**KAF 执行分配**。它是现有流程任务的执行责任，不是第二个 WorkItem 或第二套审批引擎。
4. ITSM Outbox 发布 `work_item.automation.ready` 事件，携带 `taskId`、`workItemId` 和 `correlationId`。KAF 收到事件后，以 `taskId` 查询 ITSM 的任务上下文，获得已冻结字段、允许动作及 Procedure 版本，而不依赖事件中的完整工单正文。
5. KAF 执行 AD 创建、O365 授权等工具步骤，并以 `runId + stepId` 幂等键逐步向 ITSM 回报进度和结果。
6. 成功时，KAF 提交 `complete_bpmn_task` 与进度动作；ITSM 写 WorkItem 时间线、执行审计和 BPMN 任务结果，随后推进至验证或完成。只有 ServiceRequestService/BPMN 的完成条件满足，Requested Item 才能 resolve/close。
7. KAF 工具失败或重启时不得自行关闭 WorkItem。ITSM 将流程任务保持为 failed/retryable 或转人工；KAF 通过 `GET /automation-tasks?status=ready|retryable` 补拉未完成分配。

事件推送是主路径，补偿拉取用于防止 KAF 重启或事件遗漏。`correlationId` 必须贯穿 KAF session、Langfuse trace、WorkItem、BPMN 任务、KAF run/step 和审计记录；当前 Langfuse 样本尚未具备这条端到端关联链路，这是重构的可观测性缺口。

---

## 5. 重构边界与替代关系

| 现有能力 | 新定位 | 重构动作 |
|---|---|---|
| `/portal` | ITSM 内的自助入口和直接访问兜底 | 接入 KAF 受理会话/同一 Intake API；保留目录、知识和近期请求体验 |
| `/service-catalog/request/[id]` | 目录详情/预填入口，而非独立建单系统 | 收敛到统一 Intake 会话和 `CreateWorkItemCommand` |
| `/tickets/create` | 坐席工作台快速建单，或随旧 Ticket 模型下线 | 从终端用户导航移除；删除终端用户的 CTI/type/priority 推导 |
| `/tickets/ai-create` | 历史独立 AI 页面 | 合并到 KAF/统一受理体验后删除 |
| `TicketTemplate` | 内部坐席快速录入/执行模板 | 不再代表终端用户的服务申请 |
| `ticketTypePresets`、`inferTicketType()` | 旧模型前端语义 | 删除，不迁移为新兼容层 |
| KAF `sr_batch` | 旧 ITSM 工单驱动的创建后 Service Request 履约消费者 | 迁移为消费 WorkItem 结构化快照和流程事件，保留执行门控、台账和幂等性，删除轮询/文本重分类等旧系统适配职责 |
| KAF `create_ticket()` 的 `cticode` 负载与 Web 表单模式 | 旧 ITSM API 不可用时的创建适配 | 由新的 ITSM Intake API 的结构化领域契约完全替换 |

AGENTS.md 要求新路径替代旧路径时移除旧路径，而不是长期双读双写。因此本方案不设计 `targetClass`/`itsm_type`、Catalog/Template、AI 命令/旧 Ticket DTO 的长期桥接层。迁移需以一次领域模型切换为目标，并对已存量数据提供可审计的回填工具。

---

## 6. 实施顺序（讨论后的建议）

### 阶段 1：完成模型权威化

1. 按 Unified WorkItem 契约重建 `recordClass`、专业扩展和原子创建边界。
2. 让 `ServiceCatalog.targetClass` 成为直接可维护、经 DTO/API 校验的唯一写入字段；删除运行期 `itsm_type` 映射。
3. 定义 Catalog 与 Procedure 的版本化绑定、字段快照和流程/SLA 绑定。
4. 定义 CTI、目录、表单、渠道策略的租户范围、RBAC 和审计模型。

### 阶段 2：建立 Intake 契约与 KAF 工具

1. 实现 ITSM Intake Application Service/API 和 `CreateWorkItemCommand`/`WorkItemResult` 契约。
2. 让 KAF Agent 通过 ITSM 查询工具读取当前 CTI、Catalog、表单和可见性，并通过 Intake 创建。
3. 实现 Web 确认与 Teams/WeCom 自动创建策略；将确认、来源和 AI 决定写入统一审计。
4. 引入暂存附件与结构化 CI 上下文，避免把字段复制回 description。

### 阶段 3：收敛体验与履约

1. 将 Portal、目录详情和 AI 建单页收敛到统一受理体验；移除终端用户访问旧 `/tickets/create` 的路径。
2. 将 KAF `sr_batch` 迁移为消费 WorkItem 快照与事件，逐步移除旧工单文本重分类、`cticode` 映射、Web 表单提交、轮询恢复和旧 API 适配。
3. 迁移完成后删除 `ticketTypePresets`、`inferTicketType()`、旧 AI 建单页及不再需要的兼容 DTO/路由。

---

## 7. 验收重点与风险

- 一个业务概念只有一个权威写入点：`recordClass`、CTI、Catalog、表单、附件和 Procedure 版本均不可双写。
- KAF 对自然语言理解负责，ITSM 对领域合法性与副作用负责；两者不再各自重新分类同一请求。
- 所有读取、创建、附件、相似检索和履约操作均须租户/MSP、RBAC、消息身份映射和审计闭环。
- Teams/WeCom 的自动建单必须具备幂等键（渠道消息/会话引用）并返回可追踪的结构化结果，防止重投导致重复 WorkItem。
- WorkItem 创建失败时不可留下附件孤儿、部分专业扩展或无审计的外部动作；需定义事务与补偿边界。
- KAF `sr_batch` 的操作员审核、步骤台账、幂等执行和失败回写为现有可复用资产；不应为了入口收敛而被弱化。
- 迁移完成前不得宣称 `targetClass` 已是完整权威模型，也不得把现有 KAF 的旧 `cticode` 建单适配当作新 WorkItem 契约。
