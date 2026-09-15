package service

import (
	"strconv"
	"testing"

	"itsm-backend/ent/processtask"

	"github.com/stretchr/testify/require"
)

func TestFulfillmentTaskExplicitCandidatesRemainUnassigned(t *testing.T) {
	for _, kind := range []string{"users", "group", "missing-group"} {
		t.Run(kind, func(t *testing.T) {
			f := newApprovalAssignmentFixture(t)
			requester := f.createUser(t, "requester", 0)
			support := f.createUser(t, "support", 0)
			instance := f.createInstance(t, kind, map[string]interface{}{"requester_id": requester.ID, "assignee_id": requester.ID})
			definition := &BPMNUserTask{ID: "receive", Name: "Receive request", TaskPurpose: "fulfillment"}
			switch kind {
			case "users":
				definition.CandidateUsers = strconv.Itoa(support.ID)
			case "group":
				f.createGroup(t, "helpdesk", support.ID)
				definition.CandidateGroups = "helpdesk"
			case "missing-group":
				definition.CandidateGroups = "missing-helpdesk"
			}
			require.NoError(t, f.engine.createUserTask(f.ctx, instance, definition))
			task := f.getCreatedTask(t, instance.ID, definition.ID)
			require.Empty(t, task.Assignee, "explicit candidate routing must not assign the requester")
			if kind == "users" {
				require.Contains(t, task.CandidateUsers, strconv.Itoa(support.ID))
			}
			if kind == "group" {
				require.Contains(t, task.CandidateUsers, support.Username)
			}
			if kind == "missing-group" {
				require.Empty(t, task.CandidateUsers)
			}
		})
	}
}

func TestFulfillmentTaskPreservesExplicitAssigneeAndRequesterConfirmation(t *testing.T) {
	for _, explicit := range []bool{false, true} {
		t.Run(strconv.FormatBool(explicit), func(t *testing.T) {
			f := newApprovalAssignmentFixture(t)
			requester := f.createUser(t, "requester", 0)
			support := f.createUser(t, "support", 0)
			instance := f.createInstance(t, "assigned", map[string]interface{}{"requester_id": requester.ID})
			definition := &BPMNUserTask{ID: "confirm", Name: "Confirm delivery"}
			want := strconv.Itoa(requester.ID)
			if explicit {
				definition.Assignee = strconv.Itoa(support.ID)
				definition.CandidateUsers = strconv.Itoa(requester.ID)
				want = definition.Assignee
			}
			require.NoError(t, f.engine.createUserTask(f.ctx, instance, definition))
			require.Equal(t, want, f.getCreatedTask(t, instance.ID, definition.ID).Assignee)
		})
	}
}

func TestFulfillmentCandidateCanClaimWithoutRequesterAssignment(t *testing.T) {
	f := newBPMNAuthorizationFixture(t)
	instance := f.seedRunningInstance(t, "fulfillment-claim")
	instance = instance.Update().SetVariables(map[string]interface{}{"requester_id": f.outsider.ID}).SaveX(f.userCtx)
	require.NoError(t, f.engine.createUserTask(f.userCtx, instance, &BPMNUserTask{
		ID: "receive", Name: "Receive request", TaskPurpose: "fulfillment", CandidateUsers: f.actor.Username,
	}))
	task := f.client.ProcessTask.Query().Where(processtask.ProcessInstanceIDEQ(instance.ID), processtask.TaskDefinitionKeyEQ("receive")).OnlyX(f.userCtx)
	requireBPMNForbidden(t, f.engine.TaskService().ClaimTaskByID(f.typedTaskScopeOnlyCtx(f.outsider, false), task.ID, f.outsider.ID))
	requireBPMNForbidden(t, f.engine.TaskService().ClaimTaskByID(f.typedTaskScopeOnlyCtx(f.otherActor, false), task.ID, f.otherActor.ID))
	require.NoError(t, f.engine.TaskService().ClaimTaskByID(f.typedTaskScopeOnlyCtx(f.actor, false), task.ID, f.actor.ID))
	saved := f.client.ProcessTask.GetX(f.userCtx, task.ID)
	require.Equal(t, strconv.Itoa(f.actor.ID), saved.Assignee)
	require.Equal(t, "assigned", saved.Status)
}
