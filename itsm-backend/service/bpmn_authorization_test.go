package service

import (
	"context"
	"strconv"
	"testing"
	"time"

	"itsm-backend/common"
	"itsm-backend/ent"
	"itsm-backend/ent/enttest"
	"itsm-backend/service/bpmn"

	_ "github.com/mattn/go-sqlite3"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"go.uber.org/zap"
)

type bpmnAuthorizationFixture struct {
	client      *ent.Client
	engine      *CustomProcessEngine
	resolver    *bpmnParticipationResolver
	userCtx     context.Context
	tenant      *ent.Tenant
	otherTenant *ent.Tenant
	actor       *ent.User
	outsider    *ent.User
	otherActor  *ent.User
	definition  *ent.ProcessDefinition
}

func newBPMNAuthorizationFixture(t *testing.T) *bpmnAuthorizationFixture {
	t.Helper()
	ctx := context.Background()
	client := enttest.Open(t, "sqlite3", testDSN())
	t.Cleanup(func() { require.NoError(t, client.Close()) })

	tenant, err := client.Tenant.Create().
		SetName("BPMN authorization tenant").
		SetCode("bpmn-auth").
		SetStatus("active").
		Save(ctx)
	require.NoError(t, err)
	otherTenant, err := client.Tenant.Create().
		SetName("Other BPMN authorization tenant").
		SetCode("bpmn-auth-other").
		SetStatus("active").
		Save(ctx)
	require.NoError(t, err)

	additionalRole, err := client.Role.Create().
		SetName("Network engineering").
		SetCode("network_eng").
		SetTenantID(tenant.ID).
		Save(ctx)
	require.NoError(t, err)

	actor, err := client.User.Create().
		SetUsername("bpmn.actor").
		SetEmail("bpmn.actor@example.test").
		SetName("BPMN Actor").
		SetPasswordHash("test").
		SetRole("service_agent").
		SetActive(true).
		SetTenantID(tenant.ID).
		AddRoleIDs(additionalRole.ID).
		Save(ctx)
	require.NoError(t, err)
	outsider, err := client.User.Create().
		SetUsername("bpmn.outsider").
		SetEmail("bpmn.outsider@example.test").
		SetName("BPMN Outsider").
		SetPasswordHash("test").
		SetRole("service_agent").
		SetActive(true).
		SetTenantID(tenant.ID).
		Save(ctx)
	require.NoError(t, err)
	otherActor, err := client.User.Create().
		SetUsername("bpmn.other.actor").
		SetEmail("bpmn.other.actor@example.test").
		SetName("Other BPMN Actor").
		SetPasswordHash("test").
		SetRole("service_agent").
		SetActive(true).
		SetTenantID(otherTenant.ID).
		Save(ctx)
	require.NoError(t, err)

	_, err = client.Group.Create().
		SetName("vpn-operators").
		SetTenantID(tenant.ID).
		AddMemberIDs(actor.ID).
		Save(ctx)
	require.NoError(t, err)

	deployment, err := client.ProcessDeployment.Create().
		SetDeploymentID("deployment-authorization-start").
		SetDeploymentName("Authorization Start").
		SetTenantID(tenant.ID).
		Save(ctx)
	require.NoError(t, err)
	definition, err := client.ProcessDefinition.Create().
		SetKey("authorization_start").
		SetName("Authorization Start").
		SetBpmnXML([]byte(`<?xml version="1.0" encoding="UTF-8"?>
<bpmn:definitions xmlns:bpmn="http://www.omg.org/spec/BPMN/20100524/MODEL" targetNamespace="http://bpmn.io/schema/bpmn">
  <bpmn:process id="authorization_start" isExecutable="true">
    <bpmn:startEvent id="Start_1" name="Start" />
    <bpmn:endEvent id="End_1" name="End" />
    <bpmn:sequenceFlow id="Flow_1" sourceRef="Start_1" targetRef="End_1" />
  </bpmn:process>
</bpmn:definitions>`)).
		SetDeploymentID(deployment.ID).
		SetTenantID(tenant.ID).
		SetIsActive(true).
		SetIsLatest(true).
		Save(ctx)
	require.NoError(t, err)
	engine := NewCustomProcessEngine(client, zap.NewNop().Sugar()).(*CustomProcessEngine)

	return &bpmnAuthorizationFixture{
		client:      client,
		engine:      engine,
		resolver:    &bpmnParticipationResolver{client: client, groupResolver: bpmn.NewGroupResolver(client)},
		userCtx:     ctx,
		tenant:      tenant,
		otherTenant: otherTenant,
		actor:       actor,
		outsider:    outsider,
		otherActor:  otherActor,
		definition:  definition,
	}
}

