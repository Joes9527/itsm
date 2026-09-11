# WorkItem 收敛切换与恢复手册

## 当前控制入口：准备 P、普通迁移、退役 R

历史 022/027 的 SQL 与 checksum 仅用于识别已执行历史，普通 up 不再执行它们。当前阶段顺序由唯一目录确定：普通至 021 → 手工 037 P → 普通 023–036（排除旧 027）→ 手工 038 R。最大版本号不是就绪证明；status/dry-run 分别显示 executable 与 pending_manual。down/rollback-to 使用阶段依赖顺序；reset/P/R 不提供重建空表式恢复。

先由部署运维设置只读、不可被 group/world 写入的 JSON 文件，并通过 `ITSM_MIGRATION_CONTROL_FILE` 指向它。字段来自 `migration.MigrationControlConfig`：DeploymentID、InspectionRole、ReviewedGrants、RetirementPublicKeys、HistoricalRetirementPublicKeys。公钥为独立固定的 Ed25519 公钥，JSON byte slice 使用 base64；不得从提交的 evidence 中建立 trust root。CLI Operator 始终来自实际 OS 用户。业务运行另用 `ITSM_MIGRATION_INSPECTION_DSN`（PostgreSQL URL、显式 schema、专用 inspection 用户）；它不替代业务数据库配置，连接目标必须一致。

编译后使用现有布尔 flag：

- `migrate -prepare-workitem -dry-run` / `-retire-workitem -dry-run`：只读输出当前 inventory，供环境证据绑定。
- `migrate -prepare-workitem -evidence-file /reviewed/preparation.json`：验证原始目标/摘要/真实运维身份，在同一事务提交 P 及附件。
- `migrate -up`：只执行获准普通迁移，继续报告 pending_manual；不得把该退出 0 单独视为业务就绪。
- `migrate -retire-workitem -evidence-file /reviewed/retirement.json`：验证独立公钥授权及完整 R 证据；同一已提交证据重试返回原结果。
- `migrate -status` / `migrate -dry-run`：只读展示；参数冲突/不完整请求 exit 2，连接/准入/执行拒绝 exit 1，成功 exit 0。

控制 flag 与 up/down/reset/fresh/seed 等互斥，任何连接前拒绝多操作。fresh 仍只允许显式确认的空开发目标；本方案不以 fresh 绕过恢复。空环境第一次 bootstrap 停在 P 且不 seed；已存在目标不会 overlay Ent。P 后缺任一普通迁移仍未就绪，原 P 回执不删除。012/013/014/017/028/029 只有逐个确认没有实际删除目标后才可执行；013 也检查 field_values 行。现有父表在执行前加锁，外部直接 DDL 必须服从环境维护窗口和同一迁移互斥纪律。

运行时另在双方持有的只读事务内，以两枚随机事务级 advisory lock 及 pg_locks 核验同一实际实例/数据库/backend；相同私网地址元组不足以准入。全部检查保持同一 inspection 事务，10 秒超时，成功/失败/取消均释放；不增加业务证据访问或角色权限。表级和列级 grant option（含适用 PUBLIC）均拒绝。

运行时结构准入的权限及与全局业务验证的区别，以[受控退役设计](../superpowers/specs/2026-09-11-workitem-controlled-retirement-design.md)中的“运行时只读准入边界”为准。下文旧 C1/022/027 记录是历史验证范围，不是当前执行步骤。


## 部署前未解决清单：独立 RCA 元数据 schema

当前活动 033 是 Incident status events；独立脚本 `itsm-backend/migrations/20260909_problem_rca_authority.sql` 未注册到普通目录。现有 `handlers/problem/root_cause_metadata.go` 与 `service/problem_investigation_service.go` 的 RCA 路径需要 `problem_root_cause_analyses`，但空 canonical bootstrap 不创建它。该脚本还回填旧 root_cause 并删除 root_cause_description，超出本轮“不回填、只受控替换 022/027”的授权；不得自动注册、运行或补造回执。此缺口需单独部署决策，不能据计划内 V1 核心旅程通过宣称所有独立功能就绪。恢复演练应保留环境原有 RCA 数据（如存在）。

