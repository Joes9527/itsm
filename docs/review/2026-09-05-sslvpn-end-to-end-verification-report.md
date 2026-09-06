# SSLVPN 端到端实施与验证报告

**状态：持续实施，尚未完成端到端验收。** 原会话已由用户终止；接手后完成MSP身份、入口语义、队列生命周期及027/028共享字段归一的限定修复与独立审查。Catalog当前会话读取也已通过独立审查；前端切换、目录发布、身份交换、KAF及外部授权验收仍未完成。阶段测试通过不等于完整业务交付，也不代表已部署。

原暂停快照见[开发交接报告](2026-09-05-sslvpn-development-handoff-report.md)。本报告按提交保留各阶段实际验证范围、失败修复和剩余门槛；较早段落中的“下一项”仅表示当时状态。

## 1. 验收范围与当前进度

重新接手后的独立增量审查见[接手期间增量审查报告](2026-09-05-sslvpn-successor-review-report.md)：Change/Ticket repository 当前包测试及全部后端包编译通过，其中必填字段测试跳过与真实 PG 回滚覆盖缺失两个阻塞项已修复并通过针对性复审；七个 HTTP fixture 的租户上下文/入口迁移失败也已修复并复审通过。后续 `1802a000` 已完成显式流程覆盖权限、创建 actor 审计与矩阵重放，并通过独立审查；MSP 创建身份及转换入口的限定范围验收已补齐，其余完整链路门槛保持开放。后端退役与测试迁移已保存为中间源码提交 `229d8091`，最新全部包编译检查退出 0；尚非完整 A3b/A4 审查结论。下文较早提交的阶段证据保留原有适用范围。

依据[实施计划](../superpowers/plans/2026-09-05-sslvpn-end-to-end-implementation.md)和[已确认设计](../superpowers/specs/2026-09-05-sslvpn-kaf-intake-end-to-end-design.md)，最终链路为：KAF Web 理解意图并复用收集/确认卡片 → 当前用户通过 Unified Intake 创建 ITSM 申请 → BPMN 两级审批 → Worker 委派 KAF 执行 → 查询确认外部用户组成员关系 → ITSM 记录授权结果、状态、审计 → KAF 展示结果。

成功标准是**外部用户组授权经查询确认成功**。权限到期自动回收仍为 Backlog；本期不验收 VPN 登录、网络连通性或 Teams/WeCom。

| 阶段 | 已有证据 | 尚未完成 |
| --- | --- | --- |
| 入口与字段盘点 | [归并清单](2026-09-05-intake-reconciliation-inventory.md) | 最终逐入口处置与全链路回归 |
| Intake 契约与创建事务 | 公共创建 port、Registry、严格输入、幂等回执、事务重试和配置快照已通过阶段审查 | 全入口及共享字段切换后的整体审查 |
| 持久化流程启动 | 精确流程定义、启动摘要、Outbox 重放及故障恢复已有阶段证据 | 生产装配完成后的完整流程验证 |
| Incident 创建事件 | 复用原规则引擎、事务动作与事件回执，已有阶段审查 | 与全部入口、运行角色的最终联合门禁 |
| 数据库租户隔离 | 实际受限角色、Tenant/System 独立连接、真实 RLS 策略测试及独立复审通过 | 最终迁移组合与实际服务进程启动门禁 |
| 全入口、旧路径和共享字段归并 | HTTP、BPMN、邮件、AI、Feishu适配及027/028共享字段归一已有针对性测试和分段审查 | 前端切换和最终全入口整体审查仍开放 |
| ITSM 前端与目录发布 | 已准备最终接口适配及验收清单 | 尚未完成该阶段实现与验证 |
| 用户身份交换、KAF 确认卡接入 | 已有明确接口与安全契约 | 尚未完成本期实现与集成门禁 |
| SSLVPN 授权、回执与外部验收 | 已确定策略、有限期限、结果契约和验证方案 | 尚未执行本期完整业务验收 |

