# T2 候选环境准入交接

状态：准入复核中（2026-09-14 revision-3 见文末；当前 Agent 接管原 A/B 全部职责；旧窗口失效）。

依据：[T2 计划](../superpowers/plans/2026-09-12-itsm-candidate-t2-admission.md)、[总计划](../superpowers/plans/2026-09-12-itsm-candidate-two-agent-delivery.md)、[设计](../superpowers/specs/2026-09-12-itsm-candidate-integration-delivery-design.md)。

- TaskID: T2
- Status: blocked
- Owner: Agent B / Windows–Ubuntu WSL
- SourceSHA: 8152ee668a6096c98f90349fd7e43ebcd4f1c587（业务语义检查来源）
- CandidateSHA: d7470a32dbb87acc9b5e4d9a895a146410723561（已核实 T1 bundle；仅接收，没有构建或启动）
- ConsumedHandoffs: 设计包提交 2a993f7159ed43afc471a6ae938c379a47149ec2；T1 交接提交 a3f57bf541424129409dede9ee6f91bfcb913a57 的 docs/review/2026-09-12-candidate-t1-handoff.md；文档 SHA-256 cd0f200cab11ee9d700150fdd9a92bd7cc540bf5c4570892ddfc45e64d5d0b91。
- 设计基线/main: a25e108d2a08a55469fa5ad547aac5a9adc251ff。
- 执行时间：2026-09-12，Asia/Shanghai；数据为盘点时快照，不是备份一致性边界。
- Resources: 候选数据库、实例、卷、网络、角色和监听均 not-created；仅建立 B 的文档 worktree 与任务私有证据目录。
- NextAllowedAction: A 继续后台任务隔离与 Redis 撤销安全的前置设计；B 继续只读准备，等待剩余准入关闭和具体一致性窗口确认。T3 写入/迁移/应用启动均不放行。T4/T5 尚未具备开始条件。

## 交接与隔离证据

`git bundle verify` 成功，bundle 唯一分支为 `codex/docs/candidate-delivery-design`，提交如上，前置提交为 main 基线。只导入新任务引用并创建 `/home/administrator/project/itsm/.worktrees/candidate-b-environment-delivery`，没有覆盖 main、运行副本或其他 worktree。

main 仍有原有未跟踪 `docs/poc/`，未修改。业务来源 worktree 在检查时干净。本任务不改业务源码、测试 fixture、总计划状态，不安装/启动应用，不执行初始化、数据库迁移、历史清理、R(038)、企业动作或源服务启停。

原文证据与只读采集器位于 `/home/administrator/.local/state/itsm-candidate-delivery/t2/`（目录 0700）。采集不输出完整环境、私有启动 argv、密码、connector credentials、业务正文或对象内容。数据库通过既有身份执行 `default_transaction_read_only=on`，带 statement timeout；没有调用 Migrator 构造器、Prepare、CreateSchema 或应用启动程序。全库统计使用既有容器内诊断身份 `itsm_user` 的只读会话，不能作为受限角色验收证据。

## SourceMapping

| 来源 | 实际观察 | 准入含义 |
| --- | --- | --- |
| 固定启动配置 | `~/.local/state/itsm-kaf-baseline-20260908/config/itsm-launch.json` 及两个 worker launch；cwd 为其 `config/itsm`，读取 `config.yaml` | API 二进制已更新为 `bin/itsm-api-support-handoff-20260911`；不能沿用旧开发文档的二进制描述 |
| 配置指向的业务库 | `127.0.0.1:5432/itsm_config_baseline_20260908`，schema `public`；owner `itsm_base_owner_20260908`、runtime `itsm_base_app_20260908`、system `itsm_base_system_20260908` | 维护者在本轮消息明确确认的备份源；仍不称为活动连接证实的源 |
| 主仓库 dotenv | `localhost:5432/itsm`，用户 `itsm_user` | 与固定启动配置不同；不能默认采用此文件启动候选 |
| 活动连接 | `/proc` cwd/exe 未发现 ITSM/KAF 应用或两个 Worker；3001/8080/5173/8000 未监听；5432 的其他 client backend 数为 0 | 未运行/未连接的时点观察；不能证明远端 Mac、CI 或未来启动不会写源 |
| PostgreSQL | `itsm-postgres-dev`，PostgreSQL 17.10，vector 0.8.6；卷 `itsm_postgres_dev_data`；网络 `itsm_itsm-dev-network` | 同实例有多个不同数据集，禁止合并或重置 |
| Redis | 固定配置 `127.0.0.1:6389/DB11`；`itsm-redis-dev`，卷 `itsm_redis_dev_data`；DB11 211 keys / 210 expiring，另有 DB0 | DB 编号不是完整隔离；DB11 包含鉴权/队列等状态，不能整体丢弃或重置 TTL |
| 对象存储 | 固定配置 `127.0.0.1:9012`、bucket `itsm-uploads`；`itsm-minio-dev`、卷 `itsm_minio_dev_data` | 只读 S3 列举 3 对象/176 bytes，versioning 未启用；2 个被其他库引用，详见本轮增补；未下载内容或恢复 |
| 附件/导入导出 | 配置库 `ticket_attachments` 为 0；另外存在 CMDB import/export `file_url` 字段 | 源码 fallback 为启动 cwd 下 uploads，即 config/itsm/uploads；盘点时此路径不存在，实际曾用后端仍需核验；不能以附件表为空宣称所有对象无依赖 |
| KAF/CI | KAF PG 5434、Redis 6380；acp PG 5433、Redis 6379、MinIO 9000；CI Runner.Listener 活动 | 都不作为候选资源；acp MinIO 是开发/CI 共享边界，不迁移或更改 |

