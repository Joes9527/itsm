# 开发与运维手册 (Development & Operations Guide)

> **Coding Agent 当前结构目标：** [047_bpmn_assignment_source](development-environment.md#selected-schema-target-047)。Dev 与迁移验证均面向该目标；实际升级状态见[唯一清单](superpowers/plans/2026-09-15-migration-validation-ledger.md#schema-target-status)。下文较早迁移说明不表示当前只需到031、036或046。

> **数据库操作入口（2026-09-14）：**先读[当前数据库状态与 Agent 边界](development-environment.md#agent-必读当前数据库状态2026-09-14)和[全量数据库清单](review/2026-09-14-postgresql-database-register.md)。任务二的唯一 ITSM 目标为 `ga-itsm-20260914 / itsm_ga_ready / public`；G-A 版本、保留源、备份范围及未决项以该状态页和固定交接为准。下列通用命令不是现有共享库的执行授权。
> **WSL 联调环境入口：** 先读[本机开发环境](development-environment.md)。该环境维护的 ITSM 前端固定使用 3010；运行状态、进程身份、构建版本和启动配置以运行栈记录为准。GA 文件名、目录名或端口占用都不能单独证明环境身份。查看或操作已登记进程时使用 `python3 scripts/wsl-stack.py status|start|stop [service]`，不要用通用脚本直接重建现有验收实例。3000 由 WSL Langfuse 使用，3001 不再作为 ITSM 前端入口。

> **开发与迁移验证：** 遵循[双数据库用途与版本约定](development-environment.md#development-and-migration-validation-database-contract)：同一新版代码，分别使用 Dev 和克隆／隔离验证库，两库结构均须匹配所选代码。Dev 落后时规划规范升级，禁止靠恢复旧代码切库；后续状态只维护在[唯一续办清单](superpowers/plans/2026-09-15-migration-validation-ledger.md#development-restoration-update)。

本文档维护 ITSM 项目的开发命令、部署运维和验证流程。API/DTO、前端和源文件命名的公共规则见[共享工程约定](engineering-conventions.md)，对所有开发者及 Coding Agent 同样适用；架构和领域约束见 [AGENTS.md](../AGENTS.md)。

## 候选执行范围前置修复（开发中，不能据此启动候选）

039 的范围登记及生命周期改造不等于全部业务和队列已经接入范围约束。所有 G2 生产者、领取/恢复、请求异步和历史对账未完成前，候选 API/Worker 保持不启动；固定 CandidateSHA 仍由 A 的交接发布。共享源、B 的配置、R(038) 和数据库操作授权不因本文变化而改变。

新版本启动配置要求显式 `execution.mode`（standard/candidate）、`execution.deployment_id`、`execution.capabilities`；candidate 还要求 `execution.scopes` 中唯一的 tenant_id/scope_id。standard 能力值为 enabled/disabled，candidate 为 scoped/disabled；缺省能力禁用，未知名字/值和不支持的 scoped 能力报错。能力注册以[策略代码](../itsm-backend/common/executionscope/policy.go)为准。配置中的部署、角色和范围必须与数据库准备记录一致；不能用 HTTP header 或修改旧 WorkItem 建立范围。

039 只新增范围表、角色绑定及 tickets INSERT 触发器，不补历史成员。迁移清理新对象的默认 ACL；受限运行角色仅可读取三个范围对象，不能直接登记/改成员、改模式或执行触发函数。显式角色绑定、范围准备和完整权限核验必须在获准的隔离环境完成；缺失时拒绝启动/创建，禁止临时授予 owner/BYPASSRLS 修复。已有 038 回执保持原依赖关系，039 不要求执行 038。

构造不再部署默认 BPMN 模板/绑定或创建向量结构。它们由既有受控迁移及具名配置准备负责；不能为通过启动自动执行历史 RCA SQL、初始化或回填。配置 MinIO 时 bucket 必须已由环境所有者准备，检查失败停止启动，不自动建桶或回退本地 uploads。

迁移 `041_tool_invocation_execution_scope` 为新工具调用建立 `execution_tool_invocations` 结构归属，依赖040及既有候选准备链；不改历史SQL或补登记历史调用。只有 `tool_invocations` 首次INSERT的触发器可在相同事务登记候选scope，校验冻结部署对应的session_user绑定、active scope及tenant；standard绑定不登记。运行角色只能获得明确审阅后的SELECT，不能直接增删改登记；迁移剥离PUBLIC和角色默认授权。表/函数所有者及其继承角色不是候选业务身份。该迁移目前仅完成数据库前置验证：队列Enqueue和ProcessJob共用独立事务的来源/审批/当前调用者及审批者校验，历史无登记明确拒绝，来源SQL故障保留原错误；入队检查在队列锁外执行，30秒上限，关闭取消并等待检查退出，再禁止迟到入队。入队检查不是业务原事务许可；实际业务事务必须重新核验。AI仓库CreateToolInvocation已在自有原事务BindEnt→INSERT→Commit，触发器同事务登记；待审批与auto记录共用该入口，失败回滚。该方法自行拥有事务，不用于嵌套在调用方事务中。审批经唯一DecideToolInvocation原事务核验来源和当前ai:write权限，只从pending条件更新决定/原因/actor/时间；相同actor/决定/原因的已完成审批可重放并保留首次字段，不同决定冲突。候选审批采用RepeatableRead；并发后到请求可能保留40001失败，由调用方整次重试并重新验证，相同决定重放保留首次字段，不保证同时成功。提交后才enqueue，失败返回503并明确审批已记录且执行待入队；403/404/409/500分别表示权限、缺失、冲突和内部失败，接口不返回原数据库错误。ExecuteTool在执行前检查参数序列化；未知工具、权限拒绝及只读执行成功/失败均等待审计写入，审计失败保留cause并返回ErrToolAuditUnavailable，HTTP固定503且不暴露数据库错误，失败时不返回工具结果。查询与审计不是同一事务，也未提供调用重试去重；现有可选/缓存RBAC尚不代表完整候选权限验收。业务运行身份准入已要求041登记表SELECT并拒绝表/列写权限及登记函数EXECUTE，缺对象或授权不满足时启动失败；运输身份白名单保持原范围，不授予工具登记读取权限。候选启动前仍需B在目标环境按迁移链安装041并显式授予业务身份SELECT，本机fixture不证明目标准入。ToolQueue完成/失败写回已使用自有事务重复核验来源、审批和当前身份，并以调用身份/参数/审批字段及非done状态条件更新；完成后的重放保留首次结果，失败文本固定。业务提交与结果提交分离，结果失败后依赖原业务幂等恢复；迁移042新增窄lock_candidate_tool_authority函数，候选来源复核在同一事务按binding→scope→登记持有SHARE锁至提交/回滚，阻止scope关闭越过该事务；函数固定search_path/显式tenant和session_user约束，剥离PUBLIC及默认EXECUTE，运行身份仍只读。042为历史迁移；当前部署还必须完成043替换和新函数的显式EXECUTE，运输身份不授权。真实PG验证scope撤权等待写回提交/回滚、随后调用拒绝；真实PG进一步验证binding部署标识修改、mode改standard及删除三类撤权，分别等待结果commit/rollback；pg_blocking_pids精确匹配实际结果事务PID，撤权提交后下一调用拒绝且不改回执。此证据属于候选模式，不证明standard保护或其他业务入口。并发成功/失败与审批参数变化竞争仍待验证。工具创建路径已在intake原事务绑定scope后、领取幂等记录前复用当前工具审批核验：要求ai_tool/tool_queue来源元组、真实调用ID、actor/requester、批准参数重建命令摘要及显式相同IdempotencyKey，候选043授权锁保持到业务事务结束。摘要本身不含IdempotencyKey，不能单靠摘要约束操作身份。历史调用、未批准及被篡改命令在写入前拒绝，角色查询SQL错误保留基础设施语义。工具编辑路径在编辑回执Replay前复用同一来源/审批核验，要求ai_tool与tool:update_ticket操作命名空间一致，从持久参数重建完整编辑命令并核对目标、版本、字段及Meta摘要；候选043授权锁保持到编辑事务结束。真实PG验证不存在调用/变造内容/未批准拒绝，合法队列编辑及业务回执重放保全，审批失效后重放拒绝，实际UPDATE后故障回滚与重试。审批/身份并发变化及剩余结果竞争验证仍未完成，不能单独应用后启动候选或将历史审批重新入队。真实源/候选迁移仍由B按完整清单准入。

迁移 `043_tool_execution_authorization_lock` 保留042历史校验和，安装五参 `lock_candidate_tool_authorization(uuid,text,bigint,bigint,bigint)` 并删除旧四参函数。新函数依次锁binding、scope、来源登记、原调用，再按ID锁原actor/approver及同租户额外subject用户、Role、RolePermission、Permission，直到调用事务提交或回滚；额外subject仅由创建入口传requester、审批入口传当前决策者，函数不授予代办或审批权限。原调用使用FOR UPDATE以串行化审批/参数/结果变化，其余授权记录FOR SHARE；运行身份不新增IAM配置写权限。权限匹配继续由既有Go授权代码执行，函数锁成功不等于业务授权。PUBLIC及默认角色EXECUTE被剥离，候选准入检查新函数；041/042/043必须按清单顺序应用，旧应用与新迁移不可混跑。ToolQueue前检/结果事务及审批事务使用RepeatableRead；创建/编辑沿用原有RepeatableRead，创建目录导入业务快照。并发快照失效保留底层40001，创建入口已有完整事务重试，审批/队列不吞错继续执行。仅candidate路径获得这些锁，standard不据此声明保护。真实私有PG覆盖三个不同身份及角色、权限关系、权限内容、审批状态/参数撤回等待创建commit/rollback，撤回提交后创建与回执重放拒绝。完整结果胜负竞争、目标PG17与候选真实运行验收仍需后续完成。

云发现执行入口使用冻结的ExecutionPolicy能力开关：CloudDiscoveryService.DiscoverAll、DiscoverAccount及底层cloud.Runner.RunAll在账号查询或provider调用前要求cloud_discovery启用。candidate配置只接受该能力disabled，不能借直接服务调用绕过；standard也必须显式enabled。构造器必须传入策略，缺策略、未知/缺失能力、无效租户、冲突上下文、SystemBypass或取消上下文均失败关闭。能力表从启动配置复制，后续修改配置对象不会临时授权。该检查只限制部署执行能力，不替代领域权限、账号租户归属或外部出站控制；标准模式的区域发现/持久化可靠性不因本检查获得验收。

连接器 GET 列表、配置、生命周期和健康端点均只读取本租户最近一次实际诊断快照，不触发外呼；新实例或替换实例尚未探测时保持未知。主动诊断使用POST /api/v1/connectors/health，要求connector:write，并由Manager核验冻结的connector_diagnostics能力与请求租户/取消上下文。candidate该能力只允许disabled；standard需显式enabled，未配置或缺策略均拒绝。诊断只遍历本租户实例、按请求上下文设置单次5秒上限；取消或序列化失败返回错误，真实健康失败可保留为OK=false结果。缓存绑定实例generation，替换/撤销后旧结果不能写入新实例，返回快照为独立副本；该缓存不持久化，重启后未知。Manager构造必须显式传CapabilityGate，生产使用ExecutionPolicy，不能默认standard。此诊断门禁不覆盖Provision/Send及裸Connector获取后的直接执行；LoadAll另由下述恢复门禁控制。受控目标激活仍为候选交付前置，不因GET副作用修复而放行G2。




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
### 前端生产模式与工作流入口维护

日常验收使用生产构建，避免 `next dev` 首次访问页面时按需编译。先在独立目录构建并验证，再停止已核对身份的前端进程并切换发布文件；不要在正在提供服务的 `.next` 目录执行构建。

```bash
# 在 itsm-frontend 中执行；API 代理目标必须在构建时提供。
npm ci
ITSM_BACKEND_URL=http://127.0.0.1:8080 NEXT_PUBLIC_API_URL='' \
  NEXT_PUBLIC_WS_URL=ws://192.168.31.66:3010/api/v1/ws/notifications npm run build
NODE_ENV=production HOSTNAME=127.0.0.1 PORT=3301 npm start
```

`npm run build` 会准备 `.next/standalone`，包含 `server.js`、依赖、静态资源和 `public`。发布可复制该完整目录并执行 `NODE_ENV=production HOSTNAME=0.0.0.0 PORT=3010 node server.js`；不要只复制 `server.js`。保留启动描述和上一发布目录，切换后验证登录、同源 `/api/v1/health`、静态资源及已登录业务页面。本机固定路径与启动描述见[本机开发环境](development-environment.md)。

#### Cookie transport for private HTTP environments

Session, refresh, logout, OAuth state and CSRF cookies share the backend transport
policy. `server.cookie_secure` is optional: omission keeps cookies Secure when
`ENV=production`, `GIN_MODE=release`, or `server.mode=release`. Explicit `true`
requires Secure even on HTTP; explicit `false` permits a deliberately configured
HTTP deployment without changing production authorization/runtime mode.
`ITSM_COOKIE_SECURE=true|false` overrides YAML; empty or invalid values fail startup.
Do not put an empty placeholder or a default `false` in a general-purpose recipe.

Direct TLS and `X-Forwarded-Proto: https` always force Secure, including with an
explicit `false`. The forwarding header only strengthens this policy; a claimed
`http` cannot weaken the default or an explicit `true`. This is not proxy-based
authorization: the current Gin client-IP trust list remains `127.0.0.1`. The
reverse proxy must replace inbound forwarding headers with the transport it
observes. TLS termination elsewhere requires forwarding HTTPS correctly.

For an authorized LAN HTTP deployment such as WSL `http://192.168.31.66:3010`,
set only `ITSM_COOKIE_SECURE=false` in the backend's private launch environment
and keep existing `ENV`, server mode, CSRF and authorization settings. Deploy the
reviewed backend change and update its recipe/artifact evidence together before
expecting this setting to work. For HTTPS, remove the override or set it to `true`.
Verification must use the LAN browser: login retains both HttpOnly cookies,
authenticated reads succeed, refresh renews the session, CSRF-protected writes
retain their token check, and logout clears the session. Unit tests do not prove
that target deployment or browser acceptance has occurred.

WSL 通知连接同样走 3010 → 8080。`NEXT_PUBLIC_WS_URL` 必须在构建时指定为实际浏览器入口，不能只在运行时设置，否则旧默认可能连接 localhost:8090。上例是当前 WSL LAN HTTP 地址；其它环境按真实入口使用 ws/wss。后端 `WEBSOCKET_ALLOWED_ORIGINS` 明确列出该浏览器来源（当前为 `http://192.168.31.66:3010`），不使用通配。发布后分别确认通知列表 HTTP 200 与浏览器 WebSocket 101，避免只验证普通 API。

工作流分组使用 `/workflow`，该页面跳转 `/admin/workflows`。三个默认子入口为工作流管理、流程设计器和流程实例。审批链规则使用已有页面 `/admin/approval-chains`，旧 `/workflow/approval-chains` 跳转到该页面；`workflow` 菜单修复会同步迁移旧菜单地址，保留已有分组、权限和可见性配置。动态菜单仍由后端按租户、角色和权限过滤。升级已有租户的旧菜单时，使用定向命令，而非全量初始化：

```bash
# 在 itsm-backend 中构建，再使用目标环境既有配置运行该二进制。
go build -o /tmp/itsm-reconcile-menus ./cmd/reconcile_menus
/tmp/itsm-reconcile-menus -scope workflow -tenant-id 1 -requested-by '<operator identity>'
# 服务目录管理入口与工单分类菜单权限修复：
/tmp/itsm-reconcile-menus -scope catalog -tenant-id 1 -requested-by '<operator identity>'
# 审批待办入口（修复旧 /approvals/pending）：
/tmp/itsm-reconcile-menus -scope approvals -tenant-id 1 -requested-by '<operator identity>'
```

执行前核对目标数据库、schema、租户并协调共享环境写入。命令在单一事务中修复该租户的菜单，合并旧 `/workflow`、`/workflow/list` 重复入口，保留自定义子项与既有可见/启用状态；不改角色授权、不执行 schema 迁移。审计动作 `reconcile_workflow_menus` 保存操作者和菜单前后快照，可用于核对及受控恢复。首次初始化使用同一菜单修复逻辑。

`-scope` 必须显式选择 `workflow`、`catalog` 或 `approvals`，未知值返回错误，不执行写入。`catalog` 仅维护“服务目录管理”和“工单分类”两个入口，权限分别为 `service_catalog:read`、`ticket_category:read`；保留已有菜单可见性、启用状态、父子关系和顺序，审计动作是 `reconcile_catalog_menus`。

`approvals` 将“我的待办”统一到主导航 `/approvals`（BPMN 任务收件箱），修正旧 `/approvals/pending` 并合并重复记录，保留已有可见性与启用状态。菜单权限是 `task:read`，不是流程定义管理权限；审计动作是 `reconcile_approvals_menus`。

产品用词：主导航“服务目录”用于浏览与申请；管理导航“服务目录管理”用于维护目录项、申请字段、流程和服务级别；“目录分类”是目录项的展示分组；“工单分类”是已产生工作的业务分类树。主导航“我的工单”（路由 `/my-requests`）是当前用户工单的统一入口，覆盖全部 `recordClass`，行级可见范围由后端决定、页内只切“我提交的/我处理的/全部”；只看服务请求的视角在 `/service-requests`，“我的待办”仍是 `/approvals`。当前 `ServiceCatalog.category` 是字符串，`Ticket.category_id` 关联独立分类树，二者没有自动映射。自定义字段归属于目录项或工单模板，不从分类继承。

CTI 三级约束、目录默认分类与专业完成质量的设计见[已接受的设计](superpowers/specs/2026-09-17-cti-governance-design.md)；
**代码已在 CTI 分支交付（PR #48），但迁移 `048_cti_governance` 未在任何共享/生产库应用、完成质量门禁未在任何租户启用** ——
上面的描述仍是**当前部署**的实现边界，不得把分支交付当成已部署能力。启用步骤见
[CTI 治理受控启用清单](operations/cti-governance-rollout-checklist.md)。

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

`go run main.go` **不是**受管实例的启动方式。两者读不同的 `config.yaml`，因而监听不同端口——不要把其中一个的结果当作另一个的证据：

| 进程 | 读取的配置 | 端口 |
| --- | --- | --- |
| 你自己起的 `go run main.go` / `./itsm-backend` | 仓库内 `itsm-backend/config.yaml`（`server.port: 8090`） | 8090 |
| 受管的 WSL 实例（由 `stack` 启动） | 启动 recipe 的 cwd 下的 `config.yaml`（当前 `server.port: 8080`） | 8080 |

3010 前端的同源 `/api/*` 转发指向 **8080**（构建时的 `ITSM_BACKEND_URL`），所以只起一个 8090 的本地进程，3010 不会连上它。受管实例的身份、端口与恢复边界见[本地环境](development-environment.md)。

WSL 嵌套 worktree 的发布构建应显式绑定 Git 来源：Go 的自动 VCS 探测可能取到父仓库，导致嵌入的提交与当前 worktree 不一致。先提交并验证工作树干净，在 `itsm-backend` 目录执行：

```bash
task_git_dir="$(git rev-parse --absolute-git-dir)"
task_source_root="$(git rev-parse --show-toplevel)"
GIT_DIR="$task_git_dir" GIT_WORK_TREE="$task_source_root" go build -buildvcs=true -o /tmp/itsm-api-candidate .
go version -m /tmp/itsm-api-candidate
```

发布前核对 `vcs.revision` 等于受审提交且 `vcs.modified=false`，再记录二进制 SHA256、私有启动描述和实际运行 PID。不能仅凭二进制文件名或启动描述中的版本标签认定构建来源；旧制品内嵌父仓库信息也不能单独证明实际编译的是父仓库代码。前端另行记录源码提交、Build ID 和发布目录。

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

1. **DB/Redis 是共享基础设施，不是本机沙箱。** 本机执行的迁移、`-fresh` 重置、RLS 模式切换（`RLS_MODE=shadow/enforce`）、批量数据操作，影响的是所有连到 `192.168.31.66` 的开发者和 agent，不只是当前会话。执行破坏性操作前先确认没有其他人正在使用；`-fresh` 仅允许用于可丢弃的开发/测试库，禁止用于共享或生产数据库。
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

Requester reference reads use `GET /api/v1/intake/work-item-references` under `intake:workitem:read`. Supply an exact `number` for one readable reference, or omit it for the current requester's unfinished page; pass only the returned `cursor` to continue. Numbers and cursors cannot be combined. `INTAKE_REFERENCE_PAGE_SIZE` (also supported with the `ITSM_` prefix) must be a positive integer and defaults to 50. Links use the configured `Server.FrontendURL` HTTP(S) origin. Unknown lifecycle owners or states fail closed.

A6 routes use assertion v2 only. Set `INTAKE_IDENTITY_CONFIG_FILE` (or the existing `ITSM_` environment prefix) to an owner-only regular JSON file (mode 0600/0400). The file contains `providers` keyed by registered provider, each with a distinct `secret`, allowed `channels` and allowed `purposes` (`create`, `read`); optional `maxAge`, `futureSkew`, `tokenTTL` are whole-second durations. Defaults are 60s, 5s and 5m. Max age is 1s–5m, future skew 0–30s, token TTL 1s–15m. Secrets must differ from JWT/webhook/automation credentials and stay server-side. The application rejects reuse of its loaded JWT or KAF webhook secret. Missing configuration disables exchange; unavailable Redis rejects exchange instead of falling back to memory. Use the deployment's explicit Redis host/port/database.

Create/read exchange share one atomic nonce namespace. Lost exchange responses require a fresh nonce and assertion; retain the business submission key. Only the corresponding Intake routes accept the resulting token. Every request checks current mapping version/active state and current session/target-tenant permissions. Mapping management uses native access-token tenant routes with `intake_identity_mapping:read`/`write`, and PATCH requires `version` plus `active`; immutable provider/workspace/subject/user identity is replaced through a new mapping rather than changed in place. Manage mappings with exact external subjects; email matching is unsupported.

The requester WorkItem projection preserves professional status and returns `fulfillmentState: "unknown"` and `accessResult: null` until C1 installs its authoritative fulfillment/result projection. This is a C1 gate before A7/B1 acceptance, not evidence that access was granted. The shared test-only signature vector lives in [intake-identity-signature.json](contracts/fixtures/intake-identity-signature.json).

菜单历史地址：`/workflow/automation` 跳转到 `/admin/tickets/automation-rules`，`workflow` 定向菜单修复同步迁移该地址并使用后端读取权限 `automation_rule:read`。已有分组和显示开关保留，不授予角色权限。知识库旧 `/knowledge/articles` 和 `/knowledge/articles/create` 分别跳转到 `/knowledge` 和 `/knowledge/articles/new`；静态菜单使用实际目标地址。


### Incident creation classification

`POST /api/v1/incidents` accepts the shared `cti` object (`categoryId`, optional `typeId` and `itemId`) for classification. IDs come from the authenticated tenant category tree; names are display metadata, not creation identifiers. Legacy `category`/`subcategory` creation fields are rejected by strict request binding. The backend validates active tenant-owned nodes and hierarchy before atomically creating the WorkItem and Incident. Workflow category/subcategory names are projected from the resolved records. Existing confirmed attempts retain their original payload; a changed classification requires a new confirmation. No schema migration or category seed is needed.
### WorkItem classification in create and edit forms

Ticket and Problem creation also accept shared `cti` IDs. New frontend callers obtain IDs from the authenticated tenant category tree instead of submitting category names or codes. Incident and Problem read responses expose the authoritative WorkItem `categoryId`. Their updates omit `categoryId` to preserve the existing classification, send `0` to clear it, or send a positive active tenant-owned ID to change it. Validation and WorkItem/professional-extension writes share a transaction. The shared frontend classification field reconstructs the selection by ID, excludes inactive branches, and resets edit state when the record changes. Ticket creation retains its existing public `categoryId` contract and rejects a conflict with the CTI leaf. No migration or category initialization is required.
### Problem root cause and RCA metadata

`problems.root_cause` is the authoritative root-cause body. RCA responses retain `rootCauseDescription` as a projection; analysis rows store method, evidence, confidence and review metadata only. RCA creation/update also advance the owning WorkItem version and timestamp in the same transaction. Metadata-only edits and deleting an RCA metadata record preserve the Problem root cause. Known Error publication defaults to this same Problem text.

Before deploying this contract, stop Problem/RCA writers, back up the affected Problem/RCA tables and their WorkItem/user references (or take a full backup with the designated backup role), and execute [`20260909_problem_rca_authority.sql`](../itsm-backend/migrations/20260909_problem_rca_authority.sql) as the schema owner with the explicit application `search_path` and `psql -v ON_ERROR_STOP=1`. The migration bootstraps missing RCA metadata, rejects conflicting nonempty bodies, duplicate analyses and invalid ownership, backfills an empty Problem root from the old RCA body, then removes the duplicate column. Review conflicts manually; do not choose a value automatically. Grant the configured runtime role ordinary CRUD rights on the new metadata table and usage/select on its sequence, retaining tenant RLS. Do not run global auto-migration or seeding. Deploy the matching API binary with the tenant-scoped investigation service. Rolling back an existing-table migration requires restoring the backup and previous API together.

Creation requester controls use the actual target resource's `create_on_behalf` permission plus directory read permission. Reading users, an admin-like role name or an MSP role alone does not authorize delegation. Same-tenant users without delegation submit for themselves; a cross-tenant actor must have an authorized customer requester. A rejected confirmed attempt remains immutable: change the form and explicitly confirm a new attempt when correcting its requester.


历史连接器恢复 `ConnectorController.LoadAll` 在读取恢复客户端或解析配置前，由同一Manager要求connector_poll启动能力。仅standard、冻结配置显式enabled且带内部SystemContext标记的未取消上下文可以进入；candidate即使带系统标记也拒绝，普通HTTP/tenant context不能代替启动许可。该入口使用既有只读system客户端读取配置，逐个派生tenant context再初始化实例/启动原轮询。SystemBypass只是内部代码标记，不是数据库角色认证；仍须先完成真实运行角色准入。此规则只管历史恢复，不授权新的candidate投递目标，也不解决直接Provision/Send/Get调用的其它前置。

候选连接器目标声明配置位于`execution.connector_targets`：每项包括`tenant_id`、`scope_id`、`name`、`provider`、规范64位小写十六进制`destination_digest`、`capabilities`及受保护的`credentials`/`settings`。目标必须属于已有scope、实例键唯一；能力仅限已显式scoped的notification/webhook/outbox投递owner。outbox不代表全部handler都可使用该目标，业务owner仍须验证具体持久意图和权限。配置构造时冻结完整嵌套内容，仅内部启动上下文获得独立副本；错误不输出秘密内容。settings使用JSON值，拒绝校验时可见的有损数值往返；不能恢复加载器此前已经丢失的原文精度。Manager已接入下述可信启动流程；普通配置入口与直接投递旁路仍待关闭，填写声明或通过组件测试不意味着候选已获准启动。


候选API在消费者前调用唯一Manager.ActivateStartupTargets，只消费冻结声明，先整批核验注册manifest的initialization_behavior=local_only，再复用普通Provision的内部初始化函数。Init之后按通用DeliveryDestinationIdentity核对真实目标摘要，全部成功才统一发布实例和generation；Webhook生产者和worker沿用同一身份接口，旧专用接口已移除，摘要含义未变。初始化行为纳入manifest checksum；未知行为拒绝，声明字符串不能替代实现的无外呼/无后台任务证明。目前只有经过检查的builtin Webhook声明local_only，其Init不发网络请求。

初始化失败时关闭当前及已准备对象，并保留清理错误；同一Manager只尝试一次可信启动，失败需新建实例。CloseAll先禁止新增初始化，再等待在途初始化和清理，防止关闭后发布；关闭本身不主动取消Init，生命周期调用方必须先取消上下文，Init须遵守该上下文。普通Provision与可信启动共用此关闭等待。后续消费者启动失败和正常停止由API生命周期清理已激活目标，工具队列Start失败也先Close再关闭目标依赖。可信启动后普通Provision拒绝变更，但裸Send/Get权限仍需单独接入，不把目标存在视为业务投递许可。


候选环境的 Marketplace 安装、历史安装重新启用、卸载、配置替换及连接器配置合并均在首次查询/写入前要求同一 `RequireIntegrationManagement`。当前只有 standard、有效且匹配的显式租户上下文、无 SystemBypass 且请求未取消才允许继续；缺少策略一律拒绝。此部署限制不替代现有 RBAC 或业务授权，也不因商品类型为 skill/plugin 放开配置写入。HTTP handler 传递 Request.Context 保留租户与取消信息，禁止操作返回固定403；连接器依赖缺失不能报告激活成功，standard 已提交配置与后续激活并非原子事务。

飞书 OAuth callback 在兑换令牌前通过同一部署检查，租户仅来自唯一匹配的回调实例；重复实例 ID 拒绝，query/state 不提供租户授权。配置合并 owner 自身仍再次检查，以覆盖直接调用。nil Marketplace 不跳过持久化并报告成功。本增量不补齐 OAuth state、发起人授权、防重放或兑换与持久化的原子性；普通环境完整 OAuth 安全验收仍需单独完成。候选拒绝在本机接收端验证为零兑换请求，standard 正向使用本机 provider 与 SQLite，私有 PG 配置保全另有真实测试，均不等于生产外部服务验收。


连接器 HTTP 配置创建/更新/停用和删除入口现同样在解析、实例变更、持久化和邮件轮询操作之前调用 Manager 委托的 RequireIntegrationManagement；候选、缺策略/Manager、缺失或不匹配租户、SystemBypass 返回固定403，取消等非准入拒绝返回固定失败响应。名称级删除按既有 `(tenant,name)` 数据库范围逐个撤销完整 provider 实例。Manager.Provision（含Enabled=false）及带上下文的Revoke已接入相同owner检查，Send/Get仍未封闭。standard 持久化错误的旧处理、配置与实例的非原子性，以及并发新建与名称快照撤销竞争仍待处理。


Manager 配置操作不再允许缺失部署策略或租户上下文。standard 恢复仍先检查 connector_poll 启动权限，LoadAll 按条派生 WithTenantID（清除 SystemBypass）再走普通 Provision，不为恢复放宽请求准入。candidate 只通过声明启动建立目标，普通激活、停用与撤销均拒绝。Revoke 返回关闭错误并保留实例，不把失败当作成功移除；HTTP 收到该错误后停止后续配置删除。多实例撤销仍可能部分完成，并非整组事务。普通 Provision 在初始化后、发布锁内重新检查取消；取消时关闭新对象并保留取消与清理错误。CloseAll 保持停机清理职责，不要求请求管理准入。

对应candidate测试使用真实声明启动；notification/Feishu的本地探针声明local_only和稳定通用目的地摘要，原专业投递身份与断言保留。新增目标的重放测试创建新的启动配置与Manager，检查原receipt不扩展；目标变化和发送中重绑的generation防御保留为显式standard可变实例场景。测试配置不构成目标WSL部署授权，声明目标自身也不替代投递owner对scope、意图与权限的核验。


Webhook 新意图生产者与投递 Worker 通过唯一 Manager.ResolveDeliveryTarget 解析精确 tenant/name/provider 目标。冻结 ExecutionPolicy 要求来源 Ref 的 deployment/tenant/scope 完全匹配，投递能力仅限 webhook/notification/outbox；standard 必须显式启用能力且 scope 为空，candidate 还检查实例私有声明的 scope、能力与当前目的地摘要。Webhook owner 固定使用 webhook，不由载荷选择能力。目标检查不能替代已有 source、成员、claim、租约与回执事务检查；发送使用已捕获的同一对象，发送后再次核验目标及 generation。发送前非 ErrDenied 的取消、超时和基础设施错误保留 cause；发送后不确定性仍要求核对。该接入目前只覆盖 Webhook owner，裸 Get/Send 及通知/飞书目标授权仍待处理。


通知目标协议结构准备新增注册迁移 `044_notification_connector_target`，依赖原受控迁移序列至043与P准备，保持R退役合同独立。它仅为原ticket_notifications增加可空的目标协议版本、精确connector name/provider及目的地摘要；无历史行回填或当前实例自动绑定。四字段全NULL保留原数据，绑定版本1必须完整且与渠道匹配；绑定后的目标、业务身份与内容不可变，状态/租约由原worker维护。Ent字段不公开到JSON且不可变，但数据库保护来自该注册迁移，不得用Ent overlay替代。结构准备不代表producer/worker已接入，旧无目标外发意图的拒绝行为仍需后续实现。实际候选执行前，B须在真实待执行迁移语义清单纳入044，核对历史保全/权限与运行结构；本地私有PG验证不授权WSL迁移。


通知044目标协议已在EnqueueNotificationTx、EnqueueCreationTx和原Worker的连接器分支接入。新意图按当前同渠道唯一实例解析并写入精确目标，重放只复用原身份；Worker发送前后经冻结策略解析并核验generation。TicketWorkflowService与BPMN CC已在原事务中调用同一binder，bootstrap负责注入，缺少连接器目标依赖时拒绝并回滚；相关标准回归已通过。同步SendNotification现已通过同一enqueue在原事务创建站内记录与外部意图，提交后返回结果，禁止请求内直接外发；这不等于完整通知投递或候选启动放行。无目标历史外发意图不得通过回填字段来恢复，后续必须按原owner协议显式处理。


同步通知结果：`queued` / HTTP 202 表示本次新外部意图已提交；`appliedCount` 是本次新建站内通知的接收人数，`queuedCount` 是本次新外部意图数，`idempotentCount` 是复用已有意图的接收人数，`deliveryCount` 是总持久意图数而非送达次数，`externalIntentCount` 包括新建及重放外部意图，不推断其送达状态。混合结果保留各计数；BPMN回调描述已受理/已排队，durable callback继续使用内部稳定key并限制InAppOnly。已有外部意图与InAppOnly冲突时拒绝。内部key重放核对原内容/来源并冻结原渠道；无key仅生成本次调用ID，不保证HTTP重试去重。全偏好禁用没有持久receipt，返回blocked。页面按结果显示排队、受理或站内生效，不显示虚构送达次数。email/push专业准入、真实环境重启及端到端验收仍待完成。


持久邮件设置`DisableProviderFallback`时，已配置GraphProvider但当前解析不可用（含nil sender）须返回`email_route_unavailable`，不得改用SMTP；这是零发送的`not_accepted`，不是结果未知。未配置GraphProvider的显式SMTP仍可发送。普通允许fallback调用保持原行为。该规则只限制一次调用的跨provider回退，不证明重启后的目标绑定：bootstrap的Graph解析仍需迁移精确目标合同，不能据此放行候选企业邮件。


持久push现复用原WebSocket Hub单一发送队列，frame可携带非阻塞写入回执；所有按用户投递入口均同时匹配tenant/user，普通实时通知仍best-effort且不得作持久完成依据。Notification Worker等待至少一个匹配连接的消息Write及writer.Close成功，才提交原sent/SentAt；这表示socket传输接受，不表示浏览器消费或用户已读。无匹配连接/全部缓冲满属于确定未入队，可重试；曾入队但无成功回执、断线或超时属于delivery_unknown，不自动重发。一个连接成功后不保证其余在线连接均收到。成功写入后数据库完成失败继续由原processing lease恢复为unknown，不宣称exactly-once。此结果协议不替代notification capability、执行范围与候选目标准入；相关门禁仍须分别核验。


通知Worker对已按执行范围筛选的每行，先派生所属tenant上下文并核验冻结`notification`能力，再进入pending claim或processing租约恢复。禁用时不写attempt、lease、retry或终态，返回保留ErrDenied原因的汇总错误；空队列的0,nil仅表示没有处理，不能证明能力已启用。email/push已授权业务请求仍可持久为queued，不承诺关闭能力时会自动发送；连接器producer依照原目标binder要求保持不变。标准/候选正常通知Worker必须显式启用对应能力，不能依赖测试或部署默认值。该执行开关不替代邮件精确目标/提供方身份核验。

Graph 的 DescribeDeliveryDestination 只解析发送身份（AAD tenant、公开 app ID、mailbox、有效 AAD/Graph 端点），不读取 client secret、不初始化或取令牌；Init 共用解析结果并捕获摘要。客户端拒绝 HTTP 重定向及非 2xx 成功判定。此能力已接入可信配置读取、持久邮件目标及下述候选初始化准入；仍不代表凭据有效、远程授权成立或 nextLink/绝对 URL 已受完整目标约束。旧无目标通知不得绑定当前连接器，标准模式 worker 将其标记 delivery_target_invalid，候选历史范围保全规则保持。

Registry.DescribeDeliveryDestination 对注册的纯描述器执行精确 name/provider 与摘要格式校验，不创建或激活连接器。输入必须由可信配置owner提供，返回摘要不代表发送授权；候选完整声明允许disabled或省略执行能力，ConnectorStartupTargets返回独立的完整快照，ConnectorActivationTargets只返回冻结策略已启用的目标与能力；Manager和bootstrap使用后者进行激活与依赖检查。声明本身不启动实例，混合目标不授权禁用owner。可信配置描述owner与持久邮件队列接入尚未完成。

候选生产者可通过 Manager.DescribeDeclaredDeliveryTarget 按精确Ref、owner、name/provider读取冻结声明并核对纯描述摘要；返回只含摘要，不返回凭据，不实例化连接器。策略校验租户上下文、部署/作用域和声明owner，拒绝system bypass；禁用能力只允许描述，实际投递仍走RequireConnectorDelivery。此入口拒绝standard模式；standard仍需沿持久ConnectorConfig配置owner接入，邮件队列持久身份也尚未完成。

标准生产者可调用 Manager.DescribePersistedDeliveryTarget，必须传业务原事务的 tx.Client()；接口本身仍接受普通 *ent.Client，不能据此证明调用者必然在事务内。该入口仅standard模式，精确tenant/name读取唯一enabled ConnectorConfig并核对provider；禁用notification不妨碍读取已启用连接器配置。重复/缺失/禁用配置及JSON语法或类型错误拒绝；Graph字符串身份由纯描述器验证，不宣称通用JSON重复key或数值无损解析已解决。candidate必须使用冻结声明入口。两入口均未接入邮件持久目标协议。

EmailService.DescribeDeliveryTarget现统一生成v2 EmailTarget（transport、精确Graph连接器身份、digest），参数要求原*ent.Tx，standard Graph沿tx.Client()读取，candidate Graph沿冻结声明；不查询live GraphProvider。可信email_delivery.transport（环境ITSM_EMAIL_DELIVERY_TRANSPORT）只允许graph/smtp，空默认graph，经bootstrap复制到服务；选择smtp不依赖GraphProvider是否存在，Graph描述失败不回退。SMTP摘要使用实际Host/Port/Username/From与tcp机会式STARTTLS/TLS1.2证书验证/PlainAuth，不含Password、不假装SMTP是connector。旧SendForTenant仍为原有直接发送入口；原通知生产者/worker现已接入v2；Incident持久协议已接入下述v2原outbox路径，完整准入仍未完成，不能据此启用企业发送。

新增注册045_notification_email_target（044之后、受控退休R之前），扩展原ticket_notifications目标协议：nullable immutable target_transport，保留全NULL历史及v1非邮件形状，v2仅email Graph精确msgraph-email/microsoft或SMTP空connector身份。原目标与绑定后业务身份（含SLA来源）不可变；不回填历史、不改044 SQL。该DDL允许历史空目标形状，因此新意图完整绑定仍必须由生产者原事务实现，不能仅凭迁移宣称邮件已绑定。尚未执行任何WSL目标迁移。

EmailService.SendToTarget只执行已记录且通过v2校验的目标，要求租户身份与冻结执行能力均通过；不读取当前默认通道或live GraphProvider。Graph沿Manager精确解析实例并核对发送前摘要及发送后generation/摘要，实例变化返回acceptance_unknown；使用连接器捕获的mailbox。当前Graph发送接口不支持CC、附件及仅HTML正文，固定目标入口在外呼前明确拒绝；多个收件人中已有接受后再失败也返回unknown，不能整体重试。SMTP捕获实际配置并核对摘要、只尝试一次，发送后目标变化返回unknown；密码不属于目的地，轮换仍需实际认证验证。原通知producer/worker现调用此入口：BindNotificationTargetTx显式接收原业务*ent.Tx，普通通知、Intake creation、workflow CC及BPMN CC写入完整v2目标；缺目标回滚原事务，重放沿原记录，不补绑历史NULL。worker解码持久目标，发送前拒绝记delivery_target_invalid，发送后unknown不得重发。Incident outbox使用下述独立的原队列协议；完整S5与企业发送准入仍未完成。持久owner继续负责来源、领取和回执校验。


Incident邮件告警沿原outbox写入v2载荷，冻结WorkItem/Incident/Alert/tenant/event、单收件人、正文、actor/source/correlation和EmailTarget；接受审计在同一业务事务保存唯一事件摘要清单。创建入口要求显式正actor及非空source，并用原事务查询同租户active用户；缺上下文不再自动补0/system。HTTP创建告警及升级入口传播登录身份，明确身份拒绝返回403；这不替代入口RBAC，也不证明并发撤权或消费时当前权限复核。

Incident消费者核验持久claim/token/lease/attempt、源记录与成员关系、接受审计及完整载荷摘要，随后按记录目标调用SendToTarget；v1、未知字段/渠道、篡改内容或目标、冲突回执均拒绝。发送后重验并保存稳定投递回执；已存在匹配回执、发送后回执/发布故障按unknown处理，恢复不自动重发。仅任务私有PG和loopback SMTP已提供协议证据，不能替代候选受限角色准入、Graph实际激活或企业发送验收。


共享OutboxDeliveryWorker.DispatchOnce在任何unknown事件阻断、租约恢复、领取或attempt写入前核验同一repository冻结policy的outbox能力；禁用返回明确拒绝并保全队列，不等到handler才拒绝。此运行级检查要求内部system上下文，但不授予数据库权限；原WorkerPredicate、业务RLS、来源/member与专业owner能力仍独立校验。合法业务事务在执行禁用期间仍可接受完整意图。测试fixture的Standard()默认继续禁用，需要运行worker的成功用例显式Standard("outbox")。

候选Incident SMTP/Graph的私有PG职责链现已验证：真实Intake创建来源，非super/non-bypass租户runtime写原意图与投递审计，独立非super system队列连接领取/推进；system本身具BYPASSRLS且无tickets UPDATE，隔离依赖现有WorkerPredicate，不能称其无绕过能力。禁用下完整outbox/audit不变，启用后本机SMTP或模拟Graph只接受一次且其他outbox行不变；incident.created由既有专业消费者保留，本协议用例不运行它。该证据不替代WSL PG17恢复、所有角色准入、真实企业身份认证或完整S5。


Graph清单现声明InitializationLocalOnly：该标记仅表示Init没有外部副作用，并非限制目的地为loopback，也不授予发送权限。Init仍只解析并捕获配置、构造惰性client；token/健康检查/轮询必须显式执行。候选Manager按冻结声明与能力选择激活，核对实际初始化摘要；缺密钥、摘要不符、取消均拒绝且不发布实例。实际候选EmailService/原outbox链路使用该入口，以本机模拟Graph验证令牌请求、Bearer、固定mailbox、收件人/正文、逐字段投递receipt和二次扫描零新增HTTP/完整状态保全；未使用企业端点。SMTP路由变化的零调用为发送入口探针，真实接受另有loopback证据；Graph发送中换代覆盖不能扩张成所有运输并发矩阵。consumer当前RBAC、并发撤权和其余S5仍须完成。
### Unified support handoff acceptance

KAF reference inspection uses authenticated requester read identity for exact-number
lookup and paginated unfinished lists. Display number, current status, and frontend link;
selection does not grant task execution or authorize ticket mutation. ITSM lifecycle owners
remain authoritative when a reference closes or access changes.

The intake idempotency index is tenant + actor + channel + operation + key. The same
mapped actor across KAF workspaces replays the same immutable command/key; different
actors and tenants remain isolated, and a changed command conflicts. Workspace identity
mapping remains mandatory and cannot be replaced with client-supplied requester identity.

Use isolated runtime/database manifests for live acceptance. Verify read-purpose tokens
cannot create, current role permissions apply to every receipt/replay, exact/list isolation,
professional extension ownership, and configured process/manual task persistence. A manual
Catalog fixture may have unknown provider fulfillment projection; do not report it as
access granted or completed. Ordinary association is read-only; a delegated failure uses
only the original task's allowed action and original run/idempotency identity.


### Source baseline verification

Cross-file behavioral test mappings live in `scripts/test-coverage-mappings.json`. They identify reviewed domain tests rather than duplicate filename-matching tests. Pure removal-only changes add no implementation to cover; mixed edits remain guarded. Run `node --test scripts/__tests__/test-coverage-guard.test.js` to validate paths and removal classification. Mapping a conditional PostgreSQL test does not assert that it ran; record database evidence separately.

Jest ignores `.next` build copies. Only `theme-preference.ts` is excluded from coverage instrumentation: its functions are serialized into a standalone pre-paint script, and Istanbul counters introduce unavailable module closures. Its full bootstrap and global-error behavior tests still execute. Global coverage thresholds are unchanged.


### GA Gate 的可丢弃数据库准备

`ga-gate` 使用 `scripts/ga-controlled-preparation.py` 在 GitHub Actions 专属 Compose project 内编排既有迁移 CLI。它先验证空数据库，并确认普通初始化精确停在 P037 缺失边界；随后执行实际备份、独立恢复和账本核对，通过受控 `-prepare-workitem -evidence-file` 完成 P，再执行普通迁移与 seed。最终应用健康检查及 API smoke 测试保持原门禁。

该脚本只供可丢弃 CI 使用，不是开发或生产部署入口。容器、卷、网络及日志目录均属于本次随机 project；端口已占用或发现外部资源时拒绝执行。运行时使用独立 app/system/inspection 身份。隔离附件存储使用 MinIO SDK 要求的 `minio:9000` endpoint，TLS 由既有 `minio.use_ssl` 配置决定；不将 HTTP URL 传给 SDK，也不关闭存储检查。CI 专用 `scripts/fixtures/ga-minio-provision/` 使用 backend 已锁定的 MinIO SDK，在本次私有网络内显式创建配置指定的附件桶并确认存在；复用私有运行配置，失败即停止，不进入业务 runtime。生成的控制文件、凭据、备份及原始准备证据不上传，仅上传脱敏摘要；失败日志包含已删除初始化容器与迁移命令的 stderr，经过凭据过滤，清理前验证资源归属。隔离初始化也不得在 canonical 迁移后用全表或全函数 blanket grants 覆盖迁移拥有的最小权限；标准运行身份只获得既有 runtime admission 要求的 registry 只读权限，并由隔离环境 owner 预配匹配的 standard 绑定。前端根路径按既有认证中间件重定向到登录页或工作台，smoke 的最终页面 GET 跟随最多 5 次重定向、限时 15 秒，仍要求终点 200/304；循环或错误页面判失败，后端 API 检查保持不变。Python 编排单测与实际组装验证分别记录，P/R 回执及摘要来自实际 CLI 执行，不能以 mock 结果代替。普通 bootstrap/up 不执行 P/R，R038 保持未执行；真实环境操作仍遵循 [WorkItem 受控退役 Runbook](./deployment/workitem-controlled-retirement-target-runbook.md)。

编排回归检查不连接 Docker 或数据库：

```bash
python3 -m unittest discover -s scripts/__tests__ -p test_ga_controlled_preparation.py
```

### 主数据迁移验证工具包

使用显式审核后的profile运行 `python3 -m scripts.migration verify --profile <path>`。离线检查：`python3 -m pytest scripts/__tests__/test_migration_*.py -q` 及 `python3 -m scripts.migration self-test`。真实 `backfill --apply` 尚未准入，返回阻塞；范围和证据要求见[迁移验证Runbook](migrations/runbook-data-migration-validation.md)。
