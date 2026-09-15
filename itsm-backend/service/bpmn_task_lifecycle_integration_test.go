package service

import (
	"context"
	"strconv"
	"testing"

	"itsm-backend/common"
	"itsm-backend/ent/processtask"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestBPMNTaskLifecycleMutationsIncrementAggregationVersion(t *testing.T) {
	tests := []struct {
		name    string
		prepare func(*testing.T, *bpmnAuthorizationFixture, string) string
		mutate  func(*bpmnAuthorizationFixture, context.Context, string) error
	}{
		{"assign", nil, func(f *bpmnAuthorizationFixture, ctx context.Context, id string) error {
			return f.engine.TaskService().AssignTask(ctx, id, strconv.Itoa(f.outsider.ID))
		}},
		{"claim", nil, func(f *bpmnAuthorizationFixture, ctx context.Context, id string) error {
			return f.engine.TaskService().ClaimTask(ctx, id, strconv.Itoa(f.actor.ID))
		}},
		{"complete", nil, func(f *bpmnAuthorizationFixture, ctx context.Context, id string) error {
			return f.engine.TaskService().CompleteTask(ctx, id, map[string]interface{}{"approved": true})
		}},
		{"cancel", nil, func(f *bpmnAuthorizationFixture, ctx context.Context, id string) error {
			return f.engine.TaskService().CancelTask(ctx, id, "lifecycle")
		}},
		{"delegate", nil, func(f *bpmnAuthorizationFixture, ctx context.Context, id string) error {
			return f.engine.TaskService().DelegateTask(ctx, id, strconv.Itoa(f.outsider.ID))
		}},
		{"set_variables", nil, func(f *bpmnAuthorizationFixture, ctx context.Context, id string) error {
			return f.engine.TaskService().SetTaskVariables(ctx, id, map[string]interface{}{"changed": true})
		}},
		{"counter_sign", nil, func(f *bpmnAuthorizationFixture, ctx context.Context, id string) error {
			_, err := f.engine.TaskService().CreateCounterSignTasks(ctx, id, &CounterSignRequest{
				Approvers: []string{strconv.Itoa(f.outsider.ID)}, ApprovalType: "parallel", Threshold: 1,
			})
			return err
		}},
		{"vote", func(t *testing.T, f *bpmnAuthorizationFixture, id string) string {
			task, err := f.engine.TaskService().GetTask(f.typedTaskScopeOnlyCtx(f.actor, true), id)
			require.NoError(t, err)
			_, err = f.client.ProcessTask.UpdateOne(task).SetStatus(common.ProcessTaskStatusAssigned).Save(f.userCtx)
			require.NoError(t, err)
			return id
		}, func(f *bpmnAuthorizationFixture, ctx context.Context, id string) error {
			return f.engine.TaskService().Vote(ctx, id, &VoteRequest{Approved: true})
		}},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			f := newBPMNAuthorizationFixture(t)
			task := f.seedNonParticipantApprovalTask(t, "version-"+tt.name)
			task, err := f.client.ProcessTask.UpdateOne(task).SetCandidateUsers(f.actor.Username).Save(f.userCtx)
			require.NoError(t, err)
			taskID := task.TaskID
			if tt.prepare != nil {
				taskID = tt.prepare(t, f, taskID)
			}
			before := f.client.ProcessTask.GetX(f.userCtx, task.ID)
			require.NoError(t, tt.mutate(f, f.typedTaskScopeOnlyCtx(f.actor, false), taskID))
			after := f.client.ProcessTask.GetX(f.userCtx, task.ID)
			assert.Equal(t, before.AggregationVersion+1, after.AggregationVersion)
		})
	}
}

func TestBPMNProcessLifecycleMutationsIncrementVersion(t *testing.T) {
	f := newBPMNAuthorizationFixture(t)
	instance := f.seedRunningInstance(t, "process-version")
	ctx := f.scopedCtx(false, true, false, false)

	require.NoError(t, f.engine.SuspendProcess(ctx, instance.ProcessInstanceID, "maintenance"))
	suspended := f.client.ProcessInstance.GetX(f.userCtx, instance.ID)
	assert.Equal(t, instance.Version+1, suspended.Version)

	require.NoError(t, f.engine.ResumeProcess(ctx, instance.ProcessInstanceID))
	resumed := f.client.ProcessInstance.GetX(f.userCtx, instance.ID)
	assert.Equal(t, suspended.Version+1, resumed.Version)
	activeTask := f.client.ProcessTask.Query().Where(
		processtask.ProcessInstanceID(instance.ID), processtask.Status(common.ProcessTaskStatusCreated),
	).OnlyX(f.userCtx)
	completedTask := f.client.ProcessTask.Query().Where(
		processtask.ProcessInstanceID(instance.ID), processtask.Status(common.ProcessTaskStatusCompleted),
	).OnlyX(f.userCtx)

	require.NoError(t, f.engine.TerminateProcess(ctx, instance.ProcessInstanceID, "done"))
	terminated := f.client.ProcessInstance.GetX(f.userCtx, instance.ID)
	assert.Equal(t, resumed.Version+1, terminated.Version)
	assert.Equal(t, activeTask.AggregationVersion+1, f.client.ProcessTask.GetX(f.userCtx, activeTask.ID).AggregationVersion)
	assert.Equal(t, common.ProcessTaskStatusCancelled, f.client.ProcessTask.GetX(f.userCtx, activeTask.ID).Status)
	assert.Equal(t, completedTask.AggregationVersion, f.client.ProcessTask.GetX(f.userCtx, completedTask.ID).AggregationVersion)
}
