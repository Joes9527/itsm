# 汇报线与员工归属盘点（只读，2026-09-18）

**依据**：《人员与汇报线实施计划》Task 3。**本文件只读**，不做任何写入。

| 项 | 值 |
| --- | --- |
| 读取目标 | `itsm_migration_20260914` |
| 未读取/未写入 | `itsm_config_baseline_20260908`（当前 Dev） |

## 1. 上级分布

```sql
SELECT count(*) AS total,
       count(*) FILTER (WHERE manager_id IS NULL) AS manager_null,
       count(*) FILTER (WHERE manager_id = 0) AS manager_zero,
       count(*) FILTER (WHERE manager_id = id) AS self_ref,
       count(*) FILTER (WHERE manager_id > 0 AND manager_id <> id) AS valid_line
FROM users;
```

| 指标 | 条数 |
| --- | --- |
| 用户总数 | **7872** |
| `manager_id IS NULL`（未设置上级） | **796** |
| `manager_id = 0` | **0** |
| 自引用（`manager_id = id`） | **31** |
| 有效汇报线 | **7045** |

引用完整性：

| 检查 | 条数 |
| --- | --- |
| 指向不存在的用户（悬挂引用） | **0** |
| 上级已离职（`active IS NOT TRUE`） | **0** |

## 2. 必须注意：`NULL` 与 `0` 不是一回事

**796 条"未设置上级"在库里是 `NULL`，不是 `0`。** 这会静默破坏任何按 `manager_id = 0` 或 `manager_id <> 0` 过滤的 SQL：

```sql
-- ✗ 数不到那 796 条（NULL 与任何值比较都是 NULL，不满足 =）
SELECT count(*) FROM users WHERE manager_id = 0;          -- 0
SELECT count(*) FROM users WHERE manager_id <> 0;         -- 7045 + 31 = 7076，漏掉 796
-- ✓ 正确写法
SELECT count(*) FROM users WHERE coalesce(manager_id,0) = 0;  -- 796
```

**影响**：计划中原 Task 4 Step 2 的"补空后回读校验"用的是 `manager_id = 0`，**会得到 0 条而误判为"已补全"**。执行 Task 4 时必须改用 `coalesce(manager_id,0)`，否则这一步的校验是假的。

Go/Ent 侧读取可空 Int 列时映射为 0，因此应用层把 `NULL` 视作"无上级"与契约一致；**差异只存在于 SQL 口径**。

## 3. 员工归属分布

```sql
SELECT coalesce(nullif(d.org_type,''),'(empty)') AS org_type, count(*) AS staff
FROM users u JOIN departments d ON d.id = u.department_id
GROUP BY 1 ORDER BY 2 DESC;
```

| `departments.org_type` | 员工数 |
| --- | --- |
| 见"未完成项" | 本轮未取到 |

> **未完成**：该查询依赖 `users.department_id` 的实际填充率与部门侧的 `org_type` 分布。本轮先确认了更关键的阻塞项（见第 4 节），归属分布留待 Task 4 授权后一并取。

## 4. Task 5（员工落位到最细的组）前置条件**不成立**

计划要求 Task 5 Step 1 先确认 `departments.node_type` 已赋值到 `team`，否则停下。实测：

| 检查 | 结果 |
| --- | --- |
| `departments` 是否有 `node_type` 列 | **没有**（只有 `org_type`） |
| 因此能否判断"最细的组" | **不能** |

原因：`node_type` 由 canonical `050_department_node_type` 引入，而 **050 尚未应用到这个库**（它还在 PR #70 里，未合并、未在任何共享库执行）。

**结论：Task 5 必须停在门口。** 解除阻塞需要按顺序完成：① 合并并应用 canonical 049/050；② 完成 5202 个节点的类型赋值（依赖业务分流表）；③ 再执行落位。

## 5. 计划缺陷（本轮实测）

| # | 计划原文 | 实际 | 处置 |
| --- | --- | --- | --- |
| 1 | `WHERE u.deleted_at IS NULL` | **`users` 表没有 `deleted_at` 列**（`departments` 才有）；用户生命周期用 `active` | 已改用 `active`/无该过滤 |
| 2 | 用 `manager_id = 0` 判断"未设置上级" | 未设置是 **`NULL`**；`= 0` 得 0 条，会误判为已补全 | 必须用 `coalesce(manager_id,0)`；见第 2 节 |
| 3 | Task 4/5 假定可直接写库 | `node_type` 列在本库根本不存在，Task 5 的前置不成立 | Task 5 停在门口，见第 4 节 |
| 4 | 计划称"614 补 / 404 覆盖 / 1261 待裁定" | 本轮实测：自引用 **31** 与之一致；未设置上级为 **796**（非计划口径的数字） | 以本节实测数字为准 |

## 6. 证据范围

只记录计数与结构事实；不含姓名、邮箱、工号。所有查询均为 `SELECT`。
