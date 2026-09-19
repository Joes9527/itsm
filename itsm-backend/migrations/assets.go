// Package migrations embeds operational SQL assets for the canonical runtime registry.
package migrations

import _ "embed"

// WorkItemSLACycleSQL is also runnable as the reviewed standalone migration.
//
//go:embed 20260910_workitem_sla_cycle.sql
var WorkItemSLACycleSQL string

//go:embed 20260910_problem_investigation_completion.sql
var ProblemInvestigationCompletionSQL string

//go:embed 20260911_change_professional_evidence.sql
var ChangeProfessionalEvidenceSQL string

// DepartmentCodeTenantUniqueSQL 是同时可独立执行的受审迁移脚本。
//
//go:embed 049_department_code_tenant_unique.sql
var DepartmentCodeTenantUniqueSQL string

// DepartmentNodeTypeSQL 让每个组织节点自带类型（公司/分公司/部门/组）。
//
//go:embed 050_department_node_type.sql
var DepartmentNodeTypeSQL string

// DepartmentManagerNoneSQL 把「没有负责人」统一为 NULL。
//
//go:embed 051_department_manager_none_normalization.sql
var DepartmentManagerNoneSQL string
