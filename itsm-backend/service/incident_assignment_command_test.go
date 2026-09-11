package service

import (
	"context"
	"fmt"
	"github.com/stretchr/testify/require"
	"itsm-backend/dto"
	"itsm-backend/handlers/shared/workitemmutation"
	"testing"
)

func TestIncidentReassignmentPreservesProgress(t *testing.T) {
	for _, status := range []string{"assigned", "acknowledged", "in_progress", "escalated", "triaged", "on_hold"} {
		t.Run(status, func(t *testing.T) {
			client, svc, ctx := setupIncidentTest(t)
			defer client.Close()
			tenant, err := createIncidentTestTenant(ctx, client, "reassign")
			require.NoError(t, err)
			actor, err := createIncidentTestUser(ctx, client, tenant.ID, "owner")
			require.NoError(t, err)
			actor.Update().SetRole("super_admin").ExecX(ctx)
			next, err := createIncidentTestUser(ctx, client, tenant.ID, "next")
			require.NoError(t, err)
			inc := createAutomationIncident(t, ctx, client, tenant.ID, actor.ID, "reassign")
			before := client.Ticket.UpdateOneID(inc.WorkItemID).SetStatus(status).SetAssigneeID(actor.ID).SaveX(ctx)
			cmd := dto.IncidentCommand{IncidentID: inc.ID, Action: "assign", AssigneeID: next.ID, Reason: "handover", Meta: workitemmutation.Meta{TenantID: tenant.ID, ActorID: actor.ID, ExpectedVersion: before.Version, Source: "http", OperationID: "reassign"}}
			result, err := svc.ApplyIncidentCommand(ctx, cmd)
			require.NoError(t, err)
			after := client.Ticket.GetX(ctx, before.ID)
			require.Equal(t, status, after.Status)
			require.Equal(t, next.ID, after.AssigneeID)
			require.Equal(t, before.Version+1, result.Version)
			require.Equal(t, before.FirstResponseAt, after.FirstResponseAt)
			receipt := client.AuditLog.Query().OnlyX(ctx)
			require.Contains(t, *receipt.RequestBody, `"previousAssigneeId":`)
			event := client.IncidentEvent.Query().OnlyX(ctx)
			require.Equal(t, "assignment", event.EventType)
			replay, err := svc.ApplyIncidentCommand(ctx, cmd)
			require.NoError(t, err)
			require.True(t, replay.Replayed)
			require.Equal(t, 1, client.AuditLog.Query().CountX(ctx))
			require.Equal(t, 1, client.IncidentEvent.Query().CountX(ctx))
		})
	}
}

func TestIncidentReassignmentRequiresReason(t *testing.T) {
	client, svc, ctx := setupIncidentTest(t)
	defer client.Close()
	tenant, err := createIncidentTestTenant(ctx, client, "reason")
	require.NoError(t, err)
	actor, err := createIncidentTestUser(ctx, client, tenant.ID, "reason-owner")
	require.NoError(t, err)
	actor.Update().SetRole("super_admin").ExecX(ctx)
	next, err := createIncidentTestUser(ctx, client, tenant.ID, "reason-next")
	require.NoError(t, err)
	inc := createAutomationIncident(t, ctx, client, tenant.ID, actor.ID, "reason")
	before := client.Ticket.UpdateOneID(inc.WorkItemID).SetStatus("new").SetAssigneeID(actor.ID).SaveX(ctx)
	_, err = svc.ApplyIncidentCommand(ctx, dto.IncidentCommand{IncidentID: inc.ID, Action: "assign", AssigneeID: next.ID, Reason: "  ", Meta: workitemmutation.Meta{TenantID: tenant.ID, ActorID: actor.ID, ExpectedVersion: before.Version, Source: "http", OperationID: "reason"}})
	require.Error(t, err)
	after := client.Ticket.GetX(ctx, before.ID)
	require.Equal(t, before.Version, after.Version)
	require.Equal(t, actor.ID, after.AssigneeID)
	require.Zero(t, client.AuditLog.Query().CountX(ctx))
}

func TestIncidentGenericEditCannotAssign(t *testing.T) {
	client, svc, ctx := setupIncidentTest(t)
	defer client.Close()
	tenant, err := createIncidentTestTenant(ctx, client, "edit-assign")
	require.NoError(t, err)
	actor, err := createIncidentTestUser(ctx, client, tenant.ID, "edit-owner")
	require.NoError(t, err)
	next, err := createIncidentTestUser(ctx, client, tenant.ID, "edit-next")
	require.NoError(t, err)
	inc := createAutomationIncident(t, ctx, client, tenant.ID, actor.ID, "edit")
	before := client.Ticket.GetX(ctx, inc.WorkItemID)
	_, err = svc.UpdateIncident(ctx, inc.ID, &dto.UpdateIncidentRequest{Version: before.Version, AssigneeID: &next.ID}, tenant.ID)
	require.Error(t, err)
	require.Equal(t, before.Version, client.Ticket.GetX(ctx, before.ID).Version)
}

