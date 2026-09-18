package service

import (
	"context"
	"errors"
	"testing"

	"github.com/stretchr/testify/require"
	"itsm-backend/ent"
)

// These cover the completion command surface in generic_workflow_completion.go:
// the note requirement, the reserved branch inputs it refuses to accept from a
// caller, and the approval intent that only the decision boundary may mint.

func TestGenericWorkflowCompletionRequiresNoteWithoutGlobalPropagation(t *testing.T) {
	f, _, task := seedGenericGate(t, "in_progress", "in_progress")
	f.client.ProcessTask.UpdateOne(task).SetCandidateUsers(f.actor.Email).SaveX(f.userCtx)
	ctx := f.typedTaskScopeOnlyCtx(f.actor, false)
	before := f.client.ProcessInstance.GetX(f.userCtx, task.ProcessInstanceID)
	err := f.engine.CompleteTask(ctx, task.TaskID, nil)
	require.Error(t, err)
	require.NotEqual(t, "completed", f.client.ProcessTask.GetX(f.userCtx, task.ID).Status)
	require.Equal(t, before.Version, f.client.ProcessInstance.GetX(f.userCtx, task.ProcessInstanceID).Version)
	err = f.engine.CompleteTask(ctx, task.TaskID, map[string]interface{}{WorkItemCompletionNote: "  verified handling  "})
	require.NoError(t, err)
	saved := f.client.ProcessTask.GetX(f.userCtx, task.ID)
	require.Equal(t, "completed", saved.Status)
	require.Equal(t, "verified handling", saved.TaskVariables[WorkItemCompletionNote])
	require.NotContains(t, f.client.ProcessInstance.GetX(f.userCtx, task.ProcessInstanceID).Variables, WorkItemCompletionNote)
}

func TestGenericWorkflowCompletionRejectsReservedBranchInputs(t *testing.T) {
	for _, key := range []string{"approval_required", "need_escalate", "approvalResult"} {
		t.Run(key, func(t *testing.T) {
			f, _, task := seedGenericGate(t, "assigned", "open")
			f.client.ProcessTask.UpdateOne(task).SetCandidateUsers(f.actor.Email).SaveX(f.userCtx)
			err := f.engine.CompleteTask(f.typedTaskScopeOnlyCtx(f.actor, false), task.TaskID, map[string]interface{}{key: false})
			require.Error(t, err)
			require.NotEqual(t, "completed", f.client.ProcessTask.GetX(f.userCtx, task.ID).Status)
		})
	}
}

func TestGenericWorkflowTaskSetVariablesRejectsReservedInputs(t *testing.T) {
	for _, key := range []string{"approval_required", "need_escalate", "approvalResult", WorkItemCompletionNote} {
		t.Run(key, func(t *testing.T) {
			f, _, task := seedGenericGate(t, "in_progress", "in_progress")
			f.client.ProcessTask.UpdateOne(task).SetCandidateUsers(f.actor.Email).SaveX(f.userCtx)
			before := f.client.ProcessTask.GetX(f.userCtx, task.ID)
			err := f.engine.TaskService().SetTaskVariables(f.typedTaskScopeOnlyCtx(f.actor, false), task.TaskID, map[string]interface{}{key: "injected"})
			require.Error(t, err)
			after := f.client.ProcessTask.GetX(f.userCtx, task.ID)
			require.Equal(t, before.TaskVariables, after.TaskVariables)
			require.Equal(t, before.AggregationVersion, after.AggregationVersion)
		})
	}
}

