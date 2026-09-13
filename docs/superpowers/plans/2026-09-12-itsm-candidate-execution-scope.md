# Candidate Execution Scope Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use executing-plans to implement this plan task-by-task. Do not spawn extra implementation agents. Steps use checkbox (`- [ ]`) syntax for tracking.

状态：accepted（实施中；S1/S2 已提交并完成该阶段验证，S3 基础增量已实现并验证，业务接入进行中；S4–S6 未开始）。

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

## S4：队列原子领取、恢复及周期执行

**Files:** `service/{outbox_event_repository.go,outbox_delivery_worker.go,kaf_outbox_dispatcher.go,bpmn_callback_outbox.go,ticket_notification_service.go,sla_monitor_service.go,escalation_service.go}`、bootstrap 注入点及对应现有测试。

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

## S5：Stream 与请求异步边界

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
