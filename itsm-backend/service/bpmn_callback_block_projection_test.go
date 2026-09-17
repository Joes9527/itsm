package service

import (
	"context"
	"encoding/json"
	"errors"
	"testing"

	"github.com/stretchr/testify/require"
	"itsm-backend/ent"
)

func TestCallbackBlockProjection(t *testing.T) {
	for _, scenario := range []string{"blocked", "absent", "other_tenant", "other_instance", "other_task", "optional", "pending"} {
		t.Run(scenario, func(t *testing.T) {
			f := newBPMNAuthorizationFixture(t)
			_, task := seedBoundAssignment(t, f, "callback-block")
			grantBoundPermissions(t, f, f.actor, "service_request", "read", "service_request", "provision", "task", "read", "task", "update")
			task = f.client.ProcessTask.UpdateOne(task).SetStatus("completed").SaveX(f.userCtx)
			if scenario != "absent" {
				tenantID, instanceID, taskID := task.TenantID, task.ProcessInstanceID, task.ID
				if scenario == "other_tenant" {
					tenantID = f.otherTenant.ID
				}
				if scenario == "other_instance" {
					instanceID++
				}
				if scenario == "other_task" {
					taskID++
				}
				status := "blocked"
				if scenario == "pending" {
					status = "pending"
				}
				f.client.ProcessCallbackOutbox.Create().SetExecutionKey("block").SetTenantID(tenantID).SetProcessInstanceID(instanceID).SetProcessTaskID(taskID).SetCallbackKind("user_task").SetHandlerID("ticket_handler").SetTaskType("ticket_task").SetElementID(task.TaskDefinitionKey).SetStatus(status).SetOptionalDeclared(scenario == "optional").SetLastErrorClass("SECRET must never appear").SaveX(f.userCtx)
			}
			before := f.client.ProcessTask.GetX(f.userCtx, task.ID)
			mutations := 0
			f.client.Use(func(next ent.Mutator) ent.Mutator {
				return ent.MutateFunc(func(ctx context.Context, m ent.Mutation) (ent.Value, error) { mutations++; return next.Mutate(ctx, m) })
			})
			ctx := WithBPMNAccessScope(f.userCtx, BPMNAccessScope{UserID: f.actor.ID, TenantID: f.tenant.ID, CanReadAllTasks: true})
			view, err := f.engine.TaskService().ProjectTaskView(ctx, task)
			require.NoError(t, err)
			raw, err := json.Marshal(view)
			require.NoError(t, err)
			var fields map[string]interface{}
			require.NoError(t, json.Unmarshal(raw, &fields))
			if scenario == "blocked" {
				require.NotNil(t, fields["callbackBlock"])
			} else {
				require.NotContains(t, fields, "callbackBlock")
			}
			require.NotContains(t, string(raw), "SECRET")
			require.Equal(t, "completed", fields["status"])
			rows, total, listErr := f.engine.TaskService().ListUserTaskViews(ctx, &ListUserTasksRequest{Page: 1, PageSize: 10})
			require.NoError(t, listErr)
			require.Equal(t, 1, total)
			require.Len(t, rows, 1)
			listJSON, _ := json.Marshal(rows[0])
			require.JSONEq(t, string(raw), string(listJSON))
			require.Zero(t, mutations)
			beforeJSON, _ := json.Marshal(before)
			afterJSON, _ := json.Marshal(f.client.ProcessTask.GetX(f.userCtx, task.ID))
			require.JSONEq(t, string(beforeJSON), string(afterJSON))
		})
	}
}

func TestCallbackBlockReadFailurePropagates(t *testing.T) {
	f := newBPMNAuthorizationFixture(t)
	_, task := seedBoundAssignment(t, f, "callback-failure")
	grantBoundPermissions(t, f, f.actor, "service_request", "read", "service_request", "provision", "task", "read", "task", "update")
	fault := errors.New("callback read unavailable")
	f.client.ProcessCallbackOutbox.Intercept(ent.InterceptFunc(func(next ent.Querier) ent.Querier {
		return ent.QuerierFunc(func(context.Context, ent.Query) (ent.Value, error) { return nil, fault })
	}))
	ctx := WithBPMNAccessScope(f.userCtx, BPMNAccessScope{UserID: f.actor.ID, TenantID: f.tenant.ID, CanReadAllTasks: true})
	_, err := f.engine.TaskService().ProjectTaskView(ctx, task)
	require.ErrorIs(t, err, fault)
	_, _, err = f.engine.TaskService().ListUserTaskViews(ctx, &ListUserTasksRequest{Page: 1, PageSize: 10})
	require.ErrorIs(t, err, fault)
}

func TestCallbackBlockProjectionBoundsBatchReads(t *testing.T) {
	f := newBPMNAuthorizationFixture(t)
	_, task := seedBoundAssignment(t, f, "callback-batch")
	queries := 0
	f.client.ProcessCallbackOutbox.Intercept(ent.InterceptFunc(func(next ent.Querier) ent.Querier {
		return ent.QuerierFunc(func(ctx context.Context, q ent.Query) (ent.Value, error) { queries++; return next.Query(ctx, q) })
	}))
	tasks := make([]*ent.ProcessTask, bpmnTaskReadBatchSize+1)
	for i := range tasks {
		copy := *task
		copy.ID += i
		tasks[i] = &copy
	}
	err := withBPMNTaskReadSnapshot(f.userCtx, f.client, func(ctx context.Context, tx *ent.Tx) error {
		blocks, err := loadTaskCallbackBlocks(ctx, tx.Client(), tasks)
		require.Empty(t, blocks)
		return err
	})
	require.NoError(t, err)
	require.Equal(t, 2, queries)
}
