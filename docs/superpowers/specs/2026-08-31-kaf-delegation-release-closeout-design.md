# KAF 委派执行完整性发布收口设计

> 状态：设计已批准，等待书面规格审阅与实施计划
> 日期：2026-08-31
> 上位设计：`2026-08-28-kaf-itsm-autonomous-workitem-delegation-design.md`
> 已实现基线：`2026-08-30-kaf-delegation-execution-integrity-design.md`
> 真实变更夹具：`../../testing/kaf-delegation-release-closeout-fixture.md`

## 1. 背景与目标

ITSM `main` 已包含 KAF 委派、事务性 Outbox、任务范围 API、action ledger、完成回执和恢复协调能力；KAF feature worktree 已包含委派 delivery lease 与 completion payload 重放能力。现阶段的缺口不是重做这些基础能力，而是把它们验证为一个可重复、可审计、可恢复的 Dev 稳定基线。

本增量以一个真实 SSLVPN Service Request 为权威场景，在当前机器的 KAF Dev 环境使用真实 `kaf_automation` 主体和 Microsoft Graph，把 `Julian@dawnpro.onmicrosoft.com` 加入固定 Azure AD Security Group，证明跨进程链路、恰好一次 ITSM 接受语义和外部权限恢复闭环成立。同时用不触发第二次 Graph 状态变更的确定性 breaker 覆盖重放、恢复、认证、租户、附件和 RLS。

发布收口通过之后，才单独 brainstorm 和设计统一 Intake。此处通过现有官方 process-trigger API 注入冻结的 `intake_snapshot`，只作为验收建单方式，不建立第二套 Intake 产品合同。

## 2. 已确认决策

本设计采用“受控真实主路径 + 无 Graph breaker”的收口方式：

- 只运行一次需要真实 Azure AD 加组的正式主路径，并在证据收集后强制恢复为非成员。
- action 重放与 completion payload 恢复必须走已持久化 payload 的 replay-only 路径，不得再次运行 Procedure 或 Tool。
- 并发、错误主体、跨租户、附件最小披露、callback failure 和 RLS 使用确定性或本地 PostgreSQL 探针验证，不重复制造真实权限变更。
- 修改现有唯一权威 `vpn_permission_grant` Procedure，使其从 LDAP 收敛到 Microsoft Graph；不创建 E2E 专用 Procedure、测试专用生产端点或 headless VPN 特判。
- ITSM 仍是 WorkItem、BPMN、租户、权限、action ledger、timeline 和 audit 的权威系统；KAF 仍是 Procedure 选择、Tool 调用和执行恢复的权威系统。

## 3. 范围

### 3.1 本增量包含

1. 为单次 `kaf-context` 成功读取增加显式、租户范围、无敏感内容的审计。
2. 为 delegated-list 增加单次聚合审计，避免列表中的每个任务产生一条 context audit。
3. 将权威 `vpn_permission_grant` Procedure 改为 Graph-only 的 `ad_grant_vpn_access`。
4. 在 KAF assembler 中把冻结 intake 的 `collected_fields.user_identifier` 显式绑定到 Graph Tool 输入。
5. 通过既有 Procedure ingest 流程同步 Qdrant 与 `ProcedureManifest`。
6. 验证 ITSM/KAF 健康、迁移、专用认证配置和真实跨进程 SSLVPN Service Request 主路径。
7. 验证 completion replay、恢复竞争、认证/租户拒绝、附件最小披露、callback failure 和 PostgreSQL RLS。
8. 将命令、结果、基数、失败和恢复状态写入既有执行完整性验收报告的 Live Dev Closeout Addendum。

### 3.2 明确不包含

- 统一 `CreateWorkItemCommand`、Intake Session、Intake 幂等或新的建单 UI。
- Incident `assign`、`resolve`、`close` typed actions。
- KAF headless pipeline 的结构性拆分或 Procedure 版本模型重构。
- LDAP Tool 的全局下线；本轮只禁止 SSLVPN 权威 Procedure 和验收路径使用 LDAP。
- KAF PROD `10.128.35.195` 的读取、写入或生产凭据使用。
- 以直接数据库写入、测试专用端点或第二套审批/工作流机制建立测试数据。

## 4. 架构与权威边界

```text
ITSM official WorkItem/Service Request API
  -> WorkItem
  -> official process-trigger API + frozen intake_snapshot
  -> existing BPMN approval path
  -> KAF delegation Outbox/webhook
  -> KAF delivery claim and authoritative vpn_permission_grant
  -> ad_grant_vpn_access
  -> Microsoft Graph / configured Azure AD group
  -> persisted complete_bpmn_task payload
  -> ITSM task-scoped action ledger and completion receipt
  -> BPMN advancement, timeline and audit
  -> replay same persisted payload
  -> controlled remove_vpn_access cleanup
```

