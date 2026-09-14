# ITSM 候选交付剩余任务与里程碑

> **For agentic workers:** 使用 executing-plans 按本清单逐个里程碑执行。旧实施计划提供技术步骤，本文件是唯一剩余范围、优先级及完成状态入口；不得从旧的未勾选复合项自动重新立项。

状态：accepted（维护者 2026-09-14 指示停止无上限补点、收口剩余任务）。

**Goal:** 以上线为目标，优先交付核心功能完整、现有架构可运行、数据与权限可靠、可部署和恢复的版本。先在 WSL 候选环境通过核心业务 G1/G2/G3，形成可发布代码和运行交接；飞书同步不在本轮必交功能内。

**Architecture:** 保留既有业务所有者、数据库迁移、队列和连接器边界；本文件只收口交付，不新增架构或降低安全门禁。

**Tech Stack:** Go/Gin/Ent、Next.js、PostgreSQL、Redis、MinIO；A 在 Windows/WSL 独立 worktree，B 负责环境。

## 1. 权威与范围冻结

- **2026-09-14 最新维护者范围修订：**“飞书同步不用考虑；更多关注功能、架构可用，以上线为目标”。本条明确取代旧 R2-F 的飞书同步功能与完整投递协议必交要求，也取代下文历史检查点中“继续飞书收口”和对应3–6小时估算；不是将缺陷标为已修复。
- 停止飞书创建/更新同步、claim/回执、manual/inbound 功能完善。已提交修复保留。本轮仅核实未启用飞书的部署边界，确保缺失飞书配置不会阻断核心工单旅程，不清空历史飞书任务、不关闭共享outbox或必需worker。
- 上线优先级：核心工单及专业业务闭环、BPMN/审批/任务/SLA、权限与并发、附件和必要通知、部署/备份恢复/稳定运行。只处理上述旅程的可复现阻断，不重构无业务阻断的架构，也不新增通用平台。
- 生产发布就绪纳入交付目标；实际生产切换需要明确目标环境、数据范围及发布窗口，由B执行环境操作。目前没有这些生产目标事实，不能把WSL候选验收报告成生产上线。原“不合并main、A不操作共享库/B配置”约束继续有效。

- 原[交付设计](../specs/2026-09-12-itsm-candidate-integration-delivery-design.md)和 AGENTS.md 继续定义产品、安全与验收合同。
- 本文件取代[双 Agent 总计划](2026-09-12-itsm-candidate-two-agent-delivery.md)、[执行范围计划](2026-09-12-itsm-candidate-execution-scope.md)、[鉴权计划](2026-09-12-itsm-candidate-auth-state.md)作为“接下来做什么”的入口；不撤销其已批准技术合同。旧勾选与检查点保留为历史证据，不再维护第二份剩余清单。
- 已完成的实现不重写，已通过的验证按代码指纹复用。未勾选复合项不等于内部所有步骤未实现。
- 不推送、不合并 main、不清理已有 worktree，不执行 R(038)、历史回填或企业实发。A 不修改 B 配置或执行 WSL/共享数据库变更。
- 不能以禁用 G2 必需消费者、删除副作用、临时 SQL 或补历史授权换取通过。可选入口若未安全接入，必须在真实入口明确拒绝且有保全证据；仅关闭 UI 无效。
- 新发现必须关联下列 R 编号及具体失败的交付断言。没有关联的改进进入 D 类，不自动编码。若需要新子系统、扩大功能或改变原合同，先提供影响与选择，暂停该项扩展；其他独立项可继续。

## 2. 已有成果：复用，不重新立项

2026-09-14 核对：实现 worktree `itsm-candidate-execution-scope` 在 `3098f9718`，工作区干净。固定 CandidateSHA 仍为 `d7470a32dbb87acc9b5e4d9a895a146410723561`，后续修复尚未整体交接为新候选。候选应用未启动。