## 2. 已审查的后端前置提交

| 提交 | 覆盖内容 | 证据边界 |
| --- | --- | --- |
| `62fec316`、`ffc4d17d` | 创建事务、当前权限校验、Prepare 顺序、配置快照与整事务重试 | 不代替全部真实入口验收 |
| `00034817`、`c1586c7e`、`56bc3df9` | 持久化精确流程启动、回执与重放；迁移 023 | 初始启动测试不能证明任意后续节点或定义编辑行为 |
| `e0055b83` | Incident 创建事件及规则动作事务；迁移 024 | 专业生命周期仍由 IncidentService 所有 |
| `1808f2d9`、`367f9af9` | RLS 驱动、Ent 变量处理、独立受限 System 能力和实际运行构造 | 复审两个 Important 问题均已修复；不等于共享环境已启用 enforce |

后续入口归并中的 `9850c1d6`、`a9986ccb`、`4acfcfcb`、`548d34e3` 为**开发检查点，整体尚未独立审查**。其中新增了迁移 025、目录确认版本读取、统一创建响应、持久化精确数字和单一 Feishu 创建意图。不得将中间提交单独部署或作为最终发布依据。

### 后续身份与入口验证（`1802a000`）

- `go test ./authorization ./handlers/intake ./service ./service/bpmn ./controller ./handlers/change ./handlers/problem ./handlers/service_request ./handlers/standard_change -count=1`：整体退出 1。除 controller 外八个完整包通过；controller 两处旧 Incident→Problem 转换 fixture 失败，保留待修，不计为整体通过。
- `go test ./tests/integration -run 'TestIntake|TestDynamicFields|TestServiceCatalog' -count=1 -v`：退出 0，29 个顶层用例、84 条通过事件、零跳过，17.183 秒。覆盖真实 HTTP/BPMN 入口、当前流程权限及撤权重放、actor/requester 与规范业务身份、配置矩阵重放、专业持久化故障完整回滚后重试。
- 独立审查批准这三个检查点，无 Critical/Important。实际完整 controller 日志仍包含既有缺失 tenant 上下文导致的类型断言 panic；相关测试仅证明角色门控未返回 403，不证明业务删除成功。
- MSP 原租户 actor 与客户数据租户的分离在现有统一创建校验中尚未贯通；两处旧转换测试和此身份边界仍需修复。此次未运行 PG/RLS、真实 bootstrap、前端、KAF 或外部 Graph 验收。

### 接手后的 MSP core 审查与修复（`57d519e3`、`2865ba2e`）

接手时源码已提交，但原实现报告因相对路径错误未写入。接手者恢复了历史报告，并重新验证核心授权与实际 PostgreSQL 用例。独立审查发现三个升级缺口：新增必填列先于历史回填、追加流程变量破坏旧启动摘要、审计指定回执与原业务关联冲突未拒绝。三个问题均已复现、修复，并通过限定范围的独立复审。

- `go test ./authorization ./middleware ./handlers/intake ./database ./database/rls -count=1 -timeout=120s`：退出 0。
- `go test ./service -run '^TestWorkflowStart' -count=1 -timeout=90s`：退出 0，包括旧流程已提交但 Outbox 确认丢失后的升级重放。
- `go test ./migration ./internal/bootstrap -count=1 -timeout=120s`：退出 0；补齐原迁移清单测试遗漏的 026 项。
- 设置专属 `INTAKE_POSTGRES_TEST_DSN` 后执行 `go test -tags=integration_postgres ./tests/integration -run 'TestPostgresIntakeMSP(CanonicalBootstrap|MigrationRejectsConflictingAudit|ProvenanceMigration|SharedSnapshotAndDurableEffects|EmptyDevelopmentReset)' -count=1 -timeout=120s -v`：退出 0，19.235 秒。覆盖有数据升级、重复 bootstrap、冲突历史拒绝、真实受限角色的快照与原 actor 审计、回滚/清理。
- 最初 PostgreSQL 16 测试镜像缺少 pgvector，直接 `InitializeStorage` 检查在该环境前置步骤失败。随后在独占临时 `pgvector/pgvector:pg17` 实例 `127.0.0.1:36446/sslvpn_runtime`，从提交 `2865ba2e` 的独立源码快照调用真实 `InitializeStorage`，连续两次通过（`timeout 180s go run ./cmd/takeover_storage_check`，退出 0，AutoMigrate=true、AutoSeed=false）。数据库复核：vector 0.8.6 已安装、19 项迁移已执行至 026、actor_tenant_id 为 NOT NULL。该检查证明完整存储初始化与重复执行，不代替有数据升级测试、API/Worker 启动或业务验收。

