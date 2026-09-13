# 开发与运维手册 (Development & Operations Guide)

> 在维护者的 Windows/WSL 联调环境工作前，先读[本机开发环境](development-environment.md)。其中记录已迁移源码入口、固定运行副本、3001/8080/5173/8000 端口、ITSM 专用修复二进制和维护约束。下面的通用安装/初始化命令不用于直接重建现有验收实例。

本文档汇集 ITSM 项目的日常开发命令、Docker 部署配置、历史分支复盘教训与通用规范。

## 候选执行范围前置修复（开发中，不能据此启动候选）

039 的范围登记及生命周期改造不等于全部业务和队列已经接入范围约束。所有 G2 生产者、领取/恢复、请求异步和历史对账未完成前，候选 API/Worker 保持不启动；固定 CandidateSHA 仍由 A 的交接发布。共享源、B 的配置、R(038) 和数据库操作授权不因本文变化而改变。

新版本启动配置要求显式 `execution.mode`（standard/candidate）、`execution.deployment_id`、`execution.capabilities`；candidate 还要求 `execution.scopes` 中唯一的 tenant_id/scope_id。standard 能力值为 enabled/disabled，candidate 为 scoped/disabled；缺省能力禁用，未知名字/值和不支持的 scoped 能力报错。能力注册以[策略代码](../itsm-backend/common/executionscope/policy.go)为准。配置中的部署、角色和范围必须与数据库准备记录一致；不能用 HTTP header 或修改旧 WorkItem 建立范围。

039 只新增范围表、角色绑定及 tickets INSERT 触发器，不补历史成员。迁移清理新对象的默认 ACL；受限运行角色仅可读取三个范围对象，不能直接登记/改成员、改模式或执行触发函数。显式角色绑定、范围准备和完整权限核验必须在获准的隔离环境完成；缺失时拒绝启动/创建，禁止临时授予 owner/BYPASSRLS 修复。已有 038 回执保持原依赖关系，039 不要求执行 038。

构造不再部署默认 BPMN 模板/绑定或创建向量结构。它们由既有受控迁移及具名配置准备负责；不能为通过启动自动执行历史 RCA SQL、初始化或回填。配置 MinIO 时 bucket 必须已由环境所有者准备，检查失败停止启动，不自动建桶或回退本地 uploads。

迁移 `041_tool_invocation_execution_scope` 为新工具调用建立 `execution_tool_invocations` 结构归属，依赖040及既有候选准备链；不改历史SQL或补登记历史调用。只有 `tool_invocations` 首次INSERT的触发器可在相同事务登记候选scope，校验冻结部署对应的session_user绑定、active scope及tenant；standard绑定不登记。运行角色只能获得明确审阅后的SELECT，不能直接增删改登记；迁移剥离PUBLIC和角色默认授权。表/函数所有者及其继承角色不是候选业务身份。该迁移目前仅完成数据库前置验证：队列Enqueue和ProcessJob共用独立事务的来源/审批/当前调用者及审批者校验，历史无登记明确拒绝，来源SQL故障保留原错误；入队检查在队列锁外执行，30秒上限，关闭取消并等待检查退出，再禁止迟到入队。同一默认隔离事务不是一致快照或审批锁，也不是业务原事务许可。运行时新表权限审计、AI创建/审批、业务原事务与结果回写仍未贯通，不能单独应用后启动候选或将历史审批重新入队。真实源/候选迁移仍由B按完整清单准入。

工具队列及事件订阅需要显式运行阶段启动；取消后等待已启动任务退出，再关闭数据库和连接器。禁止从业务构造器调用 Start。禁用的必需能力必须报告未验证，不能把 pending、外部阻断或未运行的 Worker 标为成功。

候选 Stream 消费者及普通模式的完整信封消费者必须通过 `EventConsumerID()` 声明稳定逻辑所有者；登记时固定身份，同一 owner/topic 重复登记拒绝。完整信封订阅登记及动态Subscribe必须已注入来源校验器，缺失时在分配持久subscriber前拒绝。审计使用 `event_audit`，Webhook 使用 `webhook`，分别持有 `itsm:<owner>` 消费组；副本共享组但由持久订阅器生成不同消费者实例名。组只在显式 Start/Subscribe 时建立，首次从 `0` 读取对应 topic 内的离线消息，已有组保留进度；不得用重建组、SETID、删除历史或订阅旧裸 topic 恢复消费。Close 先取消订阅并等待订阅建立结束，再关闭每个拥有的 subscriber 并等待处理退出。持久订阅复用唯一事件总线，采用有界XREADGROUP与带游标XAUTOCLAIM交替领取；NACK保留原PEL并让出本次处理，只有业务成功ACK才执行XACK，确认失败仍待重领。显式Subscribe执行首次真实XAUTOCLAIM核验恢复命令和权限，首次领取结果交给正常处理，不丢弃；需要Redis 6.2或以上及对应命令权限，失败不放行启动。候选多租户订阅在部分建立后失败时停止整个事件运行实例，保留组及进度，不留下部分租户继续处理；恢复需重新构造并显式启动。

