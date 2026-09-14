package service

import (
	"context"
	"github.com/stretchr/testify/require"
	"itsm-backend/ent"
	"testing"
	"time"
)

type mutableOrderedHandler struct{ ordered bool }

func (*mutableOrderedHandler) EventType() string                               { return "ordered-test" }
func (h *mutableOrderedHandler) SerialByAggregate() bool                       { return h.ordered }
func (*mutableOrderedHandler) Deliver(context.Context, *ent.OutboxEvent) error { return nil }
func TestOutboxRegistryFreezesOrderingDeclaration(t *testing.T) {
	for _, initial := range []bool{true, false} {
		handler := &mutableOrderedHandler{ordered: initial}
		registry, err := NewOutboxEventTypeRegistry([]OutboxDeliveryHandler{handler}, "reserved")
		require.NoError(t, err)
		handler.ordered = !initial
		require.Equal(t, initial, registry.SerialByAggregate(handler.EventType()))
		require.False(t, registry.SerialByAggregate("reserved"))
		require.False(t, registry.SerialByAggregate("unknown"))
	}
}
func TestOutboxOrderedClaimsRequireEventType(t *testing.T) {
	repo := NewOutboxEventRepository(nil, nil)
	_, err := repo.ClaimDueByEventType(context.Background(), time.Now(), 1, "", true)
	require.ErrorContains(t, err, "registered event type")
}