func (f *bpmnAuthorizationFixture) scopedCtx(canReadInstances, canUpdateInstances, canReadTasks, canUpdateTasks bool) context.Context {
	return WithBPMNAccessScope(f.userCtx, BPMNAccessScope{
		UserID:                f.actor.ID,
		TenantID:              f.tenant.ID,
		CanReadAllInstances:   canReadInstances,
		CanUpdateAllInstances: canUpdateInstances,
		CanReadAllTasks:       canReadTasks,
		CanUpdateAllTasks:     canUpdateTasks,
	})
}

func (f *bpmnAuthorizationFixture) actorScopeCtx(actor *ent.User, tenant *ent.Tenant, canReadAll bool) context.Context {
	ctx := context.WithValue(f.userCtx, bpmn.BPMNTenantIDContextKey, tenant.ID)
	return WithBPMNAccessScope(ctx, BPMNAccessScope{
		UserID:              actor.ID,
		TenantID:            tenant.ID,
		CanReadAllInstances: canReadAll,
	})
}

func requireBPMNForbidden(t *testing.T, err error) {
	t.Helper()
	var appErr *common.AppError
	require.ErrorAs(t, err, &appErr)
	assert.Equal(t, common.ErrCodeForbidden, appErr.Code)
}

func TestParticipationResolverMatchesExactIdentityAndGroups(t *testing.T) {
	f := newBPMNAuthorizationFixture(t)
	actor, err := f.resolver.resolveActor(f.userCtx, BPMNAccessScope{UserID: f.actor.ID, TenantID: f.tenant.ID})
	require.NoError(t, err)

	assert.True(t, f.resolver.matchesTask(&ent.ProcessTask{TenantID: f.tenant.ID, Assignee: strconv.Itoa(f.actor.ID)}, actor))
	assert.True(t, f.resolver.matchesTask(&ent.ProcessTask{TenantID: f.tenant.ID, CandidateUsers: f.actor.Username + ", someone"}, actor))
	assert.True(t, f.resolver.matchesTask(&ent.ProcessTask{TenantID: f.tenant.ID, CandidateUsers: f.actor.Email}, actor))
	assert.True(t, f.resolver.matchesTask(&ent.ProcessTask{TenantID: f.tenant.ID, CandidateGroups: "service_agent"}, actor))
	assert.True(t, f.resolver.matchesTask(&ent.ProcessTask{TenantID: f.tenant.ID, CandidateGroups: "NETWORK_ENG"}, actor))
	assert.True(t, f.resolver.matchesTask(&ent.ProcessTask{TenantID: f.tenant.ID, CandidateGroups: "other, vpn-operators"}, actor))
	assert.False(t, f.resolver.matchesTask(&ent.ProcessTask{TenantID: f.tenant.ID, CandidateUsers: strconv.Itoa(f.actor.ID) + "1"}, actor))
	assert.False(t, f.resolver.matchesTask(&ent.ProcessTask{TenantID: f.tenant.ID, CandidateUsers: "prefix-" + f.actor.Username}, actor))
	assert.False(t, f.resolver.matchesTask(&ent.ProcessTask{TenantID: f.otherTenant.ID, Assignee: strconv.Itoa(f.actor.ID)}, actor))
}

func TestParticipationResolverResolveActorFailsClosed(t *testing.T) {
	f := newBPMNAuthorizationFixture(t)

	_, err := f.resolver.resolveActor(f.userCtx, BPMNAccessScope{UserID: f.actor.ID, TenantID: f.otherTenant.ID})
	require.Error(t, err)

	inactive, err := f.client.User.Create().
		SetUsername("inactive.bpmn.actor").
		SetEmail("inactive.bpmn.actor@example.test").
		SetName("Inactive BPMN Actor").
		SetPasswordHash("test").
		SetActive(false).
		SetTenantID(f.tenant.ID).
		Save(f.userCtx)
	require.NoError(t, err)

	_, err = f.resolver.resolveActor(f.userCtx, BPMNAccessScope{UserID: inactive.ID, TenantID: f.tenant.ID})
	require.Error(t, err)
}

