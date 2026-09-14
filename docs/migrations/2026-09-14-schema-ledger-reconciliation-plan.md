# ITSM 迁移账本对账方案（只读方案，不执行）

- 日期：2026-09-14
- 状态：**Plan only — 不改任何数据库、不改任何代码**
- 触发问题：workitem 库重构与 ITSM↔KAF 合并是否已落地？各环境迁移账本是否一致？
- 依据：本地库实测（`schema_migrations` 明细 + 对象存在性）、迁移注册表源码、运行目录证据、8–9 月文档

---

## 1. 范围

本方案只做两件事：

1. 把「各环境账本 vs 代码注册表」的差异**量化并取证**（Phase A，全只读）。
2. 给出 dev 库的**可执行收敛路线**与前置条件（Phase B，需人工批准后才执行）。

明确不做：不在本方案里执行任何迁移、不 repair 账本、不 `-up`、不 restore。

---

## 2. 实测现状矩阵（2026-09-14）

### 2.1 环境 × 账本 × 关键对象

| 环境 / 数据库 | 账本条数 | 账本 head | workitem 对象 | KAF 集成对象 | 旧残留（应已删） |
|---|---:|---|---|---|---|
| `itsm`（`itsm-backend/.env` 指向的 dev 库） | 14 | `019_kaf_execution_integrity_rls` | ❌ 无 `work_item_number_sequences` | ❌ | ✅ `workflows`、`ticket_approvals` 仍在；`ticket_categories.workflow_id` 仍在；`releases.requires_approval` 仍在 |
| `itsm_baseline_20260908` | 14 | `019` | ❌ | ❌ | 同上（与 dev 同代） |
| `itsm_intake_test` | **无 `schema_migrations` 表** | — | ❌ | ❌ | 未版本化库 |
| `itsm_p1_integration_verify_20260901` | 15 | `022_drop_professional_extension_shared_fields` | ✅ | ❌ | 已清理 |
| **`itsm_config_baseline_20260908`** | 24 | `031_kaf_action_request_digest` | ✅ | ✅ | 已清理 |
| `itsm_candidate`（候选栈） | 38 | `046_auth_token_state` | ✅ | ✅ | 已清理 |
| **生产** | — | — | **本机不可见** | — | `docker-compose.prod.yml`：生产 PG 为外部托管共享实例 |

### 2.2 `itsm_config_baseline_20260908` 的身份已确认

它不是一个普通备份，而是**ITSM↔KAF 合并基线环境**正在使用的库：

- 运行目录：`/home/administrator/.local/state/itsm-kaf-baseline-20260908`
- 宿主进程：`itsm-api-support-handoff-20260911`、`itsm-worker` ×2
- 库配置：`config/itsm/config.yaml` → `dbname: itsm_config_baseline_20260908`，角色 `itsm_base_app_20260908`
- KAF 对接：`KAF_WEBHOOK_URL=http://127.0.0.1:8000/webhooks/itsm`、`INTAKE_IDENTITY_CONFIG_FILE`
- 验收证据：`evidence/acceptance-independent-review-20260908.md`、`reports/20260911-support-handoff-deployment.md`

该部署报告自身声明：**"No schema migration performed"**、**"No claim of ... production readiness"**。

---

## 3. 三条彼此独立的分叉（必须分开处理，不要混为一谈）

### D1 代码线分叉（最严重）

| 项 | 实测 |
|---|---|
| `main` | 注册表 head = `031_kaf_action_request_digest` |
| 候选线 `codex/security/candidate-auth-state-windows` @ `d4bedf7b` | head = `046_auth_token_state` |
| 差距 | `main...d4bedf7b` = **0 / 251** → main 是候选线的祖先，候选线**领先 251 个提交且从未合回** |
| 候选栈运行 | 就是这条候选线（容器镜像 `sha256:7ae6051efd0e…`，绑定 `codex/fix/candidate-incident-status` 等 6 个分支） |

含义：`main` 上根本没有 `032–046` 的迁移文件；它们只存在于候选线 worktree。**对账的"目标 head"必须先由人选定**（见 §6 的决策点 X）。

### D2 环境账本分叉

- 同一实例上并存三代：`019` / `022` / `031`，另有候选栈 `046`。
- **dev 账本不是候选账本的前缀**：dev 独有 `010_add_ticket_types`、`015_add_service_request_contact_fields`，候选线均无。
- 因此**不能**简单把 `020→046` 顺序重放到 dev。

### D3 dev 账本内部的历史异常

