package service_test

import (
	"itsm-backend/ent/outboxevent"
	"strconv"
	"testing"

	"itsm-backend/dto"
	"itsm-backend/ent/incidentmetric"
	"itsm-backend/ent/incidentruleexecution"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestIncidentCreationUsesFormalRuleEngine(t *testing.T) {
	client, incidentService, ctx := setupIncidentTest(t)
	defer client.Close()
	tenant, err := createIncidentTestTenant(ctx, client, "formal-rule-engine")
	require.NoError(t, err)
	reporter, err := createIncidentTestUser(ctx, client, tenant.ID, "formal-rule-engine")
	require.NoError(t, err)
	_, err = client.IncidentRule.Create().
		SetName("Collect creation metric").
		SetRuleType("metric").
		SetConditions(map[string]interface{}{"priority": []string{"high"}}).
		SetActions([]map[string]interface{}{{
			"type": "collect_metric", "metric_type": "automation", "metric_name": "rule_applied",
			"metric_value": 1.0, "unit": "count",
		}}).
		SetIsActive(true).
		SetTenantID(tenant.ID).
		Save(ctx)
	require.NoError(t, err)
	created, err := incidentService.SubmitCreation(ctx, &dto.CreateIncidentRequest{
		Title: "Formal engine incident", Priority: "high", Severity: "high",
	}, tenant.ID, reporter.ID)
	require.NoError(t, err)

	require.Zero(t, client.IncidentMetric.Query().CountX(ctx), "creation effects wait for durable delivery")
	event := client.OutboxEvent.Query().Where(outboxevent.EventTypeEQ("incident.created"), outboxevent.AggregateIDEQ(strconv.Itoa(*created.WorkItemID))).OnlyX(ctx)
	require.NoError(t, incidentService.RuleEngine().Deliver(ctx, event))
	require.NoError(t, incidentService.RuleEngine().Deliver(ctx, event))
	require.Equal(t, 1, client.IncidentMetric.Query().Where(incidentmetric.IncidentIDEQ(created.ID)).CountX(ctx))
	execution, err := client.IncidentRuleExecution.Query().
		Where(incidentruleexecution.IncidentIDEQ(created.ID), incidentruleexecution.ExecutionKindEQ("rule")).
		Only(ctx)
	require.NoError(t, err)
	assert.Equal(t, "completed", execution.Status)
}
func TestIncidentCreationUnknownRuleActionNeverCompletes(t *testing.T) {
	client, incidentService, ctx := setupIncidentTest(t)
	defer client.Close()
	tenant, err := createIncidentTestTenant(ctx, client, "unknown-rule-action")
	require.NoError(t, err)
	reporter, err := createIncidentTestUser(ctx, client, tenant.ID, "unknown-rule-action")
	require.NoError(t, err)
	rule, err := client.IncidentRule.Create().
		SetName("Unsupported action").
		SetRuleType("automation").
		SetConditions(map[string]interface{}{"priority": []string{"high"}}).
		SetActions([]map[string]interface{}{{"type": "not_registered"}}).
		SetIsActive(true).
		SetTenantID(tenant.ID).
		Save(ctx)
	require.NoError(t, err)

	created, err := incidentService.SubmitCreation(ctx, &dto.CreateIncidentRequest{
		Title: "Unknown rule action", Priority: "high", Severity: "high",
	}, tenant.ID, reporter.ID)
	require.NoError(t, err)

	event := client.OutboxEvent.Query().Where(outboxevent.EventTypeEQ("incident.created"), outboxevent.AggregateIDEQ(strconv.Itoa(*created.WorkItemID))).OnlyX(ctx)
	require.Error(t, incidentService.RuleEngine().Deliver(ctx, event))
	require.Error(t, incidentService.RuleEngine().Deliver(ctx, event))

	execution, err := client.IncidentRuleExecution.Query().
		Where(
			incidentruleexecution.RuleIDEQ(rule.ID),
			incidentruleexecution.IncidentIDEQ(created.ID),
		).
		Only(ctx)
	require.NoError(t, err)
	require.NotEqual(t, "completed", execution.Status)
	require.Zero(t, client.IncidentRuleActionReceipt.Query().CountX(ctx))
}