func TestParticipationResolverParticipatingInstanceIDsExactAndDeduplicated(t *testing.T) {
	f := newBPMNAuthorizationFixture(t)
	actor, err := f.resolver.resolveActor(f.userCtx, BPMNAccessScope{UserID: f.actor.ID, TenantID: f.tenant.ID})
	require.NoError(t, err)

	first := f.createProcessInstance(t, f.tenant, "first")
	second := f.createProcessInstance(t, f.tenant, "second")
	other := f.createProcessInstance(t, f.otherTenant, "other")
	f.createProcessTask(t, first, f.tenant.ID, "actor-id", strconv.Itoa(f.actor.ID), "", "")
	f.createProcessTask(t, first, f.tenant.ID, "actor-group", "", "", "vpn-operators")
	f.createProcessTask(t, second, f.tenant.ID, "actor-role", "", "", "network_eng")
	f.createProcessTask(t, second, f.tenant.ID, "id-prefix-only", "", strconv.Itoa(f.actor.ID)+"1", "")
	f.createProcessTask(t, other, f.otherTenant.ID, "other-tenant", strconv.Itoa(f.actor.ID), "", "")
	f.createProcessTask(t, other, f.tenant.ID, "cross-tenant-instance", strconv.Itoa(f.actor.ID), "", "")

	ids, err := f.resolver.participatingInstanceIDs(f.userCtx, actor)
	require.NoError(t, err)
	assert.ElementsMatch(t, []int{first.ID, second.ID}, ids)
}

func TestStartProcessPersistsAuthenticatedInitiator(t *testing.T) {
	f := newBPMNAuthorizationFixture(t)
	ctx := context.WithValue(f.userCtx, bpmn.BPMNTenantIDContextKey, f.tenant.ID)
	ctx = context.WithValue(ctx, bpmn.BPMNUserIDContextKey, f.actor.ID)
	instance, err := f.engine.StartProcess(ctx, f.definition.Key, "ticket-1", "ticket", 1, map[string]interface{}{
		"requester_id": f.outsider.ID,
	})
	require.NoError(t, err)
	assert.Equal(t, strconv.Itoa(f.actor.ID), instance.Initiator)
}

func TestStartProcessUsesTrustedRequesterFallback(t *testing.T) {
	f := newBPMNAuthorizationFixture(t)
	ctx := context.WithValue(f.userCtx, bpmn.BPMNTenantIDContextKey, f.tenant.ID)
	instance, err := f.engine.StartProcess(ctx, f.definition.Key, "ticket-2", "ticket", 2, map[string]interface{}{
		"requester_id": float64(f.actor.ID),
	})
	require.NoError(t, err)
	assert.Equal(t, strconv.Itoa(f.actor.ID), instance.Initiator)
}

func TestStartProcessUsesRequesterFallbackForZeroActor(t *testing.T) {
	f := newBPMNAuthorizationFixture(t)
	ctx := context.WithValue(f.userCtx, bpmn.BPMNTenantIDContextKey, f.tenant.ID)
	ctx = context.WithValue(ctx, bpmn.BPMNUserIDContextKey, 0)
	instance, err := f.engine.StartProcess(ctx, f.definition.Key, "ticket-3", "ticket", 3, map[string]interface{}{
		"requesterId": f.actor.ID,
	})
	require.NoError(t, err)
	assert.Equal(t, strconv.Itoa(f.actor.ID), instance.Initiator)
}

func TestListProcessInstancesScopesParticipantAndElevatedReader(t *testing.T) {
	f := newBPMNAuthorizationFixture(t)
	visible, hidden := f.seedParticipantAndNonParticipantInstances(t)

	rows, total, err := f.engine.ProcessInstanceService().ListProcessInstances(
		f.actorScopeCtx(f.actor, f.tenant, false),
		&ListProcessInstancesRequest{TenantID: f.tenant.ID, Page: 1, PageSize: 1},
	)
	require.NoError(t, err)
	require.Equal(t, 1, total)
	require.Len(t, rows, 1)
	assert.Equal(t, visible.ID, rows[0].ID)

	rows, elevatedTotal, err := f.engine.ProcessInstanceService().ListProcessInstances(
		f.actorScopeCtx(f.actor, f.tenant, true),
		&ListProcessInstancesRequest{TenantID: f.tenant.ID, Page: 1, PageSize: 20},
	)
	require.NoError(t, err)
	assert.Equal(t, 2, elevatedTotal)
	require.Len(t, rows, 2)
	assert.ElementsMatch(t, []int{visible.ID, hidden.ID}, []int{rows[0].ID, rows[1].ID})

	_, _, err = f.engine.ProcessInstanceService().ListProcessInstances(
		f.actorScopeCtx(f.actor, f.tenant, true),
		&ListProcessInstancesRequest{TenantID: f.otherTenant.ID, Page: 1, PageSize: 20},
	)
	requireBPMNForbidden(t, err)
}

