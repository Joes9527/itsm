# 包1（Dev 流程阻塞修复）实施交接

> 交接对象：接手本任务的 Coding Agent。
> 本文件是**执行交接**，不是状态台账。**唯一状态台账是**
> [`2026-09-15-migration-validation-ledger.md`](./2026-09-15-migration-validation-ledger.md)
> （本次相关记录见其 `### 2026-09-16 包1 决策记录与 A3 范围评估` 一节）。两者冲突时以台账为准。

## 1. 背景

设计已完成并冻结，见 **PR #46**（分支 `codex/chore/execute-dev-restoration`，设计提交 `8af8321b`，**尚未合并**）。设计把工作分为四包：

| 包 | 内容 | 归属 |
| --- | --- | --- |
| **1** | 修复 Dev 流程阻塞：任务页提交参数与后端回调要求不一致 + 已失败测试流程的保留式处置 | **本任务** |
| 2 | 同库不同 schema 实施设计（Dev 保留 `public`，验证用 `migration_validation`；独立权限/迁移记录/缓存/附件；克隆准入；连接配错即失败） | 未开始 |
| 3 | 从**验收后的 Dev 快照**建立验证副本，复用五批对账补齐旧 ITSM 基础/业务配置，不迁旧 ticket 与历史流程；同一 3010→8080 验证，结束切回 Dev | 未开始 |
| 4 | 清理旧测试库/临时资源 → 衔接 ITSM/KAF 单 PG 实例合并 | 未开始 |

**包1 的验收标准**：新建普通工单能够完成**处理、解决、关闭**；**不能只看服务健康检查**。

包1 在台账中拆为 A1/A2/A3/A4。**A1/A2 已交付，A3/A4 未开始**——因此包1 目前**未达验收**。

**执行纪律（设计方要求）**：设计方负责设计与集成，实现 Agent 分别实现、独立审查；**共享数据库操作始终串行**；每阶段只汇报"完成／验证／阻塞／下一步"；通过后提交、推送 PR；**不自动合并**。

## 2. 分支与基线

| 项 | 值 |
| --- | --- |
| 仓库 | `/home/administrator/project/itsm`（remote `git@github.com:Joes9527/itsm.git`） |
| 实现工作区 | `/home/administrator/project/itsm/.worktrees/dev-restoration-schema-validation` |
| 分支 | `codex/chore/dev-restoration-schema-validation` |
| 基线 | `origin/main` = `b8ac9639` |
| 设计分支合入 | `--no-ff` 合入 `origin/codex/chore/execute-dev-restoration`（tip `8af8321b`），集成提交 **`f1f31b38`**，双方提交均保留 |
| PR | **#47**（open，**未合并**，待独立复核）；#46 亦未合并 |

### 提交清单

| 提交 | 内容 |
| --- | --- |
| `c3bcf597` | **A1**：`fix(bpmn): reject missing actor assign input before task completion` |
| `6c76881c` | **A2**：`fix(bpmn): withhold simple completion when the task binds actor input` |
| `77300ab7` | A1/A2 并发·重复·跨租户负例 |
| `7bd0118b` | 唯一清单回写（含冻结摘要配方） |
| `6a77d828` | `development-environment.md` 补记定义摘要口径 |
| `008ceb37` | 唯一清单：包1 决策记录与 A3 范围评估 |
| `cd7babec` | `development-environment.md` 补记库用途台账 |

## 3. 已完成（A1/A2）

### A1 —— 契约失败必须发生在任务完成之前

Dev 现场根因：`ticket_task` 的 `assign` 回调**只声明正整数规则、未声明必填**，于是无 `assignee_id` 的完成请求被契约接受、落成持久回调，反复重试至 `handler_error`（实测 **83 次**）。

实现（`itsm-backend/service/`）：

