# T1 候选代码集成交接

- TaskID：T1
- Owner：Agent A / Mac
- Status：completed（G1 代码集成门禁；不代表 G2/G3 或 WSL 部署通过）
- CandidateSHA：`d7470a32dbb87acc9b5e4d9a895a146410723561`（B 必须构建此版本，后续交接文档提交不替代此字段）
- Branch：`codex/feat/candidate-integration`
- Worktree：`/Users/julian/.worktrees/itsm-candidate-integration`
- SourceSHA：main `a25e108d2a08a55469fa5ad547aac5a9adc251ff`；WorkItem `8152ee668a6096c98f90349fd7e43ebcd4f1c587`；主题 `eb76c3bca6a4cee4809711477231f486d01d04c5`。
- ConsumedHandoffs：设计/计划提交 `2a993f7159ed43afc471a6ae938c379a47149ec2`；T2 提交 `dfe697073b289da89d410a405623482bfe0981a8`（后续复核见第 5 节）；尚未收到 T3 交接。
- Resources：WSL not-created/not-modified；仅本机任务 worktree、依赖与私有验证日志。所有原 worktree 保留。

## 1. 集成与复核

先 fast-forward 纳入 WorkItem，再以 merge 纳入主题，保留两来源历史。分类与 RCA 修复已通过 ancestry 验证包含在 WorkItem 来源内，没有重复移植其他 runtime 分支。两来源共同修改 24 个前端文件，其中 9 个需人工解决冲突。

| 冲突 | 决议 |
| --- | --- |
| changes detail/edit | 采用主题表面/文本，保留业务字段和版本操作 |
| problems detail | 保留 panelRevision，禁止恢复已删除的第二套关联面板 |
| reports/change-success | 保留 outcome 成功率、错误提示和已删除的 byType；应用主题图表外观 |
| service-catalog/request | 保留 requesterResource 权限边界 |
| standard-changes | 保留 beforeNewConfirmation 关系刷新 |
| tickets/create | 保留分类 ID/CTI，禁止恢复 category code 推断 |
| ChangeImpactAnalysis | 保持 WorkItem 已有删除，不因主题改动恢复旧组件 |
| WorkItemSLA | 保留当前/历史周期、暂停、完成和配置缺失语义；改用主题表面 |

独立审阅者未继承会话历史，逐项检查 24 个重叠文件及自动合并，确认无 Critical/Important；主题侧非重叠文件保持来源。另一次独立复审确认测试环境修正未弱化断言。此审阅限于集成差异，不重复认证整个来源分支或目标环境。

同时纠正 AGENTS/CLAUDE 的过时实现状态、历史设计报告当前入口及 P 阶段误放完整 V1 的顺序。没有新增后端业务机制；后端源码与 WorkItem 来源相同。

## 2. 验证

运行版本：Node v24.13.1、npm 11.8.0、Go go1.26.0 darwin/arm64。独立副本使用锁文件 npm ci，不升级依赖；候选前端锁文件 SHA256 与主题来源一致。测试通过白名单环境启动，没有继承业务环境变量或复制 .env；未使用共享数据库配置。

| 检查 | 结果 | 边界 |
| --- | --- | --- |
| 最终前端全量单元 | 236/236 套件通过；3264 passed、13 skipped、0 failed | 跳过项不计为通过；不是浏览器或真实后端验收 |
| 集成重点组件/API | 5 套件、52 用例通过 | SLA、Shell、Requester、关系、Change 分类 |
| 类型/主题一致性 | npm run type-check 通过，包含 theme:check | 修正后再次通过 |
| lint | 0 errors、1 既有 unused eslint-disable warning | BPMNDesigner.tsx:348；未为消除警告混入修改 |
| 前端生产构建 | npm run build 通过，standalone prepared | 未启动候选服务；后续测试修正仅改变测试文件 |
| 后端构建 | go build ./... 通过 | Mac 构建，不能代替 WSL/Linux 构建 |
| 后端定向测试 | migration、internal/bootstrap、common/workitemidentity、authentication：164 test events passed、7 skipped、0 failed | common/workitemidentity 无测试；7 项为未配置真实 DB 的 opt-in 测试，留给隔离验证 |
| 差异检查 | git diff --check 通过 | 无未解决冲突；运行生成的已跟踪 junit.xml 已恢复，不提交测试输出 |

后端命令：`go test ./migration ./internal/bootstrap ./common/workitemidentity ./authentication -count=1 -json`。前端命令和 cwd 在私有 evidence-manifest.json 中逐项记录。完整后端/真实 PG、浏览器 E2E、Linux 构建及外部副作用不在本轮通过范围。

### 来源失败与修正证据

- 原 WorkItem 全量基线：3 failed / 225 passed 套件；10 failed、13 skipped、3206 passed 用例。
- 原主题全量基线：2 failed / 221 passed 套件；5 failed、13 skipped、3184 passed 用例。
- 候选初次全量：1 failed / 235 passed 套件；4 failed、13 skipped、3260 passed 用例。失败均在 classification-edit 的 Problem 路径。
- 单独 RED 重现 4 failed / 4 passed。代码使用 crypto.randomUUID 生成 operationId，但 jsdom 缺少该浏览器 API，调用在 API 请求前失败。
- 仅在 classification-edit.test.tsx 提供 node:crypto 的真实 randomUUID；fixture 增加 version:5，新增版本及 UUID 格式断言。没有修改生产代码或降低分类/调用次数/导航断言。
- 单独 GREEN 为 8/8；最终全量为上表 236 套件通过。
- 目录申请/通知来源曾有弹层可见性和超时失败，候选单独复现 2 套件 23/23 通过，候选两次全量也未出现同类失败；保留来源日志，不宣称已修复其根因。

## 3. 证据位置与摘要

原始日志仅在 Mac 私有目录 `/Users/julian/.local/state/itsm-candidate-delivery/t1/`，未提交源码库。`evidence-manifest.json` SHA256：`300f3f36c33956e219e84bf050741fa6ba1e54b6f6b517724cb1716ebe0ea96e`。交接包可携带该脱敏清单；需要原日志时按精确文件传递，不复制环境配置或备份。

| 日志名（.log） | SHA256 |
| --- | --- |
| candidate-unit-final | `303837441f8ba72beda95fe7ee90387c5baef76a6ca3a9f4551695f5619a8bf6` |
| candidate-focused | `f31e877919b900cf3b9350e02364de81e8fcdce28d5f76d9277b41f76729c1ea` |
| candidate-typecheck-final | `c3bd005dde7eeeb3f37382b09d2b0bf3280f5350f47c0cc15912c57fb101a83c` |
| candidate-lint-final | `c19dd0bc65816ca8cc4e6524bac3a17b445a4d3f9aaaaa2aa25e35f24ea134af` |
| candidate-build | `aa5e93318617755058d1e71f1f4d62bd582ebae1702fcee806e2389cd096d053` |
| candidate-go-build | `e3b0c44298fc1c149afbf4c8996fb92427ae41e4649b934ca495991b7852b855` |
| candidate-go-test | `f3d63548357ec1667643e74444716b7cffc9ffe4359a68403ce0ab44600e82a3` |
| classification-red | `bf4134034a87c4cc339b8e30defb31bc7784cfd4e5f5aefe661af4770a70dc7c` |
| classification-green | `9f70774be32469492fd8b0e41682745b44bd90ff29c7e38d5181928e6732103d` |
| failure-reproduction | `d13f9cb9a1dd4bff8bd5f67939c15a072017e0dba1542f01ff074245ba58641a` |
| base-workitem-unit | `61cb33ef34a59310cd8f17d2009d1a12416a1ecdc4be06ac9f1d5b4a87e86012` |
| base-theme-unit | `30d9fcbfb3da7e2fbf78d527b1b799e7feb517f7e6f2aeede459ff048bce6015` |

## 4. T1 首次交付时的下一步与未验证范围（后续状态见第 5 节）

**NextAllowedAction：** B 完成 T2，基于 CandidateSHA 重核完整迁移语义、角色、后台写入者及目标资源。只有全部准入通过，才进入 T3；不能把 G1 当作应用启动许可。

**Blockers：** T1 无未关闭代码集成阻塞；T2 尚未交付，目标迁移与 API 内消费者隔离能力尚未认证。已知 API 启动会运行后台消费者，普通迁移亦有历史删除/回填门禁；B 按 T2 提供实际证据。必要前置代码设计由 A 接收，不能由 B 绕过或清空数据。

**NotValidated：** WSL/Linux 构建；真实源/副本恢复；P/普通迁移；全部后台写入隔离；真实 DB opt-in 测试；浏览器完整 G2；Redis 冷启动撤销目标验收；HMAC 恢复；60 分钟稳定运行；维护者旅程。R、真实企业外部写入和生产上线仍排除。

A 在收到 T3 固定 EnvironmentRevision 后才执行 T4；此时不修改 B 配置、不启动其服务，也不自行执行共享数据库变更。未推送、未合并 main。

## 5. T2 消费与源选择决定（2026-09-12）

已验证 T2 bundle，本机与 WSL SHA256 均为 `3378b5b3bc80323a38171d85b7bb500b9b25327d3df1422ce63d46e5a241cf53`；仅导入独立 handoff 引用，不合并 B 分支、不修改其交接记录。T2 的 blocked 结论仍有效。

维护者在本会话明确同意：使用 `itsm_config_baseline_20260908` / `public` 作为候选数据库备份源，由 B 继续核实关联 Redis DB11、对象与本地附件范围。该决定关闭 B1 的数据库/schema 选择项；不将配置观察提升为活动连接证明。B1 的关联存储范围与一致性时间边界仍待 B 提供证据；本次同意不单独放行源停写、复制、恢复、迁移或应用启动。

CandidateSHA 仍为 `d7470a32dbb87acc9b5e4d9a895a146410723561`。与 B 检查的 `8152ee668a6096c98f90349fd7e43ebcd4f1c587` 比较，`itsm-backend/` 无差异，故 B 的后台写入与鉴权发现适用于该候选。B4 的固定版本输入现已补齐，但恢复、迁移结构与角色证据仍未关闭。

下一步职责：

- A：先设计 B2 的构造/启动副作用控制与候选执行范围，再设计 B3 的 Redis 冷启动及状态丢失拒绝策略。沿用已有任务、鉴权与租户边界；不增加第二套业务执行引擎。具体机制经设计审阅后实施，任何生产代码修复均须重新固定 CandidateSHA 并复核。
- B：在上述已确认源上继续只读核实 Redis/对象/附件、其他写入者、备份窗口、网络与秘密装载边界，更新自己拥有的 T2 交接；保留历史数据和队列状态。
- T3/T4/T5：继续遵循总计划前置门禁；本节不是启动或验收通过记录。

本次修改仅更新 A 的交接文档，未改变源码、资源配置或数据库。

### T2 revision-2 接收

已接收提交 `a0ad4617c6467df87f6bf942d5969004c60ca21c`，bundle 双端 SHA256 为 `932c91873632a41211f8930042b95a859374e710688b7ffbad127114b2d3daea`；与第一版相比仅修改 B 的 T2 交接文档。A 已检查交接增量，未重新执行 B 的私有现场采集，以下现场结果按 B 的时点证据引用。

- B 已接收 T1 固定版本，并重核后端 tree 与 24/24 ledger checksum；源库选择关闭，迁移与运行准入没有整体关闭。
- DB11 观察到 210 个已消费 refresh 状态和含 4 条历史消息的 Stream；没有撤销键不代表可豁免冷启动/状态丢失修复。保全不得延长 TTL 或授权历史消息重放。
- 确认源的附件、评论附件和 CMDB 文件结构化引用为零；共享桶 3 对象中 2 个被其他数据库引用，另 1 个归属未定。接受按确认源引用确定候选附件范围的建议，窗口前须重查；不扩大为共享全桶复制或清理授权。
- B 提出的 2026-09-13 10:00–10:15 Asia/Shanghai 仅为未批准窗口。A 前置修复、写入者协调与其余门禁未闭合，不能承诺该时段开始；窗口失效后应重新协调。

当前关键依赖仍为 A 的 B2/B3 设计与实现；B 保持只读准备。尚无 T3 EnvironmentRevision，T4 不执行。

### B2 实施检查点：S1/S2（未放行 T3）

A 的独立实现分支为 `codex/fix/candidate-execution-scope`；S1 提交 `ae97fbfd2`，S2 提交 `fbd52c240e91124f12709486c946bf6d85768770`。本节只记录这两个前置步骤，固定 CandidateSHA 仍为 `d7470a32dbb87acc9b5e4d9a895a146410723561`，未发布包含 B2/B3 的新候选。

S1 完成受限角色绑定、新 WorkItem 同事务登记及历史成员不可补录。独立审阅发现的历史 R 依赖漂移、默认 ACL 泄漏已修复，并以真实私有 PostgreSQL 角色测试复核。

S2 将事件订阅、工具队列、连接器与周期任务移到显式运行阶段；启动先核对模式、部署、范围和执行组件，先取得 HTTP 监听端口，再启动消费者。关闭时取消并等待任务，所有并发事件总线 Close 等待同一完整结果；Redis 发布与订阅各自拥有客户端。构造不再创建默认流程/绑定、向量结构或 MinIO bucket，已配置附件存储失败不能回退。架构摘要及开发手册已同步。

已执行的验证（日志根目录：`/Users/julian/.local/state/itsm-candidate-delivery/b2/`）：

| 验证 | 实际结果 | 证据 |
| --- | --- | --- |
| 后端 `go build ./...` | PASS | `s2-final-build.log` / `.json` |
| bootstrap/config/executionscope/eventbus/msgraph 并发检测 | PASS | `s2-final-race.log` |
| controller 全包及生命周期并发检测 | PASS | `s2-lifecycle-review-green.log` |
| ToolQueue 与附件启动定向并发检测 | PASS | `s2-final-services.log` |
| 真实 PG 角色准入、原子登记/回滚、越界拒绝 | PASS，非 skip | `s2-final-postgres.log` |
| 完整 NewApplication 构造前后保全 | PASS，非 skip | `s2-constructor-three-dependencies.log` |

构造保全使用标记的本机私有 PostgreSQL16、独立 Redis7.2.16 及从固定源码 `07c3a429bfed433e49018cb0f78a52145d4bedeb` 编译的 MinIO；依赖二进制指纹保存在 `s2-dependencies.json`。随机新建测试库和角色，仅放入合成历史 fixture；业务运行角色具备业务 DML，使意外构造写入可被观察，范围对象仍只读。子进程使用独立配置和清空的业务环境，仅构造、不启动 API/Worker；逐表内容及所列结构/权限/策略/sequence、Redis 键/消息/消费组、MinIO 桶/对象内容及元数据前后相同。测试进程和随机 fixture 由测试清理，专用依赖源码、二进制、日志及 PostgreSQL 测试实例保留。

独立 reviewer 已复核 S2 生命周期修复、角色准入及构造测试隔离。测试夹具曾存在端口抢占误连风险，现已改为每次随机凭据，Redis 还比对启动 PID，MinIO 检查启动进程未退出；身份核对在写 fixture 前完成。最初 MinIO 官方预编译下载返回410、源码依赖下载 TLS 超时；固定源码重试构建成功后才取得上述三依赖 PASS。早先仅 PG/Redis 的构造日志不作为 MinIO 证据。

尚未完成：S3 业务写入/结构化主体、S4 所有队列领取与恢复、S5 Stream/请求异步范围、S6 执行及重启后的历史保全，以及 B3 鉴权状态计划。有限构造观察必须与生命周期测试和审阅合看，不证明延迟周期、业务全流程或目标 PostgreSQL17 已验收。未修改 B 的配置、共享源或 main，未推送、迁移共享库或启动候选；T3/T4/T5 门禁不变。


### B2 S3 基础检查点（2026-09-13，业务接入未完成）

本增量只交付原事务适配及结构化字段，S3 不标记完成。`BindEntExecutionScope` / `RequireEntExecutionMember` 使用调用者的 `tx.Client()`，与 SQL Tx 共用同一校验和查询，不另开事务或连接。空上下文、空事务和已关闭事务拒绝执行。

`outbox_events`、`process_instances` 新增 nullable、immutable 的 `execution_work_item_id`；Ent 生成文件同步，tickets 外键及禁止改指/清空/历史补录的数据库触发器属于唯一迁移039。历史行保持 NULL。默认 ACL 剥离覆盖新触发函数。本次修改的是尚未发布的039，不能拿旧039执行收据宣称新SQL已应用；没有操作目标或共享库。外键仅保证目标存在，不证明租户、scope 或生产者归属。

本地证据目录仍为 `/Users/julian/.local/state/itsm-candidate-delivery/b2/`：

- `s3-ent-red.log`、`s3-reference-red.log` 分别保留缺失事务适配及结构字段的失败证据。
- `s3-ent-generate.log`：Ent 生成成功；变化限于两个模型与相关公共生成文件，无依赖清单变化。
- `s3-foundation-build.json`：后端全量构建 exit 0。
- `s3-foundation-final.log`：真实私有 PostgreSQL 的范围登记、结构引用约束，以及 PostgreSQL/Redis/MinIO 完整构造保全回归 PASS，真实依赖用例未 skip。
- `s3-ent-rls.log`：追加生产 RLS enforce 驱动与 off 模式的同事务测试 PASS；覆盖事务内新成员可见、其他连接不可见、历史成员拒绝、租户上下文切换拒绝且原事务仍有效、回滚无残留和关闭后拒绝。

独立 reviewer `review_execution_scope_s1` 对基础增量未发现阻断问题，建议补 RLS enforce 测试，现已补齐；其审阅不代表业务接入或消费边界通过。PostgreSQL16 合成夹具仍不替代 B 的 PostgreSQL17 目标验证。

已定位的下一步事务所有者包括 intake `createAttempt`、Incident command/rule-action transaction、Problem/Change 各 handler command、Requested Item repository/callback、共享 assignment/deletion/tag/comment/attachment/relation。原计划列出的 common creator/mutation 文件只是接口，不能作为全局拦截点。关系修改必须检查两端；附件外部存储副作用必须纳入授权时序；`ticket_service` 历史 GET 的 Feishu 异步调用必须在服务层阻断。此处是定位结果，尚未完成逐路由→服务→写主体→事务清单，不计作 S3 第一项完成。

剩余：可信启动清单向业务服务传递、所有原写事务接入、outbox/process 各权威生产者设置引用、真实 API/service 边界测试；随后 S4–S6 和鉴权计划。CandidateSHA 保持 `d7470a32dbb87acc9b5e4d9a895a146410723561`；没有启动候选、推送或合并 main，也没有更改 B 环境或执行共享数据库变更，T3/T4 保持阻塞。


### B2 S3 统一创建入口接入（2026-09-13，其他写入口待接入）

在基础提交 `6261941b4` 上推进：可信启动配置经 `database.NewExecutionPolicy` 校验并复制为私有映射，统一创建服务的构造参数必须显式提供策略；未提供或未准入租户拒绝，不能从数据库自动发现清单以外的范围，也不从请求字段赋权。bootstrap 在运行身份准入后注入；已有测试夹具显式选择 standard，没有给生产路径增加默认降级。

已核实入口与事务：

| 入口 | 所有者 / 写主体 | 原事务和执行检查 |
| --- | --- | --- |
| `/intake/work-items`；Ticket/Incident/Problem/Change/Service Request 创建；Incident convert-to-problem | intake application；receipt、base、专业扩展、来源关联、SLA、字段、快照、审计和创建事件 | `createAttempt` 的 RepeatableRead 事务。原授权通过后、receipt 前绑定；每次重试重新绑定；来源关联修改前检查 source；base 前检查 parent；扩展前检查新 base 成员。 |
| standard change、BPMN、ToolQueue、Feishu/邮件创建适配 | bootstrap 注入同一个 intake application | 同上，仅指调用统一创建路径的部分；不等于这些组件其他动作已受保护。 |
| 已完成 intake receipt 的重复请求 | 原授权及结果/关联回放读取 | 保持历史只读结果，不登记历史成员、不重写 receipt；新增写范围检查不置于此回放分支中。 |

独立核查同时确认后续生产者不只使用 `OutboxEventRepository.Enqueue`：Incident status、relation/outcome 事件、KAF `CreateDelegatedTaskWithClient` 直接 INSERT。引用应分别来自已授权 Ticket、`MutationWorkItemID`/领域 facts、新 ProcessInstance 结构化引用。Alert aggregate 是 alert ID，必须解析 Incident→WorkItem；不能直接当执行主体。ProcessInstance 的唯一 INSERT 在 `startResolvedProcess`，引用应在 Save 和 executeStep 前确定。这些生产者尚未在本增量接入。

本节不构成完整 G2 写入口清单或 S3 完成证明。共享修改、assignment/deletion/tag、评论/附件/关系独立入口、专业命令及生产者/消费链仍待逐项接入；没有发布新 CandidateSHA、启动候选或操作 B/共享环境。


本增量验证证据位于既有 B2 本地证据目录：

- `s3-policy-red-actual.log` → `s3-policy-green.log`：可信清单冻结、未准入租户及零值拒绝。最初 `s3-policy-red.log` 因测试文件路径错误未运行任何匹配测试，不作为证据。
- `s3-intake-policy-red.log` / `s3-intake-policy-green.log`：缺少策略拒绝及原 intake/bootstrap 测试。
- `s3-intake-guards-red.log` / `s3-intake-source-red.log`：临时撤销检查后，历史 parent 与 source 分别被错误接受；对照结束后恢复原实现，`s3-intake-restored-green.log` 再次运行完整真实创建边界用例 PASS。
- `s3-intake-error-red.log` → `s3-intake-error-green.log`：数据库查询失败曾被误报权限错误；修复后完整 intake 测试 PASS。独立 reviewer 已确认只有范围 ErrDenied 返回权限拒绝，其余基础设施不可用并保留 cause。
- `s3-intake-regression-green.log`：database、intake、Change、Problem、Service Request、standard change、controller、默认 integration 共8包测试 PASS。早先 fixture 参数迁移中的未定义 t 编译错误已修复，未将失败日志记为通过。
- `s3-intake-race.log`：database/intake/bootstrap 并发检测 PASS；后续错误分类修复另由完整 intake 测试覆盖。
- `s3-intake-final-postgres.log`：真实 PG scope/创建边界，以及 PG/Redis/MinIO 构造保全 PASS，三个顶层用例均未 skip。生产 tenant enforce 驱动与受限目录快照参与创建验证；先前同事务身份夹具失败不计为准入证据。
- `s3-intake-final-build.json`：后端全量构建 exit 0；`s3-intake-tagged-compile.log` 仅证明带 integration_postgres 标签的已改调用方可编译（integration/e2e/intake/service），没有运行目标环境 E2E。

独立 reviewer `review_execution_scope_s1` 对本次限定的可信策略、统一创建事务、回放和测试隔离完成审阅，错误分类问题修复后无剩余发现。其结论不覆盖尚未接入的其他业务写入口或生产者。


### B2 S3 生产者结构化引用（2026-09-13，执行准入仍待完成）

在统一创建接入 `04f07a13e` 上补齐已核实的 outbox/process INSERT 来源：Intake workflow start、Incident 创建/status、Email/Feishu 创建、relation/Change outcome/Problem resolved 均从拥有原事务的 Ticket 或领域事实设置 `execution_work_item_id`；Alert 在同一事务按租户查 Incident 扩展，再取 WorkItemID，保留原 alert aggregate 语义。BPMN 在唯一 `startResolvedProcess` 保存点、executeStep 之前，依据已验证 canonical WorkItem 身份设置引用；独立及 release 流程保持 NULL。

KAF 的 repository 和 transaction-client 两条写入路径均继承 ProcessInstance 的结构引用，历史 instance 为 NULL 时不从 BusinessID 或 payload 补造。`NewOutboxEvent` 增加显式执行主体字段；Enqueue 仅将唯一约束错误分类为 duplicate，FK 等其他约束失败保留原始错误链，避免伪装幂等成功。本增量不修改迁移039、不回填历史引用；测试中的历史 NULL 仅在新建私有夹具、安装039之前准备。

证据仍位于 `/Users/julian/.local/state/itsm-candidate-delivery/b2/`：

- `s3-producer-red.log`：缺少结构字段的编译 RED。首轮 BPMN 断言误用了 business key 中的数字，已改为真实 WorkItem/BusinessID；这也防止将字符串 key 误作执行身份。
- `s3-producer-fk-red.log`：撤回错误分类修复后，真实 PostgreSQL FK 23503 被错误包装为 duplicate；随后恢复实现并运行 GREEN。
- `s3-producer-final-tests.log`：service/intake/integration 的 outbox、BPMN start、delegation、Incident、relation、email/Feishu 和 workflow-start 定向回归 PASS。新增断言覆盖 KAF 双路径和历史 NULL、独立/release 与 Ticket ID 碰撞、Alert 三种 ID 不同、Requested Item workflow event→ProcessInstance 引用一致。
- `s3-producer-final-pg.log`：真实私有 PostgreSQL 范围登记与 intake 创建/回滚/历史拒绝 PASS，并核验 Incident/relation 事件引用，以及不存在的引用触发 FK 而非 duplicate、真实 event-ID 重复仍识别为 duplicate。未 skip。
- `s3-producer-build.json`：后端全量构建 exit 0。

独立 reviewer `review_execution_scope_s1` 对引用来源、传递路径和错误分类未发现阻断问题。以上只证明结构引用，不证明候选发布许可：Enqueue 的原事务/成员准入、未解析主体在 candidate 下拒绝、专业/共享业务修改和历史副作用仍待接入；队列 claim/recovery 和 Stream 隔离也未完成。零引用不能视为获准执行，当前不能启动候选。S3 保持未完成，CandidateSHA 仍为 `d7470a32dbb87acc9b5e4d9a895a146410723561`；未推送、合并 main、修改 B 配置或操作共享数据库。


### B2 S3 Incident command 事务范围（2026-09-13，Incident 全入口未完成）

在生产者提交 `ff3fbcd71` 上接入 `ApplyIncidentCommand` / `applyIncidentCommandTx`：原 actor/tenant 权限、幂等回执、version 和生命周期转换检查保留；首次 Ticket UPDATE 前，在调用方原事务绑定可信 scope 并核验 WorkItem 成员。范围拒绝与数据库失败分开处理，后者保留原始错误链。独立命令与 AssignmentAction、StatusChangeAction 共用此核心，不另开业务事务或补录历史成员。

IncidentService、IncidentRuleEngine、IncidentEscalationService 的构造参数显式携带 policy；bootstrap 在运行身份准入后冻结同一 policy，供 Intake 与 Incident 使用。规则解析器向动作传递该依赖；独立 container 验证执行配置后注入，但运行角色准入仍属于其调用方的启动职责。已有测试和内部 owner 构造同步，没有新增生产 standard 默认值。

本次证据仍位于 B2 本地目录：

- `s3-incident-policy-red.log`：缺少策略字段的 RED；新增测试覆盖未配置策略时无命令写入。
- `s3-incident-guard-red.log`：临时撤销原事务检查后，历史 Incident 的有效 start 命令被接受，测试如期失败；随后恢复实现。
- `s3-incident-regression-final.log`：service/intake/controller/integration 中 Incident、assignment、status action 和 intake 定向回归 PASS。前期 fixture wrapper 签名与两处 BPMN callback fixture 未显式注入造成的失败已修正。
- `s3-incident-final-pg.log`：真实私有 PG + enforce tenant driver + 受限目录快照验证 PASS。新命令成功、历史命令拒绝且事件/审计不增；迁移前由 standard service 生成的真实 command receipt 在候选中只读 replay；Assignment/StatusChange 的调用方 RR 事务内新成员可修改、历史成员拒绝，回滚后版本/状态/审计/outbox 不变。另注入 start 命令 outbox 已写入后的失败，核验 Ticket 完整字段及 audit/outbox/timeline 回滚。此用例不证明 resolve 扩展字段或 reopen SLA 归档已完成候选验收。
- 同一 final PG 日志还包含范围登记及 PG/Redis/MinIO 完整应用构造保全，三个顶层真实用例均 PASS、未 skip。
- `s3-incident-build.json`：后端构建 exit 0；`s3-incident-tagged-compile.log` 是 integration_postgres 标签下受影响调用方的编译检查，未运行目标环境 E2E。

独立 reviewer `review_execution_scope_s1` 对上述限定事务边界、构造传递、replay 与测试设计复审无阻断问题。**仍未完成的 Incident 边界**：ExecuteRule 会先写 execution 并更新规则统计，不能以受保护 action 宣称整次规则调用无历史写入；直接 EscalateIncidentTx/EscalateToMajorIncident、UpdateIncidentTx、LinkIncidentCIs、CreateIncidentEvent/Metric、NotificationAction/MetricCollectionAction 仍待接入。给这些 owner 传入 policy 本身不等于其所有方法已受保护。

S3、其他业务域/共享写入口、生产者成员准入和 S4–S6 仍未完成。未启动候选、修改 B 配置、执行共享数据库变更、推送或合并 main；固定 CandidateSHA 和 T3/T4 门禁不变。


### B2 S3 Incident metadata / 直接升级事务范围（2026-09-13）

在 `3212d33bb` 上为 UpdateIncidentTx 和 EscalateIncidentTx 增加原事务范围检查。UpdateIncident 私有实现显式接收调用方 tx；两个入口均保留原输入、租户、version 和业务校验，在首次写入前绑定可信 policy 并检查 WorkItem membership。既有 WorkItem、专业扩展、timeline 和升级告警仍使用原事务；缺少 policy/tx 明确拒绝，空升级请求返回校验错误。

私有证据位于 `/Users/julian/.local/state/itsm-candidate-delivery/b2/`：

- `s3-incident-metadata-red.log`：新增历史修改用例在修复前收到 nil error，按预期失败。
- `s3-incident-metadata-pg.log`：完整 TestCandidateIntakeCreationBoundary 在真实私有 PostgreSQL、生产 enforce 驱动和受限目录快照下 PASS，未 skip。新增两入口历史拒绝、新成员事务内写入、独立连接不可见、调用方主动回滚后 Ticket/Incident 完整字段与 timeline 不变，以及非 Tx 包装入口提交后的 title/version/escalation-level 验证。
- `s3-incident-metadata-regression.log`：service/intake/controller/integration 中 Incident、assignment、status action 和 intake 定向回归 PASS。
- `s3-incident-metadata-build.json`：后端全量构建 exit 0。

独立 reviewer `review_execution_scope_s1` 对上述限定增量审阅未发现阻断问题。测试使用空 NotifyUsers，未验证升级 alert/outbox 分支，也未注入后续写失败；主动回滚证据不能替代这些场景。

本检查点只关闭上述两入口的成员准入缺口。ExecuteRule 执行记录/统计、重大事件升级、CI、独立 event/metric、notification、其他业务域和生产者成员准入，以及 S4–S6 仍未完成。未启动候选、操作共享数据库、修改 B 环境、推送或合并 main；CandidateSHA 仍为 `d7470a32dbb87acc9b5e4d9a895a146410723561`，T3/T4 门禁不变。


### B2 S3 Incident 规则执行记录和统计（2026-09-13）

在 `ae1731978` 上接入 ExecuteRule 的执行记录边界：起始记录在查询权威 Incident 的同一事务内绑定 policy、核验 member，再 INSERT；结果更新从 execution.IncidentID 重新查权威 WorkItem，在自己的原写事务再次准入。结果与规则统计同事务提交，后者失败不再留下已完成结果；持久化错误明确返回并保留原因。各动作仍保留独立事务，不宣称整次规则调用原子化；条件不匹配、解析失败不计次数，已识别动作失败计次数的原语义保留。

私有证据仍位于 B2 本地目录：

- `s3-rule-bookkeeping-red.log`：历史 Incident 在修复前进入规则解析而非成员拒绝，新增断言如期失败。
- `s3-rule-bookkeeping-final-pg.log`：完整真实 PostgreSQL TestCandidateIntakeCreationBoundary PASS、未 skip。历史 Incident 不产生 execution 或统计修改；未知动作明确失败且新成员保留 failed 记录；已识别动作失败记录和次数持久化；统计已写后注入错误，结果与次数一起回滚；条件未命中为 skipped 且不计数，其结果写后失败明确返回；真实升级动作成功为 completed 并计数。仅在私有夹具起始 INSERT 后、提交前关闭 scope，证明后续结果事务重新准入失败、running 记录和次数保持原状，结束恢复夹具状态。
- `s3-rule-bookkeeping-regression.log`：service/intake/controller/integration 定向 Incident/assignment/status action/intake 回归 PASS。
- `s3-rule-bookkeeping-build.json`：后端全量构建 exit 0。

独立 reviewer `review_execution_scope_s1` 限定审阅未发现阻断问题；按其建议补充成功/条件不匹配/结果失败及 hook 命中断言。此检查点保护规则记录和统计，不替代 notification/metric 动作本身、独立事件/指标/CI/重大升级和其他域的写入准入；全量扫描起始查询成员过滤仍属 S4 未完成项。S3–S6、B3、T3/T4 和 G2/G3 不因此通过。固定 CandidateSHA 不变，无候选启动、共享数据库操作、B 配置修改、推送或 main 合并。


### B2 S3 Incident 时间线、指标和重大升级（2026-09-13）

在 `bebd1f726` 上接入 CreateIncidentEvent/Metric 的独立事务入口和显式 Tx 入口。原活动记录主体存在性校验改为同事务按租户解析 WorkItem，并在 INSERT 前 Bind/Require；UpdateIncident、EscalateIncident、重大升级和规则 MetricAction 内部调用显式传入原 tx，不从 transaction client 再开事务。重大升级的权威读取进入同一写事务，原状态校验和 WorkItem version CAS 保留，首次写入前核验成员。

