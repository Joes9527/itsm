package service

import (
	"encoding/json"
	"testing"

	"github.com/stretchr/testify/require"
	"itsm-backend/dto"
	"itsm-backend/ent/outboxevent"
	"itsm-backend/handlers/shared/workitemmutation"
)

func TestIncidentInitialAssignmentEventDeliveryPreservesProvenance(t *testing.T) {
	client, svc, ctx := setupIncidentTest(t)
	defer client.Close()
	tenant, err := createIncidentTestTenant(ctx, client, "assignment-delivery")
	require.NoError(t, err)
	actor, err := createIncidentTestUser(ctx, client, tenant.ID, "assignment-delivery")
	require.NoError(t, err)
	actor.Update().SetRole("super_admin").ExecX(ctx)
	inc := createAutomationIncident(t, ctx, client, tenant.ID, actor.ID, "assignment-delivery")
	item := client.Ticket.UpdateOneID(inc.WorkItemID).SetStatus("new").SaveX(ctx)
	_, err = svc.ApplyIncidentCommand(ctx, dto.IncidentCommand{Meta: workitemmutation.Meta{TenantID: tenant.ID, ActorID: actor.ID, ExpectedVersion: item.Version, Source: "http", OperationID: "initial-assignment"}, IncidentID: inc.ID, Action: "assign", AssigneeID: actor.ID, Reason: "Assign candidate owner"})
	require.NoError(t, err)
	// Status rules and shared owner notification are separate durable facts.
	require.Equal(t, 2, client.OutboxEvent.Query().CountX(ctx))
	require.Equal(t, 1, client.OutboxEvent.Query().Where(outboxevent.EventType("work_item.assigned")).CountX(ctx))
	event := client.OutboxEvent.Query().Where(outboxevent.EventType("incident.status_changed")).OnlyX(ctx)
	consumer := NewIncidentStatusDeliveryHandler(svc.RuleEngine())
	require.NoError(t, consumer.Deliver(ctx, event))
	require.NoError(t, consumer.Deliver(ctx, event))
	require.Equal(t, 2, client.OutboxEvent.Query().CountX(ctx), "status consumer replay must not synthesize another assignment")
	require.Zero(t, client.TicketNotification.Query().CountX(ctx), "status rule consumer must not materialize assignment delivery")
	var payload map[string]interface{}
	require.NoError(t, json.Unmarshal(event.Payload, &payload))
	payload["previousAssigneeId"] = float64(actor.ID + 100)
	forged := *event
	forged.Payload, err = json.Marshal(payload)
	require.NoError(t, err)
	require.Error(t, consumer.Deliver(ctx, &forged), "previous owner must match durable provenance")
	require.NoError(t, json.Unmarshal(event.Payload, &payload))
	payload["unsupportedAssignmentField"] = true
	forged.Payload, err = json.Marshal(payload)
	require.NoError(t, err)
	require.Error(t, consumer.Deliver(ctx, &forged), "unknown fields still fail closed")
}
