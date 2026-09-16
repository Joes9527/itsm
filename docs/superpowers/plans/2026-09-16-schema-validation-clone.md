# 同库schema验证克隆实施与交接计划

> **For agentic workers:** Use executing-plans task-by-task. 用户自行分派Agent；只读调查/隔离实现与共享操作分别交付，不能把本文件当无条件清库命令。

**Goal:** 在现有ITSM数据库中建立可追溯验证schema，与Dev隔离，复用迁移工具，最终退出无依赖的历史ITSM目标。
**Architecture:** 一个PG实例中的ITSM数据库使用Dev public和验证migration_validation两个schema；原历史回执保留，新目标通过独立克隆准入证明接受。长期KAF保持自身逻辑数据库，沿用任务三合并实例。
**Tech Stack:** 现有Go Migrator/runtime inspection、PostgreSQL、Python migration Toolkit、WSL stack profile。
**Status:** design ready for implementation/review；新准入契约需独立安全/迁移复核后才可共享执行。

## 全局约束与依赖

先读AGENTS.md/CLAUDE.md、工程治理、DEVELOPMENT_GUIDE、development-environment、[设计§7](../specs/2026-09-16-dev-restoration-two-database-design.md#7-接续设计执行-agent-的裁定与门槛)、[唯一清单](2026-09-15-migration-validation-ledger.md#two-database-execution)及[既有ITSM/KAF合并计划](2026-09-14-itsm-kaf-task-3-pg-consolidation.md)。独立worktree从最新main建立；本计划链接的PR46若未合并先确认文档提交。

- 不新增常驻PG容器。临时测试优先使用已批准恢复资源，先确认无其他Agent占用；隔离测试schema也必须具名且受控，不能通过默认DSN碰Dev。
- Dev当前 `itsm-postgres-dev / itsm_config_baseline_20260908 / public`，047/39真实回执；验证schema建议`migration_validation`，创建前核名。当前事实需重新读取，不凭文件名判环境。
- 不移动Dev public，不改旧SQL/校验和/P内容或伪造迁移执行，不使用Ent补表，不运行R038。
- 不导入旧ITSM ticket；新系统Dev测试数据属于克隆范围。角色身份及权限也必须纳入备份/恢复验证。
- 3010→8080只运行一个获准profile；禁用外发并分别配置缓存、附件及会话。复制来的pending任务/队列默认不消费。
- Agent A新generic流程验收通过是共享克隆的前置；旧冻结失败仍明确保留为未结项。B不得擅自修复/删除它。

## B1：核定同schema隔离的全部入口

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

**接口与记录裁定：**
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

**Files:** 新增 `scripts/migration/clone.py` 及 `scripts/__tests__/test_migration_clone.py`，复用现有profile/CLI结构；更新`docs/migrations/runbook-data-migration-validation.md`。

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

- [ ] 消费Agent A验收、B2–B4 PR/CI/独立审查。固定源码制品、准确角色、目标schema、源快照截止点、恢复及回切方案。
- [ ] 刷新Dev备份和附件恢复证据，在已审窗口执行必要新规范迁移；随后制作一致快照，源不放宽权限。
- [ ] 使用受审工具恢复验证schema，写入新准入记录；原P及账本保持不变，原/目标数据摘要与关联校验通过。
- [ ] 核查复制的未决回调/Outbox/外部任务，默认禁用消费者；没有安全筛选范围不得为了验收打开整类历史消费。
- [ ] 五批384对象只作为历史成果证据，按tenant/稳定键重新比较本次目标；一致跳过，缺失/冲突逐项列出，不全量seed，不导入旧tickets，不覆盖身份/密码。
- [ ] 约定验证窗口，切同版3010→8080到完整验证profile；先确认实际目标再运行新合成记录和迁移配置的业务路径，最后回切Dev并核实。健康与行数不替代UI。
- [ ] 生成实际source/target、备份摘要、版本、差异、运行/UI结果及回切证据，回写唯一清单。失败不扩大成数据库重置。

## B6：清理及KAF合并交接

- [ ] 原则变为“两个ITSM数据目标schema”，不是要求保留两个独立ITSM数据库；逐项识别历史库和本次临时恢复资源的消费者、成果、备份恢复证据。
- [ ] 先解决A4旧失败实例处置及未决结果保全；每个删除对象有具名清单、无依赖证明和独立复核。清单外不删，KAF/Langfuse/系统库不删，共享实例或卷不删。
- [ ] 删除分批执行，每批核查Dev和验证仍可用；停止容器也纳入最终盘点。不以备份文件存在代替恢复成功。
- [ ] 最终交付保留目标、已删对象、备份位置、角色隔离、同版切换及未覆盖业务清单。未决对象如实保留，不报“全部清理”。
- [ ] ITSM/KAF物理实例合并单独交接任务三：传入当前schema布局、新规范迁移版本/制品、角色预算及备份范围，不在本任务移动KAF或宣称G-C通过。

## 交接纪律

Agent B负责代码和隔离验证；独立审查者审核迁移证据、权限及失败恢复；集成人单独执行共享操作。每批只汇报完成/验证/阻塞/下一步。未经验证的运行命令不得写成可直接执行指令；私有DSN、日志、行数据和临时脚本不提交。状态唯一落点仍是原迁移验证清单。