`redis.event_stream.claim_idle`、`claim_interval`、`nack_delay` 使用时长格式（如 `60s`、`5s`、`1s`）；负数拒绝，零或未配置分别使用应用的60秒、5秒及1秒默认值。配置由构造器复制；`claim_idle`和`claim_interval`决定持久拒绝消息的重领节奏，`nack_delay`现在用于Redis操作故障退避，不再控制单条消息的原地重投。持久消费者使用这些值；standard 的非完整信封订阅保留原 fanout 消费语义。领取超时不是排他锁：慢处理可能被其他实例重领，写入所有者必须重验授权并提供持久幂等回执。当前审计已具备该回执，语义是至少一次投递加审计幂等，不是传输恰好一次。Webhook 在普通与候选模式均接入下述消费事务；消费者重建及拒绝消息保全已验证，完整进程重启、人工处置和其它异步入口仍未完成，不能据此启动候选。

Webhook 事件订阅只接受 `WebhookEventTopics()` 注册的类型及完整持久信封，普通与候选模式共用下述意图事务和Worker。原同步外发分支已删除；配置目标或传入原始map均不能触发直接外发。Webhook声明稳定逻辑所有者`webhook`，其消费组按既有生命周期建立/关闭，错误或旧非持久消息拒绝并NACK。持久首次拒绝诊断保存在 `<physical-topic>:rejections:<owner>` Redis hash，字段为消息/载荷/事件类型摘要的组合指纹，值只包含固定原因、摘要与首次时间；HSETNX保证重试/消费者重建不改写首次事实，不复制原始UUID、载荷或错误文本。记录是历史观察，不是ACK许可或永久禁止重验；存储失败仍NACK。键无自动TTL，操作员可只读HGETALL并结合原PEL核查，不应将记录存在解释为业务已完成。持久订阅不再在同一NACK消息上循环；坏消息保留待确认，后续合法消息可处理，待确认恢复也不会因持续新消息而饥饿。底层损坏帧先校验类型并保存wire_invalid摘要事实，不能panic、修复为有效消息或ACK。人工处置及Redis服务重启持久性仍待完成，不能据此放行整个S5。

持久事件来源校验使用冻结部署身份：`ExecutionPolicy.EventRef` 在standard模式返回部署与租户、空scope，在candidate模式返回原准入ref；`CandidateRef`仍只适用于candidate。EventRef本身不赋予业务权限。共享authority在原事务核验持久Outbox主体、事件ID、载荷及发生时间，candidate另保留scope/角色绑定/成员门禁。普通模式发布ExecutionEvent时同样强制稳定事件类型/租户、持久来源校验与原eventID；声明ExecutionEnvelopeHandler的订阅者仅在严格信封、冻结部署/模式、transport ID及来源验证后收到完整Envelope。没有持久主体的既有事件不伪造WorkItem。普通Webhook已接入同一持久意图/Worker及稳定消费组；这不代表运行角色准入、全部重启场景或目标环境上线证明。

Webhook subscriber 在两模式均必须注入数据库 client 与冻结 ExecutionPolicy，拒绝原始 map；完整 typed Envelope 的持久来源在同一 RR 事务核验，candidate另核验active scope、角色绑定与成员，按当前明确目标建立 `webhook.event.delivery.requested` outbox 意图与唯一 `webhook_consume:<sourceEventID>` 审计回执。回执状态 `enqueued` / 202 仅表示意图已提交，不表示外部投递成功。重放再次校验来源，核对原回执及完整持久意图摘要，复用原目标集合；配置新增实例不扩大旧事件的投递范围。摘要使用保留数字精度的JSON对象键规范化，容纳JSONB重排但不忽略未知字段。目标只保存provider和URL摘要，不保存URL/凭据；该摘要不等于完整配置版本。原始来源保存在审计字符串中，Worker使用回执原文重验来源，并以完整意图摘要校对JSONB副本。

Webhook handler 在两模式均接入共享outbox Worker，按当前 publishing claim/token/租约/attempt marker、WorkItem、完整意图摘要、消费回执身份及意图成员核验；发送后重验并写 `webhook_deliver:<eventID>` 交付审计，再由原Worker标记published。`webhook`能力关闭时，注册器把该type保留为known reserved，outbox可运行但不领取这些意图。前置明确拒绝记blocked，基础设施错误保留原cause由已有Worker处理；发出请求后的错误、非2xx或回执不确定记delivery_unknown，不自动重发。

发送捕获精确实例对象及进程内generation，校验对象在Init冻结的URL摘要后使用同一对象发送，发送后核对当前generation。builtin同时冻结endpoint和签名secret，不从可变Config map重新取出站目标；禁止HTTP自动重定向，3xx不视为成功。URL摘要不证明完整配置版本，generation只识别当前进程内实例重绑，不是跨进程的持久版本或并发撤权栅栏。实例在发送后变化可能意味着原目标已接收，须保留unknown；不得改投新目标。

私有PG/Redis及loopback接收端已验证普通与候选共用持久所有者，以及消费提交后ACK缺口的消费者重建恢复；原同步路径已删除。这不证明整个应用或Redis服务重启、任意外发窗口恰好一次或目标环境准入，所有S5/G2入口尚未验收，不能据此启动候选。

`TestPersistentStreamRecoversAfterConsumerProcessKill` 使用同一测试二进制的两个独立消费者子进程：父进程核验任务私有Redis的PID，配置仅从stdin传入；首进程交付完整信封后等待、不ACK，父进程核验原PEL再强制终止，新进程以同组新消费者恢复原消息，核对完整信封、PEL归零和Stream原行不变。这是传输fixture的进程终止恢复，不是业务事务回执、完整API/Worker应用重启或Redis服务重启验收；不得作为G3替代证据。

