package service

import (
	"context"
	"strconv"
	"testing"

	"itsm-backend/dto"
	"itsm-backend/ent"
	"itsm-backend/ent/processauditlog"
	"itsm-backend/ent/processinstance"
	"itsm-backend/repository/ticket"
	"itsm-backend/service/bpmn"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"go.uber.org/zap"
)

type processStartTriggerCapture struct {
	ctx context.Context
	req *dto.ProcessTriggerRequest
}

func (c *processStartTriggerCapture) TriggerProcess(ctx context.Context, req *dto.ProcessTriggerRequest) (*dto.ProcessTriggerResponse, error) {
	c.ctx = ctx
	c.req = req
	return &dto.ProcessTriggerResponse{}, nil
}

func (c *processStartTriggerCapture) TriggerByBusinessType(context.Context, dto.BusinessType, int, map[string]interface{}, string, int) (*dto.ProcessTriggerResponse, error) {
	return nil, nil
}

func (c *processStartTriggerCapture) CancelProcess(context.Context, int, string) error  { return nil }
func (c *processStartTriggerCapture) SuspendProcess(context.Context, int, string) error { return nil }
func (c *processStartTriggerCapture) ResumeProcess(context.Context, int) error          { return nil }
func (c *processStartTriggerCapture) GetProcessStatus(context.Context, int) (*dto.ProcessTriggerResponse, error) {
	return nil, nil
}

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
	assert.Equal(t, "system", instance.Initiator)
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

func TestTicketWorkflowStartUsesScopeActorInsteadOfRequestedFor(t *testing.T) {
	f := newBPMNAuthorizationFixture(t)
	trigger := &processStartTriggerCapture{}
	service := &TicketService{processTriggerSvc: trigger, logger: zap.NewNop().Sugar()}

	err := service.TriggerWorkflowForExistingTicket(
		f.scopedCtx(false, false, false, false),
		&ticket.Ticket{ID: 701, TicketNumber: "T-701", RequesterID: f.outsider.ID, RecordClass: "generic"},
		f.tenant.ID,
		"ticket_general_flow",
		nil,
	)
	require.NoError(t, err)
	require.NotNil(t, trigger.req)
	assert.Equal(t, strconv.Itoa(f.actor.ID), trigger.req.TriggeredBy)
	assert.NotEqual(t, strconv.Itoa(f.outsider.ID), trigger.req.TriggeredBy)
}

func TestTicketWorkflowStartUsesExplicitSystemWhenNoActorIsAvailable(t *testing.T) {
	f := newBPMNAuthorizationFixture(t)
	trigger := &processStartTriggerCapture{}
	service := &TicketService{processTriggerSvc: trigger, logger: zap.NewNop().Sugar()}

	err := service.TriggerWorkflowForExistingTicket(
		context.Background(),
		&ticket.Ticket{ID: 702, TicketNumber: "T-702", RequesterID: f.outsider.ID, RecordClass: "generic"},
		f.tenant.ID,
		"ticket_general_flow",
		nil,
	)
	require.NoError(t, err)
	require.NotNil(t, trigger.req)
	assert.Equal(t, "system", trigger.req.TriggeredBy)
}

func TestTicketWorkflowStartUsesRecordClassCanonicalBusinessIdentity(t *testing.T) {
	f := newBPMNAuthorizationFixture(t)
	trigger := &processStartTriggerCapture{}
	service := &TicketService{processTriggerSvc: trigger, logger: zap.NewNop().Sugar()}

	err := service.TriggerWorkflowForExistingTicket(
		f.scopedCtx(false, false, false, false),
		&ticket.Ticket{
			ID:           703,
			TicketNumber: "RITM-703",
			RequesterID:  f.outsider.ID,
			RecordClass:  "service_request_item",
		},
		f.tenant.ID,
		"service_request_flow",
		nil,
	)
	require.NoError(t, err)
	require.NotNil(t, trigger.req)
	assert.Equal(t, dto.BusinessTypeServiceRequest, trigger.req.BusinessType)
	assert.Equal(t, 703, trigger.req.BusinessID)
}

func TestIncidentWorkflowStartUsesScopeActorInsteadOfReporter(t *testing.T) {
	f := newBPMNAuthorizationFixture(t)
	workItem := f.client.Ticket.Create().
		SetTitle("incident").
		SetTicketNumber("INC-WI-701").
		SetType("incident").
		SetRecordClass("incident").
		SetStatus("new").
		SetRequesterID(f.outsider.ID).
		SetTenantID(f.tenant.ID).
		SaveX(context.Background())
	inc := f.client.Incident.Create().
		SetWorkItemID(workItem.ID).
		SetIncidentNumber("INC-701").
		SaveX(context.Background())
	trigger := &processStartTriggerCapture{}
	service := &IncidentService{client: f.client, processTriggerService: trigger, logger: zap.NewNop().Sugar()}

	err := service.triggerWorkflowForIncident(f.scopedCtx(false, false, false, false), inc.ID, f.tenant.ID)
	require.NoError(t, err)
	require.NotNil(t, trigger.req)
	assert.Equal(t, strconv.Itoa(f.actor.ID), trigger.req.TriggeredBy)
	assert.NotEqual(t, strconv.Itoa(f.outsider.ID), trigger.req.TriggeredBy)
}