5432 上只读枚举到的数据集：

| 数据库 | 大小 bytes | tickets | migration ledger |
| --- | ---: | ---: | --- |
| itsm_config_baseline_20260908 | 27424435 | 6 | 24 条，007–031 的合法历史流 |
| itsm | 86996659 | 18 | 14 条，至 019 |
| itsm_baseline_20260908 | 68794035 | 18 | 14 条，至 019 |
| itsm_intake_test | 16266931 | 1 | schema_migrations 不存在，查询显式失败；不作为候选源 |
| itsm_p1_integration_verify_20260901 | 19707571 | 0 | 15 条，至 022；不假定与候选基线兼容 |

其余库只做区分性元数据盘点，未做完整迁移许可核验；源若改选，必须重新执行本交接的数据、迁移和引用盘点。`postgres` 管理库不在业务源范围。

配置库额外快照：ServiceRequest 4、ProcessInstance 6、Problem 1；outbox published 9、callback completed 1、notification 0；RCA metadata 0；connector 配置 1 条 microsoft/enabled=false。当前没有 pending 不等于消费者具有历史隔离能力，且 SLA、模板部署等不依赖 pending 队列。

## MigrationSemanticInventory

使用业务基线的纯函数 `ControlledMigrationCatalog` 和 `GetMigrationSQL` 导出 SQL/sha256；与只读 ledger 对比，24/24 checksum 匹配，无未知版本。纯 `PlanMigrations` 检查：`prepare` 可规划 037；`up` 当前没有 executable、列出 P/R manual。这只证明目录/账本可规划，不是目标结构和角色准入通过，更不是迁移执行回执。

| 版本 | 配置库事实 | DDL、历史影响与本轮结论 |
| --- | --- | --- |
| 012 | 已应用、checksum 匹配 | 删除 service_catalog_items 和 service_catalogs.form_schema；不重放 |
| 013 | 已应用、checksum 匹配 | 删除 SR approvals、旧共享/审批列及 service_request field_values；不重放 |
| 014 | 已应用、checksum 匹配 | 删除 approval_records/approval_workflows；不重放 |
| 017 | 已应用、checksum 匹配 | 删除 ticket_types 的旧审批配置列；不重放 |
| 022/027 | 真实历史回执存在且匹配 | 仅承认已存在历史；不伪造、补写、重放或退役其他对象 |
| 028 | 已应用、checksum 匹配；七个旧共享列均不存在 | 设置 ticket_id 非空/唯一/FK、替换并强制 SR RLS、删除七列。此库不构成 pending 删除阻塞；其他源不可套用 |
| 029 | 已应用、checksum 匹配；itsm_type 不存在 | SQL 含 target_class UPDATE 和删除 itsm_type；此库不重放，不新增历史回填 |
| 037 / P | pending | 放松存在的旧共享列 NOT NULL/default；补专业扩展 WorkItem FK、非空、唯一索引；替换三域 RLS。保留旧列/行，不做历史事实 UPDATE。仍需真实副本结构分类、完整证据/角色核验后才可执行 |
| 032 | pending，依赖 P | 增加 SLA cycle/default 0 与 nullable policy 字段；增加 audit receipt 字段/唯一索引和不可变事实触发器。替换触发器对象；没有历史政策重建 UPDATE，旧行读到新增默认值需记录 |
| 033 | pending | 替换 Incident execution 验证函数；callback 增加 actor_id/actor_source；增加/替换 actor 和 audit 触发器。没有历史业务 UPDATE/删除；依赖已有 024、026、032 结构和函数 |
| 034 | pending | 增加 Problem 验证字段和 verifier FK；创建 investigation/step/solution 表、索引/FK，替换对应 RLS 并 FORCE。没有历史验证回填；IF NOT EXISTS 不能代替已有对象兼容检查 |
| 035 | pending | Change 增加 nullable outcome、assessment、review、standard_policy 证据字段；无历史结果填充或旧字段删除 |
| 036 | pending | intake_resolution_snapshots 增加 nullable workflow_definition_digest/workflow_variables；无历史上下文回填 |
| 038 / R | pending manual | 明确排除，不执行 |