| 已有结果 | 证据范围及限制 |
| --- | --- |
| WorkItem 与主题初次集成 | 保留原来源历史及 T1 证据；修复集成后的 G1 仍需复核 |
| S1 范围登记、S2 显式生命周期 | 已有实现与私有角色/构造保全测试，不重新开发 |
| 工单编辑、手动/BPMN 升级、队列范围、工具与 Stream/Webhook 多个检查点 | 已有事务、幂等、组件及私有基础设施证据；只补对应剩余断言，不把旧复合勾选当全量缺失 |
| 邮件目标合同六项 | 原事务固定目标、SMTP/Graph、候选角色职责链、禁用保全已验证；不是企业邮件履约 |
| 最近验证 | `s5-graph-activation-final-private-corrected.log` 私有 PG16/Redis/MinIO 规定测试集 race PASS；`s5-graph-activation-final-build.log` 后端构建通过；不等于所有测试或 WSL PG17 通过 |

证据细节继续保存在实现分支 `docs/review/2026-09-12-candidate-t1-handoff.md`，这里不复制全部过程。上一轮口头状态把“工单编辑”等整块列为剩余过于粗糙：其主体已有实现，剩余为组合验收及明确缺口。

## 3. 唯一剩余任务清单（交付必需）

仅以下 9 项可驱动本轮交付工作。状态为未关闭，不能从项目数量推算完成百分比。

| ID / 状态 | 单一交付物及责任 | 完成判据 | 原合同映射 |
| --- | --- | --- | --- |
| R1 / 完成本轮盘点（第7节） | A：锁定候选实际入口与现有证据差额表 | 逐项对应真实 HTTP/service/worker/工具/connector 入口、所属旅程、启用或明确拒绝、已有 SHA/日志、唯一缺口编号；专业编辑、请求人/表单、工具审批、裸实例入口不得只靠搜索结果判断。没有缺口的项直接复用，不重测。 | S3–S5、G2 启动前边界 |
| R2 / 部分完成 | A：业务与异步入口的最小安全收口 | 只修R1证明影响本轮核心功能、成员/租户/当前权限、事务或历史保全的可达入口。BPMN已验证修复复用；飞书同步转D1，仅保留部署未启用边界核实。核心入口正向可用，拒绝路径无业务副作用；不再以飞书完整协议阻塞上线准备。 | S3、S4、S5 核心业务/裸实例/必需消费者授权 |
| R3 / 待整体验收 | A：本机候选执行隔离验收包 | 复用既有测试集，补尚缺的历史逐行/关系/队列/审计/对象/Stream 对账、获准新业务、实际相关周期、取消/恢复及进程重启。区分本机测试与目标环境证据；skip 不能过门禁。只在改动影响范围内回归，里程碑结束做一次集成验证与独立审阅。 | S6、B2 |
| R4 / 待完成核验 | A：鉴权持久状态代码与私有故障证据 | 按既有鉴权计划核查并完成唯一 PG authority、规范 token、撤销与单次消费、受限角色、启动/注销/刷新失败关闭；两实例与 PG/API 重启、Redis 丢失、旧快照恢复换 authority/密钥，新登录正向。先确认已有代码，禁止仅因旧框未勾选重做。 | 鉴权 A1–A4、B3、原设计第8节 |
| R5 / 待 R2–R4 | A：已审阅的固定候选与 T1 新交接包 | 纳入已审阅修复，核对 bootstrap/迁移依赖与原 WorkItem/主题，完成受影响后端和前端 G1 检查；固定 CandidateSHA、构建/迁移指纹、配置合同、日志和未验证项，校验 bundle。代码 SHA 与文档 SHA 分开。 | 鉴权 A5、T1/G1 |
| R6 / 等待 B 交接 | B 执行、A 复核：T2 差额关闭及 T3 EnvironmentRevision | 固定新 SHA 上核对真实源与写入者、受保护备份/附件、目标 PG17/资源和受限角色；完整迁移语义在执行前准入，恢复/迁移后历史保全、出站阻断与候选入口。明确备份窗口；A 不代执行。只有完整 EnvironmentRevision 才放行 R7。 | T2/T3、B4/B6、G2 环境 |
| R7 / 待 R5/R6 | A 业务验收、B 监测：目标环境 G2 | 在同一已交接版本执行 Incident、Problem、Change、generic/Requested Item、真实 BPMN/Worker/SLA、版本冲突/权限/附件/通知，以及双主题真实浏览器旅程；受限身份正向和负向，启动/周期后历史对账。失败只回到对应 R 项，不新开无关波次。 | T4/G2 |
| R8 / 待 R7 | B 执行、A 复核：稳定运行及恢复交接 | 固定构建受控重启，关键旅程和消费者恢复；连续至少60分钟低频只读观察，保全人工新增数据，给出固定 URL/启停/资源/恢复说明及限制。 | T5/G3 |
| R9 / 待 R8 | 维护者验收、A 汇总：最终关闭 | 维护者完成代表性页面/业务旅程；G1/G2/G3 全部有证据，无未解释的阻断问题；分别报告代码、私有验证、候选运行、main、企业外发、生产部署状态。 | 原设计第7–9节 |

