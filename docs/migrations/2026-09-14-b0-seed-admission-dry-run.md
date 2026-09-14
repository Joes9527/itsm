# B0 dry-run：规范 seed 配置准入 `itsm_ga_ready`

- 状态：draft，供审查；**尚未执行任何写入**。
- 权威源：固定制品 `0788a9bb` 的 `itsm-backend/config/seed/default.json`（sha256 `372d605235f45b597e5b4ba256683cdc904a8ea32e4a2939c074e423d89ac191`）。
- 目标：`ga-itsm-20260914 / itsm_ga_ready / public`，owner/migration `ga_owner`。
- 消费门禁：`GARevision=d91b587fe3ab40cc863321346d217d258a3a96d8`（已独立核验，见 spec §1）。

## 1. 目标指纹（写入前需重新确认）

| 项 | 期望值（G-A） | 本次核验 |
| --- | --- | --- |
| 容器镜像 | `sha256:7ae6051efd0e…` | 一致 |
| 库 / owner / schema | `itsm_ga_ready` / `ga_owner` / `public` | 一致 |
| PG / 扩展 | 17.10 / plpgsql 1.0 + vector 0.8.6 | 一致 |
| 账本 | 36 条，head `046_auth_token_state`，无 R038 | 一致 |
| Phase 1 不变量 | tenants 2、departments 7975、users 7862、roles 36、permissions 349、role_permissions 1414、user_roles 7、external_identities 2 | 一致 |
| 规范配置现状 | 分类/SLA/字段/模板/目录/流程/规则 = 0 | 一致 |

> 写入前须再次确认单一写入者与窗口，并复核上述指纹；任一变化回传任务一并发布新 GARevision。

## 2. 纳入批次（B0 规范配置）

| 顺序 | seed 段落 | 行数 | 目标表 | 幂等键 | 备注 |
| ---: | --- | ---: | --- | --- | --- |
| 1 | `ticket_categories` | 182 | `ticket_categories` | `(tenant, code)` | 先插 L1/L2/L3，按 `parent_code` 解析 `parent_id`；seed 无 `itsm_type/default_priority/sla_tier` 的 47 个容器节点保持空 |
| 2 | `ticket_templates` | 10 | `ticket_templates` | `(tenant, name)` | `category`→code；`category_codes` 关联分类 |
| 2.1 | 模板 `fields` | 59 | `field_definitions` | `(tenant, entity_type, entity_id, name)` | `entity_type=ticket_template`，`options` 原样 |
| 3 | `sla_definitions` | 7 | `sla_definitions` | `(tenant, name)` | 目标已有 0 条；`business_hours` 见 B4 |
| 4 | `sla_policies` | 3 | **目标无此表**（`to_regclass` 为空） | — | **差额**：目标模型无 `sla_policies`；不得落入任意表/JSON，待确认归属（可能与 `sla_definitions` 合并） |
| 5 | `service_catalog` | 8 | `service_catalogs` | `(tenant, name)` | `target_class` 映射 `recordClass` |
| 6 | `ci_types` | 9 | `ci_types` | `(tenant, name)` | 含 `business_system`（第 1 项业务系统 CI 依赖） |
| 7 | `incident_categories` | 8 | **目标无此表**（`to_regclass` 为空） | — | **差额**：目标无 `incident_categories`，待确认归属 |
| 8 | `standard_changes` | 3 | `standard_changes` | `(tenant, title)` | 目标表已存在 |
| 9 | `known_errors` | 1 | `known_errors` | `(tenant, title)` | 目标表已存在；seed 为模板样例（占位标题） |
| 10 | `ticket_tags` | 4 | `ticket_tags` | `(tenant, code)` | 目标表已存在 |
| 11 | `ticket_views` | 5 | `ticket_views` | `(tenant, name)` | 目标表已存在 |

> **2026-09-14 用户确认**：`process_bindings`（7 条）**拆出 B0**，进入独立"规范流程初始化批次"（需先确认固定制品内的规范流程定义来源），避免悬空绑定；B0 不含流程绑定。

## 3. 排除（不纳入 B0）

| seed 段落 | 行数 | 排除理由 |
| --- | ---: | --- |
| `departments` | 14 | Phase 1 已保留真实部门（7975），重导会冲突/重复 |
| `teams` | 10 | 同上，身份域由 Phase 1 负责 |
| `roles` | 18 | 同上，且不得重置角色 |
| `incidents` / `problems` / `changes` / `knowledge_articles` | 0 | 历史/业务数据，明确不迁 |
| `seed_workflows` | bool | 仅开关；旧 BPMN 不导入（见 §4） |

## 4. 依赖与阻塞

- `process_bindings` 引用 `process_definition_key`（如 `ticket_general_flow`、`incident_emergency_flow`、`change_normal_flow`…）。G-A 交接明确规范流程**未初始化**，且不得导入旧 BPMN。因此 B0 要么同时准入**固定制品内的规范流程定义**，要么把 `process_bindings` 拆到流程初始化批次；**不能留下悬空绑定**。
- `sla_policies`、`incident_categories`、`standard_changes`、`known_errors` 的目标表/模型需按固定制品确认；缺失即列差额，不落入任意 JSON。
- `ci_types` 的 `business_system` 是第 1 项业务系统 CI 的前置；B0 必须先行。

## 5. 执行契约（审查通过后）

- 写入前：对目标做受保护备份并验证可恢复；记录目标指纹与 GARevision。
- 单批单事务 + 成功检查点；记录源 sha256、映射、插入/更新/拒绝、前后摘要。
- 幂等：以 §2 幂等键 upsert；同批重跑零增量。
- 失败整体回滚且不推进检查点；提交后回执丢失按检查点+摘要核对，不重复写。
- 校验：分类父子链与 code 唯一、模板↔字段↔分类引用、SLA/目录/CI 类型/标签计数与引用、流程绑定 key 全部有对应定义。
- 回滚：目标规范配置当前为空，失败即整批回滚；如需清理仅限本批新建对象，不动 Phase 1 与账本。

## 6. 验收与后续

- B0 通过后：分类可被模板/目录引用；`business_system` CI 类型可用；流程绑定无悬空。
- 后续：B1 分类/资产分流落位、B2 字典选项（对账表已确认）、B3 优先级/矩阵、B4 SLA 日历（`business_hours` + 2024–2026）、B5 路由（`authorizedType` 已解析）、B6 模块/流程对照。

## 7. 待你确认（窗口与依赖）

1. **写入窗口**与单一写入者确认。
2. `process_bindings` 依赖：是否在 B0 同时准入固定制品的规范流程定义，还是拆分为独立流程批次。
3. `sla_policies` / `incident_categories` / `standard_changes` / `known_errors` 的目标表确认。
4. `known_errors` 为模板样例（占位标题），是否纳入。