离线与 ACK 间隙恢复测试分别为 `TestCandidateStreamDeliversOfflineMessages` 和 `TestCandidateIntakeCreationBoundary/stream_source_requires_current_persistent_authority/stream_consumer_recovers_committed_audit_before_ack`，后者同时需要下述私有 PostgreSQL socket 和 Redis 二进制变量。它验证真实审计提交后停止 ACK、关闭旧 bus、新 bus 领取原 pending 消息，原审计不变且旧 Stream 保全；不替代整个应用/操作系统重启或完整 outbox Worker 投递验证。

本机范围测试使用专属 Unix socket PostgreSQL，`CANDIDATE_SCOPE_TEST_SOCKET` 必须指向带 `candidate-test-instance` 标记（内容为 `itsm-candidate-isolated-test` 加换行）的私有测试实例；端口为25439。测试只创建/删除随机命名的自有数据库及角色，不读取普通业务 DSN。运行 `go test -tags candidate_scope ./tests/integration -run '^TestCandidateScopeRegistration$' -count=1`。未设置变量产生 skip，不是通过；本机 PostgreSQL16 证据不能代替目标 PostgreSQL17 复核。

完整构造保全测试另要求 `CANDIDATE_TEST_REDIS_BINARY` 和 `CANDIDATE_TEST_MINIO_BINARY` 为已核验二进制的绝对路径。运行 `go test -tags candidate_scope ./tests/integration -run '^TestCandidateConstructPreservesDatabaseAndStreams$' -count=1 -v`：测试启动独立 loopback Redis/MinIO，使用每轮随机凭据，写入前核对实例身份；子进程使用独立配置和清空的业务环境，只调用 NewApplication，不启动 API/Worker。对账覆盖合成历史 fixture 的各表内容、列/索引/约束/函数/触发器/权限/策略/sequence，以及 Redis 键/消息/消费组和附件桶/对象。该有限构造观察须与生命周期测试及审阅合用，不替代 S6 的真实执行、重启与完整历史保全，也不放行目标环境。

---

### 手动升级命令（候选修复分支，尚未完整放行）

`POST /api/v1/tickets/:id/escalate` 请求现在必须携带 `reason`、正整数 `version` 和稳定 `operationId`（最多200字符）。actor/tenant/source由认证边界构造；客户端重试复用原version和operationId，不生成新编号。返回 `{workItemId, version, status, replayed}` 操作回执，不再返回完整Ticket；刷新详情应走已有读取接口。不同输入复用同operationId或过期version返回409，缺权限/执行范围拒绝返回403。

当前仅generic手动命令使用此路径，专业类型由专业所有者处理。优先级最高critical保持不变，未知优先级拒绝；升级保留现有assignee，不猜用户ID。现行授权后允许历史已提交回执只读重放；首次写入必须通过原事务执行范围，并原子提交版本、审计和通知意图。

已配置Feishu时，手动命令在原事务新增 `feishu.task.update.requested`，冻结目标、映射、操作身份和发送快照，审计回执绑定其摘要。必须已有非空且TaskID=GUID的映射；缺失或不一致会回滚整条命令，不自动创建远端任务。既有Worker按目标顺序发送，投递前和结果写回核验持久claim/attempt、范围、当前权限和操作回执；完成事务锁住事件行。远端GUID不一致、调用后错误或回执失败均转入delivery_unknown并阻挡后序，不自动重试。该协议只覆盖新的手动更新事件；TicketLifecycleService/EscalationService重复手动方法已移除，生产HTTP手动升级只有TicketService所有者。BPMN升级已接入独立工作流事务（见下），其他Feishu直发入口仍待接入，候选尚未放行。

工单详情读取 `TicketService.GetTicket` 仅调用原仓储查询，不启动飞书同步、后台事务或更新映射。同步由明确的业务写意图承担；其它旧业务写同步和手动同步API仍待完成候选准入，不能因读取已纯化而宣称全部同步安全。

### BPMN 工单升级事务

`ticket_task/escalate` 由 TicketService 的独立工作流命令处理。持久回调必须携带正整数 `version`；保留 `escalate_to`（省略/空值默认high）、`escalation_reason`、`notify_admin_ids` 及 `escalated` 状态语义。旧缺version回调不会被自动补值，必须核对其流程配置与原操作意图。仅generic可用，专业类型走所属领域命令。

引擎通过内部context传递callback id/tenant/executionKey/lease owner/attempt；这些字段必须在业务原事务与当前processing/未过期租约匹配，context值本身不是授权。事务锁定回调、流程实例和工单，读取持久动作参数、权威实例目标及actor，核验当前权限和范围。首次执行以version CAS更新、写immutable审计回执和站内通知；重放仍核验actor/claim和输入摘要，但不重新检查已完成通知接收人的当前资格。不得通过variables自报actor、目标或租约替代这条边界。

业务提交与引擎推进仍是两个事务。推进失败后使用同一回调操作回执恢复，不再次升级或通知。Typed lifecycle result沿现有回执验证更新流程version/status；此路径不启用企业通知渠道。当前验证使用私有PG及构造的流程/回调前置数据，不替代真实流程启动、用户任务完成及企业环境验收。

### Outbox 按目标串行投递