私有证据位于 B2 本地目录：`s3-incident-supplemental-red.log` 是修复前历史活动记录被接受的 RED；`s3-incident-supplemental-final-pg.log` 是完整真实 PostgreSQL TestCandidateIntakeCreationBoundary PASS、未 skip，新增历史 Event/Metric/Major 拒绝及完整 WorkItem/扩展字段、记录数不变；新成员提交成功；显式 EventTx/MetricTx 原事务内可见、其他连接不可见且调用方回滚；重大升级在 timeline 已写后注入错误，主记录、扩展和活动记录全部回滚。`s3-incident-supplemental-regression.log` 的四包定向 Incident/intake 回归 PASS。

`s3-incident-supplemental-action-pg.log` 在上述完整真实 PG 套件上补充实际规则解析的 collect_metric 成功提交及指标已 INSERT 后注入错误，证明 MetricAction 调用方事务回滚、不遗留指标；PASS、未 skip。`s3-incident-supplemental-build.json` 后端全量构建 exit 0。

独立 reviewer `review_execution_scope_s1` 确认生产内部 Tx 传递及新增边界无阻断。scope 不替代原 actor/RBAC 校验；本增量未改变其权限契约。CI、NotificationAction、其他 Incident monitoring/alert 直接写入口和其他业务域仍待盘点/接入；完整 S3、后台周期与队列隔离、鉴权修复和 G2/G3 尚未完成。未启动候选、操作共享环境或推送/合并 main，固定 CandidateSHA 保持不变。


### B2 S3 Incident CI、告警与通知动作（2026-09-13）

在 `fab50eb3f` 上将 LinkIncidentCIs 的读取、CI 租户校验和关联写入放入同一事务，写入前验证成员。IncidentAlertingService 必需传入可信 policy，bootstrap 复用已冻结实例；同域原事务检查函数用于 alert owner 和 NotificationAction，既有 actor/输入/租户校验保留。CreateAlert 的 alert、in-app notification、outbox 和 audit 仍在原事务中。Acknowledge/Resolve 的 actor 校验、所属 Incident/member 检查、条件状态更新和 timeline 现在同事务，timeline 错误明确返回，避免状态已更新却无活动记录。NotificationAction 在委托事务告警 creator 前也核验成员，规则解析器传入同一策略；测试 constructor 显式 standard，无生产默认值。

B2 私有证据：

- `s3-incident-ci-alert-red.log` 为 CI fixture Ent setter 拼写编译错误，已修正；`s3-incident-ci-alert-boundary-red.log` 为有效历史 CI 关联被接受的行为 RED。
- `s3-incident-ci-alert-verified-pg.log`：完整真实 PostgreSQL TestCandidateIntakeCreationBoundary PASS、未 skip。历史 CI/create alert 拒绝；迁移039前由 standard owner 创建的历史 alert 在候选下 ack/resolve 拒绝且完整字段/timeline 不变；新成员 CI、alert 创建与状态转换通过。ack 与 resolve 分别注入 timeline 已写后的错误，验证状态和记录全回滚；真实通知规则 parser/delegate 成功，outbox INSERT 后失败时 alert、notification、outbox、audit 全回滚。
- `s3-incident-ci-alert-regression.log`：service/intake/controller/integration 的 Incident、NotificationRule、assignment、status action、intake 定向回归 PASS。
- `s3-incident-ci-alert-build.json`：后端构建 exit 0。`s3-incident-ci-alert-tagged-compile.log`：integration_postgres 标签下 service/integration 编译通过，只编译不代表目标 E2E。

独立 reviewer `review_execution_scope_s1` 对构造传递、原事务和原业务校验限定审阅无阻断；按建议补齐 NotificationAction 实际路径及 Resolve 故障测试。未执行通知消费者或发送邮件。monitoring 的直接指标写入、全周期扫描、其他域/共享能力及 S4–S6/B3 仍未完成，不能据此放行 G2/G3 或 T3/T4；固定 CandidateSHA 不变。无共享环境写入、候选启动、推送或 main 合并。


下一批 G2 专业事务定位（独立只读审计 `review_candidate_prerequisites`，2026-09-13，非完成证据）：Problem 的 `handlers/problem/lifecycle.go` ApplyCommand/applyCommandTx、`metadata.go` ApplyMetadata 及其 root_cause/evidence applyTx、`deletion.go` Delete 是原事务 owner；Change 的 `handlers/change/commands.go` ApplyCommand/applyCommandTx、`metadata.go` ApplyMetadata、`task_command.go` CompleteChangeTask、`deletion.go` DeleteChange 和 `service/pir_mutation.go` mutatePIR 分别持有写事务。必须向 Problem/Change NewService 及独立 NewChangePIRService 显式注入现有 policy，并在原业务授权后、首次写入前核验所属 WorkItem。Change authorizeCommand 还被 GetTaskProgress 只读查询复用，不能把写范围检查塞入该授权函数；竞争回执恢复保持只读。Problem creation 的 source Incident timeline 仍需保持来源与新 Problem 双端准入。ProblemResolved/ChangeOutcome 消费者的关联 Incident 通知和诊断审计属于后续独立事务边界，不能因专业命令接入而计为完成。


### B2 S3 Problem 专业事务（2026-09-13）

在 `f1c61b724` 上向 Problem domain NewService 显式注入可信 policy，bootstrap 复用已有冻结实例，测试构造按上下文传 candidate 或显式 standard。ApplyCommand/applyCommandTx、ApplyMetadata、Delete 在各自原事务首写前 Bind/Require；生命周期/metadata 的原授权、版本、字段与依赖检查保持原顺序，历史回执恢复仍只读；Delete 在 GuardDeletionTx 的内部并发锁写入前准入。范围拒绝映射 Forbidden，数据库失败保留 cause；没有将写范围检查混入只读复用的授权函数。

B2 本地证据：

- `s3-problem-red.log` 为 fixture 错用 repository 构造名称的编译错误；修正后 `s3-problem-boundary-red.log` 验证历史 investigate 被错误接受。首次 PG GREEN 尝试仅因 Forbidden 文案与断言不一致失败，已统一领域错误文案。
- `s3-problem-final-pg.log`：完整真实私有 PG TestCandidateIntakeCreationBoundary PASS、未 skip。历史 investigate/metadata/delete 拒绝，Ticket 和 Problem 完整字段、audit/outbox 不变；迁移039前真实 command/metadata receipt 在候选下只读回放。新候选 metadata、删除及根因/永久解决方案→investigate→verify_resolution→resolve→close→reopen 通过；resolve outbox 已 INSERT 后注入故障，主记录、扩展、审计/outbox 全回滚，同一请求重试与 replay 成功。
- `s3-problem-regression.log`：Problem/intake/service/controller/integration 五包定向回归 PASS；之后仅统一 Forbidden 文案，最终真实 PG 再次验证。
- `s3-problem-build.json`：后端全量构建 exit 0；`s3-problem-tagged-compile.log`：integration_postgres 标签下六个受影响包编译通过，未运行目标环境 E2E。

独立 reviewer `review_execution_scope_s1` 对原事务/写入顺序、构造传递、回执和错误处理限定审阅无阻断。RCA/Evidence 子表分支处于 metadata guard 后，但本次尚未逐分支做真实专项运行验收；ProblemResolved 消费者及关联 Incident 副作用仍须单独接入。Change、Requested Item、共享能力、其他生产者和 S4–S6/B3 仍未完成，不放行候选或 T3/T4。固定 CandidateSHA 不变，未操作共享环境或推送/合并 main。


### B2 S3 Change 与 PIR 原事务范围（2026-09-13）

在 `fc71e6266` 上为 Change NewService 和独立 NewChangePIRService 增加必需 policy；bootstrap 复用冻结实例，Change 将同一实例交给其 PIR owner。Change command 在原授权/版本后、专业写入及流程终止前准入；metadata 在首写前准入；CompleteChangeTask 在原任务/实例归属核验后、CompleteTaskTx 前准入；Delete 在 GuardDeletionTx 前准入。PIR 在原授权/replay/version/terminal/callback-settled 检查后、行锁及写入前在同一事务准入。授权函数和只读进度/receipt 恢复未混入写范围检查，基础设施失败保留 cause。

B2 私有证据：

- `s3-change-red.log`：有效历史 Change metadata 被错误接受，新增测试如期 RED。
- `s3-change-regression.log`：constructor 批量迁移误改 Change intake fixture 中不相关 catalog/intake 构造参数，编译失败；独立 reviewer 同时指出，已完整撤回该文件误改，未弱化测试。
- `s3-change-final-pg.log`：完整真实私有 PostgreSQL TestCandidateIntakeCreationBoundary PASS、未 skip。历史 metadata/cancel/delete/PIR create 拒绝，主记录/扩展/审计/PIR 数量不变；新成员各入口成功；迁移039前的 PIR create receipt 只读重放；历史 PIR update/delete 拒绝，新 PIR CRUD 成功。metadata 和 PIR create 在 audit 已写后注入错误，Ticket、Change、PIR、audit 全回滚。
- `s3-change-regression-final.log`：Change/intake/service/controller/integration 五包定向回归 PASS。
- `s3-change-build.json`：后端全量构建 exit 0。`s3-change-tagged-compile.log`：integration_postgres 标签下受影响六包编译通过，未运行目标环境 E2E。

独立 reviewer `review_execution_scope_s1` 确认生产事务、callback 委托和构造传递无其他阻断，复审确认 fixture 编译阻断关闭并读取回归/PG PASS。**本次未完成的验收**：真实候选 task completion/callback、完整 Change 审批/实施/评审生命周期和其并发重试，Change command/metadata 的迁移前回执专项验证；不能用已通过的 draft metadata/cancel 或标签编译代替。Requested Item、共享能力、生产者/消费者完整范围与 S4–S6/B3 仍未完成，候选不启动，T3/T4/G2/G3 不放行。固定 CandidateSHA 不变，无共享环境操作、推送或 main 合并。


### B2 S3 Requested Item 原事务范围（2026-09-13）

在 `dfa851b68` 上向 Requested Item Service/EntRepository 显式注入可信 execution policy，bootstrap 复用冻结实例，测试调用显式区分 standard/candidate。Update 的原 repository 事务在 extension identity 核验后、Ticket CAS 前准入；Delete 在 GuardDeletionTx 前准入；update/provision/aggregate workflow callback 在原事务首写前准入，assignment 改由同一事务读取、准入、CAS 和提交。原业务授权、版本与 no-op 行为保留。AccessCompletion contributor 显式接收 BPMN 原 `*ent.Tx`，在 access result 首写前准入，不另开事务。scope denied 映射 Forbidden，其他错误保持原 cause。

B2 私有证据（均位于本机 candidate-delivery/b2）：

- `s3-request-boundary-red.log`：历史 Requested Item update 被错误接受，边界测试 RED。先前 `s3-request-red.log` 是缺少 approval resolver 的 fixture 失败；修复后才获得有效 RED。
- `s3-request-regression.log`：外部 service_request_test 的 repository 构造参数遗漏导致编译失败，已逐调用显式补齐 Standard，没有改用隐藏默认策略的包装器或削弱断言。
- `s3-request-regression-final.log`：service_request/intake/service/controller/integration 五包定向回归 PASS。
- `s3-request-final-pg.log`：完整真实私有 PG TestCandidateIntakeCreationBoundary PASS、未 skip。历史 update/delete 及 update/assign/provision/approve/complete callback 拒绝，Ticket、ServiceRequest 完整字段及 audit/outbox 数量不变；新成员各入口成功。update 和 complete_request 在 extension 实际写入后注入错误，base、extension、audit/outbox 全回滚。
- `s3-request-build.json`：后端全量构建 exit 0。`s3-request-tagged-compile.log`：integration_postgres 标签下六包编译通过，仅编译，不代表目标环境 E2E。

独立 reviewer `review_execution_scope_s1` 复核生产原事务、构造迁移和新增故障断言无阻断，并确认回归/最终 PG PASS。仍未验证真实 BPMN/KAF AccessCompletion 的双租户上下文传递和完整 task/ledger 回滚；直接域 callback 测试不能替代真实入口验收。update/provision 的初始读取仍在原有事务外，保留版本 CAS，本次没有声称全部读取均在事务内。共享能力、其他生产者/消费者、S4–S6/B3 仍未完成，S3 不勾选，候选不启动，T3/T4/G2/G3 不放行。固定 CandidateSHA 不变，无共享环境操作、推送或 main 合并。


### B2 S3 KAF access completion 与持久化时间精度（2026-09-13）

在 `42795a901` 后继续真实完成入口验收。新增私有 PG 子测试使用生产 enforce tenant client、candidate policy、CustomProcessEngine.CompleteKafDelegatedTask 和 Requested Item contributor。迁移039前历史 Requested Item 无成员拒绝且原数据不变；新成员在最终 ledger fence 实际 UPDATE 后注入错误，Ticket/ServiceRequest/ProcessTask/ProcessInstance/ledger 完整字段和 receipt/access result/audit/outbox 数量整体回滚；成功完成后 WorkItem resolved、task/instance completed，同一 completion 重放保持上述快照不变。

本次发现并修复真实精度缺陷：合法 RFC3339Nano verifiedAt 持久化至 PostgreSQL 微秒时间列后，原精确 Equal 误拒原凭据重放。仅在 receipt 校验时按微秒精度比较 verifiedAt/expiry，秒内采用 ties-to-even，匹配 PG 的半微秒行为；不改原请求、持久化原值、主体/目标/证据校验或外层 exact action digest。helper 不使用 UnixNano，避免其较窄日期范围溢出。

证据均在本机 candidate-delivery/b2：

- `s3-kaf-integration-compile.log`：实际 `integration` 标签下 service/service_request 编译 PASS。之前 `integration_postgres` 标签编译未覆盖 bpmn_kaf_completion_integration_test.go，本条补齐编译证据，仍不是运行该目标环境套件。
- `s3-kaf-access-pg.log`：新完成分支通过，但 fixture policy 因 snapshot FK 清理失败污染后续测试；现按 snapshot→policy 显式检查清理错误，使用独立历史 Requested Item。
- `s3-kaf-access-final-pg.log`：前置 ledger 缺少 RequestDigest，重放如约拒绝；补齐显式 fixture preclaimed digest，未放宽校验，未声称实际生成/领取验证。
- `s3-kaf-access-verified-pg.log` / `s3-kaf-access-nanosecond-red.log`：普通时钟样本 PASS；Mac time.Now 并不能确保亚微秒样本，后者文件名含 red 但实际为 PASS，不作为失败证据。
- `s3-kaf-access-submicrosecond-red.log`：明确添加123ns后原精确比较失败，证明真实重放缺陷。初版 Go Round 修复通过123/789ns，但 `s3-kaf-access-half-micro-pg.log` 在500ns中点失败；独立 reviewer 指出的边界已修复为取偶舍入。
- `s3-kaf-access-final-precision-pg.log`：最终完整真实私有 PG TestCandidateIntakeCreationBoundary PASS、未 skip，覆盖123/789/500/1500ns与上述拒绝/回滚/完成/重放。
- `s3-kaf-access-final-regression.log`：service_request/service/controller 的 KAF/Access/SSLVPN 定向回归 PASS；`s3-kaf-access-final-build.json`：后端全量构建 exit 0。

独立 reviewer `review_execution_scope_s1` 复核原事务、hook 顺序、fixture 保全、时间精度实现与最终 PG PASS 无阻断。普通 tenantctx 与 BPMN tenant/user 上下文一同进入真实引擎已验证；HTTP认证到入口的传递仅代码核查。审批决策、snapshot 和 executing ledger 是测试前置状态，未验证 HTTP、ExecuteAction 初始 claim/digest 生成/最终记账、实际审批或 provider；本轮不代表整个 KAF 周期已准入，相关 S3/S4 待办保留。共享能力及 S4–S6/B3 尚未完成，固定 CandidateSHA 不变，未操作共享环境、启动候选、推送或合并 main。


### B2 S3/S4 KAF 外层领取缺口：有效 RED（2026-09-13）

在 `6b2b50756` 完成事务检查点后，继续外层 ClaimKafAction。新增真实 PG 历史入口断言当前 **FAIL，未修复**：`s4-kaf-historical-claim-red.log` 显示 `claimed=true, ledger count before=0 after=1`，调用错误为 nil。运行使用同一生产 enforce runtime role、candidate manifest 的私有测试数据库；旧 Requested Item 在039前创建，未加入 scope。故障发生在后续 completion guard 之前，不能用上轮完成事务 PASS 放行外层动作。此提交是 TDD RED 检查点，不是可交付 GREEN。

当前代码证据和后续必须覆盖的修复范围：

- KafDelegationService.claimKafActionOnce 原事务 INSERT ledger 后 commit，再用独立自动提交语句做 pending/failed_retryable/expired-executing lease CAS，两处均无成员检查。重试必须重新检查，不能只在 HTTP 入口或第一次读取检查。
- finalizeKafAction 为独立 ledger UPDATE；finalizeAppliedKafAction 的 lease CAS 与 audit 同事务；persistNonCompletingAction 在原事务先更新 process instance，再产生 action/审计结果。每个事务必须独立准入，原 token/expiry/version fencing 保留。
- Completed receipt/recovery 及 process dispatch 属于相邻独立事务，不能把 ClaimKafAction 的修复视为整个 KAF 保护完成。历史 applied 原回执应走只读回放，不能为了探测唯一键而先 INSERT 历史台账。
- 可信 policy 需从 bootstrap 和 internal/container 现有冻结实例传入 CustomProcessEngine/KafDelegationService；HTTP 独立构造链为 BPMNWorkflowController→KafDelegationController→KafDelegationService，也须显式依赖，不能偷偷默认 standard 或新增可变 setter。独立测试按场景显式 standard/candidate。
- 候选归属应从原事务内的 task→process instance 结构化 execution_work_item_id 解析并校验成员，不从业务字符串推断/回填历史引用。缺少结构化归属要拒绝；新成员必须验证真实 claim、竞争、租约恢复、scope 关闭后再次写拒绝和原事务失败回滚。

当前 tagged candidate test 明确处于 RED，待后续修复后重新运行并审阅。未操作 WSL/共享库或启动候选，固定 CandidateSHA 不变，未推送/合并 main；S3/S4 及全交付继续未完成。

独立 reviewer `review_execution_scope_s1` 确认上述真实 RED 有效；补充必须覆盖 ensureKafCompletionReceipt/updateKafCompletionReceipt/makeKafCallbacksDue/enqueueRecoveredKafCallback/runKafFencedWrite 的独立恢复写事务，forClient 克隆需保留 policy 和原 tx。当前新 process instance fixture 仍无结构引用，正向修复测试需在新建时显式设置，不能回填历史。建议增加 claim 后关闭 scope 的 finalize/recover 完整字段保全断言。


### B2 S3/S4 KAF 原事务准入与异步恢复（2026-09-13）

关闭 `e512a0edf` 的历史 claim RED：冻结 execution policy 显式贯通 bootstrap/container→CustomProcessEngine→KafDelegationService，以及 BPMNWorkflowController→KafDelegationController 的独立构造链。测试调用显式 standard/candidate；无默认 standard 或可变 setter。forClient 保留冻结策略和调用方 owningTx。policy.IsCandidate 仅描述模式，所有调用先 BindEnt，nil policy 不得绕过写检查。

候选原事务从 tenant/task→ProcessInstance.ExecutionWorkItemID 校验成员，NULL 拒绝，不猜测业务字符串或回填历史。claim INSERT 与随后独立 lease CAS 分别准入；已 applied ledger 先只读查验回放，避免 INSERT 探测。finalizeKafAction 改为受保护事务，finalizeAppliedKafAction 与 persistNonCompletingAction 在原事务首写前准入。completion、callback retry/recovery、receipt create/update、runKafFencedWrite 也在各自原事务检查，原 token/lease/version fencing 保留。只有 ErrDenied 映射 Forbidden，基础设施故障保留 cause。

恢复测试同时复现现有缺陷：异步 KAF recovery 调用了仅同步 handler 提供的 callback contract filter，重新开放 scope 仍报 `callback handler has no synchronous contract`。异步恢复现复用首次完成的 validateAndCloneBPMNParticipantVariables 校验，再设置权威 descriptor.action；保持 KAF 不是同步 CallbackContractProvider，未知 handler 仍拒绝。

本机 candidate-delivery/b2 证据：

- `s4-kaf-writes-pg.log`：历史 claim 已 false、ledger 0→0；失败仅因旧 completion 文本断言，已改用 ErrDenied 语义断言。独立审阅指出错误分类，已修复。
- `s4-kaf-claim-final-pg.log`：真实新成员 claim、非空 digest、重复领取 InProgress；scope closed 时拒绝重取已过期 lease，ledger 完整字段不变，完整基础测试 PASS。
- `s4-kaf-recovery-final-pg.log`：恢复 scope 拒绝通过，但重新 active 遇到上述异步合同错误，未作为成功证据。
- `s4-kaf-recovery-verified-pg.log`：最终完整真实私有 PG TestCandidateIntakeCreationBoundary PASS、未 skip。包含 claim 拒绝/成功/重取范围、新成员 completion/fence 写后故障回滚与精度重放；已完成任务回执置 pending 后，closed scope 拒绝恢复，相关 receipt/callback 完整字段与 base/extension/task/instance/ledger 等不变；active 后原引擎恢复成功。
- `s4-kaf-recovery-regression.log`：service/service-bpmn/controller/service_request 四包 KAF/BPMN/Access/SSLVPN/Callback 定向回归 PASS；`s4-kaf-recovery-build.json`：最终后端全量构建 exit 0。
- `s4-kaf-integration-compile.log`：integration 标签下五包编译 PASS（其后仅恢复实现与新增测试改动，由最终构建、定向回归和真实 PG 验证）。编译不代表目标环境测试运行。

独立 reviewer `review_execution_scope_s1` 最终确认错误分类、构造传递、原事务、异步变量边界与最终 PG PASS 无阻断。**仍属分段验证**：claim 与 completion 使用不同 fixture ledger，并未证明完整 ExecuteAction；初始审批/provider 仍为前置 fixture。历史 applied claim 专项、active scope 下过期 lease 成功重取、scope 关闭后全部 finalize/non-completing 分支及 HTTP 入口还需真实专项测试。CreateDelegatedTask 两条生产路径、非 KAF 通用 callback/outbox worker、共享能力、S4其余/S5/S6/B3 尚未完成。固定 CandidateSHA 不变，候选不启动，未操作 WSL/共享库、推送或合并 main。


### B2 S3/S4 KAF 委派任务生成事务（2026-09-13）

在 `de22553c2` 后补齐 CreateDelegatedTask 两条生产写入口的成员准入。原 CreateDelegatedTaskWithClient 已移除，改为显式 CreateDelegatedTaskTx(*ent.Tx)；engine 传 owningTx，不新增并行实现。两条路径统一在原 tenant/actor 读取核验之后、首次 ProcessInstance 更新之前，使用该事务和冻结 policy 检查 ProcessInstance.ExecutionWorkItemID 与 scope 成员。task、audit、outbox 沿用原事务及结构化引用；调用方保持 commit/rollback 和 lease fence 所有权。

本机 candidate-delivery/b2 证据：

- `s4-kaf-generation-red.log`：真实历史 WorkItem 仍可生成委派任务，ErrDenied 断言失败，构成有效 RED。
- `s4-kaf-generation-verified-pg.log`：最终完整真实私有 PG TestCandidateIntakeCreationBoundary PASS、未 skip。独立事务和加入原事务两入口均拒绝历史对象；新成员生成任务并传递 outbox 结构引用。joined 主动回滚，以及 owned/joined 两路径 outbox 实际写入后故障，均保持流程完整字段与 task/audit/outbox 数量不变。
- `s4-kaf-generation-regression.log`：service/service-bpmn/controller/service_request 的 KAF/BPMN/Access/SSLVPN/Delegation 定向回归 PASS；其后只补 joined_fault 测试，由最终真实 PG 验证。
- `s4-kaf-generation-build.json`：后端全量构建 exit 0。

独立 reviewer `review_execution_scope_s1` 对两条入口、forClient owningTx 传递、原事务顺序及真实 PG 基础/owned fault 验证限定审阅无阻断；其指出的 joined fault 验证缺口随后补齐并实际 PASS。仍未运行真实候选 BPMN 节点推进到生成入口；本次为真实生成服务边界测试。完整 ExecuteAction、HTTP/审批/provider、通用 callback/outbox worker、共享能力、S4其余/S5/S6/B3 继续未完成，不能以本检查点放行候选或G2/G3。固定 CandidateSHA 不变，无 WSL/共享环境操作、候选启动、推送或 main 合并。


### B2 S4 通用 Outbox Worker：真实混排 RED（2026-09-13）

在 `9fbb96b5d` 后转向通用 worker。新增测试调用真实 OutboxDeliveryWorker.DispatchOnce 和 bootstrap 同种 clients.System 受限跨租户连接，不替代领取/恢复实现；测试 receiver 仅记录本地 ID，不外发。四条旧 outbox 在039前建立并保持 NULL execution reference，新成员事件持有真实 intake 生成的结构引用，另混入未准入租户 NULL-ref 待办。其他已有 fixture 类型被 reserved，不引入无关处理。

`candidate-delivery/b2/s4-outbox-worker-mixed-red.log` 当前 **FAIL，尚未修复**。全部历史完整行断言持续检查，实际结果：旧 pending→published，旧 unknown→blocked/attempt=1，旧 expired publishing→published，旧 ambiguous publishing→blocked/attempt=1，未准入租户待办→published。receiver 也收到了历史/外租户事件。首次 `s4-outbox-worker-red.log` 只在第一条 require 失败中止；最终 mixed 日志才是全部分支证据。新测试保持 RED，不是交付成功。

独立 reviewer `review_execution_scope_s1` 确认真实 worker 红测有效，并核对必须覆盖的原 SQL：claimDue 的 ambiguous query/update/audit、expired recovery、pending SELECT 与最终 claim CAS；BlockUnknownPendingEventTypes 的 query/update/audit；markDeliveryAttemptStarted、MarkRetry/WithAudit、MarkPublished、MarkDeliveryUnknown、markTerminal(blocked/dead_letter)。每个权威 UPDATE 都须含 manifest、event tenant、结构引用、member、active scope 条件，不能只筛查询结果；audit 仅随成功更新在同事务提交。

下一步实现约束：SystemContext 使用独立跨租户角色，不能直接调用拒绝 system bypass 的单租户 BindEnt，也不能放宽该 API。需由冻结 policy 在 worker 原事务核对实际 session_user 的 candidate runtime binding、deployment/tenant/scope 清单和只读权限，再生成原 SQL 的成员 EXISTS 限制；绑定撤销、scope关闭和数据库错误明确失败，不伪装空队列。当前 systemTablePrivileges 不含执行范围表，role 准入白名单与 candidate 测试授权须同步审阅；不得授予成员写权限或改变共享/B环境配置。NewOutboxEventRepository 还被生产者使用，不能为适配worker静默改变enqueue/原事务语义或引入默认standard旁路。

后续真实负测还需外租户有真实成员而不在本manifest的场景（本次外租户为NULL-ref，只证明违规处理，不能单独证明tenant predicate）、claim后关闭scope逐入口attempt/retry/finalize保全、audit完整记录和并发恢复。S4/全交付仍未完成，固定CandidateSHA不变，未启动候选、操作WSL/共享库或推送/合并main。


### B2 S4 通用 Outbox Worker 原事务范围隔离（2026-09-13）

在 `801150912` 的真实混排 RED 后，通用 repository 已接入冻结 ExecutionPolicy。新增 WorkerPredicate 在原事务核验实际 session_user 的 candidate binding 和全部 manifest scope，再将 event tenant、结构化 execution_work_item_id、member、active scope 与 binding 的 EXISTS 限制附加到原 SELECT/权威 UPDATE。单租户 BindEnt 保持拒绝 system bypass。system role 准入仅允许三张 scope 表可选 SELECT，不放开写权限或所有权；standard 部署不强制新增授权。这里的授权仅在本机随机测试数据库内执行，未修改 B/共享环境。

claim 的 ambiguous/expired recovery、pending SELECT 和 claim CAS，unknown-type 阻断，以及 attempt/retry/retry-audit/published/delivery-unknown/blocked/dead-letter 均受保护；原自动提交转换改为明确事务，原 claim token/expiry/status 条件保留。审计跟随成功 UPDATE 在同事务写入。两处 bootstrap 及调用方显式传 policy；三个纯 producer helper 与 repository.Enqueue 共用唯一 enqueueOutboxEvent 实现，保持原业务事务，不引入默认 standard 或第二套入队实现。

本机 candidate-delivery/b2 验证：

- `s4-worker-predicate-update-pg.log`：真实 SELECT 和 UPDATE 检验成员过滤；closed scope、撤销 binding 拒绝，数据库权限错误保留基础设施错误分类。
- `s4-outbox-worker-verified-pg.log`：最终完整 TestCandidateIntakeCreationBoundary PASS、无 skip。真实 DispatchOnce 仅交付候选事件；四条历史 unknown/pending/expired/ambiguous 与外租户 NULL-ref 及真实成员事件，status/attempt/claim/时间等完整字段不变，audit 完整快照不变。外租户成员由其独立 policy 与原 INSERT 触发器登记，未补录历史成员。
- 同一最终 PG 测试覆盖七个真实领取后的 attempt/retry/retry-audit/published/unknown/blocked/dead-letter 转换：分别关闭 scope、撤销 system binding，拒绝时事件和 audit 完整字段不变，恢复后正常转换。audit 快照按 ID 排序。
- `s4-outbox-worker-regression.log`：service、intake、service_request、database 四包 Outbox/Delivery/Kaf/Creation/Runtime/Execution 定向回归 PASS。
- `s4-outbox-worker-build.json`：后端全量构建 exit 0。`s4-outbox-worker-integration-compile.log`：integration 标签下 tests/integration、tests/e2e、service_request 编译 PASS；此项仅编译，不代表 E2E 运行。

独立 reviewer `review_execution_scope_s1` 两次只读复审无阻断，确认构造、全部原 SQL 写分支、事务/audit 与新增负测支持上述限定结论；建议的 audit 排序已落实并由最终 PG 验证。此前 generic worker RED 已修复，**S4 整体仍未完成**：candidate 并发领取/恢复、审计实际写后故障回滚、其余 callback/周期执行仍需完成；共享业务能力、S5/S6/B3 和完整业务验收继续未完成。本机私有 PG16 synthetic 证据不替代 B 的 PG17 环境准入。固定 CandidateSHA 仍为 d7470a32dbb87acc9b5e4d9a895a146410723561，候选不启动，无推送/main 合并或 WSL/共享库操作。


### B2 S4 Outbox 并发、过期恢复与审计回滚（2026-09-13）

在 `065226f1e` 后扩展真实私有 PG 测试，不改生产逻辑。`s4-outbox-worker-recovery-pg.log` 完整 TestCandidateIntakeCreationBoundary PASS，无 skip。两个并发 repository claim 经同一开始信号竞争12条候选事件，合计12个唯一ID；选取一条未写attempt的事件使lease过期后，原claim恢复为新token，旧token无法完成，新token完成成功。另一条已写attempt的过期事件转blocked、attempt=1，且仅一条delivery_unknown审计。这里只模拟持久化lease过期，不声称完成进程kill/restart或调度周期测试。

retry-with-audit、delivery-unknown、ambiguous recovery 和 unregistered阻断四分支在 System AuditLog 的真实 next.Mutate 成功后注入错误；计数证明到达真实audit写，错误后事件全字段及按ID排序的audit全表快照均回滚，解除故障后成功。测试未实发，仍用随机私有数据库/受限运行角色；固定CandidateSHA不变，未启动候选或修改WSL。S4其它callback/周期执行、S5/S6/B3和G2/G3仍未完成。


### B2 S4 BPMN callback 历史保全 RED（2026-09-13）

Outbox 并发/回滚测试提交 `56879f7c9`；独立 reviewer 确认限定结论，但起跑屏障不保证数据库语句确定性重叠，不能声称覆盖所有竞态。按审阅补齐解除audit故障后的目标状态及新增恰一条audit断言，并在本轮完整PG运行中PASS。

转入下一未覆盖入口：039前建立NULL execution ref的ProcessInstance和两条historical callback（pending与lease过期processing），通过真实 CustomProcessEngine.ProcessPendingCallbacks(context.Background) 与 SetCallbackCandidateClient(clients.System) 执行一次扫描。未知handler和简化definition只触发原claim/retry，不调用企业provider。`s4-callback-worker-historical-red.log` **FAIL，未修复**：历史pending attempt0→1，next_attempt_at/last_error_class/updated_at改变；历史processing→pending、attempt2→3、lease被清除。真实扫结果completed=0及error不掩盖其已经改写历史。其它原有scope/Outbox测试通过，新callback RED不能计为整体PASS。

下一实现必须覆盖processPending与processExecutionKeys两扫描、claim/retry/completeWithClient/persistCallbackOutcome、enqueue/enqueueBlocked及executor内token/task/instance原事务推进。结构归属沿callback.process_instance_id→process_instances.execution_work_item_id→member，禁止payload推断。System只读扫描与Tenant写事务分别核验真实绑定，不能仅过滤扫描或将单租户Bind改成system bypass。原lease/CAS及audit事务必须保留。固定CandidateSHA不变，未启动候选、修改B/WSL或共享数据库；S4/S5/S6/B3及G2/G3未完成。