任何层都不得绕过其权威边界：

- ITSM 不执行 Graph 权限变更，也不从 UI 状态推断授权。
- KAF 不直写 ITSM 数据库，不建立 WorkItem/BPMN 状态副本。
- Procedure 不硬编码 tenant、用户、组 Object ID 或凭据。
- 外部组只由 KAF 的 `VPN_USERS_GROUP_ID` 配置选择；本次固定 Object ID 仅记录在测试夹具和 Dev 配置中。
- 冻结 intake 中只携带业务输入，例如 `operationKind` 和 `user_identifier`；它不能覆盖目标 Azure AD 组。
- 所有业务状态变化通过官方 API、BPMN 或注册 Tool 完成；数据库只允许只读证据查询和专用 RLS 探针。

## 5. 组件与数据流

### 5.1 ITSM context 与列表审计

`GetTaskContext` 在完成现有认证、tenant/task scope 校验和 context 组装后、向调用方返回内容前，持久化一条 `kaf_delegate.context_read` 审计。审计失败时返回 5xx，不泄露 context，不能以日志代替持久化审计。

`ListDelegatedTaskPage` 每次成功请求只持久化一条 `kaf_delegate.list` 聚合审计。列表内部应复用一个不产生逐条读取审计的私有 context 组装路径；不能通过循环调用公开 `GetTaskContext` 形成 N+1 审计。

允许写入 audit 的字段限定为：认证派生的 tenant/actor、请求或 correlation 标识、task ID（仅单任务读取）、返回数量（仅列表）、resource/action、path、method 和 status。禁止写入标题、描述、附件元数据、冻结 intake 内容、JWT、secret、Tool 输入/输出或用户未脱敏内容。

以下情况继续 fail closed：错误主体、错误 task scope、跨租户、过期/无效 JWT 和不允许的任务状态。拒绝审计沿用既有安全审计边界，不通过本增量扩张为另一套审计模型。

### 5.2 权威 SSLVPN Procedure

现有 `scripts/procedures/vpn_permission_grant.md` 是唯一权威定义，修改后只引用 `ad_grant_vpn_access`，不得再引用 `ldap_grant_vpn_access`。Procedure 输入只保留真实执行需要的字段，并移除 `target_vpn_group`、`is_vendor`、`vendor_name` 等 LDAP 或未消费字段，避免形成虚假的双后端合同。

KAF assembler 对该操作显式绑定：

```text
intake_snapshot.collected_fields.user_identifier
  -> ad_grant_vpn_access.user_identifier
```

Tool 从 `IT_BACKEND=graph` 和 `VPN_USERS_GROUP_ID` 读取后端及目标组。缺少 Graph 配置、用户解析失败或组配置无效都必须显式失败，不能 fallback 到 LDAP、默认组或静默成功。

Procedure 修改后必须使用仓库既有 ingest 流程发布到 Dev 的 Qdrant/Procedure registry，并核对检索内容、content hash/version 和 `ProcedureManifest` 一致。不得通过直接编辑 Qdrant 或数据库规避 ingest。

### 5.3 验收建单与委派

在统一 Intake 尚未实现期间，验证器先通过现有官方 WorkItem/Service Request API 创建记录，再使用 `/api/v1/process-trigger` 启动既有流程，并在 `Variables` 中提供冻结的 `intake_snapshot`。最小业务内容包括：

```json
{
  "operationKind": "vpn_permission_grant",
  "collected_fields": {
    "user_identifier": "Julian@dawnpro.onmicrosoft.com"
  }
}
```

tenant、actor、认证主体和 task scope 必须来自正式认证及流程数据，不得由 payload 自报覆盖。审批使用现有 BPMN 能力；完成审批后由既有 Outbox/webhook 触发 KAF，不直接调用 headless runner。

### 5.4 KAF 执行与完成

KAF 沿用现有 delivery identity、lease、Procedure retrieval、assembler、Tool registry、completion client 和恢复扫描器。`KafDelegationPipeline` 不增加 VPN 特判。

Graph 加组成功后，KAF 在调用 ITSM 前持久化完整、受限且脱敏的 `complete_bpmn_task` payload。一旦该 payload 存在，该 delivery 永久进入 replay-only 语义：网络错误、`in_progress`、进程崩溃或 delegated-list 再次发现任务时，都只能重放相同 payload，不能再次执行 Procedure 或 Graph Tool。

ITSM 返回 `applied` 后 KAF 收敛为 completed；相同 scope/payload 再次提交必须返回 `already_applied`，且 action ledger、completion receipt、BPMN advancement、timeline 和 audit 的业务副作用基数保持为一。