| 异常 | 证据 | 性质 |
|---|---|---|
| `010_add_ticket_types` | `migrations.go` 的 `LegacyMigrations` 显式收录，描述为 "Retired: ticket_types is now owned by the Ent schema; retained only for checksum/history lookup" | **已退役版本**，active 入口禁止重放 |
| `015_add_service_request_contact_fields` 与 `016_add_service_request_contact_fields` | 两行 `checksum` **完全相同**（`917e74af4aca`） | **同一 SQL 的两个版本号（重复记录）**，`015_` 为历史残留 |
| `release_version` 多为 `unversioned` | 仅 `019` 为 `kaf-delegation-closeout` | 无法据发布版本追溯制品 |
| `itsm_intake_test` 无账本 | `relation "schema_migrations" does not exist` | 未版本化库，属 provisioning 文档点名的危险态 |

---

## 4. 决定"能做什么"的机制约束（先读，再定方案）

1. **校验和是硬闸门**。`migration/migrator.go` 对**已记录**的迁移比对
   `checksumSQL(GetMigrationSQL(version))`，不一致直接
   `migration checksum mismatch for <version>: applied=… current=…` 报错，**绝不静默重放**。
2. **SQL 内嵌在二进制里**。`GetMigrationSQL` 是 `migrations.go` 内的 switch，`.sql` 文件只是编写源。
   所以校验和不匹配有三种可能，不能一律判定为"账本被篡改"：
   (a) SQL 确实改过；(b) **执行迁移的二进制版本与当前不同**；(c) 账本被手工改过。
   → **对账必须用与目标环境一致的二进制**，否则必然假阳性。
3. **`LegacyMigrations` 明确禁止重放**（`010_add_ticket_types` 在其中）。
4. **RLS 约束**：合并基线环境以 `RLS_MODE=enforce` 运行（见其 `config/itsm-launch.json`），多支迁移含
   `ALTER TABLE … ENABLE/FORCE ROW LEVEL SECURITY` 与策略重建，必须用 **owner 角色**执行；
   其他环境的 RLS 模式需各自确认后再照此办理。
5. **expand/contract**：`docs/runbooks/production-initialization.md` 要求优先回滚镜像、不反向删运行数据。

---

## 5. Phase A：只读对账（无副作用，可立即做）

### A1 逐环境导出账本

```bash
# 每个目标库各跑一次；<DSN> 由负责人提供，勿写入仓库
psql "$DSN" -c "\copy (select version, applied_at, left(checksum,12) as ck, execution_ms, release_version
                      from schema_migrations order by version) to stdout with csv header"
```

### A2 逐环境对象级抽查

```bash
psql "$DSN" -c "
select
  to_regclass('public.work_item_number_sequences') is not null as workitem_allocator,
  to_regclass('public.intake_requests')             is not null as intake_requests,
  to_regclass('public.kaf_task_action_ledgers')     is not null as kaf_ledger,
  to_regclass('public.kaf_task_completion_receipts')is not null as kaf_receipt,
  to_regclass('public.process_callback_outboxes')   is not null as kaf_callback_outbox,
  to_regclass('public.workflows')                   is not null as legacy_workflows_left,
  to_regclass('public.ticket_approvals')            is not null as legacy_ticket_approvals_left,
  (select count(*) from information_schema.columns
     where table_name='tickets' and column_name='generic_subtype') as tickets_generic_subtype,
  (select count(*) from information_schema.columns
     where table_name='ticket_categories' and column_name='workflow_id') as cat_workflow_id_left;"
```

### A3 用「与环境一致的二进制」跑框架状态（确认校验和是否通过）

```bash
# 必须使用目标环境实际部署的那个制品，否则 §4.2 的假阳性
cd <与环境一致的 itsm-backend 源码/制品>
DB_DSN="$DSN" go run -tags migrate cmd/migrate/main.go -status
```

预期：输出 `Applied` / `Pending` 两段；若出现 `checksum mismatch`，按 §4.2 三因排查，不要直接改账本。

### A4 产出物

一张对账表：`环境 × 账本条数 × head × 校验和是否通过 × 缺失清单 × 多余清单 × 对象抽查结果`，
外加每个"多余条目"的定性（Legacy / 重复 / 未知）。

---

## 6. Phase B：dev 库收敛路线（需人工批准后才执行）

### 前置决策点 X：目标 head 是 `031` 还是 `046`？

- 若目标是**合并基线（031）** → 与现有 `itsm-config_baseline_20260908` 对齐。
- 若目标是**候选线（046）** → 必须先完成 D1 的代码线收敛（§7），否则没有对应制品。
- 本方案建议：**先按 `031` 收敛**，因为当前 main 的注册表就是 031，制品存在且已在运行。

