# SSLVPN 委派可靠性收口设计

> 状态：评审意见已修订，待重新批准
>
> 日期：2026-09-04
>
> 关联：[AGENTS.md](../../../AGENTS.md)、[SSLVPN Worker 生产就绪设计](2026-09-03-sslvpn-worker-production-readiness-design.md)、[生产就绪证据报告](../../reports/2026-09-03-sslvpn-kaf-worker-production-readiness-report.md)
>
> KAF 约束：`/home/administrator/actions-runner/_work/kaf/kaf/AGENTS.md` 与 `docs/kaf2/AGENTS-REFERENCE.md`

## 1. 决策摘要

此前变更已经完成 `kaf_delegate_requested` 从 ITSM API 到独立 Worker 的硬切换。本设计不恢复 API consumer，不增加回退路径，也不重新设计 SSLVPN 的 Service Request、BPMN 审批或 KAF Procedure。

本期处理运行态验证暴露出的四类剩余风险：

1. ITSM 升级迁移历史与 fresh install 使用同一可变 SQL 流，导致已发布 checksum 漂移。
2. ITSM Worker、KAF Backend 和跨系统 ACK 的 readiness/acceptance 语义不足以证明链路可用。
3. KAF delegation 的执行重试、dead-letter 和外部 Tool step fencing 不完整。
4. ITSM delegated execution reconciliation 以 AuditLog 充当业务授权状态，结论没有绑定观察时的投递尝试。

实施拆为四个独立工作流，每个工作流有自己的计划、测试和提交。Workstream 2 包含不可颠倒、可独立发布的 2a/2b：

1. ITSM migration lineage 与 fresh baseline。
2. 跨仓 ACK 切换、ITSM Worker 与 reconciliation 治理。
3. KAF delegation recovery、readiness 与 side-effect fencing。
4. 跨系统契约与受控 SSLVPN 演练。

依赖顺序是 `1 -> 2a(KAF acceptance) -> 2b(ITSM strict validation) -> 3 -> 4`。2a 必须先更新权威 OpenAPI/fixture，再发布并证明 KAF 返回新响应；只有这项生产等价证据通过，2b 才能启用 ITSM 严格校验。前一阶段未达到验收门槛时，不执行后一阶段的运行态变更。

## 2. 约束来源与优先级

所有实现必须同时满足两个仓库的 `AGENTS.md`。发生解释冲突时，先遵守各自仓库的本地约束，再遵守本设计；不得以计划内容规避仓库约束。

不可妥协的约束如下：

- ITSM 是 WorkItem、Service Request、BPMN、tenant、RBAC、审计和最终业务状态的唯一事实源。
- KAF 只拥有 Procedure、Tool 执行和 KAF 本地恢复账本，不读取或写入 ITSM 数据库。
- API 不消费 `kaf_delegate_requested`；Worker 是唯一投递者，没有同步 fallback 或双消费窗口。
- 未知 event、handler、Procedure、Tool、状态、配置或响应必须 fail closed。
- 不新增长期兼容层、双写、双读、旧新并行状态机或静默 fallback。
- AuditLog 记录已经发生的事实，但不承担可变业务授权状态。
- 所有 tenant、actor、task、correlation 和 version 必须从受信身份或 ITSM 权威记录得到，不能相信 webhook/body 自报值。
- 外部 LDAP/Graph 写操作必须具备审批证据、稳定执行身份、持久化 fencing、审计和不确定结果处理。
- 生产 secret 继续按运行角色通过只读文件注入，不进入代码、镜像、日志或 API 响应。
- 邮件告警、具体收件人、Langfuse 数据治理和入口 CIDR/TLS 仍是已确认 Backlog，不得计入本期已交付能力。

## 3. 当前事实与问题边界

### 3.1 已验证能力

- 委派任务、流程 wait state、审计和 Outbox 在 ITSM 同一事务内创建。
- Worker 在 HTTP 调用前持久化 delivery-attempt marker。
- transport/response 不确定结果进入 `delivery_unknown`，不会自动重发。
- KAF 在返回 202 前完成 HMAC 校验、事件校验和 receipt 持久化。
- KAF 使用 event ID 以及 tenant/task/correlation identity 去重，并使用执行 lease。
- ITSM completion action 校验 `kaf_automation`、tenant、task、correlation、expected version 和稳定 idempotency key。
- BPMN completion receipt 和 action ledger 能把完成回放收敛为 `already_applied`。

