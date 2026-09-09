package service

import (
	"github.com/stretchr/testify/require"
	"itsm-backend/dto"
	"itsm-backend/handlers/shared/workitemmutation"
	"testing"
)

func TestIncidentStatusEventUsesFrozenStateAndReplays(t *testing.T) {
	client, svc, ctx := setupIncidentTest(t)
	defer client.Close()
	tenant, err := createIncidentTestTenant(ctx, client, "status-effects")
	require.NoError(t, err)
	actor, err := createIncidentTestUser(ctx, client, tenant.ID, "status-effects")
	require.NoError(t, err)
	actor.Update().SetRole("super_admin").ExecX(ctx)
	inc := createAutomationIncident(t, ctx, client, tenant.ID, actor.ID, "status-effects")
	item := client.Ticket.UpdateOneID(inc.WorkItemID).SetStatus("in_progress").SaveX(ctx)
	_, err = svc.ApplyIncidentCommand(ctx, dto.IncidentCommand{Meta: workitemmutation.Meta{TenantID: tenant.ID, ActorID: actor.ID, ExpectedVersion: item.Version, Source: "http", OperationID: "resolve"}, IncidentID: inc.ID, Action: "resolve", Resolution: "service restored"})
	require.NoError(t, err)
	event := client.OutboxEvent.Query().OnlyX(ctx)
	client.IncidentRule.Create().SetName("resolved metric").SetRuleType("metric").SetTenantID(tenant.ID).SetConditions(map[string]interface{}{"event_type": []string{"incident.status_changed"}, "status": []interface{}{"resolved"}}).SetActions([]map[string]interface{}{{"type": "collect_metric", "metric_type": "automation", "metric_name": "resolved", "metric_value": 1.0}}).SaveX(ctx)
	client.Ticket.UpdateOneID(item.ID).SetStatus("in_progress").ExecX(ctx)
	client.IncidentRule.Create().SetName("legacy creation rule").SetRuleType("metric").SetTenantID(tenant.ID).SetConditions(map[string]interface{}{}).SetActions([]map[string]interface{}{{"type": "collect_metric", "metric_type": "automation", "metric_name": "legacy must not run", "metric_value": 1.0}}).SaveX(ctx)
	consumer := NewIncidentStatusDeliveryHandler(svc.RuleEngine())
	require.NoError(t, consumer.Deliver(ctx, event))
	require.NoError(t, consumer.Deliver(ctx, event))
	require.Equal(t, 1, client.IncidentMetric.Query().CountX(ctx))
}
