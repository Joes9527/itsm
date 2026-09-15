package contract

import (
	"os"
	"testing"

	"github.com/stretchr/testify/require"
	"itsm-backend/service"
)

func TestWorkItemAssignmentBrowserFixtureUsesOnlyHumanTasks(t *testing.T) {
	xml, err := os.ReadFile("../../../itsm-frontend/tests/e2e/fixtures/work-item-assignment.bpmn")
	require.NoError(t, err)
	definitions, err := service.NewBPMNParser().ParseXML(xml)
	require.NoError(t, err)
	require.Len(t, definitions.Processes, 1)
	process := definitions.Processes[0]
	require.Len(t, process.UserTasks, 4)
	require.Empty(t, process.ServiceTasks)
	require.Empty(t, process.CallActivities)
	require.Empty(t, process.ScriptTasks)
	require.Empty(t, process.BusinessRuleTasks)
	require.Empty(t, process.SubProcesses)
	require.Equal(t, "assignment_acceptance_helpdesk", process.UserTasks[0].CandidateGroups)
	require.Equal(t, "work_item_assignee", process.UserTasks[1].AssigneeSource)
	require.Equal(t, "fulfillment", process.UserTasks[1].TaskPurpose)
	require.Equal(t, "assignment_acceptance_manager", process.UserTasks[2].CandidateGroups)
	require.Empty(t, process.UserTasks[3].AssigneeSource)
	require.Empty(t, process.UserTasks[3].Assignee)
	for _, task := range process.UserTasks {
		require.Empty(t, task.ServiceTaskType())
	}
}