当前只读准入检查生成 Ent 模型全部必需表/列/类型，以及 active 007/008/034 的 13 个原生 SQL 表全部列/类型；缺失或不兼容均拒绝。它不证明任意索引、默认值、存储参数完全等价，也不把独立未注册脚本算作已完成普通迁移。原始 P 未固定 inspection role 的目标不能通过追加授权/改写附件静默适配：需另行评审访问变更，本轮无此类已部署目标。



> 2026-09-11；状态：隔离验证中，禁止据此宣称实际部署、观察或旧结构删除完成。
> 权威执行分支：`codex/refactor/workitem-next-stage`；原实现基线 `46606330`，接受设计基线 `3064ea5e`。
> 本手册继承 [原 C3](../superpowers/plans/2026-09-09-workitem-convergence-runtime.md) 和 [本轮 V2](../superpowers/plans/2026-09-11-workitem-next-stage-experience-validation.md)。

## 历史 C1–C3 验证记录（由当前 P/R 入口取代）

该历史阶段的应用迁移入口在同一事务执行 SQL 前检查 022/027 将删除的对象。存在任一旧表或旧列即失败，不删除、不补写历史、不写该迁移回执，也没有跳过开关。没有待退役对象的新规范 schema 可以继续既有 bootstrap；已有账本仍按原 checksum 校验，不重放已应用迁移。

这一保护不是允许删除的操作入口，也不验证外部备份或观察报告。没有备份、恢复、消费者退出、旅程及观察证据时，不存在自动放行路径。授权删除仍未实现、未演练、未执行。

发现的实际缺口：022 的历史 SQL 含五个 `DROP TABLE ... CASCADE`，并与正式 SQL 文件、迁移账本 checksum 对应。直接修改旧 SQL 会使已应用环境出现 checksum mismatch；直接运行它又不符合本轮无级联删除和先门禁要求。因此本轮首先在既有 Migrator 加不可绕过的自动退役拒绝，保留账本历史。历史 SQL 文件不得直接用于生产退役；运行统一入口以外的手工 SQL 不受应用保护。历史迁移的受控替换需要单独决策，不能重写已应用账本、复制另一套 schema 权威或增加跳过校验参数。

## 环境记录与暂停范围

每次环境变更先记录：环境名称、主机、数据库、schema、应用提交/镜像 digest、数据库版本、迁移账本版本和 checksum、操作者、变更单、维护窗口、备份位置及 SHA256、恢复验证记录。不要把分支 HEAD 视为部署版本，不把凭据放进报告或命令行日志。

使用既有配置：`DB_SCHEMA` 固定精确 schema；`ITSM_AUTO_MIGRATE=false`、`ITSM_AUTO_SEED=false` 用于受控环境，不能依靠应用启动自动执行历史删除。数据库与双角色配置沿用 [开发指南](../DEVELOPMENT_GUIDE.md)。设置这些开关不代表新结构已经满足应用需要，启动前仍须核对完整 schema、约束及账本。

暂停范围包括三域写 API、通用 WorkItem 写入口、BPMN 启动/用户任务提交/回调重试、规则/自动升级、SLA 监控与关系通知消费者、外部连接器和 AI 写调用。暂停由环境现有运维方式完成；只停网页不能建立一致快照。记录暂停前队列积压与在途任务，恢复后核对每个处理结果。

## 精确退役对象与消费者盘点

下列为现有 022/027 的删除清单，不是本手册的执行 SQL。操作必须带明确 schema，使用默认 RESTRICT；不得通配、CASCADE 或自动清洗冲突行。