保留 `blockHistoricalDestruction`：012/013/014/017/028/029 的精确删除目标存在时必须拒绝。必须在 P 前闭合完整后继路径；不能仅因 037 可规划就先执行它。

RCA：`20260909_problem_rca_authority.sql` 不在受控目录中，不能手工运行。配置源已有 `problem_root_cause_analyses` 的 12 个 metadata 字段、problem_id 唯一索引及 `problem_rca_tenant_isolation`，无 `root_cause_description`，结构观察支持“已存在 RCA 基础”。未验证全部 FK/运行角色权限与业务查询，因此不是 RCA 旅程通过；也不声称本库必须执行未注册 SQL。若其他源缺对象，由 A 提出有界依赖方案。

## BackgroundWriterInventory

本轮通过整个 itsm-backend Git tree 相等验证：下列源码在已收到的 CandidateSHA 上逐字节不变。A 的前置修复产生新 SHA 后须重新核验。下表区分真实配置能力与缺口；源码 nil guard/测试 hook 不是部署开关。关闭独立 Worker、禁网或故意让 RLS 报错，均不证明 API 不写历史。

| 入口/写入者 | 源码位置与启动行为 | 现有控制/缺口；启动前与启动后证据要求 |
| --- | --- | --- |
| API Redis Stream audit/webhook | bootstrap/app.go:279–318；pkg/eventbus/eventbus.go Subscribe 立即启动 goroutine，消费/ack/nack；审计 handler 可写 audit，webhook 可出站 | 未见候选记录作用域或部署禁用配置；须保护历史 stream/consumer 状态及审计基线 |
| API vector 初始化 | app.go:418；VectorStore.EnsureExtension 发 CREATE EXTENSION/TABLE | 构造时发生，无部署开关；不能当作只读构造；结构先验/运行禁用需 A 审查 |
| API 附件存储初始化 | app.go:446–456；attachment_storage.go:81–87 在构造时 BucketExists，必要时 MakeBucket；失败告警后保留 LocalAttachmentStorage("uploads") | 启动前预建候选 bucket 并限定凭据权限；验证实际后端，禁止回退到共享/非持久化 cwd；候选 uploads 映射专属持久卷并记录失败行为。源配置 cwd/uploads 当前不存在 |
| API 默认模板/绑定 | app.go:601；LoadAndDeployTemplates、InitDefaultBindings，固定 tenant 1 | 构造时无条件启动，无候选作用域；需保护 process definitions/bindings 及历史实例关联 |
| API connector 恢复/poller | app.go:460；ConnectorController.LoadAll，enabled 条目自动 provision；MS Graph 立即 pollOnce 后周期扫描；email connector 也可周期执行 | 已存 enabled=false 可避免本条恢复，但不能修改历史行作为隔离方案；复制 credentials 仍须受保护且应用不能获得真实企业权限 |
| API 普通 outbox | app.go:1263/1283；运行 NewOutboxDeliveryWorker，默认轮询 5s | 构造时总是创建；仅排除 KAF event_type，没有本任务范围门禁；保护 lease、attempt、status、所有专业消费者效果 |
| API callback | app.go:1401；2s 周期，支持立即/租约恢复扫描 | 无部署禁用/任务范围；保护历史 callback、process task/instance/approval/audit |
| API notification | app.go:1409；2s 周期交付队列 | 无部署禁用/任务范围；外部失败也可能改变 status/attempt/audit |
| API embedding | app.go:1327；初始每 tenant 200，后每 15min 50；查询失败 fallback tenant 1 | 无部署禁用；context.Background 不随 lifecycle 取消。保护知识/向量历史，不能靠缺 key 证明不写 |
| API SLA / escalation | app.go:1361；5min / 15min | 无部署禁用/任务范围；tenant 过滤不等于候选记录范围；保护 SLA、升级、通知与业务状态 |
| API tool queue | app.go:684；NewToolQueue 即启动内存 worker | 不恢复历史内存任务，但 HTTP 工具入口可写业务；需要鉴权/候选归属与出站拒绝验证 |
| 独立 KAF Worker 两份启动描述 | kaf_worker.go:33/84，dispatcher.Run；默认轮询 5s | 整个进程不启动可禁用；未见仅处理本任务新 outbox 的范围。G2 要求真实 Worker，不能永远停用后计为通过 |
| 请求触发的后台任务 | TicketService.GetTicket:406 也可能异步 Feishu 同步；工单动作异步通知/同步，CMDB import/export 异步处理；cloud.RunAll 并行发现 | 不因 HTTP GET 名称假定无副作用；仅允许明确候选记录及隔离测试接收端。未见这些任务自动恢复历史；WebSocket hub 为连接/消息处理，不能替代持久化队列盘点 |
| 宿主启动器/调度/CI | 私有 launcher 只提供 check/status/up；当前无业务进程；system/user timers 只有 OS 维护；当前用户 crontab 无任务；CI Runner 活动 | 未接管旧 launcher/CI；本轮另查 root cron、Windows 相关任务、CI 来源，见增补；远端 Mac/未来 CI 作业仍需维护者协调 |

