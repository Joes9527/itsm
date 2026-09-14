# Shared-target 受控准备执行报告（admission + P + ordinary）

> 日期：2026-09-12。目标：WSL 现有共享栈 `itsm-postgres-dev` 内的**任务专属 database**。
> 范围：准入 → P(037) → 普通迁移；**不执行 R(038)/部署/退役/V1**，不触碰共享 Redis/MinIO 数据。
> 执行后已精确清理专属 DB 与角色；现有 `itsm` 等库未被改动。

## 1. 目标与隔离

| 项 | 值 |
|---|---|
| 共享容器 | `itsm-postgres-dev`（compose project `itsm`，健康，宿主 5432） |
| 专属 database | `workitem_target_20260912102644_8d3920`（执行后已删除） |
| 专属角色 | `..._owner` / `..._app` / `..._system` / `..._inspect`（执行后已全部删除） |
| Run ID | `workitem-target-20260912102644-8d3920` |
| 二进制 | backend `a431bb1e...`、migrate `52493359...`（与隔离全流程运行一致） |
| 共享数据边界 | 未连接/未写入共享 Redis/MinIO；未改动 `itsm`、baseline、intake 等现有库 |

## 2. 执行结果

### 准入（037 fail-closed）

- `backend` bootstrap 在 037 边界失败：
  `runtime migration admission: runtime requires migration 037_work_item_structure_preparation`。
- 证明运行时在 P 之前拒绝启动；日志 SHA256 `9cd808cc...`。

### P(037)

- 只读 inventory：`InventoryDigest=1232decc...`、`LedgerDigest=ee6bf608...`。
- 真实 `pg_dump` + 独立 restore 到临时库，`schema_migrations` ledger 一致；
  `BackupDigest=9f84b120...`、`RestoreReportDigest=d90d39b0...`。
- `migrate -prepare-workitem -evidence-file` 成功：**Controlled migration committed**。
- P 回执：`work_item_migration_evidence` 037，content 长度 1510，digest `da206e84...`。

### 普通迁移

- `migrate -up` 成功：032–036 已应用，结果 `No pending migrations`；
  `038_work_item_controlled_retirement` 保持 `pending_manual`。
- ledger 核验：`037` 存在，`038` 不存在。

## 3. 安全与权限

- bootstrap 需要非 trusted 的 `vector` 扩展，临时给专属 owner 授予 `SUPERUSER`；
  bootstrap 完成后**立即回收**，最终 owner 属性 `rolsuper=false`。
- 专属 `_app` 仅获得四张准备表的 SELECT/INSERT/UPDATE/DELETE（ReviewedGrants 精确匹配）；
  `_system` 仅获得运行所需只读/配额；`_inspect` 无业务访问、无写权限。
- 所有专属角色与 DB 已在验证后删除；残留查询为空。
- 未运行 focused real-PG：其 fixture 硬校验 `127.0.0.1:36542/workitem_v2_task2_test`
  与容器 `codex-workitem-v2-task2-fix1-pg`，无法指向共享容器专属 DB。

## 4. 清理核验

- `workitem_target_%` database：无；`%_restore_p`：无；`workitem_target_%` role：无。
- 运行目录（含临时凭据/dump）已删除。
- 现有 `itsm`/baseline/intake 库列表不变；`itsm-postgres-dev`/`itsm-redis-dev`/`itsm-minio-dev` 保持 healthy。

## 5. 限制

- 这不是真实生产目标部署；只是共享 dev 容器内的专属 DB 受控准备执行。
- 未执行 R(038)、恢复演练、业务 V1、观察期、外部投递与补偿。
- 临时 superuser 使用已记录；在真实目标环境应改为受控扩展预置或独立超级用户流程。
- 证据摘要见 `shared-target-cli-preparation-summary.json`、`shared-target-inventory.json`、
  `shared-target-preparation-evidence.json` 与三份日志。