func TestGetProcessInstanceAuthorization(t *testing.T) {
	f := newBPMNAuthorizationFixture(t)
	instance := f.createAuthorizedReadInstance(t)
	instanceID := strconv.Itoa(instance.ID)

	got, err := f.engine.ProcessInstanceService().GetProcessInstance(f.actorScopeCtx(f.actor, f.tenant, false), instanceID)
	require.NoError(t, err)
	assert.Equal(t, instance.ID, got.ID)

	_, err = f.engine.ProcessInstanceService().GetProcessInstance(f.actorScopeCtx(f.outsider, f.tenant, false), instanceID)
	requireBPMNForbidden(t, err)

	_, err = f.engine.ProcessInstanceService().GetProcessInstance(f.actorScopeCtx(f.otherActor, f.otherTenant, false), instanceID)
	require.Error(t, err)
	assert.True(t, ent.IsNotFound(err), "cross-tenant lookup must be indistinguishable from absence: %v", err)
}

func TestGetProcessInstanceVariablesAuthorization(t *testing.T) {
	f := newBPMNAuthorizationFixture(t)
	instance := f.createAuthorizedReadInstance(t)
	instanceID := strconv.Itoa(instance.ID)

	variables, err := f.engine.ProcessInstanceService().GetProcessInstanceVariables(f.actorScopeCtx(f.actor, f.tenant, false), instanceID)
	require.NoError(t, err)
	assert.Equal(t, "visible", variables["classification"])

	_, err = f.engine.ProcessInstanceService().GetProcessInstanceVariables(f.actorScopeCtx(f.outsider, f.tenant, false), instanceID)
	requireBPMNForbidden(t, err)

	_, err = f.engine.ProcessInstanceService().GetProcessInstanceVariables(f.actorScopeCtx(f.otherActor, f.otherTenant, false), instanceID)
	require.Error(t, err)
	assert.True(t, ent.IsNotFound(err))
}

func TestGetProcessInstanceHistoryAuthorization(t *testing.T) {
	f := newBPMNAuthorizationFixture(t)
	instance := f.createAuthorizedReadInstance(t)
	_, err := f.client.ProcessExecutionHistory.Create().
		SetHistoryID("authorization-history").
		SetProcessInstanceID(instance.ID).
		SetProcessDefinitionKey(instance.ProcessDefinitionKey).
		SetActivityID("Start_1").
		SetActivityName("Start").
		SetActivityType("start_event").
		SetEventType("start").
		SetTenantID(f.tenant.ID).
		SetTimestamp(time.Now()).
		Save(f.userCtx)
	require.NoError(t, err)
	instanceID := strconv.Itoa(instance.ID)

	history, err := f.engine.ProcessInstanceService().GetProcessInstanceHistory(f.actorScopeCtx(f.actor, f.tenant, false), instanceID)
	require.NoError(t, err)
	require.Len(t, history, 1)
	assert.Equal(t, "authorization-history", history[0].HistoryID)

	_, err = f.engine.ProcessInstanceService().GetProcessInstanceHistory(f.actorScopeCtx(f.outsider, f.tenant, false), instanceID)
	requireBPMNForbidden(t, err)

	_, err = f.engine.ProcessInstanceService().GetProcessInstanceHistory(f.actorScopeCtx(f.otherActor, f.otherTenant, false), instanceID)
	require.Error(t, err)
	assert.True(t, ent.IsNotFound(err))
}

