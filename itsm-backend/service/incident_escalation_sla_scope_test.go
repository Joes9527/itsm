package service

import (
	"testing"

	"github.com/stretchr/testify/require"
	"itsm-backend/dto"
	"itsm-backend/handlers/shared/workitemmutation"
)

func TestIncidentEscalationSLABreachScopesWorkItem(t *testing.T) {
	for _, tc := range []struct {
		name                          string
		unrelated, resolved, escalate bool
	}{
		{name: "other_work_item", unrelated: true},
		{name: "own_resolved", resolved: true},
		{name: "own_unresolved", escalate: true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			client, _, ctx := setupIncidentTest(t)
			defer client.Close()
			tenant, err := createIncidentTestTenant(ctx, client, "sla-scope")
			require.NoError(t, err)
			actor, err := createIncidentTestUser(ctx, client, tenant.ID, "sla-actor")
			require.NoError(t, err)
			actor.Update().SetRole("super_admin").ExecX(ctx)
			other := client.Ticket.Create().SetTenantID(tenant.ID).SetRequesterID(actor.ID).SetTitle("other work item").SetTicketNumber("OTHER-SLA").SaveX(ctx)
			inc := createAutomationIncident(t, ctx, client, tenant.ID, actor.ID, "SLA-SCOPE")
			before := client.Ticket.UpdateOneID(inc.WorkItemID).SetStatus("in_progress").SaveX(ctx)
			require.NotEqual(t, inc.ID, before.ID, "SLA violation uses WorkItem ID, not Incident ID")
			definition, err := createSLATestDefinition(ctx, client, tenant.ID, "scope policy")
			require.NoError(t, err)
			targetID := before.ID
			if tc.unrelated {
				targetID = other.ID
			}
			client.SLAViolation.Create().SetTenantID(tenant.ID).SetTicketID(targetID).SetSLADefinitionID(definition.ID).SetViolationType("response_time").SetIsResolved(tc.resolved).SaveX(ctx)
			svc := NewIncidentEscalationService(client)
			_, err = svc.CreateEscalationRule(ctx, dto.CreateIncidentEscalationRuleRequest{Name: "SLA L1", TriggerType: "sla_breach", TriggerMinutes: 1, EscalationLevel: 1, TargetAssigneeType: "user", AutoEscalate: true, IsActive: true, TenantID: tenant.ID})
			require.NoError(t, err)
			_, err = svc.CheckAndEscalate(ctx, inc.ID, workitemmutation.Meta{TenantID: tenant.ID, ActorID: actor.ID, ExpectedVersion: before.Version, Source: "scheduler", OperationID: "sla-scope-attempt"})
			require.NoError(t, err)
			after := client.Ticket.GetX(ctx, before.ID)
			if tc.escalate {
				require.Equal(t, "escalated", after.Status)
				require.Equal(t, before.Version+1, after.Version)
				require.Equal(t, 1, client.Incident.GetX(ctx, inc.ID).EscalationLevel)
				require.Equal(t, 1, client.AuditLog.Query().CountX(ctx))
			} else {
				require.Equal(t, before.Status, after.Status)
				require.Equal(t, before.Version, after.Version)
				require.Zero(t, client.Incident.GetX(ctx, inc.ID).EscalationLevel)
				require.Zero(t, client.AuditLog.Query().CountX(ctx))
				require.Zero(t, client.IncidentEvent.Query().CountX(ctx))
				require.Zero(t, client.OutboxEvent.Query().CountX(ctx))
			}
		})
	}
}