| 对象 | 待退役内容 | 当前权威及检查 |
|---|---|---|
| ticket_approvals | 整表 | BPMN ProcessTask / ProcessApprovalDecision；确认旧审批读写消费者退出 |
| workflow_tasks、workflow_instances、workflow_versions、workflows | 四张旧运行表 | process_* BPMN 运行结构；保留旧实例证据和终结结果，不自动取消或迁移 |
| releases | requires_approval | Release 保留独立域和既有规范流程身份，不因 WorkItem 改造重分类 |
| ticket_categories | workflow_id | ProcessBindingService 为唯一绑定写入口；先查依赖再删除引用列 |
| incidents | title、description、status、priority、reporter_id、assignee_id、category、subcategory、source、tenant_id、version、created_at、updated_at、resolved_at、closed_at、deleted_at | 关联 tickets 的公共事实；IncidentService 持有专业动作 |
| problems | title、description、status、priority、category、assignee_id、created_by、tenant_id、created_at、updated_at、resolved_at、closed_at、deleted_at | tickets 公共事实；Problem metadata/lifecycle 同一专业事务 |
| changes | title、description、status、priority、assignee_id、created_by、tenant_id、related_tickets、created_at、updated_at | tickets 公共事实、结构化关系、Change 专业命令 |
| tickets | type | record_class 与专业 subtype，各自单一权威 |
| incidents | incident_number | tickets.ticket_number，保留原编号，不重新编号 |

需要精确验证的约束为三域 work_item_id 的 NOT NULL、唯一性、唯一且正确的 WorkItem 外键，以及基于 WorkItem tenant 与 soft-delete 条件的 RLS。022/027 的既有 verify SQL 保留检查定义；约束失败是阻塞，不能通过数据回填、删除冲突或扩大 DROP 来“修复”。

消费者退出证据：B1 旧 AssignIncident 已删除；B2/B3 通用 Ticket 核心编辑/生命周期/分派与批量、策略、MSP、BPMN ticket 写路径拒绝专业类；Problem 普通编辑、RCA、调查、步骤和候选方案写入使用观察版本与回执；B4 旧 Department/ProcessRouting 直接绑定写入已移除。共享评论/附件/通知属于保留能力，不以全库旧词表零匹配作为完成标准。详细入口见 [后端执行记录](../superpowers/plans/2026-09-11-workitem-next-stage-backend.md)。

## 只读预检

构建并运行既有 `cmd/check_workitem_cutover` 二进制，不能用 `go run` 的外层退出码判断 2：

- exit 0：当前观察快照无旧身份/依赖阻塞；仅表示身份切换预检通过。
- exit 2：有活跃/挂起旧实例、待处理旧回调、旧绑定、身份不一致、缺扩展或扫描截断，必须停止。
- exit 1：工具或数据库错误，不按成功处理。

部署前检查所有租户，不能以一个租户通过代表全环境。保存 JSON 报告、配置目标、应用版本和检查时刻；比较检查前后行数与内容摘要。exit 0 不替代备份恢复、权限、旅程、观察和环境批准，也不允许直接调用历史删除 SQL。

## Change 启动归属切换的补充盘点

V1 暴露了 Change 创建后台启动与专业 submit 的重复启动。补充修复 ed80f99b 收敛为创建冻结定义/输入、submit 唯一启动。新增036只在选定schema为既有snapshot增加摘要和输入字段，不回填历史；缺目标表会失败，不沿search_path修改其他schema。

部署前另行盘点每个租户的 Change 草稿、已有 workflow.start.requested 事件及其投递状态、已运行流程实例和冻结 intake snapshot。既有队列事件、运行实例或缺少新冻结证据的记录不得静默复用、补造快照、按最新定义重启或自动取消。逐项形成处理决定并排空未解释冲突后才允许切换。新建隔离数据的零重复启动结果不构成历史数据已处理证据。

现有 check_workitem_cutover 的 exit 0 只覆盖前述身份/旧依赖检查，不能证明这些新增冻结上下文与历史启动事件已经核对。未获得专项盘点及处理证据仍阻断环境准入；本轮不执行历史迁移。

## 历史方案提出的准入顺序（当前执行见文首 P/R 入口）

