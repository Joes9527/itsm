# 任务二交接：配置主数据适配统一 WorkItem 模型（审查后收口中）

- **Gate:** G-B
- **Status:** **BLOCKED**（G-B 未满足）。已安全写入的批次均已执行、校验并留证；**B3 优先级差异按新规范处置；B5 旧路由经用户确认移出首期范围**；B4 日历数据已写入但**截止计算验收阻塞**。
- **日期:** 2026-09-14
- **消费的 GARevision:** `d91b587fe3ab40cc863321346d217d258a3a96d8`（`docs/review/2026-09-14-database-reconciliation-handoff.md`，`Status=PASS`）。已独立复核固定制品、目标镜像 `sha256:7ae6051efd0e…`、库/owner/schema、PG 17.10 与扩展、账本与 Phase 1 不变量。
- **结论:** 在 G-A 门禁下，已把**能安全表达的配置主数据**受控准入 `itsm_ga_ready`（单事务 + 备份 + 回滚预演 + 幂等复跑 + 校验）；**不能表达的部分一律不写**，以代码证据与台账登记。本文件**不得**作为验收依据，不得据此切换连接或操作其它库。

## 1. 门禁与边界

- 目标写入只以任务一 `Status=PASS` 的交接为门禁；本任务不生成、不绕过 G-A。
- **不迁移**：历史工单/审批/评论/附件/流程实例；旧 BPMN；知识库；组织/用户全量重导；ITSM R(038)。目标 `tickets` 表 **0 行**（已核验）。
- 未 push、未合 `main`、未切连接、未停源、未操作生产；未改动既有 checkout 与基线分支。

## 2. 两仓库工作区与提交

| 仓库 | worktree | 分支 | 基线 | 最终提交 |
| --- | --- | --- | --- | --- |
| ITSM | `/home/administrator/project/itsm/.worktrees/workitem-config-migration` | `codex/feat/workitem-config-migration` | `66008779` | **`82ca81878d3731801a61b278ed3b89c5ab69f6dc`**（原始交付；正文提交 `c0f102d5`） |
| KAF | `/home/administrator/project/kaf-worktrees/workitem-config-migration` | `feat/workitem-config-migration` | `184f7868` | **`67928a8b`** |

- 上表固定原始被审交付，不以浮动 HEAD 代替。G-B 尚无 PASS 修订。审查后 KAF 工具修复为 `e6fd8a50368a15505c98c8826dee5af975cef5c2`；ITSM 工具修复固定为 `7c8cee6fae181400573308bfb7e73043d11ed0b9`，实际隔离重放五批通过，独立复审通过。KAF 工具修订不替代 G-A 的运行时 schema 修订。

## 3. 目标最终状态（写入后核验）

| 对象 | 数量 | 对象 | 数量 |
| --- | ---: | --- | ---: |
| `ticket_categories` | 185 | `ticket_templates` | 10 |
| `field_definitions` | 59 | `sla_definitions`（含日历） | 7（7/7） |
| `service_catalogs` | 8 | `ci_types` | 9 |
| `configuration_items` | 46 | `standard_changes` | 3 |
| `known_errors`（占位） | 1 | `ticket_tags` | 4 |
| `ticket_views` | 5 | `process_deployments` / `process_definitions` | 20 / 20 |
| `process_bindings` | 7 | `ticket_assignment_rules` / `ticket_automation_rules` | 0 / 0 |
| `tickets`（历史，未迁） | 0 | | |

**不变量（Phase 1，写入前后一致）**：tenants 2 / departments 7975 / users 7862 / roles 36 / permissions 349 / role_permissions 1414 / user_roles 7 / external_identities 2；`schema_migrations` 36 条，head `046_auth_token_state`（无 R038）。

## 4. 已执行批次（含证据）

