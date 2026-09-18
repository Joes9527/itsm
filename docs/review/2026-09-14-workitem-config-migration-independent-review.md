# 任务二独立审查：配置迁移交付

- 日期：2026-09-14；实库只读复核时间 22:05 CST。
- 状态：reviewed，**需要修正后复审；G-B 保持 BLOCKED**。不建议将当前工具作为安全可复用的迁移入口，不构成合并、启动应用或任务三切换准入。
- ITSM 审查范围：`660087795e6efe49e89b759d2527ad1b8320a651` → `82ca81878d3731801a61b278ed3b89c5ab69f6dc`。
- KAF 审查范围：`184f7868161794bd549a6fbe2d46e41fe0de854e` → `67928a8b5dcd1482a02386cad7d37dd0ba42a761`。
- 消费 G-A：`d91b587fe3ab40cc863321346d217d258a3a96d8`；目标 `ga-itsm-20260914 / itsm_ga_ready / public`。
- 独立审查者：`/root/gb_itsm_review`、`/root/gb_kaf_review`；主审 `/root` 负责固定提交、实库不变量、备份与结论复核。上述审查者均非任务二实现者。
- 全程未修改任务二 checkout、未执行生成 SQL、未启动应用、未调用旧企业 API；实库采用强制只读连接。离线测试／复现与主审报告存放在独立位置。

## 审查结论与当前数据事实

交接标记 BLOCKED 是正确的，B3/B5 能力差额、B4 截止计算和新建业务验收没有完成，不应提高门禁状态。以下代码缺陷独立于这些已知产品差额，不能用“G-B 本就 BLOCKED”免除工具修复。

主审实库重新读取的数量与交接一致：分类185、模板10、字段59、SLA7、目录8、CI类型9、CI46、标准变更3、Known Error占位1、标签4、视图5、流程部署／定义20/20、绑定7。规则表、ticket_types、tickets、incidents、problems、changes、process_instances 均为0。

- Phase 1 **13 张表的整行内容摘要**均与 G-A 快照一致，不只验证了行数。
- 36 条迁移的版本、checksum、evidence_digest 与 G-A 完全一致；无 R038。
- 固定候选源码检查器的 runtime structure 与 tenant/system/inspection 角色准入均 exit 0。
- 7 条 SLA 均存储 89 个假日；仅证明数据存在，不证明运行时计算正确。
- 5 份批次前 pgdump 文件存在，实际摘要与对应证据记录一致。本次未进行恢复；事务 ROLLBACK 预演也不能被称为 pgdump 恢复验证。
- 当前59条字段全部属于 `ticket_template`，185条分类全部属于tenant 1。因此 R2/R3 所述冲突条件未在当前目标中出现；没有证据表明这次已写入数据发生跨租户／跨实体污染，不据此要求回滚现有批次。

## 必须修正的问题

行号均对应上述固定提交，链接到本地实现 worktree 便于定位；分支后续变化须按 SHA 重新查看。

### R1 [P1] 克隆重建路径可能删除源数据库

位置：ITSM `scripts/clone_itsm_migration_db.sh:139`，相关校验在48/65、存在性查询76、CREATE在110/116。

脚本允许大写标识符，目标存在性和源目标相等检查区分大小写，DROP/CREATE SQL 却没有引用标识符。当数据库 `"ITSM"` 与 `itsm` 同时存在，使用目标 `ITSM`、源 `itsm`、`RECREATE_INCOMPLETE=1`，脚本会执行 `DROP DATABASE ITSM;`；PostgreSQL 将其折叠成小写 `itsm`，可能删除源库。独立审查用 fake-docker 在临时目录复现了命令路径，没有连接真实数据库。

修复：统一限制可接受库名为小写，或正确引用全部数据库标识符并按实际数据库身份校验源／目标不同。新增大小写冲突回归用例。修正前不得使用该重建路径。

### R2 [P1] 分类冲突可静默形成跨租户引用