需要顺序的handler实现 `OrderedOutboxDeliveryHandler.SerialByAggregate()`，注册表在构造时冻结声明，通用worker据此领取。`ClaimDueByEventType`现在显式接受排序布尔值；独立KAF dispatcher仍只领取自身类型并传false。事件payload不能声明或撤销排序。只有同tenant/event_type/aggregate_type/aggregate_id的所有较小ID前序均为published时，后序才可领取；历史NULL执行引用、blocked/dead_letter/未知状态及未来到期pending都保持阻挡，不能自动跳过。后序保持pending，操作员应先核对该目标的前序终态。

该能力要求生产者先取得同目标事务锁/CAS再INSERT并持有至提交；数据库序列本身不保证提交顺序。同一远端目标的所有更新必须共用相同类型与稳定aggregate键，handler仍需核验真实mapping/目的地/actor/执行范围。已有在途旧协议调用、其他类型或直接provider调用不自动获得顺序保证。手动Feishu update已声明有序投递；事件目标键绑定destination/GUID，同目标WorkItem原事务CAS在插入前持有至提交。TaskID=GUID前置条件利用现有tenant/taskID唯一约束，只约束参与新协议的映射，不证明所有旧GUID全局唯一。

## 1. 常用开发命令

### 前端 (itsm-frontend)

```bash
cd itsm-frontend
npm install              # 安装依赖
npm run dev              # 启动本地开发服务 (http://localhost:3010)
npm run build            # 生产环境构建
npm run lint             # Lint 检查并自动修复
npm run lint:check       # 仅 Lint 检查
npm run type-check       # TypeScript 类型检查
npm test                 # 运行全部测试
npm run test:unit        # 仅单元测试
npm run test:integration # 仅集成测试
npm run test:e2e         # 运行 Playwright E2E 测试
npm run theme:generate   # 从主题 token 源重新生成 CSS（修改 token 后执行）
npm run theme:check      # 检查已提交的生成 CSS 是否与 token 源一致
```

主题的权威源是 `itsm-frontend/src/design-system/theme-tokens.json`，展开逻辑位于 `itsm-frontend/src/design-system/expand-theme-tokens.mjs`，生成产物是 `itsm-frontend/src/styles/generated-theme-tokens.css`。不要直接编辑生成 CSS；修改权威源或展开逻辑后运行 `npm run theme:generate`，并把源文件与生成产物一同提交。`theme:check` 会检测漂移，并已接入前端类型检查前置步骤；开发与生产构建使用现有 npm pre-hook 自动重新生成。

### 后端 (itsm-backend)

```bash
cd itsm-backend
go run main.go           # 启动本地服务 (http://localhost:8090)
go build -o itsm-backend main.go # 编译二进制
./itsm-backend           # 运行二进制
go test ./...            # 运行全量单元与集成测试
# 已有 Ent schema 的部署：只运行已注册的 post-schema migrations。
go run -tags migrate ./cmd/migrate -up
# 仅 disposable development/test 数据库：完整且破坏性的 fresh bootstrap。
ITSM_ALLOW_DESTRUCTIVE_FRESH=true ITSM_FRESH_HOST="$DB_HOST" \
  ITSM_FRESH_PORT="$DB_PORT" ITSM_FRESH_DATABASE="$DB_NAME" \
  go run -tags migrate ./cmd/migrate -fresh
go run -tags create_user main.go
```

### BPMN instance authorization

Trusted BPMN scope is built only from authenticated `tenant_id`, `user_id`, role, and RBAC state. Elevated permissions are `process_instance:read`, `process_instance:update`, `task:read`, and `task:update`; request parameters never grant scope.

```bash
go test ./service/bpmn ./service ./controller -run 'BPMN|ProcessTask|ProcessInstance|KafDelegate' -count=1
```

### BPMN callback outbox

The callback outbox provides **at-least-once delivery**, not exactly-once delivery. Every delivery carries the stable outbox `execution_key` as `Idempotency-Key`; callback receivers must deduplicate that key and make the deduplication record and business effect atomic. A retry or lease recovery reuses the same key.

Rows move through `pending` (eligible at `next_attempt_at`), `processing` (owned by a worker lease), and `completed` (durably delivered). A processing lease lasts 60 seconds; after it expires, another worker can reclaim the row. The application starts an immediate sweep, then sweeps every 2 seconds in batches of 50. Failures return to pending with exponential backoff capped at 5 minutes.

Diagnostics must always include both the trusted tenant and exact execution key. Do not run unscoped outbox dumps on shared or production databases. Configure non-secret connection metadata in a PostgreSQL service entry (for example, `itsm-diagnostics` in `~/.pg_service.conf`) and keep the password in a mode-`0600` `.pgpass` or `PGPASSFILE`. Disable shell tracing before selecting those protected files. Invoke `psql` without a DSN argument so credentials never appear in the process argv. This example returns only operational metadata and deliberately excludes callback variables:

```bash
set +x
export PGSERVICE=itsm-diagnostics
export PGPASSFILE=/run/secrets/itsm-pgpass

psql --no-psqlrc -v tenant_id=42 -v execution_key='callback-key' -c "
SELECT execution_key, status, attempt_count, next_attempt_at,
       lease_owner, lease_expires_at, last_error_class, updated_at
FROM process_callback_outboxes
WHERE tenant_id = :'tenant_id'::bigint
  AND execution_key = :'execution_key';"
```

