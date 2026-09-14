package bootstrap

import (
	"testing"

	"github.com/stretchr/testify/require"
	"itsm-backend/config"
	"itsm-backend/service"
)

func TestWebhookOutboxRegistrationFollowsCapability(t *testing.T) {
	handler := service.NewWebhookDeliveryHandler(nil, nil, nil)
	for _, entry := range []struct {
		mode, cap string
		enabled   bool
	}{{"candidate", "scoped", true}, {"candidate", "disabled", false}, {"standard", "enabled", true}, {"standard", "disabled", false}, {"candidate", "", false}} {
		registry, err := newOutboxRegistry(config.ExecutionConfig{Mode: entry.mode, Capabilities: map[string]string{"webhook": entry.cap, "outbox": "scoped"}}, []service.OutboxDeliveryHandler{service.NewSLABreachDeliveryHandler()}, handler)
		require.NoError(t, err)
		require.Contains(t, registry.KnownTypes(), service.WebhookDeliveryRequestedEventType)
		if entry.enabled {
			require.Same(t, handler, registry.Handler(service.WebhookDeliveryRequestedEventType))
		} else {
			require.Nil(t, registry.Handler(service.WebhookDeliveryRequestedEventType))
			require.NotContains(t, registry.HandlerTypes(), service.WebhookDeliveryRequestedEventType)
		}
	}
}
