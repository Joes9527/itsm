package bpmn

import (
	"context"
	"testing"
	"time"

	"itsm-backend/ent"
	"itsm-backend/ent/enttest"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"go.uber.org/zap/zaptest"
)

type releaseDomainRecorder struct {
	command ReleaseWorkflowCommand
	outcome *ReleaseWorkflowMutation
	err     error
}

func (r *releaseDomainRecorder) ApplyReleaseWorkflowCallback(_ context.Context, command ReleaseWorkflowCommand) (*ReleaseWorkflowMutation, error) {
	r.command = command
	return r.outcome, r.err
}

func setupReleaseHandlerFixture(t *testing.T) (*ent.Client, *ReleaseServiceTaskHandler, *releaseDomainRecorder, int, *ent.Release) {
	client := enttest.Open(t, "sqlite3", "file:release_handler_test?mode=memory&cache=shared&_fk=1")
	t.Cleanup(func() { _ = client.Close() })
	ctx := context.Background()
	tenant := client.Tenant.Create().
		SetName("T").SetCode("rh-1").SetDomain("rh-1.com").SetStatus("active").SaveX(ctx)
	creator := client.User.Create().
		SetUsername("creator-rh").SetEmail("creator-rh@test.com").SetPasswordHash("x").
		SetName("发布负责人").SetTenantID(tenant.ID).SetActive(true).SaveX(ctx)
	entity := client.Release.Create().
		SetReleaseNumber("REL-RH-1").SetTitle("测试发布").SetStatus("draft").
		SetCreatedBy(creator.ID).SetTenantID(tenant.ID).SaveX(ctx)
	recorder := &releaseDomainRecorder{outcome: &ReleaseWorkflowMutation{Changed: true, Message: "applied"}}
	handler := NewReleaseServiceTaskHandler(client, zaptest.NewLogger(t).Sugar())
	handler.SetReleaseService(recorder)
	return client, handler, recorder, tenant.ID, entity
}

func TestReleaseHandler_MissingDomainServiceFailsClosed(t *testing.T) {
	client, _, _, tenantID, entity := setupReleaseHandlerFixture(t)
	handler := NewReleaseServiceTaskHandler(client, zaptest.NewLogger(t).Sugar())
	ctx := context.WithValue(context.Background(), BPMNTenantIDContextKey, tenantID)

	result, err := handler.Execute(ctx, nil, map[string]interface{}{
		"action": "schedule", "business_id": entity.ID,
	})
	require.ErrorContains(t, err, "release service is unavailable")
	assert.Nil(t, result)
}

func TestReleaseHandler_ParsesTypedProfessionalCommands(t *testing.T) {
	tests := []struct {
		name        string
		variables   map[string]interface{}
		wantAction  ReleaseWorkflowAction
		wantStatus  string
		wantComment string
	}{
		{name: "technical review", variables: map[string]interface{}{"action": "tech_review", "comment": "reviewed"}, wantAction: ReleaseWorkflowActionTechReview, wantComment: "reviewed"},
		{name: "schedule", variables: map[string]interface{}{"action": "schedule"}, wantAction: ReleaseWorkflowActionStatus, wantStatus: "scheduled"},
		{name: "execute", variables: map[string]interface{}{"action": "execute"}, wantAction: ReleaseWorkflowActionStatus, wantStatus: "in-progress"},
		{name: "verify", variables: map[string]interface{}{"action": "verify"}, wantAction: ReleaseWorkflowActionStatus, wantStatus: "completed"},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			_, handler, recorder, tenantID, entity := setupReleaseHandlerFixture(t)
			ctx := context.WithValue(context.Background(), BPMNTenantIDContextKey, tenantID)
			variables := tc.variables
			variables["business_id"] = entity.ID

			result, err := handler.Execute(ctx, nil, variables)
			require.NoError(t, err)
			require.Equal(t, CallbackEffectApplied, result.Status)
			assert.Equal(t, entity.ID, recorder.command.ReleaseID)
			assert.Equal(t, tenantID, recorder.command.TenantID)
			assert.Equal(t, tc.wantAction, recorder.command.Action)
			assert.Equal(t, tc.wantStatus, recorder.command.TargetStatus)
			assert.Equal(t, tc.wantComment, recorder.command.Comment)
		})
	}
}

