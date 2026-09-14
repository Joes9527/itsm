# 旧 ITSM 配置主数据迁移：GAP 与解决方案设计

- 状态：draft，供维护者复核；不是数据库执行授权。
- 日期：2026-09-14。
- 上游：总设计 [`2026-09-14-itsm-kaf-database-convergence-design.md`](2026-09-14-itsm-kaf-database-convergence-design.md) §5；执行入口 [`任务二计划`](../plans/2026-09-14-itsm-kaf-task-2-config-migration.md)。
- 范围：**仅配置主数据**。不含历史工单/审批/评论/附件/流程实例、旧 BPMN、知识库、组织/用户全量重导、ITSM R(038)。
- 门禁：目标写入仍受 **G-A** 约束。本文只定义方案；`GARevision` 未交付前不产生任何目标写入。

## 1. 数据权威与来源

| 侧 | 来源 | 角色 |
| --- | --- | --- |
| 旧 | 只读抽取 dump `kaf/data/legacy_itsm/test/`（来源 `https://keas-itsm-test.gazellio.com`，`fetched_at=2026-09-14T06:57:53Z`） | 映射来源，非权威 |
| 新 | ITSM `ticket_categories` / `sla_definitions` / `field_definitions` / `ticket_assignment_rules` / `ticket_types` 等 | **权威**；目标写入受 G-A 门禁 |

只读证据（KAF 仓库脚本）：
- 抽取完整性：KAF `scripts/verify_legacy_master_data.py`，10/10 资源 `ok`、sha256 与行数一致。
- 源侧冲突：KAF `scripts/report_legacy_config_conflicts.py`，blockers=8 / conflicts=13。
- 差异报告：`itsm/docs/migrations/2026-09-14-legacy-vs-new-itsm-diff.{md,json}`（名称匹配口径，仅作数量级参考）。
- CTI 差异清单：`itsm/docs/review/2026-09-14-cti-mapping-worksheet.md`。

## 2. 已确认决策

| # | 决策 | 结论 |
| --- | --- | --- |
| D1 | 分类树归属 | **以新 ITSM `ticket_categories` 为唯一权威**；旧 CTI 仅作映射来源；差异清单交业务确认映射；未命中的旧节点按层级补建 |
| D2 | 优先级/SLA 数值 | **以新 `sla_definitions` 为准**；旧 P0–P3 仅映射到 `urgent/high/medium/low`；旧响应/解决分钟作为差异记录，不迁移；清理新库重复 SLA 与脏优先级词汇 |
| D3 | 节假日日历 | **使用现有 `sla_definitions.business_hours` JSON**（`work_days/start_time/end_time/time_zone/holiday_list`）；补齐 2024–2026；不新增日历表 |

## 3. GAP 台账与解决方案

| # | 现状（旧 → 新） | GAP | 解决方案 |
| --- | --- | --- | --- |
| G1 | CTI 82 节点 3 层（`ctiId`）→ `ticket_categories` 183 节点（9/38/136） | 归一化名称重叠 0；无稳定映射键；14 条路由引用 `ctiId` 不在旧树 | 建 `源 ctiId → 目标 category.id/code` 映射表（业务确认）；未命中旧节点按层级补建，回填 `code/itsm_type/default_priority/sla_tier`；缺父引用阻塞相关批次 |
| G2 | 配置字典 988 项/183 组（全局）→ `field_definitions` 131（模板 122/目录 9） | 结构不同（全局字典 vs 每实体字段选项）；label 覆盖率 <1% | 仅迁移**在用**字典（需业务提供在用清单），落位到对应字段 `options`；无目标字段的字典组列为显式范围差额 |
| G3 | 优先级 P0–P3（15 行，按模块）→ 三套词汇并存 | `sla_definitions.priority=urgent/high/medium/low`、`category.default_priority=P1–P4`、`template.priority=low/medium/P2/P3/P4` | 统一映射：P0→urgent/P1、P1→high/P2、P2→medium/P3、P3→low/P4；修正 `ticket_templates.priority` 脏值 |
| G4 | 旧 SLA 分钟按模块不同 → 新 7 条有效（id 1–7）+ 7 条重复（8–14） | 数值差异；重复数据 | 以新 SLA 为准；旧分钟数记差异不迁移；去重保留 id 1–7 |
| G5 | 优先级矩阵 86 条（影响×紧急→优先级）→ 无矩阵表 | 无直接落点 | 转 `ticket_automation_rules`（conditions=影响/紧急，actions=设优先级）；不能落地的组合显式列出 |
| G6 | 路由 720 条（`ctiId,taskDefKey`→`authorizedType,authorizedId`+`definitionId`）→ `ticket_assignment_rules`(空) | 结构不同；`authorizedType∈{0,2}` 语义未知；14 孤立 `ctiId` | 转 `ticket_assignment_rules`（conditions=分类，actions=指派）；未知枚举与孤立引用阻塞相关批次，不猜测语义 |
| G7 | `holidays` 611 天（2018-08-05..2023-12-31）→ 无日历表，`business_hours` 全空、`exclude_*` 全 false | 源日历过期；新系统日历未配置 | 写各 SLA 的 `business_hours`（工作日/时段/`holiday_list`）；补齐 2024–2026；`exclude_holidays/exclude_weekends` 在截止计算中未消费，保持显式说明 |
| G8 | ITIL 模块 4（IN/SR/KN/SERVER）→ `ticket_types` 12 + `recordClass` | 语义层不同 | 建 ITIL 模块 → `recordClass`/`ticket_type` 对照；知识(KN)不属 WorkItem |
| G9 | 流程 145 定义/25 key/24 模型 → `process_definitions` 68 | 不导入 BPMN | 路由 `definitionId`（8 个）对照已登记流程；无对应者显式差额 |

