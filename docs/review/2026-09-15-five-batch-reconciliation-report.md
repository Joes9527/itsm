# 五批配置四方只读对账结果

状态：completed；执行 agent 完成，主 agent 已复核只读查询/比较实现，并独立抽查一项一致结果与一项配置缺口。检查时间：2026-09-15 06:47–06:54 UTC（14:47–14:54 CST）。本报告没有将数据一致性升级为 UI、SLA 计时或迁移全范围验收。

## 结论

五批配置在当前目标与重放库之间未发现已纳入对象的丢失、重复或业务字段差异。按固定工具与配置计算的最终应有值，**384 个唯一配置对象在两库均逐对象匹配**。B2 的5字段与B4的7策略已包含在B0的288对象中，不能将各批简单相加算作396个对象。无需重跑五批。

明确配置缺口：**7个流程绑定的 sla_policy_id 都是空字符串，真正非空=0**。它们与原批次要求一致，因此不是迁移丢失；但“7个已有SLA绑定”的此前判断错误，应修正。B4的业务日历仍是原五键结构，19个补班日尚未进入该配置。实际 SLA 计时未测。

## 五批矩阵

| 真实批次ID | 来源/转换 | 应有范围 | 重放/目标匹配 | 缺失/重复/额外 | 判定 |
|---|---|---:|---:|---|---|
| B0-seed-20260914 | 固定0788新规范seed，不是旧ticket导入 | 288对象 | 288/288，两库均唯一 | 0/0/0（受检14表全集） | 最终态符合seed叠加B2/B4 |
| B0-process-20260914 | 固定源码20份新BPMN及7个seed binding；不导入旧BPMN | 47对象 | 47/47 | 0/0/0 | 原批次数据一致，7条SLA空绑定为既有配置缺口 |
| B1-config-20260914 | 旧CTI经已审映射落位 | 46 CI +3分类 | 49/49 | 0/0/0 | 与固定映射一致 |
| B2-options-20260914 | 已采纳字典选项处置 | 5字段，25新增选项 | 5字段、25项均匹配 | 缺项0；全59字段options重复value=0 | 一致；不代表988旧字典全量迁入 |
| B4-calendar-20260914 | 用户选择的新规范日历 | 7 SLA定义，89假日 | 7/7 | 0/0/0 | 原配置一致；补班和计时验收另列 |

B0细分：分类182、模板10、字段59、SLA7、目录8、CI类型9、标准变更3、Known Error占位1、标签4、视图5。Known Error占位不是旧知识迁移验收成果。

## 方法及精确边界

1. 读取固定 `7c8cee6fae181400573308bfb7e73043d11ed0b9` 工具源码，在内存加载其纯构建函数；未运行CLI、protect_batch、迁移器、原重放脚本或任何生成的DDL/DML。
2. PostgreSQL查询全部显式 `BEGIN TRANSACTION ISOLATION LEVEL REPEATABLE READ READ ONLY`，statement_timeout=30s、lock_timeout=2s，限定search_path，正常COMMIT结束。两库分别采样，不宣称跨库原子快照。
3. 使用原工具targets中的稳定选择器按租户/业务编码/模板归属/legacyCtiId匹配；ID只用于库内关联。比较parent、category_ids、field.entity_id、ci_type_id、deployment_id时先解析本库业务身份。
4. 跨库比较保留全部业务字段，排除id、created_at、updated_at、deployed_at、deployment_time这五类制品本地身份/执行时间；排除规则显式记录。没有忽略JSON业务字段、枚举、priority、SLA、XML。
5. 再以原build_dml的每条INSERT字段和值表达式生成**只读SELECT**，得到库内解析关联后的预期值。B0中5字段按原B2追加25项，7 SLA按原B4替换business_hours，检查384个对象的所有原批次写入字段，384/384两库通过。
6. 14张受检配置表总对象384，与逐对象选定集合覆盖一致；不把不在五批内的ticket_types、用户、验收工单等当多余数据。没有读取ticket行。

