# 2026-09-17 dev 环境部署与配置变更记录（8080 / 3010）

> 状态：**已执行**（记录实际发生的部署、配置与数据变更；不含后续计划）。
> 归属：共享 WSL dev 环境。启动权威：`/home/administrator/apps/itsm-kaf/stack`。
> 相关决策记录：[ADR-003](../architecture/adr-003-bpmn-process-definition-deletion.md)。

## 1. 目标环境

| 项 | 值 |
|---|---|
| 主机 / 部署 ID | `JULIAN` (WSL2) / `itsm-dev-20260916` |
| 服务与端口 | `itsm` 8080、`itsm-web` 3010（KAF / workers 本次未启动，保持既有 stopped） |
| 私有状态目录 | `/home/administrator/.local/state/itsm-kaf-baseline-20260908`（recipe、日志、备份） |
| 数据库 | `itsm_config_baseline_20260908`（容器 `itsm-postgres-dev`，PostgreSQL 17） |
| 变更性质 | **共享环境变更**：制品替换 + 服务重启 + 业务配置写入（无 schema 迁移） |

## 2. 本次部署的制品

| 服务 | 制品 | revision | sha256 | 备注 |
|---|---|---|---|---|
| itsm (8080) | `/home/administrator/.local/state/itsm-dev-restoration-20260916/itsm-api-d226c1ce` | `d226c1ce` | `2e931f8d64cb52a177371cdf832ffd6a97f4af5a98a2d4c0f2ae4a28cc3a1f2d` | `go build -trimpath`（go1.25.14），模块根 `main.go`；后端内容 == `main@4de73348` 的 `itsm-backend`（`git diff` 为空） |
| itsm-web (3010) | `/home/administrator/apps/itsm-kaf/releases/itsm-web-5cf884ac-gG8AL4Vu7GgTxL7qAKG9w` | `5cf884ac`（本地构建树 = `6632ae0b` + 前端改动） | `server.js` = `83cf5ea13d3d0cfb2dc194fa22b2b4c9bf6e969fb4e0aea17c9405332c880d7a`，`build_id` = `gG8AL4Vu7GgTxL7qAKG9w` | 前端内容 == `main@4de73348` 的 `itsm-frontend`（`git diff` 为空） |

> `5cf884ac` 是**本地构建集成提交**（因为当时运行基线 `6632ae0b` 尚未进入 `origin/main`）。其前端 diff 与已推送的 `d226c1ce` 前端 diff 逐行一致；该本地分支已在清理阶段删除。

## 3. 运行配置（recipe）变更

| recipe | 变更 |
|---|---|
| `config/itsm-launch.json` | `argv[0]` → `itsm-api-d226c1ce`；`executable_sha256` / `artifact_path` / `artifact_sha256` 同步；`source_revision` → 当前锚点；**移除 `source_root`**（原因：锚定一个持续推进的 main 检出会反复产生 `source_revision drift`，导致 `stack start` 拒绝启动；保留 `source_revision` + 产物 sha 作为构建来源记录） |
| `config/itsm-web-launch.json` | `cwd` → 新 release 目录；`argv` → 新 `server.js`；`artifact_sha256` / `build_id` / `source_revision` 同步；**移除 `source_root`** |

期间经历两次锚点调整（`source_root` 指向本次 worktree → 指向主仓库 `4de73348` → 最终移除），每次改动后都重启服务使 recipe 指纹生效。

## 4. 数据库 / 业务配置写入

| 类型 | 对象 | 结果 |
|---|---|---|
| 新建流程定义 | `generic_sr_approval_flow`「通用服务请求审批流」v1.0.0 | active + latest，审批节点 `assigneeRole=dept_manager`（2 个 active 用户） |
| 新建流程绑定 | id `832`：`business_type=service_request_item` → `generic_sr_approval_flow` v1 | active + default（补齐此前缺失的该类绑定） |
| 新建服务目录 | id `41`「服务请求-通用审批目录」 | `target_class=service_request_item`，enabled（创建成功，即"发布校验"通过的证据） |
| 删除流程定义 | `workflow_1789626406862`「空白流程」（id 144，0 实例、1 条变更日志） | 删除成功；定义与变更日志均归零（**真实数据验证修复**） |
| 临时验证数据 | `verify_del_*` 流程（含一次激活产生变更日志） | 验证后已删除，未留残留 |
| 迁移 | `048_cti_governance` | **非本次执行**：由 CTI 作者当日更早应用（台账 `applied_at=2026-09-17 15:52:48`，execution_ms 28，checksum `d3b309f6…`）；本次仅只读核对，**未执行任何迁移** |

## 5. 时间线（关键事件，时区随来源标注）

| 时间 | 事件 |
|---|---|
| 2026-09-17 15:52:48（库内值，无时区） | CTI 作者对 `itsm_config_baseline_20260908` 应用迁移 `048_cti_governance`（预检通过；其记录称执行前有全量备份 `/tmp/cti-baseline-before-048.sql.gz`） |
| 2026-09-17 16:30 / 16:39（CST，制品 mtime） | 后端二进制 `itsm-api-d226c1ce`、前端 release `itsm-web-5cf884ac-…` 生成 |
| 2026-09-17 09:00:34Z | PR #49（设计器可用性修复）合并 → `4c003369` |
| 2026-09-17 09:40:21Z | PR #50（本次两个缺陷修复）合并 → `4de73348`；`origin/main` 到达该点 |
| 2026-09-17 09:40Z 之后 | 两个服务重启（含 recipe 锚点调整），`stack status` 无 drift |
| 2026-09-17 13:50:32Z | PR #48（CTI 治理）合并 → `37471635`；`origin/main` = `37471635` |
| 记录时 | 环境仍运行 §2 制品（`source_revision=4de73348`），**落后 main 一个 CTI 版本** |