建议保护断言：恢复后对历史业务行、outbox/callback/notification、workflow definitions/bindings、audit、知识/vector、Redis stream/nonce/revocation 记录清单与规范化摘要；启动及至少完整相关周期后逐项对比。不输出业务正文。当前没有候选启动或周期前后不变证据。

## ResourcePlan / Backup

下列都是拟议名称，没有创建或保留端口。B 所有，T3 开始时再检查冲突与镜像/版本摘要。

| 资源 | 拟议身份/边界 |
| --- | --- |
| Compose/project/network | itsm-candidate-b-20260912；内部网络 itsm-candidate-b-20260912-internal；不挂共享网络 |
| PostgreSQL | 专用实例，匹配 PG17/vector 0.8.6；loopback 15432；人工库 itsm_candidate、另建可重置测试库 itsm_candidate_test；专用 pgdata 卷 |
| 数据库角色 | candidate_owner、candidate_runtime、candidate_system；全新秘密，owner 不给应用；runtime 非 owner/non-super/non-bypass/无成员，system 严格既有 allowlist |
| Redis | 专用实例/卷；loopback 16389；noeviction、持久化/冷启动恢复明确配置；不复用源 DB 编号冒充隔离 |
| MinIO/附件 | 专用实例/卷，loopback 19012/19013；bucket itsm-candidate-uploads；候选独立附件目录；凭据仅能访问候选 bucket |
| API/Web | loopback 18080/3301；外部稳定访问需单独受控绑定；当前拟议端口未在 ss 中出现 |
| Worker | 候选实例，不发布健康端口到公网；明确消费者归属和启动门禁 |
| 出站 | 默认拒绝公网、企业网络、宿主/共享源端口及 Docker 网关访问；仅允许候选 PG/Redis/对象存储与经批准的测试接收端。Docker internal 网络仍需实际负向探测，不能单凭配置宣称有效 |
| 构建/证据 | 固定 CandidateSHA 的独立构建 checkout；私有配置、backup、restore-test 与人工候选分开；不挂源 Docker socket/目录/秘密 |

资源快照：WSL 文件系统可用约 634 GiB；内存可用约 28 GiB、swap 8 GiB。配置库约 26.2 MiB、bucket 目录约 32 KiB、Redis /data 约 40 KiB。预算按 `1 份备份 + 2 次恢复 + 对象/Redis副本 + 20 GiB 构建/镜像余量`，建议预留 25 GiB；其他源或完整对象清单更大时重算。Windows 承载 VHDX 的实际剩余空间/配额仍需核对；WSL 逻辑余量不替代宿主容量。

一致性方案已根据维护者源确认更新，具体范围和拟议墙钟窗口见“本轮只读增补”。单 PG dump 的 MVCC 不提供跨 Redis/对象原子性；目前没有冻结写入、备份或恢复。
Redis 历史 nonce、撤销、租约/锁保持绝对到期，不恢复已过期凭据，不把 copied pending 变为执行授权。恢复流程不能清空历史队列或伪造完成状态。新增候选 JWT/identity/worker 秘密与源隔离；备份中的企业 credentials 不能原样成为候选可用权限，安全装载边界未闭合前禁止应用启动。

## 安全准入

源码 `authentication/revocation.go` 默认 memory store；app.go:748–760 仅 Redis Ping 成功后切换 Redis 撤销存储。首次连接失败路径只 warn/close，空内存可能把已撤销 access token 当作未撤销；必须由 A 以真实断言复核并提出有界修复。运行中 Redis 查询错误可由 middleware 拒绝，不替代冷启动断言。