### 实施入口（只引用既有技术步骤）

- R1/R2：执行范围计划 S3–S5；生产入口在 `itsm-backend/service/`、`controller/`、`internal/bootstrap/app.go`、`connector/`，前端编辑调用在 `itsm-frontend/src/`。飞书完整协议开发已移出本轮；只读核实未启用部署边界和核心功能不受影响。不得另建通用路由或候选专用业务服务。
- R3：执行范围计划 S6；跨域验证落在 `itsm-backend/tests/integration/`，沿用任务私有 PG/Redis/MinIO 和既有日志目录。
- R4：鉴权计划 A1–A4 所列 authentication/middleware/bootstrap/migration 及真实故障用例；不在本清单重新设计接口。
- R5–R9：原 T1/T3/T4/T5 技术计划和 bundle/EnvironmentRevision 交接协议继续有效。

## 4. 可延后清单与不可豁免条件

| ID | 本轮处理 | 延后条件/边界 |
| --- | --- | --- |
| D1 | 飞书同步全部功能与完整协议；企业真实邮件、VPN、云资源写入 | 维护者最新范围修订明确排除飞书同步，停止原R2-F继续开发，部署保持未启用；不动历史任务。其他企业出站仍按原约束阻断，核心旅程所需通知保留。 |
| D2 | 未声明的 connector polling、embedding、云发现、批量导入导出、非必需工具能力的功能完善 | 保持明确 disabled/拒绝；R1 必须证明后端/直接服务/启动入口无副作用。若原 G2 旅程需要它，则回到对应 R 项，不能静默删旅程。 |
| D3 | 全平台所有 provider/所有运输的任意并发排列与高可用扩展 | 本轮只做实际候选启用路径及已知失败窗口；已发现可达的跨租户、错目标、重复副作用、撤权问题必须在 R2/R4 处理。 |
| D4 | 通用在途流程退役 I2、R(038)、物理清理、历史回填、多 WorkOrder、首响新口径、基础设施整合 | 继续保持原设计排除，不为“顺便完善”加入本轮。 |
| D5 | 无关旧代码重构、迁移既有测试目录、历史文档大规模整理、额外界面打磨 | 不影响原 WorkItem/主题合同及 G1/G2 的部分延后。集成回归和可用性阻断仍必修。 |
| D6 | main合并、新平台功能、长期运维/非必要性能优化 | 继续延后；生产发布就绪已按最新指示纳入目标。真实生产切换待明确目标/数据/窗口，由B执行，不以本表推定main合并或共享库操作授权。 |

除第1节维护者明确取消的飞书同步必交要求外，其他延后不改变核心安全设计；不能仅靠改名为backlog取消核心功能或安全要求。

## 5. 里程碑与停止扩张规则

| 里程碑 | 覆盖 | 退出物 | 下一步 |
| --- | --- | --- | --- |
| M0 范围收口 | 本文件及入口取代关系 | 单一清单、责任、必需/延后、证据引用 | 只开始 R1，不同时启动新实现 |
| M1 执行隔离关闭 | R1–R3 | 入口差额清零，B2 私有验收与独立审阅 | 进入 M2 |
| M2 固定可交接代码 | R4–R5 | 鉴权故障证据、G1、新 CandidateSHA/bundle | 交给 B |
| M3 候选业务验收 | R6–R7 | T3 EnvironmentRevision、真实 G2 | 进入 M4 |
| M4 稳定交付 | R8–R9 | G3、维护者验收、稳定访问/恢复入口 | 关闭完整目标 |

