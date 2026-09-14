# 任务二交接：配置主数据适配统一 WorkItem 模型

- **Gate:** G-B
- **Status:** BLOCKED（G-B 未满足：B0/B1/B2 已准入并通过校验；B6 对照完成无写入；B4 日历数据已写入但**截止计算验收阻塞**；**B3 因能力差额判定阻塞**；B5 路由与 20 个未解析用户 ID 仍未完成）
- **日期:** 2026-09-14
- **消费的 GARevision:** `d91b587fe3ab40cc863321346d217d258a3a96d8`。`docs/review/2026-09-14-database-reconciliation-handoff.md` 现已在磁盘存在且 `Status=PASS`；已独立复核固定制品、目标镜像、库/owner/schema、PG 与扩展、账本（36 条，head `046_auth_token_state`，无 R038）及 Phase 1 不变量。
- **结论:** G-A 已核验并消费；**B0 规范 seed 配置已受控准入 `itsm_ga_ready`**（见 `docs/review/2026-09-14-b0-seed-admission-evidence.md`），**规范流程初始化批次已执行**（20 个内嵌模板 + 7 条绑定，无悬空绑定，见 `docs/review/2026-09-14-process-init-evidence.md`），**B1 分类/资产落位已执行**（46 个 CMDB CI + 3 个新分类，见 `docs/review/2026-09-14-b1-landing-evidence.md`），**B2 字典选项落地已执行**（追加 25 个选项，见 `docs/review/2026-09-14-b2-dictionary-landing-evidence.md`），**B4 SLA 日历已写入**（7 条 09:00–18:00 + 89 假日；**截止计算验收阻塞**，见 `docs/review/2026-09-14-b4-sla-calendar-evidence.md`）。配置主数据映射已逐项与业务确认。因 B3/B5/B6、B4 能力差额与路由缺口未完成，G-B 仍未满足；不得据此对其它目标库写入、切换或验收。

## 1. 门禁与上游边界

- 依据总设计 §5 与任务二计划：目标写入必须等待任务一交付 `docs/review/2026-09-14-database-reconciliation-handoff.md`，且 `Status=PASS`，并提供固定 `GARevision`、目标制品、目标数据库身份与验证证据。
- 上述交接**已交付且 `Status=PASS`**（`GARevision=d91b587fe3ab40cc863321346d217d258a3a96d8`）；已核验固定制品 SHA、目标镜像 `sha256:7ae6051efd0e…`、目标库身份（`itsm_ga_ready`/`ga_owner`/`public`）、PG 17.10 与扩展、账本及 Phase 1 不变量。B0 写入以此为唯一门禁。
- `itsm_migration_20260914`（PG 17.10，容器 `itsm-postgres-dev`）仅作为既有**对照样本**，不是 G-A 准入目标；本任务未对其做任何写入。
- 上游变化或缺陷一律回传任务一；本任务不生成、不绕过 G-A，不复制出第二份上游验收权威。

## 2. 两仓库工作区与提交

| 仓库 | worktree | 分支 | 基线 | 本任务代码提交 |
| --- | --- | --- | --- | --- |
| ITSM | `/home/administrator/project/itsm/.worktrees/workitem-config-migration` | `codex/feat/workitem-config-migration` | `660087795e6efe49e89b759d2527ad1b8320a651` | `d0f3ef696e3f3bfa8409876fe098fdc796c1d613` |
| KAF | `/home/administrator/project/kaf-worktrees/workitem-config-migration` | `feat/workitem-config-migration` | `184f7868161794bd549a6fbe2d46e41fe0de854e` | `7302ad40a2dcec82f7854e7c64e48796c5501837` |