源 Redis `appendonly=no`，RDB save 为 `3600 1 300 100 60 10000`，`noeviction`、无 maxmemory 限额。RDB 正常加载并不证明最近撤销/nonce 不丢失。候选需验证：撤销后跨 API 进程/重启持续拒绝；Redis 冷启动、不可达、丢状态时 fail closed；TTL 绝对到期；refresh 单次消费与 nonce 防重放不复活。T2 不对源做 Redis 故障注入。

原 launcher 有 INTAKE_IDENTITY_CONFIG_FILE。候选 HMAC 默认不启用；需要相关旅程时配置候选专属 provider/secret，验证 provider/channel/purpose、tenant/mapping、nonce 和恢复。不能继承真实企业身份或把 HMAC backlog 当验收豁免。

## Blockers / 责任与解除条件

| ID | 阻塞级别 | 事实与责任人 | 解除条件 |
| --- | --- | --- | --- |
| B1 | 已关闭：源库选择 | 维护者确认 itsm_config_baseline_20260908/public；主仓库 dotenv 不作为候选源配置 | 仅数据库选择关闭；共享对象边界与备份窗口分别由 B6 控制 |
| B2 | 首次 API/Worker 启动、G2 | 多个自动写入者缺少部署禁用或候选记录范围；包括构造阶段。A 提案，维护者裁决 | 固定新 SHA，逐项配置/代码证据与受保护历史断言；不能以禁网/删行替代 |
| B3 | 启动安全/G2/G3 | 冷启动 Redis 失败可留空 memory 撤销存储；源未启用 AOF。A/B | A 补齐 fail-closed 证据/必要修复；B 配置持久化并在隔离候选验证冷启动和状态丢失 |
| B4 | 部分关闭：版本已收到；T3 迁移仍阻塞 | T1 bundle、CandidateSHA、后端 tree 相等及 24/24 ledger checksum 已重核；真实恢复、P 结构/角色证据尚未产生。A/B | 等待前置修复新 SHA 与其 G1/审查证据，重做受影响准入；只有剩余准入关闭后才可进入恢复与迁移流程 |
| B5 | RCA/业务验收 | 源已有 metadata 基础，但未完整验证 runtime FK/权限/旅程。A/B | 副本中核对实际结构和受限角色查询；缺口不能手跑未注册 SQL |
| B6 | 备份/恢复/启动与 G2 | 对象/本地范围已只读区分，DB11 状态已盘点；共享资源写入者排除、企业秘密装载、出站负向证明与实际一致性窗口仍未闭合。维护者/B | 按下述范围和时间表取得窗口确认，补齐远端/CI 不写入确认；之后才可受控保全，不能现在停源或创建恢复目标 |

可继续：文档/资源设计、只读复核、A 的代码集成与有界修复设计。不得开始：源停写、复制/恢复/迁移、候选应用启动、R、历史 backfill、真实企业写入。无权将未知项标为通过。

## 本轮只读增补（2026-09-12，revision-2）

### T1 接收与准入状态

`candidate-t1-a3f57bf54.bundle` verify 成功，包头为 a3f57bf541424129409dede9ee6f91bfcb913a57；d7470a32dbb87acc9b5e4d9a895a146410723561 为其祖先，两者只差 T1 文档。只 fetch 到新引用 `codex/chore/candidate-b-t1-import-a3f57bf54`，不修改 A 分支或 B 文档基线，不创建构建/运行 checkout。整个后端 Git tree 在 CandidateSHA 与上一轮 SourceSHA 都为 `4f92bcc0571a6f11512ad58a2ab05bf3648dc7ba`；重新读取源 ledger，24/24 checksum 仍匹配。已接收 G1 交接声明，不等于在 WSL 重跑 G1。后台和撤销缺口未因版本接收而关闭。

### Redis DB11 保全范围

固定 API/两个 Worker 的配置仍绑定 `127.0.0.1:6389/11`。只用 SELECT、SCAN、TYPE、PTTL、TIME、XLEN、XINFO GROUPS、CLIENT LIST 和 INFO 查询，无 GET 业务值、消费、ACK、DEL、SAVE 或配置修改。约 2026-09-12 21:39:28 CST 观察：

- `jwt:refresh:consumed:` 210 个 string，均带 TTL；剩余 TTL 约 222560992–417853033 ms。它们是已消费防重放状态，不是可再次使用的 refresh token。
- `ai.triage.completed` 一个无 TTL Stream，4 条消息，XINFO GROUPS 返回空列表。没有创建消费者或读取消息正文；没有组不等于这些历史事件可被重新执行。
- 未枚举到 `jwt:revoked:` 键；不以当前数量为零豁免撤销冷启动修复。DB0 还有 2 个持久键，排除于本源 DB11 保全范围，不能执行实例级清理或把 DB0 混入候选。
- CLIENT LIST 只有本次诊断连接；这是时点观察，不代表远端写入排除。SCAN/PTTL 非原子快照；过期自然推进，不以 TTL 相等作为内容不变标准。