### 3.2 运行态阻塞

2026-09-04 的只读检查得到：

- ITSM `/api/v1/readyz` 返回 503；数据库最高已应用 migration 为 019，运行代码要求 022。
- 本地没有 `kaf-worker` 实例，因此当前是零投递者，而不是双投递。
- KAF delegation HMAC、ITSM callback URL 和 automation token 未形成可运行配置。
- KAF `/health` 只证明 HTTP 进程存活。
- KAF sandbox 不抑制 `OpType.LDAP`，而 SSLVPN grant Procedure 会调用 `ldap_grant_vpn_access`。

因此本设计阶段及前三个实施工作流不得创建或审批真实 SSLVPN 请求。

## 4. 方案选择

### 4.1 采用：分域收口并以跨系统契约汇合

ITSM migration、ITSM delivery、KAF execution 和最终 E2E 分别实现、测试和提交。跨仓库只共享版本化 HTTP 契约与稳定关联 ID，不共享数据库模型或内部状态。

优点：每一阶段可以独立拒绝或批准，故障归属明确，符合单一事实源和向下依赖原则。

### 4.2 不采用：在当前迁移器上增加历史 checksum 白名单

单纯允许多个 checksum 会让执行器接受被修改过的已发布 SQL，却不能证明数据库已经具备后来增加的 tenant/RLS 语义。这是兼容补丁，不是迁移修复。

### 4.3 不采用：先跑通本地演练，再补 readiness 和 fencing

当前 sandbox 可能执行真实 LDAP 写入，Worker 又无法识别 schema 不兼容。先演练会把验证活动变成不可控外部变更。

## 5. Workstream 1：ITSM migration lineage 与 fresh baseline

### 5.1 模型

迁移系统分离三种事实：

1. **Published upgrade lineage**：已经发布的顺序升级脚本及其原始 checksum；文件一经发布永久不可修改。
2. **Fresh baseline**：为全新数据库建立当前 schema 所需的版本化资产，包含 Ent schema 之外的 RLS、索引、约束、触发器和初始化基础设施。
3. **Current schema state**：数据库当前兼容的 schema release/version，是 readiness 的权威来源；它不由“schema_migrations 是否恰好存在最后一行”间接推断。该事实存放在单行 `schema_state` 表中，固定主键为 `1` 并由数据库约束拒绝其他主键值，字段为 `release_id`、`schema_version`、`baseline_version`、`release_manifest_checksum` 和 `updated_at`。

`schema_migrations` 保留实际执行历史。Fresh install 不伪造自己逐条执行过所有历史升级脚本。每个 release 使用一个不可变 manifest，按确定顺序列出 release ID、Ent schema fingerprint、upgrade head、fresh-baseline 资产及 SHA-256、platform seed component/version。升级和 fresh install 最终都必须原子写入同一个 current schema state；readiness 同时校验精确 release ID、schema/baseline version 和 manifest checksum。

### 5.2 不可歧义的 lineage manifest

Lineage manifest 是随代码评审的只读验证资产，不是运行时“可接受 checksum 列表”。每一行固定：历史 ledger version、逻辑文件位置、Git commit、Git blob、原始 SQL SHA-256、当前 catalog 归属、是否可执行、所需 forward migration。

2026-09-04 本地 ledger 已观察到的漂移基线如下。当时 SQL 以内嵌 raw string 发布，因此逻辑文件明确指向对应 commit 中的同一文件，而不是当前工作树中的可变副本：