1. 校验环境变更批准和维护窗口；完成备份，并在独立数据库恢复。比较 WorkItem/专业记录/关系/流程/回执/审计的数量、内容摘要、原始时间、关键约束和权限，不能以备份命令成功替代恢复验证。
2. 在相关写入及消费者暂停后重新运行只读预检；保留旧依赖的人工处理记录，不自动迁移或取消。
3. 根据环境账本和保留结构制定可审查的新结构安装步骤。原方案要求保留自动退役阻断；在受控替换方案未批准前，不启动任何旧结构删除。
4. 核对唯一新路径、所有专业 API 的权限/版本/回执和实际调用方；完成 V1 三域及 generic/Requested Item 的完整真实旅程。
5. 恢复新路径写入并观察：三域真实操作、审批与回调、SLA 合法重开及旧周期保留、通知暂时错误重试、同键幂等均通过且无未解释错误。观察时长和样本量由环境变更记录确定，不能把一次隔离测试当生产观察。
6. 再次核对有效备份、恢复记录、暂停范围、消费者清单、V1 和观察证据；在同一迁移权威下执行经批准的精确 RESTRICT 退役操作。该历史阶段仓库尚无该允许删除入口，不能用旧 SQL 绕过。
7. 核对健康检查、账本、完整读写旅程、RLS 与审计，记录实际运行版本和恢复决定。

## 恢复与新写入补偿

删除前：优先恢复已确认的应用/消费者配置；若新 schema 已改变，先核对旧应用可用性，不能只回滚二进制。保留所有失败证据。

删除后：协调恢复数据库、应用版本及连接器/消费者配置。恢复必须包含账本与业务事实，避免旧应用运行在新 schema 或新应用运行在旧 schema。先恢复隔离目标验证，再按环境批准切换。

备份之后的新 WorkItem、专业扩展、关系、审批决定、回调、通知、附件对象及外部副作用必须逐项列入补偿清单。数据库恢复不会撤回已发送通知或外部动作；未捕获的新写入不得宣称零损失。确认补偿责任人和核验方式后才能恢复消费者。

历史豁免持续有效：旧 Problem 不补验证证据、不自动重开；旧 SLA 不重算；旧关系不搬运；旧流程不取消；原 createdAt 和编号不改写。

## 历史隔离证据与当时未完成项

隔离 PG 为既有 disposable 容器的 127.0.0.1:36444/sslvpn_test，每个测试仅创建独立 schema/角色。V1 使用另一组专用临时容器，与共享运行环境分开。备份/恢复工具使用测试容器 PostgreSQL 17，避免宿主 16 客户端与服务端版本不匹配。

真实测试分别验证自动退役拒绝与行摘要/账本不变、规范新 schema 登记、只读切换预检，以及独立数据库备份恢复和备份后新写入补偿缺口。具体结果由执行记录更新；测试产生的备份为临时验证材料，不是任何实际环境的可用恢复备份。

当时未完成：历史迁移受控替换设计及允许删除操作、允许删除后的完整恢复演练、实际环境批准/部署/观察/退役。当前代码和隔离证据已推进至 P/R 与三时点验证；独立审阅及实际环境操作仍不能由历史 C3 或 V2 记录替代。


验证记录：真实 CLI 两场景通过（4.290s），exit2/0 与前后摘要不变；自动退役原行为三场景 RED 均为“期望拒绝但返回成功”，保护实现后全部 GREEN。含真实 dump/restore 的退役验证通过（29.651s），独立恢复数据库与 schema 清理 remaining=0。独立只读审阅确认正常 bootstrap/cmd migrate 均经过 ApplyMigration，检测集合覆盖 022/027 的全部待删表/列；手工直跑历史 SQL 与并发 DDL 仍由维护窗口和操作准入约束，不宣称应用可约束数据库管理员。


补充边界：历史027的表名未限定schema。隔离反例证明，若所选schema缺少规范表，原自动检查会漏掉search_path后续schema中的旧列并执行删除。现在022/027必须先在所选schema找到完整实体表，再检查旧对象；禁止任何search_path回落。反例RED后，规范新schema/旧对象拒绝/跨schema保护三组PG通过（4.351s），历史SQL及checksum保持不变，独立复核通过。

