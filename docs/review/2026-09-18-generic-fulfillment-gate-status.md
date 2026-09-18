# 通用履约门禁（generic_fulfillment_v1）：交付状态与遗留缺口

- 状态：**命令侧已交付并接线；旧命令路径仍可绕过门禁**（见第 3 节，这是本文存在的理由）。
- 日期：2026-09-18。
- 证据基线：`main` 的 `1c52edce`（`Merge pull request #77`）。本文所有结论都回代码核实过，不是从文档或 roadmap 抄的。
- 权威契约：[AGENTS.md](../../AGENTS.md) 的 "Generic fulfillment gate contract" 一节。本文记录背景、边界和未决项，**不维护第二份规则**；两处不一致时以 AGENTS.md 为准。
- 代码入口：`itsm-backend/service/generic_workflow_gate.go`（事实装载与两个命令钩子）、`generic_workflow_completion.go`（完成命令）、`itsm-backend/service/bpmn/callback_contract.go`（回调契约）。

## 1. 已交付并已验证

PR #77（merge `1c52edce`）把门禁带进了 `main`，其中两个提交值得单独记：

| 提交 | 内容 |
|:---|:---|
| `4fd641e2` | 履约证据改为**单调累加** |
| `59527c14` | 完成命令的测试迁到同名文件，过 `Source/Test Coverage Guard` |

门禁的**命令侧已接线**，这不是推断，是调用点：

- `service/ticket_service.go:422` — 版本化编辑命令，在自身事务内调 `EnforceGenericWorkflowTransitionTx`。
- `service/ticket_escalation_command.go:75` — 升级命令，同样在自身事务内调用。

`4fd641e2` 修的是真 bug，不是风格问题：事实原来是**每任务覆盖赋值**，而契约允许**多个任务声明同一个 prerequisite**，每个任务从自己的开始时间查回执。于是后一个任务更窄的时间窗会**清掉**前一个任务已经确立的事实——一个已经 resolved、但第二个 `resolved` 阶段晚于解决回执才开始的 WorkItem 会丢掉 `Resolved`，**永远无法关闭**。改成 `||` 累加后，`TestGenericWorkflowFactsAccumulateAcrossStages` 守住它；把算子改回覆盖赋值，该测试立刻红，是变异验证过的。

## 2. 契约要点（改这块必须守住）

- **证据单调**：任务只增不减。多任务共用同一 prerequisite 是契约允许的，后阶段不得清前阶段的事实。
- **`approval_required` / `need_escalate` / `approvalResult` 是保留字段**，永不接受调用方传入；审批意图只在控制器校验过的决策边界和投票聚合处生成。
- **换负责人需要 `assignmentReason`**，且随操作回执落库。
- **未知/不可解析/非法的定义一律 fail closed**，且 `generic` 工单的转换由本契约判定，不再有第二条状态规则。
- **只有 `in_progress`、`resolved`、`closed`、`manual_escalation` 四个目标状态被门禁拦截**，其余状态直接放行。不要把 switch 扩成"看起来不正常就拦"。

## 3. 遗留缺口：旧命令路径绕过门禁

`RejectGenericWorkflowLegacyMutationTx`（定义在 `service/generic_workflow_gate.go:351`）**没有任何生产调用点**——`git grep` 只命中它自己的定义和注释。

后果是真实的：两条旧写入路径用自己的 repository 调用改状态，**既不调 `EnforceGenericWorkflowTransitionTx` 也不调拒绝钩子**：

- `TicketService.updateTicketStatus`（`service/ticket_service.go:1078`）
- `TicketLifecycleService.UpdateTicketStatus`（`service/ticket_lifecycle_service.go:152`）

它们只过两道闸，而 `generic` **两道都能过**：

1. `rejectProfessionalTicketMutation`（`service/ticket_incident_assignment_boundary.go:15`）只拒绝 `incident` / `problem` / `change_request`。
2. `IsValidTicketStatusTransition`（`service/ticket_lifecycle_service.go:321`）允许 `open → resolved`、`in_progress → resolved`、`resolved → closed`。

所以下面这些路由今天仍能把一个 `generic` WorkItem 推到 `resolved` 再 `closed`，全程不碰契约（路由行号见 `router/router.go` 的 tickets 分组）：

- `PUT /api/tickets/:id/status`
- `POST /api/tickets/:id/assign`、`/resolve`、`/close`
- `POST /api/tickets/workflow/accept`、`/withdraw`、`/forward`、`/resolve`、`/close`、`/reopen`

**不要**把版本化调用点读成已经覆盖了这些路。

**关于已有的 `a3-legacy-gates` 分支**：提交 `4e932063`（"reject legacy mutations for gated generic workflows"，改了 `ticket_service.go`、`ticket_workflow_service.go`、`ticket_assignment_callback.go` 和一个新测试文件）确实存在，但它在一个**从未推送到远程的本地分支**上（`git ls-remote` 无此 ref），且 `ROADMAP.md:274` 记它 `conflict in service/ticket_service.go`。**当作没写过**：对着当前 `main` 重新做，别假设能直接捡起来。

## 4. 未关闭项

- **旧命令路径缺口**（第 3 节）。这是一个跨五条命令的事务重构，不是小补丁：要让这些命令要么改走版本化命令，要么在自身事务内调 `RejectGenericWorkflowLegacyMutationTx`。
- **N+1 查询**：`service/bpmn_process_engine.go:4039` 在分页循环里逐行调 `projection.taskUIActions(ctx, task)`，`service/bpmn_assignment_authorization.go:200` 同理，等于每行解析一次 BPMN XML。违反 `docs/engineering-conventions.md` 的查询约定。**故意没在 #77 里顺手改**——那条路径当时有并发会话在编辑，改了会撞。
- **相关 PR 都停在旧基点**：#76（设计文档，`docs/superpowers/specs/2026-09-18-generic-fulfillment-gate-design.md` **尚未进 main**）、#79、#80 都落后约 25 个提交。设计文档落地前，AGENTS.md 那一节就是约束性表述。
- **`backend-ci.yml` 的路径过滤**使纯文档 PR 可能凑不齐三个必需检查——本文这类改动会碰到，但修 CI 不属本 PR 范围。

## 5. 建议的下一步顺序

1. **先修第 3 节的缺口**，按上表路由逐条收口；这是唯一还能让合规流程被绕过的洞。
2. 补 PostgreSQL 生命周期的门禁用例（`ROADMAP.md:260` 的验收标准 (4) 仍缺）。
3. 再处理 N+1，因为它是性能问题不是正确性问题。
4. #76 的设计文档要么 rebase 落地、要么显式废弃，别让它继续挂在旧基点上误导人。

## 6. 已拍板、不要重开的事项

- **`assign` 没有固定回退**，所以 `service/bpmn/callback_contract.go:114` 为它设 `RejectInvalidUserInput = true`：`Activity_Assign` 的处理人只能来自操作者，没有兜底值，无法提供时**必须拒绝**而不是落一条永远不可能成功的回调。`update_status` 相反——`updateTicketStatus` 会把缺失的 `new_status` 兜成 `in_progress`，所以**不设**该标志。这是**动作级的静态属性**，不是运行时判断，因此"配置固定的任务会被误禁用"这一担忧不成立（回答了 #47 留下的 I1）。
- **冻结输入被拒是有意行为**，有测试守着，不是 bug。
- **`generic` 之外的 recordClass 不走这套门禁**，Incident / Problem / Change 各自的专业生命周期不变。