位置：ITSM `scripts/migrate_config_seed/generate_seed_sql.py:66–74`；B1 `generate_b1_sql.py:49–50` 同类。

分类 code 在实际 schema 为全局唯一，但生成器 `ON CONFLICT(code) DO NOTHING` 不验证既存记录的租户／内容，父分类和模板分类查找又只过滤 code。已有tenant 2同code分类时，对tenant 1运行会跳过目标分类并把tenant 1模板关联至tenant 2分类。非owner运行时可能拒绝访问，并不能补救owner迁移写入的错误引用。

修复：按已批准映射验证既存记录的租户、稳定身份和内容；code冲突属于其他租户或内容不符时整批失败。父分类／模板关联必须同租户。不要把全局唯一约束视作租户隔离检查。

### R3 [P2] B2 字段更新遗漏 entity_type

位置：ITSM `scripts/migrate_config_seed/generate_b2_sql.py:63–65`。

`field_definitions` 同时承载模板和目录字段；模板／目录ID可以相同。UPDATE仅过滤tenant、name和entity_id，同租户同ID同字段名的 `service_catalog` 字段也会收到模板选项。

修复：明确 `entity_type='ticket_template'`，并校验预期对象唯一且存在。增加模板／目录ID相同的语义回归测试。

### R4 [P2] 缺少声明的源摘要和原子检查点，不能保证冲突恢复

位置：ITSM `scripts/migrate_config_seed/generate_seed_sql.py:76–82`、`generate_b4_sql.py:28–29`；声明见 `docs/migrations/2026-09-14-legacy-config-migration-playbook.md` §5。

五个生成器没有实现手册承诺的源／映射摘要检查和成功检查点原子提交。相同SQL重跑0新增只覆盖一种重试：B0遇同名但内容变化静默跳过，B4则覆盖目标租户所有SLA，包括首次执行后新增或修改的日历。无法识别“同批重试”“源映射变更”或“目标冲突”；提交后回执丢失的恢复声明没有实现证据。

修复：固定批次身份、源／映射／目标摘要和写入对象集合，复用或建立审查后的持久化检查点机制，与成功写入原子提交；变化时拒绝自动重放。增加提交后回执丢失、目标内容变化及B4额外SLA的回归测试。不要仅靠扩展行数检查称为完整恢复契约。

### R5 [P2] 身份抽取不完整仍可成功，影响 void 判定

位置：KAF `scripts/fetch_legacy_identity_objects.py:128–131`、137–143。

底层分页遇空页可提前返回 `len(rows)<total`，调用方仍写文件并进行身份解析；`--skip-extract` 也静默接受缺失的身份文件。离线复现第一页1行／total2、第二页空，脚本继续；身份目录不存在时离线模式返回0并将fixture有效用户判为unresolved。后续若将unresolved视为已删除，会误排除路由。

修复：身份资源也需manifest、来源／时间／摘要／total／分页完整性和唯一ID验证；缺失或不完整时阻塞解析及永久void结论。**未据此证明本次12,387用户抽取实际截断**：这是一项已复现工具风险。本地身份目录只有三份JSON，缺少固定身份manifest。

### R6 [P2] 身份缓存固定 test，无法防止跨来源混用

位置：KAF `scripts/fetch_legacy_identity_objects.py:149`。

`out_dir = Path(args.out_dir) / 'test'` 不随主抽取环境／URL变化；换环境复用会覆盖缓存，离线模式也无法拒绝将其他环境的授权表与test身份数据关联。

修复：与主抽取共享可验证的source/environment标识，并在解析前匹配授权manifest与身份manifest；目录名本身不是身份凭证。

### R7 [P2] 空父资源漏报孤立引用

位置：KAF `scripts/report_legacy_config_conflicts.py:179–182`，流程引用附近196同类。

