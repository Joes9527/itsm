# Candidate Execution Scope Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use executing-plans to implement this plan task-by-task. Do not spawn extra implementation agents. Steps use checkbox (`- [ ]`) syntax for tracking.

状态：accepted（实施中；S1/S2 已提交并完成该阶段验证，S3 基础增量已实现并验证，业务接入进行中；S4 已有分段实现与验证，S5/S6 未完成）。

**Goal:** 关闭 T2 B2，证明真实候选新任务可以执行而历史记录和队列保持不变。

**Architecture:** 在原 WorkItem 创建事务登记不可补录的执行范围；既有服务/队列在领取前和写入时执行范围约束。bootstrap 只构造依赖，显式生命周期负责启动和停止，不引入第二套调度引擎。

**Tech Stack:** Go/Gin/Ent、PostgreSQL、Redis Stream/Watermill、现有 migration 与集成测试。

## Global Constraints

- 权威：[已接受设计](../specs/2026-09-12-itsm-candidate-execution-scope-design.md)、AGENTS.md、工程治理、双 Agent 总计划。A Mac 实施，B WSL 环境；额外 agent 仅作独立只读审阅。
- 代码起点 d7470a32dbb87acc9b5e4d9a895a146410723561，已有 T1 文档提交保留。新分支 `codex/fix/candidate-execution-scope`，独立 worktree；不移动已有 checkout，不推送/合并 main。
- 本文件路径均相对于任务 worktree；命令默认 itsm-backend。先核对 clean 状态和实际依赖，不继承 .env，不连接共享数据库。真实测试仅用已明确归属的任务测试实例；没有实例则保留集成门禁未通过，不能换共享源。
- 禁止 R(038)、历史 backfill、默认数据自启、企业实发、修改 B 配置。新 migration 只添加结构，历史 checksum/顺序保持；新增版本在普通迁移段、R 之前，不能依赖 R。
- 执行顺序 S1→S2→S3→S4→S5→S6。S6 通过后进入鉴权计划，最后才发布包含两项修复的新 CandidateSHA。每项 RED→GREEN→diff 检查→具名提交。

## 文件与接口地图

新增 `common/executionscope/{policy.go,policy_test.go}`：配置解析、不可变 scope 引用、统一拒绝语义；不承载业务生命周期。
新增 `database/execution_scope.go`：事务设置与已登记 WorkItem 查询，依赖 SQL/Ent，不依赖 controller/service。
新增 `migration/candidate_execution_scope.go`：范围、成员、运行身份绑定、INSERT 触发器及最小授权 SQL；沿 migrations.go/migration_plan.go 注册。
新增 `internal/bootstrap/runtime_lifecycle.go`：显式任务启动/停止；修改 app.go、kaf_worker.go、config/config.go。
真实场景测试集中 `tests/integration/candidate_execution_scope_postgres_test.go` 和 `candidate_runtime_preservation_test.go`；纯策略测试相邻。

统一合同（S1 实现，后续任务直接使用）：
```go
// common/executionscope
type Ref struct { DeploymentID string; ScopeID string; TenantID int }
type CapabilityMode string
const Disabled CapabilityMode = "disabled"
const Scoped CapabilityMode = "scoped"
func ValidateRef(ref Ref) error
func ValidateCapability(name string, mode CapabilityMode) error

// database: caller owns tx and tenant authorization
func BindExecutionScope(ctx context.Context, tx *sql.Tx, ref executionscope.Ref) error
func RequireExecutionMember(ctx context.Context, tx *sql.Tx, ref executionscope.Ref, workItemID int) error
```
`Ref` 不能由 HTTP 字段赋权；`BindExecutionScope` 只接可信启动清单。Ent 事务适配使用其已有 SQL driver/transaction 边界，不另起事务查询成员。repository 的 SQL predicate 是相同权威表的查询表达，不再创建业务许可缓存。

## S1：范围结构、登记与受限权限

**Files:** 上述 policy/database/migration 新文件；`migration/migrations.go`、`migration/migration_plan.go`、`migration/migration_plan_test.go`；新增集成测试文件。

- [x] 写 `TestValidateRefRejectsIncompleteIdentity`，直接覆盖所有缺失项：
```go
func TestValidateRefRejectsIncompleteIdentity(t *testing.T) {
    for _, ref := range []Ref{{}, {DeploymentID: "candidate", TenantID: 1}, {ScopeID: "bad", TenantID: 1}} {
        if ValidateRef(ref) == nil { t.Fatalf("accepted incomplete ref: %#v", ref) }
    }
}
```
- [x] 运行 `go test ./common/executionscope -count=1` 保存 RED；补齐 UUID、非空部署、正数 tenant 验证及有限能力名称/mode 检查，再运行 GREEN。
- [x] 写真实 PG 测试：owner 建源 fixture；runtime 尝试直接登记历史 ID 必须 permission denied；合法新 tickets INSERT 自动登记；专业扩展失败导致 tickets/member 全回滚；跨 tenant/关闭/跨部署范围拒绝。运行新测试名 `TestCandidateScopeRegistration`，确认 RED 后再写迁移。
- [x] 新表固定为 `execution_scopes`、`execution_scope_members`、`execution_runtime_bindings`。最后一表由维护者绑定 DB session_user、deployment、standard/candidate 模式；运行角色无写权限。成员 work_item_id 唯一，scope 外键，tenant 从 tickets/scope 比较，不复制业务状态。候选角色绑定缺失不能默认标准模式。
- [x] INSERT 触发器从可信 runtime binding 与事务局部设置核对 scope/tenant，使用窄 SECURITY DEFINER 固定 search_path、全限定对象及撤销 PUBLIC EXECUTE；runtime 禁止直接成员 DML。标准角色显式 binding 才不登记；任何普通请求无法从 candidate 切换成 standard。
- [x] `BindExecutionScope` 核对绑定/活动范围后使用参数化 `set_config('app.execution_scope_id',$1,true)`，只在当前事务生效。`RequireExecutionMember` 必须命中同 deployment/tenant 的 active scope 和成员，否则返回明确错误。关闭范围先停止全部执行实例再更新。
- [x] 注册 `039_candidate_execution_scope`：先确认版本未占用；不要修改 frozenMigrationVersions，新增普通定义放在冻结普通流之后、R 之前。测试旧24条账本仍合法、037/032–036前置保留、新版本可在无038时规划、未知/漂移账本拒绝。与后续040同时集成时顺序039→040→手动038，依赖不从版本数字推断。
- [x] 运行 `go test ./migration ./common/executionscope -count=1` 及真实角色用例 GREEN；核对 SQL 无历史 UPDATE/DELETE/DROP/backfill，再提交 `feat: add bounded candidate execution membership`。

S1 提交：`ae97fbfd2`。真实私有 PostgreSQL16 的受限角色、原子登记/回滚、跨租户/部署/关闭拒绝及默认 ACL 检查通过；独立审阅的历史 R 依赖与默认 ACL 问题已修复。该结果不替代目标 PostgreSQL17 验证。

## S2：无副作用构造与显式生命周期

**Files:** `internal/bootstrap/{app.go,kaf_worker.go,runtime_lifecycle.go}`、`config/config.go`、`service/{tool_queue.go,attachment_storage.go}`、`controller/connector_controller.go`、`pkg/eventbus/eventbus.go`；新增 `internal/bootstrap/runtime_lifecycle_test.go`，更新已有 lifecycle 测试。

**Interface:** 生命周期使用 `Start(context.Context) error` / `Stop(context.Context) error`；先停止/等待消费者再关闭依赖。能力清单只接受设计第4节列出的名字，默认未声明项 disabled，required G2 项 disabled 阻塞验收。

- [x] 写 `TestConstructDoesNotStartRuntime`、`TestRuntimeStopsBeforeDependencies`，以计数器证明构造零 Start，显式运行只启动一次；Stop 等待未结束任务。运行 `go test ./internal/bootstrap -run 'TestConstruct|TestRuntime' -count=1` 保存 RED。
- [x] 从 app.go 构造移除订阅、EnsureExtension、LoadAndDeployTemplates、InitDefaultBindings、LoadAll、内存队列 goroutine；分别交给迁移、具名资源准备或显式生命周期。流程配置只读验证。不要在 Run 中无条件复刻自动初始化。
- [x] 把 embedding/SLA/escalation 的 context.Background 改为生命周期 context，ticker select 包含 Done，加入 wait group。禁用能力不创建 goroutine、不查全量数据。minio 只检查已存在 bucket，失败返回错误；选定后端不能 fallback。
- [x] config 新增显式 standard/candidate 模式、deployment/scopes/capability 清单；启动核验数据库角色绑定与范围一致。旧生产配置如何补足只记录手册，不操作旧部署。
- [x] 运行定向生命周期与 attachment/tool queue 测试 GREEN，并用 `candidate_runtime_preservation_test.go` 在真实隔离依赖核对构造前后表、bucket、stream 无新增副作用；提交 `refactor: make application runtime startup explicit`。

S2 提交：`fbd52c240e91124f12709486c946bf6d85768770`；A 的阶段交接提交 `b2cdeb62f`（实现 worktree）。实际测试使用现有生命周期测试命名并补充 `TestOccupiedPortDoesNotStartRuntime`、`TestServeFailureStopsRuntime`、`TestEnabledCapabilityRequiresRunnerBeforeStartingAnything` 及 `TestCandidateConstructPreservesDatabaseAndStreams`，未迁移既有测试。构建、定向并发检测、真实 PG 角色检查，以及 PG16/独立 Redis7.2.16/固定源码 MinIO 的完整构造前后保全均通过。独立审阅发现的关闭等待、重复关闭、错误退出和测试端口误连问题已关闭；证据及有限观察边界详见实现分支的 `docs/review/2026-09-12-candidate-t1-handoff.md` B2 检查点。S2 通过不放行候选，不替代 S3–S6 和鉴权计划。

## S3：业务创建、修改与结构化子任务归属

**Files:** `handlers/common/workitemcreation/creator.go`、`handlers/shared/workitemmutation/contract.go`、`service/ticket_service.go`、`service/ticket_attachment_service.go`；专业 `service/incident_service.go`、`service/problem_service.go`、`service/change_service.go` 和 `handlers/service_request/` 内既有写服务；`ent/schema/{outbox_event.go,process_instance.go}`；对应生产者和集成测试。

- [ ] 先列既有 WorkItem 创建事务与所有 G2 写入口，记录路由→服务→被写主体→事务。使用 `rg -n 'Create\(|Update\(|Delete\('` 定位后逐项阅读，不能以关键词扫描当完成证明。
- [ ] 写 `TestCandidateCreationAndMutationBoundary`：新建 base/extension/member 同事务；历史父、历史关联端、其他租户拒绝；新任务评论/附件正常；GET 历史工单不触发 Feishu。实际执行保存 RED。
- [ ] 在现有创建事务绑定 S1 Ref，在原业务授权/版本校验后的同一写事务核验成员。禁止新增并行创建服务。候选非必需写路由拒绝，业务服务也执行相同限制，防止 Worker/CLI 绕过。
- [ ] 为 outbox/process instance 新增 nullable、不可变、具有 tickets FK 的 `execution_work_item_id`，只由权威创建事务写新行，历史保持 NULL。这是执行主体引用，原 aggregate/business_key 保留各自业务语义，不用 JSON 字符串推断执行权限。callback 经 process_instance_id 关联；notification 用既有 ticket_id。任何不明确的新生产者拒绝 scoped 发布。
- [ ] 把新增引用纳入039；Ent schema 与唯一 SQL 同步生成，核对生成差异只限这两模型及新字段。逐个生产者从已解析 WorkItem 身份设置引用，不能仅在消费者补值。
- [ ] 运行真实 API/service 创建修改用例及现有 WorkItem 专业测试；历史摘要与关系不变，提交 `feat: constrain candidate business writes to new work items`。