func TestListApprovalDecisionsAuthorization(t *testing.T) {
	f := newBPMNAuthorizationFixture(t)
	instance := f.createAuthorizedReadInstance(t)
	_, err := f.client.ProcessApprovalDecision.Create().
		SetProcessInstanceID(instance.ID).
		SetProcessTaskID(1).
		SetProcessInstanceKey(instance.ProcessInstanceID).
		SetTaskID("approval-task").
		SetProcessDefinitionKey(instance.ProcessDefinitionKey).
		SetNodeKey("approval").
		SetActorID(f.actor.ID).
		SetAction("approve").
		SetDecision("approved").
		SetTenantID(f.tenant.ID).
		Save(f.userCtx)
	require.NoError(t, err)

	decisions, err := f.engine.TaskService().ListApprovalDecisions(f.actorScopeCtx(f.actor, f.tenant, false), instance.ProcessInstanceID)
	require.NoError(t, err)
	require.Len(t, decisions, 1)
	numericDecisions, err := f.engine.TaskService().ListApprovalDecisions(f.actorScopeCtx(f.actor, f.tenant, false), strconv.Itoa(instance.ID))
	require.NoError(t, err)
	require.Len(t, numericDecisions, 1)
	assert.Equal(t, decisions[0].ID, numericDecisions[0].ID)

	_, err = f.engine.TaskService().ListApprovalDecisions(f.actorScopeCtx(f.outsider, f.tenant, false), instance.ProcessInstanceID)
	requireBPMNForbidden(t, err)

	_, err = f.engine.TaskService().ListApprovalDecisions(f.actorScopeCtx(f.otherActor, f.otherTenant, false), instance.ProcessInstanceID)
	require.Error(t, err)
	assert.True(t, ent.IsNotFound(err))
}

func TestInstanceStatisticsAuthorization(t *testing.T) {
	f := newBPMNAuthorizationFixture(t)
	f.createProcessInstance(t, f.tenant, "statistics-visible")
	f.createProcessInstance(t, f.otherTenant, "statistics-other")

	_, err := f.engine.ProcessInstanceService().GetInstanceStatistics(
		f.actorScopeCtx(f.actor, f.tenant, false),
		&InstanceStatisticsRequest{TenantID: f.tenant.ID},
	)
	requireBPMNForbidden(t, err)

	req := &InstanceStatisticsRequest{TenantID: f.otherTenant.ID}
	stats, err := f.engine.ProcessInstanceService().GetInstanceStatistics(f.actorScopeCtx(f.actor, f.tenant, true), req)
	require.NoError(t, err)
	assert.Equal(t, 1, stats.Total)
	assert.Equal(t, f.tenant.ID, req.TenantID)
}

func TestListUserTasksForcesCallerScopeWithoutTaskRead(t *testing.T) {
	f := newBPMNAuthorizationFixture(t)
	mine, _ := f.seedMineAndOtherTasks(t)
	req := &ListUserTasksRequest{
		TenantID:       f.otherTenant.ID,
		UserID:         f.otherActor.ID,
		Assignee:       strconv.Itoa(f.outsider.ID),
		CandidateUsers: f.outsider.Username,
		Page:           1,
		PageSize:       20,
	}

	rows, total, err := f.engine.TaskService().ListUserTasks(f.scopedCtx(false, false, false, false), req)
	require.NoError(t, err)
	require.Equal(t, 1, total)
	require.Len(t, rows, 1)
	assert.Equal(t, mine.TaskID, rows[0].TaskID)
}

func TestGetTaskRejectsSameTenantNonParticipant(t *testing.T) {
	f := newBPMNAuthorizationFixture(t)
	_, other := f.seedMineAndOtherTasks(t)

	_, err := f.engine.TaskService().GetTask(f.scopedCtx(false, false, false, false), other.TaskID)
	requireBPMNForbidden(t, err)
	_, err = f.engine.TaskService().GetTaskByID(f.scopedCtx(false, false, false, false), other.ID)
	requireBPMNForbidden(t, err)
}

func TestGetTaskRejectsCrossTenant(t *testing.T) {
	f := newBPMNAuthorizationFixture(t)
	instance := f.createProcessInstance(t, f.otherTenant, "cross-tenant-task-read")
	task := f.createProcessTask(t, instance, f.otherTenant.ID, "cross-tenant-task-read", strconv.Itoa(f.otherActor.ID), "", "")

	_, err := f.engine.TaskService().GetTask(f.scopedCtx(false, false, true, false), task.TaskID)
	require.Error(t, err)
	assert.True(t, ent.IsNotFound(err), "cross-tenant task lookup must be indistinguishable from absence: %v", err)
	assert.NotContains(t, err.Error(), task.TaskName)
}

