# 统一 Work Item 重构 — 多 Agent 执行 Spec

> 状态：Proposed
> 日期：2026-08-26
> 依赖：
> - 领域模型设计：[2026-08-26-unified-work-item-model-design.md](./2026-08-26-unified-work-item-model-design.md)（本文档不重复其内容，只引用并转成可分发的任务包）
> - 前置条件核实：[2026-08-26-approval-single-track-convergence-design.md](./2026-08-26-approval-single-track-convergence-design.md)（Change 域审批单轨化已由 PR#6/Track4 完成并合并；Ticket 域 `TicketDetail.tsx` 审批接线已于 2026-08-26 当晚修复）
> - 基线 commit：`69ab3461`（当前工作分支 merge origin/main 后的 HEAD，`go test ./...` 全绿）

## 0. 文档定位

这不是重新设计领域模型——那份工作已经在 `2026-08-26-unified-work-item-model-design.md` 里做完并核实过（现状诊断经独立 agent 核实，准确率 95%+）。本文档要解决的是**执行方式**问题：这次重构要跨 Incident/Problem/Change/ServiceRequest 四个域和 BPMN 身份契约，用户计划把不同部分分给不同工具（Gemini/Codex/Copilot/Claude）在各自独立的 worktree/session 里并行执行，我（Claude，当前 session）作为总协调——写任务包、做逐任务评审、做最终整分支评审，不直接调用/监控其他工具的执行过程（环境里没有 codex/gemini/copilot 的 CLI，参见探索记录）。

**核心约束（决定了下面 Wave 划分的唯一理由）**：Ent 是全量代码生成框架，一次 `go generate` 会重写 `client.go`/`tx.go`/`mutation.go`/`runtime.go` 等共享文件。如果多个并行 worktree 各自改 `ent/schema/*.go` 并独立跑 codegen，合并时这些生成文件必然发生结构性冲突，无法人工合理解决。因此**所有 ent schema 变更必须先在一个单点串行完成**，下游的业务逻辑实现才能安全并行。

## 1. Wave 划分与依赖图

```
Wave 0（并行，4 个任务包，现在就可以分发，不依赖 Wave 1）
  Incident 回归测试补齐 / Problem 回归测试补齐 / Change 回归测试补齐 / ServiceRequest 回归测试补齐
        │
        │（产出是 Wave 2 的回归安全网，不阻塞 Wave 1 开始，但建议在 Wave 2 分发前至少
        │  Incident 那个任务包已经落地——Incident 当前覆盖率 1.3%，是四个域里唯一
        │  "改了都不知道改坏了什么"的域）
        ▼
Wave 1（我自己单线程执行，串行，不外包）
  全部域的 ent schema 变更一次性做完 + BPMN 结构化身份 + CallbackRegistry 接口收紧
  + 前端共享 WorkItem Shell 骨架
        │
        ▼
Wave 2（并行，4 个任务包，分发给不同工具）
  Incident 迁移 / Problem 迁移 / Change 迁移（在已收敛的 BPMN 审批基础上接入 WorkItem）
  / ServiceRequest 层级规范化
        │
        ▼
Wave 3（我自己执行，串行）
  整个 refactor 分支的最终评审 + Phase 6 清理 + 物理改名评估
```

依赖规则：

- Wave 0 的 4 个任务包互相独立，且不依赖 Wave 1，可以立即分发。
- Wave 1 必须在 Wave 2 全部开始前完成并合入集成分支——Wave 2 的任务包直接消费 Wave 1 产出的 ent 类型和 Shell 组件，不允许在 Wave 1 完成前提前分发 Wave 2。
- Wave 2 的 4 个任务包互相独立（各自持有不相交的文件集，见 §5 每个任务包的"范围与边界"），可以真正同步并行，不需要互相等待。
- 集成分支：`refactor/unified-work-item`，从当前 `fix/portal-approval-fake-success-and-real-data`（或届时的 main）拉出。Wave 1 直接在这个分支上开发。Wave 2 的每个任务包从这个分支拉自己的 worktree，完成后合回它（不直接进 main），后合并的任务包在合并前 rebase 到已经合并的最新状态。全部到齐 + Wave 3 最终评审通过后，`refactor/unified-work-item` 才合入 main。

## 2. 通用任务包规范（模板）

每个分发给其他工具的任务包（Wave 0、Wave 2）固定包含 6 部分，因为执行方是不同工具、不共享本 session 的上下文，必须假设它对这次会话的调查一无所知：

