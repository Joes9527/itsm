package service

import (
	"context"
	"errors"
	"testing"

	"github.com/stretchr/testify/require"
	"itsm-backend/ent"
)

type definitionStarter interface {
	StartProcessByDefinitionID(context.Context, ProcessDefinitionIdentity, string, string, int, map[string]interface{}, string) (*ent.ProcessInstance, error)
}

func TestDefinitionStartReplaysCommittedInstanceAndPinsVersion(t *testing.T) {
	f := newBPMNAuthorizationFixture(t)
	starter, ok := any(f.engine).(definitionStarter)
	require.True(t, ok, "engine must support durable start identity and exact definition")
	ctx := f.scopedCtx(false, false, false, false)
	f.client.ProcessDefinition.UpdateOneID(f.definition.ID).SetIsLatest(false).ExecX(ctx)
	vars := map[string]interface{}{"requester_id": f.actor.ID}
	first, err := starter.StartProcessByDefinitionID(ctx, FreezeProcessDefinition(f.definition), "generic:91", "generic", f.workItem(t, 91).ID, vars, "workflow-start:91:1")
	require.NoError(t, err)
	require.NotNil(t, first.ExecutionWorkItemID)
	require.Equal(t, first.BusinessID, *first.ExecutionWorkItemID)
	// A lost receipt acknowledgement must replay even after the process finishes.
	f.client.ProcessInstance.UpdateOneID(first.ID).SetStatus("completed").ExecX(ctx)
	second, err := starter.StartProcessByDefinitionID(ctx, FreezeProcessDefinition(f.definition), "generic:91", "generic", f.workItem(t, 91).ID, vars, "workflow-start:91:1")
	require.NoError(t, err)
	require.Equal(t, first.ID, second.ID)
	require.Equal(t, f.definition.ID, second.ProcessDefinitionID)
	require.Equal(t, 1, f.client.ProcessInstance.Query().CountX(ctx))
	require.Equal(t, 1, f.client.ProcessAuditLog.Query().CountX(ctx))
}

func TestDefinitionStartRejectsIdentityConflictAndRevokedActor(t *testing.T) {
	f := newBPMNAuthorizationFixture(t)
	starter, ok := any(f.engine).(definitionStarter)
	require.True(t, ok, "engine must support durable start identity and exact definition")
	ctx := f.scopedCtx(false, false, false, false)
	_, err := starter.StartProcessByDefinitionID(ctx, FreezeProcessDefinition(f.definition), "generic:91", "generic", f.workItem(t, 91).ID, nil, "start:one")
	require.NoError(t, err)
	_, err = starter.StartProcessByDefinitionID(ctx, FreezeProcessDefinition(f.definition), "generic:92", "generic", f.workItem(t, 92).ID, nil, "start:one")
	require.Error(t, err)
	f.client.User.UpdateOneID(f.actor.ID).SetActive(false).ExecX(context.Background())
	_, err = starter.StartProcessByDefinitionID(ctx, FreezeProcessDefinition(f.definition), "generic:91", "generic", f.workItem(t, 91).ID, nil, "start:one")
	require.Error(t, err)
	require.Equal(t, 1, f.client.ProcessInstance.Query().CountX(context.Background()))
}

func TestDefinitionStartRejectsChangedContextAfterVariablesAdvance(t *testing.T) {
	f := newBPMNAuthorizationFixture(t)
	ctx := f.scopedCtx(false, false, false, false)
	vars := map[string]interface{}{"title": "original", "requester_id": f.actor.ID}
	first, err := f.engine.StartProcessByDefinitionID(ctx, FreezeProcessDefinition(f.definition), "generic:91", "generic", f.workItem(t, 91).ID, vars, "start:context")
	require.NoError(t, err)
	// Process variables are mutable execution state, not the start request authority.
	f.client.ProcessInstance.UpdateOneID(first.ID).SetVariables(map[string]interface{}{"title": "advanced"}).ExecX(ctx)
	_, err = f.engine.StartProcessByDefinitionID(ctx, FreezeProcessDefinition(f.definition), "generic:91", "generic", f.workItem(t, 91).ID, vars, "start:context")
	require.NoError(t, err)
	_, err = f.engine.StartProcessByDefinitionID(ctx, FreezeProcessDefinition(f.definition), "generic:91", "generic", f.workItem(t, 91).ID, map[string]interface{}{"title": "changed", "requester_id": f.actor.ID}, "start:context")
	require.ErrorContains(t, err, "conflict")
	require.Equal(t, 1, f.client.ProcessInstance.Query().CountX(ctx))
}