- `bpmn/callback_contract.go`：`CallbackActionContract` 新增 `NonEmptyStringFields`、`RejectInvalidUserInput`；`assign` 声明 `RequiredFields+PositiveIntegerFields=[assignee_id]`；`update_status` **不设**必填（`updateTicketStatus` 有合法默认 `in_progress`），仅"存在时须非空字符串"
- `bpmn_callback_security.go`：新增类型化 `bpmnCallbackUserInputError`，区分**可修正输入错误**与**定义缺陷**；`normalizeBPMNCallbackContractPayload` 支持 `NonEmptyStringFields`
- `bpmn_callback_enqueue_plan.go`：双变体——`BuildCallbackEnqueuePlan`（冻结负载→**blocked**，行为不变）与 `BuildCallbackEnqueuePlanForActorCompletion`（actor→**拒绝**）
- `bpmn_process_engine.go`（约 :647）：交互式完成路径改用 actor 变体。**已核实**它位于 `ProcessInstance` 变量合并与 `ProcessTask.SetStatus(completed)` **之前**，且整链在 `CompleteTask` 的 `BeginTx(RepeatableRead)` + `defer Rollback` 内 → 返回 error 即整体回滚

### A2 —— 简单入口不提供必然失败的完成按钮

`bpmn_task_ui_actions.go` 新增 `actorInputGateReason`：任务**已持久化描述符**声明 `RejectInvalidUserInput` 时不再授予 `Complete` 并给出 reason；判定为**纯行读取**（**不调用**会补写描述符的 `descriptorForProcessTask`）；不可解析的 handler/action **失败关闭**；有固定回退与无回调任务保持可完成。

**前端无需改动**（已核实）：`itsm-frontend/src/components/ticket/TicketProcessTasks.tsx:81` 已由 `uiActions.complete` 门控按钮、`:78` 已渲染 `reason`、`:128` 在标志为假时拒绝执行，未硬编码 handler 名。

### 验证证据

```
go build ./...                                                              → ok
go test ./service/bpmn -count=1                                             → ok
go test ./service -count=1                                                  → ok（整包）
go test ./service/bpmn ./service -run 'Callback|BPMNTask' -count=1           → ok / ok
INTAKE_POSTGRES_TEST_DSN=... go test -tags integration_postgres ./service \
  -run 'TestActorCompletion|TestIncidentCallback' -count=1                   → ok
npm run test:unit -- --runTestsByPath src/components/ticket/__tests__/TicketProcessTasks.test.tsx \
  src/lib/api/__tests__/bpmn-workflow-api.test.ts                            → Tests: 44 passed, 44 total
npm run type-check                                                          → 无错误
git diff --check                                                            → 无问题
```

**反向验证（关键，请保留此习惯）**：临时把调用点还原为旧行为后，`RejectsMissingAssignInput` / `RepeatedRejectionHasNoEffect` / `ConcurrentRejectionHasNoEffect` 三例 **FAIL**，证明它们确实守护 A1；而 `CrossTenantIsRejected` 两种行为下均 PASS——它守的是**既有租户隔离边界，不是 A1 证据**。

新增测试文件：`service/bpmn/callback_contract_input_validation_test.go`、`service/bpmn_callback_input_rejection_test.go`、`service/bpmn_assign_input_postgres_test.go`、`service/bpmn_task_ui_actions_input_gate_test.go`。

## 4. 未完成（A3/A4）

### A3 —— 新流程版本 + 路由切换 + 真实 UI 验收

**步骤 1 已裁定（无需代码）**：新 XML 的网关条件用解析器可读形式——用 `<![CDATA[variables['x'] == true]]>`，**不要** `<bpmn:body>` 子元素，**不要** `${...}` 包装；**不改解析器**（改 `chardata` 语义会改变所有既有定义的行为，属改变已接受设计，需另行授权）。

六个条件的源语义（仅改语法）：

| 流向 | 条件 | 备注 |
| --- | --- | --- |
| `Flow_ApprovalYes` | `variables['approval_required'] == true` | |
| `Flow_ApprovalNo` | `variables['approval_required'] != true` | |
| `Flow_Approved` | `variables['approvalResult'] == 'approved'` | |
| `Flow_Reject` | `variables['approvalResult'] == 'rejected'` | **目标改为新增 `EndEvent_Rejected`**（§2.1） |
| `Flow_4` | `variables['need_escalate'] == true` | |
| `Flow_Resolve` | `variables['need_escalate'] != true` | |

**步骤 2–8 未开始**：§2.2 合同管线与门禁、XML 派生、Dev 创建未绑定定义、合成工单走查、切 825/827、A4。详见第 7 节。