### 5.5 真实 Azure AD 夹具与恢复

真实对象以 `docs/testing/kaf-delegation-release-closeout-fixture.md` 为唯一验收夹具：

- 用户：`Julian@dawnpro.onmicrosoft.com`
- Group Object ID：`b7c7f066-3042-4a11-9e36-2ea80b979ae3`
- 授权 Tool：`ad_grant_vpn_access`
- 恢复 Tool：`remove_vpn_access`

执行器首先通过 Microsoft Graph 只读查询当前成员状态。正式场景的起点必须是非成员；如果当前为成员，先执行受控移除并再次只读确认。主路径结束或后续任何证据步骤失败时，只要 Julian 为成员，都必须进入 finally-style 恢复并确认非成员。

## 6. 失败、恢复与停止条件

| 条件 | 预期行为 | 外部状态要求 |
|---|---|---|
| 健康、迁移、配置、身份或 Graph 预检失败 | 停止正式场景并记录 blocker | 不发生加组 |
| context 认证或审计持久化失败 | KAF 标记可重试或 `failed_auth`；BPMN 保持等待 | 不调用 Graph |
| Graph grant 失败 | 不生成成功 completion，不推进 BPMN | 确认为非成员或立即恢复 |
| Graph 已加组、completion payload 尚未持久化时 KAF 崩溃 | Procedure 可能被恢复后再次调用；Graph add 必须依靠 Tool 幂等收敛 | 只允许一次成员状态变化，可有可解释的重复 HTTP 尝试 |
| completion payload 已持久化后 KAF 崩溃 | 只重放相同 payload，禁止再运行 Procedure/Tool | 不发生第二次 Graph 状态变化 |
| ITSM 返回 `in_progress` 或 callback/网络失败 | delivery 保持可恢复并重放相同 payload | 不重复 Graph Tool |
| ITSM 已应用但 KAF 未持久化完成 | replay 收到 `already_applied` 后收敛 | ITSM 业务副作用仍为一 |
| breaker 或证据查询失败 | 记录失败，但仍执行恢复 | 最终必须为非成员 |
| 恢复 Tool 或恢复确认失败 | 立即停止所有后续真实变更测试，输出人工恢复信息 | 不得宣称发布收口通过 |

恢复前必须重新解析并核对准确的用户 Object ID、目标 Group Object ID 和当前 membership，避免对错误对象执行移除。异常、日志、持久化错误和报告必须应用既有递归脱敏与长度限制。

真实 Graph 场景必须串行执行。并发、lease reclaim、重复 webhook 和 callback breaker 只能使用确定性测试或不会触发 Graph 的 replay-only 路径。

## 7. 验证设计

### 7.1 ITSM 自动化验证

至少覆盖：

1. 单次 `kaf-context` 成功返回只产生一条 `kaf_delegate.context_read`。
2. audit 持久化失败时 context 不返回。
3. delegated-list 成功返回只产生一条 `kaf_delegate.list`，不按结果数产生 context audits。
4. 新增 audit 不包含标题、描述、附件内容、URL、token、secret 或 intake 快照。
5. 错误主体、task scope、跨租户和附件访问继续 fail closed。
6. 执行 KAF 委派相关 focused tests、`go test ./... -count=1`、`go build ./...`。
7. 使用真实 `RLS_TEST_DSN` 执行带 `integration_rls` tag 的 PostgreSQL 探针；任何 skip 都不能算通过。

### 7.2 KAF 自动化验证

至少覆盖：

1. 权威 Procedure 只引用 `ad_grant_vpn_access`，不存在 LDAP grant 引用。
2. assembler 把冻结 intake 的用户标识绑定到 Graph Tool 输入，且不存在默认组或 LDAP fallback。
3. ingest 后 Qdrant 内容与 `ProcedureManifest`/content hash 一致。
4. 已持久化 completion payload 的网络失败、`in_progress`、恢复扫描和重复 webhook 都不重新执行 Procedure。
5. Graph Tool 对已存在成员返回幂等成功，对配置/解析错误显式失败。
6. 执行 delegation、assembler、VPN Tool、Procedure ingest 的 focused suites。
7. 按 KAF `AGENTS.md` 运行 repository-wide pytest，记录准确的 passed/failed/skipped/xfailed 数和退出码。修改范围内不得新增失败；已有范围外失败必须逐项归类，不能把非零退出码描述为全绿。

### 7.3 真实 Dev 跨进程验收

按以下固定顺序串行执行：