## 6. 验证证据（本次实际执行）

| 验证 | 命令 / 方式 | 结果 |
|---|---|---|
| 服务健康 | `stack status`；`curl /api/v1/health`、`/login` | `itsm` PID 4091855、`itsm-web` PID 4094119；8080 health 200、3010 login 200；status **无 drift** |
| 启动可用性 | `stack start itsm` / `stack start itsm-web`（运行中校验路径） | 均 exit 0 |
| 删除修复（外键） | 临时流程 + 激活版本（产生变更日志）→ DELETE | HTTP 200；定义与变更日志行同时归零 |
| 删除语义（实例） | DELETE 有 4 个 `completed` 实例的定义 | HTTP 409「该流程定义存在 4 个历史实例，为保留流程执行历史不能删除；可停用该流程定义」；定义未变更 |
| 发布失败可诊断 | POST `/api/v1/service-catalogs`（`service_request_item`，无绑定） | HTTP 400 + `fieldErrors[0].message = "publication requires a declared process or no_process binding"` |
| 发布成功 | 补绑定后再次 POST | HTTP 200，目录 id 41 |
| 前端真实路径 | Playwright：登录 → `/admin/workflows` → 删除 → 确认 → toast | toast 显示后端真实原因（「…历史实例…」），确认框为修正后文案 |
| 迁移一致性 | `sha256(GetMigrationSQL("048_cti_governance"))` vs 台账 checksum | 完全相同（`d3b309f6…`） |

## 7. 回滚边界与已知缺口

**回滚边界（重要）**

- 本次部署的 recipe 备份（`*.before-d226c1ce.json` 等）与前一版前端 release 目录 `itsm-web-6632ae0b-…` 已按维护者要求**删除**，因此**无法就地回滚**到修复前版本；回滚需从目标 revision 重新构建部署。
- 旧的共享 dev 前端 release `itsm-web-0bfad4bd-…`、既有历史备份仍在，但与本环境当前制品无对应关系。
- 迁移 `048` **不属于本次变更**，且"旧代码 + 新结构"经实测兼容（当前即此状态），故代码回滚不需要回滚迁移。

**已知缺口 / 既有问题（非本次引入）**

| 项 | 状态 |
|---|---|
| `8080/api/v1/readyz` = 503 | 既有；`schema_migrations` 读权限问题（`active-release.json` 已记录） |
| `cmd/migrate -status` 失败 | `permission denied for table work_item_migration_evidence (42501)`；应用身份缺少证据表读权限 → 受控迁移准入的正规路径当前不可用，台账只能 SQL 直读 |
| KAF / `itsm-worker-1/2` | 保持 stopped（既有） |
| 目录 id 41 缺默认分类 | `default_ticket_category_id IS NULL`；CTI 代码上线后该目录会显示"未配置默认分类" |
| `main@37471635` 两个守卫为红 | `Lint`（`Run 'gofumpt -w .' to fix formatting`）、`Source/Test Coverage Guard`；PR #48 在红灯状态下被合并 |
| approvals Jest 偶发超时 | `src/app/(main)/approvals/__tests__/page.test.tsx` 在 CI 负载下触发 10s 默认超时；本地 20/20 通过 |

## 8. 后续（未在本次执行）

1. **把环境推进到 `main@37471635`（含 CTI 治理）**：需要重新构建后端与前端；`048` 已应用，**不需要迁移**，但部署后"分类结构治理"会立即生效（完成门禁默认仍关闭）。执行前建议先全量备份（库仅 28 MB）。
2. 修 `main@37471635` 上仍红的两个守卫（gofumpt 格式、测试覆盖映射）。
3. 按 [CTI 治理受控启用清单](../operations/cti-governance-rollout-checklist.md) 分阶段：补配目录默认分类 → 再逐租户启用完成门禁。
4. 更新 `docs/superpowers/plans/2026-09-15-migration-validation-ledger.md` 的 Dev 行（仍写"031 / 目标 047"，实际已到 048）与 CTI 清单 §9 的自相矛盾（§9 称"未在任何共享库应用"，§4 已记录 dev 应用成功）。

## 9. 证据位置

| 证据 | 位置 |
|---|---|
| 服务状态与 recipe | `$STATE/config/itsm-launch.json`、`$STATE/config/itsm-web-launch.json`、`$STATE/logs/itsm*.log` |
| 迁移台账 | `schema_migrations`（含 `048_cti_governance` 的 checksum 与 rollback_sql） |
| 业务验证数据 | `process_definitions` id 150、`process_bindings` id 832、`service_catalogs` id 41 |
| 浏览器证据 | `/tmp/ui-2-delete-error.png`（一次性，未入库） |
| 本次备份 | `$STATE/backup/`（后续重部署前的新备份应放这里，**不要放 `/tmp`**） |