## 证据链验证

固定运行输入seed：`candidate-approval-contract/itsm-backend/config/seed/default.json`，byte SHA256 `372d605235f45b597e5b4ba256683cdc904a8ea32e4a2939c074e423d89ac191`。

五批均通过：
- 原source canonical SHA256与数据库收据相同。
- 原DML+targets+new_objects+dependencies产生的mapping SHA256与收据相同。
- 历史replay SQL文件byte SHA256与replay-result.json相同。
- 数据库收据post摘要与历史replay-result相同。

当前重放完整post摘要：Process、B1、B2、B4仍与各自收据相同。B0当前摘要 `1da75af17f79a628c6ff34dce6086ea0e75f93151d53222c0ae3c6ae9ab50510` 不等于即时收据 `7dc8d4673e87d5243b052fe26cdd9d928e85901cc429f9c97a837c7f1b4cbb14`。这是B0目标中5字段随后被B2修改、7 SLA随后被B4修改后的中间态差别；最终业务字段逐项全部符合叠加结果，且B2/B4完整post摘要保持不变。**未恢复备份或重构历史时间戳，不能声称独立重现B0即时全行摘要。**

收据包含context/code/database/source_byte摘要等字段；本轮源canonical及mapping/artifact链验证通过，未将外部context JSON（不包含追加byte摘要）直接当数据库context逐字相同。目标无收据不应补造。

## 旧源与映射

源根：`/home/administrator/project/kaf/data/legacy_itsm/test/`；manifest抽取时间 `2026-09-14T06:57:53.013862+00:00`，byte SHA256 `e7aaf0cc815d946b7992166070d52d09c638f84a06baf38049b74e4ed875fe86`。

10份源文件bytes与manifest摘要、数组条数全部一致：CTI82，字典988，优先级15，矩阵86，路由720，模块4，假日611，流程定义145、模型24、部门3175。这里只读计算整体摘要/计数，不导出原数据；不是重新抽取旧系统。

- B1：46个映射legacyCtiId都唯一存在于旧CTI。45名称原样相同，1个名称在移除尾部空白后相同；46路径均与源parentId树重建、节点名称trim并以` / `连接后的层级相同。旧ctipnames原文本29项不同不能直接判为迁移错误。最初差异保留在补充JSON，已由parent图及历史worksheet解释。
- CTI范围：46叶子落CI；7容器不落CI；17组织地点归已有身份阶段；8服务类节点中3新建、4映射已有分类、1父节点排除；4测试/占位节点排除。现有3新增分类及4映射目标由B0/B1逐对象检查覆盖，不实施旧路由。
- B2：以`2026-09-14-dictionary-option-reconciliation.md`已采纳处置和固定ADDITIONS为权威，验证25写入项及最终全部field options。未逐条重裁988字典语义；其余无落点/归并/排除不是迁移遗漏。原处置表存在含替代选项的文字，未擅自扩充具体ADDITIONS。
- B4：89天来自已审新配置2024–2026；现有旧holidays.json实际仅2018–2023，不能说89天从这份旧源直接迁来。历史`2026-09-14-b4-sla-calendar-evidence.md`明确新时段09:00–18:00为用户选择。19补班数据另有文件，原批次有意未表达，当前也未写入。日历代码后续能力与实际计时另行验证。
- 不迁旧ticket、历史流程/审批/评论/附件；未读取相关行或执行UI业务写入。

## 观察窗口

两库PostgreSQL17.10。
- replay 06:47:28.038880 UTC，连接聚合1；target 06:47:28.146915 UTC，连接聚合2（包含检查连接）。
- 首轮前后14表全行摘要均稳定：replay `e84cac07dd7a8b5e24815c6061c8a72c08360664650f0b433e084a9dc1eb9897`；target `f47bd8a63101c990daa208c04f79d8afca4103135d47bae80f74d6d9726a082b`。
- 补核06:49及后续各自新只读快照全部384预期通过。没有跨库同步锁，短暂先改后恢复无法从端点摘要检测；不声称整个应用无人写入。

