# WorkItem 受控退役设计审阅报告

日期：2026-09-11。状态：reviewed，原审阅问题经修订和独立复核关闭；维护者授权进入实施计划，不是环境变更授权。

审阅对象：[详细设计](../superpowers/specs/2026-09-11-workitem-controlled-retirement-design.md)。基线 23154cfb669a10a9742b88a953aba63218d976b0，审阅提交 fab60168e173d0daa822bddadafa097a22d86f95。

方法：依 requesting-code-review 分派无会话历史的独立审阅者，主审复核代码证据。WSL 工作树 /home/administrator/project/itsm/.worktrees/workitem-next-stage。下列代码位置均相对此目录及审阅提交。

## 原稿审阅结论

无 Critical 发现；两项 Important 契约缺口需在进入实施计划前修订。P/R 方向不需推翻。保留原 SQL/checksum/真实回执、切换后不双写、精确删除及最终恢复点等原则符合既有 WorkItem 约束。

## Important 1：区分 P 事务内验证与后续完整业务验收

设计第 43–47 行规定先 P 后其余普通迁移，第 72–73 行却要求三域创建、更新、查询及 Requested Item 回归后才提交 P。对合法旧前缀至 021、缺后续结构的环境，这一表述会形成依赖循环。

证据：itsm-backend/migrations/20260910_problem_investigation_completion.sql:2-6（034）新增 verified_version、verification_digest、verified_by、verified_at、verification_note；当前 itsm-backend/ent/problem/problem.go:48-60 默认列集合包含这些字段，ent/problem_query.go:449 使用该集合。因此尚无 034 结构时，当前应用的普通 Problem 查询无法通过。

建议：P 事务内只核验本阶段结构、约束，以及真实业务角色的受限 SQL 写入与 RLS；P 提交后执行获准普通迁移，再完成当前应用的三域、共享能力、Requested Item 和 V1 验收。仅 P 成功不能表示应用就绪；完整验收通过后才恢复就绪与观察。不得把后续迁移复制到 P。

验证输入：使用至 021 的合法历史画像，证明 P 无需 034 字段即可完成自身验证；普通迁移未完成前应用不就绪；其后再运行完整业务旅程。

## Important 2：将回滚与重置纳入阶段依赖门禁

设计第 39 行列举 bootstrap/up/status/dry-run/ApplyMigration，但遗漏 down/reset/直接 RollbackMigration。第 103 行只约束 R 自身不提供 down，不能阻止其前置迁移被撤回。

证据：itsm-backend/migration/migrator.go:286-305 直接执行传入 rollback SQL 并删除 schema_migrations 回执，没有反向依赖检查；itsm-backend/cmd/migrate/main.go:473-498 的 resetMigrations 会跳过无 rollback SQL 的迁移，再回退其他版本。保留 P/R 成功回执而撤销其前置迁移，会使结构与账本矛盾。

建议：所有迁移写入口复用阶段、目录与依赖门禁；存在已提交后继阶段时，拒绝撤销其前置迁移。reset 在执行首个修改前检查整体计划，不得通过跳过不可逆阶段继续撤销依赖。直接 RollbackMigration 同样必须校验。

验证输入：P/R 已提交后，直接回退其前置版本和 reset 均应在任何 DDL/回执修改前拒绝，结构与账本保持不变。

## Minor：明确启动门禁位置

设计第 43、51 行已有先只读盘点和旧账本验证要求，建议进一步点明该步骤必须在 Prepare/CreateSchema/账本结构升级等任何写入之前。现有 itsm-backend/migration/bootstrap.go:52-60 先 Prepare/CreateSchema 再执行迁移校验；internal/bootstrap/app.go:1050-1089 还注册了多个预处理函数。此项属于实现顺序澄清，不另计阻塞问题。

## 边界与下一步

本次只读核查设计、迁移目录、相关 SQL、Ent 查询、启动与回滚入口；git diff --check 通过，WSL 工作区干净。未运行测试、迁移或数据库变更；本结论不代表运行验证通过。

原稿审阅阶段未直接修改设计。随后维护者授权“修订后再进入实施计划”，修订及复核如下。

## 修订与独立复核闭环

2026-09-11：P内只验证本阶段结构及受限SQL/RLS，P后完成其余迁移再验收当前应用；后续失败保留真实P回执。down/reset/直接RollbackMigration纳入反向依赖门禁，reset首次写入前验证全计划。只读目标分类在Prepare/CreateSchema/账本升级前完成。

独立原审阅者复核修订稿，确认两项Important和bootstrap澄清均已关闭，未发现新增Critical/Important矛盾，可进入实施计划。此结论仅针对设计，不代表实现或运行验证通过。

本次设计审阅当时，[实施计划](../superpowers/plans/2026-09-11-workitem-controlled-retirement.md)已编制，六批代码任务尚未执行。后续代码与隔离验证、复审和剩余门禁已记录在该计划；本段保留历史时点，不作为当前未完成清单。