### A4 —— 冻结无效回调的保留式处置

按 §2.3：执行器**领取后、调用 handler 前**用同一 handler 契约验证冻结 payload；对已知 handler/action 且缺失/无效必需参数的持久回调，返回现有 `handler_contract` **blocked** 效果（现有 outbox outcome 持久化 blocked + 审计），**停止重试**；不填参数、不调用业务 handler、不推进 process。原 payload、execution key、attempt 历史、task completed 记录**不变**；不得宣称"先前尝试都无效果"，只证明本次未调用 handler。工单29/流程27/任务33/callback2 按此路径标 blocked，实例保留 `running+blocked` 真实状态。

## 5. 环境事实（当前）

### 运行入口

| 服务 | 端口 | 状态 |
| --- | --- | --- |
| ITSM 前端 | 3010 | **由 stack 权威管理**：`running (PID 1503435)`，`source_revision=2988819c94cfcfb69cee5b0d1dfbe69ee0fb8c9b`，`build_id=DNEHNrmCuLdDvWw3kgnCI` |
| ITSM 后端 | 8080 | 运行中，源 `fc8de9d3626bd61e9f046085e4cde8c43d6432e4` |

**启动只经 stack**（`/home/administrator/apps/itsm-kaf/stack`）。**不要**手工起进程占端口——上一轮曾因此让 3010 脱离 stack 权威，需 `stack stop/start itsm-web` 并先停掉手工进程才能回收。

### 数据库（Dev）

`itsm_config_baseline_20260908 / public`：**047**（39 回执，最高 `047_bpmn_assignment_source`），定义 75 / 绑定 35 / 工单 33。全部库用途见 `docs/development-environment.md` 的 `### Database inventory`。

**注意：是 047，不是 048。** 048 既无回执也无对应实例。

### 包1 目标对象（Dev 实测）

| 对象 | 事实 |
| --- | --- |
| `process_definitions` id=65 | tenant1，`key=ticket_general_flow`，`version=1.3.0`，active，`deployed_at=2026-08-21T05:22:01.495923+00:00`，摘要 `6d7c436b…`（**已按冻结配方复现一致**） |
| 绑定 825 | tenant1，generic，`ticket_general_flow`，ver=1，active，`default=false`，prio=0 |
| 绑定 827 | tenant1，generic，`ticket_general_flow`，ver=1，active，`default=true`，prio=10 |
| 绑定 830 | tenant2，generic，`ticket_general_flow`，ver=1，active，`default=true`，prio=0（**保持原状**） |
| `ticket_general_flow_v2` | **不存在**（§2.1"若已存在不同内容则冲突停止"检查通过） |
| 流程实例 27 | tenant1，def=65，`status=running`，`business_type=generic`，`business_id=29`，initiator=1 |
| 任务 33 | 实例27 的唯一任务：`Activity_Assign`，`status=completed`，`action=assign`，`handler=ticket_service_handler` |
| 工单 29 | `TKT-202609-000027`，status=open，assignee=1，owner=1 |

### 隔离验证目标

**不存在。** 实例上无任何库有 `migration_validation` schema（包2/包3 未做）。设计方已裁定**破环口径 (i)**：A3 **不以隔离目标演练替代**，改为 **Dev 内自证**——新 key 先创建（未绑定即**惰性**，无路由影响）→ Dev 合成工单走通 → 切 825/827 → 再验证 → 失败用现有领域 API（CAS+审计）切回；判据为"是否优于当前已坏的现状"。**已批准在 Dev 创建未绑定的 `ticket_general_flow_v2` v1.0.0。**

## 6. 必须知道的陷阱（上一轮踩过，请勿重犯）