| 批次 | 结果 | 证据 |
| --- | --- | --- |
| **B0 规范 seed 准入** | 分类182→185（含 B1 新增 3）、模板10、字段59、SLA7、目录8、CI类型9、标准变更3、known_errors 1（占位）、标签4、视图5；幂等复跑 0 新增 | `docs/review/2026-09-14-b0-seed-admission-evidence.md` |
| **规范流程初始化** | 20 个内嵌模板（源经逐字节校验与固定制品一致）+ 7 条 `process_bindings`；无悬空绑定、无坏 XML | `docs/review/2026-09-14-process-init-evidence.md` |
| **B1 分类/资产落位** | 46 个 CMDB CI（43 业务系统 + 3 基础设施）；新建 3 分类 `COL-MAIL-004`/`ACC-LCM-003`/`APP-GEN-SVC-001`；旧 ctiId→目标 CI 全量映射 | `docs/review/2026-09-14-b1-landing-evidence.md` |
| **B2 字典选项落地** | 追加 25 个选项（`target_system` 9→16 ×3 模板；`service_type` 6→8；邮箱 `operation` 5→7）；归并/排除仅登记 | `docs/review/2026-09-14-b2-dictionary-landing-evidence.md` |
| **B6 模块/流程对照** | 模块→recordClass 与目标一致；无写入；`ticket_types` 空表登记为差异 | `docs/review/2026-09-14-b6-module-mapping-evidence.md` |
| **B4 SLA 日历** | 7 条 `business_hours` = 周一至五 09:00–18:00 + 89 假日（2024=28/2025=28/2026=33）+ `Asia/Shanghai`；**截止计算验收阻塞** | `docs/review/2026-09-14-b4-sla-calendar-evidence.md` |

写入契约（各批一致）：写入前复核目标指纹 → `pg_dump` 备份 → **事务回滚预演** → 单事务执行 → **幂等复跑** → 校验；备份位于受保护目录 `/home/administrator/.local/state/itsm-task2-b0-20260914/`。

## 5. 阻塞批次（不写目标，含代码证据）

### 5.1 B3 优先级/矩阵 — 以新规范为准，旧配置差异保留

- 旧 `priority_levels`（15 行，按模块、含分钟）与 `priority_matrix`（86 行，4 模块 × 8 影响 × 4 紧急）**无目标落点**：
  - `PriorityMatrixService` 为**纯内存缓存**（`SetMatrix` 生产无调用）、**无持久化表、无 API 控制器**；新矩阵固定 **4×4** 且**无模块维度**。
  - `ticket_assignment_rules`/`ticket_automation_rules` 的**条件字段不含 impact/urgency**（`service/ticket_rule_conditions.go`）。
- 已交付：P0–P3 归一化映射 + 86 条矩阵台账 → `docs/review/2026-09-14-b3-priority-matrix-ledger.md`。

### 5.2 B5 旧路由 — 首期不迁移（用户确认，dry-run 保留）

- 规则条件仅 `status/priority/category_id/department_id/requester_id/assignee_id`；动作仅 `user/round_robin/load_balance`（**无角色动作**），且**首条命中即返回**。
- 720 条路由：**仅 14 条**落在已映射分类上可近似（其中 9 条用户可解析），**706 条不可表达**；82 条角色路由动作不支持。
- 用户批准排除的固定范围：20 个用户 ID 涉及 68 条、14 个 CTI ID 涉及 118 条，交集 4 条、并集 182 条、其余 538 条。旧身份抽取无完整性 manifest，不能据此证明源删除；批准范围与输入摘要见 [void 批准清单](2026-09-14-routing-void-approval.json)。不得将新增 unresolved 自动视为 void。
- 2026-09-14 用户确认：**按现有分派与规范流程上线，旧路由后续处理**。本期不将上述近似项写入目标，不扩展旧角色或流程节点路由；现有分派和规范流程仍需真实验收。
- 已交付：`docs/review/2026-09-14-b5-routing-dry-run.md`（含 14 条逐条清单）。

## 6. 排除 / 失效登记（后续批次不得再纳入）

见操作手册 **§7.1**：`OA申请`（父容器）、批准排除的 20 个用户 ID、14 个 CTI ID。**不改动旧源数据。**

## 7. 验证记录

