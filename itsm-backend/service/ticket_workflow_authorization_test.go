package service

import (
	"context"
	"fmt"
	"testing"
	"time"

	"itsm-backend/ent"
	"itsm-backend/ent/enttest"
	"itsm-backend/ent/processauditlog"

	_ "github.com/mattn/go-sqlite3"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"go.uber.org/zap"
)

type ticketWorkflowAuthorizationFixture struct {
	client      *ent.Client
	service     *TicketService
	tenant      *ent.Tenant
	otherTenant *ent.Tenant
	actor       *ent.User
	ticket      *ent.Ticket
	instance    *ent.ProcessInstance
}

func newTicketWorkflowAuthorizationFixture(t *testing.T) *ticketWorkflowAuthorizationFixture {
	t.Helper()
	client := enttest.Open(t, "sqlite3", testDSN())
	t.Cleanup(func() { require.NoError(t, client.Close()) })
	ctx := context.Background()
	tenant, err := client.Tenant.Create().SetName("Ticket workflow tenant").SetCode(fmt.Sprintf("ticket-workflow-%d", time.Now().UnixNano())).SetStatus("active").Save(ctx)
	require.NoError(t, err)
	otherTenant, err := client.Tenant.Create().SetName("Other ticket workflow tenant").SetCode(fmt.Sprintf("ticket-workflow-other-%d", time.Now().UnixNano())).SetStatus("active").Save(ctx)
	require.NoError(t, err)
	actor, err := client.User.Create().
		SetUsername(fmt.Sprintf("ticket.workflow.actor.%d", time.Now().UnixNano())).
		SetEmail(fmt.Sprintf("ticket.workflow.actor.%d@example.test", time.Now().UnixNano())).
		SetName("Ticket Workflow Actor").
		SetPasswordHash("test").
		SetRole("service_agent").
		SetTenantID(tenant.ID).
		Save(ctx)
	require.NoError(t, err)
	ticket, err := client.Ticket.Create().
		SetTitle("Ticket workflow authorization").
		SetTicketNumber(fmt.Sprintf("TICKET-WORKFLOW-%d", time.Now().UnixNano())).
		SetRequesterID(actor.ID).
		SetTenantID(tenant.ID).
		Save(ctx)
	require.NoError(t, err)
	deployment, err := client.ProcessDeployment.Create().
		SetDeploymentID(fmt.Sprintf("ticket-workflow-deployment-%d", time.Now().UnixNano())).
		SetDeploymentName("Ticket workflow authorization").
		SetTenantID(tenant.ID).
		Save(ctx)
	require.NoError(t, err)
	definition, err := client.ProcessDefinition.Create().
		SetKey(fmt.Sprintf("ticket_workflow_%d", time.Now().UnixNano())).
		SetName("Ticket workflow authorization").
		SetBpmnXML([]byte("<definitions/>")).
		SetDeploymentID(deployment.ID).
		SetTenantID(tenant.ID).
		Save(ctx)
	require.NoError(t, err)
	instance, err := client.ProcessInstance.Create().
		SetProcessInstanceID(fmt.Sprintf("ticket-workflow-instance-%d", time.Now().UnixNano())).
		SetBusinessKey(fmt.Sprintf("ticket:%d", ticket.ID)).
		SetProcessDefinitionKey(definition.Key).
		SetProcessDefinitionID(definition.ID).
		SetCurrentActivityID("approval").
		SetCurrentActivityName("Approval").
		SetTenantID(tenant.ID).
		Save(ctx)
	require.NoError(t, err)
	logger := zap.NewNop().Sugar()
	engine := NewCustomProcessEngine(client, logger)
	triggerService := NewProcessTriggerService(client, engine)
	ticketService := NewTicketServiceForTest(client, logger)
	ticketService.SetProcessTriggerService(triggerService)
	return &ticketWorkflowAuthorizationFixture{
		client: client, service: ticketService, tenant: tenant, otherTenant: otherTenant,
		actor: actor, ticket: ticket, instance: instance,
	}
}

func TestTicketServiceCancelWorkflowRequiresExplicitUpdateScope(t *testing.T) {
	tests := []struct {
		name     string
		ctx      func(*ticketWorkflowAuthorizationFixture) context.Context
		ticketID func(*ticketWorkflowAuthorizationFixture) int
	}{
		{
			name:     "missing scope is rejected before lookup",
			ctx:      func(*ticketWorkflowAuthorizationFixture) context.Context { return context.Background() },
			ticketID: func(*ticketWorkflowAuthorizationFixture) int { return 999999 },
		},
		{
			name: "read scope cannot cancel",
			ctx: func(f *ticketWorkflowAuthorizationFixture) context.Context {
				return WithBPMNAccessScope(context.Background(), BPMNAccessScope{UserID: f.actor.ID, TenantID: f.tenant.ID, CanReadAllInstances: true})
			},
			ticketID: func(f *ticketWorkflowAuthorizationFixture) int { return f.ticket.ID },
		},
		{
			name: "cross tenant update scope cannot cancel",
			ctx: func(f *ticketWorkflowAuthorizationFixture) context.Context {
				return WithBPMNAccessScope(context.Background(), BPMNAccessScope{UserID: f.actor.ID, TenantID: f.otherTenant.ID, CanUpdateAllInstances: true})
			},
			ticketID: func(f *ticketWorkflowAuthorizationFixture) int { return f.ticket.ID },
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			f := newTicketWorkflowAuthorizationFixture(t)
			err := f.service.CancelWorkflow(tt.ctx(f), tt.ticketID(f), f.tenant.ID, "cancelled")
			requireBPMNForbidden(t, err)
			after, queryErr := f.client.ProcessInstance.Get(context.Background(), f.instance.ID)
			require.NoError(t, queryErr)
			assert.Equal(t, "running", after.Status)
			assert.Zero(t, f.client.ProcessAuditLog.Query().Where(processauditlog.ProcessInstanceID(f.instance.ID)).CountX(context.Background()))
		})
	}
}

func TestTicketServiceCancelWorkflowUsesAuthorizedDomainScope(t *testing.T) {
	f := newTicketWorkflowAuthorizationFixture(t)
	ctx := WithBPMNAccessScope(context.Background(), BPMNAccessScope{
		UserID: f.actor.ID, TenantID: f.tenant.ID, CanUpdateAllInstances: true,
	})

	require.NoError(t, f.service.CancelWorkflow(ctx, f.ticket.ID, f.tenant.ID, "duplicate ticket"))
	after, err := f.client.ProcessInstance.Get(context.Background(), f.instance.ID)
	require.NoError(t, err)
	assert.Equal(t, "terminated", after.Status)
	audit, err := f.client.ProcessAuditLog.Query().Where(processauditlog.ProcessInstanceID(f.instance.ID)).Only(context.Background())
	require.NoError(t, err)
	assert.Equal(t, AuditActionProcessTerminated, audit.Action)
	assert.Equal(t, f.actor.ID, audit.UserID)
	assert.Equal(t, f.tenant.ID, audit.TenantID)
	assert.Equal(t, "duplicate ticket", audit.Comment)
}