| 历史 ledger version | 历史逻辑文件 | Git commit | Git blob | 原始 SQL SHA-256 | 当前 catalog 归属 | 可执行 | Forward requirement |
|---|---|---|---|---|---|---|---|
| `007_add_change_execution_tables` | `itsm-backend/migration/migrations.go` | `a5370db83d89d22ac8b9a2b75e6d7afcb5b40d7b` | `d30270e1425a57d5182366115b6dc74282524c9f` | `1cf4fab4573d373957f8d22012e60652400eeffd09c1caf118ec640761b13d4a` | immutable upgrade lineage | 是 | 新 forward migration 以 WorkItem/tickets 为 tenant 权威修复六张 change execution 表 |
| `009_enable_rls_tenant_isolation` | `itsm-backend/migration/migrations.go` | `ef7b16a644d94b7fe53e3fce7b519350de070180` | `8951c874999ad205eecf866074704e645ed13f37` | `b88712993b527f72c945e506fecbb41da54e2aeada19317dff9bc489a94ecea0` | immutable upgrade lineage | 是 | 新 forward migration 收敛当前 tenant setting 与 RLS policy |
| `015_process_instance_running_unique_guard` | `itsm-backend/migration/migrations.go` | `5898e2244d1534e588b4659dc867bdecc7e1deb8` | `10c6109f3a3855e7ecff83847c97bee9e04d5a30` | `624c72f3fc88b299570f556742959bc1e436574881ee080b3dcd85864d1049f6` | immutable upgrade lineage | 是 | 无；恢复原始字节，当前差异仅为说明文本 |
| `015_add_service_request_contact_fields` | `itsm-backend/migration/migrations.go` | `6e52278393896d5dc13f6a60ee83c0ae00073c8f` | `b08cce42de3482d9a9c0d90af55ba4ec87b9703b` | `917e74af4aca87b2f40370239ed41c61c847b9c32588ffc8680ecaaad73a0b67` | historical validation-only lineage；当前 canonical version 为 `016_add_service_request_contact_fields` | 否 | 016 作为唯一可执行 canonical migration；不得把旧 015 作为第二条执行路径 |

Manifest 校验使用 version 对应的单一原始 SHA-256；不得为同一个 version 配置多个“均可接受”的 checksum。若其他已部署环境出现表外 version/checksum，先补充来源证据并重新评审 manifest，执行器仍保持 fail closed。

### 5.3 历史修复规则

- 从上表指定的 Git commit/blob 恢复已经部署过的 007、009、015 migration 原始 SQL 字节并逐项验证 SHA-256。
- 将历史 `015_add_service_request_contact_fields` 纳入 validation-only lineage；它不可被新环境执行，也不是 016 的别名。
- 007 tenant 来源和 009 RLS 当前需要的语义只能通过新的 forward migration 应用。
- 新 migration 编号在实施分支从最新 `origin/main` 分配，不复用任何已存在或其他待合并分支已经占用的版本。
- 禁止直接更新或删除生产/共享数据库的 `schema_migrations` 行。
- 对无法证明来源的 checksum 或未知 version 继续 fail closed，并输出不含凭据的诊断。

### 5.4 Fresh install

Fresh baseline 必须是版本化、可重复验证的安装资产。它与 upgrade lineage 共享最终 schema invariant 测试，但不调用历史升级脚本来修补 Ent 创建出的最新表。

Fresh install 完成顺序：

1. 获取 migration/bootstrap 专用锁。
2. 创建当前 Ent schema。
3. 应用当前 baseline 的非 Ent 数据库资产。
4. 执行 schema invariant 检查和 tenant/RLS 检查。
5. 写入 current schema state。
6. 执行版本化 platform seed，并写现有 initialization ledger。

所有 DDL/baseline step 必须是声明式可重入操作，并在执行后校验目标定义，而不只依赖 `IF NOT EXISTS`。进程中断后的恢复规则为：持有同一 advisory lock，读取 migration history、initialization component ledger 和 current schema state；跳过已提交且校验一致的 step，重做未提交 step；若已存在对象与 manifest 定义不一致则 fail closed。Platform seed 复用现有 component-attempt ledger，从未成功的 component 继续。任一步失败均保持 readiness=false；不得启动 API 或 Worker 绕过失败。

`schema_state` 只允许 migration principal 写入；API/Worker runtime principal 只有 SELECT 权限。写入前必须重新计算并验证 release manifest checksum。

### 5.5 升级现有数据库

升级器先校验 immutable lineage，再按顺序执行尚未应用的 forward migrations。每个 migration 与其 history row 在同一事务提交。若进程在 migration 已提交、`schema_state` 尚未提升时退出，重启后必须从 history 验证已提交步骤、重跑最终 invariant，再提升 state，而不是重新执行或篡改 history。全部完成并通过 invariant 后，才更新 current schema state。

本地当前数据库只在以下证据齐备后允许升级：