执行约束：

1. A 同时只实现一个 R 项；B 可独立准备只读准入与交接，环境实际执行沿原授权/窗口。不得让多个 agent 同时编辑相同文件。
2. R1 限一轮定向盘点，不做全仓库无限审计。输出路径、现有证据和精确缺口后结束；未知项保留“待核验”，不自动变成改造项目。
3. 每个执行批次最多60分钟，到点即报告关闭的断言、剩余差额、耗时及是否扩大范围。到点不强行判绿，也不无声开启下一轮修复。
4. 同一失败连续两轮未减少差额时，停止盲目重跑，说明根因/选择及预计影响。无法给可信估算时明确说明，不承诺“马上完成”。
5. 每个里程碑一次范围内集成验证和独立完成审阅；中间只跑改动直接影响的测试。新失败、修复或未解风险才扩大/重复验证。
6. 复审只以对应 R 的验收断言和原约束判阻断；无关建议进入 D 类。不得将每条建议追加成新前置波次。
7. 状态只在本文件更新为“部分完成/验证中/完成/等待具体交接”；完成必须附提交和日志引用。历史交接只保存证据，不再生成另一套待办。
8. 下一轮 R1 结束才给 M1 工期区间；M2 后按 B 的实际窗口估算 M3/M4。现在没有可靠的整体剩余小时数，60分钟观察也不是全部剩余工时。

## 6. 本次收口完成记录

- [x] 冻结9项交付任务、6类延后工作及5个里程碑，不改变原 G1/G2/G3。
- [x] 区分“已有主体实现”与“复合验收未关闭”，不按旧复选框重做。
- [x] 在原设计、双 Agent 总计划、执行范围及鉴权计划添加唯一入口链接。
- [x] 独立 reviewer `review_execution_scope_s1` 完成有界复核：G1/G2/G3 与鉴权合同均映射到 R1–R9，未发现阻断或暗中豁免；已修正原设计顶部旧状态入口，避免双重权威。M0 完成。

本次仅文档收口，不启动 Go 测试/构建、候选应用或 WSL 操作；不产生新 CandidateSHA。下一执行批次仅 R1，必须先交付差额表，再进入 R2 编码。

## 7. Windows 接任 R1 定向盘点（2026-09-14）

R1 状态：本轮入口盘点已完成；安全缺口未关闭，M1 不通过。代码基线为 `3098f9718b98fbcd2fa4fc422a8496c14a141d7f`，下表路径均相对此代码 worktree。这里记录源码追踪与既有证据，不声称重新执行了 Mac 测试或目标环境验收。

### 7.1 入口与证据差额

既有证据 E1：同一代码基线中的 `docs/review/2026-09-12-candidate-t1-handoff.md`，最近记录的 `s5-graph-activation-final-private-corrected.log`（私有 PG16/Redis/MinIO race），以及各行注明的测试源码。Mac 原始日志未随远端分支落到本机，故仅复用已记录结果，不将其标注为本机复跑。需要新的组合验证统一归 R3，不重复开项目。

