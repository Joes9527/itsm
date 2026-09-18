# 通用履约门禁（generic_fulfillment_v1）设计

- **Status: draft（待维护者确认）。** 本文由实现方整理，尚未经独立审查或维护者定稿，不得作为已验收契约引用。
- **源码状态：** 实现位于 PR #77（分支 `codex/fix/generic-gate-scope`，19 提交、43 文件，未合并、未部署）。按治理 §7，涉及 BPMN 的变更须由独立审查者复核，实现 Agent 不能作为唯一验收者。
- **相关：** [统一 WorkItem 模型设计](2026-08-26-unified-work-item-model-design.md) §15.2.3（绑定优先级与"无需流程"策略）、[工程治理](../../agent-engineering-governance.md)。

## 1. 业务目标与角色

**流程管理员**：为通用工单定义一个受系统约束的履约流程，确保经办人不能跳过流程节点、也不能在缺少证据时关闭工单。

**一线工程师**：在任务视图里看到"为什么现在还不能完成这个任务"，而不是一个永远灰着的按钮。

要解决的问题：通用工单（`recordClass=generic`）经由 BPMN 履约时，其流程定义与工单状态之间此前没有强制对应关系——流程可以被绕过，工单可以在流程未推进时被直接改成 resolved/closed，且没有任何证据要求。

## 2. 契约声明方式

契约是**定义上的显式选用（opt-in）**，不声明即不适用：

- 在 `<process>` 的扩展元素元数据上声明 `workItemLifecycleContract=generic_fulfillment_v1`。
- 元数据**至多声明一次**，且不得为空值（`strictLifecycleMetadata`）；重复或空值按定义错误拒绝。
- 声明了不支持的契约值同样按定义错误拒绝。
- 一个定义声明该契约时，**必须只包含一个流程**（`generic_fulfillment_v1 requires a single process`）。

未声明该元数据的定义**完全不受本契约约束**。

## 3. 契约对定义的约束

声明契约后，定义必须满足（`validateWorkItemLifecycleProcess`）：

- 只支持**顶层用户任务**；不得含子流程、调用活动、服务任务、脚本任务、业务规则任务、人工任务。
- 任务不得使用 handler 或 action 元数据。
- 每个用户任务可声明可选前置条件 `workItemPrerequisite`，取值限定为 `assigned` / `in_progress` / `escalated` / `resolved` / `closed`；其它取值按定义错误拒绝。
- 声明了前置条件的任务必须是 `taskPurpose=fulfillment` 且 `assigneeSource=work_item_assignee`，即**负责人绑定的履约任务**。

## 4. 作用域边界（本设计的核心约束）

门禁**只在一个交集内生效**，两者缺一不可：

1. **工单侧**：`recordClass=generic`。`ValidateWorkItemLifecycleRecordClass` 在契约已声明且 `recordClass != "generic"` 时报错；非 generic 的工单不因本契约改变任何行为。
2. **定义侧**：该流程定义**确实声明了** `generic_fulfillment_v1`。

### 4.1 与"无需流程"策略的关系

[统一 WorkItem 模型设计](2026-08-26-unified-work-item-model-design.md) §15.2.3 第 4 条规定："没有可用绑定时返回明确错误或执行产品配置的'无需流程'策略，禁止静默选择其他专业类流程。"

"无需流程"是本系统**既有的、正式的业务状态**，不是配置错误：

- 绑定层：`ProcessBinding` 条件 `no_process: true` 时，创建路径直接返回 `ResolvedWorkflowBinding{NoProcess: true}`，并把 `no_process` 写入 `intake_resolutionsnapshot` 留痕。
- 门禁层：`loadGenericWorkflowGate` 读到 `snapshot.NoProcess` 时返回空门禁；启动准入要求 `!snapshot.NoProcess`。**即门禁明确豁免"无需流程"工单，这是既有且正确的处理。**

本契约不改变"无需流程"策略，也不替代它。

### 4.2 创建路径不重新校验流程定义（定稿决定）

**创建路径只做契约探测，不校验定义合法性。**

理由：

- 定义能否入库由**发布校验**（`ValidateDefinitionForPublication`）负责，这是定义合法性的权威关口。
- 实现引入前，创建路径**从不解析**流程定义的 XML，只按 key/version 选定定义并冻结摘要。
- 若创建路径重新解析并校验，则解析器拒绝的**任何**定义——包括不含任何流程的定义——都会导致建单失败，且**不分工单类型**，与本契约第 4 节声明的作用域不符。

因此：无法解析的定义**不可能声明本契约**，创建路径据此不冻结任何标志，建单行为与本契约引入前一致（`genericBindingConfig`）。定义若确实损坏，由流程启动阶段可见地失败，而非在建单入口静默改写行为。

## 5. 创建期：探测与冻结

创建时若目标定义声明了本契约，则：

- 从定义自身的 `ProcessVariables` 读取 `approval_required`、`need_escalate`（必须是 JSON 布尔，否则按定义错误拒绝），作为**定义所属的可信分支标志**冻结进工单的工作流变量。
- 拒绝公开调用方传入保留输入：`approval_required`、`need_escalate`、`approvalResult`、`workItemCompletionNote`。这些是定义配置或流程内产物，不是用户输入。
- 绑定创建时，声明了契约的定义只接受 `generic` 业务类型的绑定。

标志来源必须是定义，不能是请求；冻结发生在建单事务写入之前。