1. **范围与边界**：允许改哪些文件/包；明确禁止碰的文件（尤其是其他任务包正在改的域、`ent/schema/*`、共享 Shell 组件本身）。
2. **现状证据**：直接给出本次会话已核实的 file:line 级别事实，不要求执行者重新核实全仓库。
3. **规范摘录**：从 `AGENTS.md` 摘出与这个任务直接相关的段落（完整文件很长，不能假设执行工具会认真读完）。
4. **验收标准**：具体到可执行命令（`go test ./handlers/xxx/... -cover`、`npm run type-check`、必须为空的 grep），不用"确保功能正常"这类模糊表述。
5. **明确禁止项**：不新增兼容层/桥接服务/翻译层；不长期双写；不能顺手做任务范围外的重构；`recordClass` 一旦落库不可修改。
6. **交付格式**：diff + 变更说明 + **实际跑过的验证命令的真实输出**（不接受"已完成"这类自我声明——AGENTS.md 明确写过"清理型修复不能只信任 implementer 自报 DONE"，跨工具场景下这条更重要，因为我没法像调度同源 subagent 那样追问细节）。

评审与合并：每个任务包回来的 diff，我先做逐任务代码评审（复用 `code-review` 技能），重新跑一遍第 4 部分的验收命令确认真实通过，达标才合入 `refactor/unified-work-item`。不达标就带着具体问题打回去重做，不因为"已经跑了一轮"降低标准。

---

## 3. Wave 0：回归测试补齐（4 个任务包，立即可分发）

> 这 4 个任务包已经拆成独立的自包含文件，可以直接分发给不同工具，不需要连带整份本文档：
> `docs/superpowers/tasks/2026-08-26-wave0-{incident,problem,change,servicerequest}-regression-tests.md`。
> 下面的内容是设计层面的摘要，独立文件是可执行的完整版本，两者内容一致，以独立文件为准。

目的：在动 WorkItem 结构之前，为四个域的**当前行为**建立回归基线，不是测试还不存在的新行为。这是领域模型设计文档 Phase 0 里"为主链路补合同和集成测试"的具体化——实测覆盖率（2026-08-26）：`handlers/incident` 1.3%、`handlers/problem` 31.8%、`handlers/change` 46.9%、`handlers/service_request` 64.9%。**Incident 是唯一必须在 Wave 2 开始前完成的**，其余三个覆盖率虽然不高但已有基础，可以和 Wave 2 并行补，不构成硬阻塞。

**硬性纪律（4 个任务包通用，评审补充）**：写回归测试的过程中大概率会发现现有逻辑本身有 bug——这是补测试的正常副产品，**不是顺手修复的许可**。发现的任何现有代码缺陷，一律用 `t.Skip("已知缺陷，留给 Wave 2 迁移时处理：<具体描述>")` 标注并在交付说明里单独列出来，不允许在 Wave 0 任务包里改一行非测试代码。原因：Wave 0 的 4 个任务包并行执行，如果允许顺手修生产代码，四个工具可能在不知情的情况下改到同一处共享逻辑，制造原本不该在这个阶段出现的冲突。

### 3.1 任务包：Incident 回归测试补齐（优先级最高）

- **范围**：只新增/修改 `itsm-backend/handlers/incident/*_test.go`、`itsm-backend/controller/incident_controller_test.go`（如不存在则新建）、`itsm-backend/service/incident_service_test.go`。不改任何非测试文件。
- **现状证据**：`handlers/incident` 覆盖率 1.3%（`go test ./handlers/incident/... -cover`）；创建路径在 `service/incident_service.go:59 CreateIncident`（ent 事务，只建 `Incident` + `IncidentEvent`，不涉及 `Ticket`）；服务目录 `itsm_type=Incident` 路径经 `handlers/service_request/service.go:85-87 isIncidentCatalog` 直接调 `IncidentService.CreateIncident`，绕过 `ServiceRequest`/`Ticket`/审批链；BPMN 触发在 `service/incident_service.go:1733-1734`，`BusinessID` 是 Incident 表自己的主键。
- **必须覆盖的场景**：创建（含服务目录 `itsm_type=Incident` 路径）、acknowledge/resolve/close 状态机合法与非法转换、跨租户读取失败、`AddAssociations`/`RemoveAssociation` 关联 Ticket 的多对多 edge、升级（若 `incident_escalation_service.go` 确认未接线则不测，改为记录"死代码不测"）。
- **验收标准**：`go test ./handlers/incident/... ./service/... -run Incident -cover` 通过且覆盖率相比 1.3% 有实质提升（目标不低于 40%，供 Wave 2 迁移时做真正的回归对比）；`go build ./...` 通过。
- **禁止项**：不修改任何生产代码；不为了让测试好写而改现有函数签名。

### 3.2 任务包：Problem 回归测试补齐