- 所有已有 ledger row 已映射到 immutable published lineage。
- materially changed 的 tenant/RLS 行为都有 forward migration。
- upgrade dry-run/status 成功。
- 数据库负责人明确批准写操作并确认备份/恢复路径。

## 6. Workstream 2：跨仓 ACK、ITSM Worker 与 reconciliation

### 6.1 共享 readiness 服务

API 与 Worker 必须调用同一个下层 initialization-readiness 组件。该组件不属于 `router`，也不依赖 Gin；输入为数据库连接和 required schema/baseline 描述，输出为不含敏感信息的结构化结果。

API readiness 检查：

- 数据库连接；
- current schema state 精确匹配；
- platform baseline 全组件成功且版本匹配。

Worker readiness 在以上检查之外还要求：

- Worker 专用配置已经通过构造期校验；
- Outbox 必需表、字段和约束可查询；
- dispatcher lifecycle 为 `running`，且最近一次 scheduler heartbeat 未超过 `KAF_WORKER_SCHEDULER_HEARTBEAT_TIMEOUT`。该 timeout 必须大于 poll interval，并由配置校验，不能成为隐藏常量。

KAF 短时不可达不令 Worker 退出或伪装未启动；它通过 delivery 状态和指标表达。schema 或必需配置不兼容则 readiness=503。

Dispatcher 暴露线程安全、只读的生命周期快照：`constructed -> running -> stopping -> stopped`。`Run` 进入调度循环后设置 running；每次成功完成一次 repository scheduler sweep 后更新 heartbeat，即使该轮没有 due event；DB/scheduler 连续失败导致 heartbeat 过期并使 readiness=503。收到 context cancel 时先进入 stopping，退出循环后进入 stopped。Readiness 不以“goroutine 已创建”或 KAF 网络可达作为 running 证据。

### 6.2 严格 acceptance contract

#### 6.2.1 2a：先冻结合同并发布 KAF producer response

`docs/contracts/kaf-delegate-requested.openapi.yaml` 是该 HTTP 边界的唯一权威合同。先将其升级到新的 contract version，再从同一目录的固定 fixture 验证 KAF 与 ITSM。KAF 必须先实现并在生产等价环境证明新 202 响应，ITSM 此时仍保持旧接收逻辑；不得在 2a 同时开启 ITSM 严格校验。

KAF delegation 使用独立的 acceptance DTO；不得扩大或复用仍服务于 legacy lifecycle webhook 的 `ItsmWebhookResponse`，避免把旧 `ticket_id` 合同带入新边界或意外改变 legacy lifecycle 响应。

OpenAPI 对 `KafWebhookAccepted` 的定义为：

- HTTP status 只允许 202 表示 durable acceptance；
- response media type 必须解析为 `application/json`，允许大小写无关的媒体类型和合法参数（例如 `charset=utf-8`）；
- required 为 `status`、`eventId`、`taskId`、`correlationId`；字段名一律 camelCase；
- `status` 只能为 `accepted`；三个 identity 字段必须逐项回显请求值；
- `additionalProperties: false`；禁止旧 `ticket_id` 字段；
- acceptance body 上限为 4 KiB。

固定成功 fixture 存放在 `docs/contracts/fixtures/kaf-delegate-requested/accepted.json`。两仓测试直接读取同一份 fixture 或其发布制品，不各自手写第二份期望 JSON。OpenAPI 和 fixture 必须在同一提交更新并做 schema validation。

#### 6.2.2 2b：KAF 证据通过后启用 ITSM strict consumer

KAF 成功接收响应固定为 HTTP 202：

```json
{
  "status": "accepted",
  "eventId": "<same event UUID>",
  "taskId": "<same task ID>",
  "correlationId": "<same correlation ID>"
}
```

ITSM Worker 只有在状态码、Content-Type、JSON shape 和全部 identity 字段一致时才能 `MarkPublished`。

- 明确的 4xx 契约/认证拒绝进入 `blocked`。
- 429/5xx 在上限内重试。
- 1xx、3xx 以及 202 之外的 2xx 不跟随 redirect，也不视为成功；由于请求已到达远端但无法证明是否持久化，进入 `delivery_unknown`。
- 202 的 Content-Type 错误、body 超过 4 KiB、malformed JSON、额外字段、缺失字段或 identity mismatch 均进入 `delivery_unknown`，因为 KAF 可能已经持久化，禁止自动重投。
- request 已发送但 response headers/body 读取中断同样进入 `delivery_unknown`。
- 只有完整、受限且与 OpenAPI/identity 完全一致的 202 响应进入 `published`。