func releaseApprovalTaskFixture(t *testing.T, client *ent.Client, tenantID, actorID int, action string) *ent.ProcessTask {
	t.Helper()
	ctx := context.Background()
	deployment := client.ProcessDeployment.Create().
		SetDeploymentID("release-" + action + "-deployment").SetDeploymentName("release " + action).
		SetDeploymentTime(time.Now()).SetDeployedBy("test").SetIsActive(true).SetTenantID(tenantID).SaveX(ctx)
	definition := client.ProcessDefinition.Create().
		SetKey("release-" + action).SetName("release " + action).SetVersion("1").SetIsLatest(true).
		SetBpmnXML([]byte("<definitions/> ")).SetDeploymentID(deployment.ID).SetDeployedAt(time.Now()).SetTenantID(tenantID).SaveX(ctx)
	instance := client.ProcessInstance.Create().
		SetProcessInstanceID("release-" + action + "-instance").SetProcessDefinitionKey(definition.Key).
		SetProcessDefinitionID(definition.ID).SetStatus("running").SetVariables(map[string]interface{}{}).SetTenantID(tenantID).SaveX(ctx)
	task := client.ProcessTask.Create().
		SetTaskID("release-" + action + "-task").SetProcessInstanceID(instance.ID).SetProcessDefinitionKey(definition.Key).
		SetTaskDefinitionKey("Activity_ReleaseApproval").SetTaskName("发布审批").SetTaskType("user_task").
		SetStatus("completed").SetTaskVariables(map[string]interface{}{"approvalAction": action}).SetTenantID(tenantID).SaveX(ctx)
	client.ProcessApprovalDecision.Create().
		SetProcessInstanceID(instance.ID).SetProcessTaskID(task.ID).
		SetProcessInstanceKey(instance.ProcessInstanceID).SetTaskID(task.TaskID).
		SetProcessDefinitionKey(definition.Key).SetNodeKey(task.TaskDefinitionKey).
		SetActorID(actorID).SetAction(action).SetDecision(map[string]string{"approve": "approved", "reject": "rejected"}[action]).
		SetTenantID(tenantID).SaveX(ctx)
	return task
}

func TestReleaseHandler_ApprovalRequiresMatchingPersistedDecision(t *testing.T) {
	client, handler, recorder, tenantID, entity := setupReleaseHandlerFixture(t)
	ctx := context.WithValue(context.Background(), BPMNTenantIDContextKey, tenantID)
	actor := client.User.Query().OnlyX(ctx)
	task := releaseApprovalTaskFixture(t, client, tenantID, actor.ID, "approve")
	client.ProcessApprovalDecision.Delete().ExecX(ctx)

	missing, err := handler.Execute(ctx, task, map[string]interface{}{"action": "approval", "business_id": entity.ID})
	require.NoError(t, err)
	require.Equal(t, CallbackEffectBlocked, missing.Status)

	client.ProcessApprovalDecision.Create().
		SetProcessInstanceID(task.ProcessInstanceID).SetProcessTaskID(task.ID).
		SetProcessInstanceKey("release-approve-instance").SetTaskID(task.TaskID).
		SetProcessDefinitionKey(task.ProcessDefinitionKey).SetNodeKey(task.TaskDefinitionKey).
		SetActorID(actor.ID).SetAction("approve").SetDecision("approved").SetTenantID(tenantID).SaveX(ctx)
	task.TaskVariables["approvalAction"] = "reject"
	mismatched, err := handler.Execute(ctx, task, map[string]interface{}{"action": "approval", "business_id": entity.ID})
	require.NoError(t, err)
	require.Equal(t, CallbackEffectBlocked, mismatched.Status)

	task.TaskVariables["approvalAction"] = "approve"
	matched, err := handler.Execute(ctx, task, map[string]interface{}{"action": "approval", "business_id": entity.ID})
	require.NoError(t, err)
	require.Equal(t, CallbackEffectIdempotent, matched.Status)
	assert.Zero(t, recorder.command.ReleaseID, "approval success must not mutate professional state before scheduling")
}

func TestReleaseHandler_RejectDecisionUsesDomainService(t *testing.T) {
	client, handler, recorder, tenantID, entity := setupReleaseHandlerFixture(t)
	ctx := context.WithValue(context.Background(), BPMNTenantIDContextKey, tenantID)
	actor := client.User.Query().OnlyX(ctx)
	task := releaseApprovalTaskFixture(t, client, tenantID, actor.ID, "reject")

	result, err := handler.Execute(ctx, task, map[string]interface{}{"action": "approval", "business_id": entity.ID})
	require.NoError(t, err)
	require.Equal(t, CallbackEffectApplied, result.Status)
	assert.Equal(t, ReleaseWorkflowActionReject, recorder.command.Action)
}
