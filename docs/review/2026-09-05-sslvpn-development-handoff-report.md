# SSLVPN 开发暂停与 Agent 交接报告

**状态：按用户要求暂停，待另一 Agent 接手。不得把本报告视为功能完成或发布批准。**

## 1. 接手时先做什么

1. 使用下面的**现有 ITSM worktree**，读取 `AGENTS.md`、本报告及[总实施计划](../superpowers/plans/2026-09-05-sslvpn-end-to-end-implementation.md)。不要在原 main 开发，不要重新整分支归并。
2. 检查 `git status`，保留全部已跟踪和未跟踪的工作中改动。**当前源码尚未整体构建/验证通过**；不可 `reset --hard`、`clean -fdx` 或只 cherry-pick 提交后继续。
3. 读取本地任务目录中的 `current-state.md`、`progress.md`、`A3b-A4-entry-cutover-brief.md`、`A3b-A4-entry-cutover-report.md`，以报告末尾的 **PAUSED HANDOFF** 为最近实现事实。
4. 从当前 A3b/A4 后端任务未完成处继续：先恢复构建及正在迁移的真实入口测试，再完成共享字段/schema 归并。不要重做已审查前置任务。
5. 完成本任务后，从 **`94cc5787` 到最终 HEAD** 生成整个任务审查包，安排独立审查。中间开发检查点不能替代该审查。

本地任务目录（被 Git 忽略，但本机仍完整保留）：

```text
/home/administrator/project/itsm/.worktrees/sslvpn-unified-intake/.superpowers/sdd/2026-09-05-sslvpn-end-to-end-implementation/
```

`handoff-snapshot/` 保存暂停时的源码差异、未跟踪源码、状态和任务 Markdown 资料；它是恢复备份，不是另一套开发工作区。**现有 worktree 已含全部差异，不要再次应用补丁。** 若换机器，需转移整个 feature 分支历史及该快照；仅复制本报告不足以恢复代码。

## 2. 工作区与提交边界

| 对象 | 位置/状态 |
| --- | --- |
| ITSM 实施目录 | `/home/administrator/project/itsm/.worktrees/sslvpn-unified-intake` |
| ITSM 分支 | `codex/feat/sslvpn-unified-intake`；原 main 基线 `5b2dd2c6` |
| 暂停前已提交 HEAD | `2ef94fb5`，文档提交；本次交接另有仅文档提交，可从 `git log` 查看 |
| 最后源码检查点 | `548d34e3`；**其后仍有大量未提交源码/测试修改和新文件** |
| 当前完整任务审查 BASE | `94cc5787`，不是 `548d34e3` |
| KAF 实施目录 | `/home/administrator/.worktrees/kaf-sslvpn-unified-intake` |
| KAF 分支/HEAD | 同名分支，`d07a178ccb6b825cc493dbd5daafcc8c64c61eec`；暂停核对时工作区干净，本期尚无 KAF 源码修改 |
| 原 KAF 目录 | `/home/administrator/actions-runner/_work/kaf/kaf`；用户原有 `uv.lock` 修改不得覆盖 |

暂停时未推送、合并或部署；未执行本期 Graph 成员关系查询、授权或移组。实现 Agent 已中断，仅允许补交接记录。未提交代码没有为了交接而强制提交成“完成”提交。

## 3. 已完成与未完成的准确区分

### 已通过独立阶段审查的前置能力

- A1 入口/来源审查，A2 公共契约、Registry 与基础设施。
- A3a 创建事务、当前权限、Prepare 顺序、配置快照和整事务重试：`62fec316`、`ffc4d17d`。
- 精确持久化流程启动及重放：`00034817`、`c1586c7e`、`56bc3df9`，迁移 023。
- Incident 创建事件、复用原规则引擎及事务动作回执：`e0055b83`，迁移 024。
- RLS 实际角色/驱动/连接池与独立受限 System 能力：`1808f2d9`、`367f9af9`；两个 Important 审查问题均已修复。

