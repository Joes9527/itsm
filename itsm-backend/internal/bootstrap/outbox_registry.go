package bootstrap

import (
	"itsm-backend/config"
	"itsm-backend/service"
)

// Disabled webhook delivery remains known but unclaimed. It must not be treated
// as an unknown event or enabled merely because the general outbox worker runs.
func newOutboxRegistry(execution config.ExecutionConfig, handlers []service.OutboxDeliveryHandler, webhook service.OutboxDeliveryHandler) (*service.OutboxEventTypeRegistry, error) {
	reserved := []string{service.KafDelegateRequestedEventType}
	if execution.Enabled("webhook") {
		handlers = append(append([]service.OutboxDeliveryHandler(nil), handlers...), webhook)
	} else {
		reserved = append(reserved, service.WebhookDeliveryRequestedEventType)
	}
	return service.NewOutboxEventTypeRegistry(handlers, reserved...)
}