S3 阶段记录（2026-09-13；基础提交 `6261941b4`，统一创建接入 `04f07a13e`，生产者引用 `ff3fbcd71`，Incident command `3212d33bb`，Incident metadata/直接升级 `ae1731978`，规则记录/统计 `bebd1f726`）：SQL/Ent 原事务适配及两个结构化字段已实现，私有真实 PG 的 RLS enforce/回滚/不可变外键和三依赖构造回归通过。独立基础审阅无阻断。统一 intake 创建事务与可信配置传递已接入，已核实生产者的结构引用传递已接入，Incident command core（含 Assignment/StatusChange 原事务动作）已接入，其他业务写事务与生产者的执行范围准入仍未接入，上述复合验收项继续保持未完成。实际事务所有者还包括 Incident commands、Problem/Change handlers、Requested Item repository/callback、共享 assignment/deletion/tag/comment/attachment/relation；common creator/mutation 只是接口，不能视为全局写拦截点。新增真实 `TestCandidateIntakeCreationBoundary` 覆盖新 base/Incident/member 原子性、新父子/Problem 关联、历史父/来源拒绝、历史 receipt 只读 replay、未准入租户及扩展失败回滚，使用生产 enforce 驱动和受限目录快照。独立审阅的基础设施故障误分类已修复。该测试不是原计划完整 `TestCandidateCreationAndMutationBoundary`，评论、附件、专业修改和历史 GET 异步仍未验收。生产者定向回归、真实 PG 的 Incident/relation 引用及 FK/duplicate 分类、构建通过，独立引用审阅无阻断；零引用不代表许可，候选原事务/成员准入和 claim/recovery 仍未完成。Incident command 的真实新/历史成员、迁移前 receipt replay、两种规则动作原事务回滚及 start 命令 outbox 失败回滚通过，构建/定向回归/三依赖构造检查通过，限定独立审阅无阻断。ExecuteRule 起始记录及结果/统计各自在原写事务完成成员准入，结果和统计原子提交；真实 PG 覆盖历史无记录写入、失败/成功/条件不匹配、结果及统计写后失败回滚、起始 INSERT 后关闭 scope 时结果事务再次拒绝，定向回归/构建/独立审阅通过；动作本身及全扫描过滤仍须分别验收。UpdateIncidentTx/EscalateIncidentTx 已在原事务接入，真实 PG 的历史拒绝、新成员写入与主动回滚、包装入口提交、定向回归和构建通过，限定独立审阅无阻断；升级告警分支和后续故障注入未覆盖。Event/Metric 显式原事务及重大升级已接入（实现 worktree 后续检查点），真实 PG 验证历史拒绝、新成员成功、子记录原事务回滚、Major timeline 与 MetricAction 写后故障回滚，定向回归/构建/独立审阅通过。CI、Alert create/ack/resolve、NotificationAction 已接入 `03b277f9d`；真实 PG 历史拒绝、新成员成功、ack/resolve timeline 故障及通知动作 outbox 故障回滚、定向回归/构建/独立审阅通过。Problem 专业 command/metadata/delete 已接入 `fc71e6266`；真实 PG 历史拒绝、command/metadata receipt 只读 replay、新成员调查/验证/解决/关闭/重开及 resolve outbox 故障回滚通过，定向回归/构建/独立审阅通过；RCA/Evidence 子表专项运行及关联 Incident 消费者未完成。Change/PIR 原事务已接入 `dfa851b68`，真实 PG metadata/cancel/delete/PIR CRUD 新旧边界、历史 PIR receipt 及 metadata/PIR audit 故障回滚通过，构建/定向回归/独立审阅通过；真实 task completion/callback、完整 Change 专业旅程与 command/metadata 历史回执尚未候选验证。Requested Item update/delete/workflow callback 及 AccessCompletion 原事务已接入 `42795a901`，真实 PG 历史全字段保全、新成员写入和 update/complete extension 写后故障回滚、定向回归/构建/标签编译/独立复审通过；KAF completion 原引擎/Requested Item contributor 的双上下文、最终 ledger fence 后故障整体回滚、新成员完成及只读重放在 `6b2b50756` 经真实 PG 验证；同时修复 PG 时间微秒持久化导致纳秒凭据重放误拒，含两个半微秒取偶边界，回归/构建/独立复审通过。前置审批/snapshot/ledger 为 fixture，HTTP、ExecuteAction 初始领取/digest/最终记账和真实审批/provider 尚未验收。monitoring 直接指标写入、共享能力等入口仍未完成，不能以 action core 保护替代整个规则边界。详见实现 worktree 的 T1 交接 S3 检查点。

### S3 手动升级实施检查点

`1554a4030` 新增有效RED，生产实现尚未修复：s3-manual-escalation-contract-red-2.log真实PG证明039前generic被修改、新member正向成功但出现硬编码assignee/缺审计；第二次最高级调用被user2 FK拒绝，不作为已实际降级证据。s3-manual-escalation-priority-red.log单元独立证明critical→high。首次误选Incident历史fixture的拒绝不计隔离RED。当前测试集包含明确失败回归，不沿用上一轮PASS。独立只读审阅确认来源及依赖，详细证据见实现分支T1最新交接。

- [x] 核验真实路由/唯一候选所有者、保留有效历史generic与新member正向，建立范围/处理人/审计/最高优先级RED。
- [ ] TicketService为唯一HTTP手动升级所有者；移除仅测试调用的TicketLifecycleService/EscalationService平行手动方法，迁移有效行为测试。BPMN独立升级保持专业流程语义并另行原事务接入。
- [x] 手动请求沿已有workitemmutation Meta/receipt传递稳定operationId与expectedVersion，可信actor/source来自边界；当前权限/状态/专业class校验后scope/member准入，重放必须再次授权。最高priority不降级，未知值显式拒绝；移除硬编码用户1/2/3，无真实分配策略则保留原assignee。
- [x] 在既有Feishu/Outbox边界新增update intent与具名handler：原事务冻结existing mapping ID/GUID、destination、actor/真实操作回执、WorkItem version/payload及ExecutionWorkItemID，不冒用creation。无mapping不得自动创建或假成功；注册到现有worker。
- [ ] 投递前及结果写回重新核验tenant/member/当前权限、mapping/目标身份；成功GUID不匹配、调用后错误及成功后落盘失败标记delivery_unknown，不声明ReplaySafe。同一mapping需跨worker串行和持久顺序/不确定性约束，测试不得以进程内锁或仅发送前version检查替代；其余旧Feishu入口另列缺口。
- [x] 手动原事务包含version CAS、actor/reason审计、通知意图和Feishu更新意图，移除事务外goroutine直接发送；保留已配置同步能力，不通过删除副作用或静默忽略声明完成。
- [ ] 真实PG验证历史整行/关联无变化、新member、同操作重放、权限/版本/scope撤销、通知/审计/outbox实际写后回滚、并发及多版本更新顺序；本地声明provider验证映射/目标变化及不确定投递。核验HTTP当前权限/DTO契约、构建/回归/独立审阅，全部证据具名记录后才将本检查点计为完成。

手动事务核心 `a5a9d4f92`：真实TicketService/HTTP/前端改typed命令reason/version/operationId，构造显式policy；原事务现行actor/权限→immutable Replay→首次写前scope/member/version/status→CAS/审计/通知共同提交。generic专属，critical不降级且保留assignee。s3-manual-command-full-pg.log完整边界PASS，包括新实现以Standard在039前生成receipt再迁移后只读重放、历史generic保全、新member正向/重复/conflict、actor失效/closed scope拒绝、通知/审计写后回滚恢复；定向回归/build、前端API55项、独立复审通过。历史fixture不证明旧发布版本已有receipt，未有JWT/浏览器全HTTP验收；并发恢复专项待补。

手动Feishu更新增量 `98de8076b415139d15a13021decc6f92e6d5b248` 已解除上述临时门禁：原事务冻结existing mapping/destination/actor/operation/version/task，审计绑定事件和摘要；既有Worker注册独立有序update handler，前后校验claim/attempt/当前权限/member/操作回执及mapping，完成事务锁定Outbox行，调用后不确定性blocked且后序不越过。TaskID=GUID前置条件只约束新协议参与映射，无映射拒绝而不自动创建。s3-feishu-update-final-pg.log完整候选边界PASS，无skip：双命令快照顺序/重放、未领取拒绝、目的地/映射/actor/payload变化、provider与实际mapping写后故障、producer事件/审计写后回滚恢复。审阅发现claim检查后恢复间隙，双连接有效RED后加行锁，竞争写锁超时且完成成功；只证明锁互斥，不声称整个worker恢复E2E。定向回归/build/标签编译及独立复审通过，详情见T1最新交接。旧Feishu直发/在途、两个手动平行方法/BPMN、全HTTP/SSO与多生产者专项仍待处理，所以全链复合验收项继续未勾选。S3/S4及后续门禁未完成，CandidateSHA/停止状态不变，无共享环境变更或企业实发。


唯一手动所有者增量 `5ff8d1dcf`：移除TicketLifecycleService/EscalationService无生产调用的重复手动方法、interface及独用helper；不保留包装或伪SLA history。旧测试保留原文件/函数名，改为真实TicketService命令并强化priority/version/assignee/audit/no-SLA-history断言；纯priority unknown按当前契约显式拒绝。s3-manual-owner-final-regression.log三包定向、s3-manual-owner-pg.log完整候选边界无skip、全后端build均PASS，独立复审无阻断。测试Standard/super_admin不替代真实普通角色权限。上面“唯一所有者+BPMN”复合项仍不勾选，剩余BPMN独立escalate及旧Feishu直发/GET副作用继续执行。固定CandidateSHA及停止状态不变。


### S3 BPMN升级接入检查点

真实引擎领取后由原Ticket handler另行通知/写Ticket，随后才推进流程。`s3-bpmn-escalation-scope-red.log`真实私有PG先验证正向，再在原handler调用前确定性关闭scope：sweep error但Ticket priority/status/updated_at已修改，整行保全FAIL；当前测试集不全绿。owner仅构造process/instance/callback前置夹具，不等于启动/入队全链。独立审阅确认RED有效；原测试cleanup恢复scope并隔离故意失败callback，未操作共享环境。

- [x] 引擎将当前callback id/tenant/executionKey/lease owner/attempt放入可信调用上下文；只读execution key不可作为写授权。
- [x] TicketService新增独立workflow升级所有者，由handler注入接口调用。保持escalate_to、notify_admin_ids和escalated流程语义，不复用HTTP升级语义；移除handler直接写。
- [x] 原事务锁定processing callback并核验当前owner/attempt/未过期lease/handler/action/kind及CallbackPredicate，重读instance结构WorkItem引用/业务类型/当前activity；user-task另核验completed task与持久completion actor，service-task使用initiator。
- [x] 参数只从持久callback读取，严格解析优先级与整数接收人，核验当前actor权限、接收人tenant/active、generic/未删除、scope/member及Ticket version CAS；操作摘要覆盖目标/动作/接收人，合法receipt重放仍重新授权。
- [x] 状态/priority/version、immutable audit receipt及EnqueueNotificationTx在同事务提交，保持原durable callback站内通知语义，不扩展企业渠道。领域提交后的流程推进沿既有引擎，推进失败重试以receipt幂等，不以字段相等判定已执行。
- [ ] 真实PG验证scope撤销、lease丢失、伪造目标/actor、通知/审计写后回滚及领域提交后流程推进失败重放；回归/构建及独立复审。领域事务与流程推进仍为分段事务，不声明全链原子。


工作流升级事务增量 `bcd1f5c47` 已实现上述代码接入：handler不再直接写，bootstrap注入独立TicketService workflow command；原callback attempt/lease等可信身份在业务事务持久匹配并锁定callback/instance/Ticket，原事务scope/actor/version/审计/站内通知，领域receipt重放与后续引擎推进分段。version为持久回调必需字段，缺失拒绝；generic typed result接回原生命周期投影，保留原change别名规范化。s3-bpmn-escalation-full-pg.log完整候选边界PASS，无skip；真实worker positive/default、scope撤销、lease owner变化、专业目标拒绝、通知/审计实际写后回滚恢复、领域已提交而推进实际写后失败并在接收人禁用后成功receipt重放。定向三包回归/build/标签编译通过，独立复审无新增阻断。旧永久extension失败hook限定原用例生效，原断言未削弱。user-task分支/真实流程启动、普通actor撤权、旧attempt/过期/多worker专项仍待补，复合验收项不勾选；其他BPMN和共享写入口仍待逐项处理。完整证据与限制见T1最新交接。

S3/S4/S5/S6及候选完整交付仍未完成，CandidateSHA不变、候选停止。


读取副作用修复 `9654afcab`：GetTicket仅保留repo.GetByID，删除读后Feishu goroutine/事务/映射更新。s3-ticket-read-feishu-red.log实际本地connector在Get后PATCH由1→2；green.log正向connector仍可用但Get不增加PATCH、mapping JSON不变，原两项创建意图回归亦PASS。service/controller读取定向与全后端build通过，独立复审无阻断。SQLite/httptest一秒有界观察加源码删除佐证，不作为完整候选PG或真实企业证据。六处业务写直接同步及手动同步API仍待接入，S3复合项保持未完成；完整范围验收及固定CandidateSHA/停止状态不变，见T1最新交接。


### S3 工单编辑原事务接入检查点