- 基线均为各自 `origin/main` + 精确导入的既有未合并成果，未改动既有 checkout 或分支。
- 既有成果保留：ITSM `/home/administrator/project/itsm`（`codex/migration-legacy-config-data` @ `66008779`，含克隆脚本、差异报告、账本计划）与 KAF `/home/administrator/project/kaf` 均未修改。
- 未 push、未合 `main`、未切连接、未停源、未操作生产。
- 本交接提交 SHA 记为 `GBRevision`（内容提交后由 `git rev-parse HEAD` 取得；本任务未产生任何 `GARevision`）。

## 3. 只读与离线证据（可复现）

### 3.1 抽取 manifest 与分页完整性

- 来源：`https://keas-itsm-test.gazellio.com`，`env=test`，`fetched_at=2026-09-14T06:57:53.013862+00:00`，`page_size=200`，`dry_run=false`。
- 落盘目录（`.gitignore`，仅 `manifest.json` 入库）：`kaf/data/legacy_itsm/test/`；`manifest.json` 文件 sha256 `e7aaf0cc815d946b7992166070d52d09c638f84a06baf38049b74e4ed875fe86`。
- 复现命令（KAF worktree）：
  ```bash
  .venv/bin/python scripts/verify_legacy_master_data.py \
    --dir /home/administrator/project/kaf/data/legacy_itsm/test --json-out /tmp/legacy_verify.json
  ```
- 结果：10/10 资源 `status=ok`、`rows==total`、sha256 与 manifest 全部一致；`cti_tree=82`、`config_dictionaries=988`、`priority_levels=15`、`priority_matrix=86`、`cti_authorized=720`、`modules=4`、`holidays=611`、`process_definitions=145`、`process_models=24`、`departments=3175`。退出码 0。
- 报告摘要 sha256：`/tmp/legacy_verify.json` = `13078b30f757f5402808d17e8b2d40b46cbbfa0974e40c4d6b40ec47a26d68da`。

### 3.2 差异工具基线

- KAF 既有抽取/差异单测：21 passed（`184f7868` 基线，`tests/test_itsm_master_data_extraction.py`、`tests/test_diff_legacy_vs_new_itsm.py`）。
- 既有差异报告已核对：`itsm/docs/migrations/2026-09-14-legacy-vs-new-itsm-diff.{md,json}`。其口径为“名称匹配率”，按设计 §5.1 **不能**当作迁移成功率，本任务只引用其数量级，不据此判定重复/缺失。

### 3.3 源侧冲突清单

- 复现命令（KAF worktree）：
  ```bash
  .venv/bin/python scripts/report_legacy_config_conflicts.py \
    --legacy-dir /home/administrator/project/kaf/data/legacy_itsm/test \
    --as-of 2026-09-14 --out /tmp/legacy_conflicts.md --json-out /tmp/legacy_conflicts.json
  ```
- 结果：**blockers=8，conflicts=13**。报告摘要 sha256：`/tmp/legacy_conflicts.json` = `a08a301467158885bbba9f8fa8c1c1e943c14ca35fb484d4e5d31dd3004d51d8`。
- Blocker 明细：

| 类别 | 资源 | 事实 |
| --- | --- | --- |
| mixed_tenant | `config_dictionaries` | `companyId` 3 个取值 |
| mixed_tenant | `cti_authorized` | `companyId` 2 个取值 |
| mixed_tenant | `priority_levels` | `companyId` 3 个取值，含把 `companykey` 值当 `companyId` 的 2 行 |
| mixed_tenant | `departments` | `companyId` 2 个取值（组织身份，复用 Phase 1，不在本轮写入） |
| orphan_reference | `cti_authorized` | 14 个 `ctiId` 不在 `cti_tree`（引用缺失父/被删分类） |
| orphan_reference | `priority_matrix` | 1 个 `priorityId` 不在 `priority_levels` |
| calendar_gap | `holidays` | 覆盖 2018-08-05..2023-12-31，**不含截止日 2026-09-14** |
| unknown_enum_semantics | `cti_authorized` | `authorizedType ∈ {0,2}` 语义无法从 dump 判定，须上游/源系统确认 |