独立 reviewer review_execution_scope_s1 确认 callback RED 为真实claim→失败→retry改写历史。转GREEN必须补充可处理的新成员和结果断言，避免“全部报错/全部不执行”伪通过；scope/binding失效另有明确错误断言。当前红测不单凭scanErr判断成功。


### B2 S4 BPMN callback 范围隔离及新成员真实推进（2026-09-13）

在 `8a942e4b1` 的历史 RED 后，callback 两扫描 processPending/processExecutionKeys 使用原扫描事务的结构化 predicate；跨租户 discovery 使用 SystemContext，处理前结束只读事务。claim/retry、completeTx、persistCallbackOutcome 的权威 UPDATE 均附带同范围条件。engine 领取后读取及两类推进事务按 callback 行核验范围，原token/lease条件不改。原 completeWithClient 已改为明确 completeTx，enqueue/enqueueBlocked 传原 owningTx；candidate 入队要求instance成员，blocked UPDATE也附带predicate，audit仍在调用方原事务。

共用原 executionMemberPredicate：TenantPredicate保持单租户绑定、CallbackPredicate沿process_instance_id→instance.execution_work_item_id→member关联，同时核验tenant。System角色仅允许额外可选SELECT(id,tenant_id,execution_work_item_id)三列，不授予process_instances整表读取、业务变量读取或任何写权限；额外列会被角色准入拒绝。授权仅在任务随机私有数据库fixture内发生，没有改B环境。

本机 candidate-delivery/b2 证据：

- `s4-callback-worker-final-pg.log`：最终完整 TestCandidateIntakeCreationBoundary PASS，无skip；两条历史callback完整字段不变，新成员经声明的本地handler和真实CustomProcessEngine从Current推进End，callback与instance均completed、handler仅调用一次。
- scope关闭、system binding撤销、tenant binding撤销三个场景，真实扫描明确失败、completed=0、handler未调用，callback全表完整快照不变；恢复配置后上述新成员正常推进。
- `s4-callback-worker-final-regression.log`：service、database、service_request、controller四包Callback/Outbox/Execution/Runtime/Kaf/BPMN回归PASS。此前一次因standard测试未适配enqueue签名编译失败，已修复且原actor断言保留。
- `s4-callback-worker-final-build.json`：后端全量构建exit0。`s4-callback-worker-integration-compile.log`：integration标签service与tests/integration编译PASS，仅编译不代表目标环境运行。

独立 reviewer review_execution_scope_s1 分两次只读复审无明确阻断，核对原事务、结构引用、精确身份列读取及blocked UPDATE补强。仍有明确验证缺口：candidate按executionKeys内联入口、入队正负与故障回滚、user-task callback、claim后撤销、retry/blocked/optional audit及推进故障；标准回归不替代这些candidate PG专项。既有handler执行与token推进是分段边界，不声称整条外部效果与callback状态单事务；scope错误经既有sanitized advance error后不可按cause细分。通知/SLA/escalation、S5/S6/B3与完整G2/G3未完成，S4不勾选。固定CandidateSHA不变，无候选启动、WSL/共享变更、推送/main合并。


### B2 S4 通知 Worker 历史保全与发送后拒绝写回（2026-09-13）

在 `a0a1ddc4d` 后处理 TicketNotificationService 后台投递。构造必选冻结policy，bootstrap/container和tests显式传递，无默认standard。ProcessPendingDeliveries以SystemContext在原扫描事务使用WorkerPredicate(tenant_id,ticket_id)，处理前结束扫描事务；claim/complete/retry/fail和过期processing的delivery_unknown更新均在明确事务附带同SQL成员条件，原status/lease条件保留。无新增系统角色权限。

本机 candidate-delivery/b2 证据：

- `s4-notification-worker-red.log`：039前旧pending→failed且attempt0→1，旧expired processing→failed/delivery_unknown且lease清除，真实RED。快照使用PostgreSQL row_to_json原始完整行，包含Ent JSON隐藏的attempt/lease/error字段。
- `s4-notification-worker-delivery-pg.log`：最终完整TestCandidateIntakeCreationBoundary PASS，无skip。所有历史完整字段保全；新成员缺少email transport显式failed/attempt1/delivery_target_invalid，不伪报成功。本地声明connector经原Manager与worker收到恰一条ID并标记sent；scope关闭时扫描ErrDenied、发送0次、完整行不变。
- 另一条本地connector.Send记录ID后关闭scope，发送已发生但完成写回拒绝，通知保持processing/attempt1且无sent_at。重新active并模拟lease过期，原恢复入口转failed/delivery_unknown且接收计数不增；不声称能取消已发生发送或完成真实provider验收。
- `s4-notification-worker-regression.log`：service/controller/router/bootstrap四包Notification/Workflow/Callback/Outbox/Execution定向回归PASS，container编译(no test files)。`s4-notification-worker-build.json`：后端全量构建exit0；`s4-notification-worker-integration-compile.log`：integration标签service/tests/integration编译PASS，仅编译。

独立reviewer review_execution_scope_s1两次只读复核无新增阻断，确认同SQL范围和本地交付限定。剩余candidate专项：binding撤销、并发领取、retry/fail/complete数据库故障回滚；生产者及mark-read等共享写入口仍属S3未完成。原worker仍只汇总retry/fail错误，不能据此区分scope与数据库故障。SLA/escalation及callback剩余专项、S5/S6/B3和完整G2/G3未完成，S4不勾选。固定CandidateSHA保持d7470a32dbb87acc9b5e4d9a895a146410723561，候选未启动，无WSL/共享数据库操作、企业实发、推送或main合并。


### B2 S4 SLA Monitor 历史违规写入 RED（2026-09-13）

在 `e479b8d3d` 后建立真实CheckSLAViolations测试。039前设有效SLADefinition与历史过期响应/解决deadline；测试中新成员也有有效定义及过期deadline。`s4-sla-monitor-confirmed-red.log` **FAIL，尚未修复**：旧历史sla_violations集合从[]新增response_time、resolution_time两条；非致命历史JSON断言后，新成员精确2条违规独立断言通过。其它既有candidate子测试PASS。

首轮 `s4-sla-monitor-historical-red.log` 因fixture definitionID=0导致创建失败，NewViolations=0，不作为隔离RED证据；有效定义后的valid-fixture-red首次确认历史写，最终confirmed-red同时证明新成员真实写路径。独立reviewer review_execution_scope_s1确认该限定RED有效。

已核对原写链：monitor CheckSLAViolations预加载/分页查询全部租户记录，createViolation独立INSERT后直接调用NotifySLABreached及eventbus.Publish；SLAAlertService的CheckAndTriggerAlerts/TriggerSLAWarning独立入口、checkAndCreateAlert的重复/cooldown读取、history INSERT与NotificationSent UPDATE均需原事务成员核验。critical分支还有直接email SendTicketNotification。不能仅过滤扫描或把client替换txClient就宣称外部效果可回滚。

下一接入须按单WorkItem原事务重读deadline/cycle、成员及重复条件，通知意图进入既有投递边界，事件使用可靠提交后机制；不新增candidate专用引擎。monitor当前创建失败只日志、warning只bool、alert Exist错误忽略/创建失败continue、通知失败仍NotificationSent=true，需明确错误传播，避免范围拒绝/数据库失败被当成功。当前RED只覆盖violation，不证明warning/critical/notification/eventbus；上述路径及升级扫描、其它专项/S5/S6/B3/G2/G3未完成。固定CandidateSHA不变、候选保持停止，无WSL/共享库操作或实发、推送/main合并。


### B2 S4 SLA 前置：调用方事务内通知意图（2026-09-13）

在 `f8b7043cb` 后先补原事务通知贡献能力 `TicketNotificationService.EnqueueNotificationTx`，**尚未接入monitor/alert生产调用，SLA历史RED未修复**。该方法需要调用方tx、稳定DeliveryKey及明确目标/接收人，Bind/Require范围与tenant/active recipient检查；站内通知复用唯一createInAppNotificationPair，email/sms/push只写现有TicketNotification pending，后续交给已隔离Worker。方法不commit、不发送、不另建通知表或worker。偏好服务浅复制到tx.Client，查询与调用方配置修改共享事务快照。

按既有tenant/ticket/user/DeliveryKey跨channel检查Type/Content冲突，已materialized recipient的渠道不随重放时偏好变化增加。首次全禁用偏好不写任何意图，可正常返回；没有durable suppression receipt，不代表已发送或持久化抑制。并发重复由既有唯一约束拒绝，调用方必须回滚/重试；本次不声称完成并发专项。

证据：`s4-sla-notification-intents-verified-pg.log` **仅notification_intents子测试PASS**，不是完整candidate套件PASS。真实私有PG证明：历史拒绝、主动rollback、commit后的两条TicketNotification与一条unified Notification、email pending且sent_at空、重复不增、内容冲突拒绝；unified Notification实际INSERT后故障，使调用方WorkItem标题及两表整体回滚。同事务新建email-only偏好被读取；切in_app-only后不同内容拒绝、同内容重放原email ID/渠道/内容及数量不变；全禁用后冲突仍拒绝，新key零写。最终偏好事务rollback。

`s4-sla-notification-intents-final-regression.log` Notification定向回归PASS；`s4-sla-notification-intents-final-build.json`后端build exit0，其后仅加强测试断言，由verified PG验证。独立reviewer review_execution_scope_s1发现的P2（按当前channel检查导致偏好切换绕过内容冲突）已修复，复审确认无新事务逃逸；建议补强的重放后直接行数/ID断言已落实并PASS。

下一步继续把violation/alert持久化、通知意图和可靠事件意图纳入原事务，移除其直接发送/提交前发布路径；还需单项重读与幂等、错误传播、alert/升级周期与故障测试。S4/S5/S6/B3/G2/G3均未完成，原SLA confirmed-red仍是未解决门禁。CandidateSHA不变，无候选启动、企业发送、WSL/共享数据库改动、推送/main合并。

### B2 S4 SLA violation 原事务接入（2026-09-13）

在 `af1106661` 后，前述历史违规 RED 已由本增量修复。SLAMonitorService 构造必选 frozen ExecutionPolicy；扫描在原事务附成员 SQL 条件并按 ID 分页，每个 WorkItem 的写事务重新读取 deadline/cycle、成员与重复记录。首个新增违规前以 tenant/ID/version/member 做 version+1 CAS，两类违规共享一次 fence；冲突及写入错误向调用方传播，不再日志后伪报成功。无新增违规不更新版本。

违规、站内通知/外部 pending 通知、结构化 `sla.breached` OutboxEvent 同事务提交。旧 NotifySLABreached 直接发送路径移除，改用唯一 EnqueueSLABreachedTx→EnqueueNotificationTx。通用 outbox registry 注册 SLA handler，验证 tenant/WorkItem/violation/event 身份，保留字符串 SLA 事件契约与持久化 occurrence/breach 时间；无 bus 显式 blocked，Publish 错误按 delivery_unknown 阻断自动重发，未声明 replay-safe。未进行任何企业投递或 Redis 发布验收。

实际 bootstrap timer 复用已配置 monitor，经 runSLACycle：候选只发现 frozen manifest IDs，standard 通过 system client 只读发现，逐租户 WithTenantID 撤销 SystemBypass；启动前检查 monitor/policy/discovery。移除无调用的 StartSLAWatcher/CheckAllTenantsSLA，避免平行周期入口。候选若配置尚未隔离的 alertService，扫描前显式拒绝；并未将告警能力计为完成。

本机 `candidate-delivery/b2` 证据：

- `s4-sla-atomic-events-red.log` FAIL：历史违规变化且新成员缺少 durable SLA event。
- `s4-sla-atomic-final-pg.log` **完整 TestCandidateIntakeCreationBoundary PASS，无 skip**：历史违规原始 JSON 不变；新成员两违规、两事件、四通知（站内+email pending）；重复扫描无新增及版本不变。unified notification 实际写后故障、第二条 outbox 实际写后故障使违规/通知/事件及版本整体回滚，解除故障后两违规成功。closed scope 拒绝；两并发扫描最终精确两违规/两事件、版本仅+1。并发起跑不等于确定性 SQL 竞态或进程重启覆盖。
- `s4-sla-atomic-race.log` SLA 子测试 race PASS（删除无调用旧 watcher 前，写路径相同）。`s4-sla-atomic-final-runtime.log` service/bootstrap/database SLA/事件/启动/冻结清单定向回归 PASS。handler 使用本地假 bus 验证契约与不确定错误；bootstrap 标准发现用 SQLite 实际查询验证上下文，不替代 PG 角色准入证据。
- `s4-sla-atomic-final-build.json` 后端全量 build exit0；`s4-sla-atomic-integration-compile.log` integration_postgres 标签编译 PASS（删除无调用旧 watcher 前），不代表该标签运行验收。
- 独立 reviewer `review_execution_scope_s1` 的后台 tenant context、启动依赖两项阻断均修复并复审通过；最终旧入口删除与分项依赖测试复审无新阻断。

本次完成的是 SLA violation 增量。SLAAlertService/warning/critical transport、escalation、其它共享业务写入口与完整周期/S5/S6/B3/T3/T4/G2/G3 仍未完成。CandidateSHA 仍为 `d7470a32dbb87acc9b5e4d9a895a146410723561`，候选保持停止；无 WSL/共享数据库操作、企业发送、推送或 main 合并。

### B2 S4 SLA alert 原事务创建与通知意图（2026-09-13）

在 `73cc9a3e1` 后接入 SLAAlertService 两个直接入口。构造必选冻结 policy；统一 triggerAlerts 在原事务检查 tenant/成员/deleted，并读取当前 deadline/cycle、规则、duplicate/cooldown，首次写 history 前执行带成员条件的 Ticket version CAS。history 与通知意图共用事务，错误明确返回；重复/cooldown 保留现有 ticket/rule 共享语义。时间比例采用当前 SLA cycle 并扣除已计入 deadline 的暂停延长。

通知渠道取规则配置与用户偏好的交集，空集合不回落默认渠道，未知渠道显式失败。有配置渠道但缺 notifier 时在 CAS/首写前拒绝。移除 NotifySLAAlertLevelChanged 的同步发送及 critical 额外邮件，去掉无投递证据的 NotificationSent=true。**完整发送关联及状态投影尚未实现**：新 history 暂保留 false，不将队列创建等同发送完成；monitor 的候选告警拒绝仍保留，bootstrap 暂未接入告警周期，下一增量必须完成关联/投影及接入才能放行。

证据（本机 candidate-delivery/b2）：`s4-sla-alert-direct-confirmed-red.log` 有效 RED，两入口各自实际新增 039 前历史 alert history；新成员独立触发且恰一 history，但 email pending=0、notification_sent=true。首个 direct-red 因测试表名拼写错误，仅为 fixture 失败，不计隔离证据。

`s4-sla-alert-verified-pg.log` 完整 TestCandidateIntakeCreationBoundary PASS，无 skip。历史 JSON 保全、两个直接入口新成员/history/email pending、重复与版本不变；history 与 unified notification 实际写后故障均使 history/两张通知表及版本回滚，解除后成功。空渠道、email-only 与用户禁 email、inapp交集通过；reopen/pause 是 owner 设定的前置状态，仅验证当前周期/暂停计算，不代表生命周期 E2E。`s4-sla-alert-final-regression.log` SLA/Notification/AlertChannel service/bootstrap 回归 PASS，`s4-sla-alert-final-build.json` 后端 build exit0，integration-compile 标签编译 PASS（未运行该标签集）。独立 reviewer review_execution_scope_s1 复审无新增阻断。

下步按结构化 history→notification 关联和实际状态查询投影处理发送完成，禁止从 DeliveryKey 反推授权；还需完整告警周期、escalation、S5/S6/B3/T3/T4/G2/G3。固定 CandidateSHA 不变，候选保持停止；无共享数据库/WSL操作、企业发送、推送或 main 合并。

### B2 S4 SLA 通知结构关联、投影及 monitor 接入（2026-09-13）

在 `f579a866d` 后补齐上一增量的发送状态缺口。新增独立 ordinary migration `040_sla_alert_notification_provenance`，依赖039/037，不更改038退休合同。TicketNotification 增 nullable immutable sla_alert_history_id，history 增 nullable immutable notification_tracking_version；旧行维持NULL且不回填，新告警显式version1。复合FK绑定history/id+tenant+ticket，trigger禁止改指/清空/旧NULL补填，并要求新关联指向version1。version1不能写notification_sent=true；新函数剥离PUBLIC与角色默认ACL，不扩大既有表权限。Ent按原go generate入口生成，最终修改限定两个实体及共享生成文件。

通知原事务显式携带结构关联并核对history tenant/ticket/version，重放也核对关联；DeliveryKey不用于推导业务身份或授权。GetAlertHistory按当前页history关联批量聚合：version1至少一通知且全部sent/read才为true；零通知、pending/processing/failed等均false。legacy NULL继续展示旧记录的sent事实。Worker仍只更新通知权威状态，不增加history查询/写权限。monitor现已接入已配置alert服务，两个入口各自重新准入；warning错误不再吞掉，日志改为recorded。原子性限定各领域事务，整轮扫描不因后续失败撤销先前已提交事务。

本机 candidate-delivery/b2 证据：

- `s4-sla-alert-projection-red.log`：真实站内通知已sent但history API仍false，in_app/paused_warning两例有效RED。
- `s4-sla-projection-final-pg.log`：完整TestCandidateIntakeCreationBoundary PASS，无skip。迁移前删除两个新列及新复合唯一索引，040实际重建；旧行原始JSON去除新增字段后相等，新增字段全部NULL，默认函数ACL无执行权。验证关联改指/清空、NULL回填、unknown version插入、legacy关联拒绝。复合FK在私有事务临时禁用伴随user trigger后独立以23503拒绝错误ticket/tenant/ID，事务回滚恢复trigger。
- 同一最终PG测试验证：真实inapp提交→API sent；新零渠道false；legacy true事实不变。新关联email由原Worker+原clients.System在无email provider时明确failed，无history权限扩大；随后owner设置pending/processing/sent/read/failed，仅验证混合状态投影，**不作为外部实际送达证据**。实际monitor包含alerts扫描，新成员创建告警，历史alert JSON不变。
- `s4-sla-projection-migrations.log`整个migration包PASS；包括已039待040、缺039或037却有040拒绝，以及原retired ledger升级顺序。`s4-sla-projection-final-regression.log` service/bootstrap定向PASS；final-build.json后端build exit0；integration-compile.log标签仅编译PASS。
- reviewer review_execution_scope_s1发现的Ent预建索引掩盖升级问题、DB legacy关联缺少约束已修复，最终只读复审无阻断。

040仅在任务私有PG16数据库执行，未对WSL/共享源或候选实际环境执行。专门的039最小schema注册fixture仍限定039，不伪称覆盖040；040完整Ent升级与实际业务由上述fixture验证。下一步升级链仍需原事务/范围接入，并移除其notification_sent=true等旧写路径，之后继续剩余S3/S4、S5/S6、B3/T3/T4/G2/G3。固定CandidateSHA不变，候选保持停止，无推送/main合并或企业实发。


### B2 S4 自动升级原事务与提醒回执（2026-09-13）

在 `5fa4ae3e6` 后接入自动 matrix/long_pending/unassigned 三条链。EscalationService 构造显式 frozen policy；扫描按 tenant/member 在事务内分页，单项原事务重新准入、读取当前 WorkItem/规则/级别。matrix 以 Ticket version CAS + history level CAS 同事务推进所有到期级别、写审计及 durable 通知意图；managed history 复用040结构引用，不对旧history补填。已解决/关闭及早于当前 SLA cycle 的旧告警跳过，显式用户必须同tenant且active，声明角色无接收者报错。移除不再参与执行的 matrixSvc 注入接口，SLA配置在原事务读取；新增 notifyUserIDs JSON整数校验。没有同步企业投递。

非规则 long_pending/unassigned 不再创建 AlertRuleID=0 的无效SLA history，复用 WorkItem operation receipt：每 WorkItem/SLA cycle/提醒类型一次，当前cycle起点满足原24h/2h阈值后，version fence、通知意图、审计共同提交。重复不增加版本；所有偏好禁用时仍记录本周期已处理回执，同周期再次取消分配不会再次提醒。沿用原角色筛选和阈值，本增量未重新设计接收人策略。bootstrap复用已配置notifier的实例，SLA/升级共享冻结候选租户发现，standard仅用system client发现IDs，各租户执行上下文不带SystemBypass，启动前核对依赖。

证据位于本机 `candidate-delivery/b2`：

- `s4-escalation-matrix-red.log` 有效RED：历史alert level 0→1，候选新成员独立推进但无升级专属pending通知。
- `s4-escalation-final-pg.log` 完整 TestCandidateIntakeCreationBoundary PASS，无skip：历史alert原始JSON不变；新成员真实matrix推进+email pending；owner设置重开周期后旧alert不再推进。历史非成员设为48h未分配后，WorkItem原始JSON/通知数量不变且无提醒回执。新成员两类提醒各一次、重复无版本变化、新周期年龄不足不提醒、年龄满足后各新增一次，无伪SLA history。
- 同一PG测试注入long_pending的unified Notification/AuditLog实际写后故障，版本、通知双表及提醒回执回滚；解除后准确生成一次回执。仅任务私有PG16，不替代WSL PG17环境准入。
- `s4-escalation-final-regression.log` service/bootstrap 的 Escalation/SLA/Notification/EnabledCapability 定向回归PASS；`s4-escalation-final-build.json` 全后端build exit0。第一次回归发现旧测试引用已移除helper，已改为验证未知角色/用户拒绝；第二次回归发现旧fixture告警早于工单创建，修正时间前置条件，仍保留非法配置拒绝与有效配置30分钟推进断言。
- 独立 reviewer `review_execution_scope_s1` 提出的无效配置接口已移除、旧周期缺口已修复，复审无新增阻断。

边界：重开为owner设置前置状态，不是真实生命周期E2E；unassigned故障回滚尚未单独验证，matrix多级整体回滚及确定性并发CAS仍待补证。真实HTTP TicketService.EscalateTicket及TicketLifecycleService、EscalationService旧无调用手动方法尚未接入；尤其后者仍含无效AlertRuleID=0/notification_sent=true，不将其计为本轮完成。下一步先统一真实手动升级所有者和原事务审计/通知，再处理其它共享写入口、S4剩余专项、S5/S6/B3/T3/T4/G2/G3。固定CandidateSHA仍为 `d7470a32dbb87acc9b5e4d9a895a146410723561`，未启动候选、未修改WSL/共享环境或执行共享迁移，无推送/main合并或企业实发。


### B2 S3 手动升级有效RED及原事务依赖（2026-09-13）

在 `0266df8b0` 后，核实真实生产路由为 router→TicketController.EscalateTicket→TicketService.EscalateTicket（ticket:escalate）。TicketLifecycleService 的同名方法/接口仅测试调用，EscalationService旧零AlertRuleID方法亦无生产调用；后续应保留TicketService唯一HTTP手动所有者并迁移有效行为测试、移除上述平行方法，不能添加长期兼容包装。BPMN ticket_handler 的escalate分支独立使用escalate_to及escalated状态，不在此HTTP入口覆盖内。

本轮只新增失败回归，不修改生产实现。`s3-manual-escalation-contract-red-2.log` 为真实任务私有PG16有效RED：039前generic historicalAlertItems[1]被手动升级，调用未ErrDenied，原始整行JSON的priority/status/assignee/version/updated_at变化；新candidate member的high→critical独立正向通过，但原未分配项被写为硬编码用户1，manual审计回执为0。第二次最高级调用因硬编码用户2不存在而被FK拒绝，**不能将此集成调用作为已观察到降级的证据**；`s3-manual-escalation-priority-red.log` 则直接验证真实TicketService helper，将critical实际返回high，证明降级错误。最初manual-red选择了Incident历史fixture，被已有专业边界拒绝，不计范围越界证据；已改为真实generic后确认。

原方法的另一个必要依赖是事务外goroutine飞书更新。修复不能只删掉这项既有功能：需要原事务冻结update intent（existing mapping ID/GUID、tenant/WorkItem结构引用、actor、稳定操作回执、目标identity、WorkItem version、payload），注册独立update handler并复用现有scoped Outbox Worker，不能冒用creation事件或新增worker。consumer核验真实手动操作回执、当前权限/member、mapping/目标一致，成功回执不能改写GUID；外部调用后不确定错误/成功后落盘失败明确delivery_unknown，不声明ReplaySafe。必须处理同一远端任务跨worker更新顺序，禁止仅凭发送前version检查或进程内锁宣称不会旧快照覆盖新状态。映射尚未创建不能偷偷创建远端任务或假成功。

独立review_execution_scope_s1只读确认调用归属和必要依赖。下一实施顺序已补入原执行计划：稳定手动命令元数据与唯一路径；飞书更新持久化/串行及不确定性；原事务scope/授权/version/审计/通知/事件；真实回滚/重放/权限与并发验证。当前手动升级仍未修复，新RED使当前测试集不再全绿，不将上一轮PASS套用到本HEAD。整个S3/S4和后续门禁仍未完成，CandidateSHA未更新，未启动候选，无共享环境变更、企业发送、推送或main合并。


### B2 S3 手动命令事务核心（2026-09-13）

在 `1554a4030` 的RED后接入真实TicketService HTTP手动命令。TicketServiceConfig显式注入frozen ExecutionPolicy，bootstrap/Container两条生产构造链贯通，缺policy写入拒绝。EscalateTicket改为typed command，HTTP/前端要求reason/version/operationId，可信actor/tenant/source来自认证边界，返回immutable WorkItem receipt。原事务RepeatableRead读取当前generic、actor/当前权限，先查询合法历史回执；只有新写入才Bind/member/version/status检查、更新CAS、通知意图与审计共同提交。重放不修改字段/版本，冲突operationId返回409；权限和scope拒绝403，基础设施错误不暴露原始详情。priority high→critical且critical饱和，未知值拒绝；保留assignee，移除原硬编码1/2/3。

**仅事务核心完成，手动升级全链尚未完成。** 原事务外通知/Feishu goroutine已移除；通知由原队列承接。配置Feishu时在任何业务写入前明确拒绝，直到update intent/consumer/跨worker顺序与不确定性处理完成；该门禁是临时未实现条件，不是最终同步方案或功能交付。旧TicketLifecycleService/EscalationService仅测试调用手动方法和BPMN独立升级尚未移除/接入。新命令仅generic，专业状态禁止经此改写。

本机candidate-delivery/b2证据：

- `s3-manual-command-full-pg.log` 完整TestCandidateIntakeCreationBoundary PASS，无skip。039前原owner以Standard模式运行本次新命令生成receipt，迁移后候选只读重放成功且原始WorkItem JSON不变；**不代表旧发布版本已有此receipt**。历史generic新命令ErrDenied且原始JSON保全。新member真实升级/critical不降级/无虚构assignee/审计1条，重放保持版本，operationId冲突拒绝；actor失效后重放拒绝、closed scope新命令拒绝。Notification与AuditLog实际写后故障使版本/通知回滚，解除后同命令成功。
- `s3-manual-command-final-regression.log` service/controller/bootstrap/container定向回归PASS；新增HTTP错误分类验证permission403、scope403、version409、infrastructure500隐藏细节及缺命令元数据400。它们不等于实际JWT/SSO全HTTP验收。`s3-manual-command-build.json`后端全量build exit0。
- `s3-manual-command-frontend.log` Ticket API Jest 55/55 PASS；契约测试保留同一command身份重试。package-lock与integration worktree逐字一致，当前worktree链接其已有node_modules，仅运行本地测试；任务生成的tracked junit.xml已还原，不改变原报告。未运行浏览器/前端全type-check。
- 初轮编译重复import、外部测试包装类型不匹配已修正；这些编译失败不作为业务RED。独立review_execution_scope_s1指出权限sentinel映射及历史重放时序问题，均修复并复审无新事务/权限阻断。

并发同key冲突后的自动回执恢复、真实HTTP当前角色撤销、Feishu配置门禁/持久更新、旧平行入口及BPMN仍待验证/实现；S3/S4/S5/S6/B3/T3/T4/G2/G3未完成。CandidateSHA未更新，候选未启动；无WSL/共享环境、企业发送、推送或main合并。


### B2 S4 有序Outbox领取前置（2026-09-13）

为手动升级的Feishu durable update补齐跨Worker顺序前置，复用原Outbox而不新增worker/表。handler通过OrderedOutboxDeliveryHandler声明SerialByAggregate，registry注册时冻结；worker传给唯一ClaimDueByEventType实现。候选列表与claim CAS均附相同NOT EXISTS：同tenant/event_type/aggregate_type/aggregate_id且较小ID的前序必须全部published，NULL/未知及所有其它状态均阻挡。外层原scope/member条件保留，前序子查询刻意不按member过滤，避免越过历史未决事件。KAF专属调用及原直接测试调用显式false；新能力未应用到未声明类型。

本机candidate-delivery/b2证据：

- `s4-outbox-ordering-red.log` 有效RED：两个独立真实worker，first本地receiver已进入且阻塞，second把同目标后序published而独立目标仍pending。
- `s4-outbox-ordering-final-pg.log` 完整TestCandidateIntakeCreationBoundary PASS，无skip。first阻塞时same-target successor保持pending，另一目标published；first完成后successor才published。不是仅同时起跑的概率性并发测试。
- 同一最终PG：039前NULL execution ref前序原始row_to_json所有字段保全，新成员同目标后序保持pending/attempt0；blocked/dead_letter/unknown状态与future-due pending均阻挡。真实MarkDeliveryAttemptStarted后owner设置过期，原recovery转delivery_unknown/blocked，后序不越过。
- 本地receiver已返回成功后，published实际写后注入故障，确认事务回滚留下publishing且后续worker不调用successor；过期恢复blocked后仍无第二次投递。这里使用声明的本地接收端，没有企业调用。
- `s4-outbox-ordering-regression.log` service/bootstrap Outbox/KAF/IncidentAlert定向PASS，含注册声明捕获后修改handler flag仍保持冻结结果；build.json全后端exit0；integration-compile.log为integration_postgres标签编译PASS，未运行该标签集。
- 独立review_execution_scope_s1复审SQL别名/原CAS/历史前序/冻结声明与最终故障证据，无新问题。

边界：只证明新协议领取和恢复的顺序前置。生产者必须先在同目标取得事务CAS/锁再插入，序列本身不证明commit顺序；真实远端目标必须共用类型/稳定aggregate键。Feishu producer/handler、mapping/目的地/操作回执绑定、外部不确定性验证和旧在途/直发路径未接入，因此手动升级Feishu门禁不解除。未做生产规模查询性能验收；没有新增索引/迁移。S3/S4/S5/S6/B3/T3/T4/G2/G3及固定CandidateSHA交付仍未完成，候选未启动，无共享环境/WSL变更、企业实发、推送或main合并。


### B2 S3 手动升级 Feishu 持久更新（2026-09-13）

在 `d200b864f` 后接入原事务update intent与既有Worker具名handler，解除配置Feishu一律拒绝的临时门禁。已有映射必须TaskID=GUID且非空，利用现有tenant/taskID唯一约束限定参与目标；不修改旧映射、不自动创建远端任务。Ticket CAS后冻结mapping ID/GUID、destination、actor/operation/request digest/result version、Task快照及结构WorkItem引用，审计回执绑定eventID/payload digest。原事务通知/审计/事件共同提交。handler不声明ReplaySafe，持久claim/attempt/前序、tenant/member、当前actor与ticket:escalate、真实操作回执/摘要和mapping均验证；允许current version大于已提交快照版本。成功仅接受原GUID，完成事务重新核验并锁住当前Outbox行直到mapping完成写提交，防恢复穿透；调用后错误/身份变化/回执失败进入delivery_unknown并阻挡后序。

本机candidate-delivery/b2证据：

- `s3-feishu-update-intent-red.log` 有效RED：合法已配置Feishu命令被旧临时门禁拒绝。初轮green编译发现RequestBody为nullable指针，已补nil拒绝与解引用，不把编译失败作为业务RED。
- `s3-feishu-update-final-pg.log` 完整TestCandidateIntakeCreationBoundary PASS，无skip，任务私有PG16。两次真实命令先排队再由原Worker按快照顺序published，第二次版本已提交时第一条仍正常；重放不新增事件，未领取直调拒绝。错GUID导致unknown，后序保持pending且不再调用本地接收端。
- 同一最终PG覆盖destination/映射/actor变更及payload篡改在调用前blocked；provider error、发送后mapping变化、mapping实际写后注入故障均unknown且不写synced。producer Outbox/AuditLog实际写后失败，WorkItem版本、通知/事件/审计数量全部回滚，解除后同命令只生成一个更新事件。
- 独立审阅发现post-call检查后claim可被恢复修改的间隙；`s3-feishu-claim-lock-red.log` 在mapping mutation前另一数据库连接实际UPDATE成功，明确RED。加外层SELECT FOR UPDATE后同场景竞争写锁超时，原完成事务published/synced；证明两连接锁互斥，不等于完整worker过期恢复E2E。预检事务在provider调用前释放锁，不持锁跨网络。
- `s3-feishu-update-regression.log` service/bootstrap Outbox/Feishu/手动升级定向PASS，build.json全后端exit0，integration-compile.log仅integration_postgres标签编译PASS。最终只读review_execution_scope_s1确认修复，无新增阻断。

