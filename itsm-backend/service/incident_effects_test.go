package service

import (
	"context"
	"errors"
	"fmt"
	"testing"

	"github.com/stretchr/testify/require"
	"itsm-backend/dto"
	"itsm-backend/ent"
)

// Losing the timeline must roll back the actual WorkItem mutation.
func TestIncidentEffectsUpdateTimelineRollback(t *testing.T) {
	client, svc, ctx := setupIncidentTest(t)
	defer client.Close()
	tenant, err := createIncidentTestTenant(ctx, client, "effects-update")
	require.NoError(t, err)
	actor, err := createIncidentTestUser(ctx, client, tenant.ID, "effects-update")
	require.NoError(t, err)
	inc := createAutomationIncident(t, ctx, client, tenant.ID, actor.ID, "effects-update")
	client.IncidentEvent.Use(func(next ent.Mutator) ent.Mutator {
		return ent.MutateFunc(func(ctx context.Context, m ent.Mutation) (ent.Value, error) {
			return nil, errors.New("timeline unavailable")
		})
	})
	status := "in_progress"
	_, err = svc.UpdateIncident(ctx, inc.ID, &dto.UpdateIncidentRequest{Status: &status}, tenant.ID)
	require.ErrorContains(t, err, "timeline unavailable")
	require.Equal(t, "new", client.Ticket.GetX(ctx, inc.WorkItemID).Status)
}

func TestIncidentEffectsRejectPlaceholderEscalationBeforeMutation(t *testing.T) {
	client, svc, ctx := setupIncidentTest(t)
	defer client.Close()
	tenant, err := createIncidentTestTenant(ctx, client, "effects-auto")
	require.NoError(t, err)
	actor, err := createIncidentTestUser(ctx, client, tenant.ID, "effects-auto")
	require.NoError(t, err)
	inc := createAutomationIncident(t, ctx, client, tenant.ID, actor.ID, "effects-auto")
	_, err = svc.EscalateIncident(ctx, &dto.IncidentEscalationRequest{IncidentID: inc.ID, EscalationLevel: 1, Reason: "threshold", AutoAssign: true}, tenant.ID)
	require.Error(t, err)
	require.Zero(t, client.Incident.GetX(ctx, inc.ID).EscalationLevel, "unsupported options must not mutate")
}
func TestIncidentEffectsNotificationRequiresConfiguredRecipients(t *testing.T) {
	e := &IncidentRuleEngine{}
	_, err := e.parseNotificationAction(map[string]interface{}{"type": "notify", "channels": []string{"email"}})
	require.Error(t, err, "missing recipients must not become a company fallback")
}

// A restart after a committed metric must use the frozen selection and receipt.
func TestIncidentEffectsCreatedEventReplay(t *testing.T) {
	client, svc, ctx := setupIncidentTest(t)
	defer client.Close()
	tenant, err := createIncidentTestTenant(ctx, client, "effects-replay")
	require.NoError(t, err)
	actor, err := createIncidentTestUser(ctx, client, tenant.ID, "effects-replay")
	require.NoError(t, err)
	inc := createAutomationIncident(t, ctx, client, tenant.ID, actor.ID, "effects-replay")
	client.IntakeRequest.Create().SetTenantID(tenant.ID).SetActorID(actor.ID).SetRequesterID(actor.ID).SetChannel("api").SetOperation("create").SetIdempotencyKey("one").SetRequestDigest("digest").SetDigestVersion("v1").SetStatus("completed").SetWorkItemID(inc.WorkItemID).SaveX(ctx)
	payload := []byte(fmt.Sprintf(`{"tenantId":%d,"incidentId":%d,"workItemId":%d,"actorId":%d,"channel":"api"}`, tenant.ID, inc.ID, inc.WorkItemID, actor.ID))
	event := client.OutboxEvent.Create().SetEventID(fmt.Sprintf("incident-created:%d", inc.WorkItemID)).SetEventType("incident.created").SetTenantID(tenant.ID).SetAggregateType("work_item").SetAggregateID(fmt.Sprint(inc.WorkItemID)).SetPayload(payload).SaveX(ctx)
	rule := client.IncidentRule.Create().SetName("metric").SetRuleType("metric").SetTenantID(tenant.ID).SetConditions(map[string]interface{}{}).SetActions([]map[string]interface{}{{"type": "collect_metric", "metric_type": "automation", "metric_name": "created", "metric_value": 1.0}}).SaveX(ctx)
	consumer, ok := any(svc.RuleEngine()).(interface {
		ExecuteCreatedEvent(context.Context, *ent.OutboxEvent) error
	})
	require.True(t, ok, "rule engine must implement recoverable creation consumption")
	require.NoError(t, consumer.ExecuteCreatedEvent(ctx, event))
	rule.Update().SetActions([]map[string]interface{}{{"type": "collect_metric", "metric_type": "automation", "metric_name": "changed", "metric_value": 2.0}}).SaveX(ctx)
	require.NoError(t, consumer.ExecuteCreatedEvent(ctx, event))
	require.Equal(t, 1, client.IncidentMetric.Query().CountX(ctx))
	require.Equal(t, "created", client.IncidentMetric.Query().OnlyX(ctx).MetricName)
}

func TestIncidentEffectsRejectLossyRuleIDs(t *testing.T){
 for _,value:=range []interface{}{1.5,9007199254740992.0,"1.5","9223372036854775808"}{
  _,ok:=toInt(value);require.False(t,ok,"unsafe rule integer %v",value)
 }
}