036 隔离证据：原未限定表名的SQL在selected schema缺表时误改独立shadow诱饵表（RED0.041s）；改为捕获current_schema并对关系名使用标识符限定后，缺表拒绝、正常重复执行成功、历史两新字段NULL及诱饵表不变均通过（0.049s）。独立复核通过。NULL schema分支有代码保护，未单独演练该分支；未修改任何旧迁移checksum。

后续设计入口：[受控退役设计](../superpowers/specs/2026-09-11-workitem-controlled-retirement-design.md)。设计经审阅修订，维护者已授权进入[实施计划](../superpowers/plans/2026-09-11-workitem-controlled-retirement.md)。现已实现 P/R 控制入口及三时点隔离验证；Task 6 独立审阅与全计划最终审阅仍待完成，目标环境步骤没有执行。


## 完整恢复与三时点隔离验证入口

[隔离恢复运行器](../../itsm-frontend/tests/e2e/fixtures/README-workitem-convergence.md)提供 `--recovery` 模式。它复用真实生产 CLI 执行 P、普通迁移和 R，并在保留旧结构、R 后、R 前最终备份独立恢复后三个时点执行同一组九项 V1；SKIP 不计为通过。阶段结果及限制必须以本次生成的摘要、二进制/源代码/镜像摘要和外部执行日志为证，不能引用旧版本 V1 或合成 R 报告替代本次恢复。

目标环境执行顺序仍是：单独准入与授权 → P 及普通迁移 → 业务验证及观察期写入 → 停止应用、后台消费者及附件存储写入 → 最终完整恢复点 → 独立恢复演练 → 该环境授权 R → 观察及必要时独立恢复。目标环境必须实际盘点所有写入者、额外消费者、附件后端与真实外部投递，隔离测试无法代替该清单。

完整物理备份须同时覆盖数据库集群角色/权限/序列/WAL/所有业务表及不可变回执、实际附件对象和应用/消费者配置；校验备份 WAL、一致恢复点与同版 PostgreSQL 镜像。恢复目标可采用不同自有物理容器，同时保持原有逻辑部署、库、schema 和角色身份。R 前备份包含原始 P，不应伪造后来发生的 R 回执；在数据库之外保存 R 执行和恢复日志。此方案证明同版物理恢复，不自动证明跨版本逻辑导出恢复。

观察前备份不能代表最终恢复点。最终备份后的新记录、对象写入、通知和外部动作另行捕获并制定补偿/重放清单；数据库恢复不能撤销真实外部效果。缺失捕获或尚未完成补偿时，不得声明覆盖后续写入的零损失。现有独立 RCA SQL 尚未注册的部署缺口及物理清除/可靠事务审计 backlog 保持待处理；核心 V1 通过不表示这些功能已完成部署。


## 2026-09-11 隔离结果与交付状态

Tasks 1–5 已通过各批独立审阅。Task 6 分为顺序执行的恢复运行器（6a）和既有测试准备／文档（6b）；Task 6 独立审阅及全计划最终审阅仍待完成。

6a 的同一组 V1 在保留旧结构、真实 R 后、独立恢复 R 前最终备份后三个阶段分别 9/9 通过，共 27/27，零 SKIP／失败／flaky。两个独立物理恢复目标均与声明恢复点相等；8 个实际缺失内容／附件／配置故障全部拒绝。该证据使用后端 SHA256 `a068429f4265c99c5f767744a78d5a1fc5bc641a4d315cb1ca1cf6dbc1bfa50c`、迁移工具 SHA256 `29bb8eab49958843b7f00431aa6f8cc347d12d7088c7a4be1240376dcf4d1a3b`；6b 的独立重建与两者完全一致，且未修改 V1、生产代码或恢复运行器。6a 自有最终 7 个容器和 7 个匿名卷均已验证不存在；早期探索中未能归属的潜在匿名卷仍披露为限制，未猜测清理。