- **测试**：ITSM `node --test scripts/__tests__/*.test.js` → **41/42**（唯一失败为基线即存在的 `build-start-scripts` 第 8 例 compose 镜像契约，与本任务无关）；KAF 本任务相关测试 **27/27 passed**，`ruff` 通过。（KAF 全量套件存在与本任务无关的既有失败/错误，涉及未改动模块。）
- **幂等**：B0/B1/B2/B4/流程批次复跑均为 **0 新增**。
- **完整性**：分类无孤儿、模板↔字段↔分类引用 0 悬空、CI `ci_type`↔`ci_type_id` 一致、绑定无悬空、`bpmn_xml` 可解码。
- **脱敏**：未提交 PII、凭据或导出物；身份对象 dump 在 `.gitignore` 目录，仓库仅保留 ID/计数。

## 8. 剩余工作与需产品决策

1. **B3**：用户确认以新规范为准，无法确定的差异保留；不为缺模块、未知枚举或冲突条目猜测补值。仍需核对新规范优先级的实际功能路径。
2. **B5**：旧路由后续处理已获用户确认，不再作为首期能力扩展阻塞；首期验收现有人工分派与规范流程绑定。不得将 9 条近似匹配当成获准迁移。
3. **B4**：本期保留 09:00–18:00，修复配置时区与指定日期补班计算，并限制日历有效期；不扩展午休配置界面。修复和真实业务验收完成前仍阻塞。
4. **`ticket_types`**：定向产品初始化代码 `8ba80e18` 已完成及独立复审通过；尚未在目标执行，目标仍为 0。属于产品默认配置初始化，非旧数据迁移。
5. **业务验收**：G-A 目标上的新建验收记录尚未执行；G-B 保持 BLOCKED。

## 9. 可复用产物

- ITSM：`scripts/migrate_config_seed/{generate_seed_sql,generate_process_sql,generate_b1_sql,generate_b2_sql,generate_b4_sql}.py` + `data/*.json` + node 测试；`scripts/clone_itsm_migration_db.sh`（加固）。
- KAF：`scripts/verify_legacy_master_data.py`、`scripts/report_legacy_config_conflicts.py`、`scripts/fetch_legacy_identity_objects.py` + 测试。
- 端到端流程与判定规则：`docs/migrations/2026-09-14-legacy-config-migration-playbook.md`（含 §7 决策日志 S5–S17 与 §8 复用清单）。

## 10. 审查信息

- 实现：本次编码 Agent，2026-09-14。
- 原交付独立审查已完成：`codex/chore/database-reconciliation@1b7c8a4f` 的 `docs/review/2026-09-14-workitem-config-migration-independent-review.md`，发现 R1–R8。KAF R5–R8 修复经独立复审关闭，相关离线测试 68/68；全套测试仍有既有环境收集错误，不能称全量通过。ITSM R1–R4 已独立复审 ADDRESS，并完成真实备份五批隔离重放。SLA 限定修复独立复审通过；运行时集成与实际业务验收尚未完成。涉及 WorkItem、迁移与权限的变更须由独立审查者/维护者复核（ITSM `docs/agent-engineering-governance.md` §7）；实现者不得作为唯一验收者。
- 映射、代码、源数据或目标发生影响性变化后须发布新修订；G-B 通过前本文件不得升级为 PASS。

## 11. 首期范围决定（2026-09-14，用户确认）

- 首期聚焦单租户功能可用与上线收口，不开展 MSP、多租户产品能力扩展。保留现有身份字段和关联防错校验，不重写 Phase 1 身份数据。
- 以新 ITSM 规范为准；无法确定的旧配置登记差异，不猜测迁移。旧路由后续处理，首期使用现有分派与规范流程。
- 范围缩减不等于验收通过。SLA、产品内置 ticket_types、实际新建与流程路径，以及修复后的迁移重放证据尚须完成。
- R4 新增的原子批次收据只可记录真实新执行，不给原 G-A 数据补造历史收据。本轮重放使用独立测试容器内 `gb_replay_review`，原目标保持不变。

审查后实际重放与修订证据见 [修复证据](2026-09-14-config-migration-remediation-evidence.md)。

功能核对补充：七条流程绑定的 `sla_policy_id` 均为空，当前新建链路不生成 SLA 截止时间；日历修复后仍需完成 SLA 选择接线和实际新建验收。新日历配置尚未写入目标。

运行时收口跟踪位于独立分支 `codex/feat/config-launch-integration` 的 `docs/review/2026-09-14-config-launch-closure.md`。迁移工具分支与运行时分支用途不同，不以工具 HEAD 作为运行时制品。