未来受控保全以 DB11 的全部键/类型/值/绝对 expire-at/stream 状态为范围，保留原始受保护证据；需以源 Redis TIME 和每键实际过期时间记录边界，恢复到时已过期的键不复活。旧 stream 和复制的 nonce/lease/pending 不成为新执行授权。当前 key 命名不能证明每条状态唯一属于这个 PostgreSQL 数据库；同用 DB11 的任何 API 都属于待排除写入者。候选 fresh JWT/provider 身份和消费者隔离由 A 设计决定，不能由 B 删除历史鉴权状态来迁就启动。

### 对象与本地附件范围

使用源私有配置签名、仅对 `127.0.0.1:9012/itsm-uploads` 执行 S3 GET bucket versioning 和 ListObjectsV2；未获取对象正文，未创建 bucket。3 个对象共 176 bytes，分别 41/41/94 bytes，最后修改为 2026-08-31、2026-08-31、2026-08-13；列表保存 key 的 SHA-256、大小、时间和 ETag。ETag 不作为完整内容校验或跨存储原子性证明。

| 范围 | 只读结果 | 备份/恢复边界 |
| --- | --- | --- |
| 确认源 ticket_attachments | 0 行，file_path/file_url 无引用 | 当前无需从其他数据集补附件；窗口前重新检查 |
| 确认源 ticket_comments.attachments | 0 comments，0 非空附件 JSON | 不遗漏评论附件；源正文仍随数据库保全，不自动抓取正文中的外部链接 |
| 确认源 CMDB import/export file_url | 两表非空引用均 0 | 没有当前结构化文件依赖 |
| 共享 bucket | 2 个对象 key hash 匹配 itsm 和 itsm_baseline_20260908 的附件引用；第 3 个未在所查结构化引用中匹配 | 这些对象不导入为候选源的业务附件、不删除。可另做受保护旁路归档，但须单独明确共享桶范围与窗口，不自动扩大为全桶停写 |
| 固定启动 cwd 的 uploads | `/home/administrator/.local/state/itsm-kaf-baseline-20260908/config/itsm/uploads` 不存在 | 当前不存在源 fallback 文件；后续启动前必须重新确认实际存储后端 |
| 旧运行 checkout uploads | `/home/administrator/apps/itsm-kaf/itsm/itsm-backend/uploads`，2 文件/33 bytes | 保留原位，不能按目录名混入确认源 |
| 主仓库 uploads | `/home/administrator/project/itsm/itsm-backend/uploads`，2 文件/33 bytes；与旧目录相对路径 hash 相同 | 仅路径/数量/大小一致，未宣称内容完全相同；保持原位 |

建议候选附件恢复清单只包含确认源的结构化引用及对应本地/对象文件；当前清单为零。共享桶第三对象归属未知不妨碍区分已确认源，但不得被标为垃圾或补入源。窗口前如出现新增附件/评论/导入导出引用，停止使用“零附件”结论，重新生成清单并协调相应存储写入者。候选新附件仍须使用专属 bucket/持久卷，不能访问共享桶来伪装恢复成功。

### 其他写入者与排除证据

约 21:40–21:42 CST 的 `/proc` cwd/exe、ss、pg_stat_activity 和 Redis CLIENT LIST 未发现 ITSM/KAF 应用、Worker 或其他源连接；不保证整个未来窗口无连接。Source PG 只有 plpgsql/vector 扩展，未见 pg_cron 扩展。以下主体仍列入窗口责任清单：