- 同一提交另用独占 `sslvpn_upgrade` 库构造已执行至 025 的有数据夹具，经真实 `InitializeStorage` 升级并重复执行，均通过（`timeout 120s go run ./cmd/takeover_storage_upgrade_check`，退出 0）；断言原 actor、租户、WorkItem、回执 ID 与数量不变。夹具通过生产 `ApplyMigration` 逐项装载 025 及以前迁移，026 由正常初始化入口执行。前两次夹具构造失败（残留 026 触发器、完整迁移流检查拒绝截断列表）仅作搭建记录，不计为产品失败或通过。

MSP core 阶段完成不关闭转换接口、共享字段 027+、全入口、前端、KAF 或外部授权验收。下一项为显式客户申请人及真实签名 HTTP/撤权重放验证。

### MSP 转换 HTTP 验收（`87cb95ea`）

可选 `requesterId` 经严格 DTO 绑定传给统一 Intake，显式零或负值返回 400；跨客户 MSP 会话必须提供有效客户申请人。保持原 provider actor，创建与重放分别返回 201/200 的统一回执。原来的假转换测试及通过恢复 panic 掩盖问题的绑定删除测试，已改为真实服务与数据断言。

- `go test ./controller -count=1 -timeout=120s`：退出 0，18.170 秒。完整包通过，关闭此前两处旧转换 fixture 和无 tenant 的绑定删除缺口。
- `go test ./handlers/problem ./handlers/common/intakehttp ./tests/contract -count=1 -timeout=120s`：退出 0。
- 专属 PostgreSQL 上执行 `go test -tags=integration_postgres ./tests/integration -run '^TestPostgresIncidentConversionSignedMSPHTTPAuthorizationAndReplay$' -count=1 -timeout=120s -v`：退出 0，4.802 秒，无跳过。使用真实 SwitchTenant 签名、Auth/RBAC/Tenant/路由权限链、限定客户角色权限、实际受限 Runtime/System 连接及迁移流。
- 原始申请人/actor 审计、关联与来源 Incident 保持正确；分配、权限、角色、actor、申请人及客户状态撤销后的重放全部拒绝，比较业务图数量、快照/Outbox 数量及编号分配器实际值不变。外国租户、已删除、终态来源均拒绝。角色/schema 清理与连接归还均通过。
- 独立限定范围审查结论 PASS，无新增执行检查要求。生成的完整 Swagger 和前端响应/提交键适配仍归后续协调切换，不能据此声称全部入口或端到端完成。

### 工具队列生命周期与契约（`4154f309`、`5f5b6d96`）

入队与关闭共用同一锁确定先后，所有 Close 等待同一 worker 退出。已经执行的任务按既有 context 完成；缓冲任务保留已持久化 approved/pending 状态并记录仅含安全标识的警告，沿用现有批准入口重入队。实际 Application 在关闭数据库依赖前关闭其持有的队列。该机制仍要求正在运行的处理器遵守 context，不能强杀忽略 context 的代码。

