package service

import (
	"fmt"
	"sort"
	"strings"
)

// OutboxEventTypeRegistry is the single dispatch authority for the shared
// outbox table. Reserved types are known but owned by specialised dispatchers.
type OutboxEventTypeRegistry struct {
	handlers map[string]OutboxDeliveryHandler
	reserved map[string]struct{}
}

func NewOutboxEventTypeRegistry(handlers []OutboxDeliveryHandler, reserved ...string) (*OutboxEventTypeRegistry, error) {
	r := &OutboxEventTypeRegistry{handlers: make(map[string]OutboxDeliveryHandler), reserved: make(map[string]struct{})}
	for _, eventType := range reserved {
		eventType = strings.TrimSpace(eventType)
		if eventType == "" {
			return nil, fmt.Errorf("reserved outbox event type is required")
		}
		if _, exists := r.reserved[eventType]; exists {
			return nil, fmt.Errorf("duplicate reserved outbox event type: %s", eventType)
		}
		r.reserved[eventType] = struct{}{}
	}
	for _, handler := range handlers {
		if handler == nil {
			return nil, fmt.Errorf("outbox delivery handler is required")
		}
		eventType := strings.TrimSpace(handler.EventType())
		if eventType == "" {
			return nil, fmt.Errorf("outbox delivery handler event type is required")
		}
		if _, reserved := r.reserved[eventType]; reserved {
			return nil, fmt.Errorf("outbox event type %s is reserved", eventType)
		}
		if _, exists := r.handlers[eventType]; exists {
			return nil, fmt.Errorf("duplicate outbox delivery handler: %s", eventType)
		}
		r.handlers[eventType] = handler
	}
	if len(r.handlers) == 0 {
		return nil, fmt.Errorf("at least one outbox delivery handler is required")
	}
	return r, nil
}

func (r *OutboxEventTypeRegistry) HandlerTypes() []string {
	types := make([]string, 0, len(r.handlers))
	for eventType := range r.handlers {
		types = append(types, eventType)
	}
	sort.Strings(types)
	return types
}

func (r *OutboxEventTypeRegistry) KnownTypes() []string {
	types := r.HandlerTypes()
	for eventType := range r.reserved {
		types = append(types, eventType)
	}
	sort.Strings(types)
	return types
}

func (r *OutboxEventTypeRegistry) Handler(eventType string) OutboxDeliveryHandler {
	return r.handlers[eventType]
}