func TestDefinitionStartReplaysAfterDefinitionContentChanges(t *testing.T) {
	f := newBPMNAuthorizationFixture(t)
	ctx := startProcessContext(f)
	frozen := FreezeProcessDefinition(f.definition)
	first, err := f.engine.StartProcessByDefinitionID(ctx, frozen, "generic:91", "generic", f.workItem(t, 91).ID, nil, "start:edited")
	require.NoError(t, err)
	f.client.ProcessDefinition.UpdateOneID(f.definition.ID).SetBpmnXML(startProcessUserTaskXML()).SetIsActive(false).ExecX(ctx)
	replay, err := f.engine.StartProcessByDefinitionID(ctx, frozen, "generic:91", "generic", f.workItem(t, 91).ID, nil, "start:edited")
	require.NoError(t, err)
	require.Equal(t, first.ID, replay.ID)
	_, err = f.engine.StartProcessByDefinitionID(ctx, frozen, "generic:92", "generic", f.workItem(t, 92).ID, nil, "start:new")
	require.ErrorContains(t, err, "frozen")
}

func TestDefinitionStartRejectsLegacyInstanceWithoutOriginalDigest(t *testing.T) {
	f := newBPMNAuthorizationFixture(t)
	ctx := startProcessContext(f)
	// Model an existing instance restored without the newly added nullable column.
	f.client.ProcessInstance.Use(func(next ent.Mutator) ent.Mutator {
		return ent.MutateFunc(func(ctx context.Context, mutation ent.Mutation) (ent.Value, error) {
			if m, ok := mutation.(*ent.ProcessInstanceMutation); ok && m.Op() == ent.OpCreate {
				m.ResetStartRequestDigest()
			}
			return next.Mutate(ctx, mutation)
		})
	})
	first, err := f.engine.StartProcessByDefinitionID(ctx, FreezeProcessDefinition(f.definition), "generic:91", "generic", f.workItem(t, 91).ID, nil, "start:legacy")
	require.NoError(t, err)
	require.Empty(t, first.StartRequestDigest)
	_, err = f.engine.StartProcessByDefinitionID(ctx, FreezeProcessDefinition(f.definition), "generic:91", "generic", f.workItem(t, 91).ID, nil, "start:legacy")
	require.ErrorContains(t, err, "conflict")
	require.Equal(t, 1, f.client.ProcessInstance.Query().CountX(ctx))
}

func TestDefinitionStartExecutesAutomaticServiceOnlyAfterCommit(t *testing.T) {
	f := newBPMNAuthorizationFixture(t)
	handler := &startProcessCommitProbeHandler{client: f.client, tenantID: f.tenant.ID, businessKey: "exact-post-commit"}
	f.engine.CallbackRegistry().RegisterHandler(handler)
	configureStartProcessDefinition(t, f, startProcessServiceTaskXML(handler.GetTaskType()))
	definition := f.client.ProcessDefinition.GetX(f.userCtx, f.definition.ID)
	instance, err := f.engine.StartProcessByDefinitionID(startProcessContext(f), FreezeProcessDefinition(definition), handler.businessKey, "generic", f.workItem(t, 104).ID, nil, "start:auto")
	require.NoError(t, err)
	require.True(t, handler.observedCommittedState, "independent client must see committed start/audit before handler execution")
	require.Equal(t, bpmnCallbackStatusCompleted, callbackRowForInstance(t, f, instance.ID).Status)
	require.Equal(t, 1, handler.EffectCount())
	_, err = f.engine.StartProcessByDefinitionID(startProcessContext(f), FreezeProcessDefinition(definition), handler.businessKey, "generic", f.workItem(t, 104).ID, nil, "start:auto")
	require.NoError(t, err)
	require.Equal(t, 1, handler.EffectCount())
}