- `timeout 300s go test ./service ./internal/bootstrap ./handlers/ai -count=1`：退出0，27.259/0.370/0.025秒。
- 首轮独立审查未发现生产队列竞态，要求补充真正同时竞争的测试；修复轮通过共享屏障同时释放 Enqueue 和两个 Close，验证两种合法调度结果、pending 持久状态、警告、关闭完成和后续拒绝。`timeout 180s go test -race ./service -run '^TestToolQueueEnqueueCompetesWithCloseAtSharedStartBarrier$' -count=1`：退出0，1.124秒。
- `create_ticket` schema 描述规范输入，归一化仍由原 Intake 所有；Generic 结果包含六个必填回执字段，professionalReference 为 `{type:"",id:0}`。实际 Intake 的已批准调用验证了带空白标题/优先级和空优先级默认值，相关集成命令退出0，0.886秒。未新增重复业务校验器。
- 复审 PASS，原 schema 问题根据规范输入与接受等价别名的边界撤回生产 P2 分类，竞争测试缺口已关闭。该修复轮未修改生产队列行为，因此仅运行受影响测试，没有重复完整包。

### 四入口申请人传递与重启恢复（`378e2820`，2026-09-06）

Incident、Problem、Change 创建及标准变更实例化增加可选正整数 requesterId，映射到原 Intake；缺省/null 保留原 actor 默认，显式零/负数拒绝400。申请人、原操作人、租户授权仍由既有事务快照边界管理。标准变更实际路由保留 super_admin 限制，未向 MSP 放开该入口。

- 9月5日保留日志：四入口真实 Intake 正反例通过，1.195秒；签名 MSP PostgreSQL 矩阵通过，4.667秒，覆盖三个可访问资源及标准变更403，测试角色和schema清理残留均为0。这些是重启前证据，非重启后新跑的数据库检查。
- 受影响包首次执行：controller/problem/standard_change通过，Change旧BPMN夹具缺少新的client参数导致编译失败；补齐现有隔离client后，Change完整包通过，1.873秒。此修复没有改变生产BPMN授权。
- 电脑重启使子Agent退出、专属测试容器停止，源码与日志均保留；该批变更当时尚未提交或审查。9月6日恢复时未改源码，只核对既有12文件改动并补一次定向验证：`timeout 180s go test ./tests/integration ./handlers/change ./handlers/standard_change -run '^(TestIntakeHTTPRequesterAdapters|TestChangeServiceTaskHandler_CreateChange_DelegatesToRealServiceAndCreatesWorkItem|TestStandardChangeHandler_RoutesRequireSuperAdmin|TestInstantiateStandardChange_SuperAdminOnBehalf)$' -count=1 -timeout=120s -v`，退出0，三个包0.955/0.039/0.036秒。
- 提交378e2820的独立spec/quality审查PASS，无新增问题。恢复时未启动数据库或访问外部系统。专属PG使用tmpfs，后续数据库验证需重新建立隔离夹具，不能沿用重启前初始化状态的假设。

### 输入语义与动态字段夹具切换（`e72eb323`、`eeecbf75`）

Change分类名称复用既有租户分类所有者；移除没有执行意义的FormPresetID/presetTypeId，保留真实字段定义、字段值和数据库templateId；旧模板workflow_steps为非空列表、错误形状或坏JSON时显式拒绝，缺省/null/空列表仍沿用实际流程绑定。未受影响请求保留intake-v4摘要，并有固定历史摘要和持久化回执重放回归。

- 共享契约/真实HTTP/完整Change命令定向RED→GREEN通过；Intake完整包通过2.765秒。Common完整包发现旧Generic测试仍使用已删除的嵌套共享字段别名，修正测试后通过，没有增加生产兼容字段。
- 前端实际提交参数构造的两例测试通过，`timeout 120s npm run type-check`退出0；仅移除无效preset指令，尚未完成前端全入口切换。
- 独立审查发现三处既有动态字段正向夹具仍发presetTypeId；父进程补查两例复现201/400失败。修复仅删三处旧字段，保留全部持久化/字段名/详情/重放断言。`timeout 120s go test ./tests/integration -run '^TestDynamicFields($|_)' -count=1 -timeout=90s`退出0，0.566秒，三例均匹配。限定复审通过，无新增问题。
- 旧`POST /tickets type=change`不能表达五个专业必填字段，仍不能独立完成Change创建；本阶段没有虚构默认值。HTTP测试证明分类解析/拒绝，完整统一命令证明真实Change分类持久化。A4必须通过专业表单/API和权威类型元数据解决活跃客户端输入，不能保留按模板名称正则猜业务类的路径。