1. **定义摘要口径**：`process_definitions.bpmn_xml` 存 **base64**；冻结摘要是 `sha256(base64decode(bpmn_xml))`（参考实现见证据目录 `itsm-dev-bindings-apply.py` 的 `digest_xml`，脚本自带 `definition hash drift` 断言）。用 `jsonb::text` 或"解码后 XML 文本"会得到 `9166698a…` / `dc01d828…`，**均非基线**。上一轮据此误报过"SHA 不一致"，已更正——**源并未漂移**。
2. **定义选择按 `key` + `is_active`**（`service/bpmn_version_service.go:690-709`，按 `deployed_at desc, id desc`；`version<=0` 取最新 active）。故**新 key 未绑定即惰性**；但**同一 key 下新增 active 版本**会被 `version<=0` 的绑定立刻选中——动手前必须断言路由解析未变。
3. **网关条件语法不可用**：`service/bpmn_types.go:355-358` 的 `BPMNConditionExpression.Expression` tag 是 `xml:",chardata"`，消费点 `bpmn_process_engine.go:1182/2498/2516`。definition65 用的是 `<bpmn:body>` 子元素 → **三个网关当前都不可用**。这是 §2.1 未记载但**必须修**的缺陷。
4. **流程级 metaData 引擎不解析**：`BPMNProcess`（`bpmn_types.go:26-42`）**没有** `ExtensionElements` 字段，全 `service/` 无读取点。但**仓库多个内置模板沿用流程级 extensionElements 写法**，是描述性约定。决策：v2 保留 `category/description`、对齐 `version`，**移除其中误导的 `service_task_type`/`action`**。
5. **绑定 ID 必须从 `process_bindings` 重读**，不按命名猜测；绑定表实际列名含 `process_version`（bigint）、`is_active`、`is_default`（曾因 `coalesce(...,'-')` 混类型而报错）。
6. **集成测试需要真实 PG**：现有 BPMN 任务夹具是 **sqlite**（`service/bpmn_authorization_test.go:67` → `enttest.Open(t,"sqlite3",...)`）；真实事务测试须用 `//go:build integration_postgres` 且 `INTAKE_POSTGRES_TEST_DSN` 指向 `127.0.0.1:36444` 的库 `sslvpn_test`。可用一次性 `postgres:16-alpine`（trust 认证）起停，**测试自建唯一 schema 并在结束时 DROP**，用后立即删除容器。
7. **A1 不中断既有重试**：A1 只关闭**新故障的产生路径**。领取路径 `filterPersistedBPMNCallbackPayload` 失败仍落 `handler_error` 重试——收敛为 `blocked` 是 **A4**。**不要把 A1 描述成"工单29 的重试循环已停止"。**

## 7. 下一步（A3 步骤 2–8，含已定位插入点）

| 步骤 | 内容 | 关键位置 |
| --- | --- | --- |
| 2 | **§2.2 合同管线**：定义级 `workItemLifecycleContract=generic_fulfillment_v1` + 任务级 `workItemPrerequisite=assigned\|in_progress\|escalated\|resolved\|closed`；**发布期拒绝**未知值/专业 recordClass/与该枚举混用的 handler | 发布校验 `service/bpmn_publication.go:56 ValidateDefinitionForPublication`；解析 `service/bpmn_xml_parser.go:45/60/528`；**同类先例** `service/bpmn_assignment_source.go:18 validateBPMNAssigneeSource`；节点级 metaData `bpmn_types.go:177`/`:233` |
| 3 | **门禁**：同一事务纯规则（建议 `service/generic_workflow_gate.go`），BPMN 命令 + 只读 UI actions + Ticket 领域命令共用；锁序 WorkItem→instance→task；CAS/operation receipt 去重；拒绝客户端改写 `approval_required`/`need_escalate`/`approvalResult`；**仅对有合同的定义生效**，旧定义沿用原逻辑 | — |
| 4 | 派生 `ticket_general_flow_v2` v1.0.0 XML（§2.1 节点表 + 步骤1 语法修正 + 去误导 metaData） | — |
| 5 | 在 Dev 创建**未绑定**定义 → 核对无冲突 → **断言路由解析未变** | — |
| 6 | Dev 合成工单走通 §2.1 正常 UI 路径 | 3010→8080 |
| 7 | 用现有领域 API（CAS+审计）切 825/827 → 复验 → 回退预案就绪 | — |
| 8 | **A4**：冻结 callback2 → `handler_contract` blocked，保留 payload/attempt 历史与"任务33 completed"记录 | 领取路径 `bpmn_process_engine.go:1462` 附近已有 `BlockedEffect(CallbackBlockHandlerContract, …)` 范式 |

