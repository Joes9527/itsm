package service

import (
	"context"
	"encoding/json"
	"errors"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
	"itsm-backend/ent"
	"itsm-backend/handlers/shared"
	"itsm-backend/pkg/eventbus"
)

type slaTestBus struct {
	events []interface{}
	err    error
}

func (b *slaTestBus) Publish(e interface{}) error               { b.events = append(b.events, e); return b.err }
func (*slaTestBus) Subscribe(string, shared.EventHandler) error { return nil }

func TestSLABreachDeliveryContract(t *testing.T) {
	previous := eventbus.GetGlobalEventBus()
	t.Cleanup(func() { eventbus.SetGlobalEventBus(previous) })
	id := 42
	at := time.Date(2026, 9, 13, 1, 2, 3, 0, time.UTC)
	payload := slaBreachDeliveryPayload{Version: 1, EventID: "sla-event", TenantID: 7, TicketID: id, ViolationID: 9, SLAPolicyID: 3, BreachedType: "response", BreachedAt: at}
	raw, err := json.Marshal(payload)
	require.NoError(t, err)
	row := &ent.OutboxEvent{EventID: payload.EventID, EventType: slaBreachEventType, TenantID: 7, ExecutionWorkItemID: &id, AggregateType: "sla_violation", AggregateID: "9", Payload: raw}
	handler := NewSLABreachDeliveryHandler()
	eventbus.SetGlobalEventBus(nil)
	var blocked *outboxDeliveryBlockedError
	require.ErrorAs(t, handler.Deliver(context.Background(), row), &blocked)
	bus := &slaTestBus{}
	eventbus.SetGlobalEventBus(bus)
	require.NoError(t, handler.Deliver(context.Background(), row))
	require.Len(t, bus.events, 1)
	published := bus.events[0].(*persistedSLABreachEvent)
	require.Equal(t, "sla.breached", published.EventType())
	require.Equal(t, "7", published.TenantID())
	require.Equal(t, at, published.OccurredAt())
	raw, err = json.Marshal(published)
	require.NoError(t, err)
	require.JSONEq(t, `{"ticket_id":"42","sla_policy_id":"3","breached_type":"response","breached_at":"2026-09-13T01:02:03Z","event_id":"sla-event"}`, string(raw))
	wrong := *row
	wrong.ExecutionWorkItemID = nil
	require.ErrorAs(t, handler.Deliver(context.Background(), &wrong), &blocked)
	require.Len(t, bus.events, 1)
	bus.err = errors.New("publisher accepted then connection lost")
	err = handler.Deliver(context.Background(), row)
	require.ErrorAs(t, err, &blocked)
	require.Contains(t, err.Error(), "delivery_unknown")
	require.Len(t, bus.events, 2)
}