专属PG16与Redis已在重启后恢复健康，Redis须显式使用36445端口；健康不等于数据库迁移或业务验收。PG17尚未重启。下一项为Incident来源/元数据信任边界，随后处理Feishu严格解析和027/028共享字段清理。完整前端、Catalog发布、KAF、运行进程与外部验收仍未完成。

### Incident 来源与元数据边界（`357373f4`、`4668f743`）

公开 HTTP 仅允许人工/用户来源，可信 BPMN 默认系统来源；专业服务在回执查找前验证绑定与元数据，创建时复用同一策略。合法历史请求保留 intake-v4 摘要和原回执。真实 HTTP/BPMN 及相关包已通过阶段测试；独立审查发现内部类型的指针接收者 JSON 编码器可绕过前置元数据检查，已用 owner 与匹配历史回执重放两例复现，再增加地址可取且可暴露接口的指针编码器检查。原始输入与intake-v4摘要不变，限定复审PASS，本阶段关闭。

- 重启后补跑实际 PostgreSQL：`timeout 150s go test -tags=integration_postgres ./tests/integration -run '^TestPostgresIntakeMSPSharedSnapshotAndDurableEffects$' -count=1 -timeout=120s -v`，退出0，4.427秒，无跳过；受限 Runtime/System 角色、并发撤权快照、后续重放拒绝、Worker/Incident 原操作人审计通过，角色/schema清理残留0。
- 修复回归：`timeout 180s go test ./service ./tests/integration -run '^(TestIncidentCreationMetadataRejectsAddressablePointerEncoder|TestIntakeIncidentPointerEncoderCannotReplayHistoricalReceipt)$' -count=1 -timeout=90s`，修复前退出1、修复后退出0；受影响元数据与Incident集合退出0（service0.016秒、integration1.383秒）。复审无新增问题，没有重复未改变的完整MSP检查。
- 当前容器实际映射为 `127.0.0.1:36430`，重启后 `36444` 没有监听。本次测试用仅在测试进程内存在的本地 `36444 → 36430` 转发满足隔离库硬校验，结束后已关闭。此前容器健康检查不能证明测试端口恢复；后续测试必须显式恢复其连接环境。

### Feishu 严格输入与旧入口退役（`a8f04a36`、`60ad44ce`）

真实 instance-ID Webhook 和直接 Receiver 入口共用有界 JSON 结构解析，递归重复字段、额外根对象、坏 JSON、过大/过深输入及读取失败被显式拒绝。验签仍使用原始字节，合法任务与 URL 验证保持有效，未知执行事件不再返回虚假成功。删除没有实际投递行为的管理员旧回调和无调用方 HTTPHandler；ACL生成器的旧公开豁免亦移除。

- 定向测试先复现旧问题再通过。最终 `timeout 180s go test ./common ./connector/builtin/feishu ./controller ./handlers/common/intakehttp ./router -count=1 -timeout=120s`退出0，五包全部通过；包含真实签名控制器入口、重放拒绝、无歧义分发、Intake精确字段/数值及真实路由退役检查。
- `timeout 120s node scripts/generate-acl-manifest.js --check --output /tmp/feishu-boundary-fix1-acl.yaml`退出0，527条实时路由覆盖100%；Go ACL门禁亦通过。已提交清单此前比实时路由少两条，本阶段仅移除旧入口，未借此声称清单完全同步；后续统一路由交付需消除该已知差异。
- 独立审查与单行公开豁免修复的限定复审均PASS。无外部Feishu调用、共享数据库或部署操作；测试替代仅位于现有应用服务的副作用边界。