边界：仅声明本地接收端，没有企业实发。新协议不覆盖SyncTicketToFeishu/UpdateExistingTicketTask旧直发、旧在途creation或其他类型事件，不据此宣称全局远端顺序。缺mapping拒绝是显式前置条件；未验证真实Feishu配置恢复、SSO/HTTP全链、scope关闭后的此handler专项、完整生产规模与多生产者并发。旧两个仅测试调用手动方法/BPMN及其它共享写入口仍待整理；S3/S4/S5/S6/B3/T3/T4/G2/G3未完成。固定CandidateSHA仍为 `d7470a32dbb87acc9b5e4d9a895a146410723561`，候选未启动，无WSL/共享数据变更、共享迁移、推送或main合并。


### B2 S3 手动升级唯一所有者（2026-09-13）

在 `98de8076b` 后重新检索实际Go调用，移除无生产调用的TicketLifecycleService.EscalateTicket及interface声明、其独用优先级/占位分配helper，以及EscalationService.EscalateTicket。后者原先创建AlertRuleID=0且notification_sent=true的伪SLA记录，删除后不保留兼容包装。当前生产EscalateTicket调用仅controller→TicketService typed command；BPMN独立escalate分支不是该方法，仍待接入。

保留既有测试文件、位置和函数名，生命周期四档priority/critical场景转向真实TicketService命令，显式Standard policy/notifier/repository和当前super_admin fixture；验证版本增加、处理人保全和既定in_progress状态。原EscalationService测试从仅“不panic”改为成功/critical/version/一个audit且无伪SLA history。专业边界保留service_escalate覆盖，移除不存在方法的重复分支。纯优先级表格改为现存纯函数，unknown按新契约明确错误，不再静默保留。没有迁移或删除测试文件/历史目录。

证据位于candidate-delivery/b2：s3-manual-owner-baseline.log修改前受影响测试PASS；s3-manual-owner-tests.log先将旧测试转为新所有者后PASS；s3-manual-owner-final-regression.log service/controller/bootstrap相关生命周期/升级/专业边界/Outbox/Feishu定向PASS；s3-manual-owner-pg.log完整TestCandidateIntakeCreationBoundary PASS，无skip；build.json全后端exit0。中间编译发现遗漏旧helper表格调用，已补齐；helper fixture依赖Repository缺失亦修正。这些不是业务RED，本增量移除重复代码而不新增业务行为。独立review_execution_scope_s1复审无阻断。

Standard/super_admin测试不替代普通角色授权和候选真实路径；候选边界证据来自上述私有PG16全套。BPMN、Feishu旧直发/历史GET副作用、其它共享写入口及S3/S4/S5/S6/B3/T3/T4/G2/G3仍待完成。CandidateSHA不变、候选保持停止，无WSL/共享环境变更、企业实发、推送或main合并。


### B2 S3 BPMN升级原事务缺口有效RED（2026-09-13）

在 `5ff8d1dcf` 后检索真实引擎：executeClaimedServiceTaskCallback先执行Ticket handler，再开启引擎推进事务。原escalateTicket依自己的client先通知再写Ticket，未重新核验执行范围、当前callback租约或操作回执；execution key仅幂等标签。不能用引擎领取准入替代业务原写事务准入，也不能直接套用HTTP升级命令改变流程escalate_to/notify_admin_ids/escalated语义。

新增真实私有PG测试 `BPMN escalation rechecks scope at mutation`：构造新candidate member及持久service_task callback，实际ProcessPendingCallbacks领取/恢复权威变量，再通过嵌入真实handler的测试wrapper在调用前确定性关闭私有scope。未关闭的独立正向先成功升级至critical/escalated。关闭路径sweep返回错误，但原Ticket整行JSON仍priority medium→critical、status new→escalated、updated_at改变而version仍1，保全断言明确FAIL。证据 `s3-bpmn-escalation-scope-red.log` exit1，无skip；scope在sweep返回后恢复，另有cleanup兜底恢复并将故意失败callback隔离为blocked，避免后续扫描重用；fixture数据库结束删除。只有新成员/该原业务写的证据，process/instance/callback为owner前置夹具，不是完整BPMN启动/入队链、历史目标或真实UI/provider验收。独立review_execution_scope_s1确认RED有效；cleanup表名初次拼写错误已按Ent实际表名修复，最终重跑仅保全断言失败。

此检查点只有失败回归，生产实现尚未修复，当前候选边界测试不全绿，不沿用前轮PASS。独立只读设计审阅确认：引擎传播当前callback id/tenant/execution key/lease owner/attempt可信身份，独立workflow command在原事务锁定重读持久callback及instance/完成task，核验结构WorkItem/actor/current权限/接收人/范围与version；输入取持久动作参数，原事务CAS/审计receipt/站内通知意图共同提交，重复仅凭合法receipt。领域提交后的引擎推进失败靠receipt重放，仍明确是两个事务。scope撤销、租约丢失、通知/审计写后回滚及推进失败后重放均须实际验证。

目标仍为设计规定的完整候选交付；S3/S4/S5/S6/B3/T3/T4/G2/G3未完成，CandidateSHA保持原值，候选未启动，无共享环境/WSL变更、企业实发、推送或main合并。


### B2 S3 BPMN升级工作流事务接入（2026-09-13）

在 `7b437d7ab` RED后新增独立TicketService.ApplyTicketWorkflowEscalation，bootstrap注入Ticket handler；移除handler直接通知和Ticket写入。引擎传播原领取的id/tenant/key/worker/attempt，业务事务锁定并匹配processing/未过期租约及CallbackPredicate，锁instance/Ticket，核验结构WorkItem、generic class、service-task initiator/current activity或user-task持久completion actor/已完成task。参数取持久callback，严格优先级/整数接收人/正整数version，actor权限在Replay前；无receipt才核验接收人资格和expectedVersion，新写CAS、站内通知及immutable audit一起提交。保留流程escalate_to/high默认/escalated语义，不能直接调用HTTP手动升级。缺version旧回调显式失败，不补历史payload。

Callback contract新增version及generic typed lifecycle result，沿既有流程输出/回执验证推进。原输出规范化将generic误当subtype，已对canonical class直接识别，同时保持既有change别名测试行为，无扩大别名范围。领域事务和引擎推进分段，后者失败时凭receipt恢复；不以现有字段相等作为幂等证明。

证据位于candidate-delivery/b2：

- `s3-bpmn-escalation-green.log` 原领取后关闭scope的RED变GREEN，正常流程也成功。`s3-bpmn-escalation-full-pg.log` 最终完整TestCandidateIntakeCreationBoundary PASS，无skip，私有PG16 enforce角色。实际ProcessPendingCallbacks处理独立正向critical/default high、新member撤销scope/更换lease owner拒绝、专业Incident/Problem/Change目标拒绝。
- 同一最终PG：Notification/AuditLog实际写后注入故障使Ticket整行及通知/审计数量保全，解除后原callback成功一次；ProcessInstance推进实际写后失败，领域已提交，禁用独立通知接收人后重试仍完成，版本仅+1、审计/通知各一条。独立审阅发现Replay前接收人资格阻塞恢复，已移至首次写前，并通过上述用例复验。
- `s3-bpmn-escalation-regression.log` service/bpmn/bootstrap的BPMN/Callback/生命周期/升级/Outbox定向PASS；build.json全后端exit0；compile.log仅integration_postgres标签编译PASS。原handler测试保留文件/函数名，直接调用无owner明确拒绝；专业业务断言转由真实PG所有者验证。旧extension失败测试的hook原来永久激活导致后续Incident创建失败，现限定该原用例期间，原实际写后回滚断言保留。
- 初轮typed result未声明及generic输出识别问题导致正常流程推进失败，已修正并真实推进成功；没有把其中编译/夹具失败算成业务RED。只读review_execution_scope_s1复审本轮事务主线及修复，无新增阻断。

边界：process/instance/callback为owner前置夹具，运行的是实际领取、领域命令、流程推进，不是完整流程创建/启动链。user-task completion分支、普通actor撤权、租约过期/同worker旧attempt、参数恶意篡改及跨worker同时运行专项仍待覆盖；本轮不是所有BPMN写入口接入，assign/update_status等仍需按S3逐项核验。S3/S4/S5/S6/B3/T3/T4/G2/G3仍未完成，固定CandidateSHA不变、候选未启动，无WSL/共享环境、企业实发、共享迁移、推送或main合并。


### B2 S3 工单详情读取去除飞书副作用（2026-09-13）

在 `bcd1f5c47` 后确认GetTicket在repo.GetByID成功后启动goroutine、开启事务、调用UpdateExistingTicketTask并更新映射，读取本身造成外部写入。按S3“GET历史工单不触发Feishu”要求删除该整段，保留原仓储返回与错误契约，不新增开关或异步替代路径。

`TestTicketReadDoesNotUpdateFeishuTask`保存在现有integration/feishu_creation_delivery_test.go：真实SQLite映射、实际Feishu connector指向httptest本地接收端；先直接UpdateTask正向控制PATCH=1，再GetTicket。s3-ticket-read-feishu-red.log有效RED实际PATCH=2。移除后s3-ticket-read-feishu-green.log读取PATCH仍1、映射JSON原样，另两项原创建意图/手动自动共用创建意图测试PASS。观察窗口1秒为有界动态证据，源码中完整删除读后goroutine提供直接佐证，不声称任意延迟任务的形式化证明。s3-ticket-read-regression.log service/controller读取/Feishu定向PASS，build.json全后端exit0；本轮未重跑完整候选PG，未将SQLite测试写成候选环境验收。独立review_execution_scope_s1只读审阅无阻断。

本轮仅修复GetTicket。剩余六处TicketService业务写调用UpdateExistingTicketTask及FeishuSyncService手动同步API仍绕过新update队列，需继续接入原事务/范围与可靠投递；不删除合法写入应产生的同步功能。S3/S4/S5/S6/B3/T3/T4/G2/G3仍未完成。固定CandidateSHA不变、候选保持停止，未执行企业调用、WSL/共享环境变更、共享迁移、推送或main合并。


### B2 S3 工单编辑范围及标签事务有效RED（2026-09-13）

在 `9654afcab` 后核验TicketService.UpdateTicket三个生产调用：TicketController普通编辑、UpdateSubtask、tool_queue.update_ticket。前两者覆盖req.UserID，普通编辑还有controller CanEdit；工具使用持久invocation actor但未传expectedVersion。服务当前不使用actor进行原事务授权，也无稳定operationId；repo.Update自读后CAS，标签ResolveTagIDsByNames(createMissing=true)在CAS前独立写入；状态通知、SLA违规关闭和Feishu goroutine在Ticket提交后执行，不能据此声称一个编辑命令原子完成。

新增 `ticket edits preserve historical records and reject orphan tag writes`，`s3-ticket-edit-scope-red.log` 为真实私有PG16有效RED，无skip：039前generic historicalAlertItems[1]编辑未ErrDenied，title/updated_at改变、version1→2，并新增标签；新candidate member正常编辑标题/标签、version+1独立正向成功；随后传真实旧version1（当前2）被拒绝，Ticket整行保持，却留下另一个新标签。后者证明检查版本之前的标签写不在原事务，不能仅给最终UPDATE加member条件解决。当前仅新增失败测试，生产实现未修复，测试集不全绿，不沿用此前PASS。未运行HTTP/工具真实调用或企业发送。

下一接入需统一trusted actor/source/operationId/expectedVersion并贯通普通编辑/子任务/工具与前端；原事务当前授权、历史合法receipt只读重放、scope/member及版本检查在任何tag写前完成，保留category/subtype/状态/专业共享字段边界。原仓储提供caller-tx更新而不另建并行实现，标签创建/关联、Ticket CAS、操作审计、必要状态通知/SLA收尾与Feishu update intent一起提交；飞书现有consumer仅验证manual escalation action/status，必须通过有界且审阅的命令来源契约扩展，不能伪造manual回执或删除合法同步。已有applied SLA冻结规则保持，不能借编辑重套策略。

独立review_execution_scope_s1确认RED有效及计划方向无明显错误，并补充：子任务入口当前没有CanEdit且parent核验在事务外；工具approved expectedVersion必须持久化，operationId从invocation派生，不能重试时读取新version，业务成功后done记录失败靠receipt恢复。编辑Feishu事件必须继续使用相同event_type与稳定aggregate排序键，显式校验编辑receipt，不能另起类型越过手动升级前序或接受任意audit/action/status。

完整S3/S4/S5/S6/B3/T3/T4/G2/G3仍未完成。CandidateSHA保持原值、候选未启动，无WSL/共享环境变更、企业实发、共享迁移、推送或main合并。


### B2 S3 工单编辑仓储调用方事务前置（2026-09-13）

在 `d012377ff` 后增加Repository.UpdateTx，要求非nil调用方*ent.Tx；普通Update与UpdateTx共用唯一字段映射、tenant/deleted/version CAS及专业字段限制。UpdateTx只使用tx.Client，不自行提交/回滚。原普通Update仍有未迁移生产调用，未另建平行业务实现；仓储不替代命令所有者的当前授权、scope/member或错误后回滚责任。

证据位于candidate-delivery/b2：`s3-ticket-edit-repository-red.log` 在接口不存在时运行断言失败（仓储能力缺失检查，不是新的业务缺陷RED）；实现后 `s3-ticket-edit-repository-green.log` 仓储包PASS。SQLite检查原标签替换、工单版本和新标签一起提交或回滚、nil tx拒绝。`s3-ticket-edit-repository-pg.log` 仅运行新增 `ticket repository update joins caller transaction`，真实私有PG16无skip，调用方显式Bind/member后复用现有TicketTagService(tx.Client())及UpdateTx；独立owner连接确认提交前整行和标签数不可见，提交后标题/版本/关联可见，主动回滚或stale version后回滚恢复整行、标签目录及原关联。该测试证明事务加入能力，不等于真实TicketService已接入。

`s3-ticket-edit-repository-regression.log` 仓储/工单服务/controller定向回归PASS；`s3-ticket-edit-repository-build.log` 全后端构建exit0。git diff --check通过，独立review_execution_scope_s1只读复审无阻断。未重跑完整候选PG；已知 `ticket edits preserve historical records and reject orphan tag writes` 仍未修复，当前测试集不全绿。

下一步必须把TicketService原编辑及HTTP/子任务/工具/前端统一接入trusted Meta/receipt，并在原事务内完成当前授权、成员/版本、标签、审计、通知/SLA及飞书更新意图；本次仅完成仓储前置，不能以仓储测试替代上述业务验收。S3/S4/S5/S6/B3/T3/T4/G2/G3仍未完成，固定CandidateSHA不变，候选未启动，无WSL/共享环境变更、企业实发、共享迁移、推送或main合并。


### B2 S3 工单编辑前端版本准入及调用清单修正（2026-09-13）

在 `81860d2e4` 后准备收紧后端命令时，独立审阅发现旧“前端两处”清单遗漏TicketBatchOperations实际挂载的批量status/priority编辑，原先均不带version。TicketKanban虽然挂载，但handleStatusChange只有声明，没有调用/拖拽绑定；本轮只同步其未绑定回调类型/参数，不能声称拖动可用或验收通过。useTickets将该请求原样透传legacy ticketService；TicketApi、legacy service、v2 service是三个现有transport方法。

新增唯一ticketEditVersion输入检查，三个transport均在网络调用前拒绝缺失/非正/非安全整数version，类型要求version并移除TicketApi的force输入提示；useTickets编辑参数去除any/unknown。详情普通编辑和AI建议、批量状态/优先级提交组件已有Ticket.version，没有新增重读最新版本逻辑。这里尚未冻结“打开编辑框时”的快照，不能描述为完整编辑会话版本保证；stable operationId与网络不确定重放仍待后端Meta/receipt和组件操作状态一起接入。

`s3-ticket-edit-client-version-red.log` 5项缺失/无效版本测试在原API实际resolve且未拒绝，明确RED；green.log修复后PASS。`s3-ticket-edit-client-version-regression.log` 四套Jest共123 PASS：API/两个service非法版本零请求、合法version原样PUT且无GET，hook冲突只调用一次、错误透传且不刷新列表；既有测试位置保留。`s3-ticket-edit-client-version-typecheck.log` 最终theme校验/全前端tsc exit0；初次类型检查发现旧测试缺version及原any掩盖看板status类型，已修复后复跑。Junit输出在私有目录，未改跟踪产物。git diff --check通过；独立review_execution_scope_s1确认无本轮阻断，指出组件快照和真实交互仍未验证。未运行浏览器/构建部署或新PG测试，后端未改。

同时明确后端字段缺口：RequesterID权威是tickets.requester_id，当前编辑不持久化；FormFields权威是field_values，现有FieldValueService只有INSERT能力且忽略未知key，没有可直接调用的编辑方法。后续若支持，必须在现有所有者补原事务更新/未知或歧义字段拒绝/保留快照并审计；若暂不支持须显式拒绝，不能静默忽略或把CreateValuesTx当更新，也不能写第二份custom_field_values JSON。现有共享标签覆盖六类，核心拒绝只覆盖Incident/Problem/Change；service_request_item/catalog_task现有核心编辑语义需明确归属后迁移，不能把当前实现描述成所有专业状态受保护。

本轮是完整编辑命令接入前置，不是后端业务GREEN。历史越界及孤立标签RED仍存在，后端mandatory expectedVersion、可信actor/source、operationId、子任务父范围、原事务通知/SLA/飞书意图和审计回执尚待完成。完整S3/S4/S5/S6/B3/T3/T4/G2/G3未完成，CandidateSHA保持固定原值、候选未启动；无WSL/共享数据库变更、企业调用、共享迁移、推送或main合并。


### B2 S3 工单编辑数据库主体与标签原事务接入（2026-09-13）

在 `4a69f1e29` 后迁移既有TicketService.UpdateTicket原入口：依赖缺失/tenantctx冲突显式拒绝，开启调用方RR事务，Bind/member后由tx.Client读取工单、校验目录/处理人/分类/subtype、预检传入版本，标签目录解析和创建与repo.UpdateTx共享原事务，成功后显式commit。唯一subtype解析函数改接收调用方client；不另建编辑服务、不使用生产Standard fallback。相关外部包与同包测试fixture显式注入Standard，controller普通/子任务scope拒绝映射403。

`s3-ticket-edit-scope-green.log` 将此前有效RED转GREEN：历史工单编辑ErrDenied，完整JSON及标签数不变；新member正常成功；旧version在任何标签创建前拒绝。`s3-ticket-edit-tx-pg.log` 完整TestCandidateIntakeCreationBoundary PASS、无skip：新增服务测试在真实TicketTag INSERT及Ticket UPDATE（含新标签关联）成功后注入错误，整行/标签目录/关联回滚，解除故障后原请求成功且版本只增一次。这两条服务故障场景初始无标签；原标签替换回滚由先前仓储测试覆盖，不扩大为服务场景专项已证。

`s3-ticket-edit-tx-regression.log` 服务/控制器/仓储受影响回归PASS，`s3-ticket-edit-tx-build.log` 全后端构建exit0。初次controller fixture未注入policy导致旧成功测试失败，已修正夹具并复跑；无生产绕过。git diff --check通过，独立review_execution_scope_s1限定审阅无新增阻断。测试是任务私有PG16及SQLite，非B的PG17候选运行验收。

本检查点仅数据库主体与标签：旧optional version（缺失时取事务内当前version）、返回Ticket、事务外通知/SLA收尾/Feishu仍待整体命令接入。可信actor/source、子任务父范围、稳定operationId与历史receipt只读重放、所有副作用原子提交尚未完成；RequesterID/FormFields及专业核心编辑归属按上一检查点继续处理。不得把此次候选边界测试PASS等同完整S3/G2完成。固定CandidateSHA不变、候选未启动，无WSL/共享数据操作、共享迁移、企业实发、推送或main合并。


### B2 S3 编辑通知与 SLA 收尾同事务（2026-09-13）

在 `226ffd46f` 后继续迁移UpdateTicket：状态变化的通知改为commit前EnqueueNotificationTx，按最终requester/assignee去重并保留原ticket_updated内容与渠道偏好，delivery key绑定工单ID及提交后version。邮件等外部渠道仅生成pending意图，由既有通知worker处理，编辑不调用provider。autoCloseSLAViolations唯一调用改接原*ent.Tx、tenant/ticket/未解决条件，按ID逐条更新并传播任何错误，保留原resolved_at/is_resolved/resolution_notes语义；第一条已更新而第二条失败时同事务回滚。applied SLA冻结不变。

`s3-ticket-edit-status-red.log` 是修正测试依赖后的有效私有PG RED：测试专属ticket_updated偏好只启用站内，实际Notification/TicketNotification/第二条SLAViolation写后故障都命中，原服务仍返回nil、工单/标签已提交并存在SLA部分写。首轮未配置偏好时默认email缺provider导致通知hook未命中，未把该首轮作为实际通知写后故障证据。

`s3-ticket-edit-status-green.log` 定向GREEN；最终`s3-ticket-edit-status-pg.log` 完整TestCandidateIntakeCreationBoundary PASS无skip：三个写后故障都保全Ticket整行/标签目录/通知两表及两条SLA整行；解除故障后原请求成功、version仅+1、通知两表各精确+1，核对目标ticket/recipient/delivery key。另验证相同requester/assignee去重、仅email偏好生成一条pending且未挂provider、全部禁用产生零意图、缺notifier拒绝并回滚status/version。专属偏好cleanup断言成功，后续测试不继承配置。

`s3-ticket-edit-status-regression.log` 服务/控制器/仓储/通知相关回归PASS，`s3-ticket-edit-status-build.log` 全后端构建exit0。回归初轮全局测试fixture过早注入notifier改变创建依赖，已撤回该改动，仅两个实际编辑成功测试局部注入；生产无fallback。git diff --check通过；独立review_execution_scope_s1两次限定复核无新增阻断，并按建议收紧通知精确数量与cleanup断言。未运行企业发送或候选WSL验收。

完整编辑命令仍未完成：Feishu仍在commit后旧独立路径，可信Meta/actor/parent、必需expectedVersion、稳定operationId及回执重放尚待贯通HTTP/工具/前端；业务输入字段与专业类归属缺口仍按前述处理。数据库通知意图原子提交不等于外部投递已完成，不等于整个S3/S4/S5/S6/B3/T3/T4/G2/G3通过。固定CandidateSHA不变，候选未启动，无WSL/共享数据库变更、共享迁移、企业实发、推送或main合并。


### B2 S3 编辑操作者与现行权限原事务核验（2026-09-13）

在 `49293729d` 后UpdateTicket原RR事务内新增ResolveLifecycleActor，核验actor活跃/所选tenant会话；RequireCurrentPermission读取当前角色与权限，要求ticket:update，专业共享编辑额外使用既有WorkItemPolicy的领域resource/action（例如Incident为incident:write），不以缓存权限代替原事务查询。既有CanEdit“终态不可编辑”条件进入服务，直接工具/子任务调用也不能绕过。原专业核心字段拒绝仍保留。UpdateTicketRequest.UserID改json:-，只由HTTP或持久工具边界设置；普通编辑/子任务把授权失败映射403。尚未将该输入重构为完整Meta命令。

`s3-ticket-edit-actor-red.log` 有效私有PG RED：普通授权editor对照成功，然后缺失/停用/撤权（预热旧缓存）/外租户actor均返回nil并改Ticket完整行及新增标签。首轮缺少foreign tenant变量仅编译失败，已补明确夹具后重跑，不计作业务RED。`s3-ticket-edit-actor-green.log` 定向GREEN；最终`s3-ticket-edit-actor-pg.log` 完整TestCandidateIntakeCreationBoundary PASS无skip，四种拒绝完整行/标签保全，Incident共享标签在仅ticket:update时拒绝、增加incident:write后同请求成功。普通角色证据不依赖super_admin正向。

`s3-ticket-edit-actor-regression.log` 服务/控制器/仓储定向PASS，`s3-ticket-edit-actor-build.log` 全后端构建exit0。测试fixture仅编辑场景显式授予当前权限及填入真实actor，没有生产fallback；初次HTTP成功fixture只有中间件admin权限而实际user是end_user，已明确为其真实角色授权并复跑。`s3-ticket-edit-actor-http.log` 最终真实controller函数的普通PUT/子任务PATCH均拒绝停用actor，JSON写入活动super_admin userId不能冒充，完整Ticket/标签保全；另验证DTO不会反序列化userId。测试使用身份中间件与局部路由注册，未覆盖生产认证/ACL中间件全链或真实SSO。

独立review_execution_scope_s1限定复核无新增阻断，指出终态条件进入service会收紧此前绕过controller的直接调用，符合当前目标；按其提醒将子任务测试方法与router.go的PATCH一致后复验。git diff --check通过。完整命令仍待：Meta/source、必需expectedVersion、稳定operationId、父子归属/父成员原事务、当前授权后的历史receipt只读重放及Feishu同事务意图。当前Bind/member在回执前的顺序必须随receipt接入调整；不宣称已经有历史重放。专业核心归属与RequesterID/FormFields仍按前述处理。CandidateSHA不变，S3/S4/S5/S6/B3/T3/T4/G2/G3未完成，候选停止，无WSL/共享数据库变更、企业调用、共享迁移、推送或main合并。

### B2 S3 编辑父工单范围与子任务路由同事务核验（2026-09-13）

在 `64ab11f0f` 后，UpdateTicket 在原 RR 事务内按实际 ParentTicketID 核验父成员、同租户及未删除记录，普通编辑入口同样受限；非法自指/非正父关系拒绝。PATCH 子任务仅从路由设置 ExpectedParentID（json:-），服务比对事务内真实父关系，移除控制器事务外 GetTicket 预读；输入/未找到/版本冲突采用对应错误映射。检查在标签和工单写入前完成，不修改父记录或递归操作祖先。这是 RR 快照内校验，没有锁住父记录阻止并发删除。

`s3-ticket-edit-parent-red.log` 有效私有 PG RED：新成员子工单预存关联历史父工单时，普通服务编辑返回成功并改动整行/标签；对照父子均为成员可编辑。`s3-ticket-edit-parent-green.log` 定向通过，`s3-ticket-edit-parent-pg.log` 完整 TestCandidateIntakeCreationBoundary PASS 无 skip：历史父关系拒绝且子整行/标签保全，成员父关系允许一次更新，两种父记录整行均不变。预存关系由 owner fixture 设置，不代表验证了历史父下创建许可。

`s3-ticket-edit-parent-http.log` controller 测试 PASS：错误/非正路由父 ID 拒绝且子整行/标签保全；JSON expectedParentId 不能覆盖路由，正确 PATCH 成功且版本只加一。沿用停用 actor 的 PUT/PATCH 测试也通过。`s3-ticket-edit-parent-regression.log` 服务/控制器/仓储定向回归 PASS，`s3-ticket-edit-parent-build.log` 全后端构建 exit0。独立 review_execution_scope_s1 限定审阅无新增阻断；未删除/同租户父记录的独立负测、非法子 ID 专项及并发父删除未覆盖，未声称生产认证全链或 WSL PG17 验收。git diff --check 通过。

完整命令仍待 Meta/source、必需 expectedVersion、稳定 operationId、当前授权后历史 receipt 只读重放及 Feishu 同事务意图；当前成员检查在 receipt 前的顺序须随回执接入调整。字段归属及专业核心编辑缺口仍按前述处理。S3/S4/S5/S6/B3/T3/T4/G2/G3 未完成，固定 CandidateSHA 不变、候选未启动；无 WSL/共享数据库变更、企业实发、共享迁移、推送或 main 合并。

### B2 S3 编辑回执与重复提交真实 RED（2026-09-13）

在 `157e2ea30` 后建立 `ticket edit retries preserve immutable operation result` 私有 PG 测试。通过当前 DTO 解析带固定 operationId/version 的请求，测试边界提供真实 UserID，调用原服务。当前 DTO 不接受 operationId，正是待迁移的契约缺失，不是通过测试伪造可信 Meta。首次标题/标签编辑成功为正向控制；期待同事务一条操作回执，原请求立即重试及另一独立编辑后重试均返回首次版本/状态且保全 Ticket 整行、标签目录/关联、审计数量。另用原 operationId 携带新 payload 和当前 version，要求 OperationConflict 且不写入。

最终 `s3-ticket-edit-receipt-red.log` 是预期业务失败：首次 receipt 数为0，两次重试分别发生客户端1/服务端2或3的版本冲突，复用原操作ID的新内容却成功修改标题/版本并新增、替换标签。无编译错误或 skip。这个新增测试尚未 GREEN，因此当前候选测试集不能沿用上一检查点的全绿结论；本次没有更改生产代码，也没有重跑完整候选测试或构建。独立 review_execution_scope_s1 确认 RED 有效，建议的标签关联与冲突分支审计保全断言已补入并复跑。

后续同一 UpdateTicket 接口整体迁移 typed command/可信 Meta/immutable Result，不添加兼容双入口。保留原请求 version/operationId/payload，不以重新读取当前 Ticket 冒充回执。digest 包括 edit action/目标/预期父/version/规范化业务输入（保留 tags nil 与 empty 区别），现行权限在 Replay 前，首次写入才校验 scope/member/父关系/终态/版本/目录并执行原子写入。普通HTTP的 GetTicket+CanEdit预检须移除，避免终态挡住合法重放；两入口返回 Result 并将 OperationConflict 映射409。tool_queue使用已批准版本和invocation派生操作ID，registry补明确schema，done失败复用回执。

三个前端transport及TicketDetail普通/AI、批量操作、useTickets/useTicketsQuery须同时迁移；不能把Result合并成Ticket或写入详情缓存，成功后重新读取，结果不确定时保留同意图payload/version/op。飞书沿用现有update event_type/aggregate及严格receipt来源检查，原提交后直接同步在完整接入时删除。详细顺序已同步设计worktree执行计划。当前RED后续编辑只改标题，因此独立证明原版本回放而非状态漂移；typed Result接入后补 WorkItemID/Replayed、receipt action/digest/version/status、终态及状态漂移、审计/outbox写后回滚、并发、工具done恢复和真实前端契约测试。完整S3及后续门禁未完成，固定CandidateSHA与候选停止状态不变；无WSL/共享数据变更、企业调用、推送或main合并。

### B2 S3 编辑可信命令、不可变回执与飞书意图贯通（2026-09-13）

在 `dd0ebffee` 的真实回执 RED 后迁移唯一 UpdateTicket 原接口为 TicketEditCommand 与 workitemmutation.Result，删除旧参数签名和请求中的 UserID/Force/预期父元数据。HTTP payload只携带业务字段、必需 version/operationId；边界构造可信 Meta及路由父ID。原RR事务读取当前工单及现行actor/领域权限后，按明确edit action、目标、父ID、expectedVersion和业务字段生成摘要并查历史receipt；首次写入才Bind/member、父关系、终态/专业归属、版本和目录校验，再执行标签/工单CAS、通知/SLA、Feishu intent和AuditLog唯一回执，最后commit。不以当前Ticket冒充历史结果，不保留兼容双入口。RequesterID非零/FormFields非nil现在显式拒绝，未实现其编辑所有者；专业核心归属仍按此前边界处理。

普通PUT移除事务外GetTicket+CanEdit，子任务PATCH沿用可信父ID；两者直接返回不可变Result，operation/version冲突映射409。tool_queue只从已批准持久参数解析expectedVersion，operationId固定由invocation ID派生，业务完成后done记账失败可复用原回执。update_ticket registry发布输入/结果schema，唯一toolEditCommand严格拒绝未知/重复字段、错误类型、缺少版本/编辑字段及尾随JSON，不读取新版本替换批准版本。`s3-ticket-edit-tool-red.log`通过真实ProcessJob复现未知/null状态/重复版本/尾随JSON被接受；green.log修复后PASS，包含create工具既有契约回归。此处授权审批来自fixture，未声称真实人工审批全链完成。

原enqueueManualFeishuUpdate改为唯一enqueueFeishuUpdate供手动升级及编辑复用，保持同event_type、稳定目标aggregate和原CAS锁后的入队顺序。payload明确action/resultStatus；consumer分别核验manual的generic/in_progress/escalate权限，或edit的当前ticket:update/领域权限，并匹配对应AuditLog action、摘要、结果版本/状态和payload事实。原编辑commit后直接网络同步已删除。配置飞书时仍要求现有且身份一致的映射；无映射不会静默跳过。旧payload缺action/resultStatus会明确阻断，未宣称旧持久事件升级兼容或已执行任何环境迁移。

前端三个transport必需version/operationId并返回TicketEditResult。TicketDetail普通编辑冻结打开表单时的version/status；普通/AI及已挂载批量状态/优先级保持单次意图的深拷贝payload/version/op，结果不确定时复用；成功后读取详情，useTicketsQuery只失效详情缓存，不把Result当Ticket写入。仅结构化后端409/4090被视为明确拒绝：详情关闭旧表单并刷新供重新确认，AI释放旧意图并刷新，批量释放该目标意图供刷新后重新确认；网络/5xx/无业务码代理409保留原请求。切换ticketId清理旧refs，批量失败保留表单及未决操作，全部成功才清理；Kanban回调仅同步契约，仍未绑定，不计真实拖动。