func TestGenericWorkflowApprovalIntentUsesDecisionBoundary(t *testing.T) {
	for _, action := range []string{"approve", "reject"} {
		t.Run(action, func(t *testing.T) {
			f, item, task := seedGenericGate(t, "assigned", "open")
			instance := f.client.ProcessInstance.GetX(f.userCtx, task.ProcessInstanceID)
			xml := []byte(`<definitions><process id="approval-contract" isExecutable="true">
 <extensionElements><metaData name="workItemLifecycleContract">generic_fulfillment_v1</metaData></extensionElements>
 <startEvent id="start"/><userTask id="work" taskPurpose="approval"/><exclusiveGateway id="decision"/>
 <userTask id="handle" name="Handle" taskPurpose="fulfillment" assigneeSource="work_item_assignee"><extensionElements><metaData name="workItemPrerequisite">in_progress</metaData></extensionElements></userTask>
 <endEvent id="rejected"/><endEvent id="done"/>
 <sequenceFlow id="first" sourceRef="start" targetRef="work"/><sequenceFlow id="decide" sourceRef="work" targetRef="decision"/>
 <sequenceFlow id="approved" sourceRef="decision" targetRef="handle"><conditionExpression><![CDATA[variables['approvalResult'] == 'approved']]></conditionExpression></sequenceFlow>
 <sequenceFlow id="denied" sourceRef="decision" targetRef="rejected"><conditionExpression><![CDATA[variables['approvalResult'] == 'rejected']]></conditionExpression></sequenceFlow>
 <sequenceFlow id="last" sourceRef="handle" targetRef="done"/></process></definitions>`)
			f.client.ProcessDefinition.UpdateOneID(instance.ProcessDefinitionID).SetBpmnXML(xml).SetProcessVariables(map[string]interface{}{"approval_required": true}).SaveX(f.userCtx)
			f.client.ProcessTask.UpdateOne(task).SetCandidateUsers(f.actor.Email).SaveX(f.userCtx)
			ctx := f.typedTaskScopeOnlyCtx(f.actor, false)
			vars := map[string]interface{}{"approvalAction": action, "approvalResult": map[string]string{"approve": "approved", "reject": "rejected"}[action]}
			require.Error(t, f.engine.CompleteTask(ctx, task.TaskID, vars), "plain completion cannot mint an approval decision")
			trusted := WithBPMNApprovalDecisionIntent(ctx, task.TaskID, action, nil)
			require.NoError(t, f.engine.CompleteTask(trusted, task.TaskID, vars))
			decision := f.client.ProcessApprovalDecision.Query().OnlyX(f.userCtx)
			require.Equal(t, vars["approvalResult"], decision.Decision)
			gate, err := loadGenericWorkflowGate(f.userCtx, f.client, item.TenantID, item.ID, false)
			require.NoError(t, err)
			if action == "approve" {
				require.True(t, gate.facts.Approved)
				require.Equal(t, WorkItemPrerequisiteInProgress, gate.facts.Waiting)
			} else {
				require.False(t, gate.facts.Running)
				require.NotEqual(t, WorkItemPrerequisiteInProgress, gate.facts.Waiting)
				tx, err := f.client.Tx(f.userCtx)
				require.NoError(t, err)
				defer tx.Rollback()
				require.Error(t, EnforceGenericWorkflowTransitionTx(f.userCtx, tx.Client(), item.TenantID, item.ID, "in_progress"))
			}
		})
	}
}

func TestGenericWorkflowApprovalAuditFailureRollsBackCompletion(t *testing.T) {
	f, _, task := seedGenericGate(t, "assigned", "open")
	instance := f.client.ProcessInstance.GetX(f.userCtx, task.ProcessInstanceID)
	xml := lifecycleXML(lifecycleMetadata("workItemLifecycleContract", "generic_fulfillment_v1"), "", `taskPurpose="approval"`, "")
	f.client.ProcessDefinition.UpdateOneID(instance.ProcessDefinitionID).SetBpmnXML(xml).SaveX(f.userCtx)
	f.client.ProcessTask.UpdateOne(task).SetCandidateUsers(f.actor.Email).SaveX(f.userCtx)
	ctx := f.typedTaskScopeOnlyCtx(f.actor, false)
	vars := map[string]interface{}{"approvalAction": "approve", "approvalResult": "approved"}
	forged := WithBPMNApprovalDecisionIntent(ctx, task.TaskID, "approve", map[string]interface{}{"approvalResult": "approved"})
	require.Error(t, f.engine.CompleteTask(forged, task.TaskID, vars))
	f.client.Use(func(next ent.Mutator) ent.Mutator {
		return ent.MutateFunc(func(ctx context.Context, m ent.Mutation) (ent.Value, error) {
			if _, ok := m.(*ent.ProcessApprovalDecisionMutation); ok {
				return nil, errors.New("approval audit unavailable")
			}
			return next.Mutate(ctx, m)
		})
	})
	trusted := WithBPMNApprovalDecisionIntent(ctx, task.TaskID, "approve", nil)
	require.Error(t, f.engine.CompleteTask(trusted, task.TaskID, vars))
	require.NotEqual(t, "completed", f.client.ProcessTask.GetX(f.userCtx, task.ID).Status)
	saved := f.client.ProcessInstance.GetX(f.userCtx, instance.ID)
	require.Equal(t, instance.Version, saved.Version)
	require.Equal(t, instance.Status, saved.Status)
	require.Zero(t, f.client.ProcessApprovalDecision.Query().CountX(f.userCtx))
}
