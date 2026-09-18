# 当前 DEV 连接及 WorkItem 数据保留核对

> **最终范围决定（2026-09-15，用户确认）**：当前 DEV 的 26 条 WorkItem 及其专业扩展、关联评论/附件/关系不纳入目标迁移；旧流程实例和任务不迁移、不续跑。原 DEV 数据原样保留，不删除、不清理。本报告的保留候选与依赖适配分析作为历史核对证据，不再作为待执行导入范围。目标复用 `itsm_ga_ready`，只准入所需配置和基础数据，E2E 新建业务记录验证。

日期：2026-09-15。范围：只读进程、连接配置、PG 会话、表结构及聚合关联检查。未切换连接、未更新源或目标、未启动/停止服务、未执行迁移。查询为多次只读快照，不是停写或一致性导出证明。

## 先纠正库的用途

**当前 DEV Backend 使用 `itsm-postgres-dev / itsm_config_baseline_20260908 / public`，不是 `itsm_ga_ready`。**

证据：8080监听进程PID147607，实际二进制 `itsm-api-ui-workbench-7ed97de4`，工作目录 `/home/administrator/.local/state/itsm-kaf-baseline-20260908/config/itsm`；config.yaml的非敏感连接字段为127.0.0.1:5432、dbname=itsm_config_baseline_20260908、user=itsm_base_app_20260908，进程DB_SCHEMA=public。PG观察到同库的itsm_base_system_20260908两条连接。3001前端来自ui-workbench-runtime工作树。运行代码与之前G-A固定制品不是同一名称修订，后续切换须固定实际使用代码，不能用历史DEV文档推断。

`ga-itsm-20260914 / itsm_ga_ready` 是新建结构后选择性导入身份和配置的目标，不是DEV完整克隆。本轮会话抽查无应用连接；此前最新核验tickets=0、ticket_types=0。

## 三个库的数据形态

| 库 | 账本 | WorkItem基础行 | 专业扩展 | 结论 |
| --- | --- | ---: | --- | --- |
| 当前DEV：itsm_config_baseline_20260908 | 24条，031 | 26 | Incident1 / Problem1 / Change0 / RequestedItem12 | 已完成核心WorkItem重构，仍需对齐目标046字段及配置引用 |
| 旧开发库：itsm | 14条，019 | 18 | Incident2 / Problem2 / Change7 / ServiceRequest14 | 混合旧结构，不作为当前DEV数据默认来源 |
| 旧开发克隆：itsm_migration_20260914 | 14条，019 | 18 | 同上数量 | 同样是旧结构；不要与当前DEV混为一谈或重复导入 |

## 当前DEV：哪些符合新模型

- 26条基础记录：generic12、service_request_item12、incident1、problem1；无Change。
- Incident/Problem/RequestedItem扩展的缺基础记录、错误record_class、重复基础引用、基础缺对应扩展均为0；共享字段已从专业扩展移除。
- 基础记录中非空requester/opener/assignee引用均能找到同租户用户。
- 12条generic的generic_subtype均为ticket；new/open/in_progress等状态不因存在open就被判旧结构，目标生命周期代码仍支持open。
- 现有ticket_types为12条，代码为新产品现有12个默认code，且与目标表列名集合一致。应优先核对并保留现有配置内容，而非假定源没有配置、必须重新生成；列名/code相同不等于全部内容完全一致。

这些验证支持将26条记录列为**保留候选**，不证明已可原样导入或完整业务验收通过。记录来源含manual、service_catalog、kaf_web、http；未审阅业务正文来认定每条均为人工测试数据。

## 仍需适配的部分

1. 当前DEV tickets没有目标的applied_sla_policy、sla_cycle_number、sla_cycle_started_at、sla_paused_minutes；源剩余problem_tickets列虽已不使用（非空0），也不应复制到新模型。Problem缺少新验证证据字段；这些字段不能伪造为已验收/已验证。
2. 2条工单引用分类124：目标有相同code，但原ID含义不同，必须按code重映射。1条工单引用SLA3：目标有同名定义，但原ID含义不同，须核对完整规则再映射。
3. 12条服务请求引用7个目录：14(3条)、26(4条)、27–31(各1条)。这些目录在目标均无同名项，不能按ID关联目标，也不能因为无同名项就断言无业务等价项；需明确补齐或映射目录及其依赖后保留服务请求。
4. 关联数据现有comments12、attachments6、WorkItem关系1；若保留测试记录应一并审定关系和附件实体文件范围，不能只拷tickets产生残缺用例。
5. 流程实例26（completed8、terminated12、running6），流程任务32、callback outbox1、notifications58。它们是状态数据；不能未经执行上下文核验直接激活。现有“不迁历史流程/附件”范围与新提出的DEV测试记录保留需明确区分，本文只提出候选范围，未实施扩展导入。

## 旧itsm及其旧克隆的具体不符合项

两库均有Incident2、Problem2、Change7缺少关联WorkItem；14条ServiceRequest中12条关联的基础记录仍是generic。专业扩展仍保留重复title/status/priority/tenant等字段；旧账本还有退役010及重复编号历史问题。即使tickets已出现record_class列，也不能据此认定完成新模型适配。

## 原保留建议（已被上述用户决定替代）

- 以当前DEV库为唯一默认来源，将26条WorkItem及14条专业扩展列入保留候选；不混入旧itsm/旧克隆的另一批记录。
- 12条产品类型配置先做内容对照，优先保留；补齐/映射真实被引用的分类、SLA和7个目录。无需因此再建一套测试记录。
- 评论/附件/关系及流程状态按用例完整性另列清单；未完成对象文件、执行上下文和状态适配前，不称其可完整运行或续跑。
- 在已有itsm_ga_ready落位前，固定代码、字段映射、ID映射、数据范围及回退证据。当前DEV连接和数据保留不变。本轮只读核对不等于导入批准、G-B通过或连接切换。

## 可复查证据

受保护目录：`/home/administrator/.local/state/itsm-dev-data-audit-20260915/`，包含进程非敏感连接摘要、tables.json、integrity.json、preservation-scope.json、details.json。仅保存结构、计数及关联标识；没有导出密码、工单正文或附件内容。原始只读脚本未执行任何DML/DDL。