## 6. 启动期：冻结创建证据准入

`admitGenericWorkflowStart` 要求通用履约流程的实例只能由**创建事务冻结的证据**启动：

- 必须在私有的 intake 投递上下文中执行；公开启动路径即使不传保留输入也会被拒绝（`generic lifecycle workflow requires frozen creation delivery`）。
- 校验实例身份、租户、工单、收据 ID 与摘要一致。
- 读取 `intake_resolutionsnapshot`，要求非"无需流程"、`recordClass=generic`、且定义 ID/key/version/摘要与当前定义完全一致。
- 读取对应的 `completed` 建单收据，校验请求摘要、渠道、actor 租户一致。
- 冻结变量与本次传入变量必须**逐字节相等**，且身份键（`work_item_id`、`tenant_id`、`record_class`、`requester_id`、`triggered_by`、`channel`）必须匹配。
- `approval_required` / `need_escalate` 必须与定义配置一致；新实例一律采用定义的值，绝不采用普通启动输入。
- 冻结证据中不得含 `approvalResult` 或 `workItemCompletionNote`。
- 重放保留原启动摘要，但**不得**把公开请求变成 intake 授权路径。

## 7. 完成期：阶段前置条件门禁

`EnforceGenericWorkflowTransitionTx` **设计为**在工单的版本化命令事务内执行。目标状态为 `in_progress` / `resolved` / `closed` / `manual_escalation` 时，要求流程正在运行，并按目标状态校验：

> **接线状态（截至 PR #77）：本节规则尚未在生产路径生效。** `EnforceGenericWorkflowTransitionTx` 与 `RejectGenericWorkflowLegacyMutationTx` 在本 PR 中**没有任何生产调用方**（唯一的非定义引用是 `generic_workflow_gate_test.go` 中的断言）。本 PR 唯一接进生产的是只读投影 `GenericWorkflowTaskGate`（任务视图据此不显示「完成」按钮）与启动准入。因此通用工单目前仍可经由普通状态命令被改成 `resolved` / `closed` 而不触及以上任何校验。调用方落在并行的 a3 lifecycle-gate / legacy-gates 变更里；在那之前，本节应读作**契约定义**而非**已生效的运行时约束**。

| 目标状态 | 要求 |
| --- | --- |
| `in_progress` | 当前等待阶段为 `in_progress`；若定义要求审批，须已有审批通过 |
| `manual_escalation` | 定义启用升级；当前等待阶段为 `escalated`；已存在处理证据 |
| `resolved` | 当前等待阶段为 `resolved`；已存在处理证据；定义要求升级时须已升级 |
| `closed` | 当前等待阶段为 `closed`；已存在解决确认 |

`escalated` / `resolved` / `closed` 三类证据取自 `auditlog` 的**版本化操作收据**（`genericWorkflowReceipt`）：要求 `OperationID`、64 位 `RequestDigest`、`ResultVersion > 0`、操作人、请求体齐全，且状态确实发生迁移；升级收据还要求优先级确实变化且带原因。仅凭状态相同或缺失元数据的记录不算证据。

同时：

- 声明了契约的工单拒绝遗留（非版本化）变更命令（`RejectGenericWorkflowLegacyMutationTx`）。
- 多个生效实例、多个活跃阶段均判为歧义并拒绝，而非任选其一。
- `GenericWorkflowTaskGate` 提供**只读投影**给任务视图，返回阻塞原因与是否需要处理说明；缺失处理文本表现为"需要输入"，而不是永久禁用的动作。

## 8. 明确不在范围内

- **专业生命周期**（Incident / Problem / Change / Service Request）的状态机与转派规则不由本契约改变。
- **"无需流程"策略**不由本契约新增或修改。
- 前端任务视图的阻塞原因呈现（`a4-blocked-ui`）与遗留变更拒绝的扩展面（`a3-legacy-gates`）不在本设计的第一批范围。
- 定义发布校验本身不在本设计范围内；本文只声明创建、启动、完成三个边界的行为。

## 9. 交付边界与未决问题

**合并前必须满足：**

1. 本文状态由维护者确认为 `accepted`。
2. 独立审查者复核（治理 §7）；实现方不得作为唯一验收者。
3. 全量后端套件在目标分支上为绿。

**已知未决：**

- 创建路径"不重新校验定义"（§4.2）是一个**显式决定**，其退役条件是：若产品后续要求建单时即校验定义合法性，须作为**独立变更**重新设计，不能在契约内隐式引入。
- `hasGenericFulfillmentContract` 中 `len(definitions.Processes) == 0` 的拒绝分支当前为**防御性**代码：所有调用点都先经 `ParseXML`，而解析器已拒绝零流程定义。其保留与否待复审确认。
- 门禁要求"单一流程 + 顶层用户任务"是否足以覆盖真实履约场景（例如后续需要并行或子流程），未验证。

## 10. 验证证据

实现分支上的相称验证（截至 2026-09-18，`origin/main` 为 `MAIN_EXIT=0`、63 包全绿）：

- 契约探测不影响未声明契约的定义：新增回归测试覆盖全部六个 record class × 解析器拒绝 / 畸形 XML 两种定义，断言建单正常解析且定义标志未被冻结。
- 门禁对声明契约的定义仍然生效：既有测试覆盖专业类型绑定拒绝、公开启动拒绝、保留输入拒绝。
- 全量套件结果以交付分支上的实际运行记录为准，不以本文为准。