前端接入前置 `4a69f1e29` 已修正调用清单：除Detail普通编辑/AI建议，TicketBatchOperations已挂载的批量status/priority也调用编辑；Kanban的handleStatusChange只有声明未绑定，不能计作真实拖动验收。三个既有transport入口已统一拒绝缺失/非正安全整数version，Detail/Batch与未绑定回调透传已有Ticket.version，useTickets参数有类型约束。四套Jest123 PASS、全前端类型检查/独立审阅通过；未验证真实组件交互，尚未冻结打开表单时的version或稳定operationId。此项不替代后端Meta/receipt/原事务，以下业务待办不勾选。RequesterID当前编辑未保存；FormFields只有创建INSERT能力，后续必须显式完善原所有者编辑或拒绝输入，不允许静默忽略/重复INSERT/第二份JSON。共享标签六类及service_request_item/catalog_task原核心写语义需单独保全或按专业所有者迁移。

当前真实调用是普通UpdateTicket、UpdateSubtask和tool_queue.update_ticket。历史s3-ticket-edit-scope-red.log证明越界及孤立标签，现由 `226ffd46f` 原入口RR事务Bind/member、目录及版本检查、标签resolver和UpdateTx共同提交修复。s3-ticket-edit-tx-pg.log完整候选边界PASS无skip，含实际标签/Ticket写后故障回滚与解除后成功；定向回归、后端构建和独立审阅通过。只完成数据库主体及标签原事务，旧可选version、返回Ticket、事务外通知/SLA/Feishu及actor/parent/Meta/receipt仍待接入，不将以下完整命令勾选。

- [ ] 将编辑命令统一到现有WorkItem Meta/receipt，必需expectedVersion与稳定operationId；actor/tenant/source由HTTP、子任务边界及持久工具invocation构造，不能信任JSON userId或每次重试生成新身份。前端及工具输入版本契约同时迁移，保留明确冲突响应。工具expectedVersion在批准时持久化，operationId从invocation派生；done写失败重试复用原版本/回执，不读取新version冒充原命令。
- [ ] 原服务事务读取当前WorkItem与现行actor/权限，合法历史receipt允许授权后只读重放；首次写入先Bind/member/版本检查，再允许任何标签创建或关系写。子任务父子归属及相关父成员在该事务确认。
  `64ab11f0f` 已接入原事务当前actor/tenant会话、ticket:update及专业WorkItemPolicy权限，UserID禁止JSON输入，两HTTP入口授权拒绝403。缺失/停用/预热缓存后撤权/外租户actor保全、普通editor正向、Incident标签专业权限补齐通过；完整私有PG/回归/构建及独立审阅通过，PUT/PATCH controller身份测试通过。后续 `157e2ea30` 已把实际父成员/同租户未删除记录及PATCH预期父ID比对接入原RR事务，JSON不能提供预期父ID，普通编辑也检查实际父级；历史父拒绝及成员父正向、父整行保全、HTTP路由错误和正文不能覆盖通过，完整私有PG/回归/构建及独立审阅通过。校验基于RR快照，不阻止并发父删除；父删除/外租户专项负测尚未覆盖。仍未完成Meta/source/op及历史receipt顺序，因此不勾选。
- [ ] category/subtype、专业字段/共享标签、处理人/请求人、状态/解决方案/表单字段契约逐项核对，不静默忽略客户端字段；标签目录创建及关系替换复用既有所有者/原事务，不另建平行resolver。
- [x] 仓储更新接收调用方事务并沿用唯一字段映射和CAS实现；`81860d2e4` 提供显式UpdateTx，SQLite及真实私有PG验证提交/回滚/旧版本标签回滚、另一连接提交前不可见；定向回归/全后端构建及独立审阅通过。仅仓储前置完成，TicketService及各调用入口尚未迁移，原业务RED仍存在。
- [ ] 同事务写编辑审计/稳定回执、状态通知意图、应有SLA违规收尾；所有失败传播回滚，applied SLA冻结不重套策略。保留通知偏好及原接收人语义，不把日志warning当副作用成功。
  当前 `49293729d` 已将状态通知意图及SLA收尾加入原事务：Notification/TicketNotification/第二条SLAViolation实际写后失败全回滚，重试精确一组；email仅pending/偏好全禁用/相同接收人去重/缺依赖拒绝通过。完整私有PG及定向回归/构建/独立审阅通过。此条仍不勾选：编辑审计及稳定命令receipt未接入，Feishu旧路径及Meta/actor/parent/必需version待迁移。
- [ ] 飞书更新意图与原编辑提交原子绑定，使用与manual更新相同event_type和稳定aggregate键，避免跨类型越过前序；具名编辑来源的权限/回执/resultVersion/status与payload摘要须由consumer验证，不能伪造手动升级来源或删除原同步能力。
- [ ] 真实PG覆盖历史整行/标签目录及关系保全、新member标题/分类/标签/状态、重放/冲突、标签/通知/SLA/审计/Outbox实际写后故障回滚、actor撤权与子任务父范围拒绝；相关前端/API/工具契约、构建/回归和独立审阅后才能标记完成。

当前检查点 `57cd25088`（上述旧记录保留各提交时点语义）：唯一UpdateTicket现已迁移TicketEditCommand/可信Meta及不可变Result，必需version/op、当前授权后receipt、首次写入scope/member/父级/状态/版本校验、原事务标签/通知/SLA/飞书意图/AuditLog均已接入。HTTP两入口返回Result且普通入口不再预拦截终态；tool_queue严格解析持久批准版本，op由invocation派生，registry发布schema。飞书producer/consumer复用同type/aggregate，显式edit/manual action及结果状态和当前权限核验，删除编辑commit后直接同步；配置飞书缺映射明确拒绝，旧payload缺新字段会阻断，未做旧事件升级兼容。RequesterID非零/FormFields非nil显式拒绝，不静默忽略。

三个前端transport、Detail普通/AI、批量及hooks均已迁移；普通表单冻结打开时version/status，单次意图保留深拷贝payload/version/op，Result只触发详情失效重读。明确后端409/4090结束旧意图并供刷新后重新确认，未知结果保留；未绑定Kanban不计交互完成。final-pg.log完整私有候选边界PASS无skip，包含原回执RED→GREEN、closed scope只读重放、同op变更冲突、audit/outbox实际写后全回滚恢复及edit真实worker→本地fakeprovider。recovery.log真实工具done失败恢复和PUT/PATCH终态重放通过；工具严格解析有ProcessJob RED→GREEN。定向回归/全构建/tsc与四套Jest127 PASS，独立审阅P1/P2已修并复核无新增阻断。以上测试不是WSL PG17、企业实发或真实浏览器证据；完整证据见实现分支T1最新交接。

后续验证 `12c38eda4`：两个请求在真实Ticket UPDATE前barrier会合，确定性复现一次提交/一次PQ40001；失败方原cmd重试返回胜方完整Result，单receipt及version+1保全。迁移前真实Standard owner生成edit回执，再独立编辑改变status；候选无member目标仍只读返回原Result，新op/当前version仍拒绝，Ticket/Audit整行及通知/outbox/成员保全。完整私有PG及新增两场景race检测通过，独立审阅无阻断；不代表自动重试、并发HTTP成功或通知/飞书确定性交付竞争。此复合项继续未勾选：真实组件重试/跨刷新持久恢复与浏览器、专业核心编辑归属及请求人/表单所有者仍待完成。S3/S4/S5/S6与后续交接门禁不因该检查点自动通过，固定CandidateSHA和候选停止状态不变。

组件检查点 `d27c57d68`：普通详情实际表单覆盖打开版本冻结、未知结果原请求重试及明确冲突后重新打开确认；批量实际菜单发现三个key与既有表单/执行分支不一致，真实空表单RED后统一key。状态/优先级经实际选择和确认验证未知结果复用完整请求、刷新后明确冲突新操作；标签只验证字段入口。两套完整Jest组件测试23 PASS、全前端type-check及独立审阅通过，详情与证据见T1最新交接。此为mock API边界组件测试，不能证明真实浏览器点击、HTTP/后端提交、AI建议交互或跨页面重载恢复。上述复合项保持未完成，固定CandidateSHA及候选未启动状态不变。

接续实施顺序（2026-09-13）：原UpdateTicket一次迁移为可信typed command与现有workitemmutation.Result，不保留旧接口或以当前Ticket冒充历史结果。digest包含明确edit操作名、目标、预期父ID、expectedVersion及规范化业务输入，保留tags未提交/清空区别，不按现行目录解析结果重算历史请求身份。当前授权之后查receipt；首次写入才进行scope/member、父级、终态、版本和目录校验。普通HTTP移除GetTicket+CanEdit预拦截，否则关闭后的合法重放会被挡住；两HTTP入口构造Meta并直接返回Result，操作身份冲突映射409。工具registry补齐参数/结果schema，queue仅使用已批准并持久化的expectedVersion及invocation派生operationId，done失败恢复不能重读当前版本。

前端三个transport及实际调用者必须随契约迁移：TicketDetail AI采纳不再读取updated.id或合并结果到Ticket；useTicketsQuery不再setQueryData(Result)，应使详情失效并重新读取。详情编辑/AI与批量操作保存同一意图的payload/version/operationId，结果不确定重试复用，用户改变内容才生成新意图。返回数据只表示已提交命令结果，不证明当前工单快照。飞书沿用同type/aggregate并校验明确edit audit/action/status/digest，删除该编辑入口commit后独立同步。最终需验证终态后replay、同op不同payload冲突、审计/outbox写后原子回滚、并发同命令、工具done失败恢复及前端Result不污染详情缓存。

独立review_execution_scope_s1确认RED及计划方向，强调UpdateSubtask缺少CanEdit且parent在事务外、工具done晚于业务提交、仓储须显式UpdateTx、飞书必须相同类型/目标排序。该批是原执行范围缺口修复，不扩展历史回填或共享迁移；固定CandidateSHA不变，候选停止，S3及后续门禁仍未完成。

## S4：队列原子领取、恢复及周期执行

历史 claim RED（`s4-kaf-historical-claim-red.log`）已由 `de22553c2` 修复：两条冻结 policy 构造链贯通，claim INSERT/独立 lease CAS、finalize/non-completing、completion receipt/callback recovery 原事务准入；异步恢复复用首次完成变量校验，修复误要求同步合同。真实 PG 验证历史0写、新成员claim/重复冲突、closed scope过期lease拒绝、completion及恢复保全与active恢复，最终定向回归/构建/独立审阅通过。CreateDelegatedTask 两条入口现已补齐原事务准入，joined入口改显式*ent.Tx；真实 PG 历史拒绝、新成员生成/引用、joined主动回滚及两入口outbox写后故障回滚通过，构建/回归/限定独立复审通过，见T1最新检查点。仍是分段测试，不代表完整ExecuteAction或真实BPMN节点推进；通用worker、历史applied回放及全部finalize分支等专项未完成。详见T1最新交接，S4继续未勾选。

**Files:** `service/{outbox_event_repository.go,outbox_delivery_worker.go,kaf_outbox_dispatcher.go,bpmn_callback_outbox.go,ticket_notification_service.go,sla_monitor_service.go,escalation_service.go}`、bootstrap 注入点及对应现有测试。

通用 Worker 原混排 RED 已由 `065226f1e` 修复：冻结 WorkerPredicate 在原事务核对实际 system role binding/manifest，并将 scope/member/结构引用/tenant 限制加入全部领取、恢复、unknown、attempt/retry/finalize SQL。system角色仅允许scope三表可选SELECT，单租户Bind不放宽；两bootstrap显式policy，无默认standard。最终 `s4-outbox-worker-verified-pg.log` 完整私有PG测试PASS：历史四类/外租户NULL与真实成员完整行及audit不变，新候选正常投递；7个真实claim后转换分别scope关闭/binding撤销拒绝保全，恢复后成功。四包回归、全构建、integration标签编译及独立只读复审通过。后续 `56879f7c9` 补齐两并发claim的12唯一事件、无attempt过期重领/旧token拒绝及有attempt过期blocked+audit；四分支真实audit写后故障回滚通过，解除后状态+新增audit断言也PASS。并发起跑不保证确定性SQL竞态，进程重启/完整周期未验证。callback历史RED现由 `a0a1ddc4d` 修复：两扫描、claim/retry/completeTx/outcome、engine领取后读取及推进事务、enqueue/blocked原事务范围约束；System只允许instance三个身份列可选SELECT，单租户绑定不放宽。最终 s4-callback-worker-final-pg.log 全PASS：历史两行不变，新成员本地handler经真实engine到End(callback/instance均completed)，scope与两种role binding撤销时明确失败且全表不变；四包回归/全构建/integration标签编译/独立复审通过。candidate内联执行键、入队/用户回调与故障专项仍待验证，handler与token推进保持既有分段边界；通知/SLA/escalation和完整周期待完成，S4继续未勾选，详见T1最新交接。固定CandidateSHA和候选停止状态不变。


