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

### 触发条件要说准：是**台账里的 037 行**，不是证据表

```go
// migration/migrator.go
for _, a := range applied {
    if a.Version == WorkItemPrepareVersion {   // 037_work_item_structure_preparation
        verifyPreparationReceipt(...)          // 回执检查在这里触发
    }
}
```

只要该库的 `schema_migrations` 里有 `037_work_item_structure_preparation`，就必然做回执比对。
**因此"克隆时不复制证据表"是无效的解法**——037 台账行仍在，照样触发、照样失败。（这一点曾经被我们错误地假设过，实测纠正。）

反过来，**没有 037 台账行的库不会被这道检查拦**：实测对一个无回执的库跑 `-status`，它前进到了另一个合法检查（迁移校验和不匹配）才被拒。

### 另外两项与回执无关、但同样会拦人的检查

| 检查 | 含义 |
| --- | --- |
| `runtime requires migration <version>` | `ControlledMigrationCatalog()` 里**每一条非 retire 迁移都必须已应用** → 落后于 catalog 的库过不了 |
| `migration checksum mismatch for <version>` | 台账里记录的校验和必须与当前 canonical SQL 一致 → **事后改过历史迁移的库过不了** |

## 4. 克隆库的定位（已决定，2026-09-19）

**结论：克隆库不做"可迁移目标"。** 开发库是唯一受控迁移目标；克隆库只承担"旧 ITSM 数据导入演练与验证"，可随时重建、用完可丢。

理由是三条一起看：

1. **要成为可迁移目标，唯一合规途径是为它签发一张真正的准备回执**（保留 037 台账行 + 与之匹配的证据）。那是 `privileged full preparation`，按 AGENTS.md 需要**单独授权**，且会长期增加治理面。
2. **而它的收益可以由更便宜的方式获得**：开发库应用完迁移后**重新克隆**，克隆库就继承了最新结构与数据——**不需要在克隆库上跑迁移**。
3. **真正需要反复演练的是数据导入**（旧 ITSM 的配置、组织、派单路由），那是**数据写入**，走应用角色与 RLS，**根本不经过这道门禁**。

配套结论：

- **不要改写回执来"修好"克隆库**——AGENTS.md 要求 preserve historical SQL/checksums and truthful receipts；改写回执就是伪造证据。
- 克隆库要跑后端时，注意它必须**不落后于 catalog**（否则缺列，见第 5 节）；办法是重建克隆，而不是在它上面补迁移。

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
| `itsm_migration_20260914` | 048 | 有，但写的是源库名 | ❌ | 演练克隆库；缺 049/050/051（按第 4 节，不打算迁它，需要时重建） |

**已退休（2026-09-19，删除前均有备份）**：`itsm`（019）、`itsm_baseline_20260908`（019）、`itsm_intake_test`（无台账）、`itsm_p1_integration_verify_20260901`（022）。
引用排查结论：前三个历史库**只被文档引用**，无脚本或配置依赖；`itsm` 曾被 `.env` 与 `deploy-dev.sh` 默认值引用（正是下面那条要修的坑）。

备份在 `/var/backups/itsm/retired_<库名>_20260919.dump`（+ `.sha256` 边车）：

| 库 | 备份大小 | 归档对象数 | sha256 |
| --- | --- | --- | --- |
| `itsm` | 5.2M | 11347 | `1edb493cdb2c26f5…` |
| `itsm_baseline_20260908` | 4.5M | 9322 | `0763c6450a3a0135…` |
| `itsm_intake_test` | 424K | 1056 | `2407eb869a73af57…` |
| `itsm_p1_integration_verify_20260901` | 620K | 1390 | `296ba4c2d5eae714…` |

恢复方式：`pg_restore -d <新库名> /var/backups/itsm/retired_<库名>_20260919.dump`（custom 格式，已用 `pg_restore -l` 校验可读）。

### 库名默认值必须指向当前开发库（已修）

历史库名曾同时出现在三处，**照默认值部署会连到 019 旧库**（表现为部门模块直接报列不存在）：

| 位置 | 修改前 | 修改后 |
| --- | --- | --- |
| `scripts/deploy-dev.sh` | `${DB_NAME:-itsm}` | `${DB_NAME:-itsm_config_baseline_20260908}` |
| `.env.dev.example` | `DB_NAME=itsm` | `DB_NAME=itsm_config_baseline_20260908` |
| `.env.example`（通用模板） | `DB_NAME=itsm` | 保留但**标注为占位符**（通用模板不应写死某个实例的库名） |


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
- ❌ 对已退休的历史库（如 `itsm`，台账 019）执行 `-up`——那会一次应用大量未评审迁移
- ❌ 用 `Count()` 之类不引用目标列的查询去"证明"迁移已生效
- ❌ 直接 `kill` 维护栈记录之外的进程，或在栈报 `identity mismatch` 时强行停止（见第 10 节）

