# 克隆来源与结构一致性证据（`itsm_migration_20260914`）

- 日期：2026-09-18
- 范围：把迁移目标库 `itsm_migration_20260914` 重建为**当前 Dev 的可证来源克隆**，作为组织/人员/路由迁移的工作目标库。
- 执行入口：`scripts/clone_itsm_migration_db.sh`（仓库内既有工具，未修改）
- 授权：维护者于 2026-09-18 授权执行，并要求先确认备份可恢复。
- 本文件只记录**计数与标识**，不含姓名、邮箱、工号等个人信息。

## 1. 执行参数

| 项 | 值 |
| --- | --- |
| 容器 | `itsm-postgres-dev` |
| 源库 | `itsm_config_baseline_20260908`（当前 Dev） |
| 目标库 | `itsm_migration_20260914` |
| 目标库既有内容 | 未通过完整性校验（缺 `_migration_clone_manifest`） |
| 重建开关 | `RECREATE_INCOMPLETE=1` |
| 克隆方式 | 实际走了 `pg_dump` / `pg_restore` 回退（源库有 2 个活跃连接，`CREATE DATABASE ... TEMPLATE` 不可用） |
| 结果 | 退出码 0；脚本自校验通过 |

## 2. 变更前后计数

| 指标 | 变更前（019 旧副本） | 变更后（Dev 克隆） |
| --- | --- | --- |
| `schema_migrations` 收据数 | 14 | 40 |
| 结构最高版本 | `019_kaf_execution_integrity_rls` | `048_cti_governance` |
| departments | 7975 | 7975 |
| users | 7834 | 7872 |
| users（非 end_user） | 8 | 38 |
| ticket_categories | 183 | 183 |
| process_definitions | 68 | 78 |
| service_catalogs | 25 | 34 |
| tickets | 18 | 41 |

变更后补充观测（供后续任务引用，**均为本次实测**）：

- 产品种子部门（id 1–14）：**14** —— Task 2 需隔离。
- `departments.manager_id` 有值：**1**（脏值，待《部门》计划的原子前置步骤清除）。
- `users.manager_id` 有值：**7076**。
- `users.is_leader` 为真：**0**。

## 3. 克隆清单（`_migration_clone_manifest`）

```
source_db    = itsm_config_baseline_20260908
verified_at  = 2026-09-18 02:40:27.04634+00
table_counts = ticket_categories=183;ticket_templates=23;field_definitions=137;
               sla_definitions=14;service_catalogs=34;process_definitions=78;
               process_deployments=42;process_bindings=36
```

## 4. 结构一致性校验

| 库 | 收据数 | 最高版本 |
| --- | --- | --- |
| `itsm_migration_20260914`（目标） | 40 | `048_cti_governance` |
| `itsm_config_baseline_20260908`（源/Dev） | 40 | `048_cti_governance` |

**一致。** 克隆工具是整库物理克隆（`CREATE DATABASE ... TEMPLATE`，回退 `pg_dump`/`pg_restore`），`schema_migrations` 与表结构随源库一并复制，因此结构一致性是克隆忠实的**结果**，不是额外升级的结果。

因此本步骤**不涉及、也不允许**用 `cmd/migrate -up` 去"补齐结构"：该命令只应用已注册的 post-schema 迁移，不创建 Ent 基础结构。

## 5. 已知边界

- 克隆继承 Dev 的开发期数据：38 个非 `end_user` 账号、41 个工单、测试服务目录/流程定义，以及被改成测试角色的真实员工账号。清理属 Task 2（隔离，不物理删除）。
- 结构目标表在文档中仍写作 `047`，而实测 Dev 与克隆库均为 `048_cti_governance`；该漂移属环境合同更新，需单独确认，不在本次证据范围内。
- 本库**仍不含**旧 ITIL 的组织/人员主数据（`uuid` 形态部门 = 0、`users.is_leader` = 0）；那部分迁移属后续计划。

## 6. 回滚 / 恢复

待重建对象是**派生副本**，内容可由源库确定性重建：

```bash
cd /home/administrator/project/itsm
RECREATE_INCOMPLETE=1 bash scripts/clone_itsm_migration_db.sh \
  itsm_migration_20260914 itsm_config_baseline_20260908
```

变更前的 019 状态另有一份同内容副本保留在 `itsm` 库（实测与之同构同量），因此旧状态本身也可回溯。