通知Worker范围增量 `e479b8d3d`：构造显式policy，原scan/claim/expired/complete/retry/fail均在事务附加tenant_id/ticket_id成员SQL。s4-notification-worker-delivery-pg.log全PASS：旧pending/expired原始PG全字段保全；新成员缺传输显式failed；本地声明connector成功sent；发送后scope关闭导致完成写回拒绝、后续过期转delivery_unknown且不重复发送。四包回归/container编译、全build、integration编译、独立复审通过。仅任务私有PG与本地接收端；binding/并发/写后故障及生产者/mark-read尚未验证，SLA/escalation和其余门禁未完成，详见T1最新交接。

SLA Monitor 的有效RED提交 `f8b7043cb`：s4-sla-monitor-confirmed-red.log确认历史违规集合[]→响应/解决两条，同时新成员精确2条违规独立断言通过。首次无有效definition的fixture失败已排除，不计证据。尚未修复；下一步必须同时覆盖scan/单项原事务、alert两个直接入口/重复与cooldown、通知意图及eventbus提交后边界，修复当前吞错/虚报NotificationSent路径；不能只过滤scan。该RED仅violation，warning/critical/通知/eventbus仍未验证。详见T1最新交接。

SLA事务前置能力 `af1106661`：新增EnqueueNotificationTx，调用方tx内范围/tenant/recipient校验，复用站内双表写入与现有外部pending队列，不发送/commit；偏好同事务快照，跨channel内容冲突检查及已materialized recipient渠道冻结。focused notification_intents PG、Notification回归、build通过；真实写后故障回滚与偏好切换重放测试通过，审阅P2已修复。仅前置能力，尚无monitor/alert生产调用，SLA confirmed-red仍未修复；未将focused PASS计为全套PASS。详见T1最新交接。

- [ ] 写 `TestCandidateWorkerPreservesHistoricalStates`，混排历史 unknown/pending/expired claim、候选 pending、跨租户 pending；捕获每条 status/attempt/claim/updated_at，运行一次真实 Dispatch/Claim，要求历史逐字段不变。当前全量 claim/BlockUnknown 应 RED。
- [ ] 所有未知事件标记、expired claim 回收、claim、mark attempt、retry、published/dead-letter 语句在原事务内加入同一个成员 EXISTS 条件，不能仅过滤返回的 slice。查询形状：
```sql
EXISTS (
  SELECT 1 FROM execution_scope_members m
  JOIN execution_scopes s ON s.id = m.scope_id
  WHERE m.work_item_id = outbox_events.execution_work_item_id
    AND s.id = $1 AND s.deployment_id = $2
    AND s.tenant_id = outbox_events.tenant_id AND s.status = 'active'
)
```
- [ ] callback 用 process instance 的结构化引用，notification 用 ticket_id；保留原 claim token/lease/CAS。scope 不可验证时错误，不修改历史行作诊断。
- [ ] SLA/escalation 的初始查询即限制成员，级联发事件保持同归属；独立 KAF dispatcher 使用同一 repository 策略。测试调用真正的原 worker，不创建 candidate 专用 worker 实现。
- [ ] 运行并发领取、租约超时、已执行未回执重启、未知新事件、跨租户与完整周期测试 GREEN；证明新任务原有幂等及失败状态保留，提交 `fix: scope worker claims and recovery before mutation`。

SLA violation 增量 `73cc9a3e1` 已修复历史违规 RED：冻结清单/原事务成员扫描、单工单 deadline/cycle/duplicate 重读与 version CAS；两违规、原事务通知意图和结构化 sla.breached outbox 原子提交。既有通用worker注册契约校验handler，不确定发布阻断重发。bootstrap统一周期入口按冻结候选清单或standard受限发现逐租户执行，补齐依赖启动检查并移除无调用旧watcher。`s4-sla-atomic-final-pg.log` 完整candidate intake边界PASS，含历史保全、新成员、重复、通知/第二条outbox写后故障回滚、closed scope、两并发最终无重复；race、定向回归、构建和限定复审通过。并发不证明确定性SQL竞态；SQLite标准发现/本地假bus不替代PG角色或真实事件投递。SLAAlertService/warning/critical transport及escalation仍未完成，候选配置未隔离alert分支时显式拒绝；S4保持未勾选，CandidateSHA和停止状态不变。详细证据见实现分支T1最新交接。

SLA alert 直接入口增量 `f579a866d`：CheckAndTriggerAlerts/TriggerSLAWarning 原事务 tenant/member/deleted、deadline/cycle/rules/duplicate/cooldown 与首次 version CAS；history/通知意图原子提交，规则渠道与用户偏好取交集，空渠道零通知，有渠道缺notifier拒绝。critical同步邮件与虚报NotificationSent=true已移除。s4-sla-alert-verified-pg.log完整candidate边界PASS，含两历史入口保全、新成员、写后故障（history及两通知表）回滚、渠道交集、owner设置的reopen/pause计算；回归/build/标签编译及限定复审通过。完整发送关联/状态投影尚未完成，候选monitor的alert拒绝仍保留，bootstrap尚不启用告警周期。下一步独立040 ordinary migration：TicketNotification nullable immutable history结构关联与history nullable tracking version，旧行不回填，复合tenant/ticket FK和不可改指约束；worker继续只维护通知权威状态，history查询按结构关联投影，不解析DeliveryKey授权，不改变038退休依赖。S4仍未完成，CandidateSHA及停止状态不变。

SLA发送投影及monitor接入 `5fa4ae3e6` 已完成上述结构关联增量：独立040 ordinary、nullable immutable history引用与tracking version、同tenant/ticket复合FK、legacy目标拒绝/不可改指/禁止新sent双写、默认函数ACL剥离，038合同未变。旧行NULL不回填，新告警version1原事务关联；history API仅由关联通知聚合新状态，legacy保留原事实；worker未新增history权限。monitor已接回alert并传播warning错误。s4-sla-projection-final-pg.log完整候选边界PASS，含删除Ent预建列/新索引后真实040升级、原始旧行保全、独立FK/不可变约束、旧事实/零通知/混合状态投影、原worker缺provider失败及真实monitor告警历史保全；migration全包、定向回归、build/标签编译、最终独立审阅通过。手动状态夹具不等于外部送达，单领域事务原子不等于整轮扫描原子。升级链尚有旧notification_sent写路径，仍待接入；S4及剩余门禁未完成，040未在WSL/共享环境执行，固定CandidateSHA/停止状态不变。

自动升级增量 `0266df8b0`：matrix/long_pending/unassigned 原扫描与单项事务接入frozen policy/成员；Ticket version及history level CAS、审计和durable通知共同提交，managed history复用040引用。旧周期告警不推进；非规则提醒复用每WorkItem/cycle/type操作回执，不再创建无效SLA history。bootstrap复用已配置实例，冻结租户发现及逐租户上下文/依赖检查。s4-escalation-final-pg.log完整候选边界PASS：历史alert/提醒WorkItem原始JSON保全、新成员matrix+pending、周期回执重放、long_pending通知/审计写后故障回滚恢复；定向回归、build、独立复审通过。owner设置周期不等于重开E2E，unassigned单独故障、matrix多级回滚及确定性并发仍未验证。真实手动升级和旧零AlertRuleID方法尚未接入，下一步核对真实HTTP所有者并统一原事务；S3/S4/S5/S6及后续门禁仍未完成。CandidateSHA不变、候选未启动、无共享环境变更。完整证据与限制见实现分支T1最新交接。

有序Outbox前置 `d200b864f`：handler声明按aggregate串行、registry冻结、原query及claim CAS检查同tenant/type/aggregate较小ID前序均published；历史NULL-ref、blocked/dead_letter/unknown/future-due前序不跳过，外层scope保持。s4-outbox-ordering-red.log两个真实worker确定性复现同目标越序；final-pg.log完整候选边界PASS，覆盖并行独立目标、历史前序原始行保全、未决前序阻挡、已尝试过期unknown、接收端成功后published写后故障回滚及后序零调用。回归/build/标签编译、冻结声明测试及独立复审通过。该能力不证明生产者commit顺序或Feishu投递；生产者必须同目标先CAS/锁再enqueue，所有更新共用稳定目标键。该前置提交时Feishu producer/handler尚未接入；后续 `98de8076b` 完成新手动协议接入，见S3检查点，S3/S4及其余门禁仍未完成。无新表/迁移或共享环境操作，固定CandidateSHA不变。

## S5：Stream 与请求异步边界

通知事务/Worker接入检查点 `4aecebdf8`（2026-09-14）：EnqueueNotificationTx/EnqueueCreationTx共用精确目标binder，Worker消费持久字段并前后核验同对象/generation；真实SMS producer先生成完整意图后四项错声明拒绝及合法接收PASS。补未知渠道、resolver cause与pending/failed/sent写回cause的RED→GREEN；真实事务rollback、原目标重放、冲突和多provider拒绝PASS。完整私有suite在最后cause包装前PASS，随后具名PG增量PASS，全后端build与独立复核通过，详情见T1。

更广相关单测仍有7项回归（s5-notification-protocol-unit.log）。实际TicketWorkflowService.createCCNotifications与bpmn.CCTaskHandler.createCCNotifications尚直接写无目标外发队列，下一步注入唯一binder并接入原事务/bootstrap，保留真实CC和BPMN测试，不手填协议字段消除失败。同步SendNotification直接外发同样待迁移。不能勾选通知整段：标准恢复、目标变化/重启矩阵与其余owner尚未完成，S5/S6/T3/T4/G3与固定CandidateSHA/停止状态不变，无共享环境操作。

通知目标结构检查点 `083d7d1c6`（2026-09-14）：注册044_notification_connector_target，四可空不可变字段与数据库全有/全无、版本/摘要/渠道约束，禁止NULL补绑定及绑定后目标/业务身份修改；无历史DML，R历史依赖不变。Ent生成完成，不公开目标JSON。真实私有PG从缺列状态执行DDL，旧字段JSON保全、新字段NULL、合法sms旧行绑定被不可变trigger 23514拒绝、非法目标23514、状态更新正向与继承EXECUTE剥离均PASS。migration包、全后端build与独立审阅通过；完整私有race仍仅原4项通知权限RED，无新增FAIL/SKIP/RACE。详见T1最新具名段。

此检查点只完成结构准备，不勾选下方端到端协议；producer/worker尚未写/消费这些字段。后续用真实偏好支持的sms+本地探针生成合法意图，再替换Manager；不能为复用旧webhook fixture新增产品渠道。盘点SendNotification直接外发路径，与EnqueueCreationTx/EnqueueNotificationTx一起接入原owner合同。实际B迁移清单需加入044，当前未执行WSL/共享迁移；CandidateSHA不变、候选未启动，S5/S6/T3/T4/G3仍未完成。

通知/飞书目标持久协议后续合同（2026-09-14，通知RED提交657692fe3，现有S5内依赖，待实现）：实际通知worker调用Manager.Send→Get，仅有channel；`ent/schema/ticket_notification.go`没有provider/目的地身份。飞书bootstrap注入`func(tenant)`同样只取同名实例，虽有专业Destination仍缺精确实例与generation。只检查当前目标有资格不能证明持久意图在重试/重启后仍发往原目标。以下工作复用原队列、原专业owner与唯一Manager，不新增路由服务、通知引擎或同渠道多目标产品能力。