上述阶段**不等于原 A3/A4 整体完成**。测试和审查文件均在任务目录；摘要见[验证报告](2026-09-05-sslvpn-end-to-end-verification-report.md)。

### 当前任务已提交，但尚未整体独立审查

| 提交 | 内容 |
| --- | --- |
| `9850c1d6` | 目录详情返回一致快照中的确认版本 |
| `a9986ccb` | Problem 转换、邮件、Feishu 来源图及附件恢复；迁移 025 |
| `4acfcfcb` | Generic 创建前的配置化分配/规则、事务通知与通知 Worker 边界 |
| `548d34e3` | HTTP/Catalog/BPMN/已批准 AI 入口统一回执、`intake-v4`、持久化精确数字、唯一 Feishu 创建意图 |

### 当前未提交切片

- 删除旧 Ticket、Incident、Problem、Change、Service Request 独立创建 API、旧编号器和旧创建后同步/异步触发路径。
- bootstrap 注册已审查的精确流程启动 handler，以及安装 AlertCreator 后的**同一个** IncidentService RuleEngine。
- 保留 Ticket 取消流程仍需的 ProcessTriggerService；不能随旧创建触发器一起删除。
- 正在将依赖旧 API 的测试迁移到真实 Intake/HTTP/目录配置；新建的 `*_fixture_test.go` 等未跟踪文件是工作成果，必须保留。
- 暂停时实现切片包含 **64 个已跟踪修改/删除路径和 7 个未跟踪测试文件**；不含本次新增交接文档。
- Problem 全包和 Service Request 全包已有 `count=1` 通过记录；SR 旧 SSLVPN 回归已改为消费 durable start 后再推进审批。它不是新 KAF Web/真实 Graph 端到端证据。
- Incident 省略 priority 的真实 HTTP 请求曾返回 500 `incident priority matrix is required`；已修改构造以复用已有 PriorityMatrixService，针对性创建测试已通过；自定义矩阵/重放及实际 bootstrap 仍需覆盖。
- 显式 `workflowDefinitionKey` 当前权限校验已裁定，但**不要假定已实现或已通过测试**。
- **Ticket.type、Incident.incident_number、SR 重复共享字段的完整 schema/读取方迁移尚未完成。**

## 4. 下一步任务清单（按依赖顺序）

### 先完成当前 A3b/A4 后端任务

**接手后的第一组具体失败**：最后 service 选择性测试已结束，退出码 **1**，日志 `entry-ticket-creation-red2.log`；不是仍在运行的任务。

- Incident 规则回执查询使用 `ExecutionKindEQ("creation_rule")` 返回 not found；核对真实 owner 类型，保留实际副作用和两次消费不重复断言。
- Ticket 空 description 的旧用例与真实 HTTP 必填契约不符；按真实入口契约调整，不能为测试放宽生产校验。
- 动态字段用例的 title 太短或缺 description，尚未触达要验证的字段逻辑；修正 fixture 后保留排序/映射/回滚断言。
- 跨租户 requester 测试被 **fixture 自身**提前拒绝，尚未进入实际 Intake。必须分离合法 actor 的权限配置与非法 requester 输入；不能仅改预期错误使其“通过”。
- 删除旧触发方法时移除了 4 个 actor 审计测试，**真实 Intake→Outbox 替换测试尚未补齐**；覆盖 actor≠requester、规范业务身份和缺 actor 拒绝。