禁止为旧 KAF 200/204 响应保留兼容分支。

### 6.3 Reconciliation 是结构化业务状态

新增 tenant-owned delegated execution reconciliation 记录，由 `handlers/delegated_execution` 服务拥有。记录至少包含：

- tenant ID、event ID、task ID、correlation ID；
- observed outbox status、observed attempt count、observed delivery version、observed time；
- conclusion、reason、actor ID、created time；
- consumed time 和 consuming requeue audit reference。

Outbox 新增单调 `delivery_version`，默认从 1 开始；每次 claim、attempt marker、retry、terminal transition、published 和 requeue 均以条件更新递增。它表达一次精确观察，不能由 `updated_at` 或可反复出现的 status 替代。

数据库对 `(tenant_id, event_id)` 建立“未消费记录至多一条”的部分唯一约束。重复操作者对同一 event 写入第二条未消费 reconciliation 时返回 conflict，而不是覆盖原记录。

写入 reconciliation 的事务必须重新读取 tenant-owned Outbox event 并执行以下策略：

- `not_accepted_not_started` 仅允许用于当前 `blocked` 且不是 `delivery_unknown` 的 event。
- `delivery_unknown_manual_followup` 仅允许用于当前 error class 为 `delivery_unknown` 的 event。
- pending、publishing、published、dead-letter 不接受这两个结论。

Requeue 必须在同一事务中以固定顺序 `SELECT ... FOR UPDATE` 锁定 tenant-owned Outbox event 和未消费 reconciliation；确认 delivery version、status、attempt 与观察值仍一致；以包含旧 delivery version 的条件更新把 event 转为 pending并递增 version；写 requeue audit 后设置 `consumed_at`、`consumed_by_audit_id`。任一步失败整体回滚。旧结论、已消费结论、version/attempt 已变化或 delivery_unknown 全部拒绝。

AuditLog 继续记录 actor、tenant、event/task/correlation、结论和原因，但业务判断读取 reconciliation 记录，不反查 AuditLog request body。

权限代码保持现有 `delegated_execution:view`、`delegated_execution:reconcile`、`delegated_execution:requeue`，只显式授予 sysadmin，不通过宽泛角色规则传播。

## 7. Workstream 3：KAF delegation execution hardening

### 7.1 显式启用和 readiness

KAF 使用 `ITSM_KAF_DELEGATION_ENABLED` 作为唯一功能开关。启用时，以下配置必须作为一个整体通过启动校验：

- ITSM task API base URL；
- delegation HMAC secret；
- `kaf_automation` token；
- 正数 recovery interval、retry limit 和 backoff 参数。

禁止从“某个字段非空”推断半启用状态。生产 Compose 显式启用 delegation。未启用时 webhook fail closed，且不会启动 recovery loop。

新增 readiness，与 `/health` liveness 分离。Readiness 至少验证：

- KAF PostgreSQL 可用且 Alembic revision 满足运行版本；
- delegation receipt/step-action 必需表可访问；
- 启用时配置完整且 recovery loop 存活。

它不通过调用 ITSM 或外部 LDAP/Graph 制造探针副作用。

### 7.2 Execution 与 completion 两阶段状态机

KAF delegation delivery 不再用一个 status 同时表达 Tool effect 和 ITSM completion。它持久化 `execution_status`、`completion_status`、各自的 attempt/next-attempt/lease，以及终止时间。

Execution 状态语义为：

- `received`：已持久化、待 claim；
- `running`：持有有效 lease；
- `retry_wait`：明确可重试错误，等待退避；
- `effect_confirmed`：全部外部 step effect/result 已持久化，不得再运行 Procedure/Tool；
- `failed_auth`：ITSM automation principal 失败，停止自动执行；
- `manual_intervention`：永久业务/Procedure/Tool 错误；
- `dead_letter`：临时错误耗尽重试预算；

Completion 状态语义为：

