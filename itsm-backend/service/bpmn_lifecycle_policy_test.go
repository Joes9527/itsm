package service

import (
	"context"
	"errors"
	"fmt"
	"testing"

	"itsm-backend/common"
	"itsm-backend/ent/processinstance"
	"itsm-backend/ent/processtask"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestValidateBPMNProcessLifecycle(t *testing.T) {
	tests := []struct {
		command BPMNProcessCommand
		status  string
		allowed bool
	}{
		{BPMNProcessCommandSuspend, "running", true},
		{BPMNProcessCommandSuspend, "suspended", false},
		{BPMNProcessCommandSuspend, "completed", false},
		{BPMNProcessCommandSuspend, "terminated", false},
		{BPMNProcessCommandResume, "suspended", true},
		{BPMNProcessCommandResume, "running", false},
		{BPMNProcessCommandResume, "completed", false},
		{BPMNProcessCommandResume, "terminated", false},
		{BPMNProcessCommandTerminate, "running", true},
		{BPMNProcessCommandTerminate, "suspended", true},
		{BPMNProcessCommandTerminate, "completed", false},
		{BPMNProcessCommandTerminate, "terminated", false},
		{BPMNProcessCommandSuspend, "unknown", false},
		{BPMNProcessCommand("unknown"), "running", false},
	}

	for _, tc := range tests {
		t.Run(fmt.Sprintf("%s_from_%s", tc.command, tc.status), func(t *testing.T) {
			err := ValidateBPMNProcessLifecycle(tc.command, tc.status)
			assert.Equal(t, tc.allowed, err == nil)
		})
	}
}

func TestValidateBPMNTaskLifecycle(t *testing.T) {
	knownStatuses := []string{
		common.ProcessTaskStatusCreated,
		common.ProcessTaskStatusAssigned,
		common.ProcessTaskStatusStarted,
		common.ProcessTaskStatusDelegated,
		common.ProcessTaskStatusCompleted,
		common.ProcessTaskStatusCancelled,
	}
	tests := []struct {
		command BPMNTaskCommand
		allowed map[string]bool
	}{
		{BPMNTaskCommandAssign, activeTaskStatusesForTest()},
		{BPMNTaskCommandClaim, map[string]bool{common.ProcessTaskStatusCreated: true}},
		{BPMNTaskCommandComplete, activeTaskStatusesForTest()},
		{BPMNTaskCommandCancel, activeTaskStatusesForTest()},
		{BPMNTaskCommandDelegate, activeTaskStatusesForTest()},
		{BPMNTaskCommandSetVariables, activeTaskStatusesForTest()},
		{BPMNTaskCommandCreateCounterSign, activeTaskStatusesForTest()},
		{BPMNTaskCommandVote, map[string]bool{common.ProcessTaskStatusAssigned: true}},
	}

	for _, tc := range tests {
		for _, status := range knownStatuses {
			t.Run(fmt.Sprintf("%s_from_%s", tc.command, status), func(t *testing.T) {
				err := ValidateBPMNTaskLifecycle(tc.command, status)
				assert.Equal(t, tc.allowed[status], err == nil)
			})
		}
		t.Run(fmt.Sprintf("%s_from_unknown", tc.command), func(t *testing.T) {
			assert.Error(t, ValidateBPMNTaskLifecycle(tc.command, "unknown"))
		})
	}

	assert.Error(t, ValidateBPMNTaskLifecycle(BPMNTaskCommand("unknown"), common.ProcessTaskStatusCreated))
}

func TestBPMNProcessLifecyclePredicate(t *testing.T) {
	f := newBPMNAuthorizationFixture(t)
	tests := []struct {
		name            string
		command         BPMNProcessCommand
		status          string
		rowVersion      int
		observedVersion int
		matches         bool
	}{
		{"suspend_running", BPMNProcessCommandSuspend, "running", 7, 7, true},
		{"suspend_stale_version", BPMNProcessCommandSuspend, "running", 7, 6, false},
		{"resume_wrong_status", BPMNProcessCommandResume, "running", 7, 7, false},
		{"terminate_suspended", BPMNProcessCommandTerminate, "suspended", 7, 7, true},
	}

	for i, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			instance := f.createProcessInstance(t, f.tenant, fmt.Sprintf("lifecycle-%d", i))
			_, err := f.client.ProcessInstance.UpdateOne(instance).
				SetStatus(tc.status).
				SetVersion(tc.rowVersion).
				Save(f.userCtx)
			require.NoError(t, err)

			lifecyclePredicate, err := bpmnProcessLifecyclePredicate(tc.command, tc.observedVersion)
			require.NoError(t, err)
			matches, err := f.client.ProcessInstance.Query().
				Where(processinstance.IDEQ(instance.ID), lifecyclePredicate).
				Exist(context.Background())
			require.NoError(t, err)
			assert.Equal(t, tc.matches, matches)
		})
	}

	predicate, err := bpmnProcessLifecyclePredicate(BPMNProcessCommand("unknown"), 1)
	assert.Nil(t, predicate)
	assert.Error(t, err)
}