最终证据在candidate-delivery/b2：`s3-ticket-edit-command-final-pg.log` 完整 TestCandidateIntakeCreationBoundary PASS无skip，原dd0ebffee回执RED已GREEN：首个receipt精确一条，立即/后续独立编辑/closed scope重放均返回原版本/状态且Ticket整行、标签目录/关联和audit保全；同op新payload/当前version冲突无写。新增audit及outbox实际写后故障整次回滚、解除故障原cmd成功且版本仅+1/各一条receipt与事件、再重放无新事件；edit consumer经真实worker投递到本地假provider并核验冻结任务名称/GUID。初轮该新增测试误用FeishuTask.Summary字段仅编译失败，改用实际Name后复跑，不计作业务RED。该测试故障前无原标签，原标签替换回滚由既有仓储证据覆盖。

`s3-ticket-edit-command-recovery.log` SQLite真实ProcessJob模拟done写失败：首次业务已提交，重试不变更Ticket整行/审计数，保存原版本的Replayed Result，停用actor后再拒绝；controller真实PUT/PATCH首次cancelled后同payload原version重放，通知两表与audit各仅一条。`s3-ticket-edit-command-regression.log` 服务/控制器/仓储/专业边界及Tool定向回归PASS；tool-green.log另覆盖原TestCreateTicketTool契约。`s3-ticket-edit-command-final-build.log` 全后端build exit0，final-typecheck.log主题校验/全前端tsc exit0，final-client.log四套Jest127 PASS（定向运行关闭全仓覆盖率门槛），包括操作ID缺失零请求、深拷贝和版本/op重试复用、明确冲突识别、Result不污染详情缓存。初次客户端RED实际缺operation仍发送；同时全仓覆盖率不足非业务RED，后续定向运行明确关闭coverage。旧测试按新命令/Result迁移，领域负例明确提供当前actor/权限及观测版本；未使用生产fallback。

独立review_execution_scope_s1发现并关闭两处前端作用域/初次渲染P1及确定冲突旧意图不释放P2，最终限定复审无新增阻断。git diff --check通过。当前未完成：确定性并发同命令、历史无成员编辑receipt专项、状态漂移/回执字段专项、真实组件交互/跨刷新持久恢复与浏览器验收、专业核心编辑归属及Requester/FormFields所有者；这些不可由helper测试或本地provider代替。S3/S4/S5/S6/B3/T3/T4/G2/G3仍未完成，固定CandidateSHA不变、候选未启动；无WSL/共享数据库变更、企业实发、推送或main合并。

### B2 S3 编辑并发与历史回执专项（2026-09-13）

在 `57cd25088` 后仅补验证，不改变生产代码。`concurrent ticket edits recover the original receipt` 在两个真实Ticket UPDATE之前设置测试barrier，两调用必须均完成原版本读取及无receipt查询才释放，不以同时启动goroutine代替确定性竞争。结果要求一次成功提交、另一方PQ 40001，唯一receipt、版本仅+1及目标title落库；失败方随后原cmd重试返回胜方完整Result（仅Replayed变true），Ticket整行及唯一receipt保全。该测试没有并发标签/通知/Feishu输入，不声称这些副作用的确定性交付竞争已验收，也不声称服务或HTTP自动重试。

新增历史fixture在039作用域迁移前，由真实Standard编辑所有者先new→open生成回执，再独立edit→in_progress。`historical edit receipt preserves original status and membership` 在候选runtime核实该目标成员数为0；重放返回原open/原version的完整Result，原Audit的action/digest非空长度/source/path/result字段与回执对应。换新operationId并使用当前version仍ErrDenied。Ticket与原Audit整行、Audit/Notification/TicketNotification/Outbox数量及零成员均保持。证明迁移前生成的当前编辑契约回执可只读重放，不泛化为任意旧版本回执兼容；没有回填或删除历史成员。

证据：`s3-ticket-edit-concurrent-pg.log` 初始并发定向PASS；`s3-ticket-edit-history-pg.log` 补强胜方完整Result/title和历史用例后PASS；最终 `s3-ticket-edit-concurrent-final-pg.log` 完整TestCandidateIntakeCreationBoundary PASS无skip，`s3-ticket-edit-concurrent-race.log` 两个新增场景的真实私有PG及Go race检测PASS。新用例直接通过，属于补强已有实现验证，未描述为新的业务RED。独立review_execution_scope_s1只读审阅及补强后复审均无阻断；git diff --check通过。生产代码/前端未变，未重复构建或前端测试。

同时设计worktree纠正原设计顶部“实施未开始”的过期状态，保留accepted并明确进行中、候选未启动、G2/G3未通过，链接当前实施入口。完整S3/S4/S5/S6及B3/T3/T4/G2/G3仍未完成，真实组件/浏览器、完整业务周期与候选运行不能由这些测试替代。固定CandidateSHA不变，无WSL/共享数据库操作、企业实发、推送或main合并。

### B2 S3 编辑真实组件交互与批量入口修复（2026-09-13）

在 `12c38eda4` 后补入 TicketDetail 普通编辑与 TicketBatchOperations 的真实 React/AntD 组件测试，API 边界使用 mock。Detail 通过实际编辑按钮、标题输入、Alt+R 刷新和保存按钮验证：表单打开时 version 冻结；不确定结果再次确认复用完整 payload/version/operationId；明确409/4090后刷新并重新打开表单确认才使用新版本和新操作。测试使用 document.body 作为键盘事件目标；初轮 window 目标和 JSDOM 样式查询兼容性失败属于测试环境问题，不计业务RED。

真实批量菜单测试发现 updateStatus/addTags/setPriority 与 render/execute 的 update_status/add_tags/set_priority 不一致。`s3-ticket-edit-batch-menu-red.log` 两项实际菜单操作均打开空表单并缺少对应标签；生产代码仅统一三个菜单 key 至既有执行分支，不增加映射或兼容入口。新增状态/优先级/标签菜单字段可达性测试；标签只核验打开字段，不声称提交成功。批量状态及优先级通过实际选择和确认模拟网络中断，父组件刷新版本后再次确认仍使用完整原请求；明确冲突后再次确认才使用刷新版本及新operationId，不自动重提。

最终 `s3-ticket-edit-component-full.log` 两套完整组件测试23 PASS、无skip；定向运行关闭全仓coverage门槛。`s3-ticket-edit-component-typecheck.log` 主题校验与全前端type-check exit0；git diff --check通过。独立review_execution_scope_s1只读审阅无新增阻断。未改后端，因此未重复Go构建/PG测试。PointerEventsCheckLevel.Never沿用组件测试环境约定，不能证明浏览器遮罩、布局与点击可达性；mock刷新及冲突也不能替代实际HTTP、后端提交或候选业务E2E。

本轮仅补齐普通详情/批量编辑的组件证据，AI建议实际交互、跨页面重载恢复、浏览器和候选业务周期仍未验证；S3/S4/S5/S6及B3/T3/T4/G2/G3保持未完成。固定CandidateSHA仍为d7470a32dbb87acc9b5e4d9a895a146410723561，候选未启动；无WSL或共享数据库变更、企业实发、推送或main合并。

### B2 S5 Stream 传输隔离真实 RED（2026-09-13）

在 `d27c57d68` 后转入候选启动前必需的S5，不将前端组件检查点视为整个交付完成。新建 `tests/integration/candidate_stream_preservation_test.go`，使用已有受保护目录Redis 7.2.16二进制启动临时loopback实例，随机密码和实际PID核验后才写fixture；不连接共享Redis。先向旧sla.breached写入历史事件并建立单条pending，再启动现有Watermill订阅、确认真实XREAD等待并发布新事件；handler收到新WorkItem是正向控制。

`s5-stream-isolation-red.log` 预期业务失败：旧Stream新增消息，原group lag由0变1，预期candidate:deployment:scope:stable-topic长度为0。原group的pending摘要及逐条ID/consumer/delivery count保持（自然增长idle不纳入比较）；这不是历史pending被领取或ACK的复现。现有subscriber默认fanout，测试不声称生产候选启动或完整消费组恢复验证。fixture里的scope/deployment只是输入，当前构造器没有冻结策略入口，不能把这些字段当成授权证据。未改生产实现，当前新测试仍RED，不能沿用之前完整测试全绿结论。

只读调用盘点：此Watermill Publish业务调用仅有SLABreachDeliveryHandler和handlers/ai.Service.TriageTicket。前者来源为已持久outbox；后者为建单前分诊，TicketID为空，不能捏造WorkItem归属。下一步同一构造器贯通冻结ExecutionPolicy及订阅合同，候选publisher在Redis写前验证结构化主体，subscriber只订阅可信namespace并核对envelope/tenant/member；审计所有者仍须在自身写事务核验并保留幂等回执，不能仅改topic字符串。无主体建单前AI事件不得进入受限执行空间，按既有明确禁用/失败边界处理，保留分诊业务含义。不增加第二套candidate bus、不双发旧topic，不从payload自报scope赋权。

独立review_execution_scope_s1确认传输RED有效并建议补充逐条PEL保全，已采纳；真实Audit持久化、成员撤销、非法envelope/未知事件、subscriber退出及新namespace唯一载荷校验仍待GREEN阶段。S5及完整B2/B3/T3/T4/G2/G3未完成，CandidateSHA不变且候选未启动；无WSL、共享数据、企业实发、推送或main合并。

### B2 S5 冻结配置驱动的双向 Stream 路由（2026-09-13）

在 `c94cdfba2` 的真实传输RED后，唯一NewWatermillEventBus构造器显式接收ExecutionConfig，bootstrap及测试调用同时迁移；配置验证在创建Redis clients之前，复制mode和每项deployment/scope/tenant，后续配置slice/map修改不改变路由。拒绝同scope绑定多tenant；candidate发布必须有稳定topic和规范正整数tenant，租户必须在冻结清单，topic不能直接提供物理namespace。Publish与Subscribe使用同一路由生成逻辑，前者只写对应candidate:deployment:scope:stable-topic，后者展开冻结租户清单；不双发旧topic，不读取payload自报scope/deployment决定路由。standard保持原topic语义。这只是传输限制，构造器不等于数据库角色准入或业务授权。

`s5-stream-routing-final.log` 真实Redis测试由原RED转GREEN并增强为双租户：实际两条订阅均进入XREAD后分别发布，新事件各到唯一独立Stream并核验信封tenant/目标payload；故意不一致的payload scope不能改变物理位置。旧XRANGE、group及pending摘要、逐条ID/consumer/delivery count保持，idle自然增长不比较。同一日志包含完整应用构造保全测试：真实私有PG16/Redis7.2.16/MinIO夹具保持，未启动runtime；以上无skip。单位测试覆盖冻结后配置修改、无配置/重复scope拒绝、非法topic/tenant和无稳定事件在写Redis前失败。新增非法输入用例直接通过，未宣称单独RED。

`s5-stream-routing-regression.log` eventbus及bootstrap完整包回归PASS；`s5-stream-routing-build.log` 全后端build exit0。独立review_execution_scope_s1限定审阅无新增阻断，建议的双租户真实路由补强已采纳；git diff --check通过。未改前端，不重复前端验证。

下一阶段仍需贯通窄authority与同一冻结数据库ExecutionPolicy，校验active scope、role binding、tenant/member，并用持久outbox eventID及结构主体验证生产来源；合法member本身不证明Redis payload是业务事实。消费信封须严格校验并保留可信身份，原审计写事务再次准入/幂等并在成功后ACK。当前fanout没有持久消费组恢复保证，namespace不能解决离线遗漏或重复审计；无效消息可观察阻断、建单前无主体AI事件处理、订阅取消/恢复及真实Redis+PG联测均待完成。本阶段不构成candidate启动许可，S5、完整B2/B3/T3/T4/G2/G3仍未完成；固定CandidateSHA不变，候选未启动，无WSL或共享数据操作、企业实发、推送或main合并。

### B2 S5 持久事件来源与严格信封校验（2026-09-13）

在 `ea8c2494a` 后接入候选Stream来源校验。`s5-stream-source-red.log` 实际复现无持久主体的stable event被发布；随后唯一Watermill构造器增加必需的candidate EventAuthority，bootstrap注入原runtime client及原冻结ExecutionPolicy。candidate Publish要求ExecutionEvent提供持久eventID/WorkItemID，outer execution由冻结route生成，经authority验证后才写Redis；消息UUID复用持久eventID，不在重试生成新操作身份。标准发布合同保留，建单前无主体AI事件在候选中明确返回发布错误，原调用者记录告警，未捏造WorkItem或把payload scope当授权。

新ExecutionEventAuthority通过复制的CandidateRef核对两边冻结清单，在明确tenant事务中执行原BindEnt/RequireEntMembers，检查active scope和session_user binding，再按tenant/结构WorkItem/eventID查询持久Outbox来源。SLABreachDeliveryHandler提取唯一persistedSLABreach factory，producer及authority复用其契约，按原SLA事实重建payload/occurredAt并比对；当前仅SLA具有该持久来源，未知事件显式拒绝。source检查是只读事务，不声明与Redis原子，也不表示持有发送claim；审计写事务仍必须再次准入。对JSON载荷采用生产者规范编码进行比对，仅允许空白差异，未提供多种等价编码兼容入口。

candidate消费循环在调用handler前核对消息UUID、metadata、物理route和完整信封，再执行同authority。信封有大小/深度上限，拒绝未知字段、重复字段、缺失持久身份和payload覆盖outer身份；保持outer execution/eventId传递给原handler。审阅发现encoding/json大小写别名仍可覆盖struct字段P2，增加outer/execution两层精确key白名单并补EventId/WorkItemId/重复大小写负测，复审关闭。失败消息NACK并记录错误，不ACK成功；当前fanout的无效消息重投行为及持久消费恢复仍待下一阶段处理。

`s5-stream-source-pg.log` 真实私有PG专项PASS：真实SLA monitor创建持久outbox，真实SLA handler经capture bus给出来源，authority拒绝未知ID、另一个合法member、历史member、变造payload/时间/tenant/scope及未知type；scope关闭或rolebinding变化后拒绝，恢复后原消息再次通过。原Outbox整行及audit数保持。此测试capture bus不代表Redis+PG联合消费。受控channel+fake authority单测通过真实bus消费循环验证合法ACK、非法UUID/tenant/scope/eventID/大小写字段NACK和零handler调用；不宣称Redis服务端PEL验收。

最终 `s5-stream-source-final-pg.log` 完整TestCandidateIntakeCreationBoundary、真实Redis双租户路由及完整应用构造PG/Redis/MinIO保全PASS无skip；Redis路由用明确fixture authority，不代替上述数据库权威测试。`s5-stream-source-regression.log` eventbus/bootstrap/database/service相关定向回归PASS，`s5-stream-source-build.log` 全后端build exit0，git diff --check通过。独立review_execution_scope_s1复审无新增阻断。前端未改，不重复前端测试。

后续必须完成审计原事务当前准入/持久幂等回执、真实Redis消费组身份和起始/恢复语义、无效消息可观察阻断且避免热循环、退出等待及真实Redis+PG联合重启验证；直接调用审计/webhook所有者不能绕过边界。当前消息来源校验不替代这些要求，S5和完整B2/B3/T3/T4/G2/G3仍未完成，固定CandidateSHA保持d7470a32dbb87acc9b5e4d9a895a146410723561，候选未启动。无WSL或共享数据库变更、企业实发、推送或main合并。

### B2 S5 审计原事务、唯一回执与消费上下文（2026-09-13）

在 `d8e3d0420` 后接入原EventAuditSubscriber。真实PG首轮 `s5-event-audit-red.log` 在首次写前触发RLS缺tenant：原Handle使用context.Background()，不能作为候选写入入口。先补显式tenant后，`s5-event-audit-persistent-duplicate-red.log` 再现同一持久事件两次调用新增两条审计（期望18，实际19）。另一次SQLite试验未用于定义standard无事件ID消息的幂等合同，原standard单次审计测试保留。

唯一构造器现在必需原ExecutionPolicy；candidate HandleContext只接受完整typed Envelope并自行严格解析，拒绝直接raw map。以明确tenant开始RR事务，调用抽出的ValidateEventTx在该事务检查当前scope/binding/member及持久来源，随后查询或写入原AuditLog。系统actor0+event_audit:<持久eventID>复用现有唯一operation receipt索引；完整身份/内容digest、规范化body、resource/action/path/method/status及recorded结果共同匹配才只读重放，保留原CreatedAt，不填造WorkItem version。唯一冲突直接返回并回滚，下一次投递另开事务重放。无新表、额外回执状态机或历史回填。

candidate bus不再flatten信封，保留typed Envelope交给owner；ContextEventHandler实际收到订阅context，关闭bus后同一context取消。standard仍用原map入口，同时拒绝未知审计type/无效tenant/外tenant上下文，不为无持久事件ID的standard消息宣称新增幂等保证。Webhook当前不能接受typed Envelope而明确返回错误，未暗中转换回map，不能将本轮说成所有subscriber可用。

`s5-event-audit-atomic-pg.log` 真实PG验证AuditLog实际INSERT后注入失败整事务回滚，计数回到原值；移除故障后两个调用均在Audit INSERT前barrier会合，一次提交、另一次PQ23505，唯一回执。失败方原消息重试成功，完整receipt行保全；变造事实、关闭scope后的原消息重放均拒绝，恢复scope后原回执可读。测试中的scope/binding控制变更有cleanup恢复，仅位于任务私有库。此处直接调用owner，不声称Redis ACK丢失E2E。

`s5-event-audit-final-pg.log` 完整候选intake边界、真实Redis双租户路由、完整应用构造PG/Redis/MinIO保全PASS无skip；`s5-event-audit-build.log` 全后端build exit0。`s5-event-audit-regression.log` eventbus/bootstrap/service相关回归PASS；初次测试fixture误传Standard(t)只造成编译失败，改为实际Standard()后通过，不计业务RED。受控channel消费测试验证ContextEventHandler收到真实订阅context并随Close取消，仍不是Redis服务端ACK/PEL证据。独立review_execution_scope_s1无新增阻断，旧map注释已纠正；git diff --check通过。

`s5-event-audit-race.log` 上述真实PG来源及审计故障/确定性竞争专项通过Go race检测。

S5仍需真实Redis消费组配置、无效消息持久阻断/避免热循环、已提交未ACK恢复及Redis+PG联合消费验证，Webhook直接owner准入和其余请求异步边界也未完成。固定CandidateSHA不变、候选未启动，完整B2/B3/T3/T4/G2/G3未完成；无WSL或共享数据操作、企业实发、推送或main合并。

### B2 S5 持久消费组与审计 ACK 间隙恢复（2026-09-13）

在 `7507ca616` 后继续 S5。真实 Redis `s5-stream-offline-red.log` 复现 fanout 从 `$` 启动漏收离线事件，在线 marker 可收到但离线计数为0、无消费组。唯一 Watermill bus 改为候选 logical owner 消费组：登记时冻结 `EventConsumerID`，拒绝无身份/非法身份及重复 owner/topic；同 owner 跨 topic 复用 subscriber，不同副作用使用不同组，库生成随机实例 consumer。首次建组从0开始，已有组不重置；构造/登记不建立组。standard 保持原 fanout。配置及恢复合同见开发指南的候选执行范围章节，未修改 B 的环境配置。

Close 禁止新订阅并取消 context，等待正在建立的 Subscribe 退出后关闭所有持有的 subscriber，再等待处理循环退出。只读 reviewer 发现公开动态 Subscribe 第二租户失败会留下第一租户，新增双租户测试 `s5-durable-partial-red.log` 复现 expected close1/actual0；修复为部分建立失败时关闭整个事件 runtime，保留原错误及关闭错误，不重置组。登记失败在 bootstrap 显式终止。冻结身份、独立 owner、nil factory、重复订阅、取消建立及部分启动失败均有测试。

联合恢复用真实 SLA monitor/outbox 来源、数据库 authority 和 Audit owner：在实际审计事务提交后故意阻止 handler 返回，从而不 ACK；确认原 entry 已 pending，关闭旧 bus 后再次断言同 entry/owner 仍 pending，再构造新 bus 使用原组领取该 entry。最终 PEL 清空、两个不同消费者实例可见，原 Stream entry 及审计完整行不变、审计仅增加一次，旧裸 Stream 键与组快照保持。此为至少一次传输加审计幂等；测试使用实际 SLA delivery handler 生成事件后调用 bus.Publish，不声称已覆盖整个 outbox Worker 当前 claim 或完整应用/OS 重启。离线测试使用 transport fixture authority，不单独充当数据库权限证据。

验证：`s5-stream-offline-green.log`、`s5-stream-ack-recovery-pg.log`、`s5-durable-full-private.log` PASS；最终修复后 `s5-durable-regression.log` 六个受影响包 PASS，`s5-durable-final-race.log` 同时覆盖全部新增 lifecycle、完整候选 intake、真实 Redis 离线/历史保全及 PG/Redis/MinIO 应用构造保全 PASS，无skip、无race。独立 review_execution_scope_s1 复核关闭上述P2，未发现本检查点新增阻断。全后端 `s5-durable-build.log` exit0，git diff --check通过。日志保存在任务私有 b2 目录，未提交测试产物。

S5 仍未完成：Webhook candidate typed envelope 与其写入所有者、无效消息的持久可见阻断、其它请求异步/工具入口仍待接入；外部效果不能仅靠消费组认定幂等。S3/S4/S6及B3/T3/T4/G2/G3不因本检查点放行。固定 CandidateSHA 仍为 `d7470a32dbb87acc9b5e4d9a895a146410723561`，候选未启动；未操作WSL/共享数据库，未企业实发、推送或合并main。

### B2 S5 Webhook 实例路由与必需分发失败修复（2026-09-13）

在 `5b3ad10c0` 后核查真实 Webhook owner：旧实现遍历租户实例但每次用 tenant/name 的 Manager.Send 随机选择同名实例，多目标可能重复串投；无目标静默成功，未知eventType仍出站。`s5-webhook-dispatch-red.log` 使用任务内loopback HTTP测试端点复现三项失败，多实例预期每端点2次而实际某端点4次。沿用Manager的tenant/name/provider实例键增加精确发送入口，订阅者使用枚举的provider，不创建第二套实例目录；缺失/已撤销实例不fallback。必需Webhook无目标及未注册类型返回error，不能ACK成功。

`UsesDeliveryContext` 先在 `s5-webhook-context-red.log` 复现不支持ContextEventHandler，再将Handle委派HandleContext，原传输ctx派生每次发送超时；已取消ctx、不同tenant和SystemBypass在发送前拒绝。定向测试核验零外呼及匹配tenant正向控制，精确目标测试覆盖跨tenant/缺失provider/Revoke后不fallback；这是调用前取消证据，不声称已验证发送途中断连。`s5-webhook-dispatch-green.log`、`s5-webhook-dispatch-race.log` PASS；完整service、connector/...、bootstrap及eventbus回归 `s5-webhook-regression.log` PASS。独立review_execution_scope_s1两次只读复核无新增阻断，确认ctx循环派生与实例路由边界。全后端 `s5-webhook-build.log` exit0，git diff --check通过。

当前仍是同步多目标发送：前目标成功后其它失败会导致Redis重投重复前目标。实例查找释放锁后仍可能与撤销/同key配置重绑竞争，不能把精确key当成配置版本或撤权栅栏。下一候选接入须在typed来源/成员核验原事务中，按source eventID与冻结目标建立既有outbox意图及消费回执；持久化目标/配置摘要与载荷摘要，worker重新核验当前目标一致、claim/attempt/范围，未知投递结果进入delivery_unknown，重放不重新枚举增添目标。本轮不把同步发送包装成候选幂等：typed Envelope仍拒绝，候选不放行。

未修改数据库schema、执行共享操作或使用企业目标；候选SHA不变、候选未启动，无推送/main合并。S5、完整B2/B3及T3/T4/G2/G3仍未完成。

### B2 S5 Webhook 候选消费与逐目标持久意图（2026-09-13）

在 `1d391a42b` 后接入原消费所有者。NewWebhookEventSubscriber 显式注入client与冻结ExecutionPolicy，bootstrap/单元调用同步迁移；candidate拒绝原始map，完整typed Envelope在同一RR事务校验active scope、runtime binding、成员和持久来源。逐个明确Webhook目标建立既有outbox意图（`webhook.event.delivery.requested`），与唯一 `webhook_consume:<eventID>` Audit回执原子提交。消费不外呼，回执202/enqueued仅表示入队；原始来源保留于Audit字符串。目标记录provider及URL摘要，不复制URL或凭据、不声称完整配置版本。standard保留精确实例同步路径。

真实PG `s5-webhook-intent-red.log` 先复现typed入口拒绝；首次实现可入队但JSONB重排导致重复消费摘要冲突，`s5-webhook-intent-green.log`为该真实失败，不是通过证据。摘要改为UseNumber的JSON键规范化，来源准入仍使用原字节契约。独立审阅指出结构体摘要会忽略未知字段，`s5-webhook-intent-extra-field-red.log` 通过实际jsonb_set unexpected字段复现原返回nil；修复为完整持久row.Payload参与摘要，并核对eventID/type/aggregate/WorkItem及来源摘要。

原事务验证：Outbox实际INSERT后故障与Audit实际INSERT后故障均整笔回滚；两个消费者在首次INSERT前会合，确定性一次成功/一次PQ23505，败方原env重试复用两条意图和单回执。增加第三目标不扩展既有消费目标集合，重放不重新枚举配置；无目标、raw map、closed scope和篡改意图拒绝，恢复后可重放且原审计完整行不变，测试端点接收计数始终0。scope/payload故障探针增加即时defer恢复。`s5-webhook-intent-canonical-green.log`、`s5-webhook-intent-atomic.log`通过；完整私有候选intake、Redis离线/历史保全及应用PG/Redis/MinIO构造保全的 `s5-webhook-intent-final-race.log` PASS，无skip或race。独立review_execution_scope_s1复核完整摘要修复及事务测试，无本检查点新增生产阻断。完整受影响包 `s5-webhook-intent-regression.log`、清理补强复测 `s5-webhook-intent-cleanup-pg.log` 及全后端构建 `s5-webhook-intent-build.log` exit0；git diff --check通过。

Worker尚未实现或注册，新type仍由既有未知分发机制明确阻断；不能启动候选或把入队当履约。下一步worker必须验证消费回执身份/来源摘要/意图成员，再用Audit字符串中的原始来源重验（不能直接将JSONB重排的Source喂入字节校验）；目标摘要校验须绑定实际发送实例，不能检查后再按可重绑key查找；claim/attempt/范围及调用后的结果回执、delivery_unknown沿用既有outbox协议。Redis与Webhook消费ACK间隙的联合恢复、配置重绑/并发撤权及实际投递端到端仍待完成。普通模式同步路径也是待迁移项：S5最终需统一持久来源及投递所有者、移除旧同步发送，不把两种模式各自一套长期路径视为完成。

没有新增schema/迁移、共享数据操作或企业外呼。CandidateSHA仍为d7470a32dbb87acc9b5e4d9a895a146410723561，候选未启动、无push/main合并；S5及完整B2/B3/T3/T4/G2/G3均未完成。

### B2 S5 Webhook 实际 Worker 与冻结目标投递（2026-09-13）

在 `e2d29daef` 后接入唯一共享outbox Worker。`s5-webhook-worker-red.log` 真实PG已入队但0发送；新增WebhookDeliveryHandler核验publishing token/租约/attempt marker、tenant/WorkItem/aggregate/完整JSON摘要、消费Audit身份与意图成员，用Audit字符串保留的原始Source重验authority，解决JSONB重排与字节来源合同的衔接。发送后再次核验同一持久claim/来源及实例，再原事务写 `webhook_deliver:<eventID>` Audit；原Worker最终标记published，不增加平行轮询/重试器。

Manager按tenant/name/provider返回精确已配置对象及锁内递增generation。producer从实际对象取得URL摘要；builtin Init冻结endpoint、签名secret及摘要，删除重复cfg来源，发送使用已核验的同一对象，不在发送时重新解析可重绑key。发送后当前generation变化转delivery_unknown；generation只标识本进程实例更换，不是持久配置版本或并发撤权栅栏。独立审阅P1指出默认HTTP重定向能绕过目标冻结，`s5-webhook-worker-redirect-red.log` 实际A307→B一次外呼复现；客户端禁止自动redirect，非2xx错误，GREEN后B零外呼且unknown不重投。

`s5-webhook-worker-green.log` 验证两个原目标各投递一次、published后再次poll不发送；`s5-webhook-worker-faults.log` 覆盖redirect、投递前destination_changed（零外呼明确blocked）、发送中同key重绑（原目标一次、新目标零次、unknown）、实际Audit INSERT后故障回滚与HTTP503（一次外呼、unknown）；所有blocked再次poll不增加发送次数。预检明确JSON/回执/摘要拒绝使用typed blocked，数据库临时错误保留cause由原Worker处理；发送后错误统一未知结果，不以重试掩盖。HTTP测试均为任务内loopback端点，不是企业实发。

bootstrap实际注册器按webhook capability决定handler或known reserved type；disabled时outbox仍可运行但Webhook意图不被领取、修改或转unknown。配置注册单测与真实PG reserved poll完整意图行保全/零外呼分别验证接线与Worker行为。直接新handler的内部调用仍需持久claim/attempt，不以payload或配置scope自报授权。

回归异常单列：`s5-webhook-worker-final-race.log` 首轮完整测试在执行Webhook之前的KAF_access_completion回执重放出现一次 `verified access replay evidence unavailable`（line562），本轮Webhook场景全部通过、无race警告。`s5-webhook-kaf-recheck.log` 该子测试单独race连续3次PASS；原因尚未确定，不能将其称已修复或删除失败证据。service/connector/.../bootstrap/eventbus回归 `s5-webhook-worker-regression.log` PASS。最终补强后的完整私有PG/Redis/MinIO回归 `s5-webhook-worker-final-private.log` PASS，无skip或race；全后端 `s5-webhook-worker-build.log` exit0，git diff --check通过。独立review_execution_scope_s1最终复核配置注册与错误分类无新增阻断；这些通过不消除上述偶发KAF失败的根因缺口。

本阶段仍不等于S5/G2完成：普通模式同步发送尚须迁入同一持久所有者并移除旧路径；Redis消费至出站的ACK间隙联合恢复、进程重启、完整权限撤销竞争、无效消息持久阻断及其它异步入口仍待验证。固定CandidateSHA不变、候选未启动；无schema迁移、共享环境改动、企业外呼、push或main合并。

### KAF 回执重放偶发失败的确定性修复（2026-09-13）

在 `be81ef8b4` 后追查上轮KAF replay失败。私有PG16只读探针证明PostgreSQL先将十进制秒小数解析为float再放大到微秒：`.0010005` 实际存1001μs，`.0080005`存8001μs，`.2510005`存251001μs；旧helper先把整数纳秒除1000，得到精确十进制半值，RoundToEven分别算1000/8000/251000。原fixture随机毫秒落点使该差异表现为偶发；不是Webhook外呼或已提交回执丢失。

将success_even_half固定为过去一秒的`.0010005`，fixture任务创建时间设在5秒前以满足原有验证时序；`kaf-replay-binary-half-red.log` 在原line562稳定复现相同错误。生产只修改accessReceiptTimestamp的运算顺序：先形成fractional seconds float，再乘1e6并RoundToEven；继续只处理小数部分，避免UnixNano有效年限。没有放宽±微秒容差，没有修改原始请求digest、身份/授权/字段比较或任何持久时间值，不执行历史修复/backfill。

`kaf-pg-fraction-probe.log` 保存7个实际PG输入/输出参考（含向上、精确偶/奇半值、普通纳秒及进位到下一秒）；同包单元测试使用这些参考覆盖1800/2026/2500年。初次把helper测试加在外部test package导致编译错误，见kaf-replay-fraction-regression.log，不计业务RED；已将新测试放入同目录access_completion_precision_test.go，既有测试不迁移。该日志中的service包已PASS，修正落点后kaf-replay-fraction-domain.log领域全包PASS。

`kaf-replay-binary-half-green.log` 固定真实KAF完成/重放race PASS；`kaf-replay-fraction-full-private.log` 完整intake、Webhook真实Worker正负测、Redis离线/历史保全及PG/Redis/MinIO应用构造保全PASS，无skip或race。全后端kaf-replay-fraction-build.log exit0，git diff --check通过；独立review_execution_scope_s1审阅精度、历史整数微秒和测试边界无阻断。该确定性RED→GREEN解释并修复上轮已知半微秒重放缺口，不以此前三次随机复测通过代替根因。

证据来自本机私有PG16，仍须在B的目标PG17准入/业务测试中复核；不等于目标环境验收。CandidateSHA与候选停止状态不变，S5其它入口、普通模式统一、完整T3/T4/G2/G3仍未完成，无共享环境变更、企业外呼或push/main合并。

### B2 S5 Webhook 消费提交后 ACK 缺口联合恢复（2026-09-13）

在 `565769236` 后补充真实PG/Redis/loopback联合测试 `webhook stream recovers committed intents before ack`。由真实SLA monitor及原持久发布者产生来源；真实Webhook owner提交两条目标意图和唯一消费Audit后，测试包装器等待旧消费者取消，不返回成功ACK。关闭旧bus后原PEL entry及consumer保留；新bus使用同一itsm:webhook组领取原entry，PEL归零，新旧consumer不同，Stream原entry、两条完整Outbox行及消费Audit整行不变。恢复前两个端点均零调用；随后真实共享Worker逐目标投递，两端点各一次、意图published并存在交付Audit，再poll不增加调用。旧裸Stream/组/历史pending快照保全。