- `not_ready`：external steps 尚未全部确认；
- `pending`：durable effect/result 和稳定 completion payload 已提交，可发送 callback；
- `in_flight`：completion worker 持有 lease；
- `unknown`：callback 已发送但响应未知，只允许以同一 idempotency key 重放 completion；
- `retry_wait`：ITSM 明确返回临时错误，等待独立退避；
- `confirmed`：ITSM 返回 `applied` 或 `already_applied`；
- `failed_auth`：completion principal 被拒绝；
- `manual_intervention`：task/version/tenant/correlation 或响应合同发生永久冲突；
- `dead_letter`：completion 临时错误耗尽独立预算。

Execution recovery 只能 claim `received`、到期的 execution `retry_wait` 和过期的 execution `running`。一旦进入 `effect_confirmed`，任何恢复都不得回到 Procedure。Completion recovery 只 claim `pending`、`unknown`、到期的 completion `retry_wait` 和过期的 `in_flight`，始终使用已持久化 payload 与同一 idempotency key。`already_applied` 收敛为 confirmed。

最后一个 step 的 durable action result、sanitized result summary、completion payload、`execution_status=effect_confirmed` 和 `completion_status=pending` 必须在一个 KAF 数据库事务提交；事务完成前禁止发送 completion。这样 callback timeout 不会重新执行 Tool。

数据库升级使用确定性映射，不从错误字符串猜测阶段：

- `status=completed` 映射为 execution `effect_confirmed`、completion `confirmed`；
- `completion_payload IS NOT NULL` 且未 completed 的行映射为 execution `effect_confirmed`；原 status 为 failed_auth 时 completion 为 `failed_auth`，其他情况为 `unknown`，只重放 completion；
- 没有 completion payload 的 received/running/retryable 分别映射到 execution received/running/retry_wait，completion 为 `not_ready`；
- 没有 completion payload 的 failed_auth 映射到 execution `failed_auth`、completion `not_ready`。

迁移为 retry_wait 行设置 `next_attempt_at` 为升级时间。遇到表外状态 fail closed。代码和数据库迁移在同一 KAF 发布中删除旧单 status/`retryable` 运行分支，不保留状态别名或双读。

错误分类由类型化异常和 HTTP/Tool result code 驱动，不通过字符串关键字猜测：

- 网络、限流和明确的临时 5xx 可重试；
- task 不再 delegated、tenant/correlation/version mismatch、Procedure 缺失或无步骤、未知 Tool、缺失必需字段是永久/manual；
- 401/403 是 failed_auth；
- 已发起外部写入但结果未知时，按 step recovery policy 进入相应不确定状态，不重新运行整个 Procedure；该状态与 completion `unknown` 分离。

### 7.3 Step action fencing

Procedure step 的 `recovery` 元数据从说明字段升级为执行策略。每个外部写 step 使用由以下字段生成的稳定 execution key：

`tenant + task + correlation + procedure version + step index + tool name`

持久化 step action 必须有该 key 的唯一约束、claim lease、attempt、result status 和经过脱敏的结果摘要。执行前先 claim，不能在每次图重跑时无条件插入新的 started row。

策略：

- `idempotent`：lease 过期后允许以同一 key 恢复，并要求 Tool 自身验证目标状态。
- `retryable`：仅在确认外部调用未开始或明确失败时按上限重试。
- `compensatable`：失败进入人工补偿队列，不自动假定补偿成功。
- `manual`：外部调用开始后未确认结果，直接人工处理。

已成功 step 返回持久化结果，不再次调用 Tool。未知 Tool 或缺失 metadata 必须失败，不能直接调用未治理函数。

### 7.4 通用 sandbox policy

Sandbox 在现有 governed execution choke point 实施，不为每种 Tool 建立旁路。所有 Tool 和 connector action 必须显式声明 `read_only` 和 write action 的 `recovery` metadata；字段缺失、未知 capability 或未注册 fixture 一律 fail closed。

当 sandbox 启用时：

- 所有 `read_only=false` 的 Tool/connector action 均由契约匹配的 fixture adapter 响应，不能调用 Graph、LDAP、ITSM mutation、邮件、CLI 或未来 connector backend；
- read-only action 只有在显式标记并通过 allowlist policy 后才可访问受控测试依赖；默认 fixture 化；
- 每次被抑制的写操作写入低敏 `sandbox_effect_simulated` 审计，包含 capability、execution key、fixture version 和 result code，不含用户、DN、payload 或 secret；
- Tool 内部不得隐藏第二个外部写操作。邮件、审计型外部写或 connector 调用必须拆到同一 governed boundary 下的独立 action，并拥有自己的 execution key。

