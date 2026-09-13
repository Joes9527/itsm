package service

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
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
	require.NoError(t, manager.Provision(t.Context(), cfg))
}

func TestWebhookEventSubscriber_PushesToConfiguredWebhook(t *testing.T) {
	var receivedBody []byte
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		receivedBody, _ = io.ReadAll(r.Body)
		w.WriteHeader(http.StatusOK)
	}))
	defer server.Close()

	manager := connector.NewManager(connector.Default(), zaptest.NewLogger(t).Sugar())
	provisionTestWebhook(t, manager, 7, server.URL)

	sub := NewWebhookEventSubscriber(manager, zaptest.NewLogger(t).Sugar(), nil, executionfixture.Standard())

	event := map[string]interface{}{
		"eventType":  "sla.breached",
		"tenantId":   "7",
		"occurredAt": "2026-08-14T10:00:00Z",
		"ticketId":   "28",
	}

	require.NoError(t, sub.Handle(event))
	require.NotEmpty(t, receivedBody, "webhook server should receive a POST body")

	var payload map[string]interface{}
	require.NoError(t, json.Unmarshal(receivedBody, &payload))
	// Manager.Send 通过连接器发送 Content——验证消息体含事件类型
	assert.Contains(t, string(receivedBody), "sla.breached")
}

func TestWebhookEventSubscriber_RejectsTenantWithoutWebhook(t *testing.T) {
	manager := connector.NewManager(connector.Default(), zaptest.NewLogger(t).Sugar())
	sub := NewWebhookEventSubscriber(manager, zaptest.NewLogger(t).Sugar(), nil, executionfixture.Standard())

	// Required dispatch without a target must not be acknowledged as success.
	event := map[string]interface{}{
		"eventType": "sla.breached",
		"tenantId":  "99",
	}
	require.Error(t, sub.Handle(event))
}

func TestWebhookEventSubscriber_RejectsMissingTenant(t *testing.T) {
	manager := connector.NewManager(connector.Default(), zaptest.NewLogger(t).Sugar())
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
	manager := connector.NewManager(connector.Default(), zaptest.NewLogger(t).Sugar())
	defer manager.CloseAll()
	provisionTestWebhook(t, manager, 7, server.URL)
	sub := NewWebhookEventSubscriber(manager, zaptest.NewLogger(t).Sugar(), nil, executionfixture.Standard())
	require.Error(t, sub.Handle(map[string]interface{}{"eventType": "unknown.action", "tenantId": "7"}))
	require.Zero(t, count.Load())
}

func TestWebhookEventSubscriber_SendsToEachDeclaredInstance(t *testing.T) {
	manager := connector.NewManager(connector.Default(), zaptest.NewLogger(t).Sugar())
	defer manager.CloseAll()
	counts := make([]atomic.Int32, 3)
	for i := range counts {
		server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { counts[i].Add(1); w.WriteHeader(http.StatusOK) }))
		defer server.Close()
		require.NoError(t, manager.Provision(t.Context(), connector.Config{Name: "webhook", Provider: fmt.Sprint(i), TenantID: 7, Enabled: true, Settings: map[string]interface{}{"url": server.URL}}))
	}
	sub := NewWebhookEventSubscriber(manager, zaptest.NewLogger(t).Sugar(), nil, executionfixture.Standard())
	for attempt := int32(1); attempt <= 10; attempt++ {
		require.NoError(t, sub.Handle(map[string]interface{}{"eventType": "sla.breached", "tenantId": "7"}))
		for i := range counts {
			require.Equal(t, attempt, counts[i].Load(), "every declared instance must receive this event once")
		}
	}
}

func TestWebhookEventSubscriber_UsesDeliveryContext(t *testing.T) {
	manager := connector.NewManager(connector.Default(), zaptest.NewLogger(t).Sugar())
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
	require.NoError(t, handler.HandleContext(tenantctx.WithTenantID(t.Context(), 7), event))
	require.EqualValues(t, 1, count.Load())
}

func TestWebhookExactInstanceDispatchDoesNotFallback(t *testing.T) {
	manager := connector.NewManager(connector.Default(), zaptest.NewLogger(t).Sugar())
	defer manager.CloseAll()
	var count atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { count.Add(1); w.WriteHeader(http.StatusOK) }))
	defer server.Close()
	cfg := connector.Config{Name: "webhook", Provider: "chosen", TenantID: 7, Enabled: true, Settings: map[string]interface{}{"url": server.URL}}
	require.NoError(t, manager.Provision(t.Context(), cfg))
	msg := &connector.Message{Type: "text", Content: "test"}
	require.Error(t, manager.SendToInstance(t.Context(), 8, "webhook", "chosen", msg))
	require.Error(t, manager.SendToInstance(t.Context(), 7, "webhook", "missing", msg))
	require.Zero(t, count.Load())
	require.NoError(t, manager.SendToInstance(t.Context(), 7, "webhook", "chosen", msg))
	manager.Revoke(cfg)
	provisionTestWebhook(t, manager, 7, server.URL)
	require.Error(t, manager.SendToInstance(t.Context(), 7, "webhook", "chosen", msg))
	require.EqualValues(t, 1, count.Load(), "revoked target cannot fall back to remaining instance")
}