Logs may include `tenant_id`, `execution_key`, callback kind, attempt count, status, and an allowlisted error class. Never log callback variables or bodies, raw handler errors, credentials, tokens, DSNs, or other secrets.

Load the Go integration DSN once from a secret manager or a protected local environment file while shell tracing is disabled. The file example below must be mode `0600`; replace the command substitution with the local secret-manager command where applicable. Never echo the value or expand it in the `go test` argv. Then run the callback and authorization release gate from `itsm-backend`:

```bash
set +x
export ITSM_TEST_DB="$(< /run/secrets/itsm-test-db.dsn)"

go test -tags integration ./service \
  -run 'TestClaimTaskConcurrentCASPostgres|TestBPMNCallbackOutboxLeaseRecoveryPostgres' -count=1 -v

go test ./service/bpmn ./service ./controller -run 'BPMN|ProcessTask|ProcessInstance|KafDelegate|Callback|CounterSign' -count=1
go test -race ./service ./internal/bootstrap -run 'TestBPMNAuthorization|TestTaskMutation|TestProcessInstanceMutation|TestCounterSign|TestCallback|TestClaimTask' -count=1
go test ./middleware ./router ./internal/bootstrap -count=1
go test ./... -count=1
go build ./...
git diff c15af6eda1febd47a75fb1e621907b16bbaac336..HEAD --check
```

Provision `ITSM_TEST_DB` through the protected local secret environment before running the integration gate. Do not paste its password into documentation, shell history, CI output, process arguments, or test logs, and do not substitute SQLite or skip the integration tests when PostgreSQL is unavailable.

The KAF work remains on a separate branch. After rebasing that branch onto callback-outbox or authorization changes, rerun the authorization/KAF-focused tests and the complete gate above before merge; a previously green KAF result is not evidence for the rebased result.

### 环境配置

```bash
# itsm-backend/.env 配置示例
LOG_LEVEL=info
DB_PASSWORD=your_password
JWT_SECRET=your-jwt-secret
ADMIN_PASSWORD=admin123

# Azure AD OIDC（可选；启用时下列五项必须同时配置）
AZURE_TENANT_ID=
AZURE_CLIENT_ID=
AZURE_CLIENT_SECRET=
AZURE_REDIRECT_URI=http://localhost:8090/api/v1/auth/azure/callback
# Azure 用户唯一允许进入的 ITSM 租户；部署配置是授权边界
AZURE_ITSM_TENANT_CODE=
# 仅在明确允许 Azure 首次登录创建本地用户时启用
AZURE_ALLOW_USER_PROVISIONING=false

# 前端环境变量
NEXT_PUBLIC_API_URL=http://localhost:8090
```

Azure 登录只接受部署配置 `AZURE_ITSM_TENANT_CODE`，并将该值绑定进一次性
OAuth state 后在 callback 重新校验。浏览器不能选择或覆盖租户；配置缺失、
state 租户不一致、邮箱为空或租户内邮箱不唯一时均明确失败。系统不会回退到
固定租户，也不会按邮箱跨租户匹配。任一必需配置缺失时 Azure 路由不注册。

### 开发环境拓扑（多机协作）

当前 ITSM 项目在两台机器上并行开发，不是单机自包含环境：

- **本机（Mac）**：日常编码环境，运行前端 `npm run dev`、后端 `go run main.go` 等本地进程。
- **192.168.31.66**：承载共享的 Postgres / Redis 基础设施——本机 `itsm-backend/.env` 的 `DB_HOST`/`REDIS_HOST` 均指向这台机器，本地后端连的不是私有 Docker 实例，而是这台机器上的共享数据库。同一台机器上还运行着另一路代码检出/worktree（供并行 agent 任务使用）以及 GitHub Actions self-hosted CI runner。

由此带来的实际约束：

1. **DB/Redis 是共享基础设施，不是本机沙箱。** 本机执行的迁移、`-fresh` 重置、RLS 模式切换（`RLS_MODE=shadow/enforce`）、批量数据操作，影响的是所有连到 `192.168.31.66` 的开发者和 agent，不只是当前会话。执行破坏性操作前先确认没有其他人正在使用（参见 [CLAUDE.md](../CLAUDE.md) 中关于 `-fresh` migrate 的警告）。
2. **派发并行/后台任务前，先确认远端是否已有同名 worktree 在跑。** 已有过因未核对而重复排期的教训（KAF 委派链路），核对方法与纪律见 [architecture-assessment-remediation-execution-plan-design.md](superpowers/specs/2026-08-30-architecture-assessment-remediation-execution-plan-design.md) 的"五、执行纪律"一节。
3. 连接 `192.168.31.66` 所需的跳板、端口、鉴权等个人 SSH 配置不进入本文档；需要登录该机器做运维/调试时，向持有该配置的开发者确认。

---

### RLS execution boundary

`RLS_MODE=off` passes Ent operations through; `shadow` records missing tenant context without changing SQL. `enforce` applies the canonical `app.current_tenant` on the same physical connection as the query, and rejects missing/nonpositive tenant context, unknown modes, incompatible tenant variable names, and tenant operations using a superuser or `BYPASSRLS` role. RLS policies remain migration-owned; changing the mode does not install policies.