| 实际入口 / 所属旅程 | 源码追踪和启用/拒绝结论 | 已有证据 / 唯一差额 |
| --- | --- | --- |
| generic 创建、关系/子任务，HTTP/intake | 原 creation transaction 登记成员，父/关联对象检查；构造及登记机制复用 | E1；candidate_intake_boundary_test 的 new base extension/member、historical parent/relation、rollback 子测试。无新增实现项，组合证据 R3 |
| generic 编辑/子任务编辑 → TicketService.UpdateTicket | service/ticket_service.go:409 原事务当前权限、BindEnt/RequireEntMembers、实际父项、版本和业务回执；专业字段禁止走 generic | E1 工单编辑章节及 ticket edit receipt/Feishu intent commit together。复用；发送侧另见 R2-F |
| 请求人/表单编辑 → 同一 UpdateTicket | DTO 包含 requesterId/formFields，但 service 明确报 owning command required（非 UI 隐藏、非静默忽略），在更新之前返回 | 源码 ticket_service.go:484；不新增请求人/表单编辑功能。R3 核对拒绝证据与 G2 必需旅程，不能误报该功能可用 |
| Problem 元数据 → handlers/problem/handler.go applyMetadata → ApplyMetadata | 当前命令授权、版本/replay，requireExecutionTx 在 Ticket/Problem 更新前，仍由专业所有者处理 | E1 Problem writes preserve historical work；metadata.go 实际事务已读。复用 |
| Change 元数据/分派 → command_handler.go UpdateChange/AssignChange → ApplyMetadata | 当前授权、已评估事实锁定、callback settled、版本及 requireExecutionTx 在写之前 | E1 Change/PIR historical preservation；metadata.go 实际事务已读。复用 |
| Incident 命令/metadata/规则/CI/alert | 保留专业命令及 Incident v2 持久来源、当前 actor 和 claim 检查 | E1 最后 29 项 private PG 故障证据及对应 candidate_intake 子测试；不重做主体，R3 组合验收 |
| Requested Item/履约/BPMN callback | handlers/service_request/execution_scope.go 原事务成员检查；已建基表/扩展原子创建与回滚覆盖 | E1 Requested Item writes preserve history / extension failure，KAF access completion。复用，R3 |
| 工具审批 HTTP /agent/tools/:id/approve → AI Service → EntRepository.DecideToolInvocation | 仓库方法在 RepeatableRead 内 BindEnt、RequireEntToolInvocation、active approver/current ai:write、CAS；批准后才 Enqueue。直接执行另经 ToolQueue approved source/actor/outcome tx | repository_impl.go:215 已读；E1 AI approval preserves historical invocation、工具撤权/并发结果私有测试。复用 |
| 裸实例 HTTP BPMNWorkflowController.StartProcess → StartProcessTx/startResolvedProcess；实例管理 service | HTTP 传 businessType 空、businessID=0；validateWorkItemStartTiming 对合法独立 business key 返回 nil；startResolvedProcess 随后可创建无 execution_work_item_id 实例。actor snapshot 仅检查隔离级别。实例 update access 检查 tenant/permission，尚未证明成员限制 | **R2-B**：需最小 RED 验证候选裸实例启动/管理历史实例的真实写前拒绝；不能以 callback worker 的过滤代表整个入口已封闭。未测行为不标为已复现 |
| 必需 Outbox/Callback/Notification、KAF/SLA/升级 | worker 原事务成员谓词、claim/recovery 和当前绑定；CallbackPredicate 从实例 execution_work_item_id 关联；通知已收敛 enqueue | E1 real workers preserve historical states、lease/audit fault、SLA/escalation cycles、notification 6 contracts。复用；R3 只补组合/恢复差额 |
| Feishu manual HTTP /feishu/sync/ticket/:ticket_id → SyncTicketToFeishu | 当前 actor/专业权限已有；无 execution policy。已有 mapping 分支 tx.Commit 后直接 UpdateTask，随后另写 mapping；未经过固定 claim/outbox | **R2-F**：同一飞书合同缺口，需原事务持久意图+成员/当前权限/顺序/回执；不得停掉必需发送路径假通过 |
| Feishu creation outbox → FeishuCreationDeliveryHandler.Deliver | 直接信任传入 event.Payload；无持久 row claim/lease/attempt 与成员核验；provider 只按 tenant/Destination 查找，发送后另写 mapping | **R2-F**：复用已有 origin/intake receipt/GUID，补固定 claim、完整持久目标、同实例、发送后不确定结果禁止重发 |
| Feishu update outbox → FeishuUpdateDeliveryHandler | 已有 stored claim/lease/attempt/ordered head、payload digest、成员、actor、audit receipt、mapping 和 post-send 再查；不能列为全未实现。provider target 仍只有 Destination，无完整固定实例声明 | **R2-F**：只补持久目标/同实例/重绑窗口。E1 manual escalation persists Feishu update intent、ticket edit receipt/intent tests 使用本地 mock updater，不能代替真实 Feishu client 协议 |
| Feishu connector Init/TaskDestinationIdentity/HTTP client | Init 持有 cfg map，身份读取 cfg.app_id，实际 client 捕获 appID；client 使用默认 redirect policy。实际 Manifest 未声明 InitializationLocalOnly，候选 trusted manager 的准入拒绝不能被 mock 的声明替代 | **R2-F**：真实本地接收端、不可变配置、重 Init/重绑/redirect 的已批准目标合同；当前实际 connector 正向准入仍未关闭 |
| Feishu webhook/直接 inbound service → HandleTaskEvent / SyncFeishuTaskToTicket / markTaskDeleted | 现有 inbound creation 走 creation owner；已有 mapping 更新和 task.deleted 仍直接改 Ticket/mapping，无成员策略；HandleTaskEvent 在取外部 Task 前没有该策略 | **R2-F**：同入口范围保全。先证明真实 candidate manager/直接 service 可达和拒绝/所需正向，不扩大成企业接入平台 |
| Webhook/Stream/EventAudit | 已有固定目标、订阅权限、持久命名空间与进程恢复测试；不读写旧 stream 作为消费源 | E1，candidate_stream_preservation_test.go 与 candidate_stream_process_test.go。复用；R3 整体对账 |
| 可选 cloud discovery/runner、connector restore/management/probe、embedding/poll/import-export | frozen policy 对后四类只允许 disabled；cloud/direct runner、restore/management 有拒绝保全测试。不能用 capability 枚举单独证明全部直接入口 | E1 candidate cloud direct entry disabled、connector restore rejects persisted activation、read routes do not probe 等。**R3-V**：尚未逐项证实的直接入口保留待核验，不自动新开实现；出现具体违反断言才回 R2 |
| Auth logout/refresh/token | 未在本轮发现 auth_state_authorities/auth_token_states 迁移；不据名称搜索宣称已完成或全未实现 | **R4** 专项核验唯一 PG authority/规范 token/失败关闭，R1 不实施鉴权 |