func TestGetTaskAllowsElevatedReader(t *testing.T) {
	f := newBPMNAuthorizationFixture(t)
	_, other := f.seedMineAndOtherTasks(t)

	got, err := f.engine.TaskService().GetTask(f.scopedCtx(false, false, true, false), other.TaskID)
	require.NoError(t, err)
	assert.Equal(t, other.ID, got.ID)
	got, err = f.engine.TaskService().GetTaskByID(f.scopedCtx(false, false, true, false), other.ID)
	require.NoError(t, err)
	assert.Equal(t, other.ID, got.ID)
}

func TestTaskCandidateMatchingDoesNotMatchSubstring(t *testing.T) {
	f := newBPMNAuthorizationFixture(t)
	instance := f.createProcessInstance(t, f.tenant, "substring-task-read")
	task := f.createProcessTask(t, instance, f.tenant.ID, "substring-task-read", "", strconv.Itoa(f.actor.ID)+"1", "")

	_, err := f.engine.TaskService().GetTask(f.scopedCtx(false, false, false, false), task.TaskID)
	requireBPMNForbidden(t, err)
}

func TestGetTaskVariablesAuthorization(t *testing.T) {
	f := newBPMNAuthorizationFixture(t)
	_, other := f.seedMineAndOtherTasks(t)
	other, err := f.client.ProcessTask.UpdateOne(other).
		SetTaskVariables(map[string]interface{}{"secret": "other actor data"}).
		Save(f.userCtx)
	require.NoError(t, err)

	variables, err := f.engine.TaskService().GetTaskVariables(f.scopedCtx(false, false, false, false), other.TaskID)
	requireBPMNForbidden(t, err)
	assert.Nil(t, variables)
}

func TestGetCounterSignStatusAuthorization(t *testing.T) {
	f := newBPMNAuthorizationFixture(t)
	instance := f.createProcessInstance(t, f.tenant, "counter-sign-auth")
	parent := f.createProcessTask(t, instance, f.tenant.ID, "counter-sign-parent", strconv.Itoa(f.outsider.ID), "", "")
	_, err := f.client.ProcessTask.Create().
		SetTaskID("task-counter-sign-child").
		SetProcessInstanceID(instance.ID).
		SetProcessDefinitionKey(instance.ProcessDefinitionKey).
		SetTaskDefinitionKey("counter-sign-child").
		SetTaskName("Counter sign child").
		SetParentTaskID(parent.TaskID).
		SetAssignee(strconv.Itoa(f.actor.ID)).
		SetTenantID(f.tenant.ID).
		Save(f.userCtx)
	require.NoError(t, err)
	otherInstance := f.createProcessInstance(t, f.otherTenant, "counter-sign-auth-other")
	_, err = f.client.ProcessTask.Create().
		SetTaskID("task-counter-sign-child-other").
		SetProcessInstanceID(otherInstance.ID).
		SetProcessDefinitionKey(otherInstance.ProcessDefinitionKey).
		SetTaskDefinitionKey("counter-sign-child-other").
		SetTaskName("Counter sign child other tenant").
		SetParentTaskID(parent.TaskID).
		SetAssignee(strconv.Itoa(f.otherActor.ID)).
		SetStatus("completed").
		SetTaskVariables(map[string]interface{}{"approved": true}).
		SetTenantID(f.otherTenant.ID).
		Save(f.userCtx)
	require.NoError(t, err)

	status, err := f.engine.TaskService().GetCounterSignStatus(f.scopedCtx(false, false, false, false), parent.TaskID)
	requireBPMNForbidden(t, err)
	assert.Nil(t, status)
	status, err = f.engine.TaskService().GetCounterSignStatus(f.scopedCtx(false, false, true, false), parent.TaskID)
	require.NoError(t, err)
	assert.Equal(t, 1, status.Total)
	assert.Zero(t, status.Approved)
}