Use `common/tenantctx.WithTenantID` after authenticated tenant resolution. Selecting a tenant revokes any inherited system scope. Explicit Ent transactions retain their isolation options and one fixed tenant; each transaction retains a checked-out connection and clears all session settings on commit/rollback. Ent `WithVar`/`WithIntVar` are processed through the pinned public Ent API on a real SQL transaction before executing the statement. Direct reserved tenant/role settings are rejected; canonical tenant scope is reapplied after variable processing. Ordinary statements preserve SQL autocommit semantics, with a checked-out connection held until returned rows close. Always close raw query rows. Connection release uses an independent cleanup context and evicts the physical connection if cleanup fails, including after request cancellation.

System context labels do not grant database privileges or select a connection pool. API and KAF startup use `database.InitRuntimeDatabases`: `DB_USER`/`DB_PASSWORD_FILE` open the tenant pool, while **required** `DB_SYSTEM_ROLE_USER`/`DB_SYSTEM_ROLE_PASSWORD_FILE` open an independent restricted system pool. `DB_SCHEMA` optionally selects the application schema (default PostgreSQL search path). There is no fallback to migration credentials, the legacy optional admin-role settings, or the runtime role. The standalone `RunInitialization` process still opens only its migration credentials with ordinary Ent SQL, allowing Atlas background inspection.

The system role must be `LOGIN NOSUPERUSER BYPASSRLS NOCREATEDB NOCREATEROLE NOREPLICATION NOINHERIT`, with no role memberships or schema/table ownership. Provision it separately from the runtime role and migration owner, with its own password secret. The application never creates roles or grants at startup. The authoritative startup validation is in `database/runtime_clients.go`; it rejects missing configuration, unsafe role attributes/memberships, extra table/column/sequence privileges, schema/database CREATE, and callable application SECURITY DEFINER functions. The enforced runtime role must be non-superuser/non-bypass, non-owner, and have no role memberships. A default developer superuser is therefore diagnosed instead of silently disabling enforcement.

After initialization creates the schema, an authorized database administrator grants the system role USAGE on that schema and only these capabilities:

| Object | Privilege | Owning operation |
| --- | --- | --- |
| `users` | SELECT | Credential lookup, refresh actor validation, MSP session selection |
| `tenants` | SELECT | Tenant directory and active/expiry checks |
| `msp_allocations` | SELECT | Active MSP customer authorization |
| `external_identities` | SELECT | Verified provider/workspace/subject lookup after v2 assertion validation; mapping writes use tenant runtime transactions |
| `connector_configs` | SELECT | Restore persisted connector registrations at startup |
| `process_callback_outboxes` | SELECT | Discover callback recovery candidates across tenants; claims, execution, retries and acknowledgments use the candidate tenant on the tenant client |
| `outbox_events` | SELECT, UPDATE | Cross-tenant transport claim, attempts, leases, acknowledgment and retry; enqueue stays on the tenant client |
| `ticket_notifications` | SELECT, UPDATE | Existing delivery queue scan, lease and acknowledgment; creation and recipient/WorkItem checks stay on the tenant client |
| `audit_logs` | INSERT, SELECT(id) | Append authentication and transport failure/retry audit; SELECT(id) supports Ent RETURNING without reading audit content |
| audit log ID sequence | USAGE | Allocate audit IDs only |

For a dedicated schema, the grants have this form (the administrator supplies identifier variables `app_schema` and `system_role` to psql and configures the role password using their secret-management process):

```sql
GRANT USAGE ON SCHEMA :"app_schema" TO :"system_role";
GRANT SELECT ON :"app_schema".users, :"app_schema".tenants,
  :"app_schema".msp_allocations, :"app_schema".external_identities, :"app_schema".connector_configs TO :"system_role";
GRANT SELECT ON :"app_schema".process_callback_outboxes TO :"system_role";
GRANT SELECT, UPDATE ON :"app_schema".outbox_events, :"app_schema".ticket_notifications TO :"system_role";
GRANT INSERT, SELECT(id) ON :"app_schema".audit_logs TO :"system_role";
SELECT format('GRANT USAGE ON SEQUENCE %s TO %I',
  pg_get_serial_sequence(format('%I.audit_logs', :'app_schema'), 'id'), :'system_role') \gexec
```

Do not grant this role business-table access or broad `ALL TABLES`/`ALL SEQUENCES` privileges. Public or inherited privileges count during validation; an administrator must resolve an unsafe existing grant explicitly before startup. No application migration alters shared roles or PUBLIC grants.

Only authentication lookup, tenant middleware, connector restoration and queue repositories receive the system client. AuthService's user management and permission queries keep the tenant client. Connector HTTP persistence keeps the tenant client; restored connector/poller context is derived from its stored tenant. The shared outbox worker derives the business consumer context from the durable event and clears the transport marker; Incident consumers receive only the tenant client. The KAF dispatcher transports already-authorized messages. No request flag grants access to the system pool. The ticket notification worker receives this exact queue capability separately from its tenant business client; it clears inherited system context for each recipient delivery. An expired external delivery lease or ambiguous send becomes `failed` with `last_error_class=delivery_unknown`, retaining the existing delivery key and attempt count for reconciliation. Only confirmed pre-acceptance email failures retry, with Graph/SMTP fallback disabled. Existing durable rows use the same lease and status fields; no data migration or new queue is required. Notification content and recipient user ID are frozen; delivery resolves the current address of that same active user.

