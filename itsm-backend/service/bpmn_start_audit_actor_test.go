package service

import (
	"context"
	"strconv"
	"testing"

	"itsm-backend/ent"
	"itsm-backend/ent/processauditlog"
	"itsm-backend/ent/processinstance"
	"itsm-backend/service/bpmn"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func processStartAudit(t *testing.T, f *bpmnAuthorizationFixture, instance *ent.ProcessInstance) *ent.ProcessAuditLog {
	t.Helper()
	return f.client.ProcessAuditLog.Query().Where(
		processauditlog.ProcessInstanceID(instance.ID),
		processauditlog.Action(AuditActionProcessStarted),
	).OnlyX(context.Background())
}

func TestStartProcessAuditUsesAuthenticatedScopeActor(t *testing.T) {
	f := newBPMNAuthorizationFixture(t)
	instance, err := f.engine.StartProcess(
		f.scopedCtx(false, false, false, false),
		f.definition.Key,
		"ticket:scope-actor",
		"generic",
		91,
		map[string]interface{}{
			"requester_id": f.outsider.ID,
			"triggered_by": strconv.Itoa(f.otherActor.ID), // HTTP/body input must not replace the scoped actor.
		},
	)
	require.NoError(t, err)

	audit := processStartAudit(t, f, instance)
	assert.Equal(t, f.actor.ID, audit.UserID)
	assert.Equal(t, f.actor.Name, audit.UserName)
}

func TestStartProcessAuditUsesTypedContextActor(t *testing.T) {
	f := newBPMNAuthorizationFixture(t)
	ctx := WithTrustedBPMNTenantContext(context.Background(), f.tenant.ID)
	ctx = context.WithValue(ctx, bpmn.BPMNUserIDContextKey, f.actor.ID)
	instance, err := f.engine.StartProcess(ctx, f.definition.Key, "ticket:typed-actor", "generic", 92, map[string]interface{}{
		"triggered_by": "system",
	})
	require.NoError(t, err)

	audit := processStartAudit(t, f, instance)
	assert.Equal(t, f.actor.ID, audit.UserID)
	assert.Equal(t, f.actor.Name, audit.UserName)
}

func TestStartProcessAuditUsesTrustedTriggerActor(t *testing.T) {
	f := newBPMNAuthorizationFixture(t)
	ctx := WithTrustedBPMNTenantContext(context.Background(), f.tenant.ID)
	instance, err := f.engine.StartProcess(ctx, f.definition.Key, "ticket:trusted-trigger", "generic", 93, map[string]interface{}{
		"triggered_by": strconv.Itoa(f.outsider.ID),
	})
	require.NoError(t, err)

	audit := processStartAudit(t, f, instance)
	assert.Equal(t, f.outsider.ID, audit.UserID)
	assert.Equal(t, f.outsider.Name, audit.UserName)
}

func TestStartProcessAuditUsesExplicitTrustedSystemActor(t *testing.T) {
	f := newBPMNAuthorizationFixture(t)
	ctx := WithTrustedBPMNTenantContext(context.Background(), f.tenant.ID)
	instance, err := f.engine.StartProcess(ctx, f.definition.Key, "ticket:trusted-system", "generic", 94, map[string]interface{}{
		"triggered_by": "system",
	})
	require.NoError(t, err)

	audit := processStartAudit(t, f, instance)
	assert.Zero(t, audit.UserID)
	assert.Equal(t, "system", audit.UserName)
}

func TestStartProcessRejectsWrongTenantOrInactiveAuditActor(t *testing.T) {
	f := newBPMNAuthorizationFixture(t)
	inactive, err := f.client.User.Create().
		SetUsername("inactive.start.actor").
		SetEmail("inactive.start.actor@example.test").
		SetName("Inactive Start Actor").
		SetPasswordHash("test").
		SetRole("end_user").
		SetActive(false).
		SetTenantID(f.tenant.ID).
		Save(context.Background())
	require.NoError(t, err)

	for _, actorID := range []int{f.otherActor.ID, inactive.ID} {
		t.Run(strconv.Itoa(actorID), func(t *testing.T) {
			businessKey := "ticket:bad-audit-actor-" + strconv.Itoa(actorID)
			ctx := WithTrustedBPMNTenantContext(context.Background(), f.tenant.ID)
			_, err := f.engine.StartProcess(ctx, f.definition.Key, businessKey, "generic", 95, map[string]interface{}{
				"triggered_by": strconv.Itoa(actorID),
			})
			require.Error(t, err)
			assert.Zero(t, f.client.ProcessInstance.Query().Where(processinstance.BusinessKey(businessKey)).CountX(context.Background()))
			assert.Zero(t, f.client.ProcessExecutionHistory.Query().CountX(context.Background()))
			assert.Zero(t, f.client.ProcessAuditLog.Query().CountX(context.Background()))
			assert.Zero(t, f.client.ProcessCallbackOutbox.Query().CountX(context.Background()))
		})
	}
}
