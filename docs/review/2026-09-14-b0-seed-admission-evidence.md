# B0 执行证据：规范 seed 配置准入 `itsm_ga_ready`

- 状态：**EXECUTED**（2026-09-14）。本文件记录实际写入的结果与核验。
- 计划：`docs/migrations/2026-09-14-b0-seed-admission-dry-run.md`
- 消费门禁：`GARevision=d91b587fe3ab40cc863321346d217d258a3a96d8`
- 目标：容器 `ga-itsm-20260914`，库 `itsm_ga_ready`，owner `ga_owner`，schema `public`，写入租户 `tenant_id=1`（`default`）。

## 1. 源与制品

| 项 | 值 |
| --- | --- |
| 源（固定制品 seed） | 制品 `0788a9bb` 的 `itsm-backend/config/seed/default.json` |
| 源 sha256 | `372d605235f45b597e5b4ba256683cdc904a8ea32e4a2939c074e423d89ac191` |
| 生成器 | `scripts/migrate_config_seed/generate_seed_sql.py` |
| 生成 SQL sha256 | `bb1a5c48ba97efb02628ccee816b371e4b477f301c27d7b65211cbccebe70c63`（295 行） |
| 写入前备份 | `/home/administrator/.local/state/itsm-task2-b0-20260914/itsm_ga_ready-pre-b0.pgdump`（`pg_dump -Fc`，另存 sha256 `6c3861587ae58367159ecd1f5be08edff97ed6e3740cf5ca00244ac2676bec10`） |

## 2. 执行方式（受控、单事务）

1. 写入前复核目标指纹与单一写入者（见 §5）。
2. 备份目标（`pg_dump -Fc`，见 §1）。
3. **事务回滚预演**：`sed 's/^COMMIT;/ROLLBACK;/' | psql -v ON_ERROR_STOP=1` → 全部语句成功、无错误；复核计数仍为 0（未落库）。
4. **正式执行**：`psql -v ON_ERROR_STOP=1 < b0_seed.sql`（`BEGIN … COMMIT` 单事务）→ `COMMIT`。
5. 幂等复跑：再次执行同一 SQL → 新增 0 行、计数不变。
6. 校验（见 §3、§4）。

## 3. 结果计数（写入后）

| 表 | 期望 | 实际 |
| --- | ---: | ---: |
| `ticket_categories` | 182 | **182** |
| `ticket_templates` | 10 | **10** |
| `field_definitions` | 59 | **59** |
| `sla_definitions` | 7 | **7** |
| `service_catalogs` | 8 | **8** |
| `ci_types` | 9 | **9** |
| `standard_changes` | 3 | **3** |
| `known_errors` | 1 | **1** |
| `ticket_tags` | 4 | **4** |
| `ticket_views` | 5 | **5** |

## 4. 完整性与幂等

- 分类层级：`L1=8 / L2=38 / L3=136`；`level>1` 无 `parent_id` 孤儿 = **0**。
- 模板→分类引用：`category_ids` 指向不存在分类 = **0**。
- 字段→模板引用：`entity_type='ticket_template'` 且无对应模板 = **0**。
- 幂等复跑：新增 `INSERT 0 1` = **0**；分类/模板/字段计数稳定 `182/10/59`。
- 未纳入：`departments/teams/roles`（Phase 1 身份域）、`process_bindings`（拆分到流程初始化批次）、`sla_policies`、`incident_categories`（均未接纳）、历史数据（0）。

## 5. Phase 1 与账本不变量（写入前后一致）

| 项 | 值 |
| --- | --- |
| tenants / departments / users | 2 / 7975 / 7862 |
| roles / permissions / role_permissions / user_roles | 36 / 349 / 1414 / 7 |
| external_identities | 2 |
| schema_migrations | 36 条，max `046_auth_token_state`（无 R038） |

## 6. 说明与剩余

- `known_errors` 写入的是**模板样例/占位**（标题 `Known Error 模板：请替换为真实问题标题`），非真实故障；不得计入 G-B 业务验收的真实数据证据。
- `ticket_categories` 的 47 个纯容器节点（`itsm_type/default_priority/sla_tier` 为空）按 seed 原样保留。
- 仍待推进：路由批次 20 个未解析用户 ID、B1–B6 映射批次。规范流程初始化批次**已完成**（见 `docs/review/2026-09-14-process-init-evidence.md`），B0 不再留有悬空流程绑定。
- 本批只写配置主数据，未触碰历史工单/流程实例/身份/账本。