func TestTaskStatisticsAuthorization(t *testing.T) {
	f := newBPMNAuthorizationFixture(t)
	mine, _ := f.seedMineAndOtherTasks(t)
	otherInstance := f.createProcessInstance(t, f.otherTenant, "task-statistics-other")
	f.createProcessTask(t, otherInstance, f.otherTenant.ID, "task-statistics-other", strconv.Itoa(f.otherActor.ID), "", "")

	_, err := f.engine.TaskService().GetTaskStatistics(
		f.scopedCtx(false, false, false, false),
		&TaskStatisticsRequest{TenantID: f.tenant.ID},
	)
	requireBPMNForbidden(t, err)

	req := &TaskStatisticsRequest{TenantID: f.otherTenant.ID}
	stats, err := f.engine.TaskService().GetTaskStatistics(f.scopedCtx(false, false, true, false), req)
	require.NoError(t, err)
	assert.Equal(t, 2, stats.TotalTasks)
	assert.Equal(t, f.tenant.ID, req.TenantID)
	assert.Contains(t, stats.AssigneeBreakdown, mine.Assignee)
	assert.NotContains(t, stats.AssigneeBreakdown, strconv.Itoa(f.otherActor.ID))
}

func (f *bpmnAuthorizationFixture) seedParticipantAndNonParticipantInstances(t *testing.T) (*ent.ProcessInstance, *ent.ProcessInstance) {
	t.Helper()
	visible := f.createProcessInstance(t, f.tenant, "participant-visible")
	hidden := f.createProcessInstance(t, f.tenant, "participant-hidden")
	f.createProcessTask(t, visible, f.tenant.ID, "participant-visible", strconv.Itoa(f.actor.ID), "", "")
	return visible, hidden
}

func (f *bpmnAuthorizationFixture) createAuthorizedReadInstance(t *testing.T) *ent.ProcessInstance {
	t.Helper()
	instance := f.createProcessInstance(t, f.tenant, "authorized-read")
	instance, err := f.client.ProcessInstance.UpdateOne(instance).
		SetVariables(map[string]interface{}{"classification": "visible"}).
		Save(f.userCtx)
	require.NoError(t, err)
	f.createProcessTask(t, instance, f.tenant.ID, "authorized-read", "", f.actor.Username, "")
	return instance
}

func (f *bpmnAuthorizationFixture) createProcessInstance(t *testing.T, tenant *ent.Tenant, suffix string) *ent.ProcessInstance {
	t.Helper()
	deployment, err := f.client.ProcessDeployment.Create().
		SetDeploymentID("deployment-" + suffix).
		SetDeploymentName("Deployment " + suffix).
		SetTenantID(tenant.ID).
		Save(f.userCtx)
	require.NoError(t, err)
	definition, err := f.client.ProcessDefinition.Create().
		SetKey("process-" + suffix).
		SetName("Process " + suffix).
		SetBpmnXML([]byte("<definitions/>")).
		SetDeploymentID(deployment.ID).
		SetTenantID(tenant.ID).
		Save(f.userCtx)
	require.NoError(t, err)
	instance, err := f.client.ProcessInstance.Create().
		SetProcessInstanceID("instance-" + suffix).
		SetProcessDefinitionKey(definition.Key).
		SetProcessDefinitionID(definition.ID).
		SetTenantID(tenant.ID).
		Save(f.userCtx)
	require.NoError(t, err)
	return instance
}

func (f *bpmnAuthorizationFixture) seedMineAndOtherTasks(t *testing.T) (*ent.ProcessTask, *ent.ProcessTask) {
	t.Helper()
	instance := f.createProcessInstance(t, f.tenant, "task-read-scope")
	mine := f.createProcessTask(t, instance, f.tenant.ID, "task-read-mine", strconv.Itoa(f.actor.ID), "", "")
	other := f.createProcessTask(t, instance, f.tenant.ID, "task-read-other", strconv.Itoa(f.outsider.ID), f.outsider.Username, "")
	return mine, other
}

func (f *bpmnAuthorizationFixture) createProcessTask(t *testing.T, instance *ent.ProcessInstance, tenantID int, suffix, assignee, candidateUsers, candidateGroups string) *ent.ProcessTask {
	t.Helper()
	task, err := f.client.ProcessTask.Create().
		SetTaskID("task-" + suffix).
		SetProcessInstanceID(instance.ID).
		SetProcessDefinitionKey(instance.ProcessDefinitionKey).
		SetTaskDefinitionKey("task-definition-" + suffix).
		SetTaskName("Task " + suffix).
		SetAssignee(assignee).
		SetCandidateUsers(candidateUsers).
		SetCandidateGroups(candidateGroups).
		SetTenantID(tenantID).
		Save(f.userCtx)
	require.NoError(t, err)
	return task
}