- **范围**：只新增/修改 `itsm-backend/handlers/problem/*_test.go`。
- **现状证据**：覆盖率 31.8%；创建路径 `handlers/problem/service.go:24 Service.Create`，只插入 Problem 行；`Ticket` 关联是后加的可选多对多 edge（`AddAssociations`/`RemoveAssociation`，`repository_impl.go:152`），不是创建时必建。
- **必须覆盖的场景**：创建、investigate/root-cause/known-error/resolve/close 状态机、Known Error 发布入口、Incident↔Problem 关联的建立/解除、跨租户隔离。
- **验收标准**：`go test ./handlers/problem/... -cover` 通过，覆盖率不低于 50%。
- **禁止项**：同 3.1。

### 3.3 任务包：Change 回归测试补齐

- **范围**：只新增/修改 `itsm-backend/handlers/change/*_test.go`。**不得修改任何审批相关逻辑**——Change 域审批已经在 PR#6/Track4 收敛到 BPMN 并有专门的端到端回归测试（`handlers/change/service_bpmn_test.go`），这次任务包只补审批之外的覆盖缺口（CAB 审批本身的测试已经存在，不用重复）。
- **现状证据**：覆盖率 46.9%（含 Track4 带来的审批测试）；`related_tickets` 是 `field.JSON([]string{})`，无结构化关系；`ChangeType`（standard/normal/emergency）状态机差异（emergency 无 `scheduled` 中间态）。
- **必须覆盖的场景**：非审批状态流转（implement/review/rollback/close）、`related_tickets` 的读写、风险/CAB 之外的字段校验、跨租户隔离。**不要**给已经被 `service_bpmn_test.go` 覆盖的审批流程重复写测试。
- **验收标准**：`go test ./handlers/change/... -cover` 通过，覆盖率不低于 60%；不破坏现有的 `TestChangeServiceTaskHandler_*`/`TestTransitionStatus_*` 系列测试。
- **禁止项**：同 3.1，额外禁止碰 `SubmitChange`/`TransitionStatus` 的 approve/reject 分支和 `service/bpmn/change_handler.go`。

### 3.4 任务包：ServiceRequest 回归测试补齐

- **范围**：只新增/修改 `itsm-backend/handlers/service_request/*_test.go`。
- **现状证据**：覆盖率 64.9%；`ServiceRequest.ticket_id` 是唯一索引外键，状态/审批/工作流委托给 Ticket；`form_data`（JSON）和 `field_values`（`entity_type="ticket"`）存在真实双写（`extractServiceRequestFieldValues` + `CreateValues`，`handlers/service_request/service.go:212,445-459`）；`itsm_type=Incident` 分流逻辑同 3.1。
- **必须覆盖的场景**：目录提交→Ticket 委托创建的全链路、`form_data`/`field_values` 双写的一致性断言（这是 Wave 2 要清理的目标行为，测试要先锁定"现在两边确实一致"这个不变量，方便迁移后对比）、审批链解析（`ApprovalChainResolver.ResolveForServiceRequest`）、`itsm_type=Incident` 分流不产生 ServiceRequest/Ticket 行。
- **验收标准**：`go test ./handlers/service_request/... -cover` 通过，覆盖率不低于 70%。
- **禁止项**：同 3.1。

---

## 4. Wave 1：WorkItem 地基（我自己执行，不外包）

不外包的原因：这部分改动是所有 Wave 2 任务包共同依赖的接口契约，且包含 ent codegen 这个不可并行的操作；出错的代价是连累四条并行线全部返工。

### 4.1 Ent Schema 变更（一次性做完，一次 `go generate`，一次评审）

对照领域模型设计 §6.2/§6.4/§15.2.2：

| Schema | 变更 | 依据 |
|---|---|---|
| `ent/schema/ticket.go` | 新增 `record_class`（string，默认 `generic`，创建后不可变）、`opened_by_id`（int，可选）、`assignment_group_id`（int，可选）；`workflow_instance_id` 目前不存在（走 `TicketWorkflowRecord` edge），本阶段不新增冗余字段，沿用 edge | 设计文档 §6.2、§18.3-1 |
| `ent/schema/incident.go` | 新增 `work_item_id`（int，唯一，必填） | §6.4 Incident；当前完全没有到 Ticket 的 FK，是本次改动量最大的一张表 |
| `ent/schema/problem.go` | 新增 `work_item_id`（int，唯一，必填）；**不删除**现有到 `tickets` 的多对多 edge（历史数据兼容，Wave 2 的 Problem 任务包负责决定这条 edge 的去留） | §6.4 Problem |
| `ent/schema/change.go` | 新增 `work_item_id`（int，唯一，必填）；`related_tickets` 字段本阶段保留，不删除（Wave 2 的 Change 任务包负责把它转成 `WorkItemRelation` 并删除） | §6.4 ChangeRequest、§18.6 |
| 新建 `ent/schema/work_item_relation.go` | 见 §10.1，`(tenant_id, source_work_item_id, target_work_item_id, relation_type)` 唯一索引 | §10 |
| `ent/schema/tickettemplate.go` | 不改字段（`category`/`category_ids` 双字段问题是 Wave 2 ServiceRequest/前端任务的范围，不在这里动） | — |
| `ent/schema/process_instance.go` | 新增 `business_type`（string，非空）、`business_id`（int，正数）；建 `(tenant_id, business_type, business_id, status)` 复合索引 | §15.2.2 规则 3 |
| `ent/schema/processbinding.go` | `business_sub_type` 保留但不再新增用法；新增可选 `category_id`（int）；纳入 `(tenant_id, business_type)` 匹配索引 | §15.2.3 |
| `ent/schema/servicecatalog.go` | 新增 `target_class`（string 枚举：`service_request_item`/`incident`/`change_request`） | §7.2；原方案误把这个字段的 schema 变更放进 Wave 2 §5.4 任务包，违反本节开头的单点串行约束，评审已修正——业务逻辑（`itsm_type`/`service_type` 职责拆分、绕过 Ticket 的 Incident 路径改造）仍归 Wave 2 §5.4，这里只加字段 |