| 主体 | 证据与限制 | 窗口前责任 |
| --- | --- | --- |
| 固定 API/两个 Worker、私有 dev-services launcher | 配置仍存在，进程当前未运行；launcher 可在以后启动 | B 重新检查精确 PID/cwd/配置；只有收到窗口授权才处理确实运行的源进程，不调用全局 up/kill |
| 本机 cron/timers | 当前用户和 root 均无 crontab；system/user timers 为 OS 维护；检查 cron.d/周期目录元数据 | B 窗口前复查；字符串扫描不是任意脚本行为的完整证明 |
| Windows 相关计划任务 | 定向匹配发现 UpdateWSLSSHPortProxy、WSL_Fix_Inbound；前者脚本涉及 WSL/portproxy，未发现 ITSM launcher；后者仅有 action 元数据，未完全追踪其所有间接调用 | 不关闭这些网络任务；如需将其算作完整排除，维护者/B 应补足脚本调用链。没有发现等于零业务写入的全局证明 |
| CI | 本机 JULIAN runner 归属 DawnproIN 组织，Runner.Listener 在运行，未发现 Runner.Worker；Candidate 仓库 workflows 的 runs-on 为 ubuntu-latest，没有 self-hosted 标记 | 不能推定组织其他 repo/未来 job 不会访问共享实例；A/维护者在窗口前确认无该源/DB11 写入任务，不全局停 runner |
| Mac 开发进程/手工脚本/其他操作者 | WSL 无活动连接快照不能排除以后重连；未登录 Mac 执行核实 | A/维护者明确确认窗口内不启动或写入该源及 DB11；不得将其计作已排除 |
| 共享 bucket/其他源使用者 | 两个非候选数据库引用同一 bucket 对象 | 本轮默认不对这些资源停写或恢复；若申请共享全桶归档须列独立范围，不能用本源确认自动覆盖 |

### 拟议一致性窗口（未批准、未执行）

建议最早窗口为 **2026-09-13 10:00–10:15 Asia/Shanghai（02:00–02:15 UTC）**。这只是可评审提案，不是预约、自动化或停写授权。要求 09:30 前 A 的前置设计/实现与适用门禁、资源隔离方案及维护者窗口确认全部闭合；不满足即延期并重新提出时间，绝不自动开始。

| 时间 | 获准后才可执行的步骤 | 成功/中止条件 |
| --- | --- | --- |
| 09:30–09:55 | A/维护者确认 Mac/CI/手工写入排除，B 再核实源配置、源附件清单、DB11 归属、空间与只读连通性 | 任一主体未确认，或出现新引用/来源漂移，窗口取消；不扩大停写范围 |
| 10:00–10:02 | 按批准的精确范围隔离源写入口；若源应用原本已停，维持原状态。记录进程状态、DB/Redis 连接与事务边界 | 不重启原已停服务、不禁用整个组织 CI、不影响其他数据库；无法排除写入则中止 |
| 10:02–10:08 | PG 一致快照备份并保存序列/角色/RLS元数据；DB11 受保护导出含绝对到期；重复核对源引用清单，复制其中必要对象/本地文件 | PG 快照不是跨存储原子性。对象版本化关闭，若清单非零则需同范围写入冻结与元数据/内容校验；发生变化则标记备份无效，不能声称一致 |
| 10:08–10:12 | 校验备份可读取、元数据/引用覆盖、文件校验和、DB11 绝对期限与历史 stream 保留；记录统一窗口和各存储真实捕获时间 | 不调用候选应用，不执行恢复或迁移；超时/失败留证据并停止推进 |
| 10:12–10:15 | 解除本窗口实际施加的写入限制，按窗口前状态核对源；向 A/维护者交接证据 | 原本停止的应用继续停止；失败也不覆盖源或删除其他资源。恢复/迁移仍需各自后继门禁 |

Redis TTL 自然过期不算业务停写失败；备份须记录实际 expire-at，不能以扫描开始时间加旧 TTL 重新延长寿命。当前未证明该跨存储方案已可运行，T2 保持 blocked；禁止现在执行源停写、恢复、迁移或候选应用启动。

本轮原文在 `~/.local/state/itsm-candidate-delivery/t2/revision-2/`：candidate-revalidation.json、ledger.json、redis-inventory.json、object-inventory.json、attachment-reference-inventory.json、reference-match.json、writer-inventory.json、windows-task-metadata.json、windows-task-scripts.json、launch-inventory.txt。所有来源状态只在本轮观察时点有效；未存对象正文或明文密钥。

## 独立只读复核

按 requesting-code-review 技能派发了一个不参与实现的短时 reviewer，未增加实施 Agent。审阅范围为计划、业务 SourceSHA 和脱敏证据；首轮无 Critical/P1，发现 P2：遗漏 MinIO 构造时建 bucket 及本地回退边界。B 读取对应源码复核后补齐上表、源路径观察与启动前断言。修订后同一 reviewer 只读复审确认 P2 已关闭，无新增发现。复核支持 T3 blocked，不替代 A/维护者的源选择或运行批准。

本轮 revision-2 同一独立 reviewer 对增量及证据复审，无 actionable findings；确认 DB11/附件范围与未批准窗口表述一致，T3 仍阻塞。复核不代表 A 的前置设计或运行门禁已通过。

## Evidence / NotValidated

