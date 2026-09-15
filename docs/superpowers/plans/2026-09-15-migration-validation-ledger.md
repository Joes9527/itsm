# 旧 ITSM 主数据迁移与当前目标验证：唯一续办清单

- 状态：active；范围由维护者于 2026-09-15 确认。本清单不表示未验证步骤已通过，不是数据库删除执行回执。
- 目标：保全新 ITSM Dev PostgreSQL，将必要旧主数据清洗适配到新模型，在现有 3010 → 8080 上验证迁移目标。
- 权威约束：[AGENTS.md](../../../AGENTS.md)、[工程治理](../../agent-engineering-governance.md)。本清单维护本轮续办状态；历史设计、报告保留证据，不再各自生成平行待办。
- 取代范围：取代 2026-09-15 环境审计报告中的“下一任务顺序”；不否定其当时的运行/数据库观测，不取代领域合同。

> 最新执行结论以本文末尾“最终运行与UI验收收口”为准；中间的失败/构建中条目是定位过程，不代表当前仍失败。
## 1. 数据来源与明确范围

旧系统是既有抽取设计记录的 `https://keas-itsm-test.gazellio.com`，不是 WSL 的 PostgreSQL `itsm` 库。本轮核验的是设计、交接与已有抽取证据，未重新登录旧系统或抽取最新数据。

旧系统以只读 API 提供 CTI、字典、优先级、矩阵、路由、模块、日历及流程对照；源文件位于 KAF `data/legacy_itsm/test/`。源设计固定提交 `184f7868`。旧抽取数量是 2026-09-14 快照，不能称为今天实时源数量。

**明确不迁旧 ticket、历史审批、评论、附件和流程实例。** 不复制旧 BPMN；流程只作语义对照。组织/用户复用已迁成果，仅核对身份、租户和引用，不重置密码/角色。首期旧路由不迁，使用人工分派和新规范流程；不开展 MSP 产品扩展，但保留既有隔离约束。

新模型是权威：单一字段/领域所有权、WorkItem recordClass、专业生命周期、现有 BPMN、审计和权限合同均不能为适配旧数据而退回旧结构。

| 数据对象 | 角色 |
| --- | --- |
| 旧系统抽取与 manifest | 迁移源；记录时间、来源和摘要 |
| itsm_config_baseline_20260908 | 切换前实际 Dev 数据库，保留可恢复基线 |
| itsm | 较早新 ITSM 开发库；不是旧系统本体，不用它的计数代替当前 Dev |
| itsm_migration_20260914 | 新 ITSM Dev 的克隆对照样本，不是新模型准入证明 |
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
| R2 | 当前制品与最新 main 不同，差异本身不是故障 | 固定当前 3010/8080 制品与目标合同，仅将本次验证所需修复纳入候选；需要 Assignment 才规划匹配代码+047，不自动追最新主干 |
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

## 7. 执行顺序与唯一状态维护

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
