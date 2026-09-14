package service

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"go.uber.org/zap"
	"itsm-backend/common/executionscope"
	"itsm-backend/config"
	"itsm-backend/database"
)

func TestCandidateIndependentProcessStartPreservesState(t *testing.T) {
	f := newBPMNAuthorizationFixture(t)
	policy, err := database.NewExecutionPolicy(config.ExecutionConfig{
		Mode: "candidate", DeploymentID: "bare-process-test",
		Scopes: []config.ExecutionScopeConfig{{TenantID: f.tenant.ID, ScopeID: "f0a0aa2d-b807-4d65-8433-86822245bdd6"}},
	})
	require.NoError(t, err)
	engine := NewCustomProcessEngine(f.client, zap.NewNop().Sugar(), policy)
	ctx := f.scopedCtx(true, true, true, true)
	beforeInstances := f.client.ProcessInstance.Query().CountX(ctx)
	beforeTasks := f.client.ProcessTask.Query().CountX(ctx)
	beforeAudit := f.client.ProcessAuditLog.Query().CountX(ctx)
	beforeHistory := f.client.ProcessExecutionHistory.Query().CountX(ctx)
	beforeCallbacks := f.client.ProcessCallbackOutbox.Query().CountX(ctx)

	instance, err := engine.StartProcess(ctx, f.definition.Key, "independent-local-case", "", 0, nil)

	assert.ErrorIs(t, err, executionscope.ErrDenied)
	assert.Nil(t, instance)
	assert.Equal(t, beforeInstances, f.client.ProcessInstance.Query().CountX(ctx))
	assert.Equal(t, beforeTasks, f.client.ProcessTask.Query().CountX(ctx))
	assert.Equal(t, beforeAudit, f.client.ProcessAuditLog.Query().CountX(ctx))
	assert.Equal(t, beforeHistory, f.client.ProcessExecutionHistory.Query().CountX(ctx))
	assert.Equal(t, beforeCallbacks, f.client.ProcessCallbackOutbox.Query().CountX(ctx))
}

func TestStandardIndependentProcessStartRemainsAvailable(t *testing.T) {
	f := newBPMNAuthorizationFixture(t)
	ctx := f.scopedCtx(true, true, true, true)
	instance, err := f.engine.StartProcess(ctx, f.definition.Key, "independent-standard-case", "", 0, nil)
	require.NoError(t, err)
	require.NotNil(t, instance)
	assert.Equal(t, 1, f.client.ProcessInstance.Query().CountX(ctx))
}

func TestCandidateIndependentInstanceMutationsPreserveState(t *testing.T) {
	for _, action := range []string{"suspend", "resume", "terminate", "variables"} {
		t.Run(action, func(t *testing.T) {
			f := newBPMNAuthorizationFixture(t)
			ctx := f.scopedCtx(true, true, true, true)
			instance := f.createProcessInstance(t, f.tenant, "historical-independent")
			instance = f.client.ProcessInstance.UpdateOne(instance).SetCurrentActivityID("waiting_task").SetCurrentActivityName("Waiting").SaveX(ctx)
			if action == "resume" {
				instance = f.client.ProcessInstance.UpdateOne(instance).SetStatus("suspended").SaveX(ctx)
			}
			policy, err := database.NewExecutionPolicy(config.ExecutionConfig{
				Mode: "candidate", DeploymentID: "bare-process-test",
				Scopes: []config.ExecutionScopeConfig{{TenantID: f.tenant.ID, ScopeID: "f0a0aa2d-b807-4d65-8433-86822245bdd6"}},
			})
			require.NoError(t, err)
			engine := NewCustomProcessEngine(f.client, zap.NewNop().Sugar(), policy)
			beforeAudit := f.client.ProcessAuditLog.Query().CountX(ctx)
			switch action {
			case "suspend":
				err = engine.SuspendProcess(ctx, instance.ProcessInstanceID, "candidate test")
			case "resume":
				err = engine.ResumeProcess(ctx, instance.ProcessInstanceID)
			case "terminate":
				err = engine.TerminateProcess(ctx, instance.ProcessInstanceID, "candidate test")
			case "variables":
				err = engine.ProcessInstanceService().SetProcessInstanceVariables(ctx, instance.ProcessInstanceID, map[string]interface{}{"candidate_note": "must not persist"})
			}
			assert.ErrorIs(t, err, executionscope.ErrDenied)
			after := f.client.ProcessInstance.GetX(ctx, instance.ID)
			assert.Equal(t, instance.Status, after.Status)
			assert.Equal(t, instance.Version, after.Version)
			assert.Equal(t, instance.Variables, after.Variables)
			assert.True(t, instance.UpdatedAt.Equal(after.UpdatedAt), "instance update timestamp must be preserved")
			assert.Equal(t, beforeAudit, f.client.ProcessAuditLog.Query().CountX(ctx))
		})
	}
}