**明确不做**（留给对应 Wave 2 任务包，避免 Wave 1 范围蔓延）：`ServiceRequest.ticket_id` 不改名；`ProcessTask`/`ProcessApprovalDecision` 的字段类型转换（迁移期继续保留字符串快照）；`ticket_type.go` 遗留字段清理（这是独立的 P0 收尾项，见架构评估报告 P0-1）。

### 4.1.1 `business_key`/`business_id` 一致性（评审补充）

领域设计文档 §15.2.2 已经定了唯一格式：`businessKey = "{recordClass}:{workItemId}"`，`businessId = WorkItem.ID`。Wave 1 必须做到两件事，否则这次新加的结构化字段跟历史遗留的字符串字段之间会继续漂移（今天核实已经确认这个漂移风险是真实的——`ProcessApprovalDecision` 的 `business_type`/`business_id` 目前是从 `ProcessInstance.Variables` 这个 JSON map 里 `fmt.Sprint` 出来的，跟 `business_key` 之间没有交叉校验）：

1. `ProcessInstance` 新增的 `business_type`/`business_id` 结构化字段，在流程触发时必须和 `business_key` 由同一处代码原子写入（同一个 `TriggerProcess` 调用里算一次，不能分别来源），杜绝两者不一致的可能。
2. `service/bpmn_process_engine.go` 里写 `ProcessApprovalDecision.business_type`/`business_id` 的地方（`recordApprovalDecision`，目前是 `fmt.Sprint(instance.Variables[...])`），改成直接读 `ProcessInstance` 新增的结构化字段，不再从 `Variables` JSON 里现取——这样 `ProcessApprovalDecision` 的快照值和 `ProcessInstance` 的权威值不可能分叉。
3. `ProcessTask` **不加**任何 business 字段——领域设计文档 §15.2.2 规则 4 明确"通过 process_instance_id 继承业务身份，不重复存储 businessId"，这是有意的设计，不是缺口。

### 4.2 数据回填与一致性检查

- 为 `record_class` 回填：现有 `tickets` 表默认 `generic`；`type` 字段中已经是 `incident`/`change`/`problem` 字面量的历史行**不**自动映射为对应 `record_class`（这些行本来就没有真正的 Incident/Problem/Change 记录，映射会制造假的一致性），只有 Wave 2 各域迁移在建立 `work_item_id` 关联时才决定要不要为该 Ticket 补建。
- 新增一个 `go run -tags migrate` 可调用的完整性检查任务：`record_class` 与已存在的扩展表关联一致（如果某 Ticket 的 `record_class=incident` 但查不到对应 `work_item_id` 指向它的 Incident 行，报告为异常，不自动修复）。这是设计文档 §18.3-9 的"数据完整性检查任务"。

### 4.3 CallbackRegistry 与 BPMN 触发改造

- `service/bpmn/bpmn_callback_registry.go`：Handler 接口保持不变（不引入新抽象），但**重新审查**当前已知直接绕开领域服务改 Ent 的两处（`ticket_handler.go:150`、`incident_handler.go:85,145`——按 AGENTS.md"已完成的历史结论必须重新验证"的教训，这两处是今天核实到的证据，执行时必须重新读一遍确认没有变化，不能直接假设结论仍成立）。把这几个直接 `UpdateOneID().Set...()` 的调用改为调用对应领域 service 的窄接口方法。`change_handler.go` 因为 Track4 已经收口，本阶段只读不改（除非重新核实发现审批之外的部分仍有直接 Ent 写）。
- `TriggerProcess`/`TriggerByBusinessType` 调用点（`ticket_service.go:699`、`incident_service.go:1733`、`problem_service.go:338`、`handlers/change/service.go` 内的调用、`release_service.go:112`）：**本阶段不改**——把 `BusinessID` 换成 WorkItem ID 依赖 Wave 2 各域先建好 `work_item_id` 关联，这是 Wave 2 任务包的范围（见 §5），Wave 1 只负责让 `ProcessInstance` 有地方存这个字段。

