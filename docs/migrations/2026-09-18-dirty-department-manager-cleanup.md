# 部门脏负责人值清除证据（2026-09-18）

**依据**：《部门管理单元实施计划》Task 5；《部门管理单元》设计第 8 节"原子前置步骤"。

**执行前置**：维护者授权本次数据库写入。此前置步骤必须在任何负责人数据迁入之前完成，否则无法判断哪条可信。

## 目标与边界

| 项 | 值 |
| --- | --- |
| 写入目标 | `itsm_migration_20260914` |
| 明确**未**写入 | `itsm_config_baseline_20260908`（当前 Dev）——执行后复查仍为 1 条，未被触碰 |
| 主机/实例 | 本机 Docker 中的开发实例 |

## 执行前状态（只读确认）

```sql
SELECT d.id, d.code, d.manager_id, u.role, u.active
FROM departments d JOIN users u ON u.id = d.manager_id
ORDER BY d.id;
```

结果：**恰好 1 行**。

| 部门 ID | 部门编码 | 负责人用户 ID | 该用户角色 | 在职 |
| --- | --- | --- | --- | --- |
| 635 | 11D030304 | 331 | `end_user`（非管理岗） | 是 |

部门总数（未软删除）：**7975**；其中 `manager_id <> 0`：**1**。

> 该值指向一名**普通员工**，不是管理岗——属脏数据。该部门没有可靠的权威负责人来源，因此按计划**清除**而不是猜测填充。

## 执行内容（单事务）

```sql
BEGIN;
UPDATE departments SET manager_id = 0, updated_at = now() WHERE manager_id <> 0;
SELECT count(*) AS remaining_nonzero_manager FROM departments WHERE manager_id <> 0;
COMMIT;
```

执行输出：`BEGIN` → `UPDATE 1` → `remaining_nonzero_manager = 0` → `COMMIT`。

## 执行后校验（独立回读）

| 指标 | 执行前 | 执行后 |
| --- | --- | --- |
| `manager_id <> 0` 的部门数 | 1 | **0** |
| 部门总数（未软删除） | 7975 | 7975 |
| 部门 635 的 `manager_id` | 331 | **0** |
| Dev 库 `itsm_config_baseline_20260908` 的 `manager_id <> 0` | 1 | **1（未改动）** |

## 回滚

该值本身是脏数据，**无恢复必要**。若确需回到执行前状态，可从当前 Dev 克隆重建本库（见《克隆来源证明》），或手工恢复 `departments.id = 635` 的 `manager_id = 331`。

## 证据范围说明

本文件只记录计数、部门 ID/编码与**角色**；不含姓名、邮箱、工号。负责人用户以内部 ID 表示。

## 后续

- 负责人校验（计划 2/4 Task 2/3）已就位：此后即便有人想把负责人设成脚手架账号、非在职用户或跨租户用户，写入会被拒绝；把负责人清空（`managerId: 0`）现在是**可表达**的。
- 负责人数据的真实迁入（先补公司/分公司，可回填 164 条）不在本 Task 范围内。