### 7.2 批次结果与下一步

- 已验证 origin 为 `git@github.com:Joes9527/itsm.git`，fetch 后 design=`5c968fa5bb0d0a4bf58841380532a5ea407cc169`、scope=`3098f9718b98fbcd2fa4fc422a8496c14a141d7f`、integration=`d62420b6cddbb37cb0fa6d1ec739e959689d2a7e`。用户最新推送事实替代 handoff 中 ZIP/bundle 和未推送历史步骤。
- 实现工作区 `/home/administrator/project/itsm/.worktrees/candidate-a-windows-delivery`，分支 `codex/fix/candidate-a-windows-delivery`；唯一计划工作区 `/home/administrator/project/itsm/.worktrees/candidate-a-remaining-delivery`，分支 `codex/docs/candidate-a-remaining-delivery`。
- 原 main=`a25e108d2a08a55469fa5ad547aac5a9adc251ff`，原未跟踪 `docs/poc/` 保留；未覆盖已有 worktree，未修改 B 配置或访问共享数据库，未启动候选服务。
- R1 没有运行 Go 测试/构建；“已完成”仅表示本轮定向盘点和差额落表。R2 的最小实现集中于 R2-F 与 R2-B，未知项 R3-V 不自动扩展范围。
- M1 尚无法给可信完成小时数：飞书仍有跨 producer/transport/receipt 的实质差额，先用下一批不超过60分钟的 RED/最小修复核实成本；这不是新的无限审计或完成承诺。下一项仅 R2，R3/R4 不并行编码。
- R6–R9 继续等待 B 的新版 T3 EnvironmentRevision、后续 G3 与维护者验收；旧 CandidateSHA 及旧 T2 观察不构成候选启动/验收准入。

### 7.3 R2-B 已验证批次（2026-09-14 09:42 CST）

代码提交：`437689af3861a3d0da46b40a547256943bbd3467`，基于本节 R1 基线；实现 worktree 干净。本地实施记录 09:00–09:42，约42分钟，包含首次 race 编译；本批只推进 R1/R2，没有扩大到 R3/R4 或环境交付。