func TestDefinitionStartRollsBackAutomaticIntentOnAuditFailure(t *testing.T) {
	f := newBPMNAuthorizationFixture(t)
	handler := newCountingIdempotentCallbackHandler("exact_rollback", "exact_rollback_handler", 0)
	f.engine.CallbackRegistry().RegisterHandler(handler)
	configureStartProcessDefinition(t, f, startProcessServiceTaskXML(handler.GetTaskType()))
	definition := f.client.ProcessDefinition.GetX(f.userCtx, f.definition.ID)
	forced := errors.New("forced exact start audit failure")
	failProcessAuditCreation(f.client, forced)
	_, err := f.engine.StartProcessByDefinitionID(startProcessContext(f), FreezeProcessDefinition(definition), "generic:105", "generic", f.workItem(t, 105).ID, nil, "start:rollback")
	require.ErrorIs(t, err, forced)
	assertNoStartedProcessState(t, f)
	require.Zero(t, handler.AttemptCount())
}

func TestDefinitionStartLeavesFailedAutomaticCallbackRecoverable(t *testing.T) {
	f := newBPMNAuthorizationFixture(t)
	handler := newCountingIdempotentCallbackHandler("exact_pending", "exact_pending_handler", 1)
	f.engine.CallbackRegistry().RegisterHandler(handler)
	configureStartProcessDefinition(t, f, startProcessServiceTaskXML(handler.GetTaskType()))
	definition := f.client.ProcessDefinition.GetX(f.userCtx, f.definition.ID)
	instance, err := f.engine.StartProcessByDefinitionID(startProcessContext(f), FreezeProcessDefinition(definition), "generic:106", "generic", f.workItem(t, 106).ID, nil, "start:pending")
	require.NoError(t, err)
	row := callbackRowForInstance(t, f, instance.ID)
	require.Equal(t, bpmnCallbackStatusPending, row.Status)
	require.Equal(t, "handler_error", row.LastErrorClass)
}

func TestDefinitionStartRejectsChangedActorDefinitionAndMissingScope(t *testing.T) {
	f := newBPMNAuthorizationFixture(t)
	ctx := startProcessContext(f)
	frozen := FreezeProcessDefinition(f.definition)
	_, err := f.engine.StartProcessByDefinitionID(ctx, frozen, "generic:91", "generic", f.workItem(t, 91).ID, nil, "start:identity")
	require.NoError(t, err)
	otherActor := WithBPMNAccessScope(context.Background(), BPMNAccessScope{TenantID: f.tenant.ID, UserID: f.outsider.ID})
	_, err = f.engine.StartProcessByDefinitionID(otherActor, frozen, "generic:91", "generic", f.workItem(t, 91).ID, nil, "start:identity")
	require.ErrorContains(t, err, "conflict")
	changed := frozen
	changed.ID++
	_, err = f.engine.StartProcessByDefinitionID(ctx, changed, "generic:91", "generic", f.workItem(t, 91).ID, nil, "start:identity")
	require.ErrorContains(t, err, "conflict")
	_, err = f.engine.StartProcessByDefinitionID(context.Background(), frozen, "generic:91", "generic", f.workItem(t, 91).ID, nil, "start:identity")
	require.Error(t, err)
	require.Equal(t, 1, f.client.ProcessInstance.Query().CountX(ctx))
}

func TestProcessExecutionReferenceExcludesIndependentAndRelease(t *testing.T) {
	for _, business := range []string{"", "release"} {
		t.Run(business, func(t *testing.T) {
			f := newBPMNAuthorizationFixture(t)
			ctx := f.scopedCtx(false, false, false, false)
			id := 0
			if business == "release" {
				id = f.workItem(t, 91).ID
			}
			instance, err := f.engine.StartProcess(ctx, f.definition.Key, "independent-or-release", business, id, nil)
			require.NoError(t, err)
			require.Nil(t, instance.ExecutionWorkItemID)
		})
	}
}