6b 规定 Go 组 40 个顶层／45 个子测试（132.236s）、额外实际入口组 24／34（37.215s）、回调及受影响 RLS／事件组 27／44（38.535s）均通过，零失败／SKIP；子测试数包含分组节点。最后只读检查确认测试 schema、运行／检查角色和恢复数据库均无剩余，自有 V2 容器保留供最终审阅。

6b 使用显式 V2 配置选择已拥有的独立目标 `127.0.0.1:36542/workitem_v2_task2_test`，每个测试只创建／清理自己的 schema、角色和恢复数据库；原 36444 模式仍严格保留，未启动旧容器。无 dedicated V2 变量时，单改 legacy DSN 指向 V2 会拒绝。当前测试的 P 是真实注册前置与 ApplyPreparation；夹具准入证据不是目标环境的备份或变更授权。

Change 回调旧绑定 `change` 在真实 P 后复现 blocked／handler_contract，子 WorkItem 数量期望 2、实际 1；仅修正为 `change_request` 后成功及确认后撤权场景通过，原数量／权限／重放断言保留。当前 022/027 历史入口均以 require-found 明确选取并实际验证拒绝，不再依赖可能为空的活动目录循环。旧单 schema 逻辑备份测试只证明字段／时间／旧 workflows 保留和后续写入缺口，完整恢复结论仅来自 6a。

以上为代码及隔离验证证据，不能替代实际目标环境准入、授权、维护窗口、写入者／消费者盘点、观察期或退役操作。独立 RCA 部署缺口、物理清除与事务审计、响应及时性及 Change 多 WorkOrder backlog 保持未完成；恢复最终备份后不自动重放后续业务或外部效果。


### Redis 恢复边界（Task 6 审阅修订）

此前 6a 的 27/27 是 PostgreSQL／附件恢复的历史证据，未覆盖独立 Redis，不能单独证明完整恢复。修订运行器完整保存 Redis RDB、stream／consumer group／PEL、刷新令牌消费、access 撤销和身份交换 nonce 标记及其他键；校验绝对 TTL，不因恢复延长有效期。备份停止自有写入者和 Redis，验证 RDB 后恢复到独立目标；PG、MinIO、Redis 均使用实际捕获的不可变源镜像 ID。源 Redis 停止后仍须通过恢复业务和新登录，缺少 Redis 清单或真实 pending 内容必须拒绝。

隔离演练明确采用现有 JWT_SECRET 轮换使旧及恢复点后令牌失效；这是记录摘要的受控配置差异与会话失效策略，不是全配置字节相同。先验证归档配置完整，再验证仅允许的签名 secret 差异及目标连接映射。旧 access／refresh 拒绝、新登录及 refresh 成功、refresh 重放拒绝均须真实验证。身份 provider 通过明确空配置禁用并以实际入口拒绝证明；不声称覆盖已启用 provider 的独立 HMAC 断言恢复，目标环境须另行批准 secret 轮换或最新 nonce 消费合并并验证重放。

生产代码现有 Redis 冷启动失败时 access 撤销可能使用空内存存储的限制仍待单独加固。恢复准入必须确认安全 Redis 健康且应用实际连接该独立目标，不能以启动成功代替验证。后点 Redis stream／ack 和外部效果保留在后点备份及补偿清单，不能自动重放导致重复投递；声明点恢复不表示后续数据或外部动作已零损失。早期探索匿名卷未能归属的潜在残留继续披露，禁止猜删或 prune。

修订隔离运行已重新完成三个阶段各 9/9，共 27/27，零 SKIP／失败／flaky；原 Redis 停止后，独立 Redis 的实际消费者连接、新登录／刷新与旧令牌拒绝均通过。10 个实际内容／配置损坏反例拒绝；独立 Redis／配置专测 7/7。生产二进制哈希保持上述值。最终自有 9 容器／9 匿名卷及 11 端口均核验清理；修订独立审阅与全计划最终审阅仍待完成，不能据此宣布目标环境退役完成。
