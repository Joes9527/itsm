# 旧 ITSM 主数据迁移与当前目标验证：唯一续办清单

- 状态：active；范围由维护者于 2026-09-15 确认。本清单不表示未验证步骤已通过，不是数据库删除执行回执。
- 目标：持续开发新 ITSM，并使获准旧主数据清洗后运行在新模型上；同一新版代码分别连接 Dev 与克隆／隔离验证库，两库结构匹配所选代码，现有入口保持 3010 → 8080。
- 权威约束：[AGENTS.md](../../../AGENTS.md)、[工程治理](../../agent-engineering-governance.md)。本清单维护本轮续办状态；历史设计、报告保留证据，不再各自生成平行待办。
- 取代范围：取代 2026-09-15 环境审计报告中的“下一任务顺序”；不否定其当时的运行/数据库观测，不取代领域合同。

> **当前执行入口：** 维护者已于2026-09-16确认[双数据库恢复设计](../specs/2026-09-16-dev-restoration-two-database-design.md)。后续只按[四阶段执行清单](#two-database-execution)推进；下方历史 proposed 方案和讨论暂停记录保留上下文，不再决定执行顺序。源码提交不等于实际环境交付。

<a id="schema-target-status"></a>

## 当前结构目标与实际状态

**统一目标：[047_bpmn_assignment_source](../../development-environment.md#selected-schema-target-047)。** 2026-09-16执行后状态如下；具体制品与未完成门槛见[四阶段执行证据](#two-database-execution)。

| 对象 | 最后核验结构 | 目标／状态 |
| --- | --- | --- |
| 实际Dev：`itsm_config_baseline_20260908` | 047、39条真实回执 | 已升级，R038未执行；3010→8080已指向Dev，登录成功，readiness200、普通工单创建/评论/转派通过；任务回调推进失败，UI验收未完成 |
| 长期验证克隆（同ITSM数据库、独立schema） | 尚未创建 | 维护者已否决新增PG容器；schema克隆准入及权限隔离待实现，禁止改原P回执 |
| 旧对照库：`itsm_migration_20260914` | 09-16执行前账本最高019、14条 | 不等同当时031 Dev完整克隆；仍保留，待成果及备份门槛满足后清理 |
| 前次8080目标：`itsm_ga_ready` | 046（09-15历史核验） | 所属容器已停止，保留成果；不是当前在线目标 |
| 本次Dev升级预演副本 | 047 | 152张原表字段/数据比对一致；临时恢复容器尚未退出。此前09-15副本已归档移除，与本次副本不同 |

047解决所选代码的结构要求；19条旧流程绑定属于配置兼容问题，两者分别跟踪。038退休不属于恢复Dev的必经步骤。后端、前端、数据库结构和运行配置必须分别核验，不能用其中一个版本号代替全部状态。

## 1. 数据来源与明确范围

旧系统是既有抽取设计记录的 `https://keas-itsm-test.gazellio.com`，不是 WSL 的 PostgreSQL `itsm` 库。本轮核验的是设计、交接与已有抽取证据，未重新登录旧系统或抽取最新数据。

旧系统以只读 API 提供 CTI、字典、优先级、矩阵、路由、模块、日历及流程对照；源文件位于 KAF `data/legacy_itsm/test/`。源设计固定提交 `184f7868`。旧抽取数量是 2026-09-14 快照，不能称为今天实时源数量。

**明确不迁旧 ticket、历史审批、评论、附件和流程实例。** 不复制旧 BPMN；流程只作语义对照。组织/用户复用已迁成果，仅核对身份、租户和引用，不重置密码/角色。首期旧路由不迁，使用人工分派和新规范流程；不开展 MSP 产品扩展，但保留既有隔离约束。

新模型是权威：单一字段/领域所有权、WorkItem recordClass、专业生命周期、现有 BPMN、审计和权限合同均不能为适配旧数据而退回旧结构。

| 数据对象 | 角色 |
| --- | --- |
| 旧系统抽取与 manifest | 迁移源；记录时间、来源和摘要 |
| itsm_config_baseline_20260908 | 当前 Dev 数据库，已前向升级至047并保留升级前恢复基线 |
| itsm | 较早新 ITSM 开发库；不是旧系统本体，不用它的计数代替当前 Dev |
| itsm_migration_20260914 | 历史克隆对照样本；09-16账本最高019，来源不等同升级前031 Dev，不能作为新模型准入证明 |
| itsm_ga_ready | 新模型隔离迁移目标；保全身份数据并填入规范配置，不是 Dev 全量克隆，也不称正式 GA |
| gb_replay_review / ga_acl_rehearsal_20260915 | 恢复、重放及权限验证副本，不是业务环境 |

## 2. 已完成

以下区分历史执行证据与本轮只读观测。历史通过不自动证明当前制品/目标依旧通过。

| ID | 已有成果 | 证据及边界 |
| --- | --- | --- |
| D1 | 旧主数据抽取、完整性及冲突工具 | 主干 G-B 交接记录 10/10 资源校验；旧名称匹配率不是迁移完成率 |
| D2 | 组织/用户身份基础数据及目标结构准备 | G-A 与后续配置切换交接；不重新全量导入 |
| D3 | 配置迁移工具与独立复审 | ITSM 7c8cee6f、KAF e6fd8a50；历史 PG15、Node30、KAF68 测试证据 |
| D4 | 五批重放、事务回滚预演和幂等复跑 | pre-B0 恢复到 gb_replay_review 后完成，本轮只读确认收据计数为5；不能视为当前目标同样有五条收据，更不能给原目标补造历史收据 |
| D5 | 目标类型定向初始化 | 历史执行 12 新增、复跑 0 新增；本轮只读 count 再确认 ticket_types=12 |
| D6 | 有界运行 ACL、检查身份及隔离附属存储准备 | 交接记录具名表/序列、独立 ga_inspection、Redis DB12、独立 MinIO bucket；不是全功能授权通过 |
| D7 | 3010 入口、新主题、运行管理器及源码合并 | PR30 / a59d0516；不等于已部署新主干后端 |

本轮目标只读查询：tickets=3、service_catalogs=8、ticket_types=12；迁移账本 36 条、最高 046。未查询到 config_migration_control schema 的表。以上不证明三个工单走过完整路径，不证明全部配置已正确映射；目标没有重放库收据不能通过补造收据解决。

## 3. 待补配置

| ID | 工作 | 完成条件 |
| --- | --- | --- |
| C1 | **completed：五批最终配置对账** | B0-seed288、B0-process47、B1-config49；B2更新5字段/25选项、B4更新7策略，去重384对象。两库均符合最终预期，源/映射/制品/历史收据链匹配，无需重跑。详见[对账报告](../../review/2026-09-15-five-batch-reconciliation-report.md)；不代表全部旧数据或UI验收通过 |
| C2 | **当前五批范围已核对；范围外保持排除** | 46个旧CTI身份及规范化父子路径匹配；25项获准字典选项落地。没有重裁988项字典、未迁旧路由/历史ticket；新增纳入需求另作明确决定，不把排除项变成五批缺失 |
| C3 | SLA 配置与绑定 | 更正：此前仅检查IS NULL而误称已绑定。执行agent发现并由主agent独立复核：7条中NULL=0、空字符串=7、真正非空=0。两库与原批次写空值一致，属于原计划未完成的有效SLA策略绑定，不是迁移丢失。B4原89假日日历一致，19补班未进入目标。确定覆盖范围后定向配置并验证实际deadline，不能把非NULL当有效关联 |
| C4 | 专业流程配置 | 对当前目标的 Requested Item 不支持动作、Incident 主管审批待决项重新核验；只改获准流程，不全量部署模板、不静默跳过未知任务 |

C3 的 SLA 业务覆盖、C4 的主管审批是否保留，在既有交接中未得到最终决定；执行依赖步骤前只询问尚未确认的具体业务点，不重新询问“不迁 ticket”。

## 4. 运行阻塞与待核项

| ID | 状态 | 下一步与验收条件 |
| --- | --- | --- |
| R1 | readyz 503 已观测；不等于业务全不可用 | 比对当前 health/readiness 调用是否错误使用 runtime/操作员路径；应用准入使用独立同目标 inspection 身份，不给业务身份全局账本权限，不执行038 |
| R2 | 待办：开发与验证版本、结构需要同步 | 选定同一新版前后端及规范迁移要求，分别核验 Dev 与验证库；不通过旧代码恢复 Dev。具体顺序见下方 U1–U4，不能只凭最高迁移号判定兼容 |
| R3 | 切库必须包含所有相关连接与状态 | 核对 runtime/system/inspection、Redis、附件桶、缓存、消费者和外部调用；保持 Dev PG 稳定，原配置可恢复；同一8080一次只服务一个目标 |
| R4 | KAF/worker 停止且部分执行能力有意禁用 | 只影响依赖该能力的验收；人工工单路径不因此自动判失败。需要时明确启用范围，检查实际流程外部调用，不用开全部开关替代诊断 |

## 5. 待验收：先测，再决定功能是否缺失

**“尚无当前目标完整证据”不等于“通用工单功能已证明不完整”。** 不把主干代码观察、历史故障、当前运行版本混成同一结论。

| ID | UI 验收路径 | 判定依据 |
| --- | --- | --- |
| V1 | 登录 → 新建 generic → 详情/刷新 | 记录账号角色、入口、实际字段、预期；数据库持久化、身份和新模型一致，重复提交不重复创建 |
| V2 | 分派 → 处理 → 评论/附件 → 流程动作 | 负责人和权限正确，记录/附件可读，动作与 owning service 一致；不发真实企业通知 |
| V3 | 解决 → 关闭 → 重新打开（按已定义功能范围） | 实测按钮、表单、resolution/reason、状态和审计；无入口则记功能缺口，接口拒绝则定位契约，不用数据库直接改状态通过验收 |
| V4 | 切角色及刷新/重启 | 越权被拒、刷新不丢数据、会话行为正确；源码/制品/目标固定；不以 API 测试代替浏览器操作 |
| V5 | Incident / Requested Item / SLA | 各自单独验收；generic通过不代表专业领域通过，任务完成不等于专业解决 |

每项记 PASS / FAIL / BLOCKED / NOT RUN、准确制品/目标、测试记录标识与证据。新建验收记录单列，不自动纳入正式迁移集；首次失败先定位一个具体问题，不扩大到全产品重构。

## 6. 数据库收敛与清理任务

清理是必要工作，不无限期保留所有副本。但当前同意的是形成清单与核验，未在本批执行任何删除。

| 分类 | 对象 | 建议 |
| --- | --- | --- |
| 必须保留 | 旧抽取源及manifest；实际Dev库；当前itsm_ga_ready；当前有消费者的KAF/Langfuse数据 | 保留明确用途、owner及恢复材料；不因名称ga/dev判断可删除 |
| 优先评估清理 | 无数据的3个历史tmpfs容器；无使用者的空测试库 | 确认无活跃任务/脚本引用后列首批准确对象；不使用全局prune |
| 验证完可归档删除 | restore_verify、ACL演练、gb_replay_review及其它review/test库 | 最终证据归档，核对最新备份覆盖与恢复成功，再列删除窗口；可按需重建，不长期当环境保留 |
| 待逐项判定 | 旧baseline、migration样本、candidate/handoff六个停止持久卷集群、旧ACP库 | 核对独有配置、未合并任务依赖、未决状态、备份覆盖；停止不代表废弃，不能按整个PG容器批量删仍需保留的逻辑库 |

清理清单必须逐对象记录：容器/库/卷、消费者、唯一数据、备份时间/哈希/覆盖范围、恢复验证位置和结果、证据保留、删除顺序。备份文件存在、能列出归档目录、历史恢复成功是三个不同证明；仅一份旧快照不能覆盖之后的新增配置。

执行时优先释放废弃测试库/容器；同实例存在Dev时绝不做down -v或删除整个实例。备份验收合格并形成准确删除清单后按已确认范围执行；不得把“不迁ticket”解释成删除源库权限。PG物理合并继续后置，不是完成清理的前提。

本轮只读确认两份备份文件存在：pre-B0（2026-09-14 19:59 CST，1362925字节）与before-switch（2026-09-15 09:31 CST，1415258字节）。历史交接有恢复验证记录；本轮仅核文件元数据，没有重做恢复，也未证明覆盖后续变更。

## 7. 历史执行顺序（已被文末开发恢复更新取代）

1. C1已完成：不重跑五批。清理对象表仍按备份覆盖/依赖继续准备，不因对账完成自动删除。
2. R1–R3：完成当前候选的有界运行核验；不先升级全部main。
3. V1–V3：先跑generic真实UI，记录第一个确定的失败点；只修复被证实的问题。
4. C3/C4、V4/V5：补齐获准配置并分别完成专业流程、SLA和恢复验收。
5. 满足备份/依赖证据的废弃库分批清理，可与不依赖这些库的验收准备并行；独占数据库写入者。
6. 更新本清单结论与剩余项，不复制新一轮G-A/G-B待办；最后才讨论新功能升级或PG实例合并。

## 8. 证据入口

- [配置迁移设计](../specs/2026-09-14-legacy-config-migration-gap-and-solution-design.md)：原始映射原则，后续范围以维护者决定及本清单为准。
- [原G-B交接](../../review/2026-09-14-workitem-config-migration-handoff.md)：历史BLOCKED，不用它覆盖后续已执行成果。
- [环境审计](../../review/2026-09-15-wsl-environment-and-next-task-audit.md)：2026-09-15只读快照。
- WSL `/home/administrator/project/itsm/.worktrees/config-launch-integration/HANDOFF.md`，10:18冻结；及同树 `docs/review/2026-09-14-config-launch-closure.md`：后续批次、范围裁定、已执行ACL及未验收项。其“未切换/3001”为历史状态，不能覆盖后来3010运行证据。
- KAF `184f7868:docs/superpowers/specs/2026-09-14-legacy-itsm-master-data-extraction-and-diff-design.md`：旧源系统身份和抽取边界。

历史/私有证据保留原时间与提交，不复制凭据、原始用户数据或dump入Git。状态变更只在本清单对应ID更新；具体执行证据以受保护目录或独立测试资产索引引用。

## 9. C1 执行任务书：五批四方只读对账

- 状态：completed（2026-09-15）；维护者批准只读对账，执行agent完成，主agent已复核。下述任务书保留检查方法；结果见C1及对账报告。
- 单一目标：对照原始来源、五批定义/执行证据、gb_replay_review、当前itsm_ga_ready；每批给出可复核差异。没有证据时标记不能判定，不自行推定迁移成功或缺失。
- 不执行：数据/DDL/授权写入、迁移CLI、重放/回滚、恢复备份、清理、切库、重启、旧源重新抽取、ticket内容读取、UI业务写入。PG物理合并及047升级不属于本任务。

### 输入及访问

- 先读两仓库AGENTS.md及ITSM工程治理。保留所有既有worktree、未提交文件和后台任务。
- SSH：`ssh -p 22222 administrator@192.168.31.66`；普通读取使用administrator，确需私有证据/容器查询才用sudo -n。不输出完整env、DSN、凭据、用户记录或原始业务数据。
- 历史执行入口：`/home/administrator/project/itsm/.worktrees/config-launch-integration/HANDOFF.md`、同树`docs/review/2026-09-14-config-launch-closure.md`。
- 工具证据入口：`/home/administrator/.local/state/itsm-task2-remediation-20260914/`、`/home/administrator/.local/state/itsm-task2-b0-20260914/`、`/home/administrator/.local/state/itsm-backend-switch-20260915/`；先列文件名/大小，按相关性读取，不递归输出私有目录全部内容，不读巨型运行日志。
- 来源：KAF `/home/administrator/project/kaf/data/legacy_itsm/test/`；工具ITSM `7c8cee6f`、KAF `e6fd8a50`，用git show定位准确代码，不checkout/重跑写入工具。
- 重放库：`gb-remediation-test-pg-20260914 / gb_replay_review`，已有config_migration_control.receipts五条；目标：`ga-itsm-20260914 / itsm_ga_ready`。不得因目标没有相同收据而补造。

### 步骤与检查标准

1. **固定证据窗口。** 记录UTC时间、容器/数据库/schema/版本、代码/manifest摘要；检查当前连接的聚合信息。用显式`BEGIN TRANSACTION ISOLATION LEVEL REPEATABLE READ READ ONLY`、`SET LOCAL statement_timeout='30s'`、`SET LOCAL lock_timeout='2s'`执行SELECT；大型聚合分批，不禁用超时。不能运行会EnsureMigrationsTable的-status/-dry-run。只读会话结束正常COMMIT或ROLLBACK，不跨工具调用悬挂事务。
2. **从实际定义识别五批。** 提取每批ID、依赖、对象集合、源manifest/摘要、映射修订/摘要、代码版本、纳入/排除和预期变换。批次名称必须来自文件/收据，不能编造B0-B4语义。区分新规范seed与旧数据映射，不能把seed初始化都声称旧数据迁移。
3. **验证重放证据。** 对照五条收据的batch_id/context/source_sha256/mapping_sha256/object_set/post_state_sha256与实际定义及历史执行结果。只读复用摘要规范；不重新实现另一套摘要算法。若旧算法存在错误，只记录证据差额，不改数据或收据。区分批次即时摘要与后续批次合法覆盖，不能将最终库直接比对每批中间态后误判漂移。
4. **逐对象比目标。** 使用真实稳定业务键、tenant与映射进行匹配，数值ID仅用于库内引用；对分类层级/字段归属/优先级/SLA/目录/流程绑定按实际五批对象检查字段与引用。JSON规范化按原契约，不能随意忽略字段。名称相似不自动合并，缺映射明确列出。
5. **解释差额。** 将结果归为一致、后续获准变更、缺失、冲突、无法判定；合理差异须有代码/变更证据。额外目标对象不能自动删除。基础身份只检查聚合、稳定映射和关联完整性，不导出身份内容。当前三个验收工单不用于旧数据导入对账。
6. **检查观察期间变化。** 在目标重复读取相同受检对象的摘要/计数；若变化，记录窗口及受影响批次，不能把并发变化定性迁移错误。两库不是原子跨库快照，明确各自采样时刻与限制。
7. **交付。** 生成一份脱敏结果文件，逐批列应迁对象数量/匹配/缺失/额外/冲突/排除；无法取得分母则明确未知。每个差异附稳定对象标识或脱敏标识、规则、预期与实际差异、来源路径/提交及后续建议。禁止以总数相等或5条收据替代对象级检查。

### 交付边界与完成条件

- 执行agent只写本地临时脱敏报告和必要只读查询脚本，不修改仓库业务代码、清单、数据库、远端或环境配置。原始源/映射/配置内容只在WSL受保护目录中处理，不拷贝PII至Mac/tmp/Git。
- 输出位置：`/tmp/itsm-five-batch-reconciliation-20260915.md`；如需机器可读脱敏差异，使用同名前缀.json。不得把完整context/原始JSON直接写入报告。
- 报告包含：五批矩阵、执行时刻及指纹、检查方法、每批差异、证据路径、限制、按优先级的精确下一动作，以及未做任何数据库变更的声明。
- 主agent复核关键摘要/查询及至少一个一致和一个差异例（如有），再更新本清单C1及相关C/R/V状态。没有独立核验的历史结论不升级为当前PASS。
- 一个批次或证据链超过合理查找范围时提交准确缺口；最多60分钟交付本轮结果，不无限追查不影响五批的其他库或历史任务。


## 10. 2026-09-15 当前候选 UI 验证与定向修复任务

本节为本清单最新增量，取代上文 V1–V3 的未执行状态；不改写五批迁移对账结论。主 agent 执行真实浏览器，readiness_path_audit 执行独立只读代码/目标配置核验。未部署、切库、迁移、seed、扩权或删除数据库。

### 固定测试对象

- API：c3c880df，8080；前端：24fe8546f9c0d40778e25f276f6307907ab19318，3010，Build ID fJAS7iqLTOgz4cevkG_pZ。
- 目标：itsm_ga_ready；runtime/system/inspection 均指向隔离目标。Redis DB12、独立附件桶 itsm-ga-e2e-20260915；Dev 保留，后台消费者仍按原范围禁用。
- LAN HTTP 登录：HTTP 200 后 auth/me 401。运行 ENV=production 令 access/refresh cookie 带 Secure，浏览器在 LAN HTTP 不保留。通过本机 SSH 转发 localhost:3010 访问同一 WSL 前端后登录成功，用于继续诊断；该转发不是 LAN 登录问题的修复。
- 测试账号：tenant=default 的既有 admin / super_admin。菜单为空，创建使用直接页面地址；没有把直接地址访问计作菜单导航通过。
- 唯一新建验收记录：ID 8，TKT-202609-000004，标题“迁移验收-GENERIC-20260915-1605”，recordClass=generic。创建后只读确认 status=new、version=1、自动 assignee_id=2。它是合成验收记录，不是旧票据迁移。

### 实际结果（16:05–16:14 CST）

| 路径 | 状态 | 证据与边界 |
| --- | --- | --- |
| V1 创建、详情、刷新 | 部分 PASS | UI POST 创建201/code0，详情与多次整页刷新保持；LAN 登录 FAIL、菜单导航 BLOCKED、重复提交未测，故 V1 整项未通过 |
| V2 评论 | PASS | UI 发布1条合成评论，POST200；整页刷新后仍可读 |
| V2 附件上传/下载 | PASS | UI上传合成73字节文本，POST200；列表显示1附件并通过下载按钮取回，两文件SHA256均 dfad74271555543bec60aae386c1e54e268a642b9680ebc46769e73e7f312b12 |
| V2 分派 | BLOCKED | 只加载首100用户后本地搜索；admin搜索无结果，未随意分派给真实人员、未提交分派请求 |
| V2 开始处理 | FAIL | 编辑下拉可选“处理中”，保存提示“不允许从新建转换到处理中”，前端拦截，未发更新请求 |
| V2 流程任务 | NOT ACCEPTED | 当前管理员显示无可见活动任务；不能据此证明流程不存在或完成，未启动停用消费者 |
| V3 解决/关闭/重新打开 | BLOCKED | 在开始处理失败后停止依赖步骤；未用API或数据库绕过UI |
| V4/V5 | NOT RUN | 未切角色、重启或执行专业流程/SLA验收 |

浏览器证据保存在本地忽略目录 output/playwright/.playwright-cli/，主要快照 page-2026-09-15T08-12-32-197Z.yml（状态拒绝）、page-2026-09-15T08-13-34-191Z.yml（附件成功）、page-2026-09-15T08-13-50-266Z.yml（下载）；临时产物不提交。附件内容为合成英文验证文本，无企业数据。

### 已证实缺口和下一批执行顺序

| ID | 根因与任务 | 完成条件 |
| --- | --- | --- |
| R5 | LAN HTTP 与 Secure session cookie 不匹配。authentication/cookie.go 的生产环境策略生效，Auth.CookieSecure 字段未被消费；不可直接改 ENV=development 掩盖其它启动约束 | 设计明确传输配置，生产默认安全、HTTPS始终Secure，统一登录/刷新/退出及OAuth state属性策略；真实 LAN 3010 登录和刷新通过，不泄露令牌、不退回localStorage |
| C5 | 目标 menus 总数=0，菜单API200空树。menu_service.go:232查询租户可见菜单；menu_controller.go:250–274 的init只返回提示，不创建菜单 | 从现有新模型菜单定义生成租户明确、permissionCode及路由一致的最小配置变更；审查后定向应用，禁止全量seed或静态菜单绕过权限 |
| F1 | 部署 TicketDetail.tsx:202仅取100人，:899–909本地filterOption无服务端搜索。目标租户7879用户，admin存在但首100匹配0；后端 user_service.go:149–170已支持搜索分页 | 接入既有服务端search、防抖/迟到响应保护、保留选中值及权限租户边界；证明首100之外的admin可选，不全量拉人 |
| F2 | 部署 workflow-state-machine.ts:13仅允许new→open/cancelled；TicketDetail.tsx:340–346提前拒绝。但实际c3 UpdateTicket :514–520调用repository/ticket/model.go:81–83，允许new→in_progress | 移除过期前端权威阻断，让既有版本化命令由后端裁定；保留expectedVersion/operationId、后端错误、冲突刷新及专业命令边界；验证合法提交和非法拒绝，再续测V2/V3 |
| R1细化 | readyz将业务RawDB用于账本检查且选最后注册038；启动迁移检查已有独立inspection连接并排除retire。另有平台scope0初始化六组件收据缺失，与五批配置不能等同 | 修复检查身份与阶段选择后，独立报告真实baseline状态；不GRANT业务账本权限、不执行038、不补造初始化收据或强行返回200 |

执行安排：先分别实现/评审 F1、F2 的小范围代码变更，同时准备 C5 定向配置与 R5 传输方案；固定候选组合并记录部署来源后复测。R1 的baseline合同另行明确，不以重跑五批或整套初始化替代。C3 SLA空绑定和19补班仍待补，五批384对象对账通过不覆盖菜单和端到端可用性。


## 11. 已批准修复的执行约束（2026-09-15）

维护者对§10下一步回复go ahead，按现有方案执行，不重新扩展迁移范围。

- [x] F1/F2：generic_ui_fixes 在独立分支统一修改 TicketDetail 人员搜索和过期状态阻断；先失败回归、再实现、再最小测试/类型检查。返回提交供独立复审。
- [x] R5：cookie_transport_fix 在独立分支实现可区分未指定/显式值的Cookie传输策略，保留HTTPS安全优先和默认生产安全，验证session/refresh/logout/OAuth/CSRF一致性；不修改ENV以绕过启动约束。
- [x] C5：menu_target_plan 只读检查新模型菜单来源与租户/权限/路由，准备具名对象定向事务与回滚；主agent审核后唯一写入。不是全量初始化，不能以菜单数据补齐推断RBAC或baseline已通过。
- [x] 集成：独立复核各提交；以实际前端24fe8546、后端c3c880df构造仅本批必要变化的候选，不部署主干的047或其它迁移。保存旧制品、recipe与配置哈希，预构建完成后分别短暂停启itsm/itsm-web；其它服务保持原状态。
- [ ] 验收：首先LAN HTTP 3010真实登录/刷新/退出；随后菜单、搜索首100外admin、generic处理/评论/附件/解决关闭。只使用合成验收记录，失败如实记录，不用DB改状态替代UI。

共享变更仅由主agent执行：目标itsm_ga_ready，保持Dev PG、Redis DB11不变。菜单定向写入前核验现态与备份，限定tenant和具名对象；运行切换失败恢复旧recipe/制品，菜单回滚只移除本次插入且未被其它对象引用的记录。源码提交、构建来源、部署制品和UI验收分别记录。


## 12. 修复执行证据（2026-09-15，进行中）

### 已执行与实际候选

- 主干基线a59d0516a；独立实现提交：F1/F2 dabab849b94f7e451acf0b7a81cde90ce33f06a3，Cookie 3e64fb4ba627a5e0900da2a0da07a50d88bae5f9。menu_target_plan独立复审源码及实际WSL窄移植，无P0/P1/P2。
- 实际API由c3c880df窄移植为b226142642b6bf3289102cb2548345e7369675f3；五个受影响Go包测试及二进制构建通过。ENV仍production，仅私有启动环境增加ITSM_COOKIE_SECURE=false；server.cookie_secure为实际既有字段（更正此前Auth.CookieSecure描述）。
- 实际前端先由24fe8546窄移植为5a71ae3316eb478ed4fc1643e0d107f93d07d846，Build ID qS3HboSDPngQi-Wq9cvHE；全新锁文件安装，26组件测试、类型检查、生产构建通过。未带入SLA或047代码。
- LAN登录后access/refresh/CSRF cookie均HttpOnly，HTTP明确配置下Secure=false；浏览器进入管理页、整页刷新保留登录、UI退出HTTP200且两枚会话cookie清除，再登录成功。无需localhost隧道。
- 从真实“工单管理”菜单进入ID8，搜索admin出现首100之外账号，UI分派成功且刷新后处理人为admin；该操作将new变为open，版本升2。未向真实业务处理人派单。

### C5定向菜单配置 completed

仅tenant1新增6个菜单：/dashboard、/tickets、/service-catalog、/approvals、/admin/ticket-categories、/admin/service-catalogs。沿用新模型规范权限与既有service→service_catalog权限别名；无角色/授权变更。主agent唯一写入，原配置设计与SQL经不同agent复审，修复JSON缺字段、回执作用域、跨actor、跨租户依赖及重放检查后执行。

- 前置完整备份：WSL私有目录 ~/.local/state/itsm-migration-ui-fixes-20260915/before-menu-and-cookie-20260915.dump，1491911字节，SHA256 185bb641db5f9dd06a5147925445fff7e23a123e43d60c981da800e66ba769d6。archive list通过，本轮未做全量恢复。
- 首次写入6条，第二次执行仍6条；审计ID50、operation_id=menu-core-tenant1-20260915-c5。用户/角色/权限/工单计数与身份、权限摘要前后一致。
- 同目录保存6项manifest、事务SQL、精确回滚SQL和原recipe/config，权限0600；回滚仅允许原回执ID/全列未变且无任何租户依赖的菜单，不删除审计，不重置序列。
- UI显示4主导航+2管理入口；页面可见不是对应全部业务功能验收。

### R6新增：重启暴露原有权限漂移，已恢复

16:29新候选和回退旧c3二进制均因execution runtime admission失败退出，API短暂停止；恢复后PID2341946。不是Cookie失败：变更前备份已明确保存ga_runtime对execution_tool_invocations的SELECT/INSERT/UPDATE/DELETE授权。

c3 ValidateExecutionRuntime要求该表只读，041迁移由ga_owner SECURITY DEFINER触发器登记。独立审查核实目标函数与触发器实际一致后，仅REVOKE INSERT/UPDATE/DELETE，保留SELECT与原trigger。有效表/列权限、直接函数权限检查通过；追加operation_id=runtime-scope-readonly-20260915审计。未执行迁移、未扩权或关闭检查。不能恢复该错误写权限来“回滚”代码；旧c3同样不能以该权限启动。

### R7新增：LAN HTTP操作编号

F2过期状态判断已删除，但LAN浏览器实际报告crypto.randomUUID不可用，编辑请求未发出；创建入口随后复现同根因，也未发POST、未创建第二条验收记录。追加修复由generic_ui_fixes实现，使用既有getRandomValues加密随机，不取消幂等键、不用弱随机。add1d7d57为第一版编辑修复，WSL ffedb688经独立复审与84相关测试；创建和编辑统一入口版本正在完成，不把中间修复宣称完整验收。

### R8待办：通知读取租户上下文

重启后的页面GET notifications返回500，错误为“rls: no tenant_id in context and system bypass not set”。本轮尚未修复，不通过扩权/system bypass掩盖。该报错与菜单是否可见、F1搜索结果和Cookie是否保留分开记录。


R7最终代码：f994a93e937c1d857d53fcef1d47c5150ea04a79，将创建/编辑收敛到既有sessionSecurity.generateOperationId；仅使用randomUUID或128位getRandomValues，无加密能力时明确拒绝。独立复审通过。WSL最终候选c642bbf441faf29d8ea3ac90628cc6d54844f9e1，99项组件/API/创建hook测试通过，类型检查通过，生产构建执行中。前端尚未更新到此最终候选，等待构建后真实LAN复测；不以测试通过代替部署结果。


### 版本证据的进一步更正

旧API文件名/recipe标c3c880df，其内嵌VCS为66008779 modified=true。本轮在干净b226工作树复现同样的父仓库标记，说明Go在嵌套worktree的自动VCS探测会误取父仓库；不能据此单独断言旧binary实际源码是660或私有改动。显式GIT_DIR/GIT_WORK_TREE构建后，内嵌b226且modified=false。该操作已写入Development Guide；最终发布使用准确嵌入来源的构建。

R8进一步定位：c3与660通知代码一致，Gin Context不转发typed tenant key；Request.Context中租户未被Cookie策略删除。已找到现成窄修复f3844a19569a8e51637be2f56def6978ff1eb4e7，只将8处通知持久化调用改为Request.Context，准备复用并回归测试。尽管不是Cookie逻辑因果关系，通知500仍按本次部署后观察到的运行回归处理，不能忽略。

R9新增：ID9真实LAN创建201，new→in_progress更新200/version2。解决操作请求已发送但返回500/code5001“解决工单时必须填写解决方案”；编辑UI缺输入项已证实。dde9c46898704a3d9feedad65e3f7eca42051635仅为generic新增/回显解决方案并做required提示，不修改后端规则或专业生命周期；独立及WSL 33组件tests均通过，构建后续测。


### 17:08 最终候选实测与余项更新（取代上方构建中/尚未部署描述）

API已部署71bcb0bae2d81d9908a36d88cc79feef09ccea9e（Cookie+通知上下文窄修复）；内嵌VCS相同、modified=false，二进制SHA256 b50e4364673709afa48832d7e0cd26902080dc908b8c2976bd1988bfd80aea24。前端已部署f1eda552ce5a3de3cc5323e02f4bc6b26129afdf（人员搜索、状态提交、HTTP操作编号、解决方案），Build ID S3pYCsKOf4K1LI-9qlAiA。最终33组件tests、类型检查及生产构建通过，之前统一操作编号99项回归通过。通知实现56ffd35d0与解决方案dde9c468经独立复审。

- R7已验收：LAN创建ID9 HTTP201；new→in_progress HTTP200/version2。
- R8通知列表读取已验收：刷新真实3010页面观察GET /api/v1/notifications HTTP200。
- R9解决方案已验收：填写合成解决方案并提交HTTP200；刷新显示已解决，目标数据库只读核实ID9 resolved/version3。
- 前端旧进程2370235两次SIGTERM仍残留且3010已关闭；对照管理器PID/start_ticks/cwd身份，在生命周期锁内仅结束该残留进程后正常启动新制品。当前前后端运行，KAF和两worker仍停止。
- Dev只读复核itsm_config_baseline_20260908：tickets26、menus71、users7869，保持变更前数量。目标menus6；ID8 open/version2，ID9 resolved/version3。

新增收口项：

1. 实时通知WS仍用旧默认localhost:8090。与通知列表200区分；使用构建时NEXT_PUBLIC_WS_URL指向LAN3010的/api/v1/ws/notifications，3010既有代理转8080，并限定后端允许该LAN Origin。正在构建验证。
2. resolved后actions.edit=false由后端终态规则产生，不是单纯前端旧判断。批准独立关闭按钮及actions.close能力投影，复用版本化UpdateTicket，仅generic resolved→closed纯状态命令；不开放普通编辑、不改变专业生命周期、不使用旧无可靠幂等关闭端点。实现与独立复审中，V2关闭仍未通过。

R1 readyz、C3七条SLA空字符串绑定/19补班、C4专业流程配置与消费者停用边界保持原裁定。此次没有重跑五批、seed或迁移，没有清库，没有迁移旧ticket。源码尚未推送或合并本轮修复。

端口17:12复核：3010 ITSM Next、8080 ITSM API；3000由acp-langfuse容器占用，是Langfuse，不是第二个ITSM；3001及8090无监听。不要为“统一ITSM端口”停止另有用途的Langfuse。

### 实时通知端口收口完成

WS纯配置经独立复审：构建时NEXT_PUBLIC_WS_URL=ws://192.168.31.66:3010/api/v1/ws/notifications，ITSM_BACKEND_URL=http://127.0.0.1:8080；后端仅新增WEBSOCKET_ALLOWED_ORIGINS=http://192.168.31.66:3010，无通配来源。相同f1eda552源码重新构建，Build ID Ic9uWpAfDlAOyWQpwA25S。正常停止/启动完成后，真实LAN浏览器刷新，观察连接路径3010/api/v1/ws/notifications且握手101；未将短期票据写入本清单。通知列表HTTP200和实时WS101分别通过。

### 通用工单关闭路径候选

实现3ff095360d6fff1c7c6867ae64d159377fbc17ab：后端actions.close仅generic/resolved/当前租户ticket:update，UpdateTicket仅纯status=closed命令豁免终态编辑拦截；CanEdit和全局终态规则不变。前端独立确认按钮使用既有expectedVersion/operationId命令，不接旧非幂等close接口。已独立审查，无未决P0/P1/P2；切换工单疑虑经现有session/tenant/ticket key wrapper及rerender回归证实不成立，撤回该误报。

实际WSL窄移植：API3ea58ca774fb07347dc6ad25ff0b3683bfc29a58；前端c950b60e5a36a7fcb10e593403468032d76b8e8f。仅import与相邻ref冲突，保留原基线；旧AssigneeID为int，测试夹带负责人改用非零actor ID，不迁入新DTO/047。WSL后端14场景、前端37组件tests、typecheck通过；最初npm test默认对单文件计算全仓80%覆盖率导致门禁失败，随后按已声明的组件范围运行无全仓覆盖收集的targeted Jest通过，未声称全仓覆盖率达标。最终生产构建中。

### 最终运行与UI验收收口（2026-09-15 17:22 CST）

本节取代上方“关闭未通过/构建中”等中间状态，保留原证据用于追溯。主agent负责设计、共享变更及真实UI验收；generic_ui_fixes/cookie_transport_fix分别实现，menu_target_plan独立复核代码，菜单SQL由另一agent交叉复核。

| 项目 | 最终结果 |
| --- | --- |
| API8080 | 3ea58ca774fb07347dc6ad25ff0b3683bfc29a58，PID2480484；构建内嵌同提交且modified=false；SHA256 53645b7fc69c8aef43eef722f7f2d68d0cd5c3a05dcddeb5e93cfb5368630223 |
| UI3010 | c950b60e5a36a7fcb10e593403468032d76b8e8f，PID2483592；Build ID D8aFX3Fwxo4VgBxJnUliJ；独立发布目录itsm-web-c950b60e-D8aFX3Fwxo4VgBxJnUliJ |
| 版本核验 | stack status核对无源码/制品漂移；两侧生产构建通过；窄移植最终SHA经独立复审 |
| 关闭实际路径 | LAN /tickets/9 → 关闭工单 → 确认关闭 → PUT200/code0；刷新已关闭，关闭按钮不再提供 |
| 持久化 | ID9 closed/version4；解决方案保持原合成说明，resolved_at=09:04:24Z、closed_at=09:22:06Z，解决时间未被覆盖 |
| 通知 | 最终制品刷新GET notifications200；WebSocket通过3010/api/v1/ws/notifications握手101 |
| 其它服务 | KAF、KAF web、worker1/2仍停用；没有新启消费者或外部服务调用 |

本次可关闭项：C5菜单、F1服务端人员搜索、F2后端状态裁定、R5 LAN Cookie、R6既有执行表错误写权限、R7 HTTP操作编号、R8通知租户上下文，以及R9解决方案输入/通用关闭入口、通知WS端口配置。验收记录ID8、ID9均为新建合成记录，不属于旧ticket迁移；保留用于追溯。

V1登录/创建/详情刷新已通过；V2分派/处理中/评论/附件读写已通过（附件前期localhost隧道，其它已在LAN直接验证）；V3解决→关闭已通过。未声称重新打开、跨角色全流程、Incident/Requested Item/SLA/BPMN端到端验收通过。V4只验证当前管理员会话刷新/退出/重启；权限负例来自受影响单元回归，不代替真实跨角色验收。

仍按唯一清单继续：R1 readiness身份/阶段选择与真实初始化收据；C3七个SLA空绑定及19补班配置；C4专业流程；V4跨角色与V5专业域验收。数据库清理仍待准确对象和恢复证据核验，不因有备份名称直接删除。五批384对象对账已完成，无需重跑；Dev PG未变更，未导入旧工单，未运行seed/迁移。本批源码已提交到独立分支和WSL窄候选，但尚未推送/合并；此前PR30与本批不同。

最终回滚材料仍在WSL私有目录itsm-migration-ui-fixes-20260915：before-close-itsm/itsm-web启动描述、此前制品、菜单备份/精确回滚及权限审计。回退前核对当前服务身份与依赖；不能恢复R6错误DML权限，否则旧API同样启动失败。


<a id="development-restoration-update"></a>

## 开发恢复与迁移验证约定更新（2026-09-15）

**维护者已确认目标；本次只更新文档。** 长期规则统一维护在[开发环境合同](../../development-environment.md#development-and-migration-validation-database-contract)，本节只维护状态和任务。开发使用 Dev，迁移验证使用克隆／隔离库；两者共同推进新版代码与匹配结构。禁止为了切库长期恢复旧代码，迁移验收未完成不能成为长期占用日常开发入口的理由。

### 已知状态与证据边界

- 前次切库只读预检：`itsm_config_baseline_20260908` 的迁移记录最高为 `031_kaf_action_request_digest`，未发现执行域表；所检查新版后端启动需要这些结构及受限角色绑定。结论是 Dev 与该后端不兼容，不能直接改连接后启动；不是要求恢复旧版后端。
- 该次预检停止在切换前，未修改 Dev 结构或数据。前次运行检查中 8080 指向 `itsm_ga_ready`，Redis DB12 和独立附件桶；Dev 配套为 Redis DB11。**这是此前观测，本次文档修改没有重新核查实时运行配置。** 执行 U1 时必须刷新事实，不能直接恢复旧快照中的端口、角色或制品。
- `itsm_ga_ready` 是隔离新模型目标，`itsm_migration_20260914` 是 Dev 克隆对照样本；两者不应混称全量克隆，更不称正式 GA。数据库详细用途继续以第 1 节为入口。
- 源码检索确认 Toolkit v1 位于 `codex/feat/config-launch-integration`，提交 `5f9877772d5ff6fbca80c4e02871bb4aa86d692a`。本次刷新远端后，`origin/main` 为 `ad1296472`，尚未包含该工具提交。参见固定提交的[运行手册](https://github.com/Joes9527/itsm/blob/5f9877772d5ff6fbca80c4e02871bb4aa86d692a/docs/migrations/runbook-data-migration-validation.md)和[已接受设计](https://github.com/Joes9527/itsm/blob/5f9877772d5ff6fbca80c4e02871bb4aa86d692a/docs/superpowers/specs/2026-09-15-migration-validation-toolkit-design.md)。合并后改用主干内相对链接，不复制工具实现。
- 工具入口为 `python3 -m scripts.migration`；v1 支持 users/departments 验证、映射与差异分析，数据库读取使用只读事务；补建通过产品 API，仅 `create_missing`，默认 dry-run，实际写入要求显式启用。它不负责结构升级，也不替代五批配置迁移。本次未运行工具或验证其在线写入能力；profile 中历史 DEV 标签需与准确库名和来源核对。

### 唯一续办顺序

| ID | 分类／状态 | 工作与完成条件 |
| --- | --- | --- |
| U1 | 运行阻塞／待核验 | 刷新 3010/8080 进程、完整连接配置与实际 Dev/验证库结构。选定共同使用的新版前后端提交和制品，列出 Dev 从 031 到该版本要求的精确规范迁移、准备依赖、角色绑定与验收条件；最高序号不能替代逐项核验。禁止旧代码恢复、Ent 补表或放宽准入。 |
| U2 | 运行阻塞／待方案与执行 | 在任何 Dev 写入前核验备份覆盖和隔离恢复，完成所选升级链的预演、风险及保留数据的恢复／修复方案；明确 Dev 结构变更范围并按获准范围执行。结构准备、业务验收和破坏性退休分开，不能因切库请求自动运行全部迁移或删除历史结构。 |
| U3 | 待验收／依赖 U2 | 用同一新版制品和完整 Dev profile 恢复 3010 → 8080 开发入口；验证真实数据库身份、readiness、正常登录及代表性 UI 路径，保存配置与恢复证据。此项不等待 C3/C4/V4/V5 全部完成。 |
| U4 | 待核验／可与 U1 的只读工作并行 | 核对已提交 Toolkit v1 与当前产品 API、身份/租户、源 manifest 及准确目标配置的兼容性，完成评审与主干集成；复用现有工具，只补确证缺口。只读验证和 dry-run 先行，实际补建另按具体对象与已获准范围执行。 |
| C1 | 已完成／保留 | 五批去重 384 对象对账已完成，不重跑，不补造目标历史收据；用户/组织工具结果与配置五批结果分别记录。 |
| C3/C4 | 待补配置／延续 | 7 个 SLA 空字符串绑定、19 个补班配置及专业流程继续按原范围推进；尚未决定的业务覆盖单独明确。 |
| R1、V4/V5 | 运行阻塞／待验收 | 原验证库 readiness 问题及跨角色、专业域验收继续保留；R1 的检查身份问题与 U1 的 Dev 缺表是不同故障。在约定窗口切入验证 profile，用同一发布版本完成迁移验证，结束后恢复 Dev。generic 历史通过不替代这些验收。 |

数据库清理仍沿用第 6 节的具名对象、消费者和恢复证据要求；不在本次文档更新或 Dev 恢复中顺带清库。不新增并行任务清单，不重复已完成五批工作。


### U1/U2 执行证据：Dev 恢复预检与隔离预演（2026-09-15）

开发／验证合同 PR37 已合并（`b4c061247`）。本轮源码审阅基线为其父业务基线 `ad1296472`，运行核验与源码状态分开：

| 对象 | 本轮实际核验 |
| --- | --- |
| 3010 | 前端提交 `0bfad4bd731ea9284b48c3db60ac9bc8440eeb82`，Build ID `_Y0pYHLNnccBvRBeX8DGG` |
| 8080 | 后端 `3ea58ca774fb07347dc6ad25ff0b3683bfc29a58`，目标仍为 `itsm_ga_ready`；runtime/system/inspection 完整配置在切换前仍须复核，当前 inspection 同库，Redis DB12；readiness 503 |
| Dev | `itsm-postgres-dev` / `itsm_config_baseline_20260908`，PostgreSQL 17.10；24 条真实账本记录，最高031，022/027已执行，执行域表0，工单26 |
| Dev 流程 | running6：旧 `ticket` 4、`incident` 1、`problem` 1；completed8、terminated12。未取消、改写或重新触发 |
| 验证目标 | `itsm_ga_ready` 最高046，4张执行域表，工单6；不把此前计数当实时值 |

当前规范链已通过独立源码复核：历史账本／结构只读分类 → 037 P → 普通032–036、039–047 → 受限身份、inspection及标准模式绑定 → 当前制品准入与UI → 完整Dev配置切换。038 R保持pending_manual；旧022/027不重跑。相较于当前验证库，所选主干还要求047，不能声称两库已经同步。

#### 已完成的只读与恢复核验

- 当前源码编译的 `migrate -status` 和 `-prepare-workitem -dry-run` 均通过 Dev 只读预检，旧账本与已退休结构被识别。status 的“无可执行普通迁移”同时列 P/R pending_manual，不能解释为最新结构。
- Dev 当前逻辑备份 `dev-before-upgrade.dump`：1,559,723 字节，SHA256 `2de0dc857842bee20e6ef6810efa77094ea9cc738ae47d13e08a1f8b394db194`。另保存受保护的角色备份；不提交其内容或凭据。
- 独立临时容器 `itsm-dev-upgrade-rehearsal-20260915` 使用相同 pgvector/PG17 镜像、network none、无发布端口、tmpfs，恢复同名逻辑库与角色。152 张 public 表逐表行数与全部记录摘要匹配 Dev。比较忽略 SQL UNION 输出顺序，以表名对应内容；不导出业务明文。
- 恢复报告摘要 `22e8146ba758b707b4e60cc9fc0e73eb50138a7baf9a75e502eea5fd61e6ba02`。这是逻辑数据库内容恢复证明，未验证附件、WAL、完整运行栈恢复；不能称整体零损失恢复。
- 四张 P 核心表非 owner ACL 仅为原 Dev app 的 SELECT/INSERT/UPDATE/DELETE，无 grant option；system 无这四表权限。副本内准备独立 inspection 身份及 TEMP 隔离，未修改 Dev 角色。

#### 实际阻塞与处理边界

1. **P037 旧账本兼容缺陷：** 副本实际执行在写回执时因 `schema_migrations.catalog_revision` 不存在退出1。只读 inventory 成功不覆盖 P 执行期全部检查。回滚后仍24条旧回执、P回执0、证据表不存在、新账本字段0；旧流程保持原状。修复应由现有迁移器在同一事务内准备账本字段，不能对 Dev 手工补列或改历史收据。
2. **流程切换预检未完成：** 旧 Dev 直接运行 `check_workitem_cutover` 退出1，因为039尚未添加 `process_instances.execution_work_item_id`。该列有规范迁移来源；先在升级副本重跑，不使用Ent补表。6条既有流程的继续执行能力尚未验收，旧流程不能被静默取消或改成新标识。

私有证据统一保存在 WSL `/home/administrator/.local/state/itsm-dev-compatibility-20260915`；临时容器只用于此次恢复／升级预演，不是新增长期环境。U1 已完成账本与依赖核验；U2 尚未完成升级预演和真实 Dev 升级，U3 尚未切换。后续修复、复验及临时资源归档结果继续追加在本节，保持唯一清单。


#### 修复及第二轮副本结果

- 原子账本准备修复：`0e1afe997db6474f823e87f9182ff90e3b43614e`，[PR38](https://github.com/Joes9527/itsm/pull/38)。真实PG先复现42703，修复后ControlledPreparation组10项通过，迁移器单测通过；主agent独立复核事务与历史回执边界。此处是源码与副本证据，不表示主干已经合并或Dev已部署。
- 修复后二进制 SHA256 `c43136c5de30f6af37e6e5917271ac3170f00d5c4735e249823723361c214d22` 在副本成功执行 P。第一轮后继迁移到039后因迁移执行身份 `itsm_user` 与既有 `tickets` owner `itsm_base_owner_20260908` 不同被严格触发器检查拒绝；不是要求放宽校验或修改039历史SQL。
- 保留失败副本dump后，从原备份重新恢复，改用与目标表一致的 owner 执行迁移。仅副本内临时给该运维owner全行读取所需 BYPASSRLS，演练结束撤回；业务角色保持受限，真实Dev权限不变。真实执行须将此临时运维权限和撤回纳入明确审阅范围，不能隐含给业务身份。
- 该轮 **P037和全部普通迁移032–036、039–047均成功**，最终39条回执，038为0，历史 execution_scope_members为0。152张原表的原有字段及记录摘要再次与Dev逐表匹配；历史迁移回执比较排除新增记录及新增可空字段。新表、字段、回执是规范迁移成果，不声称结构完全不变。
- 标准执行角色绑定尚待独立配置；迁移完成不是当前应用已启动或UI验收完成。

#### 结构升级后的真实切换阻塞

`check_workitem_cutover -json` 在升级副本返回 **exit2**：检查26个实例、1条callback、28条binding，发现以下准确对象；无扫描截断。此前因缺039字段返回exit1的工具错误已解除。

| 对象 | 数量／ID | 状态与后续边界 |
| --- | --- | --- |
| 活跃旧流程 | 4：7、8、9、10；tenant1，对应 `ticket:7` 至 `ticket:10`；4个created任务 | **维护者本轮明确确认是已废弃的开发测试数据。** 设计使用现有BPMN终止服务，保留流程身份、历史与审计；不是删除工单或重新解释旧身份的授权。先在副本核验真实actor/权限、任务取消、未决callback和原子回执。 |
| 活跃旧绑定 | 19：1、4、5、7、8、9、10、12、13、16、349、683、686、687、689、690、691、823、824 | 横跨tenant1/2，必须逐项判断目标类、默认/优先级/条件与所选流程，不能无差别替换字符串。6条cloud_*非规范业务类型不能随意映射；配置变更方案单独列明。 |
| 已结束旧流程 | 20 | 信息项，不阻塞；保留历史，无需迁移或清除。 |

因此，U2的规范结构链已在恢复副本走通；真实Dev升级及U3切换仍未执行。当前下一步为副本验证4条废弃流程的受审计终止、形成19条绑定逐项配置方案、完善受限运行profile，然后按具名范围执行真实Dev变更与入口验收。


#### 19条绑定的配置设计边界（待执行，不是批量改名指令）

<a id="legacy-binding-impact"></a>

**绑定的含义：** `process_bindings` 一行规定某租户的业务类型／子类型使用哪个BPMN流程定义，以及默认项、优先级、适用条件等。19表示19条配置记录，不代表19个数据库或19条历史工单；它们是新ITSM Dev中遗留的旧类型配置，不能据此认定来自旧ITSM工单迁移。同一个流程可有多条不同用途的绑定。

按本次识别的业务用途分组：

| 用途 | 数量 | 旧配置及当前影响 |
| --- | --- | --- |
| 通用工单／分派 | 4 | 使用`ticket`，新版`generic`不能按该类型匹配；涉及通用与分派流程，不能合并所有子类型和优先级 |
| 普通／紧急变更 | 4 | 使用`change`，新版`change_request`不能按该类型匹配；仍须保留普通与紧急变更的流程区别 |
| 服务请求 | 5 | 2条使用`service_request`，3条使用`ticket`加`service_request`子类型；新版为`service_request_item`，需结合服务目录与请求用途重新核对 |
| 公有云运维、私有云运维、安全扫描 | 6 | 两个租户各3条`cloud_*`类型，目前不是获准的WorkItem流程身份；业务归属尚未确定，不能擅自映射为generic |

因此，按数据库原始`business_type`计数是ticket7、change4、service_request2、cloud_*6；按业务用途是4+4+5+6。两种统计描述同一组19条，不能误认为数量矛盾。精确ID见下表。

**实际影响：** 当前流程选择器按租户和规范业务类型匹配，旧类型不会被新类型自动命中；相关新记录无法通过这些配置选到预期流程。具体入口是否拒绝创建／启动，取决于其领域合同是否要求流程，不能统称全部页面或API不可用。切换预检已明确将19条启用旧绑定判为阻塞；047只补结构，不会自动修改这些配置。

**调整风险：** 直接批量改业务类型、清空子类型或设成默认，会改变路由竞争关系。例如824的自定义服务请求流程优先级25，高于823的15和13的10；不核对适用范围就转换，可能让其它请求进入该自定义流程。停用则会停止今后选中该绑定，但不自动删除定义或终止已有流程；因此要准备正确的新绑定并验证预期路径，不能仅以旧类型计数归零验收。

**当前处理状态：** 维护者只确认4个旧运行实例是废弃开发测试数据；19条绑定的调整范围仍待确认，本次说明与文档更新不执行停用、删除、改名或切库。


| 既有绑定ID | 拟核对的新模型归属 | 必须保留／先解决的差异 |
| --- | --- | --- |
| 1、10、16、683 | generic | tenant1/2分别核对；general/assignment子类型、默认项、优先级及所选定义不混并。 |
| 4、12、349、686 | change_request | normal/emergency分开；Dev与验证库同名Change定义正文不同，不能按名称认定功能相同。 |
| 5、13、687、823、824 | service_request_item | 824引用自定义`process_1787042003593`且优先级25；必须核对目录/场景适用范围，不能清掉子类型后变成所有Requested Item的全局默认。 |
| 7、8、9、689、690、691 | cloud_public_ops / cloud_private_ops / cloud_security_scan尚无获准新归属 | 建议保留记录并显式停用、登记待补配置；不能自动映射成generic、报告已恢复或删除定义。真正需要恢复这些业务时另行确认目标Catalog/WorkItem/执行能力。 |

租户1实际可选版本由已有版本服务按active+major选择：generic为1.3.0、普通Change为1.1.0、Service Request为1.4.0；绑定中的整数1不是只选字符串1.0.0。源码依据`bpmn_version_service.go`的`selectExecutableProcessDefinition`，无需复制版本选择器。验证库只有tenant1，不能用其绑定覆盖Dev tenant2。

配置改动应通过现有ProcessBindingService/产品入口，保留准确tenant与全部未变字段；UpdateBinding会写多个标量字段，不能只传is_active而意外清空优先级、默认项、部门/团队或策略。需要修改的业务归属、匹配范围及未知cloud业务处理尚须具名审阅；U3不能把这份设计当执行收据。


#### 废弃测试流程的副本领域验证

副本通过现有`TerminateProcessTx`以原app/system边界、实际active actor1及数据库权限派生BPMN scope执行；先在RepeatableRead事务内全程rollback验证，再原子提交4条。未硬编码业务全权限，未启动worker/外部连接器。

结果：实例7–10均terminated/version2，关联4任务cancelled/aggregation_version1，新增4条actor1、tenant1的终止审计；流程身份、工单及历史仍保留。callback无改写、历史scope成员0、038回执0。主agent独立全表原字段摘要比对：仅process_instances/process_tasks/process_audit_logs发生预期变化（审计53→57，实例26和任务32行数不变），源Dev152张表原数据再次确认未变。副本新增一个standard runtime binding及四张执行域表SELECT，没有扩大业务身份权限。

一次性副本runner不作为真实Dev工具交付：其连接前已固定校验容器hostname和目标标记，WSL host实测连接前拒绝；原执行制品、受保护源码和哈希、强化目标保护后的制品分开保存。后续真实环境操作应通过维护的产品服务入口及具名目标配置，不移除副本保护来复用。


#### 本轮交付与资源收口

PR38全部CI通过后已合并，合并提交`32c39dda38422999bf556219c8d096f169d01512`。尚未部署新版API；本轮构建的后端源`0e1afe997`工作树干净，制品SHA256 `4490816c50b5a6495be377bfcda94c029ddf485e2dc5240d1667b08f857b7d88`。本机Go构建未内嵌VCS字段，来源以独立核验的源码和制品清单绑定，不能只看文件名认定版本。

WSL最终副本与角色已归档到既有私有证据目录，最终副本dump SHA256 `ed0ea71eb97c19e079d10c7885771694cf9f4ac901cc5076a6167a07536253a8`。确认本任务标签、network-none和无发布端口后，临时容器已停止并移除；本机临时PG也已清理，仅保留测试日志。没有新增长期运行环境。归档是预演结果，不能替代后续真实Dev写入前的新鲜备份和配置核对。

**待维护者确认的配置范围：** 保留并停用19条旧类型绑定，按新模型建立标准绑定；6条cloud_*及自定义服务请求绑定824保留为后续配置、不删除。若仍需使用这些自定义业务，先明确对应场景再调整。本轮已请求此项范围确认，尚未收到回复；没有把4条废弃流程的确认扩大为所有配置可删除。真实Dev结构／数据及3010→8080当前目标均未变更。


<a id="dev-clone-alignment"></a>

## 2026-09-16 目标对齐与两库收敛方案

**状态：目标 accepted；下述具名数据库收敛和实际写入方案 proposed，尚未执行。** 维护者确认目标是恢复正常开发，同时建立可重复的数据迁移验证过程。同一选定新版代码，日常使用Dev，验证使用从Dev克隆的数据库；旧ITSM ticket及其历史流程数据不迁。047是结构目标，不是数据库名称。本节取代历史材料允许将任意隔离目标长期充当Dev克隆的解释。

### 本次只读事实与纠正

- `itsm-postgres-dev / itsm_config_baseline_20260908`：24条迁移收据，最高031，作为唯一日常Dev保留。
- 同实例 `itsm_migration_20260914`：14条收据，最高019；18条tickets、28条process_bindings。先前会话把它直接称为031克隆没有足够证据，现纠正。账本序号不能独立证明全部结构或最初克隆来源，不补造收据。
- 3010与8080均无监听；stack分别报告前端和后端旧PID记录失效、服务停止。`ga-itsm-20260914`与`gb-remediation-test-pg-20260914`均已停止；前者仍挂载数据库持久卷。本轮未启动它们，不能把09-15数据计数当作今天查询结果，也不能仅凭卷存在宣称备份可恢复。
- 独立只读抽查旧019对照库：B1的46个CI及3个分类缺失；B2的25个新增选项全部缺失；B0选定288对象中5个缺失。它不含已验收五批的完整成果，不能只改库名后宣称迁移完成。
- 五批384对象的历史双库证据对应 `ga-itsm-20260914 / itsm_ga_ready` 与 `gb-remediation-test-pg-20260914 / gb_replay_review`，不对应旧019对照库。已完成证据保留，不扩展成新克隆验收。
- 主干 `6336e28a2` 尚不包含 Toolkit v1提交 `5f9877772d5ff6fbca80c4e02871bb4aa86d692a`。工具已提交但尚未主干集成；复用既有实现，不能另造一套工具。

### 唯一运行目标与既有成果保留

| 对象 | 建议职责与处理 |
| --- | --- |
| `itsm_config_baseline_20260908` | 唯一Dev；保留数据，按已预演规范链升级到047并恢复开发入口 |
| `itsm_migration_validation`（建议名，尚未创建） | 唯一长期迁移验证克隆；在Dev升级验收后从具名Dev一致性快照恢复，记录来源、时间、摘要、角色/租户与附件范围，不通过整库复制旧ITSM建立 |
| `itsm_ga_ready` | 既有新模型配置与验收成果来源，过渡保留；不再作为第二个长期验证入口，不整库覆盖Dev或新克隆 |
| `itsm_migration_20260914`、`gb_replay_review`及其它测试库 | 历史对照/证据来源；先保全备份、用途、消费者和恢复证据，再单独列具名归档删除范围，本方案不执行删除 |

长期只维护两个业务数据库角色，不要求把KAF、Langfuse及系统数据库删除。新验证库的一次创建是为了建立真实可核验的Dev克隆来源，不是仅为把结构编号改成047。保全阶段暂存旧成果库，验收和归档后退出业务运行。

### 续办顺序、交付与停止条件

| 顺序 / 沿用任务 | 工作与交付 | 验证与停止条件 |
| --- | --- | --- |
| 1 / U1-U2 | 固定共同新代码制品，刷新Dev备份及恢复证据；复用已验证P037、普通032–036/039–047路径，明确迁移角色临时权限及撤销。对4个已确认废弃实例采用领域终止路径；19绑定逐项列出保留/替换/停用及业务去向。 | 实际写入前形成可审查具名变更清单；不运行R038、不删除旧表、不批量替换字符串、不绕过RLS。6条cloud与824未定业务不得伪造支持；代表性开发路径受其阻塞时明确报告。 |
| 2 / U3 | 完成获准Dev升级及必要配置后，用完整Dev profile恢复3010→8080。runtime/system/inspection、Redis、存储、会话和消费者范围一并匹配。 | 验证进程实际数据库身份、readiness、登录和新建/处理代表性WorkItem的UI路径；未通过则保持明确失败状态，修复当前代码/配置，不以旧代码作为长期恢复办法。日常开发恢复不等待全部旧数据验收。 |
| 3 / U4 | 集成和验证现有Toolkit；从已验收Dev快照建立建议名验证克隆，独立设置凭据、运行/检查角色、缓存和附件范围，后台外发默认关闭。 | 校验克隆来源、迁移收据、结构、源数据范围和必要关联；明确复制的是新系统Dev测试记录，不是导入旧系统ticket。克隆刷新属于显式受控操作，不建立持续双向同步。 |
| 4 / C1保留、C3/C4续办 | 保全旧抽取源manifest、五批映射/制品/历史收据、用户组织工具证据及现有结果。先按租户与稳定业务键比较新克隆；已有且一致的对象跳过，缺失/冲突形成清单，再用已有适用工具执行获准差异。 | 不整库覆盖、不盲跑seed/五批、不把旧收据写入新库冒充执行证据、不覆盖密码/角色。原五批Known Error占位和示例对象不因出现在历史批次中就自动要求补入。Toolkit当前users/departments能力不等于五批配置导入器；配置复用既有批次工具，必要缺口另审。新库产生本次真实验证/执行证据。 |
| 5 / R1、V4/V5 | 约定验证窗口，将同一版本3010→8080切至克隆，验证数据在新系统实际业务路径可用；窗口结束恢复Dev。 | 结构、数据差异与跨角色/专业域UI结果分开记录；仅健康检查或数量相同不算业务验收。旧成果库退出前核验备份恢复、依赖和具名删除范围。 |

**本轮完成：** 实时只读盘点、目标与保留方案、顺序和验收边界。本轮未执行数据库写入、克隆/删除、服务启动、升级或入口切换。下一执行包是U1-U3的具名Dev恢复方案；不再重新讨论已接受的总体目标。


<a id="two-database-execution"></a>

## 四阶段执行清单（2026-09-16，设计已确认）

**状态：执行中（2026-09-16）。原 Dev 已完成 P037 与普通迁移至047；3010→8080 已连接 Dev，尚未完成 UI 验收与两库清理。** 本节取代此前续办顺序，沿用U1–U4、C1/C3/C4、R1/V4/V5作为证据索引，不另建平行任务表。

**Goal：** 恢复当前新代码的日常Dev，建立可追溯验证克隆，保全身份、业务配置及迁移成果，最终只保留Dev与验证两个长期ITSM数据目标，按最新指示使用同库不同schema。

**Architecture：** 原Dev前向升级，验证schema从验收Dev克隆；3010→8080一次只使用一个完整profile。数据库写入串行，角色/缓存/存储/会话/消费者按目标隔离。

**Tech stack：** 现有Go规范Migrator、领域服务、PostgreSQL、WSL stack管理器、Python迁移Toolkit、Next.js UI。

> 执行Agent按已批准设计逐阶段推进，可使用executing-plans或subagent-driven-development组织任务；本段不授权跳过证据门槛。独立只读审查可并行，共享写入只由一个负责人执行。

### 执行Agent接续入口（2026-09-16）

维护者已委托本轮设计，后续自行分派其他Agent执行。先交付[任务包A：Dev流程恢复](2026-09-16-dev-workflow-recovery.md)，同步可做[任务包B：schema克隆](2026-09-16-schema-validation-clone.md)的只读分析/隔离实现。共享迁移、克隆和清理按门槛串行；B若新增规范迁移须显式更新结构目标，不能把047永久写死。任务包是实施步骤，不替代本清单的实际状态；本次仅文档，不代表任何新增功能或共享变更已经完成。

**任务包文档复核（2026-09-16）：** 独立审查发现并已修正文档中的5项缺口：A完成事务与blocked区别、非空/类型验证、任务查询零写入；B准入升级生命周期设计门槛、阶段2完整验收依赖。复查未发现新的阻塞性歧义。A1/A2可窄范围实现；A3须先冻结流程制品审查；B1及B2/B3设计验证可开始，schema克隆不能报告为端到端实施就绪。两计划已补阶段阅读表与任务准入/交付门槛，AGENTS/CLAUDE同步入口。本结论仅为文档边界复核，不代表代码、环境或业务通过。

**后续设计补齐（2026-09-16，取代上段“B仅可做设计”状态）：** 维护者要求全部设计由当前设计负责人完成。新增[完整实施合同](../specs/2026-09-16-dev-schema-execution-contract.md)，冻结实际definition65派生图（只读XML摘要已核对）、领域门禁、旧callback现有blocked处置、clone表/CLI/pin/升级及撤销刷新、现有演练rename+有限模板恢复、candidate scope schema适配及Toolkit受限JWT。独立复核修正Up前结构自锁和trigger校验遗漏后通过所审条款。A/B现可依此实现，不再要求执行Agent补详细设计；共享执行仍须既定验收门槛。本次只有文档和只读核验，未修改运行环境。

### 全局约束与文件职责

- Dev：`itsm-postgres-dev / itsm_config_baseline_20260908 / public`；验证克隆按维护者最新指示使用同一数据库内独立schema（未创建）。不新增PG容器，不再采用原异库名方案；具体准入及权限隔离方案见[更新后的设计](../specs/2026-09-16-dev-restoration-two-database-design.md)。
- 所选代码结构目标047；P037及尚缺普通032–036、039–047按规范依赖执行，R038排除；旧SQL/校验和/真实回执不改，禁止Ent叠加补表和应用owner权限。
- 身份、组织、权限、密码及业务配置保留；测试数据先分类；824与6条cloud绑定保留停用；旧ITSM ticket及历史流程不迁。
- 后端工具：`itsm-backend/cmd/migrate/main.go`、`migration/work_item_preparation.go`、`cmd/check_workitem_cutover/main.go`只复用；领域操作复用现有服务。发现代码缺口才建独立修复并测试，不直接改库绕过。
- 运行操作只通过`scripts/wsl-stack.py`部署的`/home/administrator/apps/itsm-kaf/stack`及其受保护recipe；不混用历史启动器。
- Toolkit来源提交`5f9877772d5ff6fbca80c4e02871bb4aa86d692a`，路径`scripts/migration/`、`scripts/__tests__/`、`docs/migrations/runbook-data-migration-validation.md`；先检查最新主干是否已集成，防重复实现/合并。
- 文档只更新本清单结果及`docs/development-environment.md`实际入口。具名对象、备份、摘要、命令回执保存在受保护证据目录，仓库不含凭据和个人行数据。

### 阶段1：固定范围与恢复证据（U1/U2，读与备份）

输入：已接受设计、原因分析、09-15预演及五批证据。输出：唯一具名变更清单和可恢复基线。

- [ ] 刷新源码/制品、3010/8080进程、实例/库/schema与迁移账本；只输出连接身份白名单，不输出完整配置。
- [ ] 盘点保留身份/配置及关联；按tenant、稳定键和数量/摘要固定基线。区分字段存在性、真实收据与最高编号。
- [ ] 列出测试记录处理集合及依赖；7–10标已确认废弃，其它先证明为测试数据，不由命名猜测。列19绑定旧值、目标类型、流程版本、默认/优先级、替代与停用关系，824/6cloud单列保留。
- [ ] 列出所有待退出ITSM库及其专属容器/卷、消费者和成果；KAF/Langfuse/系统库排除。未知消费者的对象标阻塞，不加入可删除集合。
- [ ] 刷新Dev数据库/必要角色备份，核对附件覆盖，在隔离恢复目标验证恢复、身份/配置/关联；备份摘要、恢复结果与目标明确绑定。
- [ ] 固定所选发布版本与完整两套profile方案；核对临时迁移权限及撤销方式；独立审查具名写入与恢复范围。

**门槛：** 备份可恢复、保留对象有基线、数据清理与配置变更有准确边界。若不满足，停止实际Dev变更；已有报告不能代替新基线。

### 阶段2：升级与恢复Dev（U2/U3）

输入：阶段1变更清单/恢复证明。输出：经UI验收的Dev与可克隆基线。

- [ ] 用最终候选制品在恢复副本运行规范只读`migrate -status`、`-dry-run`、`-prepare-workitem -dry-run`，绑定实际目标及新证据。CLI通过受保护CWD配置定位，不臆造DSN参数。
- [ ] 复用已验证迁移路径，在副本补验本次配置、测试数据及角色差异；运行结构、保留基线、绑定路由与领域终止检查。不得把一次性且锁定旧容器的终止预演程序改目标用于真实Dev。
- [x] 建立维护窗口，停止相关写入/消费者；再次核对Dev身份与备份一致性。范围或源状态变化时补验受影响项。
- [x] 执行已审P证据提交和规范普通升级；仅使用此次真实目标证据，日志记录返回码/回执，不自动执行R038或重放已执行022/027。
- [x] 通过领域路径终止已确认废弃活动流程，按验证方案整理测试关联；应用已审绑定适配/停用并记录前后状态。保留自定义定义，未知类型不得继续可调度。
- [x] 配置匹配的runtime/system/inspection身份及执行绑定，撤销临时权限；使用完整Dev profile启动当前新代码，校验实际连接而非仅recipe。
- [ ] 验证迁移/结构、角色隔离、身份及配置保全、readiness、登录与代表性通用工单/变更/服务请求UI；检查旧绑定不能错误竞争路由，待适配能力显式列出。
- [ ] 记录已恢复Dev制品与profile、允许重新开放开发写入的时间，更新环境入口。失败按设计的开放前/开放后恢复规则处理，不长期退回旧代码。

**门槛：** 核心开发路径通过才开放；健康端点或cutover checker单独通过不算UI验收。活动过程处置与结构准备遵守各自领域/迁移前置条件，不机械按编号处理历史数据。

### 阶段3：建立克隆并复用迁移成果（U4、C1/C3/C4、R1/V4/V5）

**最新方向：** 本阶段及阶段4原有“验证库/两个库”用语按同库内Dev/验证两个schema数据目标理解；原独立库名只是历史提议。具体实施步骤须先补齐schema克隆准入与隔离设计，不直接照旧步骤恢复或删除。

输入：阶段2验收基线、现有源manifest/工具/成果。输出：唯一可用验证库与可重复验证过程。

- [x] 核验Toolkit实际分支与主干差异；未集成则单独评审集成，运行既有离线测试`python3 -m pytest scripts/__tests__ -q`及`python3 -m scripts.migration self-test`，不让离线检查连接共享库。
- [ ] 从已验收Dev一致性快照恢复具名验证库；保存来源/时间/摘要，校验迁移结构、身份及关联。建立独立profile，阻止复制过来的凭据/绑定意外连回Dev，禁用未获准后台外发。
- [ ] 保全原ga/replay五批映射、制品、真实收据和身份工具结果；按稳定业务键比较新克隆，列出一致、缺失、冲突及排除对象。
- [ ] 用户/组织先执行现有`verify`与`verify-profile`；需补建时先dry-run。配置用既有适用批次工具生成差异方案，不盲跑全量seed、不复制旧收据、不默认补示例对象。
- [ ] 应用获准差异，产生本次真实证据，保留身份/权限与较新合法配置；继续C3/C4已定范围，未定业务明确待适配。
- [ ] 在约定窗口以同一前后端制品切入验证完整profile，验证数据及跨角色/专业域UI；失败保留证据并切回Dev，结束无论成功失败都核实Dev恢复。

**门槛：** 克隆来源可追溯、数据差异可解释、目标业务路径通过；旧五批成功不等于新库自动通过。未通过的迁移验收保留在清单，不阻止正常Dev开发。

### 阶段4：清理与最终交付

输入：Dev/验证两个schema验收结果、旧成果保全、具名清理清单。输出：同库两个长期schema目标与最终状态。

- [ ] 重新检查每个待删库及专属资源的消费者、成果、备份恢复证明；先迁移依赖；清单外对象不删除。
- [ ] 分批归档删除旧ITSM业务/测试库及无共享依赖的专属容器/卷；每批核对Dev/验证schema仍可用。禁止名称通配删除，禁止删除共享Dev PG实例或卷。
- [ ] 临时恢复/测试资源用完退出；归档旧recipe和说明，当前文档只指向Dev与迁移验证两个profile。必要迁移证据不因清理丢失。
- [ ] 盘点所有维护范围内ITSM实例，包括已停止容器，确认仅同库内Dev/验证两个长期ITSM数据目标，历史ITSM数据库已按清单退出；备份文件及PG系统库不计入。若仍有阻塞对象，不宣称清理完成。
- [ ] 最终核验日常入口连接Dev及两个目标身份/版本，记录保留库、已删对象、备份位置和未覆盖能力；环境无新变化时不重复全套UI测试。

### 本次执行证据（2026-09-16）

本节是当前状态；前文09-15及09-16执行前的031、入口停止等观察保留为历史，不再代表实时环境。

- **恢复基线：** 私有证据目录 `/home/administrator/.local/state/itsm-dev-restoration-20260916`。`dev-before.dump` SHA-256 `53c9864ef99d3e08ed073264e2bc5b81eb4a31ca45d77c52d56d08343ee67c5e`。隔离恢复核对152张原表及身份/配置；副本升级后再核对原字段/行一致。附件9个物理文件恢复比对一致，但尚无MinIO API恢复验收，不扩大证明范围。
- **真实Dev升级：** `itsm-postgres-dev / itsm_config_baseline_20260908 / public` 已有39条真实回执，最高047；P037及14条普通迁移成功，R038未执行。原24条回执及152张原表字段/数据核对一致；临时owner权限已撤销。见 `actual-upgrade-result.json`、`actual-*` 与 `rehearsal-independent-review.json`。
- **运行入口：** 3010前端源 `2988819c94cfcfb69cee5b0d1dfbe69ee0fb8c9b`、build `DNEHNrmCuLdDvWw3kgnCI`（显式构建WSL8080通知地址）；8080源 `fc8de9d3626bd61e9f046085e4cde8c43d6432e4`，制品SHA-256 `1fc38aaa0bcfd62361c0bf9fa5d30ecf2f8ea758246755c0e845f991a8e7b6af`，含已合并PR44/45。实际连接为Dev app/system及独立inspection `itsm_dev_inspection_20260916`；Redis DB11、附件桶 `itsm-uploads`。使用development模式，未配置LLM不等于AI已可用。消费者启用outbox、callback与event audit；notification及外部能力仍关闭，日常完整流程尚未宣布验收通过。
- **登录与准入：** 3010浏览器登录成功；PR45经独立审查、CI和部署后，readiness返回200，schema047、baseline1.0.0，单次观测约0.27秒。R038仍未执行，未伪造回执或扩大权限。
- **测试流程处置：** 实例7、8、9、10经现有领域API逐条终止，全部读取验证为terminated；保留历史与领域审计。执行前锁定3010/8080实际PID、启动时间、制品、配置、上游及Dev身份，并核对无未决callback。私有 `domain-termination-before.json` 和四份 `domain-termination-after-*.json` 保存结果。没有删除工单/历史数据。
- **配置已处理与待适配：** 19条旧绑定已通过具备CAS与审计的领域API逐条停用并保留；7条generic/change替代已创建，完整字段及定义摘要回读核验通过（`binding-apply-journal.jsonl`）。源16的自动任务不受支持；服务请求源5/13/687/823存在专业完成及分支配置缺口，连同824和6条cloud保留停用待适配，不能报告为业务验收成功。
- **工具：** PR43已修复实体选择、租户范围与失败退出码问题并合并；91项离线测试通过、1项live anchor跳过，独立复审无剩余阻塞；未运行真实数据回填。`--apply`尚不满足API/数据库目标绑定，不宣称可直接回填。
- **克隆方向已纠正、实现仍阻塞：** 维护者否决新增PG容器，指定同ITSM数据库、不同schema；独立PG建议撤回，不再等待其选择。长期ITSM/KAF单实例双逻辑库整合计划继续有效。源码`verifyPreparationReceipt`同时校验database/schema，现有`DB_SCHEMA`配置不能代替schema克隆准入；需保留原回执并补齐可审计的来源/目标证明及跨schema隔离验证。验证schema尚未创建，旧成果库和临时恢复容器未清理，不能声称已收敛到两个数据目标。
- **创建阻塞及配置修复：** 普通工单UI最初返回500且事务回滚；默认Graph目标被停用，无法冻结邮件通知目标。配置Dev专用本地SMTP接收器后，同一表单成功创建工单29。`itsm-dev-mailpit`仅发布127.0.0.1:1025/8025，无relay；镜像固定`axllent/mailpit@sha256:df6c2541907e1be6fac21f509927cf6ed771617a1f4b361ef66d97bd05593d2d`。合成邮件接收验证通过；应用notification仍disabled，工单29邮件intent待处理且attempt0，站内通知已落地。原用户偏好与企业connector不改；这不是外部邮件发送验收。
- **UI实际边界：** 工单29（`DEV-RESTORE-20260916-GENERIC-01`）已通过创建、详情、评论、带原因转派至验收人、刷新及合成附件上传/下载；下载文件逐字节比对一致，SHA-256 `5e65df006000f6ed998847c0931fd0c1b76c8ca5fe849a2e60c9dcf35109f0a3`。流程27使用`generic:29`。UI完成任务33后，任务记录completed，但callback2仍pending/handler_error，流程未推进。本条记FAIL，不将任务提交/完成当流程成功。已定位：definition65的Activity_Assign回调要求assignee_id，但任务完成UI没有该输入，契约只标正整数未标required，导致空payload被接受后反复失败。只读纯handler复现一致；instance旧快照assignee2不能替代当前工单owner1，更不能补写冻结回调伪造用户选择。Activity_Resolve另需验证new_status输入。现有API没有带修正输入的callback恢复入口，实例终止也拒绝未决callback；保留实例27/任务33/回调2，不手改payload或重置任务。配置候选是新定义版本使用现有无handler fulfillment + work_item_assignee模式；若继续保留assign回调则需补UI/API必填表单契约，两者尚未取代已接受设计。当前数据恢复另需受审计方案。

### 验证、审查与汇报纪律

每阶段只汇报“完成／验证／阻塞／下一步”，在本节勾选并链接脱敏证据。已有相同源码/目标/条件的证据复用，变更和失败才补验；代码修复按影响范围跑测试。配置清理、真实写入和删除清单在各自门槛进行独立审查，共享操作不并行。文档交付完成不勾选任何环境执行项。