1. 阅读暂停交接段，处理剩余 Ticket/Incident/Change/repository 等旧创建测试；保持原权限、生命周期、关系及审计断言。先跑针对性失败用例，稳定后完整受影响包验证。
2. 完成显式流程覆盖授权：所有 command.workflowDefinitionKey，包括 BPMN 合并运行变量，均在回执查找/重放前要求发起人的当前 `workflow:write`。普通服务端 Catalog/process binding 不属于客户端覆盖；不得凭 `channel=bpmn` 放行。
3. 验证实际 bootstrap/Worker 装配、新创建路径及旧路径退出；AST 结构检查不能替代运行行为证据。
4. 配对删除共享字段与所有查询、DTO、生命周期写入、SQL/seed、监控/工作流读取方。保留既有 WorkItem 编号，专业变更需更新唯一 WorkItem 的 version/updatedAt。
5. 迁移注册表已使用 023/024/025，继续前重新核对；不要抢用旧分支迁移编号。历史冲突应 preflight 拒绝并给出修复依据；验证升级、重复应用、Ent rebootstrap、约束与真实 RLS 角色。
6. 补齐 A1 每个入口/字段处置、Swagger/契约、Change 可审批 fixture 和 Problem router 等保留门禁，执行必要构建/测试。
7. 完整更新实现报告并提交；使用 `A3b-A4-entry-review-context.md` 安排独立审查，范围 `94cc5787..最终HEAD`。未审查通过前不把后续任务当作已具备验收基线。

当前实现者另列的未闭合项：ToolQueue.Close 的 app shutdown 接线、ToolRegistry 创建工具 ArgsSchema/ResultSchema；Incident Source 信任与敏感 Metadata 校验；Ticket→Change 分类字符串解析；FormPresetID 当前仅入摘要的处置；模板 workflow_steps 的明确含义；Feishu 创建 JSON 重复 key 拒绝。按 A1 原始字段/入口要求逐一解决，不可只修到编译通过。

### 随后的既定顺序

```text
A4 前端适配
→ A5 Catalog 发布校验与流程版本约束
→ A6 身份交换/受限创建与读取 API
→ C1 有限期限策略与授权结果契约
→ A7 真实 PostgreSQL/全入口/实际进程集成门禁
→ B1-B4 KAF 当前用户受理及原确认卡集成
→ C2-C4 授权执行、回执和真实外部验收
→ 整分支独立审查与交付
```

各任务 brief 已在任务目录准备；A4 前端另读 `A4-frontend-context.md`，B1/A7/C4 有相应 context。尚未开始的任务不需要重新做 brainstorming 或再次征求用户批准既定范围。

## 5. 必须继承的契约与风险

- 这是全入口 Unified Intake 归并，不能新增 SSLVPN 专用创建链路。专业 Service 持有状态机；ITSM BPMN 审批，KAF 理解意图并治理执行。
- 当前用户创建凭据与 `kaf_automation` 执行凭据分开。身份 assertion 已确定为 v2 **10 个 LF 分隔字段**；旧七字段 brief 已淘汰。A6/B1 使用最终契约和共同 fixture。
- 旧专业创建 URL 保留，但响应统一 `CreateWorkItemResult`，新建 201/重放 200，数字 envelope。不保留 `id`/`ticketId` 详情别名。所有活跃客户端必须同一可发布批次升级；详情另走受权 GET。
- 专业 HTTP 使用客户端持有的 `Idempotency-Key`，统一端点用 JSON `idempotencyKey`；响应丢失/刷新身份后保持原键和确认内容。
- 共享根字段包含 templateId/parentTicketId/tagIds/workflowDefinitionKey，严格摘要为 `intake-v4`；不要恢复 Generic 嵌套别名。动态字段 key 不应被前端递归 camelCase 改写。
- Catalog 确认版本从详情一致快照取得，不能重试时静默取最新。Generic 规则/分配在 Prepare 中先于流程与 SLA 选择。
- 精确数字持久化已覆盖重启后固定定义下的工作流继续执行；不能修复历史已舍入数据。**同版本流程 XML 被修改后的实例继续执行风险归 A5**：已有具体代码线索，尚未作为实际测试复现/关闭。
- Feishu 自动/手动缺失映射创建共用 `feishu-create:<WorkItemID>` 的 Outbox 及 owner，普通同步只更新既有映射；不可复活第二个远端创建算法。
- 实际 Tenant/System 数据库池分离，System 仅最小显式能力，不是业务读写回退。不得为测试通过给运行角色全局绕过权限。
- 产品只承诺“申请有效期”；自动回收仍是 Backlog。首次验证授权时刻决定有限期限，回执重放不能延长。目录页旧“到期自动回收”文案已记录待修。