Fixture 存放在测试/seed 资产中，由 capability schema 校验；不得把租户用户、DN、组名、URL 或收件人写入生产 Python。

### 7.5 SSLVPN 的具体副作用边界

`ldap_grant_vpn_access` 的加组操作保留“先检查成员、再幂等 add”的领域行为。除此之外：

- KAF VPN grant 记录必须使用稳定 execution key 防重复。
- 完成通知必须从 `ldap_grant_vpn_access` 内部直接调用中拆出，通过可去重的 governed notification action 发送，不能在 Procedure 整体重跑时重复发送。
- LDAP、Graph、通知和所有其他写能力统一遵循 7.4，不建立 SSLVPN 专用 sandbox 分支。

本期不改变 Procedure 文档作为 Service Request 执行步骤唯一事实源的原则。

## 8. Workstream 4：跨系统契约与受控演练

### 8.1 自动化契约验证

跨仓库 contract suite 固定验证：

- event schema、字段命名、UUID/RFC3339、recordClass；
- HMAC 对完全相同 payload bytes 生效；
- `X-Event-ID` 与 body eventId 相等；
- 202 acceptance response 的四个字段完全匹配；
- KAF callback request/response、expectedVersion、idempotency key；
- duplicate event、duplicate step、duplicate completion 不产生第二次外部副作用或 BPMN 推进；
- tenant/task/correlation mismatch 均 fail closed。

两个仓库各自保留本地契约测试；跨系统测试使用固定版本 fixture，不通过复制生产实现生成期望值。

### 8.2 无外部副作用演练

第一阶段使用一次性、生产等价数据库和受控 KAF workspace：

1. 从 approved fresh baseline 初始化 ITSM。
2. KAF 升级到要求的 Alembic revision。
3. 启动一个 ITSM API、两个 ITSM Worker 和 KAF Backend/Gateway。
4. 确认 API/Worker/KAF readiness 内容，而不只检查 HTTP 200。
5. 启用通用 sandbox policy，以受控 fixture 拦截该流程及其嵌套调用中的所有非只读 Tool/connector action，再创建 SSLVPN 请求并完成两级审批。
6. 记录 event/task/correlation/execution key。
7. 证明一条 Outbox、一次 KAF receipt、一次 step effect、一次 completion、一次 BPMN 推进。
8. 重放 webhook、KAF recovery 和 completion，证明没有第二次副作用。
9. 注入 4xx、429、5xx、timeout、进程终止、lease expiry 和永久 Procedure error，验证状态机。

### 8.3 真实外部变更演练

真实 LDAP/Graph 演练不是前三个 workstream 的隐含授权。只有用户再次明确批准且满足以下前置条件后才允许执行：

- 使用既有受控夹具身份和目标组；
- 只读确认测试主体当前不是目标组成员；
- 指定变更负责人、恢复负责人和演练时间窗；
- 证明授权与回收命令均可用；
- 记录一条真实 add、幂等重放和最终 remove；
- 回收后只读确认恢复为非成员；
- 清理失败立即保持 No-Go 并升级人工处置。

## 9. 可观测性与敏感信息

ITSM Worker 指标继续保持低基数，只使用 event type、status、error class 等标签。KAF 新增低基数 delegation 指标，分别按 execution 与 completion 阶段表达 received/claimed/retry/effect-confirmed/confirmed/manual/dead-letter，并单独记录 lease lost 和 completion replay；不再用一个 `completed` 指标混淆外部 effect 与 ITSM completion。

任何指标、health/readiness、API 或默认日志均不得包含：

- webhook secret、automation token、Authorization；
- Outbox payload、Procedure 输入全文或原始外部错误 body；
- LDAP DN、用户邮件、人员身份、Langfuse trace 内容；
- tenant/task/event/correlation 作为 Prometheus label。

受权运维 API 可以返回 event/task/correlation 标识用于对账，但不能返回 payload、原始错误、lease owner 或凭据。

## 10. 测试策略

每个 workstream 均采用 TDD，测试层次如下：

### Migration