### 4.4 前端共享 WorkItem Shell 骨架（评审补充：锁定契约，不只是骨架）

- 新建 `itsm-frontend/src/components/work-item/WorkItemShell.tsx`：提供设计文档 §17.3 列的公共区块骨架（编号、标题、状态、优先级、请求人、分派、SLA、流程、评论、附件、时间线、关系），专业 Tab/Panel 用具名 slot 传入，本阶段不实现任何专业 Panel（那是 Wave 2 各前端任务的范围）。
- **必须同时导出并锁定以下契约**，Wave 2 的 4 个前端任务包直接消费，不允许各自重新定义形状：
  - `WorkItemShellProps` TypeScript 接口：`workItem`（公共字段）、`actions`（后端计算的操作权限，形状对齐后端 §12.2 示例的 `actions` 结构）、`professionalPanelSlot`（具名 slot，专业 Panel 挂载点）、`onActionDispatch`（统一的操作分发回调，专业 Panel 触发 approve/resolve 等动作时走这个回调，不自己拼 API 调用）。
  - 一个 `WorkItemContext`/`useWorkItemContext()` Hook：暴露当前 WorkItem 的公共状态、SLA 倒计时（一个计算好的 `remainingSeconds`/`isBreached`，不要求每个 Panel 自己重新算时间差）、评论/附件的挂载点组件（`<WorkItemComments />`/`<WorkItemAttachments />`，Panel 直接引用，不重新实现一套）。
- 目的：避免 4 个 Wave 2 任务包各自发明一份 Shell 或猜测 Props 形状，合并时在集成点（而不是各自内部）打架——这类问题只有 Wave 3 的整分支评审才会发现，成本比现在锁死接口高得多。
- 验收标准相应增加：`WorkItemShellProps`/`WorkItemContext` 有类型测试或至少一个消费示例（哪怕是占位 Panel），确认接口可用而不是只存在于类型声明里。

### 4.5 验收标准

- `go generate ./ent && go build ./...` 通过。
- `go test ./...` 全绿（含 Wave 0 此时已经合入的回归测试）。
- 新增的完整性检查任务能正确识别一个人为造出的不一致样本（`record_class=incident` 但无对应 Incident 行）。
- `npm run type-check` 通过；`WorkItemShell` 有一个最小的 Storybook/单测确认它能渲染任意 children。
- 提交前对照 §4.1 表格逐行确认已完成，逐行给出 file:line 证据（不是"已完成"这种自我声明——即使是我自己做，也要按同样的标准要求自己）。

### 4.6 Wave 1 完成记录

**完成日期**：2026-08-27

**最终 commit**：`4bda61b0`（feat(work-item): add shared WorkItemShell, context, and Props contract for Wave 2 frontend tasks）

**验收结果**：全部通过

- `go build ./...` ✓ 无错误
- `go test ./...` ✓ 全绿（无 FAIL 行）
- `npm run type-check` ✓ 无错误
- Schema 表 §4.1 逐行验证 ✓ 所有字段/索引已创建
- Task 1-7 的 "Produces" 接口 ✓ 已验证全部存在
- WorkItemShell 新增单测 ✓ 2 tests passing

**已知范围/计划调整**（供 Wave 2 参考）：

1. **Task 3 的工作被 Task 2 吸收**：原计划 Task 3 单独修复 `recordApprovalDecision` 读取 `ProcessInstance` 结构化字段而非 Variables JSON；实际在 Task 2 的 `StartProcess` 接口设计时，发现这个问题需要先有 ProcessInstance 的结构化字段才能解决，所以 Task 2 同步完成了结构化字段的写入和 Task 3 的修复，Task 3 最后只作为回归测试验证存在。两个任务因此共享了同一个 commit（`caf8992b`），不影响功能但与原计划的任务边界有所重叠。

2. **Task 5/6 的 CallbackRegistry 访问模式**：原计划说"重新审查当前已知直接绕开领域服务改 Ent 的两处"，实际执行时发现需要在 bootstrap 里从 `CustomProcessEngine` 访问内部的 `CallbackRegistry` 去注入服务。解决方案是在 `CustomProcessEngine` 增加了 `CallbackRegistry()` 公共访问器，供 bootstrap 用类型断言 `processEngine.CallbackRegistry().GetHandler("ticket_service_handler").(*bpmn.TicketServiceTaskHandler)` 完成延迟注入。这个模式是对原计划"窄接口"原则的保留方案——不引入新抽象（接口保持不变），只暴露既有私有字段的访问器。