## 可复核产物

- `/tmp/itsm-five-batch-reconciliation-20260915.json`：逐批摘要链、范围、匹配数、首末指纹。
- `/tmp/itsm-five-batch-supplement.json`：源文件manifest、384预期值检查结果、B2全量25项、SLA空绑定及旧CTI路径解释。
- `/tmp/itsm-five-batch-readonly.py`、`/tmp/itsm-five-batch-supplement.py`：可通过SSH stdin执行的只读检查脚本。脚本只在远端内存读取受检原数据，输出摘要和脱敏差额。
- WSL证据根 `/home/administrator/.local/state/itsm-task2-remediation-20260914/`：replay-result.json、replay-batches.py（**只读，不运行**）、replay-{B0,Process,B1,B2,B4}.sql（**不执行**）及preflight。
- 固定工具docs/review：`2026-09-14-b1-landing-evidence.md`、`2026-09-14-cti-mapping-worksheet.md`、`2026-09-14-dictionary-option-reconciliation.md`、`2026-09-14-b4-sla-calendar-evidence.md`。

独立复核建议：用父级已执行的B2邮箱两项作为一致抽样；差额抽样查询binding同时统计NULL、trim空字符串、真正非空（预期0/7/0）。若重跑脚本验证本报告，只执行只读脚本，绝不执行历史SQL或replay-batches.py。

## 下一动作

1. C1可记已纳入五批最终配置一致；不要写“全部旧主数据/完整UI已迁移验收通过”。
2. 修正C3：7条SLA绑定空，19补班未进入目标；按获准范围准备定向配置和计时验收，不重跑整个Process或B4。
3. 保留R1运行准入和V1–V5真实UI验收项。数据一致不能替代readyz修复或专业流程验收。
4. 归档这些脱敏证据后，数据库清理另按备份覆盖/依赖/明确对象执行；本轮不删除任何库。

本次没有数据库变更、授权变更、恢复、清理、迁移、切库、重启、重新抽取或Git修改。Dev未连接写入。

## 最终结束核对

2026-09-15T06:52:47.861832+00:00：同14表选择范围的完整行摘要与首轮一致，两库均稳定。首轮窗口06:47:28–06:47:30与此结束快照分开采样，不声称持续监控。最终JSON已合并384预期检查与来源补核。

只读脚本SHA256（仅脚本代码，无原始配置/数据）：
- `/tmp/itsm-five-batch-readonly.py`：`e1ea362e1db83435a1eec946c2dc26b5e21715f503193557e361834600f97ca5`
- `/tmp/itsm-five-batch-supplement.py`：`175cf7ea880eb6ea549adb73f6932391ee813a26dd2271c95c77d24bd5a5a45a`
- `/tmp/itsm-five-batch-final-window.py`：`3b6e6a2dd1449b7944059c40bb641b95396031cb0c6ce66a320358321c0ad86a`

## 主 agent 复核与续办入口

- 已阅读固定batch_receipt.py、B2/B4生成器及执行agent只读脚本，核对源canonical、SQL映射、文件byte、PostgreSQL全行摘要的不同口径。
- 独立查询两库邮箱模板operation字段：各唯一1条，群组邮箱与公共邮箱两项均存在。
- 独立查询当前目标process_bindings：总7、NULL0、空字符串7、真正非空0；已撤回此前仅查NULL产生的错误判断。
- 原批次中间态不与最终态强行比摘要；未重构历史时间戳、未恢复备份，保留上述验证边界。
- 机器可读脱敏证据：[逐批摘要与来源补核](2026-09-15-five-batch-reconciliation-report.json)。临时只读脚本路径及哈希保留供本机复核，不把临时脚本作为产品代码提交。
- 后续工作只维护[唯一续办清单](../superpowers/plans/2026-09-15-migration-validation-ledger.md)，本报告为完成的对账快照。
