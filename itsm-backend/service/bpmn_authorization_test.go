package service

import (
	"context"
	"strconv"
	"testing"

	"itsm-backend/ent"
	"itsm-backend/ent/enttest"
	"itsm-backend/service/bpmn"

	_ "github.com/mattn/go-sqlite3"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

type bpmnAuthorizationFixture struct {
	client      *ent.Client
	resolver    *bpmnParticipationResolver
	userCtx     context.Context
	tenant      *ent.Tenant
	otherTenant *ent.Tenant
	actor       *ent.User
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

	_, err = client.Group.Create().
		SetName("vpn-operators").
		SetTenantID(tenant.ID).
		AddMemberIDs(actor.ID).
		Save(ctx)
	require.NoError(t, err)

	return &bpmnAuthorizationFixture{
		client:      client,
		resolver:    &bpmnParticipationResolver{client: client, groupResolver: bpmn.NewGroupResolver(client)},
		userCtx:     ctx,
		tenant:      tenant,
		otherTenant: otherTenant,
		actor:       actor,
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

func (f *bpmnAuthorizationFixture) createProcessTask(t *testing.T, instance *ent.ProcessInstance, tenantID int, suffix, assignee, candidateUsers, candidateGroups string) {
	t.Helper()
	_, err := f.client.ProcessTask.Create().
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
}
