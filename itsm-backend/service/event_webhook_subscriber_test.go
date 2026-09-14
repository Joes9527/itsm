package service

import (
	"context"
	"fmt"
	"itsm-backend/common/tenantctx"
	"itsm-backend/pkg/eventbus"
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"testing"

	"itsm-backend/connector"
	_ "itsm-backend/connector/builtin/webhook" // 触发 webhook 连接器 init 注册
	executionfixture "itsm-backend/tests/fixtures/execution"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"go.uber.org/zap/zaptest"
)

// provisionTestWebhook 在 Manager 中配置一个指向测试服务器的 webhook 连接器
func provisionTestWebhook(t *testing.T, manager *connector.Manager, tenantID int, url string) {
	t.Helper()
	cfg := connector.Config{
		Name:     "webhook",
		TenantID: tenantID,
		Enabled:  true,
		Settings: map[string]interface{}{
			"url": url,
		},
	}
	require.NoError(t, manager.Provision(tenantctx.WithTenantID(t.Context(), cfg.TenantID), cfg))
}

func TestWebhookEventSubscriber_PushesToConfiguredWebhook(t *testing.T) {
	var received atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { received.Add(1); w.WriteHeader(http.StatusOK) }))
	defer server.Close()
	manager := connector.NewManager(connector.Default(), zaptest.NewLogger(t).Sugar(), executionfixture.Standard())
	defer manager.CloseAll()
	provisionTestWebhook(t, manager, 7, server.URL)
	sub := NewWebhookEventSubscriber(manager, zaptest.NewLogger(t).Sugar(), nil, executionfixture.Standard())
	// A configured endpoint does not authorize raw synchronous delivery.
	require.Error(t, sub.Handle(map[string]interface{}{"eventType": "sla.breached", "tenantId": "7", "ticketId": "28"}))
	require.Zero(t, received.Load())
	var _ eventbus.ExecutionEnvelopeHandler = sub

}

func TestWebhookEventSubscriber_RejectsTenantWithoutWebhook(t *testing.T) {
	manager := connector.NewManager(connector.Default(), zaptest.NewLogger(t).Sugar(), executionfixture.Standard())
	sub := NewWebhookEventSubscriber(manager, zaptest.NewLogger(t).Sugar(), nil, executionfixture.Standard())

	// Required dispatch without a target must not be acknowledged as success.
	event := map[string]interface{}{
		"eventType": "sla.breached",
		"tenantId":  "99",
	}
	require.Error(t, sub.Handle(event))
}

func TestWebhookEventSubscriber_RejectsMissingTenant(t *testing.T) {
	manager := connector.NewManager(connector.Default(), zaptest.NewLogger(t).Sugar(), executionfixture.Standard())
	sub := NewWebhookEventSubscriber(manager, zaptest.NewLogger(t).Sugar(), nil, executionfixture.Standard())

	err := sub.Handle(map[string]interface{}{"eventType": "sla.breached"})
	require.Error(t, err)
}

func TestWebhookEventTopics(t *testing.T) {
	assert.Contains(t, WebhookEventTopics(), "sla.breached")
}

func TestWebhookEventSubscriber_RejectsUnknownEventBeforeSend(t *testing.T) {
	var count atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { count.Add(1); w.WriteHeader(http.StatusOK) }))
	defer server.Close()
	manager := connector.NewManager(connector.Default(), zaptest.NewLogger(t).Sugar(), executionfixture.Standard())
	defer manager.CloseAll()
	provisionTestWebhook(t, manager, 7, server.URL)
	sub := NewWebhookEventSubscriber(manager, zaptest.NewLogger(t).Sugar(), nil, executionfixture.Standard())
	require.Error(t, sub.Handle(map[string]interface{}{"eventType": "unknown.action", "tenantId": "7"}))
	require.Zero(t, count.Load())
}