> G5/G6/G8/G9 未逐项单独决策，沿用已批准的"映射优先/融合、不新增能力"默认方针；如维护者另有取向可在此修订。

### 3.1 源侧阻塞项（必须显式处置，不得静默合并）

- `mixed_tenant`：`config_dictionaries`、`cti_authorized`、`priority_levels`（含把 `companykey` 当 `companyId` 的异常行）`companyId` 不唯一。
- `orphan_reference`：`cti_authorized` 14 个 `ctiId` 不在 `cti_tree`；`priority_matrix` 1 个 `priorityId` 不在 `priority_levels`。
- `unknown_enum_semantics`：`authorizedType∈{0,2}`。
- `calendar_gap`：源日历不覆盖截止日。

## 4. 源身份与租户映射

- 稳定身份用源主键：`ctiId`、`dictionariesId`、`priorityLevelId`、`priorityMatrixId`、`ctiauthorizedId`、`holidaysId`、`moduleId`、`process_definitions.id`、`process_models.id`。**禁止按名称自动合并**。
- 复合身份：优先级/矩阵/路由须带 `moduleId` 与 `companyId` 消歧。
- 租户：`companykey` 与 `companyId` 为不同命名空间，分别映射；不唯一者阻塞相关批次。
- 目标 ID 映射在 G-A 固定目标身份后生成，本设计不预置目标 UUID。

## 5. 批次、幂等与中断恢复

- 依赖顺序：分类（G1）→ 字典/字段（G2）→ 优先级与矩阵（G3/G5）→ SLA 与日历（G4/G7）→ 路由（G6）→ 模块/流程对照（G8/G9）。
- 每批单事务，记录：源摘要（sha256）、映射、插入/更新/跳过/拒绝原因、检查点。
- 幂等键 `(source_system, source_id, tenant)`；同批重跑不产生重复或额外变更。
- 失败批次保留检查点并显式失败，不假成功；租户错配、孤立引用、未知流程动作显式拒绝。
- dry-run 变更清单经审查后，才在获准隔离目标执行。

## 6. 校验与验收

- 每批：行数/约束对账、源摘要一致、拒绝清单为空或已解释。
- 幂等：同批重复执行零增量。
- 中断恢复：中途失败后按检查点恢复，状态可追踪。
- 业务验收：新建可追踪验收记录验证分类、目录、SLA、专业流程与权限；验收记录与正式迁移清单隔离，且不删除历史制造干净结果。

## 7. 排除与范围差额

- 排除：历史工单及审批/评论/附件/流程实例、旧 BPMN、知识库、组织/用户重导、密码/角色重置、ITSM R(038)。
- 范围差额（显式未接纳，不静默发明字段或第二套权威）：无目标落点的字典组、`authorizedType` 未确认语义、需独立日历表的诉求、G-A 未固定的目标模型。

## 8. 待输入（阻塞最终映射）

1. CTI 映射确认：`2026-09-14-cti-mapping-worksheet.md` 的"你的确认"列。
2. 2024–2026 节假日数据（旧数据缺失）。
3. 字典"在用"清单（决定 G2 迁移范围）。
4. 目标身份/制品（G-A）：固定后生成目标 ID 映射与批次目标身份。

## 9. 非目标

- 不实现写入代码、不执行目标写入、不改动 G-A 门禁、不新增产品能力（如独立日历表）。
- 不迁历史工单等排除项；不建立第二份 CTI 权威字典或第二套规则引擎。