- [ ] **通知协议与迁移。** 修改`ent/schema/ticket_notification.go`，在原行加入可空且不可变的connector name/provider、目的地摘要与目标协议版本；仅连接器传输使用该组完整身份，in_app/email/push保留原专业传输路径并单独核验它们的准入。由`migration/migrations.go`实际注册序列选择下一未占用版本，新迁移与测试放原migration目录；不能修改历史SQL/checksum或已有迁移文件。旧行NULL保全，不回填、不默认当前实例。受保护历史行仍不领取；标准旧外发意图若缺目标身份须明确失败/待核对，不能静默适配到新实例。新身份必须全有或全无，未知协议拒绝；DB约束/不可变触发器以及Ent生成通过仓库既有流程实现，真实私有PG验证历史行原始摘要与字段篡改拒绝。不得以Ent overlay代替迁移。
- [ ] **原事务生产者。** 修改`service/ticket_notification_transaction.go`、`ticket_notification_creation.go`与必要的`ticket_notification_service.go`调用点；在已验证WorkItem与recipient的原事务内，固定notification能力、真实ExecutionPolicy Ref，先确定同channel唯一启用实例再走ResolveDeliveryTarget。来源仅现有可信配置/声明，不能从请求载荷选provider；零个或多个实例均显式拒绝，不能取首项。只在生产新意图时选择目标，写入捕获的精确身份；保留现有每用户/渠道唯一索引，不引入fan-out。原receipt/idempotency重放验证既有目标协议完整性并复用原渠道集合，不因配置变化添加目标；相同业务键冲突内容仍拒绝。新增真实事务失败回滚、并发重放、目标变更后重放保全测试。当前4项worker RED使用直接queue fixture，不替代真实producer验收；GREEN阶段必须先由合法producer生成带完整目标协议的意图，再只替换错误Manager，断言安全错误分类，排除缺字段或数据库故障造成假绿。
- [ ] **Worker独立核验。** 修改原dispatchClaimedDelivery，用行中精确身份和固定notification能力解析；不使用Manager.Send/Get当前首项。保留原scope/member/租约/recipient检查，发送捕获的同一对象；返回后重验目标摘要与generation，再由原completeDelivery完成。明确前置拒绝零调用且不写SentAt；取消/基础设施故障正确分类。发生调用后的不确定结果沿用delivery_unknown，不自动重试。加入实际worker错scope/deployment/webhook-only/outbox-only、多个实例、同provider目的地改变、发送中重绑与重启后拒绝测试。保留合法可信声明实际本地接收端正向、原历史行摘要与关闭scope恢复断言。
- [ ] **飞书同一边界。** 在`service/feishu_creation_delivery.go`、`feishu_update_delivery.go`、`feishu_sync_service.go`及`internal/bootstrap/app.go`同时迁移producer与consumer；固定outbox能力并传递真实Ref，消除只按tenant返回裸对象的授权缺口。保留既有专业Destination/GUID/幂等映射与有序outbox，不用通用摘要覆盖专业语义；精确实例身份必须来自持久意图或既有不可变关系，不能Worker临时选择。涉及payload版本必须拒绝未知和无法证明归属的旧意图，不回填历史。真实create/update、错声明、重绑、映射/回执后置失败分别验收。
- [ ] **集成复核。** 执行私有PG/Redis/MinIO具名race suite、受影响单测、全后端build与migration检查；独立审阅明确目标选择、事务原子性、历史NULL和重放协议。只在全部目标持久协议通过后关闭本段；仅当前目标资格检查通过不得标记完成。同步T1及开发指南，再继续裸实例/其它请求异步入口盘点。禁止共享/WSL数据库写入，实际目标迁移仍由B按T3前置准入执行，CandidateSHA不变。

Webhook精确目标检查点 `3e4e0e257`（2026-09-14）：producer与Worker已按下方合同接入唯一Manager.ResolveDeliveryTarget/冻结ExecutionPolicy，原8项声明归属RED关闭；保留原source/member/claim/lease/receipt与同对象/generation规则。standard重绑及ACK恢复改完整standard链，私有临时角色有BYPASSRLS，仅为组件夹具，非目标standard角色准入，数据库触发器未放宽。审阅P2发送前cause丢失由真实PG Deliver三例RED复现后修复，非ErrDenied原cause保留，发送后仍delivery_unknown；测试MarkBlocked是直接claim清理，不是worker重试结果证据。

s5-webhook-target-full-green.log完整私有PG16/Redis/MinIO所选race suite PASS，无FAIL/SKIP/DATA RACE；三包具名unit race与全后端build PASS，独立最终复核无新增阻断。精确目标能力单测另证明全局notification启用不允许使用webhook-only目标。日志、夹具限制及变更证据见实现T1最新段。下方9343ac41f是修复前历史RED，不再表示当前失败。裸Get/Send/GetInstance、通知/飞书owner等仍待迁移，S5/S6/T3/T4/G3未完成，CandidateSHA与候选停止状态不变，无共享环境操作、企业/云外呼或push/main合并。

Webhook声明不匹配RED `9343ac41f`（2026-09-14）：真实producer和OutboxWorker分别复现错scope、错deployment、notification-only/outbox-only声明被忽略。producer写入intent/消费receipt；worker使用先前合法intent、仅替换运行Manager后仍实际loopback发送、published并记录delivered。完整私有suite仅这8场景失败，无skip/race，其余PASS；最终补验source整行保全及终态重轮询，错误交付receipt仍触发RED。独立复核有效，尚无生产修复；另一scope仅Manager声明，不是另一个已建立active DB scope。完整日志及限制见T1；下方合同为下一实施步骤，当前G2仍未放行，CandidateSHA与停止状态不变。


Webhook目标声明消费的后续实现合同（2026-09-14，基于实际producer/worker旁路RED）：沿唯一Manager与冻结ExecutionPolicy提供精确目标解析，输入为已由owner验证的执行Ref、代码固定owner能力及tenant/name/provider。拒绝nil/取消/SystemBypass、上下文错租户、未知能力；冻结deployment/mode/tenant scope与Ref完全一致。candidate还须比对私有targetAuthority的scope、能力、摘要和实例身份，standard须空scope、相同实际deployment及显式启用投递能力。返回捕获对象/generation/目的地身份；Worker用同对象发送，后置复核保留该规则。此为部署限制，不另建业务授权引擎，不替代原source/member/claim/lease/receipt校验。

Producer在新intent提交前接入；重放原receipt不得重新发现目标。Worker独立在既存合法intent发送前及回执复核接入，不能仅依靠producer gate。必须分别证明错scope、错deployment、notification-only及outbox-only声明拒绝Webhook，并保留合法声明真实投递与重启正向。现standard Manager/candidate worker混合的可变目标防御夹具须拆为完整standard source/authority/worker/Manager链，不放宽Ref一致性迁就旧测试；原generation及发送中重绑防御断言必须保留。Send/Get/GetInstance裸入口及其余通知/Feishu owner仍须后续迁移，不能因新解析方法存在而声称整体权限边界完成。


Manager配置owner检查点 `15374aa47`（2026-09-14）：Provision（含disabled）和contextful Revoke自身调用唯一RequireIntegrationManagement，candidate/nil/错租户/SystemBypass拒绝，standard恢复保留启动gate后WithTenantID。直接Revoke可信实例RED→GREEN；关闭失败保留对象并返回，HTTP不继续DBdelete。审阅P2取消Init后发布经确定性RED修复：发布锁内取消检查，关闭新对象并保留两个cause。candidate通知/Feishu/Webhook正向迁移真实声明启动，third新Manager重放不扩展原receipt；standard目标重绑防御保留原generation断言，未把candidate RED统一换成standard。

s5-manager-gate-full-private.log完整私有PG16/Redis/MinIO候选边界/构造保全/Stream恢复race PASS，507f62293剩余三项直接激活RED已关闭，无skip/race。随后取消补丁和7场景×3操作owner矩阵经具名race PASS；standard Feishu/BPMN CC/消费者清理补验通过，integration_postgres仅编译通过未执行DB测试，全后端build和独立最终复核通过。详情见实现T1。此为配置owner门禁完成，Send/Get/GetInstance裸实例及targetAuthority在投递owner中的权限消费仍待关闭，不勾选整个S5/G2；WSL目标恢复、T3/T4/G3仍未完成，CandidateSHA及停止状态不变，无共享环境操作或push/main合并。


连接器 HTTP 管理准入检查点 `5e57b761f`（2026-09-14）：Provision/Revoke 入口在解析、实例操作、持久化和轮询之前委托同一RequireIntegrationManagement，拒绝candidate/nil策略/无或错tenant/SystemBypass，取消固定失败。私有PG DELETE先RED复现原行被删，现保全；合法standard provider正向又复现原Revoke丢失provider导致实例残留，现按tenant/name携完整cfg逐一撤销，与原数据库删除范围一致。具名race、多provider关闭及外租户保全、后端build和独立复核通过。s5-connector-http-verified-private.log完整私有PG16/Redis/MinIO套件HTTP创建负例及删除保全/standard真实DB操作通过，原七项激活失败降至直接Manager三项，整套仍FAIL，无skip/race，不放行候选。

后续仍须接入Manager.Provision（含disabled）与contextful Revoke owner门禁、Send/Get权限；快照撤销并发竞争与旧持久化错误语义未关闭。LoadAll WithTenantID已撤销继承SystemBypass，无需放宽gate。candidate通知/飞书/Webhook正向fixture应迁移可信声明，standard可变目标防御用例须保留原重绑/generation验证，详见实现T1交接，不以统一改standard消除candidate RED。固定CandidateSHA、候选停止及共享环境边界不变，完整T1–T5/G1–G3目标保持未完成。


Marketplace 配置写入检查点 `860222303`（2026-09-14）：唯一冻结策略 RequireIntegrationManagement 仅 standard+匹配显式tenantctx+无SystemBypass+未取消允许配置管理；四个公开写owner（Install含重新启用、Uninstall、Update、Merge）首次查询/持久化前拒绝candidate/nil策略，不按商品类型放行。HTTP保留Request.Context并正确403，nil连接器runtime不静默成功。独立审阅发现Merge旁路，经真实PG RED后关闭；飞书回调复用同gate在兑换前检查，nil服务拒绝、重复callback ID fail closed，非ErrDenied固定500。standard本机provider+SQLite真实回调保存及query不能改租户通过，但OAuth state/actor/防重放与跨兑换持久化原子性未补齐。

s5-marketplace-full-private.log 私有PG16/Redis/MinIO中新13配置保全/标准真实写入及standard HTTP正向PASS；整套仍有507f62293普通Manager/Controller请求激活七项既有RED，无SKIP/race，明确不是全绿。最终错误分类增量经s5-marketplace-final-review-unit.log具名race复核，未重新宣称完整suite；全后端build通过，独立最终审阅无新增阻断。详见实现分支T1现有交接。下一步仍须关闭启动前Provision、Revoke/配置删除、Send/Get裸实例入口；不勾选普通请求整体门禁，不关闭S5/S6或G2。固定CandidateSHA与候选停止状态不变，无共享环境操作、企业/云外呼、push/main合并。


连接器可信目标激活实施范围（2026-09-14，accepted，尚未实现）：

可信启动增量`5ebf4a7df`：API消费者前由唯一Manager预检整批local_only manifest、统一初始化、核对通用目的地身份并原子发布；失败清理当前及prepared对象，Close等待在途初始化，后续工具队列启动失败先Close再清理目标。真实bootstrap RED→GREEN、独立P2关闭竞争RED→GREEN、具名race与最终后端build通过；候选Webhook ack恢复旅程已改为声明激活，真实worker发送/去重通过。完整私有suite仍有507f62293七项已知失败，整体FAIL。启动前普通配置和Marketplace持久化、裸Send/Get及全部provider边界未关闭，因此下方复合项继续未完成，CandidateSHA与停止状态不变。详细证据与100ms关闭观察窗口、非所有消费者故障穷举等限制见T1。

前置RED `507f62293`：真实Manager/Gin Provision handler允许未声明candidate目标，测试随后通过builtin Webhook各向loopback发送一次；HTTP还改写私有fixture既存配置。s5-connector-request-activation-final-red.log七项预期失败，无skip/race，独立复核有效。配置行数保全断言已补；不覆盖Marketplace、生产认证或迁移前遗留数据。当前含新用例的测试为RED，生产未修复，不能引用此前绿色结果放行。

- [x] 在现有ExecutionConfig中声明精确tenant/scope、connector name/provider、允许的既有投递能力和不可变配置身份；验证scope归属、能力开启、重复实例键，构造时深复制。声明来自可信启动配置，凭证沿既有安全配置来源解析，不写日志或提交秘密。前置提交`0347fdc78`：具名config/database race、完整config包及最终后端build通过，独立审阅无阻断；修复既有环境解析不递归列表对象的问题。实际目标身份核验和Manager消费仍属于下项，未实现。目的地摘要不等于业务投递授权；outbox不能授权所有handler。配置数值有损JSON往返拒绝，不恢复加载前精度。完整私有suite仍仅含507f62293七项已知失败，整体为FAIL，详细证据见T1。
- [ ] 唯一Manager复用内部构造流程：candidate启动只激活声明目标，不枚举历史数据库；普通Provision请求即使提交相同scope/配置也不能取得启动权。初始化前核验可信来源及manifest初始化行为声明，初始化后核验真实目的地身份；未知初始化行为拒绝，失败关闭新对象，成功才发布generation，部分启动失败清理。
- [ ] Connector管理及Marketplace安装/启用/配置更新入口在首次持久化之前检查准入；拒绝时零factory/Init、历史配置整行不变。Marketplace当前先UpdateInstallationConfig后Provision，不能只修Manager而保留前置写入。
- [ ] 现有通知/Webhook投递owner继续验证持久意图、成员、租约、精确目标与generation；清点Get/GetInstance/完整Connector直接调用，启动目标资格不能授权Test、诊断、polling或绕开业务投递。不得把connector_poll当投递许可。
- [ ] 真实测试同时证明未声明目标拒绝、声明目标经生产启动接入和既有worker投递成功、错误scope/配置/目的地拒绝、重复无新增效果，以及初始化失败/部分启动清理。现有手动Provision夹具需转换为可信启动路径后重跑，不能保留测试专用生产后门。全关Manager不满足G2。