func TestWebhookEventSubscriber_SendsToEachDeclaredInstance(t *testing.T) {
	manager := connector.NewManager(connector.Default(), zaptest.NewLogger(t).Sugar(), executionfixture.Standard())
	defer manager.CloseAll()
	counts := make([]atomic.Int32, 3)
	for i := range counts {
		server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { counts[i].Add(1); w.WriteHeader(http.StatusOK) }))
		defer server.Close()
		require.NoError(t, manager.Provision(tenantctx.WithTenantID(t.Context(), 7), connector.Config{Name: "webhook", Provider: fmt.Sprint(i), TenantID: 7, Enabled: true, Settings: map[string]interface{}{"url": server.URL}}))
	}
	sub := NewWebhookEventSubscriber(manager, zaptest.NewLogger(t).Sugar(), nil, executionfixture.Standard())
	for attempt := int32(1); attempt <= 10; attempt++ {
		require.Error(t, sub.Handle(map[string]interface{}{"eventType": "sla.breached", "tenantId": "7"}))
		for i := range counts {
			require.Zero(t, counts[i].Load(), "raw replay must not send to any declared target")
		}
	}
}

func TestWebhookEventSubscriber_UsesDeliveryContext(t *testing.T) {
	manager := connector.NewManager(connector.Default(), zaptest.NewLogger(t).Sugar(), executionfixture.Standard())
	defer manager.CloseAll()
	var count atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { count.Add(1); w.WriteHeader(http.StatusOK) }))
	defer server.Close()
	provisionTestWebhook(t, manager, 7, server.URL)
	sub := NewWebhookEventSubscriber(manager, zaptest.NewLogger(t).Sugar(), nil, executionfixture.Standard())
	handler, ok := interface{}(sub).(eventbus.ContextEventHandler)
	require.True(t, ok, "subscriber must accept transport cancellation and tenant context")
	event := map[string]interface{}{"eventType": "sla.breached", "tenantId": "7"}
	ctx, cancel := context.WithCancel(t.Context())
	cancel()
	require.ErrorIs(t, handler.HandleContext(ctx, event), context.Canceled)
	require.Error(t, handler.HandleContext(tenantctx.WithTenantID(t.Context(), 8), event))
	require.Error(t, handler.HandleContext(tenantctx.SystemContext(t.Context(), "webhook-test", "no bypass"), event))
	require.Zero(t, count.Load())
	require.Error(t, handler.HandleContext(tenantctx.WithTenantID(t.Context(), 7), event))
	require.Zero(t, count.Load())
}

func TestWebhookExactInstanceDispatchDoesNotFallback(t *testing.T) {
	manager := connector.NewManager(connector.Default(), zaptest.NewLogger(t).Sugar(), executionfixture.Standard())
	defer manager.CloseAll()
	var count atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { count.Add(1); w.WriteHeader(http.StatusOK) }))
	defer server.Close()
	cfg := connector.Config{Name: "webhook", Provider: "chosen", TenantID: 7, Enabled: true, Settings: map[string]interface{}{"url": server.URL}}
	require.NoError(t, manager.Provision(tenantctx.WithTenantID(t.Context(), cfg.TenantID), cfg))
	msg := &connector.Message{Type: "text", Content: "test"}
	_, _, ok := manager.GetInstance(8, "webhook", "chosen")
	require.False(t, ok)
	_, _, ok = manager.GetInstance(7, "webhook", "missing")
	require.False(t, ok)
	require.Zero(t, count.Load())
	chosen, _, ok := manager.GetInstance(7, "webhook", "chosen")
	require.True(t, ok)
	require.NoError(t, chosen.Send(t.Context(), msg))
	require.NoError(t, manager.Revoke(tenantctx.WithTenantID(t.Context(), cfg.TenantID), cfg))
	provisionTestWebhook(t, manager, 7, server.URL)
	_, _, ok = manager.GetInstance(7, "webhook", "chosen")
	require.False(t, ok)
	require.EqualValues(t, 1, count.Load(), "revoked target cannot fall back to remaining instance")
}
