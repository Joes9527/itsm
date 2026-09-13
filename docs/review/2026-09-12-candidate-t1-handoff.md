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