独立只读审阅确认当前ExecutionConfig没有目标清单，既有fixture注入不构成生产可信来源。以上为既有交付隔离设计的实施细化，不新增共享环境操作授权。

历史连接器恢复入口检查点 `bae2c0cfd`（2026-09-14）：LoadAll在客户端检查、配置读取、解析与Init之前核验冻结启动能力，只允许standard显式connector_poll enabled和内部SystemContext；candidate、nil gate和普通租户请求均拒绝。真实私有PG测试验证候选零Init/无实例、同条配置standard正常恢复一次且配置整行保全；内部上下文标记不替代数据库角色准入。具名race、完整私有PG16/Redis/MinIO候选边界与Stream回归、全后端build通过，独立审阅无新增阻断。integration_postgres标签仅编译通过，未执行其数据库测试；未验证目标PG17。详见T1交接记录。可信scope新目标激活、Provision/Send/Get旁路和实际poll/provider仍待完成；CandidateSHA与停止状态不变，S5/S6及T3/T4/G3未放行。

连接器读取/诊断修复检查点 `20c99482e`（2026-09-14）：下方4a92cc3a8 GET外调RED已GREEN。四GET及Provision响应只读本租户观测快照，删除HealthCheckAll；POST /health要求connector:write与Manager冻结connector_diagnostics能力，candidate只能disabled，standard显式enabled，未探测不伪造健康。快照深副本、实例generation替换保护、取消返回error及本/外租户探测边界均验证；完整私有PG16/Redis/MinIO race、具名回归、全后端build和独立审阅通过，无skip/race，见T1。POST完整认证/RBAC浏览器路径尚未E2E；实例激活/LoadAll/Send/Get旁路及可信scope目标声明仍待实现，不能把此修复等同整个Manager或G2完成。CandidateSHA、停止及共享环境边界不变。


连接器读取外调RED `4a92cc3a8`（2026-09-14）：实际Gin四GET对预置当前及另一租户无candidate来源准入实例同步HealthCheck，loopback接收端每路由本/外租户各+1，八项预期失败，无race；显式Health不强制200以容许后续明确拒绝。未走LoadAll或生产认证，不外推历史恢复验收。独立复核有效，该提交时含新测试的全套为RED；现读取/诊断部分由上方20c99482e修复，其余门禁仍未放行。下一实现保留唯一Manager：读取只读健康快照，主动诊断/历史恢复由实际owner拒绝candidate；受控新scope投递目标以启动可信声明绑定tenant/scope/精确实例/目标摘要/能力，Init副作用纳入manifest，继续由原worker验证意图/成员/租约/摘要/generation。检查Get/GetInstance裸Connector旁路，不用connector_poll代替通知/Webhook/诊断授权，也不全关Manager后宣称G2完成。详情见T1。CandidateSHA及停止状态不变，S5及后续门禁未放行。


云发现门禁检查点 `664251d94`（2026-09-14）：真实PG空账号扫描复现候选DiscoverAll/RunAll返回nil的RED；两个构造器现必需冻结ExecutionPolicy，DiscoverAll/DiscoverAccount/RunAll在I/O前要求cloud_discovery显式启用。candidate保持disabled，standard也不默认启用，能力配置复制防运行中对象修改授权。独立审阅提出缺租户上下文P2，经RED后改为必须匹配tenant context；nil策略/取消/bypass/未知能力/单账号绕过负测通过。standard owner空账号扫描正向不代表provider可靠性。最终完整私有PG16/Redis/MinIO race、database/cloud包race、全后端build及独立复核通过，无skip/race，见T1。连接器、embedding、导入导出直接入口和剩余S5/S6、鉴权及目标T3/T4/G3仍未完成；CandidateSHA、停止状态和共享环境边界不变。


工具结果竞争检查点 `7acbcaba0`：真实失败业务INSERT后回滚与成功业务提交后，两个ProcessJob结果写回事务竞争同一调用；done先/failed先均验证实际锁等待、后到40001保留cause及整次重试。最终一工单一创建回执，done首次完成整行不被覆盖，failed可恢复正确done；结果WorkItemID/编号/recordClass及Error=nil与真实创建记录匹配，工单整行（状态/版本在内）不变。业务两次尝试有意先后完成，不宣称同时INSERT竞争。完整私有PG16/Redis/MinIO race与独立审阅通过，无skip/race；本轮仅测试/文档，未重复生产构建，证据见T1。工具复合项和S5/S6、鉴权、目标T3/T4/G3保持未完成；固定CandidateSHA及候选停止状态不变。


工具授权事务修复检查点 `eedec3389`：下述e12a7d297身份撤权RED已修复。新增043替换旧042函数入口，候选来源锁扩展至原调用、不同actor/approver/requester及当前Role/RolePermission/Permission，权限规则仍复用既有Go逻辑；Queue与审批使用RR，原业务RR与目录共享快照不变，40001保留并要求完整事务重试。9类撤权×业务提交/实际INSERT后回滚共18项真实PG验证等待精确业务PID，撤权提交后必须明确身份/权限拒绝；原审批竞争验证等待及冲突后整次重试。新增用户fixture曾影响后续SLA收件人数，已按生命周期停用且保留引用，原断言未放宽。最终完整私有PG16/Redis/MinIO race、具名回归、迁移/database测试、全后端build及独立审阅通过，详情与失败记录见T1。该矩阵仅证明创建路径，不外推编辑/审批/结果各自完整矩阵；结果竞争与剩余S5/S6、目标PG17/T3、真实T4/G3仍未完成。固定CandidateSHA及候选未启动状态不变，无共享环境操作或push/main合并。


首次业务写身份撤销RED `e12a7d297`：真实intake已核验工具后、Ticket INSERT前提交用户停用，仍创建工单/receipt各一条；撤权实际提交标记通过，5秒有界，独立复核确认，见T1和s5-tool-actor-revocation-red.log。actor/approver/requester同一fixture，非三者专项。该RED提交时生产未修、包含新用例的全套为RED；现已由上方eedec3389修复，不以此历史状态代替最新证据。下一步在窄权限边界保护原invocation、去重排序的相关用户及现有授权读取的Role/RolePermission/Permission，再复用领域规则重验（含真实super_admin role）；实际支持委派时覆盖会话/分配依赖，不另建权限规则或扩大业务配置写权。统一锁序并验证撤权先提交拒绝、业务先锁则撤权等待commit/rollback，保留RR冲突重试；替换同步hook防止自等锁/超时假通过。CandidateSHA与未启动状态不变，后续门禁未放行。

运行绑定撤销验证 `32d374953`：扩展真实PG结果事务锁验证，binding deployment变化、mode改standard、删除绑定分别等待结果commit/rollback；从实际mutation事务取得PID并与撤权连接pg_blocking_pids精确匹配，撤权完成后下一调用拒绝、回执保全。取消等待后再恢复fixture，独立复核无阻断；定向及最终完整私有race通过，无skip/race，见T1。仅测试/文档，无生产变更或重复build；不证明standard保护或目标PG17。审批/身份变化及结果竞争仍待完成，CandidateSHA与未启动状态不变，T3/T4/G2/G3未放行。

工具编辑原事务检查点 `b107922c1`：真实UpdateTicket以不存在调用ID修改版本RED→GREEN；在业务Replay前复用当前审批/来源/042锁，绑定ai_tool与tool:update_ticket操作身份及完整获批编辑DTO摘要（含OperationID）。无调用、title/version/source/operation变造与pending拒绝；合法queue一次版本更新，业务回执重放整行保全，批准失效后重放拒绝。实际Ticket UPDATE后故障回滚且不提交审计回执，原调用重试一次修改；不是审计INSERT后故障。完整私有race、具名工具及普通编辑race、build和独立复核通过，无skip/race，见T1。target/actor/parent逐项及审批/身份并发专项不计入本项，目标T3/T4/G2/G3仍未放行，CandidateSHA与未启动状态不变。

工具创建原事务检查点 `e9db9de9a`：直接intake绕过queue以历史approved来源创建业务RED→GREEN；createAttempt在Bind后/Claim前复用工具领域审批来源核验，绑定来源元组、真实ID、actor/requester、批准参数重建摘要及显式相同IdempotencyKey，042锁保持到业务事务结束。新增operation负测发现摘要不含key的P1已修；真实users SELECT故障RED关闭P2，actor/approver非NotFound错误保留基础设施cause。旧源、篡改title/key、缺source/EXECUTE、pending/rejected/dryrun/inactive拒绝不新增业务/receipt；合法queue创建重试一工单。最终完整私有race、intake/bootstrap全包race、具名工具race、build和独立复核通过，无skip/race，详见T1。工具编辑原事务与审批/身份变化竞争仍待完成，不把创建检查点扩大为完整授权链；CandidateSHA与未启动状态不变，目标T3/T4/G2/G3未放行。

候选工具授权锁检查点 `63e0c08cd`：042普通迁移提供窄SECURITY DEFINER候选来源锁函数，按session_user binding→active scope→登记FOR SHARE保持至调用者提交/回滚，冻结deployment/tenant/事务设置并检查真实调用；唯一来源查询接入，运行身份保持只读，PUBLIC/default EXECUTE剥离，候选准入需显式EXECUTE，system不扩权。原撤权RED转为有界独立连接测试：pg_blocking_pids证明scope关闭等待写回commit/rollback，随后关闭完成并拒绝下一调用；失败清理先cancel/等待revoker再恢复scope。最终完整私有race、migration/database/bootstrap全包race、build及独立复核通过，无skip/race，详见T1。新迁移依赖与旧退役兼容验证通过；standard非此候选函数保护，binding撤销专项/结果审批竞争/业务首次写仍待完成。B需目标PG17迁移与授权后才可准入，CandidateSHA和未启动状态不变，未放行T3/T4/G2/G3。

结果写回撤权RED检查点 `73c3ce8a4`：真实ToolQueue业务提交后、结果实际UPDATE前，独立连接成功提交scope关闭，ProcessJob仍nil且pending→done。最终race RED确认撤权提交标记、调用整行变化，见T1及s5-tool-revocation-final-red.log。当前测试失败，生产缺陷未修，不能引用此前GREEN关闭此项。下一步在可信数据库窄入口锁住session_user binding与scope至原事务提交/回滚，保持业务身份只读；锁后核验mode/deployment/tenant/scope/登记和事务设置，standard显式binding，固定search_path/剥离PUBLIC和默认ACL，统一与INSERT/审批锁顺序。验证撤权先提交拒绝，以及写事务先持锁时撤权实际等待提交/回滚后生效；新锁下同步hook须改为有界并发测试，不能把超时当撤权成功。CandidateSHA与未启动状态不变，全部交付门禁未放行。

工具完成记录检查点 `da9a5783c`：真实PG重放覆盖首次replayed结果RED→GREEN，done经当前来源/审批身份预检后不再执行；完成/失败写回自有事务复核并按身份/参数/审批和非done条件更新，错误固定。实际post-UPDATE故障回滚调用整行，创建业务已提交，重试一工单恢复done；nil registry失败路径重试固定failed，非未知工具查找验收。回归另发现关闭竞态取消错误遗漏ErrClosed及测试清理挂死，现统一锁内分类保留双cause并保证释放；具名工具race三次、最终完整私有race、build和独立复核通过，无skip/race，证据详见T1。并发成功/失败与审批参数变化竞争、scope撤权栅栏及业务首次写授权仍待完成。CandidateSHA和未启动状态不变，未放行后续门禁。

041业务身份准入检查点 `ace4236d2`：真实PG复现缺SELECT、表/列UPDATE、登记函数EXECUTE均被旧检查放行的四项RED，现要求工具登记表只读并拒绝登记函数直接执行；有效身份与撤回危险授权后均通过。运输身份未放宽，工具来源读取属tenant路径。ScopeRegistration、完整私有恢复/构造回归、database/bootstrap race及build通过，无skip/race，独立复核无阻断，详见T1。fixture不是完整迁移目录顺序验收；目标PG17迁移和显式角色授权仍待B交接。业务原事务与结果写回继续待完成，CandidateSHA与未启动状态不变，交付门禁未放行。

工具审计失败传播检查点 `dcde9bdc3`：未知工具审计错误被吞RED→GREEN；执行前验证参数序列化，未知/拒绝/只读成功和失败均等待审计，保留双cause，审计不可用固定HTTP503且无结果/原错误泄露。真实工具读取后审计INSERT故障双回滚；真实撤销业务SELECT后读取失败但failed审计及登记提交。HTTP、AI/bootstrap race、完整私有回归、全后端build及独立复核通过，无skip/race，证据见T1。查询与审计非同一事务，无重试去重；可选缓存RBAC、首次业务原事务、结果条件回写、041运行角色准入仍待完成。CandidateSHA与未启动状态不变，不放行S5或后续交付门禁。