- `inventory.txt`：启动配置 allowlist、进程 cwd/exe、容器镜像 ID/卷/网络/Compose 来源。
- `config-metadata.txt`、`other-metadata.txt`：固定配置与主仓库 dotenv 的非秘密连接元数据、cron 摘要、存储大小。
- `db-inventory.txt`：只读身份、库大小/连接、ledger 字段、roles/RLS/owner、目标列元数据。
- `db-detail.txt`、`extra.txt`：24 条真实 checksum、队列计数、RCA/connector/角色信息、其他数据集区别、Redis keyspace/持久化设置。
- `catalog.json`、`migration-comparison.txt`、`ledger.json`、`pure-plan.txt`：从 SourceSHA 导出的 SQL/逐项 checksum 与纯规划结果；无迁移执行。
- `historical-baseline.txt`：单个 repeatable-read/read-only 事务中的核心历史表计数与 SHA-256 内容摘要；仅为源观察基线，不是候选启动前后证明。
- `worktrees.txt`、`main-status.txt`：现有工作区保全记录。
- `evidence-sha256.json`：私有原文与采集脚本的 SHA-256 清单，随离线包另提供脱敏摘要文件。

没有运行业务测试、源故障注入、备份/恢复、迁移、候选构建/启动、G1/G2/G3、60 分钟观察或 A/维护者业务验收。没有推送/合并 main。T2 的 blocked 是明确准入结论，不是候选交付完成。

## 2026-09-14 revision-3：统一责任与源已重新运行

当前 Agent 同时负责代码和环境。维护者已接受新 PG 鉴权存储尚未接入启动/登录/刷新/注销、真实鉴权重启与恢复未验证；旧 B3 对这些特定缺口的强制前置要求由唯一剩余计划第7.11节取代，不将缺口标为完成。运行限制已随发布分支 `3142247e` 的 `docs/deployment/itsm-candidate-runtime.md` 提交。

11:35–11:37 CST 重新执行只读采集，原 evidence 不覆盖；新证据在 `~/.local/state/itsm-candidate-delivery/t2/revision-3-20260914/`。四个采集器均退出0，源 SQL 使用 read-only 与15秒 statement timeout。

- 原固定源 API PID2248818、Worker PID2249326/2249327 现正在运行；exe/cwd 精确匹配原 baseline 启动目录。不得再采用“源已经停止”的结论。
- 源库仍为 `itsm_config_baseline_20260908/public`，tickets 从旧6条变为8条，comments 4条，附件表0条，ledger24条。源 SQL 快照看到4个既有 system 角色连接；idle 不等于整个备份窗口不会写入。
- Redis DB11 现为220个已消费 refresh string、1个 ratelimit zset、1个历史 Stream；有既有连接，观察到 EXPIRE/XADD/XREAD 等命令。未读取业务值、消费或修改源状态。
- S3 列举仍为3对象；源附件路径/URL及CMDB导入导出结构化引用仍为0。评论附件内容边界必须在窗口前再次核对，不把comments数量非零误记为无附件。
- 没有执行源停写、备份恢复、迁移或候选应用启动。源及候选部署状态不因私有代码测试通过而改变。

### 可评审备份窗口（待维护者确认，尚未执行）

窗口为维护者确认后约15分钟；到执行前重新报告实际起止时间并复查身份。仅冻结上述 baseline 的 API 与两个 Worker，暂时中断其登录/工单写入和后台处理，备份后恢复原进程状态；不停止共享 PostgreSQL、Redis、MinIO或CI Runner，不影响其他数据库。

1. 核对当前 PID/exe/cwd、配置来源和进程重启方法；确认 Mac/其他操作者在窗口中不写该源与DB11，排除重启器。任一未排除则中止。
2. 受控停止准确的源 API/两个 Worker，核对对应 PG/DB11 连接退出；不全局 kill、不改共享数据或权限。
3. 使用 PG 一致快照导出确认源，单独保全角色/权限/序列元数据；DB11 导出所有键值、Stream/消费者状态及绝对过期时间到私有目录，恢复时不复活已过期键。重新检查引用，仅复制确属该源的对象/本地附件；共享桶非源对象保持原位。
4. 记录各资源捕获时间与哈希、验证备份可读及引用覆盖；源发生漂移或无法确认一致性则将此次备份标无效，中止后续恢复。
5. 按窗口前状态恢复源 API/Worker，核对健康与原访问入口。失败保留证据并报告，不重置源。

此窗口仅针对备份与源短暂停写；后续独立候选恢复、037准备与普通迁移仍按固定SHA/受限角色/完整语义准入执行，R(038)始终排除。恢复目标不得复用源实例或企业秘密。此前2026-09-13拟议窗口未执行且已过期。