### 步骤 2 的待确认点（**接手前请先确认**）

新增定义级合同需要一个承载位置。**建议**沿用仓库内置模板的流程级写法（`<bpmn:process><bpmn:extensionElements><bpmn:metaData name="workItemLifecycleContract">…`），**但当前 `BPMNProcess` 没有 `ExtensionElements` 字段 → 需新增并解析**。该改动是**纯增量**（当前无人读取流程级，不改变既有定义行为），与"改条件表达式 chardata 语义"不同。**需设计方确认是否允许**；若不允，需另选承载方式（如定义行字段）。

### 步骤 2 的 TDD 断言清单（先写断言，再实现）

1. 合法：仅 `generic_fulfillment_v1` + 五个合法 prerequisite → 通过
2. 拒绝：合同值未知／拼写变体／空
3. 拒绝：`workItemPrerequisite` 未知值／空
4. 拒绝：该合同 + 专业 class（incident/change_request/…）
5. 拒绝：该合同 + 节点带 `service_task_type`·`action`
6. **兼容**：无合同的既有定义（definition65）→ 校验通过、行为不变
7. 解析：`BPMNProcess.ExtensionElements` 取到流程级 metaData；缺失为 `nil` 且不 panic

## 8. 阻塞与未验证项（如实保留）

| 项 | 说明 |
| --- | --- |
| **独立复核未做** | 涉及 BPMN 与权限边界，按治理实现者不得自审通过。PR #47 待独立复核 |
| **I1（设计待确认）** | A2 门控对"**固定配置已满足输入**"的 `assign` 任务可能误禁用。合同 §2.3"固定配置未满足输入时 complete=false"隐含一套"固定配置可满足输入"的机制，但完成 payload 取自**请求变量**、`task.TaskVariables` 不参与 enqueue → 该机制确切来源**未确认**。按"报告差异、不自行更换方案"挂起 |
| **I2（已知边界）** | A2 门控只读**已持久化**描述符；若回调仅声明在定义、尚未持久化，按钮仍会出现，由 A1 在提交时拒绝 |
| **#46 未合并** | PR #47 的 diff 含 #46 的 9 个文档提交（因已 `--no-ff` 合入）。**建议先合 #46**，否则文档随 #47 进 main |
| **KAF 回执头血统未定** | `itsm` / `itsm_baseline_20260908` / `itsm_migration_20260914` 三库的 `schema_migrations` 头是 **KAF 命名**（`019_kaf_execution_integrity_rls`），而 Dev 是 ITSM 命名。作为克隆/验证来源前须确认血统 |
| **无隔离目标** | 无任何库有 `migration_validation`；包2/包3 未做（已被破环口径 (i) 绕过，但正式验收前仍需落地） |
| **未清理的旧库** | `itsm`、`itsm_baseline_20260908`、`itsm_migration_20260914`、`itsm_intake_test`、`itsm_p1_integration_verify_20260901` 仍在（属包4 范围，需逐项授权） |
| 前端未覆盖项 | 本 PR 无前端改动；`login.spec.ts` 定位器陈旧、`test:smoke` 指向不存在文件、`package-lock.json` 不同步等既有问题不在本任务范围 |

## 9. 已清理（勿再重复）

本会话已删除自己创建的遗留资源并核验：

```
DROP DATABASE itsm_e2e_b8ac9639 WITH (FORCE)      → 残留库 0
DROP ROLE e2e_app_20260916 / e2e_system_20260916  → 残留角色 0
git worktree remove .worktrees/e2e-source-main    → 已移除
一次性 PG 容器 itsm-a1-pg-test-20260916            → 已删除，36444 已释放
```

## 10. 纪律提醒（来自 AGENTS.md 与本项目治理）

- 共享数据库操作**始终串行**；一次只允许一名写入负责人
- **不改**历史迁移 SQL/校验和/回执；不伪造验收；不用 Ent overlay 绕过准入
- 运行操作**只经 stack**；不手工占端口
- 涉及 BPMN／WorkItem／迁移／权限的改动**必须独立复核**
- 不自动合并 PR；文档交付完成**不等于**环境执行项完成
- 不新建根目录 `HANDOFF.md`、不提交私有脚本/截图/凭据