审批事务检查点 `183fb6d4c`：历史Service拒绝审批仍改旧行的RED→GREEN；通用UpdateToolInvocation删除，唯一Decide事务绑定来源/当前有效审批者与ai:write权限，仅pending CAS决定字段，批准与拒绝记录actor/time，同actor/决定/原因重放首次字段不变，不同决定冲突，提交后enqueue。实际UPDATE后故障回滚、撤权后重放拒绝、同/反决定双UPDATE barrier一成功一冲突再重放保全均真实PG race通过；同决定并发loser可409后重试。独立P2 HTTP全404已修，冲突409/权限403/缺失404/内部500，审批已提交但入队失败明确503，不泄rawcause；HTTP测试、AI/bootstrap race、完整私有回归和build通过，无skip/race，最终复核无阻断，证据见T1。仍需业务首次写、执行结果回写、审计失败传播及041运行角色准入，CandidateSHA与未启动状态不变，全部交付门禁未放行。

AI创建事务检查点 `47b40a726`：真实仓库INSERT缺scope/42501的RED→GREEN；EntRepository显式冻结policy，CreateToolInvocation统一自有事务BindEnt/INSERT/Commit，提交才返回，trigger同事务登记。pending/auto记录形状、实际post-INSERT故障双回滚、nil/异tenant/closed拒绝、明确standard绑定零候选登记均真实PG race验证；AI/bootstrap全包race、完整私有回归、build及独立复核通过，无skip/race，见T1。两记录形状不证明完整ExecuteTool/recordToolAudit/RBAC，standard不证明角色准入；审批更新、审计失败传播、首次业务原事务、结果写回和新表运行权限仍待完成。自有事务方法不用于嵌套调用。CandidateSHA与候选未启动状态不变，不放行交付门禁。

工具入队检查点 `ed4f8c360`：真实Enqueue历史返回nil的RED已转GREEN；入队与执行共用同事务来源/审批/当前actor和approver/ai:write权限预检，执行仍重新检查。锁外30秒生命周期context校验、锁内前后状态核验及in-flight登记，Close取消并等待所有准入退出。pending/rejected/dryrun/inactive actor入队拒绝且原行不变，新调用经实际queue完成并重放一工单；取消后阻塞检查的Close等待、返回取消或忽略取消返回nil两分支均拒绝迟到入队。定向工具/生命周期race、完整私有回归、build及独立增量审阅通过，无skip/race，详见T1。默认隔离事务不是一致快照/审批锁；AI创建审批写、首次业务原事务、结果条件写回、新表角色准入仍待完成，不放行S5/T3/T4/G2/G3。CandidateSHA与未启动状态不变。

工具来源预检 `677fdd6b2`：ToolQueue显式冻结policy，ProcessJob先拒绝上下文冲突/bypass，并在任何invocation写入前独立事务核验041关联、真实调用、tenant、active scope/角色绑定。历史调用直接执行RED已转GREEN，旧行/工单/成员保全；新登记approved调用两次只建一工单并重放。独立P2“SQL故障伪装ErrDenied”已关闭，只有无记录分类拒绝，实际撤销SELECT保留pq42501且非ErrDenied；原事务未提交登记正向和历史负向均验证。定向工具/生命周期回归、完整私有环境race、build及独立复核通过，无skip/race，详见T1。此项仅为预检，未完成首次业务原事务授权、审批/enqueue/结果回写及运行角色新表准入，不关闭S5与全部交付门禁。CandidateSHA及候选未启动状态不变。

工具来源数据库前置 `762bd4f61`：新增041唯一注册迁移，039→040→041依赖及037准备、038旧退休契约保留；新invocation INSERT触发器在原事务登记结构scope/tenant关联，复合FK、绑定/active/tenant校验、固定search_path及触发位置，历史不回填，运行角色只能显式只读。真实私有PG注册缺失RED→历史整行保全/新登记同提交同回滚/未绑定拒绝/直接补登记删除拒绝GREEN，默认角色表和函数ACL剥离、外租户/closed/撤销binding无残留负测race PASS。migration全包及build通过，独立复核无迁移前置阻断。完整私有回归仍FAIL于历史ProcessJob子测试，其余具名项通过，无skip/race，不能报告全通过。角色准入名单与AI/队列/业务事务尚未接入，041不能单独用于启动应用；后续必须关闭来源RED并保留新工具旅程。T1记录全部证据，CandidateSHA及未启动状态不变。

工具来源RED检查点 `674fc9885`：真实私有PG测试确认候选ToolQueue.ProcessJob可执行scope建立前的approved create_ticket。使用受限tenant client与真实candidate intake，返回nil、历史invocation pending→done并写result、WorkItem与member各新增1，四项保全断言FAIL；独立审阅确认缺陷。当前新增测试未GREEN，不能引用此前full PASS称当前范围通过。生产调用链为handlers/ai.Service.ExecuteTool→EntRepository.CreateToolInvocation、ApproveTool→UpdateToolInvocation→Enqueue、ToolQueue.ProcessJob→intake/edit/registry→完成/失败写回；均需贯通来源范围，不能仅靠Start开关。T1保留完整RED证据，CandidateSHA及未启动状态不变。

工具来源事务链实施细化（落实原S5工具入队/执行双边界，不增加独立业务引擎）：

- [ ] 在唯一迁移注册体系追加结构化invocation执行归属及必要同tenant约束/RLS；版本依据当前注册表确定，不修改039/040等历史checksum，不回填历史invocation。创建工具尚无WorkItem，scope关联以真实invocation为来源，不能伪造未来工单身份。
- [ ] 在invocation首次INSERT原事务由冻结policy绑定active scope/角色，写不可变归属；覆盖待审批调用和recordToolAudit，历史调用不能在审批/执行时补登记。明确支持工具合同，未知或未接入写工具拒绝，不能经registry default绕行。
- [ ] 在既有审批所有者的原事务核对来源、当前审批者权限及允许原状态，条件更新处理并发，提交后入队。enqueue读取持久许可，ProcessJob再次核验；内存ToolJob仍只携带InvocationID/TenantID，不自报scope授权。
- [ ] 执行许可须进入创建/编辑所有者首次写入原事务，复用服务器建立的来源身份、稳定invocation operation ID及既有幂等回执；编辑同时验证目标成员。拒绝历史/撤销来源发生在副作用前，不把预检事务结束后的窗口当已关闭。
- [ ] 完成及失败回写绑定当前范围/来源并条件更新，拒绝历史调用保留整行；业务提交后回写故障须用原命令回执恢复。真实新建→审批→入队→执行→回执正向、历史负测、跨租户、关闭scope、撤权/并发及ACK缺口恢复全部验证后才关闭该项。


消费者进程恢复检查点 `2626cd59c`：真实私有Redis与两个独立测试子进程，首进程交付完整信封后不ACK，父进程核验身份/原PEL后强制Kill，原待确认身份保留；第二PID从同组新consumer恢复同一完整信封，PEL归零、Stream整行不变。独立审阅指出Stat先于完整文件写入的同步竞态，已改为等待完整JSON和预期主体并保存快照，复审关闭。完整私有环境race通过，补强后定向race连续3次通过，无skip/race；仅测试/说明变更，详见T1。本项不是完整API/Worker或Redis服务重启，不以fixture收据代替业务事务回执，不放行G3；完整业务旅程、鉴权冷启动、其它异步入口及T3等仍待完成。CandidateSHA和未启动状态不变。

消费推进检查点 `aab8dfcc2`：恢复坏消息之后合法消息必须可交付的原断言，两模式真实RED复现旧Watermill同步NACK阻塞；唯一bus的持久组底层改为有界新消息读取与带游标XAUTOCLAIM交替，NACK保留原PEL，业务ACK才XACK，确认失败可重领。首次真实claim核验Redis6.2+/实际ACL并保留领取结果，修复独立P2的命令存在不代表权限；空页非零游标持续扫描，损坏wire保留诊断且不panic/调用业务/ACK。真实Redis覆盖诊断WRONGTYPE、消费者重建、后续好消息、来源恢复、XACK禁用恢复、持续流量下pending推进、1/21条初始/跨页claim及双活消费者尾部无重复。eventbus/config/bootstrap race、最终完整私有PG/Redis/MinIO回归（含真实审计与两模式Webhook ACK缺口）、后端build和独立复核通过，无skip/race，详见T1最新记录。此检查点关闭下方历史记录中的队头阻塞缺陷，不把诊断当永久隔离；人工处置、实际进程/Redis服务重启、其它异步入口及全部后续门禁仍待完成。CandidateSHA和未启动状态不变，无共享环境操作或push/main合并。

首次拒绝诊断 `d541ed9f3`：两模式typed/candidate拒绝在冻结物理topic/owner独立Redis hash以HSETNX保存固定原因/摘要/首次时间，零原载荷/UUID/error复制，仍NACK；首次事实不阻止重新授权。真实Redis两模式缺记录RED→持久记录/同PEL跨consumer保留/整行不变，WRONGTYPE故障保全且不ACK，fixture同消息授权恢复后ACK/PEL0且首记录不变。相关回归、完整私有环境race、build和独立限定复核通过，无skip/race。重要未完成项：首轮实现测试实际证明watermill同步ResendLoop会让坏消息阻住后续；该失败保留T1，当前没有实现永久隔离或人工处置，不能把删除本轮额外可用性断言当作修复。真实PG撤权联合恢复、Redis服务/应用进程重启及其它异步入口也未完成。此项仅为首次拒绝诊断，不放行S5/B2/B3/T3/T4/G2/G3；CandidateSHA及未启动状态不变。

Webhook两模式统一 `4b7db1035`：普通同步外发分支及无生产调用SendToInstance已删除，普通/候选使用唯一typed消费事务、意图/Audit及原Webhook Worker。普通typed订阅采用稳定owner组并冻结登记身份，复用既有factory/Close；缺authority的登记/动态订阅P2经RED复现后在分配前拒绝，复审关闭。真实PG/Redis普通消费缺Audit RED→同组ACK缺口恢复、完整意图/回执保全、真实Worker两目标各一次published GREEN；完整私有环境race、相关包最终回归、build及独立审阅通过，无skip/race。测试source来自候选新成员的真实SLA事实，standard owner连接证明共享合同而非standard运行role准入，详见T1。代码统一项完成，未知/旧非持久消息持久阻断、进程重启、撤权竞争及其它异步入口未完成，S5和后续门禁未通过；CandidateSHA及未启动状态不变。

普通持久传输前置 `83a9591a3`：普通ExecutionEvent不再丢失身份或生成随机UUID，复用候选的严格信封/authority并固定原eventID。显式ExecutionEnvelopeHandler订阅在两模式校验部署/scope/tenant、UUID/metadata和来源后取得完整信封；candidate准入不放宽。独立P2的缺失稳定合同raw发布经RED复现后在最前拒绝，复审关闭。真实PG源+Redis标准typed传输及源行/Audit保全通过；历史fixture改为明确迁移前非持久类型，旧Stream/组/PEL断言不变。相关三包race、最终完整私有PG/Redis/MinIO race、build及独立审阅通过，无skip/race，初次fixture失败证据保留T1。普通Webhook尚待typed持久接线/删除同步分支及durable组，不能把传输前置视为全链路；S5和后续门禁、CandidateSHA及未启动状态不变。

普通持久来源前置 `536b48e3d`：ExecutionPolicy冻结deploymentID，EventRef按明确模式提供standard空scope或candidate原准入ref；CandidateRef不放宽。原authority共用该身份并保留原事务/持久来源/字节载荷/发生时间核验。真实PG standard正向RED→GREEN，伪造ref与env部署、scope、主体、tenant、缺失来源、未知类型及载荷/时间篡改拒绝，两模式不可互换降级；源行及审计保全。相关四包回归、完整私有PG/Redis/MinIO race、build和独立审阅通过，无skip/race，详见T1。owner测试连接仅证明来源合同，不代表standard应用role准入或候选成员登记适用于standard。普通传输typed身份和Webhook持久接线/删除同步分支尚待完成，不将此检查点算S5完成；固定CandidateSHA、未启动状态及后续门禁不变。

Webhook ACK恢复检查点 `15e6e2c01`：真实PG/Redis联合验证消费意图与Audit提交后阻断ACK、关闭旧bus保留同entry/owner，新bus同itsm:webhook组领取后PEL清空；原Stream entry、完整意图集合及消费Audit不变。后续真实共享Worker向两个loopback目标各一次，published及交付Audit成立，再poll不增加调用，旧Stream/组/pending保全。定向与完整私有PG/Redis/MinIO回归race通过，无skip/race，独立审阅无阻断。仅测试增量，首次即PASS，不虚构生产RED。详细证据见T1；不等于应用强杀/Redis服务重启、所有出站窗口exactly-once或HTTP业务body逐字段验收。普通模式统一、进程重启及其它异步入口仍待完成，S5及后续门禁保持未完成，CandidateSHA与未启动状态不变。

