# 规范化迁移准入与克隆库约束

**状态**：本机开发环境实测结论（2026-09-19）＋ 049/050/051 的执行记录。适用于所有需要在既有库上变更 schema 或数据的工作。

**一句话结论**：既有库的变更**只能**走 `cmd/migrate` 这条规范化路径；该路径有运行时准入，而准入要求**一张写着目标库名的准备回执**。因此"哪个库能被迁移"由回执决定，不由你连哪个库决定——**用 `TEMPLATE` 克隆出来的库永远过不了准入**。

---

## 1. 规范路径：`cmd/migrate`

```bash
cd itsm-backend

DB_HOST=localhost DB_PORT=5432 DB_USER=itsm_user DB_PASSWORD=<...> \
DB_NAME=<目标库> DB_SSLMODE=disable \
ITSM_MIGRATION_CONTROL_FILE=/home/administrator/.itsm/migration-control.json \
ITSM_MIGRATION_INSPECTION_DSN="postgres://itsm_dev_inspection_20260916@localhost:5432/<目标库>?sslmode=disable" \
go run -tags migrate ./cmd/migrate -status        # 只读：看状态与待执行清单
```

其他子命令：`-dry-run -up`（只打印将执行的 SQL）、`-up`（执行）、`-list`、`-version`。
**`-dry-run` 与 `-status` 是只读的，变更前先跑它们。**

> `cmd/migrate` 带 `//go:build migrate` 标签：**不加 `-tags migrate` 会报 "build constraints exclude all Go files"**。

### 为什么不能用应用的 `AutoMigrate`

`AutoMigrate=true` 会执行 `client.Schema.Create(ctx)`（**Ent 建表**）。这正是 AGENTS.md 禁止的 *"existing targets advance through canonical migrations **without Ent overlays**"*。它只适用于全新空库（`-fresh` 那条开发路径），**不是既有库的迁移手段**。

## 2. 运行时准入的三项要求

| 要求 | 内容 | 本机现状 |
| --- | --- | --- |
| 受信任控制文件 | `ITSM_MIGRATION_CONTROL_FILE` 指向一个 JSON，须是非 group/world 可写的普通文件；`DeploymentID` 必填；`Operator` 由**当前 OS 用户**推出，不来自文件 | `/home/administrator/.itsm/migration-control.json`（0600） |
| 独立只读检查身份 | `ITSM_MIGRATION_INSPECTION_DSN` 的**用户名必须等于** 控制文件里的 `InspectionRole`；该角色只能对 `schema_migrations` 与 `work_item_migration_evidence` 有 SELECT | 角色 `itsm_dev_inspection_20260916` 已就绪 |
| 回执绑定目标 | 已存在准备回执时，其 `Target.Database` / `Target.Schema` 必须与连接的 `current_database()` / schema 一致 | 见第 3 节 |

检查身份的硬约束（准入会逐条核验，任一不满足即拒绝）：非超级用户、无 `bypassrls`、无 `createrole/createdb/replication`、**不隶属任何角色**、不拥有任何对象、无 schema CREATE 权限、除上述两张表外**无任何 SELECT**、无序列权限；证据表上必须**恰好一条**非属主授权（该角色的 SELECT，不可转授），且不得存在列级授权。

> 本机 Postgres 对 `127.0.0.1` 是 **trust** 认证，因此连接检查身份**不需要密码**，也不需要改动该角色的凭证。

## 3. 关键约束：回执绑定的是**库名**

准入会比对：

```go
attachment.Evidence.Target.Database != database || attachment.Evidence.Target.Schema != schema
// → "preparation receipt target mismatch"
```

后果：**能迁移的库 = 回执上写的那个库名**，与"你想迁哪个库"无关。

本机实测：回执写的是 `itsm_config_baseline_20260908`，所以**只有它能走门禁**；对克隆库 `itsm_migration_20260914` 会直接报该错误。

## 4. 克隆库的固有限制（后续开发必须知道）

`CREATE DATABASE target TEMPLATE source` 会把源库的**准备回执一起复制过来**，而回执里仍写着**源库名**。于是克隆库处于一种自相矛盾的状态：*看起来已准备*，但回执对不上它自己。

由此得出三条结论：

1. **克隆库不具备受控迁移资格**。要跑后端或迁移，请用回执绑定的库。
2. **不要改写回执来"修好"它**——AGENTS.md 要求 preserve historical SQL/checksums and truthful receipts；改写回执就是伪造证据。
3. 克隆库的正确用途是**只读对账/分析**。若确实需要可迁移的副本，应由运维按规范准备流程**为该库另行签发回执**，而不是复制源库的回执。

## 5. Ent schema 与迁移的先后：schema 迁移是**上线前置条件**

新增/修改 Ent 字段后，Ent 生成的查询列会引用新列（如 `node_type` 已进入 `ent/department/department.go` 的 `Columns`）。**任何**部门读写都会带上该列，因此：

