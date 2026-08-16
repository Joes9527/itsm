# 完整性审计整改 — Design Spec

**Status:** Approved (user confirmed 2026-08-16: "同意你报告中的分析，请按报告中的建议生成task,顺序 执行")

## Background

2026-08-16 对 ITSM 平台三个模块（流程引擎、审批链配置、自定义表单）做了功能完整性审计，覆盖分支
`track4-change-approval-bpmn-migration` @ `c098995d`。审计发现的共同模式：不是报错，而是"看起来配好
了、跑起来什么也没发生"（静默失效）。完整审计报告（含逐条 file:line 证据）已发布为 Artifact：
https://claude.ai/code/artifact/7fa5a069-5fea-44e9-81b7-a394eb1468cd

用户已确认同意报告分析，要求按报告"建议优先处理顺序"章节列出的 6 条建议逐条生成任务、按顺序执行。

## Scope decisions (confirmed by user via AskUserQuestion, 2026-08-16)

1. **分支策略**：这轮修复在独立的新分支/worktree 里做（`worktree-itsm-completeness-remediation`，
   base `origin/main`），不并入 `track4-change-approval-bpmn-migration`（PR #6，专注变更审批迁移，
   范围不应扩大到工单/流程引擎/自定义表单）。
2. **serviceTask 自动化边界**：选择"把打桩 handler 做成真实实现"（而非"止血+标注限制"的小方案）。
   这意味着 Task 4 需要：(a) 给 `BPMNServiceTask` 补 `ExtensionElements` 解析，让引擎能读到
   `service_task_type` metaData；(b) 把 `ServiceRequestServiceTaskHandler`
   （`service/bpmn/service_request_handler.go`）的 8 个动作做成真实 Ent 写入；(c) 把
   `GenericServiceTaskHandler`（`service/bpmn/generic_handler.go`）在能通用化的范围内做成真实动作
   执行器；(d) 给 `release_task` 建一个新的 `ReleaseServiceTaskHandler` 并接入回调注册表。

## The six tasks (report priority order — execute in this order)

1. **工单审批链 Tab 静默失效** — `ApprovalWorkflowPanel.tsx` 调用已删除的
   `/api/v1/approval-workflows`，请求失败被静默吞掉，永远显示"未走审批流程"。改为读
   `ProcessApprovalDecision`（走后端变更域已验证过的 `GetApprovalHistory` 同款读法，工单域需要新增
   等价的只读查询接口）。同时删除死链的 `/admin/approvals` 页面与 `ticket-approval-api.ts`。
2. **BPMN 版本管理 `is_latest` 破坏** — `BPMNVersionService.CreateVersion`/`ActivateVersion` 从不
   维护 `is_latest`，导致同一 `key` 可能有多行 `is_latest=true`，`GetLatestProcessDefinition` 取到
   不确定的行。补事务性的"新版本置为最新、旧版本降级"逻辑，参照
   `bpmnProcessDefinitionService.CreateProcessDefinition` 已经写对的写法。
3. **流程设计器缺少"不支持元素"提示** — bpmn-js 调色板允许自由画并行/包容网关、定时器、消息事件、
   子流程，后端引擎对这些要么静默单分支执行要么完全忽略。给设计器校验器（`BPMNDesigner.tsx`
   `handleValidate`）加一条规则：检测到这些元素类型时给出明确警告，说明当前引擎不支持。
4. **serviceTask 自动化能力补全（真实实现，非止血）** — 见上方 scope decision 2 的四个子项。
5. **自定义字段类型校验缺失** — `FieldValueService`（`service/field_value_service.go`）存值前不检查
   `field_type`/`options`；`field_type` 本身在定义保存时（`ticket_template_service.go`
   `validateTemplateFields`）也不检查是否属于允许的 8 种类型。两处都要补最基础的格式/类型校验。
6. **工单创建校验失败留孤儿行** — `ticket_service.go` 里落库（`:165` 附近）先于必填字段校验
   （`:181-187` 附近）执行，校验失败时已落库的工单行不回滚。改成先校验后落库，或校验失败时删除已
   落库的行。

## Non-goals (explicitly out of scope this round)

- 流程引擎其余"高风险静默失效"项（`SuspendProcess` 不真正阻止任务完成、`StartProcess`/
  `CompleteTask` 缺少通用级中断恢复、并行/包容网关真正的 fork/join 语义、tenant_id=0 放行设计、
  `IncidentServiceTaskHandler` 缺租户过滤）——报告里标记为"可见缺口"或未进入"优先处理"清单的项，
  本轮不做，留待后续单独立项。
- 审批链配置的"CAB 无独立名单"“ProcessBinding.approval_chain_id 摆设”“紧急变更流程无差异化”——
  同样是可见的配置缺口，不在本轮 6 条任务范围内。
- 自定义表单的条件显隐、跨字段校验、计算字段、管理端字段构建器补齐 multiselect/boolean/file 类型
  选项——本轮只做"类型校验"这一条，UI 层的类型覆盖补齐不在范围内。

## Verification approach

- 每个任务遵循仓库既有的 TDD 约定（`stretchr/testify` + `enttest.NewClient()` 做后端表驱动测试，
  Jest + React Testing Library 做前端测试），按 CLAUDE.md "测试闭环" 要求逐任务补测试。
- 每个任务完成后跑该任务改动包的窄范围测试；全部任务完成后跑一次 `go test ./...` 和相关前端
  `type-check`/测试作为整体回归。