实际关闭的断言：
- `startResolvedProcess` 在原事务检查候选成员，拒绝独立实例及历史 WorkItem 启动；获准新成员正向仍成功。
- 暂停、恢复、终止、实例变量修改通过同一原事务检查不可变 `execution_work_item_id`；不登记历史实例。
- `authorizeTaskCommandActorWithClient` 在公共任务授权边界验证实例成员，覆盖该边界下的任务命令；实测认领、变量修改、取消、完成的历史/独立拒绝和成员正向。既有角色、专业授权、CAS、审批回执及 KAF 逻辑保留。
- BPMN HTTP 对范围拒绝（含包装错误）返回403；数据库故障保留500，不将基础设施失败伪装为权限拒绝。

证据目录：`/home/administrator/.local/state/itsm-candidate-delivery/agent-a-windows/`。所有下列 PASS 均在本机实际运行，未使用 Mac 日志替代：
- RED：`r2-b-start-red.log` 复现候选裸实例新增实例/审计；`r2-b-instance-red-corrected.log` 复现4个实例管理入口写入；`r2-b-member-start-red.log` 复现历史 generic 启动；`r2-b-task-red.log` 复现8个历史/独立任务入口成功写入；`r2-b-http-red.log` 复现范围拒绝500。第一次实例 RED 的 fixture 缺 activity_id 与 time.Time 表示差异已修正，不作为成功复现证据。
- `r2-b-final-race.log`：service/controller 受影响启动、实例生命周期、任务生命周期、变量及HTTP错误映射测试，`-race -count=1` PASS，无 skip/race。
- `r2-b-final-private-race.log`：`candidate_scope` 下新增26个实例/启动/任务用例，以及已有 `KAF access completion uses candidate transaction`、`real callback worker preserves historical states`，`-race -count=1` PASS。拒绝路径比较所有 public 表完整 SQL 行、队列/审计载荷及 schema/sequence 元数据；不是 Ent JSON 比较。
- `r2-b-build.log`：`go build -p 1 ./...`，退出0（空日志）。

私有依赖：新建任务容器 `itsm-agent-a-pg-20260914-0918`，镜像本机 `postgres:16` 实测 PostgreSQL16.15，网络模式 none、PGDATA tmpfs，唯一主机挂载 `/tmp/itsm-agent-a-pg-20260914-0918` → `/candidate-socket`，port25439，测试角色 `candidate_test_owner`；测试自行创建/删除随机数据库及角色。未连接B共享源。初始化两次因镜像脚本清空PGHOST而失败，经读取镜像 entrypoint 确认后同时保留容器内默认与任务专用socket解决；未对共享服务重试。

本批未关闭：R2-F 全部持久目标/同实例/claim及回执差额；R3 整体周期、恢复和独立里程碑审阅；R4 鉴权；R5 新候选集成；R6–R9 环境与真实验收。R2-B 上述已验证入口不重新立项；未覆盖的配置编辑/事件入口或任意并发排列不因本批自动扩展，只有原交付断言的可达失败才能回R2。没有生成新 CandidateSHA、没有推送或合并main。下一实施仍是 R2，转向 R2-F；M1 总工时仍不足以可靠估算。

### 7.4 R2-F 目标组件进展（2026-09-14 09:53 CST）

中间代码提交：`fa0820704d6eff6035452e9c6522ad9dd48b3d70`；仍不是 CandidateSHA，R2-F 未关闭。

