package migration

import (
	"context"
	"fmt"
	"strings"

	"entgo.io/ent/schema/field"
	entmigrate "itsm-backend/ent/migrate"
)

// inspectCurrentRequiredStructure reads catalog metadata, never business rows.
// It establishes required current table/column/type availability, not identical
// indexes, defaults, storage options or global data quality. Extra retained
// legacy columns are intentionally allowed until controlled retirement.
func inspectCurrentRequiredStructure(ctx context.Context, q migrationQuery) error {
	rows, err := q.QueryContext(ctx, `SELECT c.relname,a.attname,t.typname
 FROM pg_class c JOIN pg_namespace n ON n.oid=c.relnamespace
 JOIN pg_attribute a ON a.attrelid=c.oid AND a.attnum>0 AND NOT a.attisdropped
 JOIN pg_type t ON t.oid=a.atttypid
 WHERE n.nspname=current_schema() AND c.relkind IN ('r','p')`)
	if err != nil {
		return err
	}
	defer rows.Close()
	actual := map[string]map[string]string{}
	for rows.Next() {
		var table, column, kind string
		if err := rows.Scan(&table, &column, &kind); err != nil {
			return err
		}
		if actual[table] == nil {
			actual[table] = map[string]string{}
		}
		actual[table][column] = kind
	}
	if err := rows.Err(); err != nil {
		return err
	}
	check := func(table, column string, allowed ...string) error {
		kind := actual[table][column]
		for _, want := range allowed {
			if kind != "" && kind == want {
				return nil
			}
		}
		return fmt.Errorf("runtime required structure missing or incompatible: %s.%s", table, column)
	}
	for _, table := range entmigrate.Tables {
		for _, column := range table.Columns {
			var types []string
			switch column.Type {
			case field.TypeBool:
				types = []string{"bool"}
			case field.TypeInt, field.TypeInt64:
				types = []string{"int8", "int4"}
			case field.TypeFloat64:
				types = []string{"float8"}
			case field.TypeString, field.TypeEnum:
				types = []string{"varchar", "text"}
			case field.TypeJSON:
				types = []string{"jsonb"}
			case field.TypeTime:
				types = []string{"timestamptz"}
			default:
				return fmt.Errorf("unsupported required Ent column type: %s.%s", table.Name, column.Name)
			}
			if custom, ok := column.SchemaType["postgres"]; ok {
				if custom != "timestamp with time zone" {
					return fmt.Errorf("unsupported required Ent PostgreSQL type: %s.%s", table.Name, column.Name)
				}
				types = []string{"timestamptz"}
			}
			if err := check(table.Name, column.Name, types...); err != nil {
				return err
			}
		}
	}
	for _, table := range requiredOrdinaryStructures {
		for _, group := range table.columns {
			parts := strings.SplitN(group, ":", 2)
			for _, column := range strings.Fields(parts[1]) {
				if err := check(table.name, column, strings.Split(parts[0], "|")...); err != nil {
					return err
				}
			}
		}
	}
	return nil
}

// These compact postconditions cover only non-Ent tables owned by active
// ordinary migrations 007/008/034. Defining SQL remains immutable and owns
// constraints/defaults/indexes; this registry adds read-only admission checks.
var requiredOrdinaryStructures = []struct {
	name    string
	columns []string
}{
	// 007_add_change_execution_tables
	{name: "change_approvals", columns: []string{"int8:id change_id tenant_id approver_id", "text:status comment", "timestamptz:approved_at created_at updated_at"}},
	{name: "change_approval_chains", columns: []string{"int8:id change_id tenant_id approver_id", "int4:level", "text:role status", "bool:is_required", "timestamptz:created_at"}},
	{name: "change_risk_assessments", columns: []string{"int8:id change_id tenant_id", "text:risk_level risk_description impact_analysis mitigation_measures contingency_plan risk_owner", "timestamptz:risk_review_date created_at updated_at"}},
	{name: "change_rollback_plans", columns: []string{"int8:id change_id tenant_id", "jsonb:trigger_conditions rollback_steps", "text:responsible communication_plan test_plan", "int4:estimated_time", "bool:approval_required", "timestamptz:created_at updated_at"}},
	{name: "change_rollback_executions", columns: []string{"int8:id change_id tenant_id rollback_plan_id initiated_by", "text:trigger_reason status result", "timestamptz:start_time end_time created_at updated_at"}},
	{name: "change_implementation_plans", columns: []string{"int8:id change_id tenant_id", "text:phase description responsible success_criteria status", "jsonb:tasks prerequisites dependencies", "timestamptz:start_date end_date created_at updated_at"}},
	// 008_add_initialization_ledger
	{name: "initialization_installations", columns: []string{"int8:id scope_id fencing_token last_run_id", "varchar:scope_type component installed_version source_checksum status lease_owner error_code", "timestamptz:heartbeat_at lease_expires_at created_at updated_at", "text:error_message", "jsonb:result_summary"}},
	{name: "initialization_runs", columns: []string{"int8:id scope_id", "varchar:scope_type target_version release_version requested_by executor_id status", "timestamptz:started_at completed_at created_at", "jsonb:result_summary", "text:error_message"}},
	{name: "initialization_component_attempts", columns: []string{"int8:id run_id scope_id fencing_token", "varchar:scope_type component from_version target_version source_checksum status error_code", "int4:attempt", "timestamptz:started_at completed_at created_at", "text:error_message", "jsonb:result_summary rollback_metadata"}},
	{name: "initialization_managed_records", columns: []string{"int8:id scope_id", "varchar:scope_type component source_key source_version manifest_checksum ownership_mode", "jsonb:managed_fields last_applied_values stable_key_aliases", "timestamptz:local_modified_at created_at updated_at", "bool:deprecated"}},
	// 034_problem_investigation_completion
	{name: "problem_investigations", columns: []string{"int8:id problem_id investigator_id", "text:status investigation_summary", "timestamptz:start_date estimated_completion_date actual_completion_date created_at updated_at"}},
	{name: "problem_investigation_steps", columns: []string{"int8:id investigation_id assigned_to", "int4:step_number", "text:step_title step_description status notes", "timestamptz:start_date completion_date created_at updated_at"}},
	{name: "problem_solutions", columns: []string{"int8:id problem_id proposed_by approved_by", "text:solution_type solution_description status priority risk_assessment approval_status", "timestamptz:proposed_date approval_date created_at updated_at", "int4:estimated_effort_hours", "float8:estimated_cost"}},
}
