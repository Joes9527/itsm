# Dev流程恢复实施与交接计划

> **For agentic workers:** Use executing-plans task-by-task. 用户自行分派Agent；本文件不触发自动执行或共享环境写入。

**Goal:** 恢复新generic工单完整UI路径，阻止空参数产生永久失败回调，保留旧失败实例证据。
**Architecture:** 后端契约负责输入和权限，简单任务UI只展示可执行操作；新流程复用owner-bound履约任务和现有领域命令，不重写旧运行。
**Tech Stack:** Go/Gin/Ent、现有BPMN引擎、Next.js、PostgreSQL。
**Status:** ready for implementation/review；共享操作按具名预检和独立审查门槛执行。

## 入口、基线和约束

先读AGENTS.md、docs/agent-engineering-governance.md、docs/engineering-conventions.md、docs/development-environment.md以及[设计§7](../specs/2026-09-16-dev-restoration-two-database-design.md#7-接续设计执行-agent-的裁定与门槛)。唯一状态在[四阶段清单](2026-09-15-migration-validation-ledger.md#two-database-execution)。独立worktree从最新origin/main建立；PR46文档若未合并，明确引用其提交，不覆盖其他Agent工作。

已验证运行基线是前端2988819c / build DNEHNrmCuLdDvWw3kgnCI、API fc8de9d36、Dev `itsm-postgres-dev / itsm_config_baseline_20260908 / public`，047/39回执，R038未执行。这是2026-09-16快照；操作前刷新进程/制品/目标身份。证据目录 `/home/administrator/.local/state/itsm-dev-restoration-20260916`，不得打印cookie、配置密码、用户行数据。

- 3010→8080保持，禁止长期回滚旧代码、直接更新业务表、放宽权限或静默忽略callback。
- 19旧绑定已停用、7替代已建，旧实例7–10已终止；不得重跑旧脚本。
- 失败对象是工单29、流程27、任务33、callback2；原定义65保留。原instance assignee2不是当前owner1，不可用来补冻结payload。
- notification及外部执行关闭；本地SMTP捕获配置已存在，不重复安装。健康200不代替流程验收。

## A1：契约失败必须发生在任务完成之前

**Files:**
- 修改 `itsm-backend/service/bpmn/callback_contract.go`：声明assign/update_status必填字段。
- 修改/复用 `itsm-backend/service/bpmn_callback_security.go`、`bpmn_callback_enqueue_plan.go`：现有归一化及写前验证入口。
- 测试 `itsm-backend/service/bpmn_callback_security_test.go`、`bpmn_callback_enqueue_plan_test.go`；真实事务场景放同service现有Postgres测试夹具或tests/integration。

**Consumes:** 已冻结定义中的handler/action及当前授权命令；**Produces:** 缺输入时显式验证错误且数据库无任务完成/回调新增/业务变更。

- [ ] 先补回归断言：assign的空/缺失/null/零/负/小数字段拒绝，合法整数通过；update_status缺失/null/空串拒绝；伪造tenant/business_id不进入可信上下文。
- [ ] 在现有CallbackContract内最小声明，沿用字段归一化。核心变更示意：

```go
if action == "assign" {
    contract.RequiredFields = []string{"assignee_id"}
    contract.PositiveIntegerFields = []string{"assignee_id"}
}
if action == "update_status" {
    contract.RequiredFields = []string{"new_status"}
}
```

- [ ] 实测写前验证确实在同一任务完成事务中；覆盖失败后task仍active、callback计数不变、process未推进，不能只测handler返回错误。
- [ ] 覆盖有效提交重复/并发提交只有一次业务效果、权限/租户负例，以及固定配置输入的既有解析优先级；不擅自新增请求字段或改变旧payload。
- [ ] 运行 `go test ./service/bpmn ./service -run 'Callback|BPMNTask' -count=1`（cwd itsm-backend），按已有夹具配置跑受影响Postgres事务测试；跳过的集成测试不能计PASS。
- [ ] 独立审查后提交单一修复PR。

## A2：简单任务UI不再提供必然失败的完成按钮

**Files:** `itsm-backend/service/bpmn_task_ui_actions.go`、`bpmn_task_ui_actions_test.go`；`itsm-backend/dto/bpmn_task_dto.go`仅在现有reason不够时修改；`itsm-frontend/src/components/ticket/TicketProcessTasks.tsx`及相邻`__tests__`；API测试沿用`src/lib/api/__tests__/bpmn-workflow-api.test.ts`。

**Consumes:** A1权威契约和现有taskUIActions授权；**Produces:** 需要输入且当前简单入口不能满足时complete=false并给出可理解原因，无需前端复制handler规则。

- [ ] 测试无表单却需要输入的callback任务，后端不授予简单complete；无handler履约任务仍按既有权限可完成。
- [ ] 复用后端契约解析，未知handler/action失败关闭；不在前端硬编码assign等名称。
- [ ] UI展示服务端reason，已提交与流程已推进分别呈现；会话切换/重复点击保护保留。
- [ ] 运行受影响Go测试、前端定向组件测试与类型检查；不要把缺参数改成读取旧instance快照，也不要发送当前owner来猜测用户选择。
- [ ] 与A1可同PR，但必须同时验证后端拒绝直接绕过UI调用。

## A3：新版本generic流程与真实UI验收

**Files:** 不修改生产seed覆盖现有配置。通过现有发布/绑定领域API创建新定义版本，具名定义、节点、租户、绑定差异存私有证据；必要操作说明回写唯一清单。

- [ ] 只读导出定义65和受影响generic绑定，设计新版本：保留适用审批条件；普通履约采用现有无handler、work_item_assignee绑定；分派/解决/关闭使用现有领域入口。
- [ ] 独立审查完整节点图、完成条件、权限及绑定优先级，先在隔离测试目标验证发布/路由。不得把含assign handler节点原地改属性后直接发布。
- [ ] 固定部署版本和操作窗口，经协调暂停相关写入；发布新版本并仅切换已审generic绑定。保留旧版本与历史引用。
- [ ] 使用合成新工单走UI：创建→分派/转派（原因）→履约→解决→关闭；刷新后核对工单状态、流程结束、任务实际actor/责任人、Outbox与callback结果一致。另用无权限角色验证不能操作。
- [ ] 若既有生命周期入口不能满足完整路径，将具体缺口放回同任务修复；不得手改状态完成验收。
- [ ] 保存版本、前后绑定、UI证据及领域审计；切换后已有新写入不盲目恢复旧数据库快照。

## A4：旧失败实例的处置交接

- [ ] 只读固定29/27/33/2的状态、租约、尝试和可能业务效果；输出不含payload秘密的摘要。
- [ ] 维持旧回调不可变。若无法经既有领域API安全处置，列为未结阻塞并提供独立恢复设计：操作者权限、原因/版本、执行租约互斥、旧→新操作因果关系、未知效果禁止重放、原记录和审计保留。
- [ ] 新通用重试、跳过或修正接口不属于A1/A2授权范围。未完成处置不影响如实交付“新流程验收通过”，但禁止宣称历史失败已清理或最终计划完成。

## 交付给集成人

提供PR/完整SHA、受影响测试及真实UI结果、Dev实际目标、配置差异、失败实例处置状态和回退边界。只更新唯一清单的实际状态；不创建根目录HANDOFF.md、不提交私有脚本/截图/凭据。Agent A无权开始克隆、清库或搬迁KAF。
