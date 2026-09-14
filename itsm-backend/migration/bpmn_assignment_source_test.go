package migration

import (
	"os"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestBPMNAssignmentSourceMigrationRegistered(t *testing.T) {
	const version = "032_bpmn_assignment_source"
	require.Equal(t, version, RegisteredMigrations[len(RegisteredMigrations)-1].Version)
	asset, err := os.ReadFile("../migrations/" + version + ".sql")
	require.NoError(t, err)
	require.Equal(t, strings.TrimSpace(GetMigrationSQL(version)), strings.TrimSpace(string(asset)))
}

func TestBPMNAssignmentSourceMigrationRejectsUnsupportedAndConflictingBindings(t *testing.T) {
	sql := GetMigrationSQL("032_bpmn_assignment_source")
	require.Contains(t, sql, "assignee_source")
	require.Contains(t, sql, "work_item_assignee")
	require.Contains(t, sql, "candidate_users")
	require.Contains(t, sql, "candidate_groups")
	require.Contains(t, sql, "CHECK")
	require.Contains(t, sql, "BEFORE UPDATE OF assignee_source")
	require.Contains(t, sql, "NEW.assignee_source IS DISTINCT FROM OLD.assignee_source")

	verify, err := os.ReadFile("../migrations/032_bpmn_assignment_source_verify.sql")
	require.NoError(t, err)
	require.Contains(t, string(verify), "source_column.column_default <> $default$''::character varying$default$")
	require.Contains(t, string(verify), "is_nullable")
	require.Contains(t, string(verify), "work_item_assignee")
	require.Contains(t, string(verify), "process_tasks_assignee_source_immutable")
}
