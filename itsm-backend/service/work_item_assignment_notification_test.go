package service

import (
	"context"
	"encoding/json"
	"testing"

	"github.com/stretchr/testify/require"
	"itsm-backend/common/tenantctx"
	"itsm-backend/ent"
	assignment "itsm-backend/handlers/common/workitemassignment"
)

func TestWorkItemAssignmentNotificationRejectsForgedPayload(t *testing.T) {
	p := assignment.Event{RecordClass: "generic", Command: assignment.Command{WorkItemID: 2, TenantID: 1, ActorTenantID: 1, ActorID: 3, AssigneeID: 4, ExpectedVersion: 1, Source: "workflow"}, PreviousAssigneeID: 0, Version: 2, EventID: "work-item-assigned:1:2:2"}
	data, err := json.Marshal(p)
	require.NoError(t, err)
	good := ent.OutboxEvent{EventID: p.EventID, EventType: "work_item.assigned", TenantID: 1, AggregateType: "work_item", AggregateID: "2", Payload: data, ExecutionWorkItemID: func() *int { id := 2; return &id }()}
	_, err = decodeWorkItemAssignment(&good)
	require.NoError(t, err)
	err = NewWorkItemAssignmentNotificationHandler(nil, nil).Deliver(tenantctx.WithTenantID(context.Background(), 2), &good)
	var scopeBlocked *outboxDeliveryBlockedError
	require.ErrorAs(t, err, &scopeBlocked)
	for _, change := range []func(*ent.OutboxEvent){
		func(e *ent.OutboxEvent) { e.TenantID = 2 }, func(e *ent.OutboxEvent) { e.AggregateID = "9" }, func(e *ent.OutboxEvent) { e.EventID = "forged" },
		func(e *ent.OutboxEvent) { e.Payload = append(append([]byte{}, data...), []byte(" {}")...) },
		func(e *ent.OutboxEvent) { e.EventType = "unknown" },
	} {
		event := good
		change(&event)
		err := NewWorkItemAssignmentNotificationHandler(nil, nil).Deliver(context.Background(), &event)
		var blocked *outboxDeliveryBlockedError
		require.ErrorAs(t, err, &blocked)
	}
}

func TestWorkItemAssignmentWriterRequiresValidatedSession(t *testing.T) {
	writer := NewWorkItemAssignmentWriter(nil)
	_, err := writer.Apply(context.Background(), nil, assignment.Command{WorkItemID: 1, TenantID: 1, ActorTenantID: 1, ActorID: 1, ExpectedVersion: 1, Source: "manual"})
	require.Error(t, err)
}

func TestWorkItemAssignmentEventRequiresRecordClass(t *testing.T) {
	for _, recordClass := range []string{"", "unknown", "incident"} {
		t.Run(recordClass, func(t *testing.T) {
			payload := map[string]any{"command": assignment.Command{WorkItemID: 2, TenantID: 1, ActorTenantID: 1, ActorID: 3, AssigneeID: 4, ExpectedVersion: 1, Source: "workflow"}, "previousAssigneeId": 0, "version": 2, "eventId": "work-item-assigned:1:2:2"}
			if recordClass != "" {
				payload["recordClass"] = recordClass
			}
			data, err := json.Marshal(payload)
			require.NoError(t, err)
			_, err = decodeWorkItemAssignment(&ent.OutboxEvent{EventID: "work-item-assigned:1:2:2", EventType: assignment.EventType, TenantID: 1, AggregateType: "work_item", AggregateID: "2", Payload: data, ExecutionWorkItemID: func() *int { id := 2; return &id }()})
			if recordClass == "incident" {
				require.NoError(t, err)
			} else {
				require.Error(t, err)
			}
		})
	}
}
