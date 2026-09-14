# 任务二交接（最终）：配置主数据适配统一 WorkItem 模型

- **Gate:** G-B
- **Status:** **BLOCKED**（G-B 未满足）。已安全写入的批次均已执行、校验并留证；**B3 优先级/矩阵、B5 路由因目标能力差额判定阻塞**；B4 日历数据已写入但**截止计算验收阻塞**。
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
| ITSM | `/home/administrator/project/itsm/.worktrees/workitem-config-migration` | `codex/feat/workitem-config-migration` | `66008779` | **`c0f102d5`**（本交接正文所在提交；如需再追加文档提交，以 `git rev-parse HEAD` 为准） |
| KAF | `/home/administrator/project/kaf-worktrees/workitem-config-migration` | `feat/workitem-config-migration` | `184f7868` | **`67928a8b`** |

- 本交接提交 SHA 记为 `GBRevision`（`git rev-parse HEAD`：ITSM `a4283cdb`、KAF `67928a8b`；内容提交后如再提交须更新）。

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

### 5.1 B3 优先级/矩阵 — BLOCKED

- 旧 `priority_levels`（15 行，按模块、含分钟）与 `priority_matrix`（86 行，4 模块 × 8 影响 × 4 紧急）**无目标落点**：
  - `PriorityMatrixService` 为**纯内存缓存**（`SetMatrix` 生产无调用）、**无持久化表、无 API 控制器**；新矩阵固定 **4×4** 且**无模块维度**。
  - `ticket_assignment_rules`/`ticket_automation_rules` 的**条件字段不含 impact/urgency**（`service/ticket_rule_conditions.go`）。
- 已交付：P0–P3 归一化映射 + 86 条矩阵台账 → `docs/review/2026-09-14-b3-priority-matrix-ledger.md`。

### 5.2 B5 路由 — BLOCKED（dry-run 完成）

- 规则条件仅 `status/priority/category_id/department_id/requester_id/assignee_id`；动作仅 `user/round_robin/load_balance`（**无角色动作**），且**首条命中即返回**。
- 720 条路由：**仅 14 条**落在已映射分类上可近似（其中 9 条用户可解析），**706 条不可表达**；82 条角色路由动作不支持。
- 已处置未决项（用户确认 **void**）：**20 个已删除用户 id（68 条）**、**14 个已删除 CTI 节点（118 条）** → `docs/review/2026-09-14-b5-open-items-resolution.md`。
- 已交付：`docs/review/2026-09-14-b5-routing-dry-run.md`（含 14 条逐条清单）。

## 6. 排除 / 失效登记（后续批次不得再纳入）

见操作手册 **§7.1**：`OA申请`（父容器）、20 个已删除用户 id、14 个已删除 CTI 节点。**不改动旧源数据。**

## 7. 验证记录

- **测试**：ITSM `node --test scripts/__tests__/*.test.js` → **41/42**（唯一失败为基线即存在的 `build-start-scripts` 第 8 例 compose 镜像契约，与本任务无关）；KAF 本任务相关测试 **27/27 passed**，`ruff` 通过。（KAF 全量套件存在与本任务无关的既有失败/错误，涉及未改动模块。）
- **幂等**：B0/B1/B2/B4/流程批次复跑均为 **0 新增**。
- **完整性**：分类无孤儿、模板↔字段↔分类引用 0 悬空、CI `ci_type`↔`ci_type_id` 一致、绑定无悬空、`bpmn_xml` 可解码。
- **脱敏**：未提交 PII、凭据或导出物；身份对象 dump 在 `.gitignore` 目录，仓库仅保留 ID/计数。

## 8. 剩余工作与需产品决策

1. **B3**：矩阵持久化 / 按模块维度 / 规则扩展 impact+urgency / 或明确接受差异。
2. **B5**：规则是否扩展维度（`ctiId`/`taskDefKey`/`definitionId`）与 **role 动作**；补齐旧→新用户 id 映射后方可写 9 条上限。
3. **B4**：`time_zone` 消费、**分段时段（午休）**、**周末补班**能力；未解决前 SLA 验收阻塞。
4. **`ticket_types`**：产品内置默认未初始化（差异项，非旧数据迁移）。
5. **业务验收**：G-A 目标上的新建验收记录尚未执行；G-B 保持 BLOCKED。

## 9. 可复用产物

- ITSM：`scripts/migrate_config_seed/{generate_seed_sql,generate_process_sql,generate_b1_sql,generate_b2_sql,generate_b4_sql}.py` + `data/*.json` + node 测试；`scripts/clone_itsm_migration_db.sh`（加固）。
- KAF：`scripts/verify_legacy_master_data.py`、`scripts/report_legacy_config_conflicts.py`、`scripts/fetch_legacy_identity_objects.py` + 测试。
- 端到端流程与判定规则：`docs/migrations/2026-09-14-legacy-config-migration-playbook.md`（含 §7 决策日志 S5–S17 与 §8 复用清单）。

## 10. 审查信息

- 实现：本次编码 Agent，2026-09-14。
- 独立审查：**待指派**。涉及 WorkItem、迁移与权限的变更须由独立审查者/维护者复核（ITSM `docs/agent-engineering-governance.md` §7）；实现者不得作为唯一验收者。
- 映射、代码、源数据或目标发生影响性变化后须发布新修订；G-B 通过前本文件不得升级为 PASS。