下一阶段为027身份字段退役与028服务请求共享字段归并；A4前端、A5目录发布、A6身份交换、C1及KAF完整链路保持未完成。

### 027身份字段迁移（`8bb91677`、`7e81359e`、`9ce0ab86`）

`8bb91677`已退役tickets.type和incidents.incident_number的存储及生成字段，读取方改为WorkItem投影；迁移对冲突字段保留证据并拒绝执行。`7e81359e`仅移除一处退役setter后留下的无用测试变量。真实PG16验证了冲突拒绝、正常迁移/重复执行/Ent重建、一对一约束及受限角色的跨租户/缺GUC拒绝；最终全部包编译检查退出0。

独立审查发现TicketService.entToDomain遗漏RecordClass，影响搜索、SLA逾期及MSP查询的类型投影，已在9ce0ab86用mapper到公共响应的定向RED/GREEN修复，并通过限定复审。父进程已在精确旧提交f615664f的隔离PG17库上执行两次InitializeStorage并保存两个租户、4个WorkItem、2个Incident、2份回执快照；精确8bb91677的空库及有数据升级均经真实InitializeStorage连续两次通过（每条命令timeout240s、退出0），20项迁移至027、退役列保持不存在、所有非退役业务与回执数据同升级前JSON快照逐字节一致。最终9ce0ab86全后端包编译检查退出0；后续mapper/测试/三处RLS探针SQL改动未触及存储启动代码，未重复该启动检查。该阶段完成，不代表API/Worker或SSLVPN端到端已完成。

前端输入裁定：按已确认A4契约，在录入前明确选择普通工单、Incident、Problem、Change或服务目录。普通工单继续保留模板/自定义字段；专业类型进入完整专业表单，目录通过真实ID与版本确认。当前模板没有权威类型/目录关联，因此不新增自动模板路由存储，也不再根据名称猜类型。代价是不会提供“选模板自动决定专业类型”；如需此能力，必须另有显式配置。该裁定取代早期盘点中建议新增模板目标元数据的方案。

### Service Request 共享字段归一（`5722be7a`、`82866abe`）

七个重复共享字段已移除，由 WorkItem 统一提供租户、申请人、处理人、版本及公共时间字段；迁移028在 Ent 建立外键前检查历史关联和值冲突。历史时间戳不一致会明确拒绝升级，不静默选值覆盖。

- 七个受影响包完整测试及全后端编译退出0；真实 PostgreSQL16 覆盖七字段冲突、升级/重放、Ent重启、受限角色隔离和开发重置拒绝。
- 固定提交5722be7a、独占PG17：实际 `InitializeStorage` 空库两次及有数据升级两次均退出0，21项迁移至028，027/028九个退役列不存在；所有非退役业务、回执、租户及actor数据JSON字节一致。
- 独立历史孤儿关联夹具启动验证退出0：启动被包含具体记录ID的诊断阻止，七个旧列、原数据及027迁移账本保持不变。上述命令均使用 `timeout 240s go run`，仅验证存储初始化。
- 独立审查发现完成说明更新改写原解决时间、聚合回调与其他更新路径加锁顺序相反两项问题；82866abe已修复。时间戳回归与真实PG16并发测试均先失败后通过，后者在旧代码明确复现40P01死锁。新增先写WorkItem后扩展失败的事务回滚检查；Service Request与BPMN完整包退出0。限定复审PASS，无新增阻塞项；最终82866abe全后端编译退出0，此阶段关闭，仍不代表完整链路验收。
- Minor保留项：028独立verify只检查RLS启用/强制标志和策略数量，不能识别被替换成宽松表达式的策略；迁移apply会重建正确策略，实际受限角色负例通过。最终整体审查需处理该验证缺口。

### A4 Catalog 当前会话读取（`0a0465f4`）

