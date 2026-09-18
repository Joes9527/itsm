# WorkItem 受控退役 — 目标环境执行 Runbook（待授权执行）

> 状态：**未在真实目标环境执行**。本文件是准入、权限核验、P→普通迁移→观察→R→恢复的操作手册与证据模板。
> 任何真实目标操作都需要单独书面授权、维护窗口、审批人与回滚负责人。本文不构成授权。

## 1. 角色、连接与权限边界

| 角色 | 用途 | 允许 | 禁止 |
|---|---|---|---|
| 迁移/运维连接 | P、普通迁移、R、Reconcile | 目标 schema DDL/写入、TEMP、读取业务表 | 复用业务角色、跳过门禁、执行未授权 SQL |
| inspection role | 运行时只读准入 | 仅同库/schema 的 `schema_migrations`、`work_item_migration_evidence` SELECT | 业务表读取/写入、ownership、grant option、角色继承、BYPASSRLS、TEMP |
| 业务角色 | 应用/V1 | 正常业务读写（RLS 生效） | 全局历史证据读取、迁移 |
| 备份/恢复身份 | 物理备份与独立恢复 | 集群角色/权限/序列/WAL/业务表/附件 | 复用原实例、伪造恢复点 |

必查（在目标环境执行并留证）：

```sql
-- inspection role 负向检查
SELECT has_table_privilege(current_user,'public.tickets','SELECT');   -- 期望 false
SELECT has_table_privilege(current_user,'public.tickets','INSERT');   -- 期望 false
SELECT rolbypassrls, rolsuper FROM pg_roles WHERE rolname=current_user; -- 期望 false/false
SELECT has_database_privilege(current_user,current_database(),'TEMP'); -- 期望 false（inspection）
-- 运维连接 TEMP 正向检查（事务内创建后回滚）
BEGIN; CREATE TEMP TABLE itsm_temp_probe(x int); ROLLBACK;
```

同时记录：PostgreSQL 版本/镜像、目标 host/port/db/schema、部署身份、`search_path` 必须为单一显式 schema、迁移控制文件/公钥授权来源。

## 2. 阶段 0：准入、授权与写入者盘点（未完成）

- [ ] 书面授权：批准人、范围、维护窗口、回滚负责人、允许的 P/R 版本（037/038）。
- [ ] 目标身份核对：数据库/schema/服务器、部署 ID、迁移控制文件、R 公钥指纹。
- [ ] 写入者/消费者盘点：应用实例、后台 worker、KAF/outbox 消费者、通知 worker、附件/MinIO 写入、外部连接器、定时任务、人工 DB 访问。
- [ ] 外部投递盘点：邮件/ITSM/Graph/HRIS 等真实出站；记录可观测回执与取消窗口。
- [ ] 备份能力：同版 PostgreSQL 镜像、WAL/角色/序列/附件/配置的完整物理备份与独立恢复目标。
- [ ] 失败模式：明确 P 后应用回滚兼容性、R 失败回滚路径、数据/对象/外部效果补偿负责人。
- [ ] 记录基线：业务行/关系/SLA/流程/审计/回执/附件摘要；旧结构保留证明。

## 3. 阶段 1：结构准备 P（037，需独立授权）

- [ ] P 前完成与本次变更范围匹配的备份、摘要和独立恢复验证；记录数据库、角色、附件、配置覆盖范围，不能把逻辑库恢复称为完整物理恢复。
- [ ] `migrate -prepare-workitem -dry-run` 产出清单并与已批准清单逐项比对。
- [ ] `migrate -prepare-workitem -evidence-file /reviewed/preparation.json` 执行 P：校验可信配置的目标、实际 OS 身份、库存、制品和真实备份／恢复摘要；核对新回执、保留对象和历史数据。P 不使用 R 的 Ed25519 签名契约；不能省略证据文件另执行一次 P。
- [ ] 运行 `ReconcileSchemaInvariants`；确认访问 invariants 已存在且 identity 未变。
- [ ] 本阶段仅核验 P 所需结构、受限角色/RLS 与真实回执；完整三域 + Requested Item V1 在下一阶段普通迁移完成后执行。

## 4. 阶段 2：普通迁移与观察（P 之后，需独立授权）