KAF重放修复检查点 `565769236`：已定位并确定性修复下述Webhook阶段记录的偶发失败。私有PG16实际解析`.0010005`为1001μs，旧helper直接从整数纳秒除1000后取偶得到1000μs；改为与PG一致的先解析小数秒、再缩放取整顺序。固定输入在原业务重放处RED，修复后真实KAF完成/重放race及完整私有PG/Redis/MinIO回归PASS，无skip/race；七个PG参考值覆盖半值、普通纳秒与跨秒，并验证1800/2026/2500年。未放宽比较容差、改动请求摘要或修改持久时间。领域/service回归、全后端构建和独立审阅通过，原失败及编译修正记录完整保留于T1。此检查点关闭该已知精度缺口，目标PG17仍待B环境复核；S5其它项与T3/T4/G2/G3不因此通过，CandidateSHA及候选停止状态不变。

Webhook Worker检查点 `be81ef8b4`：已注册共享outbox handler，真实claim/attempt/租约/完整意图摘要/消费Audit身份和意图成员核验，使用Audit原Source重新过authority；捕获精确实例对象和generation，同对象发送，后置重验并写交付Audit，再由原Worker标published。producer从真实对象取得Init冻结URL摘要；builtin冻结endpoint/secret，拒绝自动redirect及非2xx。真实PG+loopback从0发送RED→两个目标published且再poll不发送；独立P1的307→B一次RED已修，redirect/503/发送中重绑/实际回执INSERT故障均unknown不重投，预先换目标零外呼明确blocked。webhook disabled在实际bootstrap保留known reserved类型，配置分支与真实PG pending整行保全均已验证。定向回归、最终完整私有PG/Redis/MinIO race无skip、build及独立审阅通过。首次完整race在Webhook之前KAF replay一次不明失败，随后定向count3及最终full均PASS；根因未确定，完整证据保留T1，不称已修复。S5仍需普通模式统一/移除同步路径、Redis至投递ACK联合恢复、进程重启、未知消息与其它异步入口；generation不是持久配置版本或并发撤权栅栏。CandidateSHA与停止状态不变，全部交付门禁未因本检查点放行。

候选Webhook消费检查点 `e2d29daef`：原subscriber强制policy/client，typed来源在同一RR事务核验active/binding/member/持久来源，逐provider与URL摘要建立既有outbox意图并写唯一消费Audit回执；202/enqueued仅表示入队，零外呼。重放重新授权并核对原意图集合，不随新配置扩展目标。真实PG的typed RED、JSONB重排摘要RED、未知持久字段被忽略RED均已修复；完整JSON规范摘要保留数字精度。实际Outbox/Audit写后故障回滚、首INSERT前确定性双消费者一次成功/一次23505及原请求重试、closed scope/篡改拒绝恢复、整行回执不变通过。完整私有PG/Redis/MinIO回归race无skip，定向回归、全后端build及独立审阅通过，证据见T1。Worker及registry尚未接入，新type仍按未知分发阻断；不能把入队当履约或启动候选。下一worker须验证回执身份、来源摘要及意图成员，并使用Audit保存的原始来源重验，绑定摘要匹配的实际发送实例，贯通claim/attempt/结果回执与delivery_unknown。普通模式同步发送也须迁入同一持久所有者并删除旧路径，不能长期分叉。S5与全部后续门禁未完成，CandidateSHA和停止状态不变。

Webhook前置检查点 `1d391a42b`：真实loopback HTTP复现多实例按名称串投/无目标静默成功/未知事件出站，修复为tenant/name/provider精确实例发送，缺失/撤销不fallback，未知类型和无目标error；新增ContextEventHandler从传输ctx派生超时，拒绝取消/异tenant/SystemBypass。RED→GREEN、精确路由与上下文race、service/connector/.../bootstrap/eventbus回归及全后端build通过，独立复核无新增阻断。仅调用时实例选择与发送前取消保证，尚非并发撤权/配置重绑栅栏，多目标部分成功仍可能被Redis重投；typed candidate owner尚未接入，不能放行。下一步显式注入原冻结policy并在来源/成员核验事务按source eventID＋冻结目标持久化原outbox意图及消费回执；worker复核目标配置摘要/载荷/claim/attempt/范围，未知出站结果保留delivery_unknown，重放不重新枚举目标。完整证据见T1，CandidateSHA及候选停止状态不变。

持久消费者检查点 `5b3ad10c0`：真实Redis离线消息RED后，candidate唯一bus采用冻结logical owner、独立稳定group和随机实例consumer；新组从0读取，已有组保留进度。Close取消并等待建立完成，部分多租户订阅失败统一关闭runtime。该失败经独立审阅发现、双租户RED复现和修复后复审关闭。真实PG/Redis联合验证审计提交后阻断ACK、关闭旧bus仍保留原PEL、新bus领取同entry后清空PEL且完整审计回执/原entry不变，旧裸Stream/组保全。完整候选intake、构造PG/Redis/MinIO保全及新增生命周期race、六包回归与全后端build通过，无skip；配置默认及限制见实现分支开发指南，完整证据见T1最新交接。只证明至少一次传输加审计幂等，不证明整个应用/OS重启或outbox Worker当前claim。Webhook typed owner、无效消息持久阻断和其它请求异步仍待接入；下面复合项及S5/G2/G3保持未勾选。固定CandidateSHA与未启动状态不变，无共享环境变更。

审计检查点 `7507ca616`：原owner显式ExecutionPolicy与typed Envelope，RR原事务ValidateEventTx准入/持久来源、唯一AuditLog事件回执、完整digest/body/元数据校验后重放；不虚构WorkItem版本。修复原空白上下文RLS错误及真实重复审计RED。真实PG插入后故障回滚、双INSERT barrier一次提交/一次PQ23505、原消息重放整行保全、变造和closed scope拒绝通过；完整候选边界/Redis路由/应用构造保全、定向回归、build及race通过，独立审阅无阻断，证据见T1。bus透传消费context与完整typed Envelope；standard保持map，Webhook尚未适配而明确拒绝。Redis durable group、ACK丢失恢复、无效消息持久阻断和联合消费尚未完成，S5及启动门禁保持未勾选。

来源检查点 `d8e3d0420`：candidate bus必需narrow authority，typed持久eventID/WorkItem生成outer execution，复用eventID为消息UUID；数据库authority核对冻结ref、active/binding/member及同tenant/结构主体/ID的持久Outbox，复用唯一SLA factory验证内容/发生时间。subscriber在handler前验证严格信封、物理route、UUID/metadata及来源；未知/无主体事件明确拒绝。大小写JSON别名P2已修并复审关闭。真实PG来源负测与scope/binding变化恢复通过，Outbox整行/audit数保全；受控channel验证ACK/NACK及拒绝零handler。完整私有候选边界/真实Redis路由/应用构造保全、定向回归和build通过，无skip，证据见T1。PG用capture bus、Redis用fixture authority，尚非联合消费；审计自身事务幂等、持久group/无效消息阻断/重启恢复及直接owner边界仍待完成。S5及candidate启动门禁不因此放行。

路由检查点 `ea8c2494a`：唯一Watermill构造器必需显式ExecutionConfig并冻结复制refs；candidate Publish/Subscribe统一可信配置namespace，拒绝非法topic/非规范或清单外tenant、无稳定事件和重复scope，不从payload自报scope取路由。原传输RED已GREEN：真实Redis双租户各有独立订阅/唯一消息，载荷核验及旧Stream/组/逐条PEL保全通过；完整应用构造在私有PG/Redis/MinIO保全通过，无skip。eventbus/bootstrap包回归、全后端build和独立审阅通过，详情见T1最新交接。仅传输路由完成，尚无authority/持久eventID/严格信封/审计原事务幂等或可靠消费组恢复，不构成candidate启动许可，下面复合项保持未勾选。

传输RED检查点 `c94cdfba2`：新真实Redis测试TestCandidateStreamPreservesLegacyTopicOnPublish经过现有Watermill构造/订阅/发布，新事件handler正向控制成功，但旧Stream新增、旧group lag增加、预期candidate namespace为空；历史pending逐条ID/consumer/delivery count保持，不声称旧pending消费。测试私有随机密码/PID核验的Redis7.2.16，无共享操作。独立审阅确认RED有效，补强PEL后复跑仍预期FAIL、无skip；当前该测试未GREEN。完整证据见T1最新交接。

下一实现沿用唯一Watermill bus：bootstrap注入冻结策略及明确订阅合同，所有Publish/Subscribe共用可信namespace解析；服务提供已验证WorkItem主体，subscriber验证namespace/envelope/tenant/member后交给原审计所有者，审计在自身事务重新验证并幂等。已盘点两个业务发布者：SLA durable outbox与建单前AI分诊；后者空TicketID不能伪造主体或从payload自报scope获授权。不得双发旧topic或创建平行candidate bus。新namespace载荷唯一性、真实审计、成员撤销、未知事件和退出验证仍需完成；S5及后续门禁保持未勾选，CandidateSHA及未启动状态不变。

**Files:** `pkg/eventbus/{eventbus.go,eventbus_test.go}`、`service/{tool_queue.go,ticket_service.go}`、`controller/connector_controller.go`、bootstrap；事件发布者由 `rg -n 'Publish\('` 生成调用清单逐项接入。

- [ ] 写 `TestCandidateStreamDoesNotConsumeHistory`：旧 topic 放历史消息、记 XINFO GROUPS，新 scope 发布并订阅一次，断言旧 topic 消息/组完全不变，新消息能产生一次声明的审计。RED 后再实现。
- [ ] 新传输 topic 固定为 `candidate:<deployment>:<scope>:<stable-topic>`，校验各分量，消费者仅订阅该 namespace。Envelope 增加可验证 scope 与 WorkItem 主体；生产者来源必须是服务已验证对象。不得双发旧 topic，不从 payload 自报归属赋权。
- [ ] 订阅 handler 前验证 namespace、tenant、成员一致；未知/非规范事件显式失败，新独立诊断可写，不能 ACK 成功。context 取消关闭 subscriber 并等待消费退出。
- [ ] tool enqueue/执行两处校验，历史 GET 外部副作用在 owning service 阻断；embedding、云发现、导入导出、历史连接器实例启动保持 disabled。覆盖直接服务调用，不能只关闭按钮。
- [ ] 运行 `go test ./pkg/eventbus -count=1` 与真实 Redis Stream/请求副作用测试 GREEN；提交 `fix: isolate candidate events and asynchronous effects`。

## S6：历史保全验收与独立交接

**Files:** `tests/integration/candidate_runtime_preservation_test.go`、`docs/DEVELOPMENT_GUIDE.md`、AGENTS.md/CLAUDE.md；A 的 T1 交接追加具名 B2 段，不修改 B 文件。

- [ ] 建立固定历史 ID 清单，对原历史业务行、定义、关系、队列、审计、vector 与 stream 逐行摘要。新增行和编号 sequence 前进单列，禁止通过整体 count 替代历史内容检查。
- [ ] 同一候选 scope 完成 generic/Incident/Problem/Change/Requested Item 的新建、原有流程/通知/回调及 SLA 周期；全周期和重启后再次对账。外部实发禁止，测试接收端明确区分。
- [ ] 运行 `go build ./...`、`go test ./migration ./internal/bootstrap ./common/executionscope ./pkg/eventbus -count=1` 和上述具名真实测试；真实测试配置缺失出现 skip 时门禁不通过。仅列实际结果，不把计划断言当验证证据。
- [ ] 同步架构摘要的执行许可与生命周期合同、开发指南的配置/显式准备/退出步骤，写出所有 disabled 与 G2 缺口。
- [ ] 请求未参与实现的独立 reviewer 复核迁移、租户、pre-claim、各生产者和历史断言。关闭全部 Critical/Important，再记录 B2 代码 SHA/配置合同/迁移指纹/证据/未验证项，提交 `docs: hand off candidate execution scope verification`。
- [ ] 后续进入[鉴权计划](2026-09-12-itsm-candidate-auth-state.md)；B2 单独通过不放行 T3，最终集成同时重核040后的迁移规划。不得销毁任务测试资产或已有 worktree。

## 自审覆盖

设计§1/2→S1/S3；§3→S2；§4→S3/S4/S5；§5→S4/S6；§6/7→S6及鉴权计划A5。成员不回填、构造无副作用、全部领取更新受限、旧Stream不消费、API服务双边界、完整周期保全均有独立断言。未有结构化归属的能力保持禁用，若为G2必需则交付阻塞，不把范围缩减为验收通过。