仅增加集成测试并将既有ACK包装器的具体审计类型改成共享窄接口，原审计恢复测试仍调用真实owner；没有生产代码、schema、配置改动。新增覆盖首次运行即PASS，不虚构RED或声称本轮修复生产缺陷。`s5-webhook-ack-recovery.log` 定向race PASS；`s5-webhook-ack-full-private.log` 完整候选intake、旧审计恢复、Webhook负测和新联合恢复、Redis离线/历史保全、PG/Redis/MinIO构造保全race PASS，无skip/race。git diff --check通过，独立review_execution_scope_s1只读复核无阻断。本轮仅测试/文档变化，以真实集成编译及运行验证，不重复既有生产构建。

该测试证明消费者关闭后重建的同组恢复，不证明整个应用强杀、Redis服务重启持久性、任意出站窗口exactly-once或并发撤权。HTTP端点断言调用次数，本项未逐字段核验收到的业务body。普通模式同步路径统一、进程重启、未知消息与其它异步入口仍未完成，S5/T3/T4/G2/G3未放行。固定CandidateSHA不变、候选未启动，无共享环境变更、企业外呼、push或main合并。

### B2 S5 普通/候选持久来源共用校验前置（2026-09-13）

在 `15e6e2c01` 后推进普通模式Webhook统一，发现ExecutionPolicy丢弃standard部署身份，ExecutionEventAuthority只接受CandidateRef。新增真实PG `standard persistent authority retains source boundaries`，原生产代码在可信standard身份正向调用处稳定拒绝，见 `s5-standard-source-red.log`。policy现在冻结deploymentID，EventRef返回standard的可信部署/tenant与空scope或candidate原ref；CandidateRef仍拒绝standard，nil/无效tenant/未知模式/清单外candidate仍拒绝。authority改用EventRef，复用原BindEnt/RequireEntMembers、持久Outbox结构主体和eventID、原载荷字节及发生时间比较，不新增平行校验器。

`s5-standard-source-green.log` 真实PG正向与伪造部署、额外scope、错误主体、零主体、缺失来源、跨tenant、未知类型、载荷/时间篡改负测race PASS；同时伪造ref与env不能替换冻结身份，两种模式不能通过互换ref降级。该测试显式使用私有owner连接读取真实SLA持久源，证明来源校验及源行/审计保全，不证明standard应用角色准入；standard不同主体负测证明持久来源不匹配，不声称standard需要候选成员登记。

`s5-standard-source-regression.log` database/service/eventbus/bootstrap回归PASS，`s5-standard-source-full-private.log` 完整候选边界、来源负测、Webhook Worker与Redis ACK恢复、PG/Redis/MinIO构造保全race PASS，无skip/race。全后端 `s5-standard-source-build.log` exit0，git diff --check通过；独立review_execution_scope_s1只读复核无阻断。开发指南已说明EventRef不是执行许可。

普通传输仍须保留持久身份并使用明确typed订阅合同，随后将普通Webhook接入同一意图/Worker并删除旧同步发送；本前置不算S5统一完成。全应用重启、其它异步入口及B2/B3/T3/T4/G2/G3仍未完成，CandidateSHA和未启动状态不变。没有共享环境修改、企业外呼、push或main合并。

### B2 S5 普通模式持久事件传输前置（2026-09-13）

在 `536b48e3d` 后接入普通传输的持久身份。`s5-standard-transport-red.log` 复现普通发布生成随机UUID且订阅者收到flattened map；现在ExecutionEvent在两模式均验证稳定类型/tenant、完整信封与真实authority，使用原persistentID。streamRoutes冻结standard部署，空scope仅由standard配置产生；candidate仍使用准入scope和独立namespace。新增ExecutionEnvelopeHandler显式订阅合同，两模式在严格JSON、部署/scope/tenant、消息UUID/metadata及持久来源验证后才调用该owner；没有持久主体的旧事件不伪造WorkItem，既有非typed普通订阅仍按其原合同接收。

独立审阅发现P2：仅实现ExecutionEvent而不实现稳定事件接口时仍能落入raw发布；`s5-standard-transport-contract-red.log` 复现返回nil。现于序列化/路由前拒绝该形状，负测同时要求publisher零写，复审关闭。`s5-standard-transport-green.log` 初步eventbus race PASS；补强后 `s5-standard-transport-regression.log` eventbus/service/bootstrap race全包PASS。

`s5-standard-transport-pg-redis.log` 使用真实SLA持久来源、明确私有owner校验连接及真实Redis，普通bus发布两次均以原eventID交付typed Envelope，冻结部署、空scope、WorkItem与payload保持，源Outbox整行和Audit保全。该连接不代表standard应用角色准入；传输重复交付不是外部副作用恰好一次。首次完整 `s5-standard-transport-full-private.log` 因旧历史fixture误用实现ExecutionEvent的类型且无authority而拒绝；改用明确不声明持久合同的historicalStreamEvent定义类型，保留原JSON字段/稳定元数据和原历史Stream/组/PEL断言，未放宽生产校验。最终 `s5-standard-transport-final-private.log` 完整候选边界、普通来源传输、Webhook Worker/ACK恢复及PG/Redis/MinIO构造保全race PASS，无skip/race；全后端 `s5-standard-transport-build.log` exit0，git diff --check通过。独立review_execution_scope_s1复核P2及fixture修订无新增阻断。

普通Webhook尚未声明typed合同，仍须迁入原意图/Worker并删除同步分支；普通durable consumer组、进程重启和其它异步入口继续待完成。本前置不等于S5或候选交付完成，CandidateSHA及候选停止状态不变，无共享环境修改、企业外呼、push或main合并。

### B2 S5 普通/候选 Webhook 持久所有者统一（2026-09-13）

在 `83a9591a3` 后删除Webhook subscriber的完整standard同步外发分支，两模式唯一调用consumeExecutionWebhook；声明ExecutionEnvelopeRequired，原意图事务、消费Audit与WebhookDeliveryHandler不复制实现。普通typed订阅强制稳定logical owner，采用同一itsm:owner持久组/factory和Close管理；重复登记/启动拒绝，身份登记时冻结，同owner跨topic复用subscriber。非typed普通订阅仍保留原fanout合同，不伪造建单前AI事件主体。盘点无生产调用后同时删除旧SendToInstance helper，既有精确实例测试保留原位置并核验Worker真实使用的GetInstance。

`s5-standard-webhook-red.log` 在旧路径返回成功后缺少消费Audit，确定性FAIL；`s5-standard-webhook-green.log` 真实PG/Redis/loopback原意图及Audit提交、阻断ACK、旧consumer退出同PEL保留、新consumer同组领取后原完整意图/Audit/Stream entry不变，通过race。随后真实共享Worker两目标各一次、published及交付Audit、重复poll无新增发送，另一历史topic/组/PEL保全。源由本测试candidate创建/monitor生成真实SLA事实，普通消费/Worker显式使用私有owner连接与standard policy；该组合验证共享所有者及传输合同，不冒充standard应用角色准入或完全普通模式创建旅程。端点断言次数，未逐字段核验body，重建consumer不等于OS/Redis服务重启。

独立P2指出typed普通订阅缺authority仍可登记启动，`s5-standard-webhook-authority-red.log`复现；consumerIdentity现在登记和动态Subscribe均先拒绝缺失依赖，测试要求零durable分配且非typed普通订阅仍可启动。`s5-standard-webhook-authority-green.log` eventbus全包race PASS，复审关闭。`s5-standard-webhook-regression.log`相关包PASS；`s5-standard-webhook-full-private.log`完整候选边界、两模式来源/传输、Webhook故障和ACK恢复、PG/Redis/MinIO构造保全race PASS，无skip/race。删除无调用旧helper后 `s5-standard-webhook-final-regression.log` eventbus/service/bootstrap/connector/...再次PASS；全后端 `s5-standard-webhook-build.log` exit0，git diff --check通过。独立review_execution_scope_s1最终复核无阻断，开发指南同步删除旧同步流程描述。

该检查点完成普通Webhook与候选持久消费/投递所有者的代码统一，不等于全部S5或候选交付完成。不可处理/旧非持久消息当前明确NACK，持久可见阻断、整个进程重启、并发撤权和其它异步入口仍待完成。CandidateSHA不变、候选未启动，B2/B3/T3/T4/G2/G3尚未放行；无共享环境修改、企业外呼、push或main合并。

### B2 S5 持久首次拒绝诊断与已确认队头阻塞（2026-09-13）

在 `4b7db1035` 后补齐计划允许的新独立诊断。typed/candidate的信封、身份、来源及handler拒绝使用固定原因，复用唯一Redis publisher client向 `<physical-topic>:rejections:<frozen-owner>` 执行HSETNX。指纹由payload/UUID/event_type固定宽摘要组合生成，记录仅含status=rejected、允许的原因、摘要及首次时间，不复制原始载荷/UUID/任意错误文本。键来自冻结订阅路由，不从未授权payload自报tenant或scope建记录。首次事实不可重写、无自动TTL；不是业务Audit回执、不是ACK许可或永久禁用来源。失败记录写入失败也继续NACK，客户端仍在消费者退出后随publisher关闭。

`s5-stream-rejection-red.log` 两模式真实Redis缺失诊断RED。第一次实现验证 `s5-stream-rejection-green.log` 实际FAIL：诊断已存在/可跨consumer恢复，但后续好消息未被处理。核对实际watermill-redisstream v1.4.5的processMessage同步ResendLoop，NACK会阻塞该消费者的后续消息。本轮未修改此行为，保留失败证据并将持久隔离/人工处置列为后续缺口；移除了本轮自行附加但未实现的“后续好消息继续”成功断言，不能因此声称队头阻塞已解决。独立审阅亦纠正先前将待执行预期称为测试支持的表述。

最终 `s5-stream-rejection-evidence.log` 两模式真实Redis验证首次记录、零handler、同PEL entry跨consumer重建保留及完整诊断不变；WRONGTYPE真实诊断键故障验证错误观察、原key保全、PEL1/零handler，移除私有故障后记录成功。另一个测试先发布同一消息，再令fixture authority拒绝，恢复允许后原消息消费ACK、PEL0、Stream仍一entry且首次记录不变。此授权使用fixture，不等于真实PG撤权联合恢复或永久隔离；未验证Redis服务重启持久性。两个测试race PASS。

`s5-stream-rejection-regression.log` eventbus/service/bootstrap回归PASS；`s5-stream-rejection-full-private.log`完整候选边界、普通/候选来源与Webhook恢复、PG/Redis/MinIO构造保全及新增拒绝测试race PASS，无skip/race。全后端 `s5-stream-rejection-build.log` exit0，git diff --check通过。独立review_execution_scope_s1复核记录边界、故障和同消息恢复无新增阻断，结论限定为首次拒绝诊断及可重验；开发指南已明确队头阻塞与证据读取方式。

S5不可处理消息的完整处置、进程重启及其它异步入口继续未完成，不以该诊断检查点放行B2/B3/T3/T4/G2/G3。CandidateSHA及未启动状态不变，无共享环境变更、企业外呼、push或main合并。


### B2 S5 拒绝消息保全与后续消费推进（2026-09-13）

在 `d541ed9f3` 后恢复“拒绝原消息后，后续合法消息仍可交付”的真实Redis断言，`s5-stream-progress-red.log` 在普通及候选模式稳定失败。根因为原watermill-redisstream订阅器同步NACK循环阻塞后续读取。本轮只替换唯一WatermillEventBus中持久组的底层订阅器；普通非持久fanout保留既有合同，没有新增业务总线或第二个消费所有者。

新持久订阅交替执行有界XREADGROUP及带游标XAUTOCLAIM，NACK保留原PEL并继续读取，只有业务ACK后执行XACK；XACK失败原消息仍待恢复。空扫描页保留非零游标；显式订阅的首次真实claim既验证命令权限，又把领取结果交给正常处理。构造仍无网络副作用，不重设或删除旧组。Redis最低要求6.2及XAUTOCLAIM权限，NackDelay改为Redis操作故障退避，拒绝重领由ClaimIdle/ClaimInterval控制。底层损坏帧在解码前校验类型，记录固定wire_invalid摘要诊断，保留原PEL、不调用业务、不伪造有效载荷；诊断失败也不ACK。并发Close共享退出结果并关闭自有连接。

`s5-stream-progress-green.log` 首次使用不存在的CommandInfo方法导致编译失败，不计业务RED；修正后 `s5-stream-progress-recovery.log` 两模式坏消息保全/诊断存储失败/消费者重建/后续好消息和来源恢复race PASS。`s5-stream-progress-faults.log` 实际ACL禁止XACK后原PEL保留，恢复权限重领同源并清PEL；持续新消息期间旧pending恢复PASS。独立P2指出命令存在探针不证明ACL许可，`s5-stream-progress-acl-red.log` 实际禁用XAUTOCLAIM复现错误放行Start；改成首次实际claim后 `s5-stream-progress-acl-green.log` PASS。首次领取结果不丢弃，`s5-stream-progress-cursor-wire.log` 覆盖1条初始可领消息、21条pending中连续空页后的老消息、同组两活消费者及损坏帧后的正常推进，race PASS。

独立review_execution_scope_s1复核生产逻辑无新增阻断；两项测试建议已关闭：并发Close后Ping严格要求redis.ErrClosed，两活消费者在PEL0后显式关闭并检查尾部缓冲无重复。`s5-stream-progress-final-regression.log` eventbus/config/bootstrap race PASS；最终 `s5-stream-progress-full-private.log` 完整intake、真实PG审计与普通/候选Webhook ACK缺口恢复、离线与历史Stream保全、所有PersistentStream测试及PG/Redis/MinIO构造保全race PASS，无skip/race。全后端 `s5-stream-progress-build.log` exit0，git diff --check通过。

证据仅为本机私有PG16/Redis7.2/MinIO及loopback；目标PG17和B环境准入未验证。消费者重建不等于应用强杀或Redis服务重启，传输不是恰好一次，慢处理并发重领仍依赖业务持久幂等；永久拒绝消息人工处置及其它异步入口仍待完成。修正开发指南中普通Webhook仍同步发送的过时段落。S5/T3/T4/G2/G3保持未完成，固定CandidateSHA不变、候选未启动，无共享数据库或B配置改动、企业外呼、push或main合并。


### B2 S5 独立消费者进程强制终止恢复（2026-09-13）

在 `aab8dfcc2` 后增加 `TestPersistentStreamRecoversAfterConsumerProcessKill`，复用真实WatermillEventBus和私有Redis，使用当前测试二进制启动两个不同PID的消费者子进程。父进程创建私有随机口令/PID核验Redis，连接配置通过stdin传递；子进程连接后先核验同一Redis PID，再显式订阅，不接触共享环境。首进程收到原完整信封后写测试收据并等待，不返回ACK；父进程核验完整可解析收据的EventID/WorkItemID以及原PEL entryID后调用Process.Kill，确认非正常退出和原PEL/consumer仍在。第二进程从同组恢复，核验完整信封等价、PEL归零、Stream原行完整不变和不同消费者身份。

独立review_execution_scope_s1发现首轮仅Stat收据可能在文件创建但尚未写完时Kill，导致测试偶发失败；现等待ReadFile+完整JSON及预期主体再保存首快照，最终比较该快照，复审已关闭。本项只增加测试，无生产修复，不虚构业务RED。`s5-stream-process-kill.log` 首次定向race PASS；`s5-stream-process-full-private.log` 完整候选intake、真实审计/Webhook恢复、所有PersistentStream及私有PG/Redis/MinIO构造保全race PASS。仅同步断言补强后 `s5-stream-process-final.log` 定向race连续3次PASS，无skip/race；git diff --check通过。仅测试和说明变更，以真实测试编译/执行验证，不重复上轮已通过且生产代码未变的全后端build。

结论只覆盖消费者独立进程强制终止的至少一次传输恢复：测试authority及本地信封收据不是PG业务提交回执，也不证明完整应用/Worker或Redis服务重启、WSL角色/目标PG17准入及G3的60分钟观察。后续仍需真实环境和完整业务路径验证。CandidateSHA及候选未启动状态不变，S5/T3/T4/G2/G3未完成，无共享数据、B配置、企业外呼或push/main合并。


### B2 S5 历史工具审批触发候选执行的真实 RED（2026-09-13）

在 `2626cd59c` 后按总设计§5与S5检查工具队列：bootstrap只控制Start，ToolQueue没有冻结policy；Enqueue只检查整数身份/队列状态，ProcessJob可被直接调用。ToolInvocation没有结构化scope关联；现有审批更新及完成/失败写回也未绑定候选执行事务。WorkItem写入者的成员保护不能证明其来源工具调用获准执行。

新增 `TestCandidateIntakeCreationBoundary/historical approved tool cannot authorize candidate execution`：在私有PG的候选scope准备前创建历史approved create_ticket，随后使用真实受限tenant runtime client、candidate intake及原ToolQueue直接ProcessJob；未启动候选应用、未使用owner执行业务。owner连接只用于测试准备及整行对账。`s5-tool-history-red.log` 实际FAIL：ProcessJob返回nil，历史调用由pending改为done并新增result，WorkItem从10增至11，scope成员从0增至1。四项断言分别检查错误、调用整行、WorkItem数、成员数；此项不声称覆盖全部历史业务行或整个HTTP审批链。

独立review_execution_scope_s1确认根因与RED有效。修复必须从invocation首次INSERT原事务建立可信结构归属，历史行保持未登记；create_ticket尚无WorkItem，不能拿未来工单或Arguments/时间戳作来源许可。审批更新本身、enqueue、执行预检、业务首次写入原事务及完成/失败回写均需重新守卫。仅增加启动开关或执行前一次检查不能关闭缺陷，拒绝历史调用不得将其改写failed。现有稳定invocation operation ID和业务回执继续负责ACK缺口恢复，不能另建业务状态机。

本轮仅新增真实回归测试，生产缺陷尚未修复，当前该测试及包含它的完整套件不能报告GREEN；此前通过结果属于之前代码/断言范围。没有运行与本项无关的重复构建，git diff --check通过。下一步按执行计划中的工具来源事务链补齐迁移与角色、创建/审批/队列/业务/回执共同边界后将该RED转GREEN，保留新工具正向与重放能力，不把永久disabled当作G2完成。CandidateSHA保持不变、候选未启动、S5/B2/B3/T3/T4/G2/G3仍未通过，无共享环境改动、企业外呼或push/main合并。


### B2 S5 工具来源041结构登记前置（2026-09-13）

在 `674fc9885` 后新增唯一注册迁移 `041_tool_invocation_execution_scope`，目录按039→040→041，要求040与既有037准备链，不改038退休契约及历史SQL/checksum。新execution_tool_invocations以invocation_id为主键，复合外键约束invocation/tenant及scope/tenant。只有tool_invocations首次INSERT的SECURITY DEFINER触发器登记；固定search_path并验证触发位置，锁定session_user绑定及active scope，核对部署/tenant/session tenant；standard绑定直接返回不登记。候选原INSERT失败则登记回滚，历史调用没有登记更新路径。新表RLS只读、PUBLIC和角色默认表/函数授权均剥离，不授予运行角色直接写入。

`s5-tool-enrollment-red.log` 真实私有PG测试先在未注册迁移处FAIL；`s5-tool-enrollment-green.log` 新结构初步race PASS。审阅建议后补充默认ALL TABLES授权剥离、外租户/closed scope/撤销binding的INSERT拒绝及原表/登记表计数无残留，显式事务立即登记rollback清理。`s5-tool-enrollment-final.log` 真实受限tenant角色race PASS：历史invocation整行JSON不变、无历史登记、新INSERT与登记同事务可见/回滚/提交、未绑定插入失败、直接补登记及删除拒绝。外租户拒绝可能由既有RLS先执行，因此仅证明整体隔离，不单独声称命中新触发器。测试owner仅作准备和对账，数据库为任务私有PG16，不是B的目标PG17。

`s5-tool-enrollment-migration.log` migration全包PASS，新增合法040账本仅待041及缺037/039/040的非法041账本拒绝，既有退休账本升级测试仍通过。独立review_execution_scope_s1最终只读复核无新增迁移前置阻断。`s5-tool-enrollment-full-private.log` 完整私有回归实际FAIL，仅历史工具执行子测试及其父测试失败，其余具名子项通过，无skip/race；不能称完整GREEN。新增登记不阻止ProcessJob读取旧approval，故该RED按预期保留。全后端 `s5-tool-enrollment-build.log` exit0，git diff --check通过。

下一步必须贯通运行时新表只读权限准入（runtime_clients及execution admission）、AI首次INSERT/审批原事务、队列及业务首次写入来源复核、结果条件写回；尚未接入的新调用路径会因未绑定范围而拒绝，不能单独迁移后启动应用。此项只完成数据库前置，不以它关闭S5/B2/B3/T3/T4/G2/G3。CandidateSHA与未启动状态不变，未改B配置/共享数据库、未企业外呼、未push/main合并。


### B2 S5 ToolQueue 持久来源预检（2026-09-13）

在 `762bd4f61` 后给ToolQueue构造器显式注入冻结ExecutionPolicy，bootstrap和既有标准fixture同步。ProcessJob在读取审批或任何调用写入前，拒绝nil/异tenant/SystemBypass上下文，在独立原连接事务BindEnt并RequireEntToolInvocation：查询041结构关联、真实invocation、tenant、active scope和session_user绑定，缺来源返回ErrDenied，不登记或改写历史。标准模式保持既有审批、actor及工具业务合同；来源helper不是业务授权，预检结束后仍需在首次业务写事务复核。

`s5-tool-origin-green.log` 把此前历史调用返回nil/改旧行/新增WorkItem/member的RED转为来源拒绝保全。独立P2指出初版把所有SQL错误都转换ErrDenied，可能以缺表/权限假GREEN；现仅sql.ErrNoRows分类拒绝，其余保留原cause。历史独立子测试明确准备041及SELECT权限，正向测试在原事务未提交登记时通过，历史无登记ErrorIs(ErrDenied)。`s5-tool-origin-controls.log` 实际REVOKE SELECT产生非ErrDenied且可提取pq.Error 42501，恢复权限后新登记approved调用ProcessJob两次只新增一个WorkItem/member，旧调用整行不变，race PASS。新调用审批由测试准备，不声称验证HTTP提议/审批链。

`s5-tool-origin-regression.log` database/service/bootstrap及既有工具集成的具名定向race通过；不是四个包全部测试。独立review_execution_scope_s1最终复核P2关闭，无新增预检阻断。`s5-tool-origin-full-private.log` 完整私有PG/Redis/MinIO候选边界、两模式Webhook/审计恢复、Stream消费及进程终止恢复race PASS，无skip/race；`s5-tool-origin-build.log` 全后端build exit0，git diff --check通过。

该检查点只关闭已复现的历史调用直接执行入口；enqueue、AI创建及审批原事务、业务首次写事务再次校验、完成/失败回写条件以及新表运行时权限审计仍未完成。独立预检不能关闭其后的撤权竞争，不用它宣称S5或真实候选业务准入。CandidateSHA及候选未启动状态不变，无共享环境改动、企业外呼、push/main合并。


### B2 S5 工具入队与执行共用来源和审批预检（2026-09-13）

在 `677fdd6b2` 后新增真实Enqueue断言，`s5-tool-enqueue-red.log` 复现历史调用入队返回nil；执行预检随后拒绝不代表入队已受控。现在Enqueue和ProcessJob复用loadApprovedTool，在一个事务读取041来源、当前审批状态、有效调用者/审批者及既有ai:write权限，删除分散的重复预检逻辑。执行仍重新调用，不信任内存任务已有检查结果；新工具通过实际worker完成，再直接重放只新增一个WorkItem/member。该读取事务使用默认隔离，不声称一致快照/审批锁，也不替代业务原事务授权。

入队在accepting状态锁内登记进行中检查，锁外以worker生命周期派生30秒上限context校验，完成后锁内重查状态与容量。Close取消worker/context并等待worker及所有入队检查退出；Add与停止状态切换同锁，关闭后没有新Add。测试初始化显式注入测试admit函数，生产构造固定真实校验且不接受nil，不新增配置旁路。新TestToolQueueCloseWaitsForAdmissionAndPreventsLateEnqueue在校验收到取消后故意阻塞，证明Close仍等待，释放后没有业务调用且再入队ErrClosed。

`s5-tool-enqueue-green.log` 原入队RED→GREEN；补强后 `s5-tool-enqueue-lifecycle.log` 工具/生命周期及既有工具集成的具名定向race PASS。`s5-tool-enqueue-full-private.log` 完整候选边界/PG/Redis/MinIO构造保全、审计/Webhook恢复及Stream/子进程恢复race PASS，无skip/race；包含pending、rejected、dry_run、inactive actor入队拒绝且调用整行不变/无工单新增。独立review_execution_scope_s1复核限定入队增量无新增阻断。`s5-tool-enqueue-build.log` 全后端build exit0，git diff --check通过。审阅后的测试补强覆盖忽略取消仍返回nil时最终ErrClosed拒绝，及各等待点5秒超时；`s5-tool-enqueue-late-success.log` 两种取消行为race PASS，生产代码未再变更。

尚未完成：AI创建/审批写入原事务、业务首次写事务复核、结果条件回写、新表运行角色准入。当前actor/approver查询沿用既有PermissionDenied包装并保留cause，不把它说成全部基础设施错误分类已统一。CandidateSHA及候选未启动状态不变，S5和后续G2/G3未放行；没有共享环境、B配置、企业外呼、push/main合并。


### B2 S5 AI 工具调用创建事务接入（2026-09-13）

在 `ed4f8c360` 后真实AI仓库创建测试 `s5-tool-create-red.log` 复现原autocommit INSERT被041触发器以execution scope required/42501拒绝。EntRepository构造现在显式要求冻结ExecutionPolicy和tenant client，bootstrap统一注入。CreateToolInvocation拒绝nil/非法tenant/异tenant上下文/SystemBypass，在自有事务BindEnt→INSERT→Commit，触发器在相同事务登记；只有提交成功才返回，不增加历史补登记或fallback。该方法自行拥有事务，未来其它原子写必须提供显式原事务入口，不能嵌套调用。

`s5-tool-create-green.log` 创建RED→GREEN；`s5-tool-create-atomic.log` 私有PG真实受限tenant client覆盖pending与auto两种记录形状均登记、实际INSERT成功后Ent hook注入故障时invocation及登记同时回滚、nil/异tenant/closed scope拒绝无新增、临时明确standard绑定时正常创建且零候选登记，race PASS。auto/pending仅证明持久记录形状，不代替Service.ExecuteTool/RBAC/recordToolAudit完整流程；standard仅证明模式合同，不代替角色启动准入。测试owner只作任务私有准备和对账，无共享操作。

`s5-tool-create-regression.log` handlers/ai与bootstrap全包race PASS；独立review_execution_scope_s1只读审阅本创建前置无阻断。`s5-tool-create-full-private.log` 完整私有PG/Redis/MinIO候选边界及恢复回归race PASS，无skip/race；`s5-tool-create-build.log` 全后端build exit0，git diff --check通过。

仍须完成审批更新事务、审计写失败传播、业务首次写事务来源再校验、结果回写及041表角色准入；S5和T3/T4/G2/G3保持未完成。固定CandidateSHA与候选未启动状态不变，无企业外呼、B环境修改、push或main合并。


### B2 S5 工具审批决定事务与接口分类（2026-09-13）

在 `47b40a726` 后真实Service.ApproveTool拒绝历史调用测试 `s5-tool-approval-red.log` 复现返回成功并改写旧调用。删除仓库通用UpdateToolInvocation，收窄为唯一DecideToolInvocation(id/tenant/actor/approve/reason)事务：BindEnt、041来源、当前有效审批者及ai:write权限，读取原调用，仅needsApproval且非dry_run的pending/pending行CAS更新决定/原因/actor/服务器时间，不回写陈旧Result/Status等其它字段。相同actor/决定/原因且已有决定时间时重放保持原字段；不同最终决定冲突。批准与拒绝都保存actor/time；Service仅在事务提交后enqueue。

独立P2发现HTTP仍把全部错误映射404；已改冲突409、范围/权限403、实际NotFound404、内部故障500，固定文本不泄rawcause。新增ErrToolExecutionPending标识审批已提交而enqueue失败，优先返回503并明确执行仍待入队；调用方不能把该情况当未审批。approve必须显式bool，缺用户身份401。`s5-tool-approval-http.log` AI全包race及具名HTTP分类/敏感文本不泄漏测试PASS，最终独立复核关闭P2。

`s5-tool-approval-green.log` 原历史改写RED→范围拒绝/整行保全；`s5-tool-approval-atomic.log` 真实PG新审批提交后queue缺失、原决定重试首次整行不变、相反决定冲突、当前role撤权后重放拒绝、拒绝actor/time记录、实际UPDATE后注入故障整行回滚均PASS。`s5-tool-approval-concurrent.log` 补同/反决定的双UPDATE barrier，两请求均到原pending更新前，一成功一冲突，随后获胜原请求重放整行不变，race PASS。相同决定并发loser允许409后再重试，不声称两请求立即都成功；此记录是审批决定保全，不是完整不可变执行结果回执。

`s5-tool-approval-regression.log` handlers/ai与bootstrap全包race PASS，独立review_execution_scope_s1最终只读复核无新增阻断。`s5-tool-approval-full-private.log` 完整私有PG/Redis/MinIO候选边界与恢复回归race PASS，无skip/race；`s5-tool-approval-build.log` 全后端build exit0，git diff --check通过。首次业务写原事务、执行结果回写、审计失败传播和041角色准入仍未完成；候选未启动、固定CandidateSHA不变，所有交付门禁未据此放行，无共享环境修改、企业外呼、push/main合并。

### B2 S5 工具审计失败传播（2026-09-13）

原未知工具路径忽略审计写入错误，`s5-tool-audit-red.log` 证明底层cause丢失。现ExecuteTool在执行前校验参数序列化；未知工具、权限拒绝、只读成功及失败均等待审计持久化，使用固定结果标签，errors.Join保留执行与审计cause。审计失败返回ErrToolAuditUnavailable且不返回工具结果；HTTP不再提前跳过未知工具审计，固定503隐藏底层原因，其余失败也不返回原错误。直接写工具仍走既有拒绝入口。

真实PG回归使用真实registry/IncidentService/AI仓库：成功读取后审计INSERT故障使调用与scope登记共同回滚；撤销本测试角色的incidents SELECT使实际工具读取失败，但成功新增一条failed审计和登记，未误报审计不可用。权限在测试结束恢复。HTTP实际handler测试确认503及cause隐藏。`s5-tool-audit-packages.log` AI/bootstrap全包race PASS；`s5-tool-audit-full-private.log` 完整私有PG/Redis/MinIO候选边界、Webhook/审计与Stream恢复回归race PASS，无skip/race；`s5-tool-audit-build.log` 全后端build exit0。独立review_execution_scope_s1复核无新增阻断，git diff --check通过。

证据仅为本机私有PG16等依赖。查询与审计不在同一事务，未实现请求重试去重；直接测试身份/可选缓存RBAC不代表完整HTTP权限验收。首次业务写原事务来源复核、结果条件回写、041运行角色准入等仍未完成。CandidateSHA与候选未启动状态不变，S5/T3/T4/G2/G3未放行，无B环境/共享数据库变更、企业外呼、push/main合并。

### B2 S5 041业务运行身份准入（2026-09-13）

真实PG `s5-tool-role-red.log` 复现四种旧准入错误放行：041登记表缺SELECT、表UPDATE、列UPDATE及登记函数EXECUTE。ValidateExecutionRuntime现将execution_tool_invocations纳入必需只读对象，并检查register_new_execution_tool_invocation()不可由业务身份直接执行。既有有效身份通过，逐项危险授权拒绝，撤回后再次通过；不修改迁移内容或放宽运输身份白名单。工具来源由tenant路径读取，运输身份不需要新增工具登记权限。

构造保全fixture在快照前安装041，保持业务身份SELECT及运输身份原最小权限。它不是完整迁移目录顺序验收。`s5-tool-role-full-private.log` 包含ScopeRegistration、完整Intake、构造保全及Stream/Webhook/审计恢复race PASS，无skip/race；`s5-tool-role-packages.log` database/bootstrap全包race PASS，`s5-tool-role-build.log` 全后端build exit0。独立review_execution_scope_s1复核无阻断，git diff --check通过。

本检查点完成代码侧041业务角色准入，缺对象/授权会启动失败；B仍须在目标PG17按完整迁移链和显式只读授权执行准入。本机PG16合成fixture不替代T3环境交接。业务首次写原事务来源复核及完成/失败条件写回仍待完成，S5/T3/T4/G2/G3未放行。固定CandidateSHA与候选未启动状态不变，无共享数据库、B配置、企业外呼、push/main合并。

### B2 S5 工具完成记录保全与事务写回（2026-09-13）

`s5-tool-receipt-replay-red.log` 真实PG证明重复ProcessJob覆盖首次完成结果，replayed从false改为true。最初筛选tool_queue未命中目标子测试，不算RED/GREEN证据。现done在当前来源/审批/身份预检后直接返回；完成与失败写回使用自有事务的共同approvedToolInTx，再按调用tenant/actor/工具/参数/审批者/审批时间/approved且非done条件更新，防止覆盖首次完成记录，失败文本固定。没有将查询预检视为业务首次写许可或并发撤权栅栏。

新增实际UPDATE后故障两分支：创建业务先提交，但调用整行回滚；重试恢复done且仍仅一张工单。失败分支由nil registry触发registry unavailable（不是实际未知工具查找），调用整行回滚，重试failed固定文本且无新业务。首轮可空Error字段断言类型错误已修正，未改变生产逻辑来迎合断言。首次完成整行快照重放保全通过。

