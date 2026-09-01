package service

import (
	"context"
	"strconv"
	"testing"

	"itsm-backend/common"
	"itsm-backend/ent/processauditlog"
	"itsm-backend/service/bpmn"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func requireBPMNLifecycleConflict(t *testing.T, err error) {
	t.Helper()
	var appErr *common.AppError
	require.ErrorAs(t, err, &appErr)
	assert.Equal(t, common.ErrCodeConflict, appErr.Code)
}

func TestBPMNKafDelegatedTaskRejectsHumanMutations(t *testing.T) {
	mutations := map[BPMNTaskCommand]func(*bpmnAuthorizationFixture, context.Context, string) error{
		BPMNTaskCommandAssign: func(f *bpmnAuthorizationFixture, ctx context.Context, taskID string) error {
			return f.engine.TaskService().AssignTask(ctx, taskID, strconv.Itoa(f.outsider.ID))
		},
		BPMNTaskCommandClaim: func(f *bpmnAuthorizationFixture, ctx context.Context, taskID string) error {
			return f.engine.TaskService().ClaimTask(ctx, taskID, strconv.Itoa(f.actor.ID))
		},
		BPMNTaskCommandComplete: func(f *bpmnAuthorizationFixture, ctx context.Context, taskID string) error {
			return f.engine.TaskService().CompleteTask(ctx, taskID, nil)
		},
		BPMNTaskCommandCancel: func(f *bpmnAuthorizationFixture, ctx context.Context, taskID string) error {
			return f.engine.TaskService().CancelTask(ctx, taskID, "human")
		},
		BPMNTaskCommandDelegate: func(f *bpmnAuthorizationFixture, ctx context.Context, taskID string) error {
			return f.engine.TaskService().DelegateTask(ctx, taskID, strconv.Itoa(f.outsider.ID))
		},
		BPMNTaskCommandSetVariables: func(f *bpmnAuthorizationFixture, ctx context.Context, taskID string) error {
			return f.engine.TaskService().SetTaskVariables(ctx, taskID, map[string]interface{}{"changed": true})
		},
		BPMNTaskCommandCreateCounterSign: func(f *bpmnAuthorizationFixture, ctx context.Context, taskID string) error {
			_, err := f.engine.TaskService().CreateCounterSignTasks(ctx, taskID, &CounterSignRequest{Approvers: []string{strconv.Itoa(f.outsider.ID)}})
			return err
		},
		BPMNTaskCommandVote: func(f *bpmnAuthorizationFixture, ctx context.Context, taskID string) error {
			return f.engine.TaskService().Vote(ctx, taskID, &VoteRequest{Approved: true})
		},
	}
	for command, mutate := range mutations {
		t.Run(string(command), func(t *testing.T) {
			f := newBPMNAuthorizationFixture(t)
			instance := f.createProcessInstance(t, f.tenant, "kaf-fence-"+string(command))
			task := f.createProcessTask(t, instance, f.tenant.ID, "kaf-fence-"+string(command), strconv.Itoa(f.actor.ID), "", "")
			task, err := f.client.ProcessTask.UpdateOne(task).
				SetTaskType(bpmn.KafDelegateTaskType).
				SetStatus(common.ProcessTaskStatusDelegated).
				SetTaskVariables(map[string]interface{}{"preserved": true}).
				Save(f.userCtx)
			require.NoError(t, err)

			err = mutate(f, f.scopedCtx(false, false, false, false), task.TaskID)
			require.Error(t, err)
			after := f.client.ProcessTask.GetX(f.userCtx, task.ID)
			assert.Equal(t, common.ProcessTaskStatusDelegated, after.Status)
			assert.Equal(t, task.Assignee, after.Assignee)
			assert.Equal(t, task.TaskVariables, after.TaskVariables)
			assert.Equal(t, task.AggregationVersion, after.AggregationVersion)
			assert.Zero(t, f.client.ProcessAuditLog.Query().Where(processauditlog.ProcessInstanceID(instance.ID)).CountX(f.userCtx))
		})
	}
}

func TestBPMNTaskTerminalMutations(t *testing.T) {
	mutations := map[BPMNTaskCommand]func(*bpmnAuthorizationFixture, context.Context, string) error{
		BPMNTaskCommandAssign: func(f *bpmnAuthorizationFixture, ctx context.Context, taskID string) error {
			return f.engine.TaskService().AssignTask(ctx, taskID, strconv.Itoa(f.outsider.ID))
		},
		BPMNTaskCommandClaim: func(f *bpmnAuthorizationFixture, ctx context.Context, taskID string) error {
			return f.engine.TaskService().ClaimTask(ctx, taskID, strconv.Itoa(f.actor.ID))
		},
		BPMNTaskCommandComplete: func(f *bpmnAuthorizationFixture, ctx context.Context, taskID string) error {
			return f.engine.TaskService().CompleteTask(ctx, taskID, map[string]interface{}{"changed": true})
		},
		BPMNTaskCommandCancel: func(f *bpmnAuthorizationFixture, ctx context.Context, taskID string) error {
			return f.engine.TaskService().CancelTask(ctx, taskID, "terminal")
		},
		BPMNTaskCommandDelegate: func(f *bpmnAuthorizationFixture, ctx context.Context, taskID string) error {
			return f.engine.TaskService().DelegateTask(ctx, taskID, strconv.Itoa(f.outsider.ID))
		},
		BPMNTaskCommandSetVariables: func(f *bpmnAuthorizationFixture, ctx context.Context, taskID string) error {
			return f.engine.TaskService().SetTaskVariables(ctx, taskID, map[string]interface{}{"changed": true})
		},
		BPMNTaskCommandCreateCounterSign: func(f *bpmnAuthorizationFixture, ctx context.Context, taskID string) error {
			_, err := f.engine.TaskService().CreateCounterSignTasks(ctx, taskID, &CounterSignRequest{
				Approvers: []string{strconv.Itoa(f.outsider.ID)}, ApprovalType: "parallel", Threshold: 1,
			})
			return err
		},
		BPMNTaskCommandVote: func(f *bpmnAuthorizationFixture, ctx context.Context, taskID string) error {
			return f.engine.TaskService().Vote(ctx, taskID, &VoteRequest{Approved: true, Comment: "terminal"})
		},
	}

	for command, mutate := range mutations {
		for _, status := range []string{common.ProcessTaskStatusCompleted, common.ProcessTaskStatusCancelled} {
			t.Run(string(command)+"_from_"+status, func(t *testing.T) {
				f := newBPMNAuthorizationFixture(t)
				instance := f.createProcessInstance(t, f.tenant, "terminal-"+string(command)+"-"+status)
				task := f.createProcessTask(t, instance, f.tenant.ID, "terminal-"+string(command)+"-"+status, strconv.Itoa(f.actor.ID), "", "")
				beforeVariables := map[string]interface{}{"preserved": true}
				task, err := f.client.ProcessTask.UpdateOne(task).
					SetStatus(status).
					SetTaskVariables(beforeVariables).
					Save(f.userCtx)
				require.NoError(t, err)

				err = mutate(f, f.scopedCtx(false, false, false, false), task.TaskID)
				requireBPMNLifecycleConflict(t, err)

				after := f.client.ProcessTask.GetX(f.userCtx, task.ID)
				assert.Equal(t, status, after.Status)
				assert.Equal(t, beforeVariables, after.TaskVariables)
				logs := f.client.ProcessAuditLog.Query().Where(
					processauditlog.ProcessInstanceID(instance.ID),
					processauditlog.Action(AuditActionTaskMutationRejected),
				).AllX(f.userCtx)
				require.Len(t, logs, 1)
				assert.Equal(t, string(command), logs[0].Metadata["command"])
				assert.Equal(t, status, logs[0].Metadata["current_status"])
				assert.Equal(t, "conflict", logs[0].Metadata["result"])
			})
		}
	}
}

func TestBPMNProcessTerminalMutations(t *testing.T) {
	tests := []struct {
		name, status, successAction string
		mutate                      func(*bpmnAuthorizationFixture, context.Context, string) error
	}{
		{"suspend_completed", "completed", AuditActionProcessSuspended, func(f *bpmnAuthorizationFixture, ctx context.Context, id string) error {
			return f.engine.SuspendProcess(ctx, id, "terminal")
		}},
		{"resume_terminated", "terminated", AuditActionProcessResumed, func(f *bpmnAuthorizationFixture, ctx context.Context, id string) error {
			return f.engine.ResumeProcess(ctx, id)
		}},
		{"terminate_completed", "completed", AuditActionProcessTerminated, func(f *bpmnAuthorizationFixture, ctx context.Context, id string) error {
			return f.engine.TerminateProcess(ctx, id, "terminal")
		}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			f := newBPMNAuthorizationFixture(t)
			instance := f.createProcessInstance(t, f.tenant, tt.name)
			instance, err := f.client.ProcessInstance.UpdateOne(instance).SetStatus(tt.status).Save(f.userCtx)
			require.NoError(t, err)

			err = tt.mutate(f, f.scopedCtx(false, true, false, false), instance.ProcessInstanceID)
			requireBPMNLifecycleConflict(t, err)
			after := f.client.ProcessInstance.GetX(f.userCtx, instance.ID)
			assert.Equal(t, tt.status, after.Status)
			assert.Equal(t, instance.Version, after.Version)
			assert.Zero(t, f.client.ProcessAuditLog.Query().Where(
				processauditlog.ProcessInstanceID(instance.ID),
				processauditlog.Action(tt.successAction),
			).CountX(f.userCtx))
		})
	}
}