已分配客户权限的原生 MSP actor 在真实签名 HTTP 下原先返回401，读取路径接入现有当前会话授权与同一RR快照身份目录后返回200；不引入代客户创建权限或申请人要求。四个受影响包通过，最终PG16矩阵退出0，覆盖撤权、跨租户拒绝、快照一致性及目录故障；每次请求结束Runtime/System连接池InUse均为0。目录import故障通过真实PG错误注入，生产适配器自身import分支另有既有database单测覆盖。全后端编译退出0，定向integration/router检查通过；contract包使用的正则未匹配测试，不计为该包验收。独立spec/quality审查PASS，无新增问题；该读取前置阶段关闭，不计为A4前端完成。

### A4 前端创建入口切换（`25306d3f`—`0518a0f3`，源码及限定复审通过）

七类创建API及活跃页面使用统一六字段回执，并保留一次确认的内容、提交键和已验证用户/租户上下文。未知结果重试遇权限或校验错误时不抹去原申请的不确定状态；成功回执也不会因导航回调失败而反转。Catalog按确认版本和专业目标提交完整字段；普通工单先明确选择目标，改进工单保留既有improvement子类型。无调用方的旧创建包装已移除。

Incident创建页三个未保存的输入（固定字符串受影响系统、仅更新接口接受的rootCause、指向/upload.do的假上传）已移除，保留真实CI和受支持创建字段；附件说明指向既有共享工单详情。代价是这些原本未生效的创建控件不再显示，不代表新增根因或上传创建能力。

- 冻结源码完整CI退出0：213/213套件，3091项通过、13项跳过，194.06秒；Statements80.16%、Branches65.97%、Functions82.23%、Lines81.56%，原阈值未调整。先前完整运行因覆盖率79.85%失败；补充真实传输/提交恢复分支后通过。
- type-check、lint、最终生产build均退出0；生成125页和standalone。既有BPMNDesigner unused-disable及Next ESLint-plugin警告保留。
- Playwright仅执行发现检查：80项测试、6文件，退出0；不是浏览器执行或持久化证据。父进程仅另验证Chromium可启动about:blank，无应用验收含义。
- 独立审查发现两项Important：目录重载移除/重命名字段或切换专业目标后可能静默丢弃原答案；消息包含fetch时通用catch会抹去ApiError结构化字段。修复轮1已提交6f106fd2：新增不兼容答案确认、保留兼容字段及重载申请人、完整保留ApiError；四例缺陷及申请人丢失均先复现后通过。6个受影响套件78项断言、类型检查、lint和构建退出0。限定复审确认原两项已解决，但发现重载/确认与旧申请重试重叠时可能忽略确认重置、发送旧快照；修复轮2已提交0518a0f3：将忙碌期间的确认重置保留到请求结束，旧申请仍可独立恢复；两条目录交错场景及成功/失败结束场景通过。7个相关套件77项断言、类型检查、lint、构建均退出0，独立限定复审通过，无未解决发现。前端会话取自真实/auth/me与/auth/tenants，但后端当前原生MSP客户会话投影仍有缺口：真实CommonHandler路径未完整表达已签名客户选择。将串行修复并验证，不由浏览器猜测租户。真实API、浏览器及外部链路门槛仍开放。

## 3. 可定位的 RLS 验证记录

以下结果对应 `367f9af9` 阶段的稳定代码，工作目录为 `itsm-backend`。数据库类测试使用专属临时 PostgreSQL 16 实例 `127.0.0.1:36444/sslvpn_test`；环境变量 `ITSM_TEST_DB` 和 `INTAKE_POSTGRES_TEST_DSN` 指向该隔离库，不使用共享数据库。

