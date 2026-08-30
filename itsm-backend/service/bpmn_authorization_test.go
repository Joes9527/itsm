package service

import (
	"context"
	"database/sql"
	"errors"
	"strconv"
	"strings"
	"testing"
	"time"

	"itsm-backend/common"
	"itsm-backend/ent"
	"itsm-backend/ent/enttest"
	"itsm-backend/ent/processapprovaldecision"
	"itsm-backend/ent/processauditlog"
	"itsm-backend/ent/processtask"
	"itsm-backend/service/bpmn"

	"entgo.io/ent/dialect"
	entsql "entgo.io/ent/dialect/sql"
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
	return newBPMNAuthorizationFixtureWithDSN(t, testDSN())
}

func newBPMNAuthorizationFixtureWithDSN(t *testing.T, dsn string) *bpmnAuthorizationFixture {
	t.Helper()
	client := enttest.Open(t, "sqlite3", dsn)
	return newBPMNAuthorizationFixtureWithClient(t, client)
}

func newBPMNAuthorizationFixtureWithClient(t *testing.T, client *ent.Client) *bpmnAuthorizationFixture {
	t.Helper()
	ctx := context.Background()
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

func (f *bpmnAuthorizationFixture) typedTaskScopeOnlyCtx(actor *ent.User, canUpdateTasks bool) context.Context {
	return WithBPMNAccessScope(context.Background(), BPMNAccessScope{
		UserID:            actor.ID,
		TenantID:          f.tenant.ID,
		CanUpdateAllTasks: canUpdateTasks,
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

func (f *bpmnAuthorizationFixture) taskActorScopeCtx(actor *ent.User, tenant *ent.Tenant, canReadAll bool) context.Context {
	ctx := context.WithValue(f.userCtx, bpmn.BPMNTenantIDContextKey, tenant.ID)
	ctx = context.WithValue(ctx, bpmn.BPMNUserIDContextKey, actor.ID)
	return WithBPMNAccessScope(ctx, BPMNAccessScope{
		UserID:          actor.ID,
		TenantID:        tenant.ID,
		CanReadAllTasks: canReadAll,
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

	got, err = f.engine.ProcessInstanceService().GetProcessInstance(f.actorScopeCtx(f.outsider, f.tenant, true), instanceID)
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

	variables, err = f.engine.ProcessInstanceService().GetProcessInstanceVariables(f.actorScopeCtx(f.outsider, f.tenant, true), instanceID)
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

	history, err = f.engine.ProcessInstanceService().GetProcessInstanceHistory(f.actorScopeCtx(f.outsider, f.tenant, true), instanceID)
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

	elevatedDecisions, err := f.engine.TaskService().ListApprovalDecisions(f.actorScopeCtx(f.outsider, f.tenant, true), instance.ProcessInstanceID)
	require.NoError(t, err)
	require.Len(t, elevatedDecisions, 1)
	assert.Equal(t, decisions[0].ID, elevatedDecisions[0].ID)

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

func TestProcessInstanceMutationsRequireUpdatePermission(t *testing.T) {
	f := newBPMNAuthorizationFixture(t)
	instance := f.seedRunningInstance(t, "mutation-permission")
	mutations := map[string]func(context.Context) error{
		"suspend": func(ctx context.Context) error {
			return f.engine.SuspendProcess(ctx, instance.ProcessInstanceID, "maintenance")
		},
		"resume": func(ctx context.Context) error {
			return f.engine.ResumeProcess(ctx, instance.ProcessInstanceID)
		},
		"terminate": func(ctx context.Context) error {
			return f.engine.TerminateProcess(ctx, instance.ProcessInstanceID, "cancelled")
		},
		"variables": func(ctx context.Context) error {
			return f.engine.ProcessInstanceService().SetProcessInstanceVariables(ctx, instance.ProcessInstanceID, map[string]interface{}{"safe": true})
		},
	}

	for name, mutate := range mutations {
		t.Run(name, func(t *testing.T) {
			requireBPMNForbidden(t, mutate(f.scopedCtx(false, false, false, false)))
			requireBPMNForbidden(t, mutate(f.scopedCtx(true, false, false, false)))
			requireBPMNForbidden(t, mutate(f.userCtx))
		})
	}
}

func TestProcessInstanceMutationAuditRollback(t *testing.T) {
	forcedAuditErr := errors.New("forced audit failure")
	tests := []struct {
		name    string
		prepare func(*testing.T, *bpmnAuthorizationFixture, *ent.ProcessInstance) *ent.ProcessInstance
		mutate  func(*bpmnAuthorizationFixture, context.Context, *ent.ProcessInstance) error
	}{
		{
			name: "suspend",
			mutate: func(f *bpmnAuthorizationFixture, ctx context.Context, instance *ent.ProcessInstance) error {
				return f.engine.SuspendProcess(ctx, instance.ProcessInstanceID, "maintenance")
			},
		},
		{
			name: "resume",
			prepare: func(t *testing.T, f *bpmnAuthorizationFixture, instance *ent.ProcessInstance) *ent.ProcessInstance {
				updated, err := f.client.ProcessInstance.UpdateOne(instance).SetStatus("suspended").Save(f.userCtx)
				require.NoError(t, err)
				return updated
			},
			mutate: func(f *bpmnAuthorizationFixture, ctx context.Context, instance *ent.ProcessInstance) error {
				return f.engine.ResumeProcess(ctx, instance.ProcessInstanceID)
			},
		},
		{
			name: "terminate",
			mutate: func(f *bpmnAuthorizationFixture, ctx context.Context, instance *ent.ProcessInstance) error {
				return f.engine.TerminateProcess(ctx, instance.ProcessInstanceID, "cancelled")
			},
		},
		{
			name: "variables",
			mutate: func(f *bpmnAuthorizationFixture, ctx context.Context, instance *ent.ProcessInstance) error {
				return f.engine.ProcessInstanceService().SetProcessInstanceVariables(ctx, instance.ProcessInstanceID, map[string]interface{}{"safe": true})
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			f := newBPMNAuthorizationFixture(t)
			instance := f.seedRunningInstance(t, "rollback-"+tt.name)
			if tt.prepare != nil {
				instance = tt.prepare(t, f, instance)
			}
			beforeTasks, err := f.client.ProcessTask.Query().
				Where(processtask.ProcessInstanceID(instance.ID)).
				Order(ent.Asc(processtask.FieldID)).
				All(f.userCtx)
			require.NoError(t, err)
			beforeTaskStatuses := make([]string, len(beforeTasks))
			for i, task := range beforeTasks {
				beforeTaskStatuses[i] = task.Status
			}

			f.client.Use(func(next ent.Mutator) ent.Mutator {
				return ent.MutateFunc(func(ctx context.Context, mutation ent.Mutation) (ent.Value, error) {
					if _, ok := mutation.(*ent.ProcessAuditLogMutation); ok {
						return nil, forcedAuditErr
					}
					return next.Mutate(ctx, mutation)
				})
			})

			err = tt.mutate(f, f.scopedCtx(false, true, false, false), instance)
			require.ErrorIs(t, err, forcedAuditErr)
			after, queryErr := f.client.ProcessInstance.Get(f.userCtx, instance.ID)
			require.NoError(t, queryErr)
			assert.Equal(t, instance.Status, after.Status)
			assert.Equal(t, instance.Variables, after.Variables)

			afterTasks, queryErr := f.client.ProcessTask.Query().
				Where(processtask.ProcessInstanceID(instance.ID)).
				Order(ent.Asc(processtask.FieldID)).
				All(f.userCtx)
			require.NoError(t, queryErr)
			require.Len(t, afterTasks, len(beforeTaskStatuses))
			for i, task := range afterTasks {
				assert.Equal(t, beforeTaskStatuses[i], task.Status)
			}
			assert.Zero(t, f.client.ProcessAuditLog.Query().Where(processauditlog.ProcessInstanceID(instance.ID)).CountX(f.userCtx))
		})
	}
}

func TestProcessInstanceMutationAuditMetadata(t *testing.T) {
	tests := []struct {
		name    string
		prepare func(*testing.T, *bpmnAuthorizationFixture, *ent.ProcessInstance) *ent.ProcessInstance
		mutate  func(*bpmnAuthorizationFixture, context.Context, *ent.ProcessInstance) error
		action  string
		reason  string
		before  map[string]interface{}
		after   map[string]interface{}
	}{
		{
			name: "suspend", action: AuditActionProcessSuspended, reason: "maintenance",
			before: map[string]interface{}{"status": "running"}, after: map[string]interface{}{"status": "suspended"},
			mutate: func(f *bpmnAuthorizationFixture, ctx context.Context, instance *ent.ProcessInstance) error {
				return f.engine.SuspendProcess(ctx, instance.ProcessInstanceID, "maintenance")
			},
		},
		{
			name: "resume", action: AuditActionProcessResumed,
			before: map[string]interface{}{"status": "suspended"}, after: map[string]interface{}{"status": "running"},
			prepare: func(t *testing.T, f *bpmnAuthorizationFixture, instance *ent.ProcessInstance) *ent.ProcessInstance {
				updated, err := f.client.ProcessInstance.UpdateOne(instance).SetStatus("suspended").Save(f.userCtx)
				require.NoError(t, err)
				return updated
			},
			mutate: func(f *bpmnAuthorizationFixture, ctx context.Context, instance *ent.ProcessInstance) error {
				return f.engine.ResumeProcess(ctx, instance.ProcessInstanceID)
			},
		},
		{
			name: "terminate", action: AuditActionProcessTerminated, reason: "cancelled",
			before: map[string]interface{}{"status": "running"}, after: map[string]interface{}{"status": "terminated"},
			mutate: func(f *bpmnAuthorizationFixture, ctx context.Context, instance *ent.ProcessInstance) error {
				return f.engine.TerminateProcess(ctx, instance.ProcessInstanceID, "cancelled")
			},
		},
		{
			name: "variables", action: AuditActionVariableChanged,
			before: map[string]interface{}{"existing": "value"}, after: map[string]interface{}{"safe": true},
			mutate: func(f *bpmnAuthorizationFixture, ctx context.Context, instance *ent.ProcessInstance) error {
				return f.engine.ProcessInstanceService().SetProcessInstanceVariables(ctx, instance.ProcessInstanceID, map[string]interface{}{"safe": true})
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			f := newBPMNAuthorizationFixture(t)
			instance := f.seedRunningInstance(t, "metadata-"+tt.name)
			if tt.prepare != nil {
				instance = tt.prepare(t, f, instance)
			}
			ctx := context.WithValue(f.scopedCtx(false, true, false, false), "user", f.outsider)
			require.NoError(t, tt.mutate(f, ctx, instance))

			audit := f.client.ProcessAuditLog.Query().
				Where(processauditlog.ProcessInstanceID(instance.ID)).
				OnlyX(f.userCtx)
			assert.Equal(t, f.actor.ID, audit.UserID)
			assert.Equal(t, f.actor.Name, audit.UserName)
			assert.Equal(t, f.tenant.ID, audit.TenantID)
			assert.Equal(t, tt.action, audit.Action)
			assert.Equal(t, tt.reason, audit.Comment)
			assert.Equal(t, tt.before, audit.VariablesBefore)
			assert.Equal(t, tt.after, audit.VariablesAfter)
		})
	}
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
	mine, _ := f.seedMineAndOtherTasks(t)
	mine, err := f.client.ProcessTask.UpdateOne(mine).
		SetTaskVariables(map[string]interface{}{"secret": "other actor data"}).
		Save(f.userCtx)
	require.NoError(t, err)

	variables, err := f.engine.TaskService().GetTaskVariables(f.taskActorScopeCtx(f.actor, f.tenant, false), mine.TaskID)
	require.NoError(t, err)
	assert.Equal(t, "other actor data", variables["secret"])

	variables, err = f.engine.TaskService().GetTaskVariables(f.taskActorScopeCtx(f.outsider, f.tenant, false), mine.TaskID)
	requireBPMNForbidden(t, err)
	assert.Nil(t, variables)

	variables, err = f.engine.TaskService().GetTaskVariables(f.taskActorScopeCtx(f.outsider, f.tenant, true), mine.TaskID)
	require.NoError(t, err)
	assert.Equal(t, "other actor data", variables["secret"])

	variables, err = f.engine.TaskService().GetTaskVariables(f.taskActorScopeCtx(f.otherActor, f.otherTenant, true), mine.TaskID)
	require.Error(t, err)
	assert.True(t, ent.IsNotFound(err))
	assert.Nil(t, variables)
}

func TestGetCounterSignStatusAuthorization(t *testing.T) {
	f := newBPMNAuthorizationFixture(t)
	instance := f.createProcessInstance(t, f.tenant, "counter-sign-auth")
	parent := f.createProcessTask(t, instance, f.tenant.ID, "counter-sign-parent", strconv.Itoa(f.actor.ID), "", "")
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

	status, err := f.engine.TaskService().GetCounterSignStatus(f.taskActorScopeCtx(f.actor, f.tenant, false), parent.TaskID)
	require.NoError(t, err)
	assert.Equal(t, 1, status.Total)
	assert.Zero(t, status.Approved)

	status, err = f.engine.TaskService().GetCounterSignStatus(f.taskActorScopeCtx(f.outsider, f.tenant, false), parent.TaskID)
	requireBPMNForbidden(t, err)
	assert.Nil(t, status)

	status, err = f.engine.TaskService().GetCounterSignStatus(f.taskActorScopeCtx(f.outsider, f.tenant, true), parent.TaskID)
	require.NoError(t, err)
	assert.Equal(t, 1, status.Total)
	assert.Zero(t, status.Approved)

	status, err = f.engine.TaskService().GetCounterSignStatus(f.taskActorScopeCtx(f.otherActor, f.otherTenant, true), parent.TaskID)
	require.Error(t, err)
	assert.True(t, ent.IsNotFound(err))
	assert.Nil(t, status)
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

func TestTaskMutationsRequireParticipantOrTaskUpdate(t *testing.T) {
	mutations := taskMutationCases()
	for name, mutate := range mutations {
		t.Run(name, func(t *testing.T) {
			f := newBPMNAuthorizationFixture(t)
			_, task := f.seedMineAndOtherTasks(t)
			requireBPMNForbidden(t, mutate(f, f.scopedCtx(false, false, false, false), task))
		})
	}
}

func TestTaskParticipantCanMutateOwnTask(t *testing.T) {
	for name, mutate := range taskMutationCases() {
		t.Run(name, func(t *testing.T) {
			f := newBPMNAuthorizationFixture(t)
			task, _ := f.seedMineAndOtherTasks(t)
			require.NoError(t, mutate(f, f.scopedCtx(false, false, false, false), task))
			audit := f.client.ProcessAuditLog.Query().
				Where(processauditlog.ProcessInstanceID(task.ProcessInstanceID)).
				OnlyX(f.userCtx)
			assert.Equal(t, expectedTaskMutationAuditAction(name), audit.Action)
			assert.Equal(t, f.actor.ID, audit.UserID)
			assert.Equal(t, f.tenant.ID, audit.TenantID)
			if name == "counter-sign" {
				assert.Equal(t, 1, f.client.ProcessTask.Query().Where(processtask.ParentTaskID(task.TaskID), processtask.TenantID(f.tenant.ID)).CountX(f.userCtx))
				assert.Equal(t, float64(1), f.client.ProcessTask.GetX(f.userCtx, task.ID).TaskVariables["total"])
			}
		})
	}
}

func TestTaskUpdaterCanMutateOtherTask(t *testing.T) {
	for name, mutate := range taskMutationCases() {
		t.Run(name, func(t *testing.T) {
			f := newBPMNAuthorizationFixture(t)
			_, task := f.seedMineAndOtherTasks(t)
			require.NoError(t, mutate(f, f.scopedCtx(false, false, false, true), task))
			assert.Equal(t, 1, f.client.ProcessAuditLog.Query().
				Where(processauditlog.ProcessInstanceID(task.ProcessInstanceID)).
				CountX(f.userCtx))
		})
	}
}

func TestTaskUpdaterCanClaimCompleteAndVoteNonParticipant(t *testing.T) {
	tests := []struct {
		name   string
		mutate func(*bpmnAuthorizationFixture, context.Context, *ent.ProcessTask) error
	}{
		{
			name: "claim",
			mutate: func(f *bpmnAuthorizationFixture, ctx context.Context, task *ent.ProcessTask) error {
				return f.engine.TaskService().ClaimTask(ctx, task.TaskID, strconv.Itoa(f.actor.ID))
			},
		},
		{
			name: "claim by id",
			mutate: func(f *bpmnAuthorizationFixture, ctx context.Context, task *ent.ProcessTask) error {
				return f.engine.TaskService().ClaimTaskByID(ctx, task.ID, f.actor.ID)
			},
		},
		{
			name: "complete",
			mutate: func(f *bpmnAuthorizationFixture, ctx context.Context, task *ent.ProcessTask) error {
				return f.engine.TaskService().CompleteTask(ctx, task.TaskID, map[string]interface{}{"approved": true})
			},
		},
		{
			name: "submit decision",
			mutate: func(f *bpmnAuthorizationFixture, ctx context.Context, task *ent.ProcessTask) error {
				return f.engine.TaskService().CompleteTask(ctx, task.TaskID, map[string]interface{}{
					"approvalAction":  "approve",
					"approvalResult":  "approved",
					"approvalComment": "approved",
				})
			},
		},
		{
			name: "vote",
			mutate: func(f *bpmnAuthorizationFixture, ctx context.Context, task *ent.ProcessTask) error {
				task, err := f.client.ProcessTask.UpdateOne(task).SetStatus(common.ProcessTaskStatusAssigned).Save(f.userCtx)
				if err != nil {
					return err
				}
				return f.engine.TaskService().Vote(ctx, task.TaskID, &VoteRequest{Approved: true, Comment: "approved"})
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name+" allows typed-scope-only participant", func(t *testing.T) {
			f := newBPMNAuthorizationFixture(t)
			task := f.seedNonParticipantApprovalTask(t, "participant-"+strings.ReplaceAll(tt.name, " ", "-"))
			task, err := f.client.ProcessTask.UpdateOne(task).SetCandidateUsers(f.actor.Username).Save(f.userCtx)
			require.NoError(t, err)
			require.NoError(t, tt.mutate(f, f.typedTaskScopeOnlyCtx(f.actor, false), task))
		})
		t.Run(tt.name+" rejects typed-scope-only read-only nonparticipant", func(t *testing.T) {
			f := newBPMNAuthorizationFixture(t)
			task := f.seedNonParticipantApprovalTask(t, "readonly-"+strings.ReplaceAll(tt.name, " ", "-"))
			requireBPMNForbidden(t, tt.mutate(f, f.typedTaskScopeOnlyCtx(f.actor, false), task))
		})
		t.Run(tt.name+" allows typed-scope-only task updater nonparticipant", func(t *testing.T) {
			f := newBPMNAuthorizationFixture(t)
			task := f.seedNonParticipantApprovalTask(t, "updater-"+strings.ReplaceAll(tt.name, " ", "-"))
			require.NoError(t, tt.mutate(f, f.typedTaskScopeOnlyCtx(f.actor, true), task))
		})
	}
}

func TestTypedScopeOnlyClaimRejectsRequestedIdentityMismatch(t *testing.T) {
	f := newBPMNAuthorizationFixture(t)
	task := f.seedNonParticipantApprovalTask(t, "claim-identity-mismatch")
	err := f.engine.TaskService().ClaimTask(
		f.typedTaskScopeOnlyCtx(f.actor, true),
		task.TaskID,
		strconv.Itoa(f.outsider.ID),
	)
	requireBPMNForbidden(t, err)
}

func TestTaskMutationRejectsCrossTenant(t *testing.T) {
	for name, mutate := range taskMutationCases() {
		t.Run(name+" target", func(t *testing.T) {
			f := newBPMNAuthorizationFixture(t)
			instance := f.createProcessInstance(t, f.otherTenant, "mutation-cross-tenant-"+name)
			task := f.createProcessTask(t, instance, f.otherTenant.ID, "mutation-cross-tenant-"+name, strconv.Itoa(f.otherActor.ID), "", "")
			err := mutate(f, f.scopedCtx(false, false, false, true), task)
			require.Error(t, err)
			assert.True(t, ent.IsNotFound(err), "cross-tenant mutation must be indistinguishable from absence: %v", err)
		})
	}

	t.Run("assign foreign user", func(t *testing.T) {
		f := newBPMNAuthorizationFixture(t)
		task, _ := f.seedMineAndOtherTasks(t)
		err := f.engine.TaskService().AssignTask(f.scopedCtx(false, false, false, false), task.TaskID, strconv.Itoa(f.otherActor.ID))
		require.Error(t, err)
		assert.Equal(t, task.Assignee, f.client.ProcessTask.GetX(f.userCtx, task.ID).Assignee)
	})

	t.Run("counter-sign foreign approver", func(t *testing.T) {
		f := newBPMNAuthorizationFixture(t)
		task, _ := f.seedMineAndOtherTasks(t)
		_, err := f.engine.TaskService().CreateCounterSignTasks(
			f.scopedCtx(false, false, false, false),
			task.TaskID,
			&CounterSignRequest{Approvers: []string{strconv.Itoa(f.otherActor.ID)}, ApprovalType: "parallel", Threshold: 1},
		)
		require.Error(t, err)
		assert.Zero(t, f.client.ProcessTask.Query().Where(processtask.ParentTaskID(task.TaskID)).CountX(f.userCtx))
	})
}

func TestTaskMutationAuditRollback(t *testing.T) {
	forcedAuditErr := errors.New("forced task audit failure")
	for name, mutate := range taskMutationCasesWithoutCounterSign() {
		t.Run(name, func(t *testing.T) {
			f := newBPMNAuthorizationFixture(t)
			task, _ := f.seedMineAndOtherTasks(t)
			before := f.client.ProcessTask.GetX(f.userCtx, task.ID)
			failProcessAuditCreation(f.client, forcedAuditErr)

			err := mutate(f, f.scopedCtx(false, false, false, false), task)
			require.ErrorIs(t, err, forcedAuditErr)
			after := f.client.ProcessTask.GetX(f.userCtx, task.ID)
			assert.Equal(t, before.Status, after.Status)
			assert.Equal(t, before.Assignee, after.Assignee)
			assert.Equal(t, before.TaskVariables, after.TaskVariables)
			assert.Zero(t, f.client.ProcessAuditLog.Query().Where(processauditlog.ProcessInstanceID(task.ProcessInstanceID)).CountX(f.userCtx))
		})
	}
}

func TestCounterSignCreatesChildrenParentAndAuditAtomically(t *testing.T) {
	forcedAuditErr := errors.New("forced counter-sign audit failure")
	f := newBPMNAuthorizationFixture(t)
	parent, _ := f.seedMineAndOtherTasks(t)
	beforeVariables := parent.TaskVariables
	failProcessAuditCreation(f.client, forcedAuditErr)

	children, err := f.engine.TaskService().CreateCounterSignTasks(
		f.scopedCtx(false, false, false, false),
		parent.TaskID,
		&CounterSignRequest{Approvers: []string{strconv.Itoa(f.actor.ID), strconv.Itoa(f.outsider.ID)}, ApprovalType: "parallel", Threshold: 2},
	)
	require.ErrorIs(t, err, forcedAuditErr)
	assert.Nil(t, children)
	assert.Zero(t, f.client.ProcessTask.Query().Where(processtask.ParentTaskID(parent.TaskID)).CountX(f.userCtx))
	assert.Equal(t, beforeVariables, f.client.ProcessTask.GetX(f.userCtx, parent.ID).TaskVariables)
	assert.Zero(t, f.client.ProcessAuditLog.Query().Where(processauditlog.ProcessInstanceID(parent.ProcessInstanceID)).CountX(f.userCtx))
}

func TestVoteWritesDecisionAndCompletesTaskAtomically(t *testing.T) {
	forcedAuditErr := errors.New("forced vote audit failure")
	f := newBPMNAuthorizationFixture(t)
	instance := f.createProcessInstance(t, f.tenant, "vote-rollback")
	task := f.createProcessTask(t, instance, f.tenant.ID, "vote-rollback", strconv.Itoa(f.actor.ID), "", "")
	_, err := f.client.ProcessTask.UpdateOne(task).SetStatus(common.ProcessTaskStatusAssigned).Save(f.userCtx)
	require.NoError(t, err)
	failProcessAuditCreation(f.client, forcedAuditErr)

	voteCtx := context.WithValue(f.scopedCtx(false, false, false, false), bpmn.BPMNTenantIDContextKey, f.tenant.ID)
	voteCtx = context.WithValue(voteCtx, bpmn.BPMNUserIDContextKey, f.actor.ID)
	err = f.engine.TaskService().Vote(
		voteCtx,
		task.TaskID,
		&VoteRequest{Approved: true, Comment: "approved"},
	)
	require.ErrorIs(t, err, forcedAuditErr)
	after := f.client.ProcessTask.GetX(f.userCtx, task.ID)
	assert.Equal(t, common.ProcessTaskStatusAssigned, after.Status)
	assert.Empty(t, after.TaskVariables)
	assert.Zero(t, f.client.ProcessApprovalDecision.Query().CountX(f.userCtx))
	assert.Zero(t, f.client.ProcessAuditLog.Query().Where(processauditlog.ProcessInstanceID(instance.ID)).CountX(f.userCtx))
}

func TestVoteTenantScopesRelatedTasks(t *testing.T) {
	f := newBPMNAuthorizationFixture(t)
	instance := f.createProcessInstance(t, f.tenant, "vote-tenant-scope")
	parent := f.createProcessTask(t, instance, f.tenant.ID, "vote-tenant-parent", strconv.Itoa(f.actor.ID), "", "")
	otherInstance := f.createProcessInstance(t, f.otherTenant, "vote-tenant-scope-other")
	foreignChild := f.createProcessTask(t, otherInstance, f.otherTenant.ID, "vote-foreign-child", strconv.Itoa(f.otherActor.ID), "", "")
	foreignChild, err := f.client.ProcessTask.UpdateOne(foreignChild).
		SetParentTaskID(parent.TaskID).
		SetStatus("created").
		Save(f.userCtx)
	require.NoError(t, err)

	children, err := f.engine.TaskService().CreateCounterSignTasks(
		f.scopedCtx(false, false, false, false),
		parent.TaskID,
		&CounterSignRequest{Approvers: []string{strconv.Itoa(f.actor.ID), strconv.Itoa(f.outsider.ID)}, ApprovalType: "serial", Threshold: 2},
	)
	require.NoError(t, err)
	require.Len(t, children, 2)

	voteCtx := context.WithValue(f.scopedCtx(false, false, false, false), bpmn.BPMNTenantIDContextKey, f.tenant.ID)
	voteCtx = context.WithValue(voteCtx, bpmn.BPMNUserIDContextKey, f.actor.ID)
	require.NoError(t, f.engine.TaskService().Vote(voteCtx, children[0].TaskID, &VoteRequest{Approved: true, Comment: "first approval"}))

	assert.Equal(t, "completed", f.client.ProcessTask.GetX(f.userCtx, children[0].ID).Status)
	assert.Equal(t, common.ProcessTaskStatusAssigned, f.client.ProcessTask.GetX(f.userCtx, children[1].ID).Status)
	assert.Equal(t, "created", f.client.ProcessTask.GetX(f.userCtx, foreignChild.ID).Status)
	assert.Equal(t, 1, f.client.ProcessApprovalDecision.Query().Where(processapprovaldecision.TenantID(f.tenant.ID)).CountX(f.userCtx))
	assert.Zero(t, f.client.ProcessApprovalDecision.Query().Where(processapprovaldecision.TenantID(f.otherTenant.ID)).CountX(f.userCtx))
}

func TestTaskAuditRejectsForeignProcessInstance(t *testing.T) {
	mutations := taskMutationCases()
	mutations["vote"] = func(f *bpmnAuthorizationFixture, ctx context.Context, task *ent.ProcessTask) error {
		ctx = context.WithValue(ctx, bpmn.BPMNTenantIDContextKey, f.tenant.ID)
		ctx = context.WithValue(ctx, bpmn.BPMNUserIDContextKey, f.actor.ID)
		return f.engine.TaskService().Vote(ctx, task.TaskID, &VoteRequest{Approved: true, Comment: "invalid reference"})
	}

	for name, mutate := range mutations {
		t.Run(name, func(t *testing.T) {
			f := newBPMNAuthorizationFixture(t)
			foreignInstance := f.createProcessInstance(t, f.otherTenant, "foreign-audit-instance-"+name)
			task := f.createProcessTask(t, foreignInstance, f.tenant.ID, "foreign-audit-task-"+name, strconv.Itoa(f.actor.ID), "", "")
			task, err := f.client.ProcessTask.UpdateOne(task).
				SetStatus(common.ProcessTaskStatusAssigned).
				SetTaskVariables(map[string]interface{}{"before": "unchanged"}).
				Save(f.userCtx)
			require.NoError(t, err)

			err = mutate(f, f.scopedCtx(false, false, false, false), task)
			require.Error(t, err)
			after := f.client.ProcessTask.GetX(f.userCtx, task.ID)
			assert.Equal(t, common.ProcessTaskStatusAssigned, after.Status)
			assert.Equal(t, task.Assignee, after.Assignee)
			assert.Equal(t, map[string]interface{}{"before": "unchanged"}, after.TaskVariables)
			assert.Zero(t, f.client.ProcessTask.Query().Where(processtask.ParentTaskID(task.TaskID)).CountX(f.userCtx))
			assert.Zero(t, f.client.ProcessApprovalDecision.Query().CountX(f.userCtx))
			assert.Zero(t, f.client.ProcessAuditLog.Query().CountX(f.userCtx))
		})
	}

	t.Run("completion audit helper", func(t *testing.T) {
		f := newBPMNAuthorizationFixture(t)
		foreignInstance := f.createProcessInstance(t, f.otherTenant, "foreign-completion-audit")
		task := f.createProcessTask(t, foreignInstance, f.tenant.ID, "foreign-completion-audit", strconv.Itoa(f.actor.ID), "", "")
		err := f.engine.auditService.RecordTaskCompleted(f.userCtx, task, f.actor.ID, f.actor.Name, nil, map[string]interface{}{"approved": true})
		require.Error(t, err)
		assert.Zero(t, f.client.ProcessAuditLog.Query().CountX(f.userCtx))
	})
}

func TestVoteDuplicateReturnsConflictWithoutDuplicateEffects(t *testing.T) {
	f := newBPMNAuthorizationFixture(t)
	instance := f.createProcessInstance(t, f.tenant, "vote-duplicate")
	task := f.createProcessTask(t, instance, f.tenant.ID, "vote-duplicate", strconv.Itoa(f.actor.ID), "", "")
	_, err := f.client.ProcessTask.UpdateOne(task).SetStatus(common.ProcessTaskStatusAssigned).Save(f.userCtx)
	require.NoError(t, err)
	voteCtx := context.WithValue(f.scopedCtx(false, false, false, false), bpmn.BPMNTenantIDContextKey, f.tenant.ID)
	voteCtx = context.WithValue(voteCtx, bpmn.BPMNUserIDContextKey, f.actor.ID)

	require.NoError(t, f.engine.TaskService().Vote(voteCtx, task.TaskID, &VoteRequest{Approved: true, Comment: "first"}))
	err = f.engine.TaskService().Vote(voteCtx, task.TaskID, &VoteRequest{Approved: false, Comment: "duplicate"})
	requireBPMNConflict(t, err)
	assert.NotContains(t, err.Error(), task.TaskID)
	assert.NotContains(t, err.Error(), f.tenant.Code)
	assert.Equal(t, 1, f.client.ProcessApprovalDecision.Query().Where(processapprovaldecision.ProcessTaskID(task.ID)).CountX(f.userCtx))
	assert.Equal(t, 1, f.client.ProcessAuditLog.Query().Where(processauditlog.ProcessInstanceID(instance.ID), processauditlog.ActivityID(task.TaskDefinitionKey)).CountX(f.userCtx))
}

func TestVoteConcurrentCallsCommitOnce(t *testing.T) {
	rawDB, err := sql.Open("sqlite3", testDSN())
	require.NoError(t, err)
	rawDB.SetMaxOpenConns(1)
	client := ent.NewClient(ent.Driver(entsql.OpenDB(dialect.SQLite, rawDB)))
	require.NoError(t, client.Schema.Create(context.Background()))
	f := newBPMNAuthorizationFixtureWithClient(t, client)
	instance := f.createProcessInstance(t, f.tenant, "vote-concurrent")
	task := f.createProcessTask(t, instance, f.tenant.ID, "vote-concurrent", strconv.Itoa(f.actor.ID), "", "")
	_, err = f.client.ProcessTask.UpdateOne(task).SetStatus(common.ProcessTaskStatusAssigned).Save(f.userCtx)
	require.NoError(t, err)
	voteCtx := context.WithValue(f.scopedCtx(false, false, false, false), bpmn.BPMNTenantIDContextKey, f.tenant.ID)
	voteCtx = context.WithValue(voteCtx, bpmn.BPMNUserIDContextKey, f.actor.ID)

	start := make(chan struct{})
	results := make(chan error, 2)
	for i := 0; i < 2; i++ {
		go func(approved bool) {
			<-start
			results <- f.engine.TaskService().Vote(voteCtx, task.TaskID, &VoteRequest{Approved: approved, Comment: "concurrent"})
		}(i == 0)
	}
	close(start)

	errs := []error{<-results, <-results}
	successes, conflicts := 0, 0
	for _, voteErr := range errs {
		if voteErr == nil {
			successes++
			continue
		}
		var appErr *common.AppError
		if errors.As(voteErr, &appErr) && appErr.Code == common.ErrCodeConflict {
			conflicts++
		}
	}
	assert.Equal(t, 1, successes, "concurrent vote errors: %v", errs)
	assert.Equal(t, 1, conflicts, "concurrent vote errors: %v", errs)
	assert.Equal(t, 1, f.client.ProcessApprovalDecision.Query().Where(processapprovaldecision.ProcessTaskID(task.ID)).CountX(f.userCtx))
	assert.Equal(t, 1, f.client.ProcessAuditLog.Query().Where(processauditlog.ProcessInstanceID(instance.ID), processauditlog.ActivityID(task.TaskDefinitionKey)).CountX(f.userCtx))
}

type taskMutation func(*bpmnAuthorizationFixture, context.Context, *ent.ProcessTask) error

func taskMutationCases() map[string]taskMutation {
	mutations := taskMutationCasesWithoutCounterSign()
	mutations["counter-sign"] = func(f *bpmnAuthorizationFixture, ctx context.Context, task *ent.ProcessTask) error {
		_, err := f.engine.TaskService().CreateCounterSignTasks(ctx, task.TaskID, &CounterSignRequest{
			Approvers: []string{strconv.Itoa(f.actor.ID)}, ApprovalType: "parallel", Threshold: 1,
		})
		return err
	}
	return mutations
}

func taskMutationCasesWithoutCounterSign() map[string]taskMutation {
	return map[string]taskMutation{
		"assign": func(f *bpmnAuthorizationFixture, ctx context.Context, task *ent.ProcessTask) error {
			return f.engine.TaskService().AssignTask(ctx, task.TaskID, strconv.Itoa(f.outsider.ID))
		},
		"cancel": func(f *bpmnAuthorizationFixture, ctx context.Context, task *ent.ProcessTask) error {
			return f.engine.TaskService().CancelTask(ctx, task.TaskID, "invalid")
		},
		"variables": func(f *bpmnAuthorizationFixture, ctx context.Context, task *ent.ProcessTask) error {
			return f.engine.TaskService().SetTaskVariables(ctx, task.TaskID, map[string]interface{}{"x": 1})
		},
	}
}

func expectedTaskMutationAuditAction(name string) string {
	switch name {
	case "assign":
		return AuditActionTaskAssigned
	case "cancel":
		return AuditActionTaskCancelled
	case "variables":
		return AuditActionTaskVariablesChanged
	case "counter-sign":
		return AuditActionCounterSignCreated
	default:
		return ""
	}
}

func failProcessAuditCreation(client *ent.Client, forcedErr error) {
	client.Use(func(next ent.Mutator) ent.Mutator {
		return ent.MutateFunc(func(ctx context.Context, mutation ent.Mutation) (ent.Value, error) {
			if _, ok := mutation.(*ent.ProcessAuditLogMutation); ok {
				return nil, forcedErr
			}
			return next.Mutate(ctx, mutation)
		})
	})
}

func requireBPMNConflict(t *testing.T, err error) {
	t.Helper()
	var appErr *common.AppError
	require.ErrorAs(t, err, &appErr)
	assert.Equal(t, common.ErrCodeConflict, appErr.Code)
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

func (f *bpmnAuthorizationFixture) seedNonParticipantApprovalTask(t *testing.T, suffix string) *ent.ProcessTask {
	t.Helper()
	deployment, err := f.client.ProcessDeployment.Create().
		SetDeploymentID("deployment-nonparticipant-" + suffix).
		SetDeploymentName("Nonparticipant " + suffix).
		SetTenantID(f.tenant.ID).
		Save(f.userCtx)
	require.NoError(t, err)
	definition, err := f.client.ProcessDefinition.Create().
		SetKey("nonparticipant-" + suffix).
		SetName("Nonparticipant " + suffix).
		SetBpmnXML([]byte(`<?xml version="1.0" encoding="UTF-8"?>
<bpmn:definitions xmlns:bpmn="http://www.omg.org/spec/BPMN/20100524/MODEL" targetNamespace="http://bpmn.io/schema/bpmn">
  <bpmn:process id="nonparticipant" isExecutable="true">
    <bpmn:startEvent id="start" />
    <bpmn:userTask id="approval" name="Approval" />
    <bpmn:endEvent id="end" />
    <bpmn:sequenceFlow id="to-approval" sourceRef="start" targetRef="approval" />
    <bpmn:sequenceFlow id="to-end" sourceRef="approval" targetRef="end" />
  </bpmn:process>
</bpmn:definitions>`)).
		SetDeploymentID(deployment.ID).
		SetTenantID(f.tenant.ID).
		Save(f.userCtx)
	require.NoError(t, err)
	instance, err := f.client.ProcessInstance.Create().
		SetProcessInstanceID("instance-nonparticipant-" + suffix).
		SetProcessDefinitionKey(definition.Key).
		SetProcessDefinitionID(definition.ID).
		SetCurrentActivityID("approval").
		SetCurrentActivityName("Approval").
		SetTenantID(f.tenant.ID).
		Save(f.userCtx)
	require.NoError(t, err)
	task, err := f.client.ProcessTask.Create().
		SetTaskID("task-nonparticipant-" + suffix).
		SetProcessInstanceID(instance.ID).
		SetProcessDefinitionKey(definition.Key).
		SetTaskDefinitionKey("approval").
		SetTaskName("Approval").
		SetCandidateUsers(f.outsider.Username).
		SetTenantID(f.tenant.ID).
		Save(f.userCtx)
	require.NoError(t, err)
	return task
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

func (f *bpmnAuthorizationFixture) seedRunningInstance(t *testing.T, suffix string) *ent.ProcessInstance {
	t.Helper()
	instance := f.createProcessInstance(t, f.tenant, suffix)
	instance, err := f.client.ProcessInstance.UpdateOne(instance).
		SetCurrentActivityID("approval").
		SetCurrentActivityName("Approval").
		SetVariables(map[string]interface{}{"existing": "value"}).
		Save(f.userCtx)
	require.NoError(t, err)
	f.createProcessTask(t, instance, f.tenant.ID, suffix+"-active", strconv.Itoa(f.actor.ID), "", "")
	completed := f.createProcessTask(t, instance, f.tenant.ID, suffix+"-completed", strconv.Itoa(f.actor.ID), "", "")
	_, err = f.client.ProcessTask.UpdateOne(completed).SetStatus("completed").Save(f.userCtx)
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