- [ ] 按所选源码的唯一迁移目录列出准确普通迁移计划；2026-09-15 主干目录到 047，031 历史目标在 P 后需要 032–036、039–047。运行 `migrate -up` 不自动执行 P/R，038 保持 pending_manual；以后目录变化须重新核对，不能把 036 或最高编号当完成证明。
- [ ] 独立核对 runtime/system/inspection 角色、执行域 SELECT-only 与 deployment/standard 绑定；建表完成不等于运行配置完成。开发恢复沿用[开发环境合同](../development-environment.md#development-and-migration-validation-database-contract)，无需提前执行 R。
- [ ] 应用启动仅走 canonical 流，不使用 Ent overlay；确认只读准入通过。
- [ ] 运行完整业务 V1；观察期正常写入（记录/关系/SLA/流程/审计/附件）。
- [ ] 记录观察期开始/结束时间、业务验收人、异常与处置。
- [ ] 明确 I2 backlog 影响：若 P 前存在活动流程/任务/回调，观察期变化可能导致 R 严格拒绝；不得据此自动取消流程或清洗数据。

## 5. 阶段 3：最终恢复点与独立恢复演练

- [ ] 停止所有写入者、消费者、附件写入与外部投递；记录停止顺序与确认证据。
- [ ] 生成最终完整物理备份（PG 集群 + WAL + 角色/权限/序列 + 业务表 + 不可变回执 + 附件对象 + 应用/消费者配置）。
- [ ] 在独立目标执行同版物理恢复，核对记录/内容摘要/时间/编号/租户/关系/流程/回执/审计/附件。
- [ ] 记录最终备份后的新增业务/Redis/对象/外部动作；制定补偿/重放清单，不宣称总体零损失。
- [ ] 复核 R 准入证据：最终恢复点、签名、清单、inventory 与不变量。

## 6. 阶段 4：受控退役 R（038，需单独授权）

- [ ] 停止旧应用/消费者；确认新路径 sole-path 与业务验收。
- [ ] 执行带签名的 R：精确清单、RESTRICT、原子回执；禁止 CASCADE/通配符/递归。
- [ ] 核对 R 回执、旧对象已删、业务行/附件/审计保留、精确清理 inventory。
- [ ] R 后 `ReconcileSchemaInvariants` 与 `InspectRuntimeMigrations` 必须通过且不产生 drift。
- [ ] 启动新应用；运行 R 后业务 V1 与观察。
- [ ] 若失败：停止写入、保留真实证据、按授权回滚/恢复；不得自动取消旧流程或改写账本。

## 7. 失败/回滚决策树

1. 准入失败（回执缺失、未知版本、checksum 变化、结构矛盾）→ 不写；提交维护者裁决。
2. P/普通迁移失败 → 事务回滚/恢复备份；保留日志与真实回执。
3. 观察期 drift 或 I2 类流程变化 → 保存证据；不得刷新备份/签名来掩盖；由维护者决定继续观察或回滚。
4. R 前恢复演练失败 → 不执行 R；修复恢复能力后重做。
5. R 执行失败 → 按 R 事务边界与回执判断；必要时授权恢复；外部效果单独补偿。
6. 任何未授权/不确定 → fail-closed 停止并上报。

## 8. 证据模板（每次变更独立命名域）

- `admission-<env>-<date>.json`：授权、身份、schema、窗口、审批人。
- `inventory-writers-<env>-<date>.json`：写入者/消费者/外部投递/附件。
- `p-preflight-<env>-<date>.json`、`p-receipt-<env>-<date>.json`。
- `observation-<env>-<date>.json`：业务 V1、异常、I2 影响。
- `final-backup-<env>-<date>.json`、`restore-rehearsal-<env>-<date>.json`。
- `post-point-compensation-<env>-<date>.json`：后点新增/外部效果/补偿或重放状态。
- `r-receipt-<env>-<date>.json`、`post-r-v1-<env>-<date>.json`。
- 所有摘要记录 SHA256；不发布含凭据/业务敏感原文。

## 9. 明确禁止

- 未授权 P/R、部署、共享环境迁移、真实外部投递压测。
- 修改历史 SQL/checksum、伪造回执、把未执行旧迁移写成 applied。
- CASCADE/通配符/猜测归属清理、把旧流程静默取消或清洗。
- 用本 runbook 或隔离 V1 结果宣称目标环境已完成退役。


## 10. 隔离执行证据（非真实目标环境）

### 2026-09-12 当前 HEAD 重跑（`workitem-v1-e27375881582`）

- Source commit `a0c5b81a`；base port 19930；retained/retired/restored 各 9/9，共 **27/27**，0 skip/flaky。
- 覆盖 observation、R(038)（6 个对象）、恢复演练与业务 V1；negativeCases 28。
- 清理：9/9 自有容器与 9/9 自有卷精确核验不存在；19930–19940 free。
- 证据：W 下 `runbook-isolated-execution-report-finalhead.md`、`runbook-isolated-execution-summary-finalhead.json`。
- 仍是隔离环境，不是真实生产部署/退役。
- 共享目标：在 `itsm-postgres-dev` 专属 DB 上已完成真实 CLI 准入（037 fail-closed）、P(037) 与普通迁移；ledger 037 存在、038 `pending_manual`；执行后专属 DB/角色已删除，现有库与共享 Redis/MinIO 未被改动；完整 observation/R/恢复未执行。


2026-09-12 在 WSL 新建一次性隔离环境，按本 runbook 顺序执行了 P(037) → 普通迁移 →
观察期写入 → 最终恢复点/独立恢复演练 → R(038) → R 后验证，并完成精确清理：

- Run：`workitem-v1-8d7e97c803f2`；source commit `b861db2f...`；端口 19910–19920；未连接 shared/default 服务。
- V1：retained 9/9、retired 9/9、restored 9/9，合计 27/27，0 skip/flaky。
- negativeCases 28 项；`zeroLossAtDeclaredFinalPreRPoint=true`、`zeroLossIncludingLaterWrites=false`。
- 清理：9/9 自有容器与 9/9 自有卷按精确 ID 不存在；端口全部 free。
- 证据：W 下 `runbook-isolated-execution-report.md`、`runbook-isolated-execution-summary.json`
  （含 evidence SHA256 与阶段映射）。
- **边界：** 这不是真实目标环境部署、观察或退役；后点补偿/外部投递未执行；
  I2 `BL-WI-PROCESS-AUDIT-CONTINUITY` 仍是 backlog；真实目标仍需单独授权与
  TEMP/inspection 权限核验。