### 路线 B1（推荐）：dev 库可丢弃 → 从零重建

适用：dev 库无必须保留的业务数据。

1. 按 `docs/runbooks/production-initialization.md` 的流程，用**与环境一致的制品**在全新库上做正式初始化
   （`ITSM_BOOTSTRAP_ONLY` → migrate → initialize → verify）。
2. 完成后跑 §5 A2/A3 复核，head 应为 031 且校验和全绿。
3. 旧 dev 库保留为只读快照，不删除。

优点：无账本修复风险；与生产上线路径同构。缺点：丢 dev 里的临时数据。

### 路线 B2：dev 库必须保留 → 账本修复 + 前向补迁移

**必须先在克隆库演练**（沿用本项目已有先例：如 `itsm_migration_20260914` 这类同实例克隆库）。

B2-1 **冻结与备份**：停止对 dev 的写入；`pg_dump -Fc` 全量备份并记录 sha256。

B2-2 **修账本异常（D3）**，仅动账本、不动业务表：

| 异常 | 建议动作 | 说明 |
|---|---|---|
| `010_add_ticket_types` | 保留**不改**（代码已把它归入 Legacy，`Status` 不会要求重放） | 仅在对账表中标注为"已退役" |
| `015_add_service_request_contact_fields` | 与 `016_` 校验和相同 → 标注为重复；如需清理须**单独评审**后再删行 | 删账本行是高风险动作，默认不做 |
| `release_version = unversioned` | 不回填 | 缺信息，编造即伪造历史 |

B2-3 **逐支前向补迁移** `020 → 031`，每支执行前先跑该迁移自带的 preflight：

- 高危支（含 DROP / 数据前置校验）：`020_work_item_number_allocator`、`022_drop_professional_extension_shared_fields`、
  `027_work_item_identity_field_retirement`、`028_service_request_work_item_authority`。
- 迁移自带 `*_verify.sql` 的，先跑 verify 再跑迁移。
- 任一支 preflight 报错 → **停**，不要跳过、不要手工补数据。

B2-4 **校验和处置**：若 `-status` 报某支 mismatch，先判定是"制品版本不同"还是"SQL 改过"；
**不得**为过校验而改写账本校验和。

B2-5 **验收**：迁移后跑 §5 A2/A3；head=031；KAF 集成对象齐备；旧残留对象为空。

### 路线 B3：暂不收敛

把 dev 标记为"历史只读"，所有新验证改用候选栈或克隆库。适用于 dev 已无价值但需保留取证。

---

## 7. Phase C：代码线收敛（不在本方案内，但必须先立项）

`main` 落后候选线 251 个提交，且候选线承载了 032–046 的迁移与生产化改动。
在 D1 未收敛前：

- 不要基于 `main` 向任何环境推新迁移（会产生第三个分叉）。
- 需要一份独立的**分支收敛计划**（范围、评审、迁移合并顺序、回滚点），由人指定负责人。

---

## 8. 非目标

- 不执行任何迁移、不 restore、不改账本、不删账本行。
- 不动 `itsm_config_baseline_20260908`（合并基线在跑）。
- 不动候选栈（`itsm-candidate-20260914-*`）。
- 不碰生产。
- 不处理 KAF 侧 `control_plane`（其 alembic 停在 `036`，代码 head `038`，属另一条待办）。

## 9. 验收标准

1. §5 对账表覆盖全部本地环境，且每处差异都能归因到 D1/D2/D3 之一或"未知（需进一步取证）"。
2. 选定路线（B1/B2/B3）并记录决策人与理由。
3. 若走 B2：克隆库演练先行通过，dev 备份 sha256 已记录，逐支 preflight 全绿。
4. 收敛后 `-status` 无 `checksum mismatch`，head 与决策点 X 一致。

## 10. 附：生产核对清单（需 DBA / 生产主机执行）

```bash
psql "$PROD_DSN" -c "select version, applied_at, release_version from schema_migrations order by version desc limit 5;"
psql "$PROD_DSN" -c "select to_regclass('public.work_item_number_sequences') as win,
                            to_regclass('public.kaf_task_action_ledgers')    as kaf,
                            to_regclass('public.workflows')                  as legacy_wf,
                            to_regclass('public.ticket_approvals')           as legacy_ta;"
```

判定：`win`/`kaf` 非空且 `legacy_wf`/`legacy_ta` 为空 → workitem 与 KAF 集成都已上生产；
否则均**未上生产**。在拿到这三行输出之前，任何"已上生产"的说法都不应被采信。
