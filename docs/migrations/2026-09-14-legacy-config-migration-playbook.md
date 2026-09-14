# 旧 ITSM 配置主数据迁移 操作手册（可复用）

- 状态：draft，供维护者复核；随任务二执行更新，不替代 G-A/G-B 门禁。
- 日期：2026-09-14。
- 目的：把「旧 ITSM 配置主数据 → 新 ITSM」的**全部操作、判定规则、命令与证据**记录成可复用流程，使下一次迁移照本执行即可复现。
- 关联：[GAP 与解决方案 spec](../superpowers/specs/2026-09-14-legacy-config-migration-gap-and-solution-design.md)、[映射与日历工作簿](../review/2026-09-14-config-mapping-workbook.md)、[CTI 节点分流工作表](../review/2026-09-14-cti-mapping-worksheet.md)、[原 G-B 交接](../review/2026-09-14-workitem-config-migration-handoff.md)。

## 0. 复用原则

1. **单一权威**：新 ITSM 为权威；旧侧只作映射来源，禁止按名称自动合并。
2. **身份固定**：每次迁移先固定源 manifest 摘要、目标制品摘要、`GARevision`、目标库身份与指纹；任一变化发布新修订，不沿用旧检查点。
3. **混合树分流**：旧 CTI 不是纯分类树，必须按节点语义分流（见 §2），禁止整树当分类。
4. **不静默**：未知枚举、孤立引用、租户错配、无落点配置显式阻塞或列差额，不静默丢弃/发明字段。
5. **只提交脱敏证据**：原始 dump/凭据/PII 留在受保护目录，仓库只存计数与摘要。

## 1. 操作步骤与命令

| 步骤 | 命令（KAF worktree） | 产物 |
| --- | --- | --- |
| S1 抽取 | `ITSM_URL=… ITSM_USERNAME=… ITSM_PASSWORD=… .venv/bin/python scripts/fetch_itsm_master_data.py` | `data/legacy_itsm/<env>/*.json` + `manifest.json` |
| S2 核验完整性 | `.venv/bin/python scripts/verify_legacy_master_data.py --dir data/legacy_itsm/<env> --json-out <evidence>.json` | 10/10 资源 sha256/行数一致 |
| S3 源冲突 | `.venv/bin/python scripts/report_legacy_config_conflicts.py --legacy-dir data/legacy_itsm/<env> --as-of <cutoff> --out <md> --json-out <json>` | blockers/conflicts 清单 |
| S3.1 授权对象解析 | `.venv/bin/python scripts/fetch_legacy_identity_objects.py --authz …/cti_authorized.json --out-dir data/legacy_itsm/identity --summary-out <json>`（只读；PII 落 gitignored 目录） | `authorizedType`→对象类型解析 |
| S4 差异对照 | `.venv/bin/python scripts/diff_legacy_vs_new_itsm.py --legacy-dir … --out itsm/docs/migrations/<date>-diff.md` | 名称匹配口径（仅数量级参考） |
| S5 节点分流 | 见 §2（本手册规则） | CTI 工作表 |
| S6 目标核验 | 只读核对目标容器/库/schema/制品/账本/Phase1 行数（见 §4） | 核验记录 |
| S7 批次 dry-run | 生成变更/拒绝/重绑清单，审查后执行 | 批次清单 |
| S8 写入与校验 | 单事务 + 检查点（见 §5） | 批次回执 |
| S9 业务验收 | 新建可追踪验收记录（与迁移清单隔离） | 验收记录 |

## 2. 旧 CTI 节点类型判定模型（核心）

旧树混了多种语义；按节点分流到不同目标维度：

