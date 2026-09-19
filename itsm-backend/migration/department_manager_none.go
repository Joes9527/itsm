package migration

// DepartmentManagerNoneVersion 把「没有负责人」统一为 NULL。
//
// departments.manager_id 是可空列，历史上同时存在 NULL 与 0 两种写法，容易让统计与
// 校验查询漏掉一半（详见迁移 SQL 内的说明）。
const DepartmentManagerNoneVersion = "051_department_manager_none_normalization"
