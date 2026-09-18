package migration

// DepartmentCodeTenantUniqueVersion 让部门编码在租户内唯一。
//
// SQL 资产由 migrations 包嵌入（migrations.DepartmentCodeTenantUniqueSQL），
// 注册表通过 MigrationSQL 取用，避免同一段 SQL 在两处各写一遍。
const DepartmentCodeTenantUniqueVersion = "049_department_code_tenant_unique"