补充服务回归暴露关闭竞态：admission返回取消错误时跳过关闭状态分类，既有并发测试断言失败后清理又等待未释放的processor。保留SIGQUIT栈于s5-tool-receipt-regression.log；本次异常终止不算通过。现admission返回后锁内统一判断关闭，errors.Join保留ErrToolQueueClosed及原cause；测试defer+sync.Once保证失败也释放processor。s5-tool-receipt-regression-final.log具名Tool/CreateTicketTool race连续3次PASS（不是service全包），独立review_execution_scope_s1最终复核无阻断。

剩余：成功/失败并发写回及执行中审批/参数变更竞争尚待真实验证，scope撤权竞争栅栏与业务首次写原事务仍未完成。目标PG17/T3环境准入、真实业务/主题T4及G3未通过；固定CandidateSHA不变、候选未启动，无共享环境修改、企业外呼、push/main合并。

最终验证：s5-tool-receipt-final-private.log完整私有PG16/Redis/MinIO的ScopeRegistration、Intake、构造保全及Stream/Webhook/审计恢复race PASS，无skip/race；s5-tool-receipt-build.log全后端build exit0，git diff --check通过。上述均不替代目标环境或完整业务并发验收。

### B2 S5 结果写回撤权窗口的真实RED（2026-09-13）

在da9a5783c结果事务复核之后继续验证并发撤权。真实ToolQueue创建业务提交后，Ent UPDATE hook在实际结果UPDATE前通过独立owner连接关闭scope并提交；随后ProcessJob仍返回nil，调用从pending变done且写入结果。s5-tool-revocation-red.log证明单次事务内查询与条件UPDATE并不封闭scope状态的竞争窗口。测试保留调用整行快照、已提交业务数、撤权实际提交标记，并将撤权连接等待限定5秒；结束时恢复私有scope。此改动仅测试，不修改生产行为。

独立review_execution_scope_s1确认RED有效。后续需在可信数据库边界锁住session_user binding与active scope，锁持有到结果原事务提交/回滚，锁后核验mode/deployment/tenant/scope/登记及事务设置。不可通过授予业务身份范围表UPDATE来获取锁；standard也须明确真实binding。窄SECURITY DEFINER入口须固定search_path、全限定名称、剥离PUBLIC/default ACL，并与现有INSERT/审批统一锁顺序。不能只用FOR KEY SHARE，因为状态更新也必须串行化。

实施验证须区分“撤权先提交→写入拒绝”与“写事务先持锁→独立撤权等待提交/回滚后生效”。若在当前预检位置加锁，现有同步hook会等待自身持有的锁；需改成独立goroutine并以数据库等待证据验证顺序，不能把超时/错误当成撤权已提交。业务首次写、审批参数及结果并发竞争继续待完成。新增RED使当前具名测试及包含它的全套不能报告通过；此前GREEN只属于此前范围。CandidateSHA及候选未启动状态不变，未放行S5/T3/T4/G2/G3，无共享环境操作、企业外呼或push/main合并。

最终具名race复验s5-tool-revocation-final-red.log仍为预期RED：撤权提交标记通过，失败仅为应拒绝却返回nil及调用整行被改写。没有运行不相关构建来掩盖失败；git diff --check通过。生产修复尚未完成。

### B2 S5 候选工具授权事务锁（2026-09-13）

在73c3ce8a4真实撤权RED之后，新增普通迁移042_tool_execution_authority_lock（依赖041/040/039及037，不改变旧迁移SQL或038退役契约）。窄SECURITY DEFINER函数lock_candidate_tool_authority使用session_user按binding→scope→登记FOR SHARE锁序核验显式candidate mode、deployment、tenant、scope、事务设置和真实调用，锁持有至调用者事务提交/回滚。固定search_path和全限定业务对象，剥离PUBLIC/default角色EXECUTE；只有明确授权的候选业务身份可调用，不赋予登记/范围配置DML。登记SELECT不足仍保留42501。候选RequireEntToolInvocation以此唯一入口替换无锁查询；standard保持既有路径，函数本身拒绝standard，不能据此宣称standard授权锁完成。

准入新增候选锁函数EXECUTE要求；真实PG验证缺能力拒绝、明确授权通过。构造fixture先安装042并仅授予业务身份EXECUTE；system运输白名单未放宽。迁移测试按新增实际目录更新数量并覆盖042合法前缀与缺037/039/040/041的拒绝，既有退役账本仍可升级。

原同步撤权hook在新锁下会等待自身，故改为独立连接/goroutine：pg_blocking_pids确认撤权真正等待结果事务，结果提交和实际UPDATE后注入错误回滚两种序列均完成撤权，下一调用拒绝且调用整行保全；已提交创建业务保持一张工单。测试清理先取消并等待已启动revoker完成，再独立有界ctx恢复scope，关闭独立审阅发现的失败清理竞争。s5-tool-authority-lock-private.log定向race GREEN；s5-tool-authority-lock-final-private.log最终完整私有PG16/Redis/MinIO ScopeRegistration、Intake、构造保全及Stream/Webhook/审计恢复race PASS，无skip/race。s5-tool-authority-lock-packages-final.log migration/database/bootstrap全包race PASS；独立review_execution_scope_s1最终复核无阻断。

界限：该项验证候选scope撤权与工具来源原事务；binding并发撤销专项、成功/失败和审批参数变化竞争、业务首次写入来源授权仍待完成。B的目标PG17迁移/角色与T3、真实业务主题T4/G3未通过。目标启动前须按完整清单安装042及显式EXECUTE，不能仅复制代码启动。CandidateSHA与候选未启动状态不变，无共享环境操作、企业外呼、push/main合并。

最终s5-tool-authority-lock-build.log全后端build exit0，git diff --check通过。日志及私有数据库测试结果不替代目标环境验收。

### B2 S5 工具首次创建事务来源核验（2026-09-13）

s5-tool-intake-source-red.log真实PG复现：直接app.Create使用历史approved工具来源（绕过queue）仍创建工单/成员/幂等记录。现intake createAttempt在scope绑定后、receipt Claim前调用同一工具领域来源核验，原事务复用loadApprovedToolTx核验042来源锁、审批和当前身份；验证ai_tool/tool_queue/source元组、规范调用ID、原actor/requester、create_ticket类型，并使用已有toolCreationCommand从批准参数重建命令。与提交命令比较规范摘要及显式IdempotencyKey；不复制第二套参数解析规则。042锁延续至业务原事务结束，旧队列预检保持入队/执行边界用途。

新增负测发现摘要故意不包含IdempotencyKey（s5-tool-intake-source-full-private.log），已显式绑定操作key，关闭独立P1；未通过放宽断言绕过重复创建。另一真实REVOKE users SELECT的s5-tool-intake-actor-sql-red.log暴露SQL故障误包PermissionDenied，现actor/approver仅NotFound作权限拒绝，其他原因%w保留，由intake映射基础设施失败，关闭P2。inactive actor在更早认证层返回AuthenticationRequired，测试遵守现有分类。

真实PG覆盖历史source/篡改标题与operation/缺source/缺042执行权限/pending/rejected/dryrun/inactive拒绝且不新增业务/receipt，合法queue创建与重复调用保留一工单和完成回执。缺EXECUTE为InfrastructureUnavailable而非权限拒绝。s5-tool-intake-source-packages.log intake/bootstrap全包race PASS；s5-tool-intake-source-tools.log具名Tool/CreateTicketTool race PASS，非service全包。独立review_execution_scope_s1最终复核P1/P2关闭、无新增阻断。

范围仅工具创建路径。工具编辑原事务、审批/身份并发改变、binding撤销专项及成功/失败结果竞争尚待验证；不声称完整业务授权链已完成。目标PG17/T3交接及真实T4/G3未通过，CandidateSHA与未启动状态不变，无共享环境变更、企业外呼、push/main合并。

最终s5-tool-intake-source-verified-private.log完整私有PG16/Redis/MinIO候选ScopeRegistration、Intake、构造保全及Stream/Webhook/审计恢复race PASS，无skip/race；s5-tool-intake-source-build.log全后端build exit0，git diff --check通过。

### B2 S5 工具编辑原事务来源核验（2026-09-13）

s5-tool-edit-source-red.log真实PG复现不存在的999999调用ID仍可经TicketService.UpdateTicket修改工单、版本从1变2。现工具编辑在业务回执Replay之前调用requireToolEditAuthority，原事务复用当前审批/来源/042授权锁；要求ai_tool与tool:update_ticket操作命名空间一致、规范正整数ID、update_ticket类型与原actor匹配，并用已有toolEditCommand从批准参数重建完整命令。workitemmutation.Digest覆盖整个DTO（目标、ExpectedParentID、版本、Fields和Meta含OperationID），不使用创建命令那个排除IdempotencyKey的摘要。非工具入口维持原行为。

s5-tool-edit-source-green.log定向race RED→GREEN；真实负向覆盖无调用、额外title、改version/source/operation及pending拒绝且原工单整行不变。合法queue执行版本仅+1，直接业务回执重放保留整行，随后fixture owner改rejected，直接重放必须重新核验并拒绝。追加实际Ticket UPDATE后故障，工单整行回滚且没有提交审计回执，保留底层cause，重试只执行一次业务修改。目标/actor/parent独立变造专项未逐一运行，不扩大上述测试结论。

s5-tool-edit-source-full-private.log完整私有PG16/Redis/MinIO候选ScopeRegistration、Intake、构造保全及Stream/Webhook/审计恢复race PASS，无skip/race。s5-tool-edit-source-regression.log具名工具、CreateTicketTool及普通TicketService_UpdateTicket相关race PASS，无skip/race，非整个service包。独立review_execution_scope_s1限定增量复核无阻断。

剩余审批/身份并发变化、binding撤销专项及结果竞争需继续验证；创建/编辑原事务接入不等于完整并发授权链验收。目标PG17/T3和真实T4/G3仍未通过，CandidateSHA与未启动状态不变，无共享环境操作、企业外呼或push/main合并。

最终s5-tool-edit-source-build.log全后端build exit0，git diff --check通过；独立最终审阅确认UPDATE后故障与重试断言有效，不把它说成审计INSERT后的故障。

### B2 S5 运行绑定撤销并发验证（2026-09-13）

在既有scope关闭commit/rollback测试上，增加binding部署标识变化、mode改standard、删除绑定三种真实撤权，各自验证提交和回滚共六种序列。实际ToolInvocationMutation.Tx()中查询结果写回事务backend PID，断言该PID出现在独立撤权连接pg_blocking_pids；结果事务结束后撤权成功提交，下一调用拒绝且调用整行不变，已提交业务保持一条。此为候选模式绑定撤权，不是standard执行路径认证。

清理保留cancel→等已启动revoker→恢复fixture原值顺序，删除绑定以upsert恢复，覆盖撤销已完成和取消后尚未删除两种清理情况。独立review_execution_scope_s1确认锁证据、实际事务PID读取与清理无新增阻断。首次测试表初始化笔误导致编译失败，修正后s5-tool-binding-revocation-green.log定向race PASS；不将编译失败称为业务RED。最终s5-tool-binding-revocation-full-private.log完整私有PG16/Redis/MinIO候选边界及恢复race PASS，无skip/race，git diff --check通过。本轮仅增加测试与文档，未修改生产代码，故不重复此前已通过的全后端build。

该项关闭本机候选结果事务的binding撤销专项证据缺口；审批/身份并发改变及结果竞争仍待完成，目标PG17、T3交接和真实T4/G3未验收。CandidateSHA与候选未启动状态不变，无共享环境操作、企业外呼、push/main合并。

### B2 S5 工具首次写入身份撤销窗口RED（2026-09-13）

s5-tool-actor-revocation-red.log真实PG证明：已登记approved工具经intake原事务检查后、Ticket实际INSERT前，独立owner连接成功提交users.active=false，app.Create仍返回nil并新增一条工单及一条IntakeRequest。测试使用实际服务/数据库，Ticket hook只安排提交顺序，不替换业务执行；撤权有5秒上限和实际提交标记，结束恢复私有fixture身份。当前actor、approver、requester为同一fixture用户，不能把此用例说成三类不同身份分别验证。

042函数当前只锁binding、scope及结构登记，原工具行及用户/权限授权记录仍是无锁读取；RepeatableRead和在原事务内再查询不等于撤权同步。后续需要在可信权限边界保护原调用、actor/approver及相关当前RBAC，维持至业务提交/回滚，锁后核验授权并统一与审批更新、结果写回及现有binding→scope→登记锁序。不得通过任意扩大业务身份对用户/角色配置写权限来获得锁；保留SQL故障原因与已有事务冲突重试语义。

修复验证须区分撤权先提交拒绝、业务事务先锁定则撤权等待其提交/回滚，两种顺序都需真实数据库证据。新锁引入后不能沿用同步hook等待自身锁或把超时当撤权成功。审批参数变更、不同actor/approver/requester、RBAC关系撤回仍须分别验证。本轮仅增加真实RED与记录，无生产修复；当前具名及包含本项的完整套件不能报告通过，之前GREEN只代表之前验证范围。未重复不相关构建，git diff --check通过。CandidateSHA和候选未启动状态不变，全部后续交付门禁未放行，无共享环境操作、企业外呼、push/main合并。

独立review_execution_scope_s1确认RED有效：后续用户锁须去重并按ID排序，保护真实role（含super_admin分支），Role/RolePermission/Permission应按现有授权查询依赖锁定；如路径实际支持委派，还需保护会话/分配记录，不扩大现行不支持路径。数据库边界只负责锁定与来源一致性，权限匹配继续复用领域授权代码，避免第二套规则。


### B2 S5 工具业务事务授权锁（2026-09-13）

针对e12a7d297已提交的身份撤回RED，新增普通迁移043，保持042历史SQL不变。新五参lock_candidate_tool_authorization替换并删除旧入口，候选准入/调用/fixture同步切换。固定binding→scope→登记→原调用→按ID用户/Role/RolePermission/Permission锁序；原调用FOR UPDATE，其余FOR SHARE。actor/approver取自原调用，创建额外锁requester，审批额外锁当前决策者；不赋予IAM配置DML，权限解释继续由既有Go逻辑完成。Queue前检/结果与审批采用RR，创建/编辑沿用原RR，目录导入业务快照；序列化错误保留cause，创建既有完整事务重试，审批/队列不在旧快照继续执行。

真实审批竞争现在证明第二事务等待原调用锁，失败保留40001；整次重试仍按原决定/actor/原因核验并保留首次字段。创建撤权矩阵使用不同actor/approver/requester，覆盖三个身份停用、actor角色改变、角色停用、权限关系删除、权限内容修改、审批撤回、批准参数变化，共9×提交/实际INSERT后回滚=18个序列。每项精确匹配业务Tx backend PID与pg_blocking_pids；事务结束后独立撤权真正提交，再次创建/回执重放必须明确AuthenticationRequired或PermissionDenied，不能以基础设施错误充当拒绝。工单和IntakeRequest数量验证commit只新增各一条、rollback不新增；这不是所有既有行的整行保全断言。

s5-tool-authorization-migration.log迁移/database包PASS；s5-tool-authorization-private.log矩阵扩充前完整私有套件PASS；s5-tool-authorization-matrix.log18项定向race PASS。首次具名回归误用不存在的bootstrap目录导致setup失败，改为internal/bootstrap后s5-tool-authorization-regression-verified.log四包具名Tool/CreateTicket/TicketService_UpdateTicket/Intake race PASS，非四包全测试。补强错误类型后的s5-tool-authorization-final-private.log发现新增36个身份影响后续SLA角色收件人，预期1实际37；现各用例结束停用新身份并保留其业务引用，原SLA断言不变。最终完整回归与build结果在下方补记。

独立review_execution_scope_s1复核授权锁、快照与替换无新增阻断，要求补强错误类型已落实。此矩阵是创建路径证据，不外推为编辑、审批、结果写回各自完整矩阵。结果胜负竞争、其它S5边界、目标PG17/T3和真实T4/G3仍待完成；CandidateSHA保持d7470a32dbb87acc9b5e4d9a895a146410723561，候选未启动，无共享环境操作、企业外呼、push/main合并。

最终s5-tool-authorization-verified-private.log完整私有PG16/Redis/MinIO候选ScopeRegistration、Intake、构造保全及Stream/Webhook/审计恢复race PASS，无skip/race；包括补强错误类型的18项矩阵和原SLA断言。s5-tool-authorization-build.log全后端build exit0，git diff --check通过。


### B2 S5 工具成功与失败结果竞争（2026-09-13）

新增两种真实ProcessJob结果竞争顺序：done先持锁、failed先持锁。两次业务调用使用实际intake；失败通过Ticket INSERT后注入故障回滚，成功提交。测试wrapper只延迟实际业务返回，保留原始结果/错误，不伪造业务执行。两次业务事务有意先后完成，随后两结果写回事务真正并发；不将其描述为并发业务INSERT验收。

先行结果在真实ToolInvocation UPDATE前读取自身事务PID；后行结果必须在pg_blocking_pids中等待该PID，先行提交后后行必须保留40001，失败业务的cause也保留。整次ProcessJob重试后，failed可恢复done，已done不能被后到失败替换。解析完成Result校验唯一实际WorkItemID、编号、recordClass且Error清空；工单整行JSON在业务提交后与结果重试后保持一致（包括状态/版本），最终一工单一IntakeRequest，后续完成重放保留调用整行。

s5-tool-outcome-competition.log定向真实PG race PASS。独立review_execution_scope_s1确认竞争顺序及有界取消/等待清理无阻断，提出首次结果内容断言已补强；完整私有套件结果在下方补记。本轮只增测试，无生产修改，不重复上一检查点的全后端构建。创建/编辑/审批各自剩余矩阵、其它S5副作用入口、S6、鉴权及目标T3/T4/G3仍须逐项验证；不因该结果关闭整个工具复合项。CandidateSHA和未启动状态不变，无共享环境操作、企业外呼或push/main合并。

最终s5-tool-outcome-competition-full-private.log完整私有PG16/Redis/MinIO候选边界、构造保全及Stream/Webhook/审计恢复race PASS，无skip/race；包含完成Result与工单整行的补强断言。git diff --check通过。


### B2 S5 云发现直接执行入口禁用（2026-09-14）

s5-cloud-discovery-red.log真实私有PG证明CloudDiscoveryService.DiscoverAll与cloud.Runner.RunAll在候选fixture空账号扫描均返回nil，没有要求cloud_discovery能力许可；这证明入口缺少拒绝，不证明已调用真实provider。现两个构造器必需显式ExecutionPolicy，DiscoverAll、DiscoverAccount与RunAll在查询/账号解引用日志/provider调用之前检查冻结能力；candidate原配置只允许cloud_discovery=disabled，未新增scoped支持。缺策略不降级为standard，无旧构造fallback。

ExecutionPolicy复制能力开关，未知、缺失或禁用能力失败；检查上下文、取消、SystemBypass和租户一致性。部署能力不是业务授权，不能替代账号归属/RBAC或出站隔离。s5-cloud-capability-unit.log数据库/cloud包race通过；独立审阅指出无租户上下文也应拒绝，修正及最终验证结果在下方补记。直接单账号使用nil client证明拒绝先于I/O；standard显式enabled使用owner空账号扫描作为正向，仅证明入口可达，不证明普通运行角色、provider或reconcile可靠性。既有区域/持久化错误处理未在本轮重构。

本轮只处理cloud_discovery，连接器、embedding和导入导出直接入口仍需逐项核验；S5/S6及鉴权、目标T3/T4/G3未完成。CandidateSHA和候选停止状态不变，无共享环境操作、真实云调用、企业外呼或push/main合并。

审阅P2以s5-cloud-context-red.log实际复现：standard enabled与context.Background返回nil。改为必须有匹配租户上下文后，s5-cloud-capability-verified-unit.log数据库/cloud两包race PASS；独立复核确认P2关闭、无新增阻断。修正前完整私有suite也通过，但最终结果仍以下方修正后验证为准。

最终s5-cloud-capability-verified-private.log完整私有PG16/Redis/MinIO候选边界、构造保全及Stream/Webhook/审计恢复race PASS，无skip/race；s5-cloud-capability-build.log全后端build exit0。git diff --check通过。独立最终复核无新增阻断。


### B2 S5 连接器读取接口隐式外调RED（2026-09-14）

实际Gin ListMarket/ListConfigs/Health/Lifecycle四个GET调用Manager.HealthCheckAll；Health还使用context.Background丢失请求取消。新增仅连接httptest本机接收端的HealthProbe，预置当前及另一租户的两个运行实例，不附candidate结构归属。s5-connector-read-effects-red.log每个GET实际发出两请求；补独立租户计数时s5-connector-read-effects-verified-red.log曾编译失败，修正后s5-connector-read-effects-tenant-red.log八项断言真实RED：每路由本租户与另一租户均+1，共8次本机请求，无race报告。Manager.CloseAll、HTTP接收端及私有数据库均按fixture生命周期清理；未连接企业端点。

本测试在候选DB fixture内直接构造既有Controller/Manager，证明这些入口没有接收候选策略并会探测无归属与其他租户的运行实例；没有执行LoadAll，不声称已复现数据库历史实例恢复。当前生产尚未修复，包含新增用例的完整套件为RED；之前完整GREEN不能放行当前版本。没有生产变更，未重复全后端build。

独立只读调查确认修复应保留唯一Manager并区分实例激活许可与业务投递授权：读取接口只读实际健康快照，主动诊断需要有租户/取消上下文的owner入口；候选LoadAll在读取历史配置前拒绝。新scope投递目标由启动时可信声明绑定tenant/scope、精确实例、目标摘要和允许能力，不凭cfg.Enabled/请求自报scope/创建时间，Init副作用也要纳入现有manifest合同。不把connector_poll偷换成notification/webhook或诊断许可；既有worker仍核验持久意图、成员、租约、目标摘要/generation，Manager.Get/GetInstance及取得完整Connector后的直接调用必须一并检查。不能通过全关Manager破坏已要求的受控通知/Webhook旅程后声称G2完成。下一批先落实该边界及RED→GREEN。

CandidateSHA、候选停止状态与共享环境边界不变。S5/S6、鉴权与目标T3/T4/G3仍未完成，无共享数据库操作、真实企业外呼、push/main合并。

独立复核确认RED有效；显式Health移除强制200断言，为未来明确权限拒绝保留空间，其他读取仍要求成功且不得外调。最终s5-connector-read-effects-final-red.log仍为八项预期失败，无race；不把负测失败描述为通过。git diff --check通过。


### B2 S5 连接器读取与主动诊断分离（2026-09-14）

修复4a92cc3a8真实GET外调RED：删除Manager.HealthCheckAll，四GET及Provision响应统一读取本租户HealthSnapshot；新实例未知，不伪造健康成功。Manager新增唯一RefreshHealth主动诊断owner，构造必需CapabilityGate，生产注入冻结ExecutionPolicy；独立connector_diagnostics能力candidate只disabled、standard显式enabled，nil gate/租户或上下文不匹配拒绝。POST /connectors/health在真实路由注册connector:write权限，controller保留请求上下文，执行未获准为403而非成功健康报告。

Refresh仅抓取目标租户实例，单次5秒上下文，取消返回错误不记录当次成功；真实HealthStatus.OK=false与执行拒绝分开。JSON保存/返回快照提供嵌套Extra副本，generation匹配才写入缓存；替换/撤销清除旧实例快照。缓存只存已完成观测，非持久健康账本。测试覆盖本/外租户计数、nil/candidate拒绝、标准显式允许、深副本、替换进行中实例丢弃旧结果及取消不伪造成功。所有既有测试构造显式nil仅表示无主动诊断许可，不影响尚未在本轮改造的投递入口；没有隐式standard fallback。

s5-connector-health-unit.log具名connector/config/controller race PASS；s5-connector-health-full-private.log完整私有PG16/Redis/MinIO候选边界、构造保全及Stream/Webhook/审计恢复race PASS，包括四GET零本/外租户探测及candidate POST403。独立审阅无新增阻断。真实POST的完整认证/RBAC中间件尚未单独端到端验证，不把静态路由权限注册和owner测试当作完整认证验收。后续回归/构建结果在下方补记。

本轮只关闭读取外调与主动诊断边界。Provision/LoadAll的实例激活来源、Send/Get/GetInstance裸Connector旁路及受控scope目标声明仍需继续实现；既有通知/Webhook私有测试通过不等于生产可信目标启动准入。CandidateSHA与停止状态不变，S5/S6/鉴权及目标T3/T4/G3未完成，无共享操作、企业外呼、push/main合并。

最终s5-connector-health-regression.log具名bootstrap/service Webhook/Notification/Connector/Graph/Callback race PASS（非两包全测试）；s5-connector-health-build.log全后端build exit0；完整私有suite无skip/race，git diff --check通过。新POST沿真实auth组已有CSRF中间件，健康路径不在其跳过清单；这是代码核查，非浏览器认证验收。


### B2 S5 历史连接器恢复入口准入（2026-09-14）

s5-connector-restore-red.log真实PG复现直接LoadAll在候选策略下返回nil，读取持久配置并Init一次、Manager出现实例；配置行未变化。现CapabilityGate扩充RequireStartupCapability，ExecutionPolicy只允许standard+冻结显式enabled+未取消内部SystemBypass上下文；Manager.RequireRestore固定检查connector_poll，LoadAll在恢复客户端检查/查询/解析/factory之前调用。nil gate不降级，普通tenant context不能进行跨租户恢复，candidate即使标记SystemContext仍拒绝；未放宽请求级RequireCapability。

新真实PG测试candidate同条持久配置零Init/无实例且配置整行不变；standard enabled普通tenant context拒绝，之后以既有clients.System与SystemContext实际Init一次并保全配置。nil恢复客户端负测证明gate先于I/O。SystemBypass仅内部标记，不能当成数据库角色认证；既有受限system角色与启动角色准入保留。原integration_postgres恢复fixture改为显式standard connector_poll enabled，其执行状态单独列明，不混同candidate_scope套件。

s5-connector-restore-unit.log四包具名Capability/Health/Connector/Runtime/API race PASS；独立review_execution_scope_s1复核无新增阻断。实际poll/provider、受控scope新目标激活、直接Provision/Send/Get路径仍未完成，本轮仅历史恢复准入。CandidateSHA、候选停止及共享环境边界不变，S5/S6、鉴权及目标T3/T4/G3继续待验收。未执行共享数据库操作、真实企业/云调用或push/main合并。

最终s5-connector-restore-full-private.log完整私有PG16/Redis/MinIO候选边界、构造保全及Stream恢复race PASS，日志无FAIL/SKIP/DATA RACE；s5-connector-restore-legacy-compile.log以integration_postgres标签和空匹配编译通过（no tests to run），未执行该标签的数据库测试；s5-connector-restore-build.log全后端build exit0。git diff --check通过。这些证据不替代Agent B目标PG17环境验收。

### B2 S5 请求激活未声明连接器目标RED（2026-09-14）

新增真实Manager与Gin Provision handler负测，使用生产builtin Webhook、仅loopback接收端以及受限runtime客户端。未声明目标的直接Provision返回nil、HTTP返回200，两者均发布实例；测试随后显式Manager.Send各产生一次本机请求，不能表述为Provision本身发送消息。HTTP路径还改写本子测试预置既存配置的provider、enabled、settings及时间等字段，原行JSON比较失败；该行是私有fixture新建，不是迁移前遗留行。新增租户配置行数保全断言避免未来拒绝时误插新行。

s5-connector-request-activation-red.log与补充行数断言后的s5-connector-request-activation-final-red.log均为七项预期断言失败，无SKIP/DATA RACE。独立审阅确认RED有效且正确拒绝不会因发送探针被误判（无实例时不发送）。本轮只有测试和计划细化，未修复生产入口，不重复生产构建，也不将此前绿色套件当作当前版本放行依据。尚未测试Marketplace或生产认证中间件。

独立只读调查还发现Marketplace先提交UpdateInstallationConfig后Provision；下一修复必须在首次持久化前拒绝，并在既有ExecutionConfig增加冻结的可信精确目标声明、复用唯一Manager和manifest初始化行为约束。既有通知/Webhook worker仍拥有业务投递权限，不因声明目标存在而开放Test/诊断/polling；详见设计worktree现有S5实施范围。CandidateSHA和停止状态保持不变，完整目标未完成，无共享数据库操作、真实企业外呼、push或main合并。

### B2 S5 可信连接器目标声明与冻结前置（2026-09-14）

ExecutionConfig新增connector_targets，校验精确tenant/scope、不可歧义name/provider、重复实例键、规范目的地摘要与非空无重复的既有notification/webhook/outbox能力，能力须显式scoped。standard不能携带候选目标。s5-connector-target-config-red.log通过真实Viper配置解码复现旧配置忽略无效声明；校验实现后正向与各非法分支通过。outbox仅为既有投递owner的基础能力，不授权所有handler或直接Send。

ExecutionPolicy在构造时序列化拥有全部嵌套声明；ConnectorStartupTargets仅candidate内部SystemContext返回独立副本，拒绝nil/standard/普通tenant context和取消。s5-connector-target-policy-red.log记录缺失读取合同；实现后输入配置变更、返回值变更均不能修改原目标、嵌套settings、凭据或能力。内部标记仍不是角色或业务授权。s5-connector-target-number-red.log复现大整数settings可被JSON复制舍入；现拒绝校验时可见的有损往返，不宣称能够恢复加载器此前丢失的原文精度。

s5-connector-target-final-unit.log具名config/database race PASS，包含notification/outbox正向、缺失能力、错误消息不含合成秘密。独立最终审阅无新增阻断。此为冻结声明前置，尚无Manager启动调用；507f62293请求激活RED保留，初始化manifest合同、真实目标核验、首次配置写入前拒绝和业务投递旁路仍需完成。完整目标及原CandidateSHA/停止边界不变；未操作共享环境或企业端点。完整回归与构建结果在下方补记。

实际配置链补查发现既有resolveMapEnvVars不进入列表内对象，s5-connector-target-env-red.log真实Viper YAML路径复现凭证环境引用保持原文；改为同一解析器递归map/列表，嵌套列表也覆盖。s5-connector-target-env-verified.log具名config/database race PASS，s5-connector-target-full-config.log完整config包race PASS；独立复核无阻断。缺失环境变量仍沿原默认值/空字符串语义，本轮没有新增凭证完整性校验或真实凭证解析验收。

s5-connector-target-full-private.log在环境解析增量之前执行完整私有PG16/Redis/MinIO候选边界与Stream回归，只有507f62293具名请求激活的七项已知断言失败，其余所选子测试通过，无SKIP/DATA RACE；整套结果明确为FAIL，非验收通过。后续环境解析改动通过上方真实配置链测试，未重复不读取配置加载器的私有fixture套件。s5-connector-target-build.log为环境解析增量前全后端构建通过，最终构建另记。

最终s5-connector-target-final-build.log全后端build exit0，git diff --check通过。当前只完成声明配置/冻结前置，不能据此关闭507f62293或S5/G2。

### B2 S5 可信目标启动与生命周期接入（2026-09-14）

s5-connector-startup-red.log实际API生命周期复现声明未激活就进入背景任务，以及未知registry/无效目的地/摘要不符/未声明初始化行为不阻止启动，共五场景RED。现消费者前调用唯一Manager.ActivateStartupTargets；冻结声明整批预检registry manifest local_only，再复用普通Provision内部初始化，Init后核对通用DeliveryDestinationIdentity，全部成功才发布generation。原Webhook专用身份接口全量替换，既有URL摘要含义不变；初始化行为加入manifest checksum。当前只有检查过的builtin Webhook声明local_only，无Init网络请求；未知行为拒绝，不以声明字符串代替真实实现检查。

失败批次关闭当前失败对象和已准备对象，Join保留原错与清理错；已尝试Manager不得再次启动。独立审阅P2指出CloseAll可能早于阻塞Init清理返回，s5-connector-startup-close-red.log真实复现；现初始化在锁内登记WaitGroup，关闭先标closed再解锁等待清理，最终发布再检查closed/context。普通Provision同样受关闭等待及可信启动后不可变更约束。关闭竞争测试用同步通道与100ms观察窗口；Close不主动取消Init，调用方须取消生命周期且Init须遵守上下文。

s5-connector-startup-consumer-close-red.log补测复现工具队列Start失败未Close；现先取消/Close失败队列与事件运行时，再清理连接器依赖。s5-connector-startup-final-unit.log具名bootstrap/connector/service race PASS，覆盖当前失败对象/部分批次、目的地身份缺失、错误保留、关闭竞争、失败后禁止重试和后续消费者启动失败。不是全部消费者失败的穷举。独立最终审阅无新增阻断。

s5-connector-startup-full-private.log将候选Webhook stream ack恢复旅程从手动Provision改为真实Manager冻结声明激活：原持久消息恢复、意图保全、真实outbox worker两接收端各一次发送、重复调度零新增均PASS；真实接收端仅loopback。整体私有PG16/Redis/MinIO suite仍只有507f62293请求激活七项已知失败，无SKIP/DATA RACE，整套为FAIL。之后仅补工具队列失败Close和接口嵌入，最终具名回归通过，不把此前完整suite描述为当前整套绿色。

本轮完成可信目标启动路径，不关闭启动前DirectProvision、Marketplace首次持久化、Send/Get裸实例旁路，也未证明完整通知/所有provider或候选整进程重启。CandidateSHA与停止状态不变，S5/S6及T3/T4/G3未完成，无共享环境变更、真实企业外呼或push/main合并。最终构建结果下方补记。