func TestBPMNTaskLifecyclePredicate(t *testing.T) {
	f := newBPMNAuthorizationFixture(t)
	instance := f.createProcessInstance(t, f.tenant, "task-lifecycle")
	tests := []struct {
		command BPMNTaskCommand
		status  string
	}{
		{BPMNTaskCommandAssign, common.ProcessTaskStatusStarted},
		{BPMNTaskCommandClaim, common.ProcessTaskStatusCreated},
		{BPMNTaskCommandComplete, common.ProcessTaskStatusDelegated},
		{BPMNTaskCommandCancel, common.ProcessTaskStatusAssigned},
		{BPMNTaskCommandDelegate, common.ProcessTaskStatusCreated},
		{BPMNTaskCommandSetVariables, common.ProcessTaskStatusStarted},
		{BPMNTaskCommandCreateCounterSign, common.ProcessTaskStatusDelegated},
		{BPMNTaskCommandVote, common.ProcessTaskStatusAssigned},
	}

	for i, tc := range tests {
		t.Run(string(tc.command), func(t *testing.T) {
			task := f.createProcessTask(t, instance, f.tenant.ID, fmt.Sprintf("lifecycle-%d", i), "", "", "")
			_, err := f.client.ProcessTask.UpdateOne(task).
				SetStatus(tc.status).
				SetAggregationVersion(9).
				Save(f.userCtx)
			require.NoError(t, err)

			lifecyclePredicate, err := bpmnTaskLifecyclePredicate(tc.command, 9)
			require.NoError(t, err)
			matches, err := f.client.ProcessTask.Query().
				Where(processtask.IDEQ(task.ID), lifecyclePredicate).
				Exist(context.Background())
			require.NoError(t, err)
			assert.True(t, matches)

			stalePredicate, err := bpmnTaskLifecyclePredicate(tc.command, 8)
			require.NoError(t, err)
			matches, err = f.client.ProcessTask.Query().
				Where(processtask.IDEQ(task.ID), stalePredicate).
				Exist(context.Background())
			require.NoError(t, err)
			assert.False(t, matches)

			_, err = f.client.ProcessTask.UpdateOneID(task.ID).
				SetStatus(common.ProcessTaskStatusCompleted).
				Save(f.userCtx)
			require.NoError(t, err)
			matches, err = f.client.ProcessTask.Query().
				Where(processtask.IDEQ(task.ID), lifecyclePredicate).
				Exist(context.Background())
			require.NoError(t, err)
			assert.False(t, matches)
		})
	}

	predicate, err := bpmnTaskLifecyclePredicate(BPMNTaskCommand("unknown"), 1)
	assert.Nil(t, predicate)
	assert.Error(t, err)
}

func TestBPMNLifecycleErrorsAreTyped(t *testing.T) {
	tests := []error{
		ValidateBPMNProcessLifecycle(BPMNProcessCommand("unknown"), "running"),
		ValidateBPMNTaskLifecycle(BPMNTaskCommand("unknown"), common.ProcessTaskStatusCreated),
		bpmnProcessLifecycleConflict(BPMNProcessCommandSuspend),
		bpmnTaskLifecycleConflict(BPMNTaskCommandClaim),
	}

	for _, err := range tests {
		var appErr *common.AppError
		require.True(t, errors.As(err, &appErr), "%T must be a typed application error", err)
		assert.Contains(t, []common.ErrorCode{common.ErrCodeValidation, common.ErrCodeConflict}, appErr.Code)
	}
}

func activeTaskStatusesForTest() map[string]bool {
	return map[string]bool{
		common.ProcessTaskStatusCreated:   true,
		common.ProcessTaskStatusAssigned:  true,
		common.ProcessTaskStatusStarted:   true,
		common.ProcessTaskStatusDelegated: true,
	}
}