**没有遗漏或缺陷**：所有 Wave 1 设计文档 §4 列的需求点已完成，Wave 2 可按计划开始。

**给 Wave 2 读者的两个遗留点**（均非阻塞项，写在这里避免和新回归混淆）：

1. `cmd/check_work_item_integrity` 在本次验收所用的沙箱环境里没有真实 Postgres，因此没有对着一个活库跑通过；命令本身的逻辑已由 Task 4 新增的 enttest 单测覆盖并通过。Wave 2 执行者第一次拿到真实开发库时应补跑一次这个命令，确认端到端可用。
2. 前端 Jest 有约 50 个失败用例（`sla-api.test.ts`、`template-api.test.ts`、`ServiceRequestPanel.test.tsx` 等），是 Wave 1 改动之前就存在的问题（字段命名 camelCase/snake_case 不一致、pointer-events 相关），已经过 controller 独立复核确认与本计划无关。Wave 2 执行者跑 `npx jest` 看到这些失败时不要误判为自己引入的新回归——对照失败用例文件名即可分辨。

3. **`process_instances.business_type` 用的还是迁移前的词表（全分支评审补充）**：Wave 1 给 `ProcessInstance` 加了结构化的 `business_type`/`business_id` 两列，但实际写进 `business_type` 的是 `dto.BusinessType` 的取值（`ticket`/`change`/`incident`/`service_request`/`problem`/`release`，见 `itsm-backend/dto/bpmn_process_trigger_dto.go`），不是本文档和设计文档使用的 recordClass 词表（`generic`/`service_request_item`/`incident`/`problem`/`change_request`/`catalog_task`）。两个词表有两个值对不上：`change` vs `change_request`、`ticket` vs `generic`。

   这是有意为之的迁移期状态：Wave 1 阶段各专业域还没有自己的 WorkItem，流程触发方只知道自己的域名字，写不出 recordClass。**收敛到 recordClass 是 Wave 2 各域迁移任务的责任**——每个域在建立 `work_item_id` 关联、拿到 WorkItem 之后，同步把该域触发流程时写入的 `business_type` 改成对应的 recordClass，并为存量行准备一次转写（可参考 `cmd/backfill_process_instance_business_identity` 的形状）。在那之前，任何按 `business_type` 过滤流程实例的新代码都必须用 `dto.BusinessType` 常量，不要用 recordClass 字面量。`ent/schema/process_instance.go` 上的字段注释已同步说明这一点。

---

## 5. Wave 2：四个域迁移任务包（分发给不同工具，真正并行）

前提：Wave 1 已合入 `refactor/unified-work-item`，四个任务包都从这个分支拉 worktree。

### 5.1 任务包：Incident 迁移到 WorkItem

- **范围**：`itsm-backend/handlers/incident/**`、`itsm-backend/controller/incident_controller.go`（如仍有遗留逻辑）、`itsm-backend/service/incident_service.go`、`itsm-frontend/src/components/incident/**`、`itsm-frontend/src/app/**/incidents/**`。**不改** `ent/schema/*`（Wave 1 已经建好 `work_item_id` 字段）、不改 `WorkItemShell.tsx` 本体（只消费它）、不改其他三个域的文件。
- **现状证据**：Incident 与 Ticket 目前零结构关联；服务目录 `itsm_type=Incident` 绕过 Ticket（`handlers/service_request/service.go:85-87`）；`incident_service.go` 用自己的主键触发 BPMN（`:1733`）；`incident_handler.go`/`repository_impl.go` 整个包在 `internal/bootstrap/app.go` 里已经不接路由（"has been removed from router config"），只被 `srIncidentBridge` 间接保留——**这次迁移顺带确认这个包是否可以整体删除**，如果确认可删，删除后把真正在用的 Incident 逻辑迁到哪里由执行者根据现状判断并在交付说明里写清楚。
- **必须完成**：为每条 Incident 建/关联 `work_item_id`；创建路径改为事务内同时写 WorkItem 公共字段和 Incident 专业字段；服务目录 Incident 路径改走同一专业服务（不再绕过）；`TriggerProcess` 的 `BusinessID` 改传 WorkItem ID，`BusinessType` 传 `"incident"`（这是 `processbinding` 匹配用的值，需要同步检查 `config/seed/default.json` 里 incident 的绑定是否需要调整）；迁移存量运行中的 ProcessInstance（按 §15.2.6 的 8 条规则，重点是"无法唯一映射的实例停止迁移并输出异常清单"）；前端接入 `WorkItemShell` + Incident 专业 Panel。
- **验收标准**：`go test ./handlers/incident/... ./service/... -cover` 通过且不低于 Wave 0 建立的基线；`go build ./...`；`npm run type-check`；Playwright 走一遍"服务目录报障→Incident 创建→acknowledge→resolve→close"真实浏览器路径；`grep -rn "Incident.Create\|Incident.Update" service/bpmn/incident_handler.go` 确认不再有绕开领域服务的直接 Ent 写。
- **禁止项**：不引入"Incident 和 Ticket 两边都保留一份权威字段"的过渡态；不delete 存量数据；`record_class` 一旦为某行写入不可再改。