## 10. 维护中的开发栈：启动方式与就绪探针

**不要用 `go run` 或裸 `nohup` 起后端。** 维护中的 WSL 栈由脚本托管：

```bash
cd /home/administrator/apps/itsm-kaf
./stack status            # 只读：每个服务的记录身份 vs 实际情况
./stack stop  itsm        # 停止单个服务
./stack start itsm        # 按 recipe 启动
```

- recipe 在 `/home/administrator/.local/state/itsm-kaf-baseline-20260908/config/<name>-launch.json`（含 `argv`/`cwd`/`env`/`source_revision`/`artifact_sha256`）。
- **`stop` 会拒绝漂移**：若记录的进程身份与实际不一致，它抛 `identity mismatch ...; refusing to stop actual process`，**不会**误杀复用 PID 或他人进程。这是保护机制，别绕过。
- 栈只把 `HOME/LANG/TZ/PATH` 加上 recipe 的 `env` 传给进程——**recipe 里没有的变量，进程就没有**。这也是为什么"绕开 recipe 手动起的进程"会缺配置。

### 三条实测结论（本环境当前状态）

1. **就绪路径是 `/api/v1/readyz`**；探测 `/readyz` 会得到 404，容易被误读成"这个构建没有就绪路由"（我们就被误导过一次）。健康端点 `/api/v1/healthz` 正常返回 200。
2. **recipe 里本来就有检查身份配置**（`ITSM_MIGRATION_CONTROL_FILE` + 带口令的 `ITSM_MIGRATION_INSPECTION_DSN`），因此**经栈启动时准入能通过**。实测（只读跑 `InspectRuntimeDatabase`）：

   ```
   控制配置: DeploymentID="itsm-dev-20260916" InspectionRole="itsm_dev_inspection_20260916"
   运行时准入: 通过        → /api/v1/readyz 预期 200
   ```

   > **更正**：本文档早先一版写着"recipe 里没有 `ITSM_MIGRATION_*`，所以 readyz 必 503"——那是我读了**另一个文件**（`active-release.json`，发布记录）得出的错误结论。真实的 launch recipe 里是有的。
   >
   > 当前 503 的真正原因是：**8080 上那个进程是绕开 recipe 手动启动的**，它的进程环境里没有这些变量。

3. **8080 上的进程已漂移**，而且比"漂移"更严重：它的**自证来源与实际记录都对不上**——

   | 项 | 值 | 在 main 上？ |
   | --- | --- | --- |
   | 二进制自证 `vcs.revision` | `51678954`（一个**前端**提交） | ✅ 在 |
   | recipe 记录的 `source_revision` / 文件名 | `d310caba` | ❌ **不在** |
   | 自证 `vcs.modified` | `true`（带未提交改动构建） | — |

   结论：**该后端无法由任何记录的修订重建**。要切换版本，需先把栈记录与实际对齐，**不能靠强杀**。

### 构建产物必须能自证来源（本环境教训）

- `go build main.go`（**按文件**构建）**不写 VCS 戳**；在 **git worktree** 里构建同样不写。
- 只有**普通 clone + 包形式** `go build -o <bin> .` 才会得到 `vcs.revision` / `vcs.modified`，可用 `go version -m <bin>` 校验。
  这才让产物的来源可被独立验证，而不是只靠 recipe 里的一句声称。

### ⚠️ 安全问题：recipe 里内嵌口令，却是 0644

`config/itsm-launch.json` 及历史 `itsm-launch.before-*.json` 的 `env` 含**明文数据库口令**（`ITSM_MIGRATION_INSPECTION_DSN` 里的 inspection 角色口令），但文件权限是 **`0644`（任何本地用户可读）**。

建议两步收敛：

1. 立即把该目录下所有含口令的 recipe 收紧为 **`0600`**；
2. 让代码支持 `ITSM_MIGRATION_INSPECTION_DSN_FILE`，与本仓库既有的 `*_PASSWORD_FILE` / `*_SECRET_FILE` 约定一致——recipe 只放路径、不放口令（属代码改动，需单独提出）。


## 相关

- [开发指南](../DEVELOPMENT_GUIDE.md)、[命令参考](../dev-commands-reference.md)
- [WorkItem 切换与恢复手册](./workitem-convergence-cutover.md)、[受控退役目标环境执行 Runbook](./workitem-controlled-retirement-target-runbook.md)
- 脏负责人清除证据：[部门脏负责人值清除证据](../migrations/2026-09-18-dirty-department-manager-cleanup.md)
- 组织节点类型设计：《组织》设计（`docs/superpowers/specs/2026-09-18-organization-model-design.md`）
