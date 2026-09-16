# 同库schema验证克隆实施与交接计划

> **For agentic workers:** Use executing-plans task-by-task. 用户自行分派Agent；只读调查/隔离实现与共享操作分别交付，不能把本文件当无条件清库命令。

**Goal:** 在现有ITSM数据库中建立可追溯验证schema，与Dev隔离，复用迁移工具，最终退出无依赖的历史ITSM目标。
**Architecture:** 一个PG实例中的ITSM数据库使用Dev public和验证migration_validation两个schema；原历史回执保留，新目标通过独立克隆准入证明接受。长期KAF保持自身逻辑数据库，沿用任务三合并实例。
**Tech Stack:** 现有Go Migrator/runtime inspection、PostgreSQL、Python migration Toolkit、WSL stack profile。
**Status:** draft（详细准入/恢复实现设计待补齐）；同库不同schema总体方向已确认。现在可执行B1与B2/B3设计验证；通过下述设计门槛后才开始相应生产实现，共享执行再过B5门槛。

## 全局约束与依赖

先读AGENTS.md/CLAUDE.md、工程治理、DEVELOPMENT_GUIDE、development-environment、[设计§7](../specs/2026-09-16-dev-restoration-two-database-design.md#7-接续设计执行-agent-的裁定与门槛)、[唯一清单](2026-09-15-migration-validation-ledger.md#two-database-execution)及[既有ITSM/KAF合并计划](2026-09-14-itsm-kaf-task-3-pg-consolidation.md)。独立worktree从最新main建立；本计划链接的PR46若未合并先确认文档提交。

### 按执行阶段阅读背景

| 阶段 | 必读入口 | 用途 |
| --- | --- | --- |
| 开工 | [AGENTS.md](../../../AGENTS.md)、[CLAUDE摘要](../../../CLAUDE.md)、[工程治理](../../agent-engineering-governance.md)、[工程约定](../../engineering-conventions.md) | 权限/tenant边界、事务、迁移、跨模块所有权及交付规范 |
| B1/共享操作 | [环境合同](../../development-environment.md)、[开发指南/RLS](../../DEVELOPMENT_GUIDE.md#rls-execution-boundary)、[命令参考](../../dev-commands-reference.md) | 受限连接、现有运行入口和实际profile，不凭环境名猜目标 |
| B2/B3 | [受控退役设计](../specs/2026-09-11-workitem-controlled-retirement-design.md)、[实施与证据](2026-09-11-workitem-controlled-retirement.md)、[目标运行手册](../../deployment/workitem-controlled-retirement-target-runbook.md) | P/普通迁移/R阶段、真回执、inspection、回滚/reset、恢复和已知backlog |
| B4/B5 | [迁移工具手册](../../migrations/runbook-data-migration-validation.md)、[五批配置交接](../../review/2026-09-14-workitem-config-migration-handoff.md)、[唯一清单](2026-09-15-migration-validation-ledger.md) | 工具边界、源manifest/成果来源、apply阻塞和旧ticket排除范围 |
| B6 | [ITSM/KAF总设计](../specs/2026-09-14-itsm-kaf-database-convergence-design.md)、[单PG任务三](2026-09-14-itsm-kaf-task-3-pg-consolidation.md) | 双产品各自逻辑库、G-C依赖；本任务只交接不迁KAF |
| 独立验收 | [代码审查指南](../../code-review-guide.md)、[E2E指南](../../e2e-testing-guide.md)、[路线图](../../../ROADMAP.md) | 证明源码/运行/数据/业务分别成立，不用历史PASS代替新目标 |


### 所有执行Agent的任务准入与交付门槛

仓库权威文件名为`AGENTS.md`，不是另建`AGENT.md`。以下是本任务检查入口，不复制或取代原规范：

- 开工记录单一目标/排除范围、影响模块和契约、共享写入标识、验证方式、依赖/冲突分支；按[治理§7](../../agent-engineering-governance.md#7-任务协同与角色分工)满足Definition of Ready。
- 独立worktree及`codex/<type>/<scope>-<description>`分支，禁止main直写、覆盖他人未提交内容；PR正文引用本任务编号、当前文档提交和完整源码SHA。
- 修改前运行最小基线测试，修改后补真实回归及受影响构建/类型/契约检查。环境未具备导致skip必须记未验证；不得为了测试启动共享服务、seed、reset或隐式迁移。
- 后端拥有规则/权限/租户/事务；前端只消费DTO和授权投影。流程身份使用`common/workitemidentity`；不得恢复ticket/change/service_request旧BPMN词表。
- 修改架构/领域契约时同步AGENTS.md和CLAUDE.md；修改开发流程更新DEVELOPMENT_GUIDE；API/前端公共约定变更同步engineering-conventions。本任务发现未知外部动作时失败关闭。
- 交付前`git diff --check`、受影响测试和必要CI通过，检查无秘密/临时文件。涉及BPMN/WorkItem/迁移/权限必须独立复核；实现者不能独自宣布验收。
- 共享操作实行一个写入负责人：执行前固定具名目标、备份恢复、实际PID/源码/制品/profile、影响范围、回滚/补救和消费者；已授权范围可继续，不重复索取笼统许可；超出范围先记录差异再做范围决定。
- 交付分开写“代码已合并”“实际部署”“业务验收”“未完成/未覆盖”。报告必须包含失败和跳过，不以健康200、任务completed、旧截图或离线测试替代本次业务通过。

- 不新增常驻PG容器。临时测试优先使用已批准恢复资源，先确认无其他Agent占用；隔离测试schema也必须具名且受控，不能通过默认DSN碰Dev。
- Dev当前 `itsm-postgres-dev / itsm_config_baseline_20260908 / public`，047/39真实回执；验证schema建议`migration_validation`，创建前核名。当前事实需重新读取，不凭文件名判环境。
- 不移动Dev public，不改旧SQL/校验和/P内容或伪造迁移执行，不使用Ent补表，不运行R038。
- 不导入旧ITSM ticket；新系统Dev测试数据属于克隆范围。角色身份及权限也必须纳入备份/恢复验证。
- 3010→8080只运行一个获准profile；禁用外发并分别配置缓存、附件及会话。复制来的pending任务/队列默认不消费。
- 共享克隆消费唯一清单阶段2的完整验收：A的generic通过仅是必要条件，还须核对身份/业务配置保全及Change/Service Request代表性UI。A不负责的缺项由集成人明确分派；未通过时B5保持阻塞，若需缩减范围必须有具名维护者决定，不能自行省略。旧冻结失败仍明确保留，B不得擅自修复/删除它。

## B1：核定跨schema隔离的全部入口

**Files:** `itsm-backend/database/runtime_clients.go`、`config/config.go`、`migration/migrator.go`、`migration/runtime_inspection.go`、`migration/work_item_preparation.go`、`cmd/migrate/main.go`；`scripts/migration/target.py`、`profile.py`、`backfill.py`；`scripts/wsl-stack.py`。

**Consumes:** 明确的source/target database-schema-deployment、角色和制品；**Produces:** 一张来源→目标对象/角色映射和实际受影响文件列表，写入本计划对应PR说明。

- [ ] 只读确认DB_SCHEMA进入所有runtime/system/inspection/迁移连接；查找public硬编码、隐式search_path、序列默认值及函数跨schema引用。
- [ ] 确认Toolkit目标读取固定public的问题，检查API写入目标证明缺失；--apply当前拒绝是正确保护，不提前移除。
- [ ] 核验目标schema名不存在；列出PUBLIC、default privileges、extension、角色membership和跨schema依赖。未知依赖保留阻塞，不全局REVOKE或CASCADE。
- [ ] 复用现有锁/迁移事务/证据读取入口，记录哪些校验需要显式clone分支。禁止去掉`verifyPreparationReceipt`的database/schema相等判断作为修复。

## B2：规范化克隆准入，保留来源真实历史

**Files:**
- 新增 `itsm-backend/migration/clone_admission.go` 和同目录`clone_admission_test.go`，职责为验证/记录本次来源与目标；不是第二个Migrator。
- 新增专用规范迁移，在`migration/migrations.go`注册下一可用版本，创建`work_item_clone_admissions`表；SQL按现有migrations规范落点，不预占编号。
- 修改既有`migration/control_config.go`、`migration/runtime_inspection.go`、`migration/work_item_preparation.go`、`migration/migrator.go`及`cmd/migrate/main.go`，共用同一克隆证据验证入口。
- 修改AGENTS.md及CLAUDE.md的controlled-migration摘要，链接本设计；更新环境结构目标和命令说明。

### B2/B3实现前的设计审查门槛

当前表名及新增文件是候选落点，不是已经审定的DDL/CLI接口。Agent先在关联设计§7补齐以下内容，独立审查者确认后再编码；本文件明确承认这些尚未完成，不能由实现者自行猜测：

1. 新表字段/主键/唯一约束/不可变策略；作为部署级受限证据表的tenant/MSP边界；稳定操作ID、source与target角色映射及精确CLI入参/退出码，明确runtime不具备写准入能力。
2. 状态与事务：目标未获准→封存验收→可启动；写入失败、连接中断、重复提交、撤销及重建的状态转移；可信配置摘要发布与DB事务不原子时如何失败关闭。
3. 升级/回滚：准入时结构摘要是快照证据，后续规范迁移如何推导当前结构；不能每次普通升级后永久失配，也不能忽略差异。覆盖source先升级、target滞后再升级、允许的rollback及不可回滚承载表；reset若删除证据必须拒绝或采用已审完整重建路径。R仍禁止。
4. 验证模式：source原P按源身份验证；target通过不可变来源证据加当前目标准入验证。建立有限角色/schema映射，不把信任配置的自声明摘要当成实际恢复证明；无源在线连接时runtime仍能验证已封存证据，不能依赖长期读Dev。
5. 恢复算法和支持对象清单：选定实际工具及版本；明确schema/role映射、SECURITY DEFINER、函数体字符串、序列/视图/RLS处理。先对代表性复杂对象做技术验证；若必须新增通用SQL解析平台或无法安全转换，报告设计阻塞，不默认开发大框架。
6. 消费者隔离：具体哪些现有scope/角色/运行配置可让新验收记录执行而历史pending不执行；旧记录不能事后加入candidate scope。若现有能力不满足，必须先设计最小可靠方案，否则B5业务验收保持阻塞。

设计复核需覆盖上述六项并引用实际源码/测试，不只审批一段文字。源Dev增加承载表之前，还须列出新版本号、源角色ACL变化和只影响该范围的验收；不因需要克隆隐式升级Dev。

**候选接口与记录要求（以上门槛通过后冻结）：**
- 原schema_migrations仍是唯一迁移执行账本；新表仅记录克隆准入，不能伪装P已在目标重新执行。
- 一次准入记录绑定source/target三元身份、源快照/备份/账本/P摘要、目标结构及ACL摘要、明确角色映射、版本、操作人/时间和验证结果。记录不可被普通运行账号改写。
- control配置必须从受保护文件指定精确目标、预期准入摘要；调用参数中的自声明成功/签名不可信。新增参数名称由实现PR固定并同步CLI help/文档，不发明未实现的运行命令。
- 记录校验共用于runtime、普通迁移、rollback/reset入口；R对克隆目标拒绝。原非克隆路径的检查与负例保持不变。

- [ ] 写失败测试：原P复制到异schema仍拒绝；任意clone布尔值不能放行；缺源证明、目标不符、摘要损坏、inspection错配、超权ACL都拒绝。
- [ ] 实现来源正常升级→一致快照恢复→目标离线验证/写入准入→只读启动检查顺序。新迁移首先在来源按正常规则执行，避免目标在获准前依赖普通迁移自我授权。
- [ ] 目标结构按明确映射逐对象检查；只能归一化获准的schema/角色标识，不可忽略索引、触发器、函数体或策略差异。原P摘要不重新计算覆盖；目标摘要独立记录。
- [ ] 对同一次操作输入实现幂等，同身份不同输入拒绝；记录写入与目标封存状态原子提交。失败目标不启动，保留恢复材料。
- [ ] 验证业务角色无证据读取/写入；目标inspection只读目标所需证据、不读源或业务行；升级新增表后原inspection白名单按最小范围调整。
- [ ] 运行 `go test ./migration -count=1`；用真实PG隔离夹具覆盖受影响准入/迁移入口。只跑mock不满足门槛。
- [ ] 独立审查完成后提交PR；新增迁移导致目标超过047时，明确新目标与源码制品。尚未实际升级时不得把Dev状态写成新版本。

## B3：受控快照恢复和schema映射工具

**Files:** 若B3技术验证证明可复用现有工具，优先复用；确需编排入口时新增 `scripts/migration/clone.py` 及 `scripts/__tests__/test_migration_clone.py`，复用现有profile/CLI结构；更新`docs/migrations/runbook-data-migration-validation.md`。

- [ ] 工具先输出只读plan：来源、目标、快照边界、角色映射、对象清单、数据摘要、消费者状态及拒绝条件；凭据通过受保护文件传入。
- [ ] 保留原备份，生成独立转换产物及摘要。选择能解析对象标识的恢复方式，拒绝简单替换SQL中的public文本。覆盖函数内字符串、默认序列、外键、视图、RLS、触发器和扩展依赖；不能证明的对象阻塞。
- [ ] 在已有获准隔离PG资源建立source/target测试schema，验证一致性快照、完整数据/关联/序列、回执逐字节保留；新增目标记录单独比较。
- [ ] 验证目标runtime/system显式SELECT/INSERT/UPDATE/DELETE Dev对象均拒绝，反向亦然；缺schema、错误schema、错误role及inspection连接不一致全部拒绝。
- [ ] 验证目标更新/迁移只影响target，Dev原数据/结构摘要不变；失败恢复只处理工具拥有的目标对象，不使用未审查DROP CASCADE。
- [ ] 保证目标已存在非空时拒绝；刷新需要保存验证增量、明确停写及新的审核清单，不能把--force当默认行为。
- [ ] 测试无凭据/不一致mapping/同源同目标/部分失败/重复执行；提交独立PR及测试证据。

## B4：Toolkit读写绑定同一目标

**Files:** `scripts/migration/target.py`、`profile.py`、`backfill.py`、`__main__.py`；对应`scripts/__tests__`；后端仅在缺少可验证目标接口时增加受限管理契约，并走独立API审查。

- [ ] profile增加明确schema，SQL使用安全标识符引用和只读事务；禁止字符串插值search_path，禁止public回退。
- [ ] 每次写入前核对API与SQL指向相同database/schema/deployment、选定制品及可信tenant/actor；不得只比较HTTP地址或只在批次开始核对一次。
- [ ] 防止检查后切库：执行批次期间锁定profile切换，服务端写命令核对绑定的目标身份；缺少端到端绑定时--apply继续拒绝。
- [ ] 复用现有实体选择、稳定业务键、tenant scope、失败非零退出及脱敏报告，不另建导入器。
- [ ] 执行 `python3 -m pytest scripts/__tests__ -q` 与 `python3 -m scripts.migration self-test`；测试覆盖SQL指验证/API指Dev时零写入、运行中profile变化拒绝、重试不重复创建。
- [ ] 真实写入之前只做verify/dry-run；关闭apply的保护只能在独立审查和目标绑定验证后移除。

## B5：唯一操作者执行共享恢复、迁移验证和回切

- [ ] 消费唯一清单阶段2完整验收（含A及Change/Service Request等门槛）、B2–B4 PR/CI/独立审查。固定源码制品、准确角色、目标schema、源快照截止点、恢复及回切方案。
- [ ] 刷新Dev备份和附件恢复证据，在已审窗口执行必要新规范迁移；随后制作一致快照，源不放宽权限。
- [ ] 使用受审工具恢复验证schema，写入新准入记录；原P及账本保持不变，原/目标数据摘要与关联校验通过。
- [ ] 核查复制的未决回调/Outbox/外部任务，默认禁用消费者；没有安全筛选范围不得为了验收打开整类历史消费。
- [ ] 五批384对象只作为历史成果证据，按tenant/稳定键重新比较本次目标；一致跳过，缺失/冲突逐项列出，不全量seed，不导入旧tickets，不覆盖身份/密码。
- [ ] 约定验证窗口，切同版3010→8080到完整验证profile；先确认实际目标再运行新合成记录和迁移配置的业务路径，最后回切Dev并核实。健康与行数不替代UI。
- [ ] 生成实际source/target、备份摘要、版本、差异、运行/UI结果及回切证据，回写唯一清单。失败不扩大成数据库重置。

## B6：清理及KAF合并交接

- [ ] 原则变为“两个ITSM数据目标schema”，不是要求保留两个独立ITSM数据库；逐项识别历史库和本次临时恢复资源的消费者、成果、备份恢复证据。
- [ ] 对每个待删对象核对A4失败证据/未决结果依赖；无关历史库不因A4而被无条件阻塞，承载未保全证据的对象不得删除。每个删除对象有具名清单、无依赖证明和独立复核。清单外不删，KAF/Langfuse/系统库不删，共享实例或卷不删。
- [ ] 删除分批执行，每批核查Dev和验证仍可用；停止容器也纳入最终盘点。不以备份文件存在代替恢复成功。
- [ ] 最终交付保留目标、已删对象、备份位置、角色隔离、同版切换及未覆盖业务清单。未决对象如实保留，不报“全部清理”。
- [ ] ITSM/KAF物理实例合并单独交接任务三：传入当前schema布局、新规范迁移版本/制品、角色预算及备份范围，不在本任务移动KAF或宣称G-C通过。

## 交接纪律

Agent B负责代码和隔离验证；独立审查者审核迁移证据、权限及失败恢复；集成人单独执行共享操作。每批只汇报完成/验证/阻塞/下一步。未经验证的运行命令不得写成可直接执行指令；私有DSN、日志、行数据和临时脚本不提交。状态唯一落点仍是原迁移验证清单。

### 运行与验收交接表（执行人逐项填实际值）

PR正文/私有证据需记录以下字段，未取得值时该阶段不得执行：source/target实例、database、schema、deployment；runtime/system/inspection/migration角色；源码完整SHA与制品摘要；源备份/附件恢复证明与截止点；准入摘要与迁移版本；消费者和profile切换锁持有人；本次变更/删除具名集合；失败恢复负责人及步骤。秘密仅记录受保护引用。

最低验收矩阵：原Dev基线仍通过；跨schema及跨租户拒绝；新target首次准入/重试/篡改拒绝；target后续普通升级；source与target独立变更；复制pending不执行且新合成业务可执行；Toolkit错目标与切换竞态零写入；UI迁移配置可用；窗口结束Dev恢复。每项提供运行命令的实际退出码或UI路径证据，未跑不得勾选。

测试资源由协调人分配已有隔离实例/schema，明确owner及可删除对象；没有可用隔离资源时可先做代码/静态分析，不自动新建容器、不把真实Dev作为测试夹具。B2/B3共享`migration/`与注册表，B4共享`profile.py/__main__.py`，由一名实现负责人串行整合；并行Agent不得分别占用相同迁移编号或覆盖这些文件。