`if not child_rows or not parent_rows: continue` 把“父资源为空”当作无需检查。离线输入一条引用missing优先级的矩阵行与空priority_levels，返回references空、blockers=0。父资源rows=total=0可以是有效抽取，前置摘要验证不能补救。

修复：区分未取得资源与已取得空资源；子资源有引用而父资源空时，所有相应引用均须报孤立并阻塞。

### R8 [P2] 永久 void 缺精确名单且计数未去重

位置：ITSM `docs/migrations/2026-09-14-legacy-config-migration-playbook.md:124–125`；`docs/review/2026-09-14-b5-open-items-resolution.md:38–40`。

文档要求后续永久排除20用户ID和14 CTI，但只提供计数，链接的处置文档同样无确切名单或可定位摘要。对现有本地dump按路由行计算，缺用户68行与缺CTI118行**重叠4行**，应排除182行、剩余**538行**，不能直接720−68−118得到534。用户批准void无需重问，但必须冻结批准所对应的确切对象集合。

修复：保存不含姓名／邮箱的稳定ID及来源／租户／摘要、对应路由集合，按并集计数；保留失效依据，不将未来同名／相似对象自动套入旧批准。

## 交接元数据补正

最终交接§2同时出现 c0f102d5、a4283cdb 和“以HEAD为准”，与本次提交82ca8187不一致。下一版应固定完整ITSM／KAF审查SHA及唯一交接修订的定位方法；不要用浮动HEAD指代历史结果。GBRevision是ITSM交接提交，不是两仓各一个运行SHA。此项不改变BLOCKED结论，但必须在交给任务三前消除歧义。

## 测试和证据覆盖

- ITSM新增相关离线Node测试29/29通过；SQL字符串和fake-docker检查没有覆盖上述数据库语义。
- KAF新增测试27/27通过，使用 `pytest --noconftest -p no:cacheprovider` 隔离既有应用配置；默认conftest存在Settings环境缺失，未连接企业环境补齐。
- 配置源manifest `fetched_at=2026-09-14T06:57:53.013862+00:00`、env=test；10个资源的实际JSON行数与manifest rows/total及SHA256一致。身份资源完整性证据单独存在R5/R6差额。
- 固定0788 seed字节摘要与B0文档一致；KAF新增6文件与固定67928a8b一致。
- 主审实库及备份摘要证据：`/home/administrator/.local/state/itsm-task2-independent-review-20260914/live-verification.json`、`scope-check.json`、`runtime.log`、`roles.log`；仅记录计数／摘要和操作身份，不提交源数据或凭据。

## 对下一步产品决策的建议（尚未执行）

1. **先修R1–R8并复审**，当前已写入数据保留；不能用产品扩展掩盖迁移工具缺陷。
2. **B3：补产品侧持久化优先级策略**，按tenant、recordClass和规范impact/urgency配置，建立API与审计；旧模块映射到现有专业类别，避免另建旧模块权威或第二套分类器。不要把86条旧矩阵直接塞进不支持条件的规则JSON。
3. **B5：沿既有规则／BPMN／RBAC扩展**。先将旧CTI映射到已准入分类或CI，再核对流程绑定／任务节点和准确用户身份；角色动作必须有明确候选人解析、租户及空候选失败语义，不新增审批引擎。9条只是待评估上限，不是已准入可写集合。
4. **B4：优先补时区和日期级工作／休息例外**，用于中国周末补班；09:00–18:00已由用户选定，午休／分段可作为独立能力需求，不因为旧系统有午休就擅自撤销新时段决定。仍须验证跨日、节假日、补班和恢复后的截止计算。
5. **ticket_types：走产品规范初始化**，不做旧数据迁移。固定0788的创建路径会查询该配置；先核对默认项与generic subtype及专业recordClass的现行合同，审查最小初始化批次，不能直接因空表而重跑全量seed。
6. 能力和必要配置满足后，用新建可追踪记录做G-B功能／权限验收；再发布新GBRevision。此前不进入任务三搬迁／切换。