1. 核对 ITSM/KAF 当前 commit、服务健康、数据库 migration head 和专用 `ITSM_KAF_*`/Graph 配置；只记录配置是否存在，不输出值或 secret。
2. 获取真实 Dev `kaf_automation` JWT，并证明它是 task-scoped 主体；JWT 不落盘、不入报告。
3. 通过 Graph 建立 Julian 非成员基线。
4. 通过官方 API 创建 WorkItem、启动既有 BPMN、提交冻结 intake snapshot 并完成所需审批。
5. 等待 Outbox 交付、KAF claim、Procedure/Tool 执行和 ITSM action。
6. 只读确认 Julian 已成为目标组成员。
7. 只读查询同一 correlation/task scope 的 WorkItem、ProcessTask、Outbox、delivery、action ledger、completion receipt、timeline 和 audit，证明每类权威副作用基数符合设计。
8. 重放同一持久化 completion payload，确认 `already_applied`，并再次证明 BPMN、timeline、audit 和领域副作用未重复。
9. 执行不会触发 Graph 的错误主体、跨租户、附件最小披露、recovery/callback breaker。
10. 使用配置的 PostgreSQL `RLS_TEST_DSN` 实际运行 RLS tenant-isolation 探针并确认零 skip。
11. 调用 `remove_vpn_access`，再通过 Graph 只读确认 Julian 已恢复为非成员。

## 8. 证据与验收报告

不新建第二份彼此竞争的执行完整性报告。实施完成后，在 `docs/reports/2026-08-30-kaf-delegation-execution-integrity-report.md` 增加 **Live Dev Closeout Addendum**，至少记录：

- ITSM 与 KAF 的精确 commit、migration head、执行时间和 Dev 环境标识。
- 每条验证命令、退出码、准确测试计数和 skip 原因。
- 脱敏的 WorkItem/task/event/delivery/action ledger/receipt/correlation 引用。
- Graph 执行前、授权后、恢复后的 membership 布尔状态，不记录凭据或 access token。
- Procedure/Tool 调用次数、ITSM replay 返回值和各类业务副作用基数。
- RLS 探针实际执行且零 skip 的证据。
- 遇到的失败、重试、人工干预以及最终恢复确认。

报告不得包含 Azure client secret、KAF automation JWT、webhook secret、原始 Tool 输出、未脱敏异常、附件 URL/路径或敏感 intake 内容。证据查询脚本如进入仓库，必须只调用官方 API/Tool 和只读数据库查询，并在使用前验证目标环境不是 PROD。

## 9. 完成标准

只有同时满足以下条件，才能将执行完整性发布收口标记为通过：

1. ITSM/KAF 修改范围的 focused tests 全部通过；ITSM 全量 Go test/build 通过；KAF 全量 pytest 的实际结果被如实记录，且修改范围内无新增失败。
2. `kaf-context` 和 delegated-list 审计满足单条/聚合、fail-closed 和无敏感内容要求。
3. 权威 SSLVPN Procedure 已从 LDAP 收敛为 Graph，且 Dev registry/manifest 与源码一致。
4. 一次真实跨进程 Service Request 从 BPMN 委派到 Graph 加组再到 ITSM 完成收敛成功。
5. 同一 completion payload 重放返回 `already_applied`，不产生第二次 Graph、BPMN、timeline、audit 或领域副作用。
6. 认证、tenant、附件、恢复竞争和 callback breaker 得到可复现证据。
7. PostgreSQL RLS 探针使用真实 `RLS_TEST_DSN` 执行，不能由 deterministic SQL、SQLite 或 skip 代替。
8. Julian 最终经 Graph 确认为目标组非成员；恢复失败时无条件判定收口失败。
9. Live Dev Closeout Addendum 与命令输出、持久化记录和 Graph 状态一致。

收口通过只建立 Dev 稳定基线，不构成 PROD 发布批准。统一 Intake、Incident typed actions、KAF 执行模型收敛和 UI 仍按上位设计中的独立 spec → plan → implementation 周期推进。

## 10. 预期实现边界

实施计划应围绕以下现有边界细化任务，不引入新的横向抽象：

- ITSM：`KafDelegationService` 的 context/list audit 及其 service/controller/tenant/RLS 回归测试。
- KAF：`scripts/procedures/vpn_permission_grant.md`、assembler 的 typed input binding、既有 Procedure ingest 与相关测试。
- Dev 验收：复用官方 process-trigger、BPMN、delegation、action 和 Graph Tool 接口的可重复验证步骤。
- 文档：真实变更夹具与既有执行完整性验收报告 addendum。

任何实施中发现需要新增 Intake 应用服务、测试专用 API、VPN headless 特判、硬编码组/tenant 或第二套 Procedure 的情况，都视为偏离本设计，应停止并重新评审，而不是在发布收口中顺带实现。