| 节点类型 | 判定特征 | 目标维度 | 批量 |
| --- | --- | --- | --- |
| `business_system` | 根为 `企业应用系统` 子树；或平铺系统名（含"系统/平台"、`*BI`） | CMDB CI（`ci_type=business_system`），开单时以 `ci_ids` 选择，**不进分类树** | B1a |
| `ticket_category` | `OA申请` 子树（服务/动作）；动作型节点（如 `K3.5数据变更`） | `ticket_categories`，键用 seed `code` | B1b |
| `org_location` | `本地系统-*支持中心` 及其地点子节点 | Phase 1 部门/地点（不重导） | B1c |
| `infra_ci` | `基础架构`（网络/服务器/数据库） | CMDB CI（infra 类型） | B1d |
| `exclude` | `test`、`test2`、`其他` | 不迁 | — |
| `UNKNOWN` | 其余（本次：无，`KOMS主客户实施` 已归 `business_system`） | 待确认，阻塞相关批次 | — |

> 判定以 `cti_tree.json` 的 `parentId/ctiName` 树形为准；同名节点以 `ctiId` 区分，不得按名合并。

## 3. 映射规则

- **分类**：`ticket_category` 节点 → seed `ticket_categories`（182；键 `code`）；无候选者业务确认后补建（`code/itsm_type/default_priority/sla_tier/parent_code`）。
- **业务系统**：`business_system` 节点 → CMDB `business_system` CI（名称、环境、关键度按旧树层级/业务确认）；**不写入分类**。
- **优先级**：P0→`urgent`/`P1`、P1→`high`/`P2`、P2→`medium`/`P3`、P3→`low`/`P4`；旧分钟数不迁移；`ticket_templates.priority` 逐项归一化。
- **SLA**：以 seed 7 条为准；不按旧 ID。
- **日历**：`sla_definitions.business_hours`（`work_days/start_time/end_time/time_zone/holiday_list`）；2024–2026 全国基线已展开（89 假日/19 补班）；**午休时段、周末补班、时区键未消费 = 能力差额**。
- **矩阵**：`priority_matrix` → `ticket_automation_rules`（conditions=影响/紧急，actions=设优先级），需确认条件字段与命中顺序。
- **路由**：`cti_authorized` → `ticket_assignment_rules`，保留 `ctiId/definitionId/taskDefKey/moduleId/租户/authorizedType/authorizedId`；`authorizedType{0,2}` 语义与 14 孤立 `ctiId`（118 路由）先处置。
- **模块**：IN→incident、SR→service_request_item、SERVER→generic、KN→知识库（排除 WorkItem）。

## 4. 目标核验（每次写入前）

只读核对并记录：
- 容器/镜像 `sha256`、库/schema、owner、PG 版本、扩展；
- `schema_migrations` 条数与 head（本次 36 / `046_auth_token_state`，无 R038）；
- Phase 1 行数不变量（tenants 2 / departments 7975 / users 7862 / roles 36 / permissions 349 / role_permissions 1414 / user_roles 7 / external_identities 2）；
- 目标规范配置与业务表为空（本批前）；
- 单一写入者与窗口确认。

## 5. 批次、幂等与中断恢复契约

- 依赖顺序：B0 规范配置 seed → B1 分类/资产/组织/基础设施分流 → B2 字典字段 → B3 优先级/矩阵 → B4 SLA 日历 → B5 路由 → B6 模块/流程对照。
- 幂等键：`(source_system, resource_type, source_tenant_namespace, source_tenant_id, source_id, module_scope, target_tenant)`；一对多字典另带目标实体/字段。
- 每批单事务 + 成功检查点原子提交；记录源 sha256、映射修订、插入/更新/拒绝、前后摘要；失败整体回滚且不推进检查点。
- 提交后回执丢失：读检查点核对摘要，一致返回既有结果，不重复写；不确定则阻塞人工核验。
- 源/映射摘要变化拒绝复用旧检查点。

## 6. 证据与文档清单（仓库内）

