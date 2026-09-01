package service

import (
	"context"
	"strconv"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestBPMNInstanceAccessPolicyReadMatrix(t *testing.T) {
	f := newBPMNAuthorizationFixture(t)
	policy := newBPMNInstanceAccessPolicy(f.client, f.resolver)
	instance := f.createAuthorizedReadInstance(t)

	_, err := policy.loadForRead(f.scopedCtx(false, false, false, false), instance.ProcessInstanceID)
	require.NoError(t, err)

	_, err = policy.loadForRead(f.actorScopeCtx(f.outsider, f.tenant, false), instance.ProcessInstanceID)
	requireBPMNForbidden(t, err)

	_, err = policy.loadForRead(f.actorScopeCtx(f.otherActor, f.otherTenant, true), instance.ProcessInstanceID)
	assert.Error(t, err)
	assert.NotContains(t, err.Error(), f.tenant.Code)

	_, err = policy.loadForRead(f.scopedCtx(true, false, false, false), instance.ProcessInstanceID)
	require.NoError(t, err)
}

func TestRequireBPMNInstanceReadAllRejectsParticipantScope(t *testing.T) {
	_, err := RequireBPMNInstanceReadAll(WithBPMNAccessScope(context.Background(), BPMNAccessScope{
		UserID: 1, TenantID: 1,
	}))
	requireBPMNForbidden(t, err)
}

func TestBPMNInstanceAccessPolicyUpdateRequiresElevatedScope(t *testing.T) {
	f := newBPMNAuthorizationFixture(t)
	policy := newBPMNInstanceAccessPolicy(f.client, f.resolver)
	instance := f.createAuthorizedReadInstance(t)

	_, err := policy.loadForUpdate(f.scopedCtx(false, false, false, false), instance.ProcessInstanceID)
	requireBPMNForbidden(t, err)

	_, err = policy.loadForUpdate(f.scopedCtx(true, false, false, false), instance.ProcessInstanceID)
	requireBPMNForbidden(t, err)

	got, err := policy.loadForUpdate(f.scopedCtx(false, true, false, false), instance.ProcessInstanceID)
	require.NoError(t, err)
	assert.Equal(t, instance.ID, got.ID)

	otherTenantCtx := WithBPMNAccessScope(context.Background(), BPMNAccessScope{
		UserID:                f.otherActor.ID,
		TenantID:              f.otherTenant.ID,
		CanUpdateAllInstances: true,
	})
	_, err = policy.loadForUpdate(otherTenantCtx, instance.ProcessInstanceID)
	require.Error(t, err)
	assert.NotContains(t, err.Error(), f.tenant.Code)
}

func TestBPMNInstanceAccessPolicyAuthorizedInstanceIDs(t *testing.T) {
	f := newBPMNAuthorizationFixture(t)
	policy := newBPMNInstanceAccessPolicy(f.client, f.resolver)
	participating, initiated := f.seedParticipantAndNonParticipantInstances(t)
	initiated, err := f.client.ProcessInstance.UpdateOne(initiated).
		SetInitiator(strconv.Itoa(f.actor.ID)).
		Save(f.userCtx)
	require.NoError(t, err)
	f.createProcessTask(t, initiated, f.tenant.ID, "initiated-participant-dedup", f.actor.Username, "", "")
	f.createProcessInstance(t, f.tenant, "not-authorized")
	foreign := f.createProcessInstance(t, f.otherTenant, "foreign-initiator")
	_, err = f.client.ProcessInstance.UpdateOne(foreign).
		SetInitiator(strconv.Itoa(f.actor.ID)).
		Save(f.userCtx)
	require.NoError(t, err)

	ids, err := policy.authorizedInstanceIDs(f.scopedCtx(false, false, false, false))
	require.NoError(t, err)
	assert.ElementsMatch(t, []int{participating.ID, initiated.ID}, ids)

	allTenantIDs, err := policy.authorizedInstanceIDs(f.scopedCtx(true, false, false, false))
	require.NoError(t, err)
	assert.Len(t, allTenantIDs, 3)
	assert.NotContains(t, allTenantIDs, foreign.ID)
}

func TestBPMNInstanceAccessPolicyLoadsNumericAndBusinessKeys(t *testing.T) {
	f := newBPMNAuthorizationFixture(t)
	policy := newBPMNInstanceAccessPolicy(f.client, f.resolver)
	instance := f.createAuthorizedReadInstance(t)

	byNumericID, err := policy.loadForRead(f.scopedCtx(false, false, false, false), strconv.Itoa(instance.ID))
	require.NoError(t, err)
	assert.Equal(t, instance.ID, byNumericID.ID)

	byBusinessKey, err := policy.loadForRead(f.scopedCtx(false, false, false, false), instance.ProcessInstanceID)
	require.NoError(t, err)
	assert.Equal(t, instance.ID, byBusinessKey.ID)
}

func TestBPMNInstanceAccessPolicyForClientRebindsResolver(t *testing.T) {
	f := newBPMNAuthorizationFixture(t)
	policy := newBPMNInstanceAccessPolicy(f.client, f.resolver)
	tx, err := f.client.Tx(f.userCtx)
	require.NoError(t, err)
	t.Cleanup(func() { _ = tx.Rollback() })

	bound := policy.forClient(tx.Client())
	assert.Same(t, tx.Client(), bound.client)
	assert.Same(t, tx.Client(), bound.participationResolver.client)
}