For local startup, supply the separate role variables above after authorized provisioning; do not change a shared `.env` or apply these grants to a shared instance without the environment owner's approval. Production Compose supplies `ITSM_MIGRATION_DB_USER` and its secret only to initialization; API and Worker receive `ITSM_RUNTIME_DB_USER` and `ITSM_SYSTEM_DB_USER` with their distinct secret files. `ITSM_DB_SCHEMA` selects the same schema for initialization and both runtime processes (default `public`); `RLS_MODE` is forwarded to API/Worker with its existing `off` default. Enabling enforcement remains an explicit rollout decision. These changes prepare the deployment contract; they do not apply grants to a deployed database.

The runtime regression command is `go test -tags integration_postgres ./tests/integration -run TestPostgresRLS -count=1 -v` with explicit `INTAKE_POSTGRES_TEST_DSN`. Its guarded disposable target is `127.0.0.1:36444/sslvpn_test`; it installs registered policies 009 then 024, uses unique schemas and temporary login/non-login roles, closes all pools and verifies role cleanup. It covers the actual runtime factory, login/refresh/MSP, connector restoration, queue/business separation, variable processing, cancellation and migration-role access. It does not certify every legacy application RLS path or activate the deferred entry/schema cutover.

## 2. Docker 部署与运维排查

### 生产环境启动（必须显式传入 env-file）

```bash
# 正确：显式传入 --env-file
docker-compose -f docker-compose.prod.yml --env-file .env.prod up -d

# 错误：缺少环境变量文件直接启动
docker-compose -f docker-compose.prod.yml up -d
```

KAF 委派投递由独立 `itsm-worker` 负责。生产环境需要提供
`KAF_WEBHOOK_URL`、`KAF_WEBHOOK_SECRET_FILE`、`ITSM_RUNTIME_DB_PASSWORD_FILE`、
`ITSM_SYSTEM_DB_PASSWORD_FILE` 和 `ITSM_MIGRATION_DB_PASSWORD_FILE`。API 与 Worker 挂载 runtime 与受限 system 数据库
secret，初始化任务只挂载 migration 数据库 secret；Worker 不发布宿主机端口，
标准启动至少为两个副本：

```bash
docker compose --env-file .env.prod -f docker-compose.prod.yml config
docker compose --env-file .env.prod -f docker-compose.prod.yml up -d --scale itsm-worker=2 itsm-worker
```

API 仅接收非秘密的 `KAF_WEBHOOK_URL`，目录发布使用它校验投递地址是否已配置且格式有效；此检查不代表 Worker 健康或其凭据已部署。Worker 启动仍单独强制签名 secret 和执行参数。不要在 API 容器中配置 KAF webhook secret，也不要把 Worker health port 发布到宿主机。
生产 Compose 不再创建 PostgreSQL 容器；必须配置外部实例的 `ITSM_DB_HOST`、
`ITSM_DB_NAME`、`ITSM_RUNTIME_DB_USER`、`ITSM_SYSTEM_DB_USER`、`ITSM_MIGRATION_DB_USER` 与 TLS 设置。
KAF 与 ITSM 使用同一实例时仍必须使用不同逻辑数据库和用户。

### 常用排查命令

```bash
# 检查容器状态
docker ps --format "table {{.Names}}\t{{.Status}}\t{{.Ports}}"

# 检查容器日志
docker logs <container> --tail 30

# 检查容器网络
docker inspect <container> --format '{{json .NetworkSettings.Networks}}' | jq -r 'keys[]'

# 从容器内测试后端健康检查 API
docker exec <container> wget -qO- http://localhost:8090/api/v1/health
```

### 网络隔离排查

生产容器与开发容器可能运行在不同网络：
- `itsm_itsm-network` - 开发网络
- `itsm_itsm-prod-network` - 生产网络
如遇 DNS 解析失败或容器间互通异常，先检查容器所在网络归属。

---

## 3. 复杂功能开发复盘教训（源自 dynamic-custom-fields 分支实践）

多任务拆解、逐任务评审的开发流程本身不会自动保证质量；以下几条是历史重构中实际踩过的坑，作为今后工作的检查项：

1. **"已完成"的历史结论必须重新验证，不能直接继承。**
   设计阶段得出的"这段代码零路由/零调用方"之类结论，到实际执行删除/重构前必须重新跑一遍验证命令，不能假设结论仍然成立——历史调查可能漏看了一条独立的调用链。发现结论与现状矛盾时，停下来重新调查、提给相关方确认范围，而不是凭旧结论盲目继续。
2. **任务级别的代码评审不能替代整分支的最终评审。**
   功能拆成多个子任务、每个任务分别评审通过，不代表功能作为整体是对的——跨任务的集成点（比如某任务的后端解析逻辑要跟另一任务的前端提交格式严格对齐）只有在看到完整分支 diff 的最终评审阶段才可能被发现。多任务功能必须有一次覆盖全分支 diff 的最终评审。
3. **修同一类缺陷时，主动搜索代码库里是否有其它地方犯过同样的错误。**
   修复某个结构性缺陷类别后，应该主动 grep 代码库里结构相似的其它调用点，而不是只改报告里点名的那一处。
4. **"清理型"修复本身要经过独立复核，不能拿到 implementer 自报的 DONE 就结束。**
   一次修复（比如清理孤儿数据）可能引入新的回归（如把软删除配成硬删除）。这类修复必须再过一轮独立于原实现者的复核，不能只信任自我报告。