func assignIncidentForTest(t *testing.T, svc *IncidentService, ctx context.Context, id, assigneeID, tenantID int) (*dto.IncidentResponse, error) {
	t.Helper()
	inc := svc.client.Incident.GetX(ctx, id)
	item := svc.client.Ticket.GetX(ctx, inc.WorkItemID)
	actor := svc.client.User.GetX(ctx, item.RequesterID)
	actor.Update().SetRole("super_admin").ExecX(ctx)
	_, err := svc.ApplyIncidentCommand(ctx, dto.IncidentCommand{IncidentID: id, Action: "assign", AssigneeID: assigneeID, Reason: "test handover", Meta: workitemmutation.Meta{TenantID: tenantID, ActorID: actor.ID, ExpectedVersion: item.Version, Source: "http", OperationID: fmt.Sprintf("assign-%d-%d", item.Version, assigneeID)}})
	if err != nil {
		return nil, err
	}
	return svc.GetIncident(ctx, id, tenantID)
}

func TestIncidentAssignmentRuleUsesCommand(t *testing.T) {
	client, svc, ctx := setupIncidentTest(t)
	defer client.Close()
	tenant, err := createIncidentTestTenant(ctx, client, "rule-assign")
	require.NoError(t, err)
	actor, err := createIncidentTestUser(ctx, client, tenant.ID, "rule-owner")
	require.NoError(t, err)
	actor.Update().SetRole("super_admin").ExecX(ctx)
	next, err := createIncidentTestUser(ctx, client, tenant.ID, "rule-next")
	require.NoError(t, err)
	inc := createAutomationIncident(t, ctx, client, tenant.ID, actor.ID, "rule-assign")
	inc.Edges.WorkItem = client.Ticket.UpdateOneID(inc.WorkItemID).SetStatus("in_progress").SetAssigneeID(actor.ID).SaveX(ctx)
	ctx = WithIncidentAlertActor(ctx, actor.ID, "incident_rule", "assign-rule-attempt")
	action := &AssignmentAction{AssigneeID: next.ID, Reason: "route to support", client: client, logger: svc.logger}
	require.NoError(t, action.Execute(ctx, inc, tenant.ID))
	require.NoError(t, action.Execute(ctx, inc, tenant.ID))
	after := client.Ticket.GetX(ctx, inc.WorkItemID)
	require.Equal(t, "in_progress", after.Status)
	require.Equal(t, next.ID, after.AssigneeID)
	require.Equal(t, inc.Edges.WorkItem.Version+1, after.Version)
	require.Equal(t, 1, client.IncidentEvent.Query().CountX(ctx))
	require.Equal(t, 1, client.AuditLog.Query().CountX(ctx))
}

func TestIncidentEscalationAssignmentRequiresTrustedActor(t *testing.T) {
	client, _, ctx := setupIncidentTest(t)
	defer client.Close()
	tenant, err := createIncidentTestTenant(ctx, client, "escalation-actor")
	require.NoError(t, err)
	actor, err := createIncidentTestUser(ctx, client, tenant.ID, "escalation-actor")
	require.NoError(t, err)
	inc := createAutomationIncident(t, ctx, client, tenant.ID, actor.ID, "escalation-actor")
	inc.Edges.WorkItem = client.Ticket.GetX(ctx, inc.WorkItemID)
	before := inc.Edges.WorkItem
	owner := NewIncidentEscalationService(client)
	rule := client.IncidentEscalationRule.Create().SetTenantID(tenant.ID).SetName("handover after escalation").SetTriggerType("time_based").SetTriggerMinutes(1).SetTargetAssigneeType("user").SetEscalationLevel(1).SetTargetAssigneeID(actor.ID).SaveX(ctx)
	_, err = owner.escalateIncident(ctx, inc, rule)
	require.Error(t, err)
	require.Equal(t, before.Version, client.Ticket.GetX(ctx, before.ID).Version)
	require.Zero(t, client.AuditLog.Query().CountX(ctx))
}

func TestIncidentAssignmentRejectsUnsupportedStatus(t *testing.T) {
	for _, status := range []string{"resolved", "closed", "cancelled", "unknown"} {
		t.Run(status, func(t *testing.T) {
			client, svc, ctx := setupIncidentTest(t)
			defer client.Close()
			tenant, err := createIncidentTestTenant(ctx, client, "blocked-assign")
			require.NoError(t, err)
			actor, err := createIncidentTestUser(ctx, client, tenant.ID, "blocked-owner")
			require.NoError(t, err)
			actor.Update().SetRole("super_admin").ExecX(ctx)
			inc := createAutomationIncident(t, ctx, client, tenant.ID, actor.ID, "blocked-assign")
			before := client.Ticket.UpdateOneID(inc.WorkItemID).SetStatus(status).SaveX(ctx)
			_, err = svc.ApplyIncidentCommand(ctx, dto.IncidentCommand{IncidentID: inc.ID, Action: "assign", AssigneeID: actor.ID, Meta: workitemmutation.Meta{TenantID: tenant.ID, ActorID: actor.ID, ExpectedVersion: before.Version, Source: "http", OperationID: "blocked-assign"}})
			require.Error(t, err)
			require.Equal(t, before.Version, client.Ticket.GetX(ctx, before.ID).Version)
			require.Zero(t, client.AuditLog.Query().CountX(ctx))
		})
	}
}