> **部署新构建之前必须先应用对应迁移，否则部门功能整体不可用（不是"少个字段"，是直接报错）。**

实测（迁移前，三个库一致）：

```
pq: column departments.node_type does not exist at column 292 (42703)
```

**验证这类问题的正确探针**：用 `Query().First()`（选取全部列），**不要用 `Query().Count()`**——`Count()` 只发 `SELECT COUNT(*)`，不引用该列，**证明不了列是否存在**（我们一开始就用错了探针，导致误判"克隆库也好了"）。

## 6. 本机库状态对照（2026-09-19 实测）

| 库 | 迁移台账 | 回执 | 能否走门禁 | 说明 |
| --- | --- | --- | --- | --- |
| `itsm_config_baseline_20260908` | 051 | 有（写的就是自己） | ✅ | **当前开发库**；049/050/051 已应用 |
| `itsm_migration_20260914` | 048 | 有，但写的是源库名 | ❌ | 克隆库；仍缺 049/050/051 |
| `itsm` | 019 | 无（连证据表都没有） | ❌ | **陈旧库，落后 30 个迁移**；不要对它 `-up`，那会一次应用大量未评审迁移 |

## 7. 本次执行记录（2026-09-19）

| 项 | 值 |
| --- | --- |
| 目标库 | `itsm_config_baseline_20260908`（回执绑定的库） |
| 执行方式 | `cmd/migrate -up`（规范化路径，未绕过门禁、未使用 Ent overlay） |
| 写前备份 | `/home/administrator/.itsm/backups/pre_049_050_itsm_config_baseline_20260908.sql`（6.0M，`sha256:23a507ad9c6b32b6…`） |
| 写前备份（051 前） | `pre_051_itsm_config_baseline_20260908.sql`（6.0M，`sha256:0930fb4b4fbcebdf…`） |
| 应用结果 | `049_department_code_tenant_unique` 10:27:18、`050_department_node_type` 10:27:18、`051_department_manager_none_normalization` 10:30:51 |
| 结构校验 | `node_type` 列＝1、`idx_departments_tenant_code`＝1、`departments_node_type_value_check`＝1 |
| 功能校验 | `First()` 探针读取成功：`部门 id=1, node_type=""`（此前报列不存在） |
| 幂等校验 | 再次 `-up` → `No pending migrations` |
| 未触碰 | `038_work_item_controlled_retirement` 仍为 `pending_manual`（需人工/另行授权） |
| 数据更正 | 部门 635 的脏负责人（331，普通员工）按守卫清除为 `NULL`；写后 `NULL=7975、零=0、有负责人=0` |

## 8. 错误信息对照表

| 错误 | 含义 | 处置 |
| --- | --- | --- |
| `build constraints exclude all Go files` | 漏了 `-tags migrate` | 加上标签 |
| `pwd authentication failed for user "itsm"` | 没传全 `DB_*`，CLI 用了默认用户名 | 显式传 `DB_USER/DB_PASSWORD/DB_HOST/DB_PORT/DB_NAME` |
| `runtime migration admission requires explicit inspection identity and deployment configuration` | 没配控制文件或 `InspectionRole` 为空 | 配 `ITSM_MIGRATION_CONTROL_FILE` |
| `unreviewed evidence attachment access` | 检查身份未配／证据表 ACL 不满足"恰好一条只读授权" | 按第 2 节核验 ACL |
| `inspection role must have only ledger/evidence SELECT…` | 检查身份带了写权限/角色继承/所有权 | 只授予两张表的 SELECT |
| `preparation receipt target mismatch` | 回执写的库名 ≠ 当前连接库 | 换成回执绑定的库，或为当前库另行签发回执（见第 4 节） |
| `column departments.node_type does not exist` | 代码已含新字段但库未迁移 | 先应用迁移再部署（第 5 节） |

## 9. 禁止清单

- ❌ 用 `AutoMigrate` / `client.Schema.Create` 迁移既有库（Ent overlay）
- ❌ 绕过准入直接 `psql` 执行迁移 SQL，或手工补台账记录（会让台账失真、后续执行器重复应用）
- ❌ 改写/伪造准备回执，或把源库回执复制给另一个库充当"已准备"
- ❌ 对 `itsm`（台账 019 的陈旧库）执行 `-up`
- ❌ 用 `Count()` 之类不引用目标列的查询去"证明"迁移已生效

## 相关

- [开发指南](../DEVELOPMENT_GUIDE.md)、[命令参考](../dev-commands-reference.md)
- [WorkItem 切换与恢复手册](./workitem-convergence-cutover.md)、[受控退役目标环境执行 Runbook](./workitem-controlled-retirement-target-runbook.md)
- 脏负责人清除证据：[部门脏负责人值清除证据](../migrations/2026-09-18-dirty-department-manager-cleanup.md)
- 组织节点类型设计：《组织》设计（`docs/superpowers/specs/2026-09-18-organization-model-design.md`）