- 非阻塞告警：`cti_tree` 名称重名 4 组、`config_dictionaries` 中文名重名 86 组、`priority_levels` 优先级名重名 4 组、`process_models` 名称重名 1 组、`dictionariesCode` 重复 4 组。
- 引用完整性通过项：`cti_authorized.definitionId -> process_definitions.id` 无缺口（8 个被引用定义均在抽取集合内）。

## 4. 源身份与租户映射

- **稳定身份规则（源侧）**：一律使用源系统主键，禁止按名称合并。
  - `cti_tree.ctiId`、`config_dictionaries.dictionariesId`、`priority_levels.priorityLevelId`、`priority_matrix.priorityMatrixId`、`cti_authorized.ctiauthorizedId`、`holidays.holidaysId`、`modules.moduleId`、`process_definitions.id`、`process_models.id`。
  - 复合身份：优先级/矩阵/路由还须带 `moduleId` 与 `companyId` 消歧。
- **租户映射**：源侧 `companykey` 单一（`2016082500001`），`companyId` 在 4 个资源上不唯一（见 §3.3）。`companykey` 与 `companyId` 属不同命名空间，须分别映射，不得混为一谈。
- **目标 ID 映射**：**待上游核实**。目标库身份、目标 UUID/编码体系未由 G-A 固定；本任务不生成目标 ID、不预置映射表。
- **冲突处置策略**：同名冲突、缺父/孤立引用、租户错配一律**显式阻塞相关批次**，不自动模糊合并、不静默丢弃。

## 5. 目标模型映射（待上游核实）

以下为候选目标概念（依据 `docs/superpowers/specs/2026-08-26-unified-work-item-model-design.md` 与 ITSM `ent/schema/` 现状），**版本与结构须由 G-A 固定的目标制品核实，本任务不认定**：

| 源资源 | 候选目标 | 状态 |
| --- | --- | --- |
| `cti_tree` | `ticket_categories`（分类树，`recordClass` 映射由分类/目录决定） | 待上游核实 |
| `config_dictionaries` + 自定义字段 | `field_definitions.options` / `ticket_templates.form_fields` | 待上游核实 |
| `priority_levels` | 优先级枚举 + `sla_definitions`（响应/解决时间） | 待上游核实 |
| `priority_matrix` | 影响×紧急→优先级/SLA 的矩阵配置 | 待上游核实 |
| `holidays` | SLA 业务日历 | 待上游核实；且源日历已过期（§3.3） |
| `cti_authorized` | `ticket_assignment_rules`（分类路由） | 待上游核实 |
| `modules` | `ticket_types` / `recordClass` 对照 | **语义层不同**，须显式映射决定 |
| `process_definitions` / `process_models` | 既有已登记流程绑定（仅对照，不导入 BPMN） | 待上游核实 |

**显式范围差额（不得静默发明字段）**：`authorizedType` 语义、日历扩展至截止日、`modules` 到 `recordClass` 语义对齐、目标分类/目录/SLA 的既有 seed 与新模型的最终归属——这些若须新增产品能力才能映射，作为范围差额上报，不在本任务内发明字段或第二套结构。

## 6. 纳入 / 排除清单

**纳入（配置主数据，待 G-A 后分批写入）**：CTI 分类树、配置字典、优先级与优先级矩阵、SLA 相关配置与业务日历、CTI 分类路由、ITIL 模块对照，以及分类/路由到 `recordClass`、目录、SLA、已登记流程的绑定。

**排除（本轮不迁）**：历史 ticket 及审批/评论/附件/流程实例；旧 BPMN 与知识库；组织/用户全量重导（复用 Phase 1 成果，仅做身份与引用核对，不重置密码/角色）；ITSM R(038)。

**不接纳（保持显式未接纳）**：无法判定语义的 `authorizedType`；不覆盖截止日的源日历；无目标落点的孤立引用。