| 命令 | 退出码/结果 |
| --- | --- |
| `go test -json ./common/tenantctx ./database ./database/rls ./config ./authentication ./authorization ./handlers/common ./middleware ./service ./internal/bootstrap ./migration ./tests/contract ./tests/rbac ./controller ./router -count=1` | 0；15 个包、3,033 条 test/subtest 通过事件、7 个既有跳过项。跳过不计为通过证据 |
| `go test -tags integration_postgres ./tests/integration -run 'TestPostgresRLS\|TestPostgresIncidentEffectsRLSUnderNonBypassRole' -count=1 -v` | 0；11 个受影响 PostgreSQL 测试通过，无跳过，22.004 秒 |
| `go test -tags integration_postgres ./tests/integration -run TestPostgresRLSRuntimeStatementsAndTransactions -count=1 -v` | 0；验证跨租户 INSERT 被实际 RLS 策略拒绝，同租户 INSERT 成功 |
| `go test -tags integration_postgres ./tests/integration -run TestPostgresRLSSystemCapabilityConstruction -count=1 -v` | 0；验证独立凭据构造、缺失/过量权限、错误所有权和业务访问拒绝 |
| `go test ./config -count=1` | 0；独立文件秘密配置及错误配置场景 |
| `go test ./database/rls -count=2` | **失败**；既有 helper 重复 `sql.Register` 导致 panic。曾被后续命令的退出码误判，已更正；没有整体重复运行通过的证据 |

`-count=2` 的 helper 重复注册问题已作为 Minor 保留给后续维护/最终审查；不覆盖上述独立 `count=1` 和真实 PostgreSQL 的通过结果。数据库测试创建的专属临时角色/schema 已在该阶段清理并检查零残留。

## 4. 对调用方的明确变更

- 保留的专业创建 HTTP URL 统一返回 `CreateWorkItemResult`：`workItemId`、`number`、`recordClass`、`professionalReference`、`workflowStartStatus`、`replayed`，沿用数字 `code/message/data` envelope。新建 201，重放 200；不继续返回旧 `id`/`ticketId`/完整详情别名。
- 专业 HTTP 创建接口由调用方持有 `Idempotency-Key`；统一 Intake 接口使用 JSON `idempotencyKey`。响应丢失或身份刷新后的重试必须保持原键和确认内容。
- 目录详情提供确认用 `catalogVersion`/`formSchemaVersion`；重试不能静默读取新版并替换原确认快照。
- 共享创建字段归于单一根命令，严格契约当前为 `intake-v4`；所有活跃客户端必须随同发布批次升级。
- 工作流启动待处理不代表审批完成或外部授权成功。详情读取失败也不能把已提交的创建回执解释为创建失败。

这些是需要协调升级的响应契约变更；前端与 KAF 最终验收通过前，本批次不满足可发布条件。

## 5. 环境、外部影响与后续证据

- ITSM 和 KAF 使用独立 feature worktree；原 KAF 工作区已有 `uv.lock` 修改保持原样。
- 专属临时 PostgreSQL 16 测试端点为本机端口36444；重启后确认容器实际映射36430，定向测试通过临时本地转发访问，转发不常驻；新增独占 pgvector PostgreSQL 17 容器使用本机端口 36446，仅用于运行初始化验证，数据位于 tmpfs。专属 Redis 使用 `127.0.0.1:36445`，健康检查 PONG，关闭持久化；ITSM/KAF 分别预留 DB0/DB1。依赖健康不计为 API、Worker 或业务链路通过。
- 未向共享数据库或现有角色写入本期变更，未推送、合并或部署，未执行本期 Graph 成员关系查询或外部授权变更。
- 后续必须补充：最终 schema 升级/恢复与 Ent 重启检查、每个真实创建入口、Catalog 发布组合校验、当前用户身份与撤权、确认卡丢响应恢复、两级审批、两个 Worker、外部成员关系核验、首次授权时间重放不变、回执/审计一致性、外部测试清理。
- 流程实例等待期间修改同一流程版本的行为尚需 A5 具体验证；不能由精确初始启动摘要或固定定义下的数值精度测试推出流程定义不可变。

最终验收应记录当时的提交、完整命令及独立退出码、跳过项、数据库/角色模式、外部测试对象与变更标识、恢复结果；未运行的项目持续保持未验证状态。
