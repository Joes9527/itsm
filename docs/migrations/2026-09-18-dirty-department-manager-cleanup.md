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

结果：**恰好 1 行**（`JOIN` 本身只匹配非空且存在的用户，所以它天然看不见 NULL 行）。

| 部门 ID | 部门编码 | 负责人用户 ID | 该用户角色 | 在职 |
| --- | --- | --- | --- | --- |
| 635 | 11D030304 | 331 | `end_user`（非管理岗） | 是 |

部门总数（未软删除）：**7975**；其中 `manager_id > 0`（有负责人）：**1**。

## 「无负责人」在库里有两种表示（本次查明）

`departments.manager_id` 是**可空**列，因此"没有负责人"在数据里同时存在两种写法：

| 表示 | 条数（执行前） | 说明 |
| --- | --- | --- |
| `NULL` | 7974 | 表里的**多数**写法，也是可空列的自然语义 |
| `0` | 0 → 执行后 1 | 本次清除把脏值写成了 `0` |

这带来一个容易踩的坑：**`manager_id <> 0` 看不见 NULL 行**（SQL 三值逻辑），所以下面这种"用 `<> 0` 数负责人"的查询会漏掉全部 NULL，从而得出错误结论——"只有 1 个部门没有负责人"或反过来"全部都有负责人"。

凡是统计负责人状态的查询都必须同时处理两种写法：

```sql
-- 有负责人
WHERE manager_id IS NOT NULL AND manager_id <> 0
-- 无负责人（两种写法都算）
WHERE manager_id IS NULL OR manager_id = 0
```

代码侧读取用 `manager_id > 0` 判断是安全的：Ent 对可空列读出的 `0` 与 `NULL` 都不会满足 `> 0`，两条写法自然都落进"无负责人"。**风险只在不做 `> 0` 判断、直接比较的查询与分析脚本里。**

> 待办（需单独授权写入）：把这 1 行的 `0` 归一为 `NULL`，与表内 7974 行的多数写法一致。
> 归一是写入动作，会改变本文件已记录的执行后状态，因此不在本次只读修正范围内。

> 该值指向一名**普通员工**，不是管理岗——属脏数据。该部门没有可靠的权威负责人来源，因此按计划**清除**而不是猜测填充。

## 执行内容（单事务）

```sql
BEGIN;
UPDATE departments SET manager_id = 0, updated_at = now()
WHERE manager_id IS NOT NULL AND manager_id <> 0;   -- 必须显式排除 NULL，否则 NULL 行不可见
SELECT count(*) AS remaining_nonzero_manager FROM departments
WHERE manager_id IS NOT NULL AND manager_id <> 0;
COMMIT;
```

执行输出：`BEGIN` → `UPDATE 1` → `remaining_nonzero_manager = 0` → `COMMIT`。

> 写入值为 `0` 而不是 `NULL`，与表内 7974 行的写法不一致（见上一节）。这是本次执行的既有事实，未在此处改写；归一待单独授权。

## 执行后校验（独立回读）

| 指标 | 执行前 | 执行后 |
| --- | --- | --- |
| `manager_id IS NOT NULL AND manager_id <> 0` 的部门数 | 1 | **0** |
| `manager_id IS NULL OR manager_id = 0`（无负责人） | 7974 | **7975** |
| 部门总数（未软删除） | 7975 | 7975 |
| 部门 635 的 `manager_id` | 331 | **0** |
| Dev 库 `itsm_config_baseline_20260908` 的 `manager_id > 0` | 1 | **1（未改动）** |

## 2026-09-19 追加：开发库的同项清除与「无负责人」归一

上一节只覆盖了克隆库。开发库 `itsm_config_baseline_20260908` 的同一处脏值（部门 635 → 331）
当时**未**清除，2026-09-19 一并处理，并把「没有负责人」的两种写法收敛为一种。

| 项 | 值 |
| --- | --- |
| 目标库 | `itsm_config_baseline_20260908`（准备回执绑定的库） |
| 写前备份 | `/home/administrator/.itsm/backups/pre_051_itsm_config_baseline_20260908.sql`（6.0M，`sha256:0930fb4b4fbcebdf…`） |
| 归一迁移 | `051_department_manager_none_normalization`（把 `manager_id = 0` 归一为 `NULL`，走 `cmd/migrate -up`，2026-09-19 10:30:51） |
| 脏值清除 | 带守卫的单事务：`UPDATE departments SET manager_id = NULL WHERE id = 635 AND manager_id = 331` |
| 写后状态 | 部门 635 的 `manager_id` = `NULL`；全表 `NULL = 7975`、`零 = 0`、`有负责人 = 0` |

守卫的意义：只在它**仍指向那个已知脏值**时才清除；若期间有人合法地改了该部门的负责人，本操作不会覆盖。

**归一的理由与方向**：`NULL` 是本列的既有惯例（同表 `parent_id` 亦用 `NULL` 表示"没有"），
而且两种写法会让查询两边都漏——`manager_id <> 0` 看不见 `NULL`，`manager_id IS NULL` 看不见 `0`
（本文件早先那版校验语句正是踩了前者）。详见迁移 SQL 内的说明与
[规范化迁移准入与克隆库约束](../deployment/canonical-migration-admission-and-clone-constraints.md)。

**克隆库未处理**：`itsm_migration_20260914` 仍是 `零 = 1`。它不在准备回执绑定的库之列，
走不了规范化路径；按规定不得改写回执，故保持原样并在此记录。

## 回滚

该值本身是脏数据，**无恢复必要**。若确需回到执行前状态，可从当前 Dev 克隆重建本库（见《克隆来源证明》），或手工恢复 `departments.id = 635` 的 `manager_id = 331`。

## 证据范围说明

本文件只记录计数、部门 ID/编码与**角色**；不含姓名、邮箱、工号。负责人用户以内部 ID 表示。

## 后续

- 负责人校验（计划 2/4 Task 2/3）已就位：此后即便有人想把负责人设成脚手架账号、非在职用户或跨租户用户，写入会被拒绝；把负责人清空（`managerId: 0`）现在是**可表达**的。
- 负责人数据的真实迁入（先补公司/分公司，可回填 164 条）不在本 Task 范围内。