### 5.2 任务包：Problem 迁移到 WorkItem

- **范围**：`itsm-backend/handlers/problem/**`、`itsm-frontend/src/components/problem/**`、`itsm-frontend/src/app/**/problems/**`。不改其他域文件、不改 `ent/schema/*`。
- **现状证据**：Problem 与 Ticket 现在只有一条后加的可选多对多 edge（不是创建时必建）；架构评估报告已确认存在"两套调查入口并存"（`handlers/problem/handler.go` 的 `/problems/:id/investigate` + 独立的 `ProblemInvestigationController`）。
- **必须完成**：为每条 Problem 建/关联 `work_item_id`；把现有多对多 `tickets` edge（无方向性的一般关联，不是"调查根因"那条 `investigated_by`/`caused_by` 关系——那是 Incident→Problem 方向，跟这条 Problem→任意 Ticket 的 edge 是两回事）迁移到 `WorkItemRelation`，`relation_type` 用 `related_to`，旧 edge 数据迁完后删除；**顺带解决"两套调查入口"**（选一个权威，删除另一个，这是同一批文件里的死代码清理，不算范围外）；Known Error 发布入口接入统一时间线/审计；前端接入 `WorkItemShell`。
- **验收标准**：同 5.1 的模式（对应 Problem 域）；额外要求 `router.go` 里两个调查入口只剩一个。
- **禁止项**：同 5.1。

### 5.3 任务包：Change 迁移到 WorkItem（不动审批）

- **范围**：`itsm-backend/handlers/change/**`（**排除** `SubmitChange`/`TransitionStatus` 的 approve/reject 分支和 `service/bpmn/change_handler.go` 里跟审批相关的部分——这些已经在 PR#6/Track4 收敛，本任务包只处理 WorkItem 委托，不重新设计审批）、`itsm-frontend/src/components/change/**`。
- **现状证据**：`related_tickets` 是无结构 JSON；Change 当前用自己的主键触发 BPMN 和做审批桥接（`processinstance.BusinessKey("change:%d", changeID)`——这个约定在迁移到 WorkItem ID 之后必须同步改，否则 Track4 刚收敛好的审批桥接会因为 `businessKey` 对不上而失效，这是本任务包**风险最高的一步**，务必先在测试环境验证审批全流程（提交→CAB 批准→排期→实施→关闭）在切换 `businessKey` 后仍然正常，再合并）。
- **必须完成**：为每条 Change 建/关联 `work_item_id`；`related_tickets` JSON 转成 `WorkItemRelation`，`relation_type` 用 `related_to`（这是无方向性一般关联，不是 `requested_change`——后者按设计文档 §10.2 特指"Requested Item → Change"这个方向，`related_tickets` 现在存的是任意关联工单，语义对不上，不要用错关系类型）；**同步修改** `SubmitChange`/`completeChangeApprovalTask`/`findPendingApprovalTask` 里所有用 Change 主键拼 `businessKey` 的地方，改用 WorkItem ID，且要迁移存量运行中的流程实例（按 §15.2.6）；前端接入 `WorkItemShell`。
- **验收标准**：除 5.1 模式外，**必须**新增一个端到端回归——用切换前后的两种 `businessKey` 格式分别起一个流程实例，确认迁移脚本能正确处理两种情况，且 Track4 已有的 `service_bpmn_test.go` 全部仍然通过（这是最强的回归信号，不能破坏它）。
- **禁止项**：同 5.1，额外禁止修改审批的判权逻辑（`authorizeTaskActor`/`assigneeRole=change_manager`）。

### 5.4 任务包：ServiceRequest 层级规范化

