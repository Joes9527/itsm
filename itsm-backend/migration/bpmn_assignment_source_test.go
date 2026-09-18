package migration

import (
	"os"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestBPMNAssignmentSourceMigrationRegistered(t *testing.T) {
	const version = "047_bpmn_assignment_source"
	// 047 keeps its place: later ordinary migrations append after it and before retirement.
	require.Equal(t, version, RegisteredMigrations[len(RegisteredMigrations)-5].Version)
	require.Equal(t, CTIGovernanceVersion, RegisteredMigrations[len(RegisteredMigrations)-4].Version)
	require.Equal(t, DepartmentCodeTenantUniqueVersion, RegisteredMigrations[len(RegisteredMigrations)-3].Version)
	require.Equal(t, DepartmentNodeTypeVersion, RegisteredMigrations[len(RegisteredMigrations)-2].Version)
	require.Equal(t, WorkItemRetireVersion, RegisteredMigrations[len(RegisteredMigrations)-1].Version)
	asset, err := os.ReadFile("../migrations/" + version + ".sql")
	require.NoError(t, err)
	require.Equal(t, strings.TrimSpace(GetMigrationSQL(version)), strings.TrimSpace(string(asset)))
}

func TestBPMNAssignmentSourceMigrationRejectsUnsupportedAndConflictingBindings(t *testing.T) {
	sql := GetMigrationSQL("047_bpmn_assignment_source")
	require.Contains(t, sql, "assignee_source")
	require.Contains(t, sql, "work_item_assignee")
	require.Contains(t, sql, "candidate_users")
	require.Contains(t, sql, "candidate_groups")
	require.Contains(t, sql, "CHECK")
	require.Contains(t, sql, "BEFORE UPDATE OF assignee_source")
	require.Contains(t, sql, "NEW.assignee_source IS DISTINCT FROM OLD.assignee_source")

	verify, err := os.ReadFile("../migrations/047_bpmn_assignment_source_verify.sql")
	require.NoError(t, err)
	require.Contains(t, string(verify), "source_column.column_default IS DISTINCT FROM $default$''::character varying$default$")
	require.Contains(t, string(verify), "is_nullable")
	require.Contains(t, string(verify), "work_item_assignee")
	require.Contains(t, string(verify), "process_tasks_assignee_source_immutable")
}

func TestBPMNAssignmentSourceAppendPreservesExistingRetirementReceipt(t *testing.T) {
	catalog := ControlledMigrationCatalog()
	// prior 是"缺少追加尾部"的账本：047 及其之后追加的普通迁移全部排除，
	// 它们必须能在同一个批次里按顺序执行完。
	tail := map[string]bool{
		"047_bpmn_assignment_source":      true,
		CTIGovernanceVersion:              true,
		DepartmentCodeTenantUniqueVersion: true,
		DepartmentNodeTypeVersion:         true,
	}
	var prior []Migration
	for _, definition := range catalog {
		if !tail[definition.Migration.Version] {
			prior = append(prior, definition.Migration)
		}
	}
	plan, err := PlanMigrations(catalog, controlledReceipts(prior), OpUp, nil)
	require.NoError(t, err)
	require.Len(t, plan.Executable, 4)
	require.Equal(t, "047_bpmn_assignment_source", plan.Executable[0].Version)
	require.Equal(t, CTIGovernanceVersion, plan.Executable[1].Version)
	require.Equal(t, DepartmentCodeTenantUniqueVersion, plan.Executable[2].Version)
	require.Equal(t, DepartmentNodeTypeVersion, plan.Executable[3].Version)
	require.Empty(t, plan.PendingManual)
}