5. **验证方法的"保真度"要讲清楚，不能用低保真度验证悄悄替代高保真度验证。**
   用 API 直接调用（如 curl）走查是合理的替代方案，但必须明确说明它覆盖不到哪些层（如前端 http-client 的请求体转换逻辑）。API 走查通过不代表真实浏览器路径没问题；报告里要老实写清楚哪部分测到、哪部分没测到。
6. **功能"看起来已完成"不等于真的可用，必须走一遍真实使用路径再下结论。**
   代码交付、单测/集成测通过，都不能替代一次端到端的真实操作验证；至少要用真实 HTTP 客户端路径走一遍主链路。
7. **复用旧代码时，要重新审视它当初依赖的假设是否还成立。**
   当一个此前半成品的功能第一次被真正打通、有真实数据流过时，依赖它的旧代码需要重新审视，不能假设其历史设计假设仍然合理。


### Intake identity exchange configuration

A6 routes use assertion v2 only. Set `INTAKE_IDENTITY_CONFIG_FILE` (or the existing `ITSM_` environment prefix) to an owner-only regular JSON file (mode 0600/0400). The file contains `providers` keyed by registered provider, each with a distinct `secret`, allowed `channels` and allowed `purposes` (`create`, `read`); optional `maxAge`, `futureSkew`, `tokenTTL` are whole-second durations. Defaults are 60s, 5s and 5m. Max age is 1s–5m, future skew 0–30s, token TTL 1s–15m. Secrets must differ from JWT/webhook/automation credentials and stay server-side. The application rejects reuse of its loaded JWT or KAF webhook secret. Missing configuration disables exchange; unavailable Redis rejects exchange instead of falling back to memory. Use the deployment's explicit Redis host/port/database.

Create/read exchange share one atomic nonce namespace. Lost exchange responses require a fresh nonce and assertion; retain the business submission key. Only the corresponding Intake routes accept the resulting token. Every request checks current mapping version/active state and current session/target-tenant permissions. Mapping management uses native access-token tenant routes with `intake_identity_mapping:read`/`write`, and PATCH requires `version` plus `active`; immutable provider/workspace/subject/user identity is replaced through a new mapping rather than changed in place. Manage mappings with exact external subjects; email matching is unsupported.

The requester WorkItem projection preserves professional status and returns `fulfillmentState: "unknown"` and `accessResult: null` until C1 installs its authoritative fulfillment/result projection. This is a C1 gate before A7/B1 acceptance, not evidence that access was granted. The shared test-only signature vector lives in [intake-identity-signature.json](contracts/fixtures/intake-identity-signature.json).

### Incident creation classification

`POST /api/v1/incidents` accepts the shared `cti` object (`categoryId`, optional `typeId` and `itemId`) for classification. IDs come from the authenticated tenant category tree; names are display metadata, not creation identifiers. Legacy `category`/`subcategory` creation fields are rejected by strict request binding. The backend validates active tenant-owned nodes and hierarchy before atomically creating the WorkItem and Incident. Workflow category/subcategory names are projected from the resolved records. Existing confirmed attempts retain their original payload; a changed classification requires a new confirmation. No schema migration or category seed is needed.
### WorkItem classification in create and edit forms

Ticket and Problem creation also accept shared `cti` IDs. New frontend callers obtain IDs from the authenticated tenant category tree instead of submitting category names or codes. Incident and Problem read responses expose the authoritative WorkItem `categoryId`. Their updates omit `categoryId` to preserve the existing classification, send `0` to clear it, or send a positive active tenant-owned ID to change it. Validation and WorkItem/professional-extension writes share a transaction. The shared frontend classification field reconstructs the selection by ID, excludes inactive branches, and resets edit state when the record changes. Ticket creation retains its existing public `categoryId` contract and rejects a conflict with the CTI leaf. No migration or category initialization is required.
### Problem root cause and RCA metadata

`problems.root_cause` is the authoritative root-cause body. RCA responses retain `rootCauseDescription` as a projection; analysis rows store method, evidence, confidence and review metadata only. RCA creation/update also advance the owning WorkItem version and timestamp in the same transaction. Metadata-only edits and deleting an RCA metadata record preserve the Problem root cause. Known Error publication defaults to this same Problem text.

Before deploying this contract, stop Problem/RCA writers, back up the affected Problem/RCA tables and their WorkItem/user references (or take a full backup with the designated backup role), and execute [`20260909_problem_rca_authority.sql`](../itsm-backend/migrations/20260909_problem_rca_authority.sql) as the schema owner with the explicit application `search_path` and `psql -v ON_ERROR_STOP=1`. The migration bootstraps missing RCA metadata, rejects conflicting nonempty bodies, duplicate analyses and invalid ownership, backfills an empty Problem root from the old RCA body, then removes the duplicate column. Review conflicts manually; do not choose a value automatically. Grant the configured runtime role ordinary CRUD rights on the new metadata table and usage/select on its sequence, retaining tenant RLS. Do not run global auto-migration or seeding. Deploy the matching API binary with the tenant-scoped investigation service. Rolling back an existing-table migration requires restoring the backup and previous API together.

Creation requester controls use the actual target resource's `create_on_behalf` permission plus directory read permission. Reading users, an admin-like role name or an MSP role alone does not authorize delegation. Same-tenant users without delegation submit for themselves; a cross-tenant actor must have an authorized customer requester. A rejected confirmed attempt remains immutable: change the form and explicitly confirm a new attempt when correcting its requester.