所有历史 `Ruling:` 原文及代价保留在 `progress.md` 和暂停快照的 `rulings.md`；接手者不得丢弃。这里仅摘要高影响项，并未撤销其余裁决。

## 6. 测试证据与已知限制

- 最近未提交切片的全包绿：`go test ./handlers/problem -count=1`、`go test ./handlers/service_request -count=1`。具体后续 Incident/Ticket 检查以 Agent 的 PAUSED HANDOFF 为准。
- **没有最终整个仓库构建通过的结论**；旧测试仍在迁移。`go test ./... -run <部分模式>` 即使成功也只是全部编译与匹配用例，不是全量测试通过。
- RLS `go test ./database/rls -count=2` 实际因既有 helper 重复 `sql.Register` 失败，先前错误成功声明已撤回；独立 count=1 和真实 PG 通过证据保留。此 Minor 及其他 deferred 项见 ledger，最终审查需处理。
- KAF 曾完成隔离环境准备，基线 111 个委派测试、前端 89 个测试及构建通过；**本期 KAF 尚未实现，完整 pytest 未跑**。
- Python API 测试辅助和 smoke 脚本改动仅有语法检查，不是共享环境真实 API 通过。
- 暂停只整理文档/快照，没有补跑开发测试，不能把暂停后的文件状态推断为已通过旧命令。

## 7. 已保留的独立运行环境

| 资源 | 位置与约束 |
| --- | --- |
| PostgreSQL | 容器 `codex-sslvpn-intake-pg-20260905`，本机 `36444`，PostgreSQL16/tmpfs；ITSM 专属库 `sslvpn_test`，KAF 库 `kaf_test` |
| ITSM PG 测试 DSN | `postgres://postgres@127.0.0.1:36444/sslvpn_test?sslmode=disable`；仅专属测试实例 |
| KAF PG DSN | `postgresql+asyncpg://postgres@127.0.0.1:36444/kaf_test` |
| Redis | 容器 `codex-sslvpn-intake-redis-20260905`，只监听 `127.0.0.1:36445`，无持久化；ITSM DB0、KAF DB1 |
| KAF 环境 | 自有 `.venv`/依赖已准备；`DEBUG=true uv run --frozen --no-sync`，显式设置独立 DB/Redis，禁止使用共享默认端口 |

暂停保留这两个任务专属容器供接手使用；不要重启 tmpfs PostgreSQL 后仍假定测试数据存在。整个计划结束再清理仅这两个任务资源。共享服务保持原状。运行角色与 schema 应由具体隔离测试按唯一名称创建/清理，不能改现有共享角色。

真实外部验收需先读 `C4-context.md`、`docs/testing/kaf-delegation-release-closeout-fixture.md` 及生产就绪设计中的运行手册。它们记录专用开发测试对象与恢复条件，**不证明当前成员关系或执行窗口已满足**。不得使用 KAF PROD、生产凭据或 LDAP 替代既定场景；先完成本地实现及前置门禁，再落实具体可审查的外部验收条件。

## 8. 可直接转发给接手 Agent

> 接手 SSLVPN E2E 实施。请在 `/home/administrator/project/itsm/.worktrees/sslvpn-unified-intake` 读取 `docs/review/2026-09-05-sslvpn-development-handoff-report.md`，再读取其中任务目录的 current-state、progress、当前 entry-cutover brief/report，特别是 PAUSED HANDOFF。保留现有未提交及未跟踪源码，从 A3b/A4 旧创建路径退役和共享 schema 未完成处继续；不要重做已审查前置任务。当前整个任务 review BASE 为 `94cc5787`。完整顺序和 KAF worktree/测试环境见交接报告。用户已批准既定范围，权限到期自动回收为 Backlog；不可宣称现有后端回归等于 KAF Web→审批→外部组授权完整验收。