- immutable checksum 和 unknown lineage 单元测试；
- 从历史 ledger fixture 升级的 PostgreSQL 集成测试；
- 空数据库 fresh baseline 测试；
- fresh 与 upgrade 最终 schema invariant/RLS 等价测试；
- readiness schema state 测试。

SQLite 不作为 PostgreSQL RLS、锁、identity 或 migration SQL 的通过证据。

### ITSM

- Worker 与 API 共用 readiness 组件；
- dispatcher lifecycle transition、scheduler heartbeat freshness 和 DB sweep failure；
- 严格 202 ACK 及 malformed/mismatch/ambiguous response；
- reconciliation partial unique constraint、行锁、delivery version/attempt fencing 和重复操作者 conflict；
- tenant/RBAC/脱敏测试；
- 两 Worker 并发 claim。

### KAF

- 启用配置完整性和 readiness；
- retry backoff、ceiling、dead-letter、manual/failed_auth；
- 多实例 lease claim；
- step execution key 唯一性、lease recovery 和已成功回放；
- execution effect confirmed 后不再进入 Procedure；
- completion pending/unknown 独立 claim、稳定 payload/idempotency replay 和 `already_applied` 收敛；
- 通用 sandbox 对 Graph、LDAP、ITSM mutation、通知及测试 connector 均不调用真实 client，并拒绝缺失 metadata/fixture 的 capability；
- 每个模拟写产生低敏审计。

### Cross-system

- 固定 fixture contract tests；
- KAF 新 202 response 先于 ITSM strict consumer 的发布顺序测试/部署检查；
- 无副作用 E2E；
- 故障注入与 replay；
- 真实演练只作为单独批准的发布证据。

## 11. 发布与回滚

每个 workstream 独立提交和审查，不把 ITSM、KAF、数据库写操作混入同一不可审查提交。

推荐发布顺序：

1. 发布 migration/bootstrap 修复并升级受控 ITSM 数据库。
2. 发布 OpenAPI/fixture contract，再发布仅改变 delegation acceptance DTO 的 KAF 2a；用旧 ITSM consumer 证明新 202 响应。
3. 发布 ITSM 2b 的共享 readiness、strict ACK consumer、delivery version 和 reconciliation；验证 ITSM API 及两个 Worker readiness。
4. 发布 KAF execution/completion schema、bounded recovery、step fencing、通用 sandbox 与 readiness。
5. 配置双方专用 secret/URL/token，执行无副作用演练。
6. 经单独批准执行真实外部变更演练。

本设计不提供 API dispatcher fallback。应用回滚只能回到仍遵守“Worker 唯一消费者”的兼容版本；不可逆数据库变更采用 forward fix。若任一 readiness 或数据 invariant 失败，停止扩大流量并保持 Outbox/任务待处理状态。

## 12. 验收门槛

只有以下条件全部满足，生产就绪报告才能从 Conditional No-Go 改为 Go：

- immutable lineage、fresh baseline 和现有升级路径均通过 PostgreSQL 验证；
- `schema_state` release manifest checksum 与代码发布清单一致，runtime role 无写权限；
- ITSM API 及两个 Worker readiness=ready，且 API 没有 KAF dispatcher；
- Worker dispatcher lifecycle=running 且 scheduler heartbeat 未过期；
- Worker health/metrics 未发布宿主机端口；
- KAF readiness 证明 schema、receipt storage、配置和 recovery loop 可用；
- 严格 202 ACK contract 在两个仓库通过；
- KAF delivery 有独立 execution/completion 阶段、有界 retry、dead-letter/manual state 和可观测指标；
- step action fencing 证明 replay 不产生第二次受控副作用；
- reconciliation 使用结构化状态并绑定 observed delivery version/attempt，数据库保证每 event 至多一个未消费结论；
- sandbox 证明所有非只读 Tool/connector action 都被 fixture 化并留下低敏模拟审计；
- tenant、RBAC、secret masking 和数据库隔离检查通过；
- 无副作用 SSLVPN E2E 和故障注入通过；
- 若执行真实演练，最终非成员基线已恢复并有审计证据。

邮件告警、收件人、Langfuse 数据治理和入口网络/TLS Backlog 不得被误报为已完成；它们是否成为正式上线硬门槛由后续产品/部署决策单独确认。