最终s5-connector-startup-build.log全后端build exit0，git diff --check通过。尚未关闭的七项RED继续阻止当前版本放行。


### B2 S5 Marketplace 配置写入与 OAuth 前置检查（2026-09-14）

新增唯一 ExecutionPolicy.RequireIntegrationManagement：仅 standard、正租户、显式匹配租户上下文、无 SystemBypass、未取消请求可继续；candidate/nil gate 拒绝。Marketplace Install（包括历史重新启用）、Uninstall、UpdateConfig 和 MergeConnectorInstallationConfig 四个公开写 owner 均在首次查询/持久化前检查，不按商品类型放开 skill/plugin。该限制不替代既有 RBAC。生产 bootstrap 必需传入冻结策略；HTTP handler 改用 Request.Context 保留类型化租户和取消，ErrDenied 固定403；连接器 runtime 缺失不再静默激活成功，standard 已提交配置不因此自动回滚。

s5-marketplace-management-red.log 真实私有 PG 复现 connector/skill/plugin × install/reactivate/update/uninstall 共12操作写入与配置整行/计数改变。s5-marketplace-http-denial-verified-red.log 校正真实 middleware tenant context fixture 后复现三 handler 未返回403；此前未提供 middleware tenant context 的日志不是有效产品 RED。s5-marketplace-http-context-red.log 复现 standard 合法 HTTP 因传递 GinContext 丢失 tenantctx 返回403；s5-marketplace-runtime-missing-red.log 复现 nilManager 返回nil。以上均已修复。不得把 handler fixture 等同完整认证/RBAC E2E。

独立审阅发现 Merge 公共写入口遗漏；s5-marketplace-merge-red.log 真实 PG 复现其写入未准入 oauth 配置并修改时间戳，现同 gate 首行拒绝。完整 s5-marketplace-full-private.log 新13配置操作（含 connector merge）候选零变更、standard 对应真实写入和 standard HTTP 安装/更新/卸载均 PASS。标准策略正向使用同一私有受限 runtime client，不替代 standard 整进程角色准入。完整私有 PG16/Redis/MinIO suite 仍只有507f62293普通 Manager/Controller 请求激活七项原有失败，无SKIP/DATA RACE；整体明确为FAIL，未关闭该门禁。

飞书 OAuth callback 原先先兑换再 Merge，nil Marketplace 还跳过保存；现只从已解析实例派生 tenantctx，并在兑换前调用同一 gate，Merge 内再次检查覆盖直接调用。s5-marketplace-oauth-red.log 候选/缺策略/缺服务三个场景各触发一次真实 loopback 请求，现零请求并403。审阅指出重复 callback ID 不保证唯一；s5-marketplace-oauth-ambiguous-red.log 复现任意首项命中，Manager 现多匹配 fail closed。s5-marketplace-oauth-final.log 飞书测试 PASS，含 standard 本机 provider + SQLite 实际两次兑换请求、租户17保存、保留旧字段、query/state 中租户18不影响目标、响应不含令牌。此证据不代表 OAuth state/发起人授权/防重放已实现，也不保证兑换与持久化原子性。

最后审阅指出所有 preflight 错误映射403不准确；s5-marketplace-oauth-canceled-red.log 真实取消请求复现后修为仅 ErrDenied 返回403，其余固定失败响应不输出 cause。最终具名回归、构建与审阅结果下方补记。本轮未修复启动前 Manager Provision、Revoke/删除和 Send/Get 裸实例边界，未更新固定 CandidateSHA，候选未启动，未进行共享环境、企业/云外呼、push或main合并。S5/S6和T3/T4/G3继续未完成。

最终 s5-marketplace-final-review-unit.log 五包所选具名 race PASS（未匹配的包不视为全包测试），包括取消请求分类与 standard OAuth 正向；独立最终复核无新增阻断。s5-marketplace-build.log 全后端 build exit0，git diff --check通过。最后增量仅回调错误分类，已运行对应回归；上方完整私有 suite 不宣称在该增量后重跑，既有七项激活RED继续保留。

### B2 S5 连接器 HTTP 配置入口与名称级撤销（2026-09-14）

Manager 新增 RequireIntegrationManagement 委托既有冻结策略；HTTP Provision/Revoke 在请求解析、实例变更、持久化和邮件轮询操作之前检查，nilManager/nilgate/candidate/缺失或不匹配tenantctx/SystemBypass拒绝，取消等非ErrDenied固定500。既有配置HTTP单测显式使用standard策略与局部请求tenantctx，不修改其他领域共用认证fixture，也不把此夹具当生产RBAC验证。Manager.Provision/Revoke本身尚未调用该gate。

s5-connector-delete-red.log 真实私有PG复现候选DELETE返回200且原配置行消失；修复后原行JSON保全。s5-connector-http-gate-private.log 中原507f62293 HTTP激活分支已PASS，直接Manager仍三项RED。标准正向初版空provider不符合Ent NotEmpty，在s5-connector-http-final-private.log暴露旧HTTP持久化错误被忽略，不能作为合法正向证据；改用合法local-test后，s5-connector-revoke-provider-red.log 进一步复现删除配置但运行实例残留。Revoke现按当前租户+name筛选所有实例，携带完整cfg（含provider）撤销，与原数据库名称级删除范围一致。

s5-connector-http-verified-private.log完整私有PG16/Redis/MinIO回归中，HTTP激活负例、候选删除整行保全、standard真实HTTP更新/实际DB保存/实例激活/删除及实例移除PASS，初始化本机Webhook零请求。整套仅剩直接Manager Provision三项原有断言失败，无SKIP/DATA RACE，整体仍为FAIL。没有通过缩减测试或改换candidate策略消除其RED。标准PG正向仍使用同一私有受限runtime client，不代替目标standard角色准入。

s5-connector-http-final-unit.log具名controller/connector/database race PASS。新增多provider名称级撤销测试验证同tenant两个provider分别Close一次并移除、外tenant同名实例保留且零Close；此单测不使用DB，持久化由上方PG正向单独验证。快照枚举与并发Provision竞争、Close错误和实例/DB原子性仍待后续生命周期修复，不将此循环表述为原子删除。

下一步继续Manager.Provision（含Enabled=false）、contextful Revoke的owner门禁及Send/Get直接投递边界。审阅确认LoadAll的WithTenantID已清除SystemBypass，可在独立启动准入后保留普通租户管理检查，不能放宽gate。candidate通知/飞书/Webhook初始目标fixture改可信声明启动；目标变化/发送中重绑防御测试保留为显式standard可变实例场景，不能只改成拒绝重绑便宣称原generation防线通过。新目标重放测试用新启动配置/Manager验证既存receipt不扩展目标；此前GET读取测试中刻意预置的非候选实例须明确负测前置。保留已完成ACK可信启动旅程，不重新执行或改写其业务协议。

CandidateSHA、候选停止及共享环境边界不变，完整目标与S5/S6/T3/T4/G3未完成。无共享数据库变更、真实企业/云调用、push或main合并。最终构建及审阅下方补记。

最终 s5-connector-http-build.log 全后端build exit0，git diff --check通过，独立最终审阅无新增阻断。当前完整私有suite仍为上述三项直接激活RED，未放行候选。

### B2 S5 Manager 配置 owner 准入与可信投递夹具迁移（2026-09-14）

Manager.Provision第一行调用同一RequireIntegrationManagement，Enabled=false不再通过早返回绕过准入；Revoke改为带context并返回error，在自身owner检查策略、租户、取消及生命周期状态。普通入口只允许standard显式匹配租户；candidate必须走既有ActivateStartupTargets，不因现存配置/HTTP已检查而放行。LoadAll保留先connector_poll启动准入、再WithTenantID清除SystemBypass的链路，真实私有standard恢复正向保留。

s5-manager-revoke-red.log通过真实声明启动builtin Webhook，复现旧直接Revoke移除已声明实例；修复后拒绝且对象/generation不变。关闭错误返回而不移除实例，HTTP停止后续DB删除；Enabled=false同样传播关闭错误。多实例撤销仍可能部分完成，未解决实例/数据库跨步骤事务及快照并发。CloseAll仍负责停机清理，不使用请求gate。

candidate正向夹具逐项迁移：本地通知和Feishu探针声明local_only及稳定通用目的地摘要，保留原TaskDestinationIdentity、通知/专业投递协议；Webhook初始目标走冻结声明，third目标用新启动配置和新Manager验证旧receipt目标集不扩展；ACK可信启动旅程未改回普通Provision。destination_changed/during_send_rebind保留显式standard可变实例以验证原摘要/generation防御；读取既存实例同样明确standard负测前置，不能当candidate激活证据。其余普通connector/controller/service/Feishu夹具显式standard策略与tenantctx，未把原candidate请求RED换成standard。

s5-manager-gate-full-private.log完整私有PG16/Redis/MinIO候选注册、intake边界、构造保全及Stream恢复race PASS，无FAIL/SKIP/DATA RACE；507f62293原直接激活三项RED现关闭，候选请求零实例/零外呼/配置保全。此证据在下述取消补丁之前，后续针对变化补验，不能声称目标WSL PG17/完整应用G2通过。

独立审阅P2指出Init期间取消仍可能发布实例，s5-manager-cancel-red.log用阻塞Init、cancel后释放且Init返回nil真实复现旧Provision成功。现发布锁内重新ctx.Err，关闭新对象，errors.Join保留取消及关闭失败cause；实例不发布。s5-manager-final-unit.log四包具名race PASS；s5-manager-gate-matrix.log Manager矩阵PASS，覆盖nilManager/nilgate/缺tenant/外tenant/SystemBypass/canceled/candidate，分别激活/disabled/直接Revoke均在Init之前拒绝；关闭失败保留对象/generation。s5-manager-standard-fixtures.log补齐飞书手动/自动/并发/读取、BPMN CC及消费者启动失败清理具名race PASS。未将未匹配测试当全包验证。

独立最终复核无新增阻断。声明的targetAuthority scope/能力尚未由全部投递owner消费，Send/Get/GetInstance裸实例权限仍需封闭；目标存在不等于投递授权。当前完整目标、S5/S6及T3/T4/G3仍未完成，CandidateSHA保持不变，候选未启动；无共享环境操作、真实企业/云外呼、push/main合并。兼容编译及最终构建下方补记。

最终 s5-manager-legacy-compile.log 的integration_postgres标签编译通过（no tests to run，未执行该标签数据库测试）；s5-manager-build.log 全后端build exit0，git diff --check通过。无进行中的Go进程，后续可安全继续编辑。

### B2 S5 Webhook 声明归属与投递能力消费 RED（2026-09-14）

实际owner调查确认producer snapshotWebhookTarget与worker h.target仅比较GetInstance/目的地摘要，不消费Manager私有targetAuthority的scope/capabilities，也不验证Manager deployment与source Ref一致。新增四种声明不匹配：另一scope、另一deployment、notification-only、outbox-only；均保持真实来源、同tenant、相同精确provider和同一loopback URL。

s5-webhook-target-authority-red.log真实PG producer分别返回nil、创建未准入intent与消费成功receipt。断言在清理前执行；清理只覆盖本次fresh WorkItem的Webhook意图及对应操作receipt，原source与历史记录不删除。s5-webhook-worker-authority-red.log独立由合法Manager先创建合法意图，随后只替换worker运行Manager；实际OutboxWorker DispatchOnce在四场景各产生一次本机HTTP、published状态与交付receipt，证明worker自身也缺检查，不能仅修producer。

s5-webhook-target-full-red.log完整私有PG16/Redis/MinIO race仅新增8个场景失败，其余所选回归通过，无SKIP/DATA RACE，整体为FAIL；上一轮15374aa47绿色证据不能覆盖新增要求。最终s5-webhook-authority-final-red.log补验producer原source整行不变及worker重复轮询：source保全通过，终态不再次发送，但原不当交付receipt仍存在，新增断言保持RED。替代scope仅作为Manager冻结声明，没有建立第二个真实active数据库scope，不宣称完成跨有效scope生命周期测试。

独立只读审阅确认两侧RED有效且清理范围正确。后续沿唯一Manager/ExecutionPolicy建立精确目标解析：真实owner已验证Ref、代码固定capability、tenant/name/provider与冻结mode/deployment/scope完全一致；candidate私有目标scope/能力/摘要匹配，standard空scope、实际deployment一致且显式启用投递能力。producer提交新意图前检查，worker已有意图发送前及回执复核独立检查，保留同对象/generation及原DB source/member/claim/lease/receipt合同。原standard Manager与candidate worker混合的重绑防御fixture必须改完整standard链，不放宽一致性迁就旧测试。完整合同已补原S5计划。

本轮仅新增真实RED与实施合同，未修复生产路径；未进行不相关构建或将编译成功当验收。Send/Get/GetInstance裸入口及通知/Feishu目标消费仍未完成，完整目标、CandidateSHA、候选停止及共享环境边界不变，无企业/云外呼、共享数据库修改、push/main合并。


### B2 S5 Webhook 精确声明消费与发送前错误分类（2026-09-14）

producer 与 Worker 现统一调用 Manager.ResolveDeliveryTarget，以冻结 ExecutionPolicy 比较来源 Ref 的 deployment/tenant/scope 与显式投递能力。candidate 同时检查实例私有声明的 scope、能力、目的地摘要，standard 要求同 deployment、空 scope、显式启用 webhook；精确 tenant/name/provider 比较保留。Worker 捕获同一实例发送并后置检查 generation，原 source/member/claim/lease/receipt 事务合同不变。新意图提交前核验，旧消费回执重放不扩大目标集合。

s5-webhook-target-gate-private.log 原8项 RED 全部转绿。严格身份检查暴露旧 standard Manager/candidate source 混合夹具；destination_changed、during_send_rebind 与 standard ACK 恢复现使用完整 standard 链和真实 SLA source。首次 owner 连接建单被数据库运行身份触发器拒绝（s5-webhook-standard-chain-verified.log），未削弱触发器；改为本任务临时数据库内独立 standard 绑定角色，s5-webhook-standard-bound-role.log PASS。该角色有宽表权限及 BYPASSRLS，仅为组件夹具，不代表 standard 应用角色准入；WorkItem 是直接 seed，不能称创建 E2E。临时角色随本次随机数据库关闭清理，不触及 WSL/共享库。

独立审阅 P2 指出发送前所有 resolver 错误错误地转永久 blocked。s5-webhook-resolver-cause-red.log 在实际 PG claim、attempt marker 与 Deliver 路径分别复现临时、取消、deadline cause 丢失，三者零 HTTP/零交付 receipt。修复为明确目的地不匹配包装 ErrDenied，发送前复用 webhookPreflightError 保留其它 cause；发送后不确定结果仍 delivery_unknown。测试中的最终 MarkBlocked 是直接 claim 清理，不作为实际 Worker 重试结果证据。

s5-webhook-target-full-green.log 完整私有 PG16/Redis/MinIO 所选注册、intake、构造保全、Stream race suite PASS，无 FAIL/SKIP/DATA RACE，包含新增8项授权拒绝及3项cause回归；不能替代 WSL PG17/G2。s5-webhook-target-unit.log database/connector/service 具名 race PASS，覆盖精确冻结身份、上下文/能力拒绝、standard 正向，以及全局 notification 已启用但精确目标仅允许 webhook 时仍拒绝的矩阵；未宣称全包运行。独立复核 P2 已关闭且无新增阻断，最终构建和增量审阅下方补记。

裸 Get/Send/GetInstance、通知/飞书目标消费与剩余请求异步入口仍待接入。完整目标、S5/S6/T3/T4/G3 未完成；固定 CandidateSHA 不变，候选未启动，无共享环境操作、企业/云外呼、push 或 main 合并。

最终 s5-webhook-target-build.log 全后端 build exit0；git diff --check通过。独立最终增量复核无新增阻断，未运行额外外部测试。新增单测在完整私有suite之后，仅修改测试与文档，无生产增量。


### B2 S5 通知目标权限 RED 与持久协议依赖（2026-09-14）

实际通知worker dispatchClaimedDelivery仍通过Manager.Send→Get按tenant/channel取首项，未消费冻结targetAuthority。s5-notification-target-authority-red.log 四例在合法WorkItem和真实queue claim下分别换成错scope、错deployment、webhook-only、outbox-only声明；同一类型本地进程内connector探针实际各收到一次Send，ProcessPendingDeliveries报告1项完成且队列sent/SentAt。没有企业或HTTP外呼。来源scope未变，替代scope仅是Manager声明，不是第二个有效DB scope。

最终s5-notification-target-full-red.log完整私有PG16/Redis/MinIO race只有新增4例失败，其余所选用例通过，无SKIP/DATA RACE。重复poll比较原接收列表，未增加第二次Send；原历史通知整行JSON保全。该终态不重复不能抵消首次不当发送。当前整套状态为FAIL，上一检查点3e4e0e257绿色证据不能覆盖这4项新增要求；本轮尚未修复生产路径。

独立owner调查确认TicketNotification没有精确provider/目的地持久字段，飞书bootstrap仅注入func(tenant)取实例。不能用当前目标合格检查替代意图身份冻结。原S5新增后续合同：原通知行结构化不可变目标与协议版本、无历史回填的注册迁移、原事务唯一目标选择与幂等重放、Worker同对象/generation前后核验；飞书保留原专业Destination/GUID及有序outbox而补精确实例身份。不能新增同渠道多目标路由、临时选择首项或另建队列。现4例为worker直接queue夹具，真实producer、迁移、重放与重启须另外验证。

CandidateSHA、候选停止与共享环境边界不变；完整S5/S6/T3/T4/G3未完成，无WSL操作、共享数据库修改、企业外呼、push或main合并。本轮仅测试与计划合同，未运行无关生产构建。最终独立复核下方补记。

独立最终复核确认失败仅新增四项且RED有效、实施合同无阻断。GREEN阶段必须改为合法producer生成完整目标意图后只替换Manager，并断言安全错误分类，排除缺协议字段或数据库故障造成假绿；重复poll不代表恢复成功。email/push仍需独立准入。git diff --check通过。


### B2 S5 通知目标协议044结构准备（2026-09-14）

新增注册 `044_notification_connector_target`，沿受控顺序依赖043及P准备，保持R依赖和历史SQL/checksum不变。四个可空字段保存目标协议版本、connector name/provider及目的地摘要；全NULL保留历史，版本1全字段非NULL、渠道匹配、规范摘要。UPDATE触发器阻止NULL补绑定、目标改指/清空，以及绑定后业务身份和内容修改；状态/attempt/lease仍可由原worker更新。函数权限从PUBLIC及继承角色默认ACL撤销。Ent按既有go generate流程生成，字段不可变且JSON隐藏；没有Ent overlay或共享数据库操作。

s5-notification-target-migration-red.log先复现未注册SQL；s5-notification-target-migration-private.log初版真实PG通过。审阅指出邮件历史行会因渠道CHECK而假绿，现另建合法sms旧NULL夹具，绑定明确要求23514且不可变trigger消息；非法INSERT/UPDATE均要求23514，清理断言成功。迁移前显式授予runtime默认EXECUTE，后验证已剥离。s5-notification-target-migration-final-private.log强化后的真实私有PG race PASS：从缺列表执行真实DDL、旧字段逐行聚合JSON一致、新列全NULL、禁止改绑、合法新目标行可更新状态。该测试使用任务私有owner验证DDL，不代替受限应用角色准入。

s5-notification-target-ent-generate.log生成exit0，最终差异仅通知相关与必要共享生成代码；s5-notification-target-migration-unit.log完整migration包PASS，新增依赖缺失拒绝测试，原后继数量断言随新增044更新，未削弱依赖。独立最终增量复核无新增阻断。

本次仅结构准备，producer/worker未接入，原四项通知目标权限RED不得因缺目标字段而假绿；后续必须合法producer创建完整意图再换Manager。完整目标/S5/S6/T3/T4/G3未完成，固定CandidateSHA不变、候选未启动。B的真实迁移语义清单后续必须纳入044，未授权/执行WSL迁移、企业外呼、push/main合并。完整私有回归及构建结果下方补记。

完整s5-notification-target-structure-full-private.log仅原四项通知权限RED及其父用例失败，其余所选PASS，无SKIP/DATA RACE；整套仍FAIL。下一producer正向应使用真实偏好支持的sms渠道及本地sms探针，不为复用webhook队列fixture新增产品渠道；现有SendNotification中的直接外发路径也必须随原事务入口盘点，不因EnqueueNotificationTx通过而遗漏。

最终s5-notification-target-structure-build.log全后端build exit0，git diff --check通过。无进行中的Go进程；本轮结构准备具备上述独立审阅与验证证据，投递权限修复仍待执行。


### B2 S5 通知原事务目标绑定与Worker精确解析（2026-09-14）

EnqueueNotificationTx/EnqueueCreationTx通过同一notification target helper，在新意图的原事务内要求显式notification能力、真实Ref、同渠道唯一启用provider，并经Manager.ResolveDeliveryTarget冻结044四字段。重放检查原协议完整性与业务内容，复用原渠道/目标，不重新读取目标；未知渠道仅in_app/email/push之外一律不能当作合法无目标行。Worker按行中精确目标解析并发送捕获对象，返回后检查摘要/generation；原scope/member/lease/回执链保留。

s5-notification-producer-frozen-red.log在正确注入偏好服务后实际生产sms队列，复现目标版本NULL；初次fixture遗漏偏好服务导致多渠道OnlyX panic不作协议RED。生产者必须显式notification=scoped，原fixture未声明能力被正确拒绝（s5-notification-producer-green.log），现仅对应通知夹具声明能力，未放宽全局策略。s5-notification-producer-worker-private.log原4项拒绝和合法本地接收PASS：先真实SMS producer生成全部字段、再只替换错误Manager，不以缺字段假绿；另有原事务rollback零队列。SMS为既有偏好渠道，本地探针无网络/企业外呼。

独立审阅P2发现未知渠道与发送前cause问题。s5-notification-unknown-red.log、s5-notification-resolver-red.log分别真实复现，现仅明确非连接器渠道允许无目标；dispatch返回安全分类与cause，ProcessPendingDeliveries以errors.Join保留。s5-notification-writeback-red.log进一步实际注入pending/failed/sent写回错误，复现内部包装丢失cause，现三处%w保留；creation目标拒绝映射typed PermissionDenied，其余InfrastructureUnavailable。发送后不确定状态仍沿用原delivery_unknown，并未统一所有Ticket/User/email/provider错误路径。

s5-notification-target-protocol-full-private.log完整私有PG16/Redis/MinIO race PASS，无FAIL/SKIP/DATA RACE（在最后写回包装补丁之前）；s5-notification-protocol-final-private.log最后增量具名PG race PASS，覆盖3个resolver cause、3个实际worker写回故障、无Manager重放原行保全、内容冲突、双provider拒绝零新意图、未知旧行replay拒绝与四项错声明。写回失败保持processing且SentAt为空；测试随后标failed是私有清理，不代表恢复完成。独立复核P2关闭。

扩大相关单测后s5-notification-protocol-unit.log仍有7项FAIL：WorkerKeepsUnavailableConnectorRetryable、WorkerFencesUnknownSendWithStableDeliveryKey、BPMNCCFanoutUsesDistinctStableConnectorDeliveryKeys、WorkerFencesExpiredLeaseAfterSentStatusWriteFailure、WorkerCASPreventsCompetingLiveLeaseDispatch、WorkerRunsImmediateSweepAndStopsOnCancellation、ReadStateDoesNotChangeDurableDeliveryState（共同TestTicketNotification前缀按日志全名）。实际所有写入者复查发现TicketWorkflowService.createCCNotifications与bpmn.CCTaskHandler.createCCNotifications仍直接写无目标外发队列，相关合法正向不能用手填四字段修饰。下一步必须将唯一binder注入这两个原owner及bootstrap，保持原CC事务/幂等协议，真实标准配置显式开启notification并由owner生产目标；同步SendNotification直接外发也仍未迁移。generic_handler站内路径单独核查，不因此扩展新产品渠道。

当前不是通知完整交付通过：上述7项回归、剩余producer、目标变化/重启完整矩阵、飞书及裸Get/Send仍待处理。S5/S6/T3/T4/G3与总目标未完成，CandidateSHA不变、候选未启动，无共享环境/WSL变更、企业外呼、push/main合并。最终构建结果下方补记。

最终s5-notification-protocol-build.log全后端build exit0，git diff --check通过。相关单测仍为上述7项回归，构建成功不替代业务验收；后续继续沿原入口修复。


### B2 S5 工单及BPMN抄送目标owner接入（2026-09-14）

唯一目标binder导出为BindNotificationConnectorTarget（无旧别名）。TicketWorkflowService注入同一TicketNotificationService，withClient保留依赖，原createCCNotifications事务builder写目标；BPMN CCTaskHandler通过窄NotificationTargetBinder端口调用同一owner，避免service/bpmn循环依赖或复制选择逻辑。bootstrap将已配置的通知服务注入API workflow实例和实际callback registry的cc_handler。连接器渠道缺binder拒绝，原email/in_app/push仍沿各自传输，不把此项当它们已准入。原渠道解析、CC关系/幂等/事务语义不变。

s5-notification-cc-owner-unit.log原7项相关回归全部关闭。标准夹具显式启用notification、保持相同deployment/provider/摘要；通过真实CC生产者先冻结目标，再模拟Worker Manager不可用/恢复，没有手填044字段。原稳定投递key、重试、未知结果、租约竞争/恢复及读取状态断言均保留。初始目标和Worker恢复实例是同一受控目的地身份，不能推广为任意目的地重绑定许可。

s5-notification-cc-owner-private.log真实PG两个owner正向/拒绝通过；s5-notification-cc-full-private.log完整私有PG16/Redis/MinIO race PASS，无FAIL/SKIP/DATA RACE。最终拒绝测试覆盖缺Manager、错scope且明确ErrDenied，ticket_ccs/ticket_notifications/notifications/audit_logs/ticket_workflow_records五表整行JSON与调用前一致，证明已写CC关系随目标绑定失败回滚。成功意图四字段完整；移除Manager后重放不增意图且producer零Send。此BPMN测试是直接handler调用，不是callback Worker/租约全链；重放本次只断言数量与零Send，未另比成功行整行摘要。测试清理仅删除新通知，不当作实际外发完成。

s5-notification-cc-owner-regression.log service、service/bpmn、internal/bootstrap三包具名race回归PASS；不声称三包所有测试均执行。独立最终复核无新增阻断。同步SendNotification直接外发、email/push专业准入、目标变更/重启矩阵、飞书与裸Get/Send尚待完成，不勾选整个S5/S6/G2。固定CandidateSHA与停止状态不变，无共享数据库/WSL操作、企业外呼、push/main合并。最终构建下方补记。

最终s5-notification-cc-build.log全后端build exit0，git diff --check通过。当前无进行中的Go进程，后续可继续同步通知入口工作。


### B2 S5 同步通知直接外发 RED 与结果合同（2026-09-14）

SendNotification现仅先提交站内行，随后直接调用email/SMS/push；纯外部渠道没有队列事实，先前DeliveryKey存在性查询无法提供幂等。新增真实PG direct_notification_enqueues_without_provider_calls：新合法WorkItem、明确email-only偏好、进程内GraphMailSender探针，稳定内部DeliveryKey连调两次。s5-notification-direct-outbound-red.log复现探针分别1/2次、零外部通知意图、applied；并非真实企业邮件或HTTP调用。

s5-notification-direct-full-red.log完整私有PG16/Redis/MinIO race仅此新增用例及父测试FAIL，无其他FAIL/SKIP/DATA RACE；上轮f02bb4197绿色证据不覆盖新增要求。本轮无生产修复，也未将单测仅编译或无关构建当作验证。新意图测试清理只删除fresh WorkItem通知（当前0行），不触及受保护历史。

调用方调查：DTO当前仅applied/idempotent/blocked，HTTP始终200，前端TicketNotificationSection无条件显示“通知已投递deliveryCount次”。BPMN仅在有durable callback key时设置InAppOnly；现enqueue已检查该字段，不能误称完全忽略。原S5追加实现合同：唯一enqueue返回精确结果并在原事务冻结所有意图，SendNotification删除直接外发；queued/站内计数分开、HTTP202、前端类型与文案同步；内部key继续去重，HTTP缺key生成本次意图ID不等于网络重试幂等，不扩大为新请求ID产品能力。全偏好禁用无receipt不算交付；非durable BPMN不能将queued译为delivered。旧provider正向要改为queue→真实worker，不能删掉投递验证。

当前完整目标/S5/S6/T3/T4/G3未完成，CandidateSHA不变、候选停止，无WSL/共享环境修改、企业外呼、push/main合并。最终独立复核下方补记。

独立最终复核确认RED有效、合同无阻断。GREEN须将NotEqual(applied)收紧为明确queued/准确计数和重放结果，排除任意错误effect假绿；git diff --check通过。

### B2 S5 同步通知入队收敛 GREEN（2026-09-14）

关闭dced3d585的直接外发RED。SendNotification只拥有事务，调用唯一enqueueNotificationResultTx，站内双表与全部外部意图一起提交，删除请求内email/SMS/push调用。原EnqueueNotificationTx和创建/升级调用共享同一实现。内部DeliveryKey保留内容/来源冲突检查及原渠道重放；缺key生成本次调用ID，不提供HTTP重试幂等。缺recipient或后续目标绑定失败回滚整个事务。InAppOnly不新建外部意图，碰到同key已有外部意图拒绝。

返回queued/HTTP202与新外部意图数；站内新建、重放接收人、总持久意图及外部意图计数明确分开。旧行不按当前状态冒充新排队或送达。BPMN将入队视为持久受理动作，输出对应证据，applied/idempotent均不再宣称外部delivered；durable key仍强制InAppOnly。UI按queued/accepted/站内生效提示。独立审阅指出并修复queued非法effect及混合外部重放文案问题，最终无新增阻断。

验证证据（本机任务私有范围）：
- s5-notification-sync-full-private.log：完整私有PG16/Redis/MinIO race PASS，无FAIL/SKIP/DATA RACE；原直接外发RED改为明确queued/精确计数、pending/SentAt及重放结果。此套执行后仅增加BPMN受理文案和定向测试，没有更改通知事务实现。
- s5-notification-sync-final-unit.log：service/controller/service-bpmn/bootstrap四包具名race回归PASS，无FAIL/SKIP/DATA RACE。含混合目标失败全回滚、内容冲突、偏好变化不扩展、外部重放+新站内混合结果、InAppOnly及HTTP202；不声称四包所有测试都执行。
- 原同步Graph spy正向改为先验证零调用及pending，再由真实ProcessPendingDeliveries调用进程内Graph sender一次。旧日志测试迁移到真实worker失败，保留敏感字段/内容/错误不泄漏，并核验持久错误分类与尝试次数；未发送企业邮件。
- s5-notification-sync-ui.log：2 suites / 22 tests PASS；仅定向测试，关闭全项目coverage阈值，不宣称整体覆盖率达标。
- s5-notification-sync-types.log：theme check与tsc通过；s5-notification-sync-build.log：全后端build exit0。git diff --check通过。

本增量不完成email/push专业目标准入、飞书与裸实例入口、目标变化/重启矩阵或S5/S6/T3/T4/G3。CandidateSHA保持d7470a32dbb87acc9b5e4d9a895a146410723561，候选未启动，无WSL/共享数据库修改，无push/main合并。下一步核对email/push真实owner及其持久意图目标身份，再处理其余S5入口。

### B2 S5 持久邮件解析不可用时禁止跨provider回退（2026-09-14）

当前目标复核后继续email/push准入盘点，发现EmailService.SendForTenant的DisableProviderFallback仅覆盖Graph发送错误，配置了GraphProvider但解析不可用或nil sender时仍会实际调用SMTP并返回成功。s5-email-unavailable-fallback-red.log两个进程内SMTP探针用例准确复现；无企业/网络邮件。沿原发送owner修复该分支：禁止fallback时返回email_route_unavailable，不切换提供方；该错误证明零发送，分类not_accepted，避免Notification Worker或IncidentAlertDeliveryHandler误标delivery_unknown。无GraphProvider的显式SMTP保留成功正向，允许fallback旧语义不变。

s5-email-unavailable-fallback-green.log：EmailService/EmailAndCC/TicketNotification/SendNotification具名race回归PASS；s5-email-unavailable-incident.log IncidentAlert具名race回归PASS。s5-email-unavailable-full-private.log完整私有PG16/Redis/MinIO suite race PASS，无FAIL/SKIP/DATA RACE；s5-email-unavailable-build.log全后端build exit0；独立审阅无新增阻断，git diff --check通过。

影响边界：bootstrap始终注入GraphProvider，因此持久邮件在该解析器无可用Graph目标时保持未发送，不再以全局SMTP代替；本次没有启用候选邮件。此项只关闭单次调用跨provider回退，不证明跨重启目标绑定。实际newTenantGraphProvider仍用Manager.Get(tenant, msgraph-email)，专业邮件身份/准入未完成；Incident告警和通知共用此owner。push SendToUser按user ID投递且不返回接收事实，Notification Worker仍无从证明已送达，须在原Hub/队列协议内解决，不能以void返回当投递证据。后续继续真实owner目标/结果合同，S5/S6/T3/T4/G3与总目标保持未完成。

固定CandidateSHA与候选停止状态不变，无WSL/共享数据库操作，无push/main合并。源码与测试当前均无运行进程。
