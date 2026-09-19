-- 「没有负责人」统一为 NULL。
--
-- departments.manager_id 是可空列，但历史上同时存在 NULL 与 0 两种"没有负责人"的写法
-- （截至 2026-09-19：7974 行为 NULL、1 行为 0）。两种写法会造成真实的误判风险：
-- SQL 的三值逻辑让 `manager_id <> 0` **看不见 NULL 行**，而 `manager_id IS NULL`
-- 又会漏掉 0——任何统计或校验查询只要漏掉一种写法就会给出错误结论。本项目已经因此
-- 产出过一份失真的"负责人已清理"证据（校验语句用的是 `manager_id <> 0`）。
--
-- NULL 是本列的既有惯例，也是同表 parent_id 表达"没有"的方式（表内 7974/7975 已是 NULL），
-- 因此把 0 归一为 NULL，而不是反向。
--
-- 幂等：重复执行时已无 0 可改，不会产生额外影响。
-- 仅改表示方式，不改变任何业务含义——0 与 NULL 在读取侧都表示"没有负责人"。
UPDATE departments
SET manager_id = NULL, updated_at = now()
WHERE manager_id = 0;