- 真实 Feishu client 的 RED 已证明：调用方 map 修改可改变 destination/callback 标识、重复 Init 可重绑同一实例、token/task HTTP307会转发到第二个本地接收端。
- 复用既有 `DeliveryDestinationDescriber` / `DeliveryDestination` / Manager 声明与激活协议；Feishu Init 与描述共享纯解析，捕获 app/baseURL/callback 身份，拒绝重复初始化，HTTP不跟随重定向且非2xx显式报错。未另建路由或候选业务服务。
- 目标摘要采用 `feishu-task-v2` 路由身份，不含密钥；旧意图没有被转换或重写。接下来的持久payload/原producer必须完整固定目标，不能把本提交的字符串摘要等同于完整交付协议。
- `r2-f-target-red.log`、`r2-f-activation-red.log` 为实际失败证据；`r2-f-target-green.log` 和 `r2-f-activation-green.log`（`go test -p 1 -race ./connector/... -count=1`）PASS，无skip/race。真实Manager在纯初始化后通过原Feishu client向本地接收端create/update成功；disabled、缺secret、错误digest、调用方配置修改和错owner受到检查。
- 仍需 R2-F：原事务producer的版本化目标；creation持久claim/成员/actor核验；update同实例发送；manual同步消除直接外发；inbound已有mapping/删除边界；发送后重绑/回执失败保持unknown且不重发。尚未做此提交上的全后端构建、完整私有S6或里程碑独立审阅，不复用此前构建为这次变更的构建证明。

### 7.5 R2-F 创建 producer 检查点（2026-09-14 10:15 CST）

代码提交 `9921707da8a384bac190dcb70e8c09310b11175b`。本批累计 09:42–10:15，约33分钟，涵盖7.4目标组件和本检查点；R2仍部分完成。

- 创建 producer 不再依赖已激活 `Manager.Get`：candidate 从冻结声明读取，standard 使用原事务中的持久配置读取。版本化 FeishuTarget 固定协议、name/provider/destinationDigest；禁用outbox/尚未激活/没有发送secret时，合法声明仍产生pending意图，零网络请求。
- 仅可选目标确实不存在时省略。目标描述错误、错误provider、错tenant和取消上下文不被当成无配置；新增缺失错误仍同时包装原ErrDenied，保留其他调用方的拒绝语义。
- 配置读取故障保持 InfrastructureUnavailable 与原始错误链，创建事务回滚。旧payload不转换，不重写历史队列；manual创建暂只适配payload类型，其来源和投递端仍待完整收口。
- 回归fixture明确提供standard策略、租户上下文与持久配置；飞书专用fixture通过持久通知偏好关闭邮件（保持站内通知），不把未配置邮件运输纳入飞书测试。原冻结内容、共享创建意图、当前权限撤销、未知结果和读取不外发断言保留。这些SQLite回归不作为持久claim/锁协议证据。

本机证据（目录同7.3）：
- `r2-f-producer-red.log`：创建成功但禁用outbox时遗漏意图；`r2-f-producer-fault-red.log`：配置读取故障错误地归为DomainValidationFailed。
- `r2-f-producer-private-race.log`：私有PG候选原producer正向PASS，冻结完整目标、pending、未激活、零请求。
- `r2-f-producer-regression-race.log`：受影响Feishu创建/manual/并发/只读/配置故障测试 `-race -count=1` PASS。
- `r2-f-producer-description-race.log`：database声明与connector持久描述受影响测试 `-race -count=1` PASS。
- `r2-f-producer-build.log`：当前代码 `go build -p 1 ./...` 退出0；日志空。以上无skip，不冒称完整G1/S6。

剩余差额仍为7.4末项：creation持久claim/成员/actor与发送后回执；update完整目标及同实例；manual原事务意图；inbound已有mapping/删除；R3整体验收和里程碑独立审阅。M1尚未关闭，按当前跨入口规模暂估还需3–6小时（工作估算，非准入承诺），下一批以creation持久投递的实际差额校准。R4–R9未开始；没有新版CandidateSHA或B的T3 EnvironmentRevision，不执行真实候选验收、B配置操作或共享库操作。
### 7.6 维护者调整为核心功能与上线优先

当前执行顺序：R2仅完成非飞书核心阻断与飞书未启用边界核实 → R3核心业务/架构运行验收 → R4必要鉴权可靠性 → R5固定可发布代码 → B环境交接和R7真实功能验收 → R8恢复稳定性与R9维护者验收。R1完成，R2部分完成，其他状态不因范围调整自动变为通过；M1–M4继续沿用。

历史7.1–7.5的缺陷与日志保留作事实记录，其中飞书后续开发安排已被本节取代。此前M1剩余3–6小时估算主要包含飞书开发，现不再适用；下一批根据核心业务实际失败给出差额与工时，不沿用该估算。