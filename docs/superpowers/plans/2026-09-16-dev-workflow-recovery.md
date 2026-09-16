# Dev流程恢复实施与交接计划

> **For agentic workers:** Use executing-plans task-by-task. 用户自行分派Agent；本文件不触发自动执行或共享环境写入。

**Goal:** 恢复新generic工单完整UI路径，阻止空参数产生永久失败回调，保留旧失败实例证据。
**Architecture:** 后端契约负责输入和权限，简单任务UI只展示可执行操作；新流程复用owner-bound履约任务和现有领域命令，不重写旧运行。
**Tech Stack:** Go/Gin/Ent、现有BPMN引擎、Next.js、PostgreSQL。
**Status:** accepted（总体方向）；A1/A2可开始最小实现与回归，A3须先冻结具体流程图并独立审查，A4只授权诊断与处置设计。不能把整份计划视为已完成全部设计。

## 入口、基线和约束

先读AGENTS.md、docs/agent-engineering-governance.md、docs/engineering-conventions.md、docs/development-environment.md以及[设计§7](../specs/2026-09-16-dev-restoration-two-database-design.md#7-接续设计执行-agent-的裁定与门槛)。唯一状态在[四阶段清单](2026-09-15-migration-validation-ledger.md#two-database-execution)。独立worktree从最新origin/main建立；PR46文档若未合并，明确引用其提交，不覆盖其他Agent工作。

### 按执行阶段阅读背景

| 阶段 | 必读入口 | 用途 |
| --- | --- | --- |
| 开工 | [AGENTS.md](../../../AGENTS.md)、[CLAUDE摘要](../../../CLAUDE.md)、[工程治理](../../agent-engineering-governance.md)、[工程约定](../../engineering-conventions.md) | 领域所有权、目录/测试/分支/独立审查及DTO规则 |
| 接手运行 | [环境合同](../../development-environment.md)、[开发指南](../../DEVELOPMENT_GUIDE.md)、[命令参考](../../dev-commands-reference.md)、[031/047原因分析](../../review/2026-09-16-dev-schema-divergence-report.md) | 先认实际环境；历史原因不当当前部署状态 |
| A1/A2 | [持久回调计划](2026-08-30-bpmn-durable-callback-outbox.md)、当前callback代码及测试 | 冻结payload、原子入队、blocked与输入错误的区别；历史计划必须对照当前实现 |
| A3 | [WorkItem设计](../specs/2026-08-26-unified-work-item-model-design.md)、[任务绑定设计](../specs/2026-09-14-work-item-task-assignment-design.md)、[09-15分配报告](../../review/2026-09-15-work-item-task-assignment-report.md) | 专业生命周期与流程职责、绑定履约限制、终态actor/责任人证据 |
| A4 | [受控退役设计](../specs/2026-09-11-workitem-controlled-retirement-design.md)及[目标运行手册](../../deployment/workitem-controlled-retirement-target-runbook.md) | 历史不可变、审计连续性backlog与R边界；本任务不执行R |
| 验收/交付 | [代码审查指南](../../code-review-guide.md)、[E2E指南](../../e2e-testing-guide.md)、[文档索引](../../README.md)、[路线图](../../../ROADMAP.md) | 权限负例、真实UI、当前范围；不得扩大成全产品验收 |


### 所有执行Agent的任务准入与交付门槛

仓库权威文件名为`AGENTS.md`，不是另建`AGENT.md`。以下是本任务检查入口，不复制或取代原规范：

- 开工记录单一目标/排除范围、影响模块和契约、共享写入标识、验证方式、依赖/冲突分支；按[治理§7](../../agent-engineering-governance.md#7-任务协同与角色分工)满足Definition of Ready。
- 独立worktree及`codex/<type>/<scope>-<description>`分支，禁止main直写、覆盖他人未提交内容；PR正文引用本任务编号、当前文档提交和完整源码SHA。
- 修改前运行最小基线测试，修改后补真实回归及受影响构建/类型/契约检查。环境未具备导致skip必须记未验证；不得为了测试启动共享服务、seed、reset或隐式迁移。
- 后端拥有规则/权限/租户/事务；前端只消费DTO和授权投影。流程身份使用`common/workitemidentity`；不得恢复ticket/change/service_request旧BPMN词表。
- 修改架构/领域契约时同步AGENTS.md和CLAUDE.md；修改开发流程更新DEVELOPMENT_GUIDE；API/前端公共约定变更同步engineering-conventions。本任务发现未知外部动作时失败关闭。
- 交付前`git diff --check`、受影响测试和必要CI通过，检查无秘密/临时文件。涉及BPMN/WorkItem/迁移/权限必须独立复核；实现者不能独自宣布验收。
- 共享操作实行一个写入负责人：执行前固定具名目标、备份恢复、实际PID/源码/制品/profile、影响范围、回滚/补救和消费者；已授权范围可继续，不重复索取笼统许可；超出范围先记录差异再做范围决定。
- 交付分开写“代码已合并”“实际部署”“业务验收”“未完成/未覆盖”。报告必须包含失败和跳过，不以健康200、任务completed、旧截图或离线测试替代本次业务通过。

已验证运行基线是前端2988819c / build DNEHNrmCuLdDvWw3kgnCI、API fc8de9d36、Dev `itsm-postgres-dev / itsm_config_baseline_20260908 / public`，047/39回执，R038未执行。这是2026-09-16快照；操作前刷新进程/制品/目标身份。证据目录 `/home/administrator/.local/state/itsm-dev-restoration-20260916`，不得打印cookie、配置密码、用户行数据。

- 3010→8080保持，禁止长期回滚旧代码、直接更新业务表、放宽权限或静默忽略callback。
- 19旧绑定已停用、7替代已建，旧实例7–10已终止；不得重跑旧脚本。
- 失败对象是工单29、流程27、任务33、callback2；原定义65保留。原instance assignee2不是当前owner1，不可用来补冻结payload。
- notification及外部执行关闭；本地SMTP捕获配置已存在，不重复安装。健康200不代替流程验收。

## A1：契约失败必须发生在任务完成之前

**Files:**
- 修改 `itsm-backend/service/bpmn/callback_contract.go`：声明assign/update_status必填字段。
- 修改/复用 `itsm-backend/service/bpmn_callback_security.go`、`bpmn_callback_enqueue_plan.go`，并核对 `itsm-backend/service/bpmn_process_engine.go` 的真实任务完成事务入口：参与者输入验证必须早于completed状态及callback持久化。
- 测试 `itsm-backend/service/bpmn_callback_security_test.go`、`bpmn_callback_enqueue_plan_test.go`；真实事务场景放同service现有Postgres测试夹具或tests/integration。

**Consumes:** 已冻结定义中的handler/action及当前授权命令；**Produces:** 缺输入时显式验证错误且数据库无任务完成/回调新增/业务变更。

- [ ] 先补回归断言：assign的空/缺失/null/零/负/小数字段拒绝，合法整数通过；update_status缺失/null/空串拒绝；伪造tenant/business_id不进入可信上下文。
- [ ] 在现有CallbackContract内最小声明，沿用字段归一化。以下仅为字段声明示意，不能独立满足输入校验（见后续要求）：

```go
if action == "assign" {
    contract.RequiredFields = []string{"assignee_id"}
    contract.PositiveIntegerFields = []string{"assignee_id"}
}
if action == "update_status" {
    contract.RequiredFields = []string{"new_status"}
}
```

- [ ] 当前`RequiredFields`仅检查key存在；为update_status增加handler-owned的字符串类型/非空校验（含null、空白、数字等负例），状态是否合法仍交既有领域服务。不要全局改变所有handler必填语义而不做兼容审查。
- [ ] 当前`BuildCallbackEnqueuePlan`遇定义/契约缺陷返回blocked plan且error为nil；不得为满足本计划把所有blocked改为错误。用户可修正的输入须在完成命令的现有校验入口拒绝；定义缺陷仍保留既有可见blocked与审计。测试分别覆盖两类路径。
- [ ] 实测写前验证确实在同一任务完成事务中；覆盖用户输入拒绝后task精确保持原状态（不能假设名为active）、callback计数不变、process未推进，不能只测handler返回错误。
- [ ] 覆盖有效提交重复/并发提交只有一次业务效果、权限/租户负例，以及固定配置输入的既有解析优先级；不擅自新增请求字段或改变旧payload。
- [ ] 运行 `go test ./service/bpmn ./service -run 'Callback|BPMNTask' -count=1`（cwd itsm-backend），按已有夹具配置跑受影响Postgres事务测试；跳过的集成测试不能计PASS。
- [ ] 独立审查后提交单一修复PR。

## A2：简单任务UI不再提供必然失败的完成按钮

**Files:** `itsm-backend/service/bpmn_task_ui_actions.go`、`bpmn_task_ui_actions_test.go`；`itsm-backend/dto/bpmn_task_dto.go`仅在现有reason不够时修改；`itsm-frontend/src/components/ticket/TicketProcessTasks.tsx`及相邻`__tests__`；API测试沿用`src/lib/api/__tests__/bpmn-workflow-api.test.ts`。

**Consumes:** A1权威契约和现有taskUIActions授权；**Produces:** 需要输入且当前简单入口不能满足时complete=false并给出可理解原因，无需前端复制handler规则。

- [ ] 测试无表单且权威配置不能满足必需输入的callback任务，后端不授予简单complete；固定配置已合法满足字段的任务不能误禁用。无handler履约任务仍按既有权限可完成。
- [ ] 只复用或拆取纯只读后端契约解析；不得从GET投影调用会补写旧任务descriptor的`descriptorForProcessTask`。增加历史任务读取零写入测试，无法解析时仅禁用操作并给原因。未知handler/action失败关闭；不在前端硬编码assign等名称。
- [ ] UI展示服务端reason，已提交与流程已推进分别呈现；会话切换/重复点击保护保留。
- [ ] 前端cwd `itsm-frontend`运行 `npm run test:unit -- --runTestsByPath src/components/ticket/__tests__/TicketProcessTasks.test.tsx src/lib/api/__tests__/bpmn-workflow-api.test.ts` 及 `npm run type-check`；核对确实发现并执行测试，`passWithNoTests`的空结果不计通过。运行受影响Go测试；不要把缺参数改成读取旧instance快照，也不要发送当前owner来猜测用户选择。
- [ ] 与A1可同PR，但必须同时验证后端拒绝直接绕过UI调用。

## A3：新版本generic流程与真实UI验收

**Files:** 不修改生产seed覆盖现有配置。通过现有发布/绑定领域API创建新定义版本，具名定义、节点、租户、绑定差异存私有证据；必要操作说明回写唯一清单。

- [ ] 只读导出定义65和受影响generic绑定，设计新版本：保留适用审批条件；普通履约采用现有无handler、work_item_assignee绑定；分派/解决/关闭使用现有领域入口。
- [ ] 把具体新版本BPMN、节点/handler/input表、审批分支及结束条件、租户/绑定ID清单、每项旧→新行为与UI动作写入可审查制品。不得仅把assign/resolve节点删除后称业务等价；无法证明必需审批/交付条件不丢失时，A3停在设计审查，不直接发布。
- [ ] 独立审查完整节点图、完成条件、权限及绑定优先级，先在隔离测试目标验证发布/路由。不得把含assign handler节点原地改属性后直接发布。
- [ ] 固定部署版本和操作窗口，经协调暂停相关写入；发布新版本并仅切换已审generic绑定。保留旧版本与历史引用。
- [ ] 使用合成新工单走UI：创建→分派/转派（原因）→履约→解决→关闭；刷新后核对工单状态、流程结束、任务实际actor/责任人、Outbox与callback结果一致。另用无权限角色验证不能操作。
- [ ] 若既有生命周期入口不能满足完整路径，先按领域所有者记录缺口和窄修复边界；需要新增状态机、审批机制或专业域重构时另行设计，不无限扩大A；不得手改状态完成验收。
- [ ] 保存版本、前后绑定、UI证据及领域审计；切换后已有新写入不盲目恢复旧数据库快照。

## A4：旧失败实例的处置交接

- [ ] 只读固定29/27/33/2的状态、租约、尝试和可能业务效果；输出不含payload秘密的摘要。
- [ ] 维持旧回调不可变。若无法经既有领域API安全处置，列为未结阻塞并提供独立恢复设计：操作者权限、原因/版本、执行租约互斥、旧→新操作因果关系、未知效果禁止重放、原记录和审计保留。
- [ ] 新通用重试、跳过或修正接口不属于A1/A2授权范围。未完成处置不影响如实交付“新流程验收通过”，但禁止宣称历史失败已清理或最终计划完成。

## 交付给集成人

提供PR/完整SHA、受影响测试及真实UI结果、Dev实际目标、配置差异、失败实例处置状态和回退边界。只更新唯一清单的实际状态；不创建根目录HANDOFF.md、不提交私有脚本/截图/凭据。Agent A无权开始克隆、清库或搬迁KAF。

### 验收记录最小字段

每个UI场景记录：目标profile/版本、租户及账号角色（无凭据）、菜单/页面/按钮、合成输入、预期结果、实际结果、工单/流程/任务标识、证据位置。A3至少包含有权限执行、无权限拒绝、重复点击/刷新后的持久结果；流程结束与工单解决/关闭分列。A1/A2可先代码交付，A3未验收则不报告“Dev完整恢复”。