## 7. 批次计划（G-A 后执行；当前仅为设计）

1. **依赖顺序**：分类树 → 配置字典/自定义字段 → 优先级与矩阵 → SLA 与业务日历 → 分类路由 → 模块/流程绑定对照。
2. **批次边界**：每批单一依赖层，单事务；记录源摘要（sha256）、映射、插入/更新/跳过/拒绝原因与检查点。
3. **幂等**：以 `(source_system, source_id, tenant)` 稳定键做幂等 upsert；重跑同批不得产生重复或额外变更。
4. **中断恢复**：检查点表记录已完成批次；失败批次保留检查点并显式失败，不假成功；租户错配、孤立引用、未知流程动作明确拒绝。
5. **前置门禁**：dry-run 变更清单经审查后，才在**获准的隔离目标**执行；目标身份在每批固定并核对。

## 8. 克隆工具加固（已交付）

- 修复前缺陷（已在测试中复现）：目标库已存在（含半恢复）即 `skip` 成功；`pg_restore` 失败后重跑假成功；标识符未校验即拼入 SQL；仅比对 4 张表行数。
- 修复后行为（ITSM `d0f3ef69`）：
  - docker/SQL 前校验容器、库、角色、表标识符，并拒绝 target==source；
  - 用显式 marker 表 + 8 张纳管配置表的存在性与行数做完整性验证，替代 4 表抽查；
  - 目标不完整时 fail-closed；须显式 `RECREATE_INCOMPLETE=1` 才允许覆盖；`pg_restore` 失败后重跑持续失败。
- 测试：`node --test scripts/__tests__/clone-itsm-migration-db.test.js` → **6/6 passed**；`make verify-scripts` 已纳入该用例。
- 既有无关失败（`scripts/__tests__/build-start-scripts.test.js` 第 8 例，`docker-compose.prod.yml` 镜像契约）在基线 `66008779` 即存在，属其他关注点，按范围纪律未修改。

## 9. 业务验证（未执行，G-B 未满足）

- 分类、目录、SLA、专业流程及权限的**新建验收记录**验证必须在 G-A 准入目标上进行，当前无法执行。
- 验收记录要求：可追踪、与正式迁移清单隔离（正式迁移清单不得自动包含演练记录），也不得删除历史来制造干净结果。
- 因此 G-B 保持 **BLOCKED**：映射、写入、幂等/中断恢复实跑与业务验收证据均缺失。

## 10. 剩余差额与阻塞项

1. G-A 交接缺失（`GARevision` 不存在）——目标制品 SHA、目标库身份/指纹、目标模型版本均待上游核实。
2. 8 项 blocker 未处置（§3.3）。
3. 目标 ID 映射未生成；批次未 dry-run、未执行。
4. 幂等 upsert 与检查点机制已设计但**未实跑**。
5. 业务验收记录与权限验证未执行。

## 11. 交付检查

- `git diff --check`：两 worktree 均干净无告警。
- 测试：KAF 迁移相关 4 个测试文件 **43 passed**（21 既有 + 22 新增），`ruff check` 通过；ITSM 克隆脚本 **6/6 passed**。
- 敏感信息：未提交任何 PII、导出物、凭据或临时证据；原始 dump 保持 `.gitignore`，仅 `manifest.json` 入库；本文件仅含计数与源侧标识符。
- 证据报告（`/tmp/legacy_verify.json`、`/tmp/legacy_conflicts.{json,md}`）为本地脱敏证据，未入库。

## 12. 审查信息

- 实现：本次编码 Agent，2026-09-14。
- 独立审查：**待指派**。涉及 WorkItem、迁移与权限的变更须由独立审查者/维护者复核（ITSM `docs/agent-engineering-governance.md` §7）；实现者不得作为唯一验收者。
- 未获 G-A 前，本文件不得升级为 PASS；映射、代码、源数据或目标发生影响性变化后须发布新修订。