| 文档 | 作用 |
| --- | --- |
| 本手册 | 可复用流程与判定规则 |
| `docs/superpowers/specs/2026-09-14-legacy-config-migration-gap-and-solution-design.md` | GAP 台账与决策 D1–D4 |
| `docs/review/2026-09-14-config-mapping-workbook.md` | 优先级/SLA 日历/模块/批次 |
| `docs/review/2026-09-14-cti-mapping-worksheet.md` | CTI 节点分流与逐节点映射 |
| `docs/review/2026-09-14-dictionary-option-reconciliation.md` | 字典→字段选项对账（已覆盖/差额/未接纳） |
| `docs/migrations/2026-09-14-b0-seed-admission-dry-run.md` | B0 规范 seed 准入 dry-run（纳入/排除/依赖/契约） |
| `docs/review/2026-09-14-b0-seed-admission-evidence.md` | B0 执行证据（计数/完整性/幂等/不变量） |
| `docs/review/2026-09-14-workitem-config-migration-handoff.md` | G-B 交接（消费固定 GARevision） |

## 7. 决策日志

| 编号 | 决策 | 来源 |
| --- | --- | --- |
| D1 | 新 ITSM 分类为唯一权威，旧 CTI 仅映射来源 | 用户确认 |
| D2 | 以新 SLA 为准，旧分钟数记差异 | 用户确认 |
| D3 | 用 `business_hours` JSON，不新增日历表 | 用户确认 |
| D4 | 规范配置来源 = 固定制品 seed `0788a9bb` | 用户确认 |
| S5 | 旧 CTI 混合树按节点类型分流（业务系统→CMDB CI，服务→分类） | 用户确认 |
| S5.1 | 服务分类节点：AD账户申请→`ACC-AD-001`、O365邮箱账户申请→`COL-MAIL-001`、SSLVPN账号申请→`NET-VPN-001`、K3.5数据变更→`APP-IL-DAT-002`；邮箱导出/业务系统账号/业务系统服务→新建正式分类；`OA申请` 父容器→排除 | 用户确认 |
| S5.2 | 配置字典按 **seed 选项集对账**：关联组精确对账（已覆盖 5 / 差额 37），其余 178 组未接纳；不整包导入。差额逐项建议（归并/新增选项/排除）**已全部采纳** | 用户确认 |
| S6 | 路由授权语义：只读补抽 `sysUser/sysRole/sysGroup`；`authorizedType=0`→**角色**（82/82 命中 `roleId`）、`=2`→**用户**（570 行命中 `userId`；68 行/20 个 ID 未命中，阻塞相关路由） | 用户确认 + 实测 |
| S7 | B0 准入固定 seed 规范配置（分类/SLA/目录/字段/CI类型/标签/视图等）；**`process_bindings` 拆出 B0** 至独立流程初始化批次；`departments/teams/roles` 与历史数据排除 | 用户确认 |
| S8 | `sla_policies`(3) 与 `incident_categories`(8) **均未接纳**；事件分类概念映射到 182 树已有 38 个 `itsm_type=Incident` 分类（B0 §3.1），不新增扁平节点 | 用户确认 |
| S9 | **B0 已执行**：`generate_seed_sql.py` 生成幂等 SQL，备份+回滚预演+单事务写入 `itsm_ga_ready`（租户 1）；结果 分类182/模板10/字段59/SLA7/目录8/CI9/标准变更3/KE1(占位)/标签4/视图5；幂等复跑 0 新增；Phase 1 不变量与账本不变 | 用户授权 + 实测 |

## 7.1 删除/排除登记（不得在后续批次再纳入）

| 对象 | ctiId | 处置 | 日期 | 依据 |
| --- | --- | --- | --- | --- |
| `OA申请`（父容器） | `6217e2ebb22b4ef890d0af9af31c8f7a` | 迁移排除/删除，不写入目标；**不改动旧源数据** | 2026-09-14 | 用户确认 |


## 8. 下一次迁移复用清单

- [ ] 换 `ITSM_URL` 重跑 S1–S3，比对 manifest 摘要。
- [ ] 固定新目标制品/库身份与 `GARevision`。
- [ ] 复用 §2 节点类型判定与 §3 映射规则；仅复核有变化的企业口径。
- [ ] 按 §5 批次与幂等契约执行；每批留证。
- [ ] 更新本手册的决策日志与证据清单。