- **范围**：`itsm-backend/handlers/service_request/**`、`itsm-frontend` 里服务目录/服务请求相关组件。**不改 `ent/schema/*`**——`target_class` 字段已经在 Wave 1 §4.1 加好，这里只消费。
- **现状证据**：`ServiceRequest.ticket_id` 已经是委托模式，不需要新建 `work_item_id`（沿用现有列，语义上视为 WorkItem ID，不做物理改名）；`form_data` 和 `field_values` 真实双写（§3.4 已锁定基线）；`ServiceCatalog.itsm_type`（决定审批路由）和 `service_type`（业务表单/资源类型）语义混用，`category` 是无约束字符串；`TicketTemplate.categoryIds` 在 create/update 路径完全被丢弃（比设计文档描述的"可能有缺口"更严重——`CreateTemplateRequest`/`UpdateTemplateRequest` 结构体压根没有这个字段）。
- **必须完成**：`ServiceCatalog` 新增 `target_class`（枚举，替代 `itsm_type` 的裁决职责，`itsm_type`/`service_type` 按设计文档 §7.2 拆分职责）；停止 `form_data`/`field_values` 双写，选 `field_values` 为唯一权威（`form_data` 只保留非结构化上下文，按设计文档 §8.3）；修复 `TicketTemplate` 的 `categoryIds` 写路径（这是一个独立发现的真实 bug，不是"顺手做的范围外重构"——不修的话新模型下这个字段会继续是死的）；前端接入 `WorkItemShell`。
- **验收标准**：`go test ./handlers/service_request/... -cover` 不低于 Wave 0 基线；新增测试断言"提交表单后 `field_values` 有值，`form_data` 不再包含重复的结构化字段"；`categoryIds` 的 create/update 集成测试（提交非空 `categoryIds`，读回确认落库）。
- **禁止项**：不新增 `itsm_type`/`target_class` 两个字段并存的过渡态超过这一次提交——要么这次切完，要么明确记录为需要迁移窗口的独立后续项。

---

## 6. Wave 3：整合与最终评审（我自己执行）

- 全部 Wave 2 任务包合入 `refactor/unified-work-item` 后，做一次**覆盖整个集成分支的最终评审**（不是四次任务级评审的简单叠加）——重点检查跨任务集成点：四个专业 Panel 对 `WorkItemShell` 的用法是否一致、`WorkItemRelation` 的 `relation_type` 取值是否在四个域之间保持语义一致、`ProcessBinding` 的 `business_type` 取值集合是否真的收敛（不再有历史的 `ticket+incident` 两层形状残留）。
- Phase 6 清理项（设计文档 §18.8）：删除重复公共字段和旧写路径、死服务、旧审批入口残留（`ticket_type.go` 的 `approval_workflow_id`/`approval_chain`、`docs/docs.go` 过期 swagger）。
- 决定是否物理重命名 `tickets` → `work_items`（设计文档 ADR-1 建议第一阶段不做，这里重新评估，不预设结论）。
- 全量验证：`go test ./...`、`npm run type-check`、相关 E2E、`docs/architecture-and-roadmap-assessment-2026-08-26.md` 里列的实测指标（覆盖率、controller 体积）重新测一遍存档对比。
- 通过后才合入 main。

## 7. 风险与缓解（补充设计文档 §23，聚焦多 agent 执行特有的风险）

| 风险 | 缓解 |
|---|---|
| Ent codegen 跨 worktree 冲突 | 见 §0，全部 schema 变更收在 Wave 1 单点完成 |
| 不同工具产出质量参差 | 每个任务包回来先经我重新跑验收命令，不信任自我报告；不达标打回重做或换工具 |
| 任务级评审通过不代表整体正确 | Wave 3 强制做一次覆盖全分支的最终评审，不省略 |
| Change 任务包改 `businessKey` 格式，动到 Track4 刚收敛好的审批 | §5.3 要求先双格式回归测试通过、`service_bpmn_test.go` 全绿才能合并 |
| 执行工具不了解本次会话已核实的现状，重新调查一遍浪费 token/引入误判 | 每个任务包内嵌"现状证据"章节，直接给结论和 file:line，不要求重新调查 |
| "已完成"的历史结论过期（本次会话已经在 P0 审批上踩过一次） | 任务包模板第 2 部分明确要求执行者在动手前用给定的 grep/读码方式重新确认一次现状证据仍然成立，不是无条件信任 |

## 8. 与领域模型设计文档的映射

| 领域模型设计章节 | 对应 Wave/任务包 |
|---|---|
| §6.2 WorkItem 基础表 | Wave 1 §4.1 |
| §6.4 专业扩展表（Incident/Problem/ChangeRequest/ServiceRequestItem） | Wave 1 §4.1（schema）+ Wave 2 §5.1-5.4（数据回填与业务逻辑） |
| §7 服务目录目标模型 | Wave 2 §5.4 |
| §8.3 动态字段归属 | Wave 2 §5.4 |
| §10 跨域关系模型 | Wave 1 §4.1（建表）+ Wave 2 §5.1/5.2/5.3（各域迁移自己的关系数据） |
| §15.2 BPMN 身份契约 | Wave 1 §4.1/4.3（结构化字段+接口收紧）+ Wave 2 各任务包（迁移各自的 businessId） |
| §17.3 详情页 Shell | Wave 1 §4.4（骨架）+ Wave 2 各任务包（专业 Panel） |
| §18 数据迁移方案 Phase 0-6 | Phase 0→Wave 0，Phase 1→Wave 1，Phase 2-5→Wave 2 对应四个任务包，Phase 6→Wave 3 |
