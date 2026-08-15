package service

import (
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"testing"

	"itsm-backend/connector"
	_ "itsm-backend/connector/builtin/webhook" // 触发 webhook 连接器 init 注册

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"go.uber.org/zap/zaptest"
)

// provisionTestWebhook 在 Manager 中配置一个指向测试服务器的 webhook 连接器
func provisionTestWebhook(t *testing.T, manager *connector.Manager, tenantID int, url string) {
	t.Helper()
	cfg := connector.Config{
		Name:    "webhook",
		TenantID: tenantID,
		Enabled: true,
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

	sub := NewWebhookEventSubscriber(manager, zaptest.NewLogger(t).Sugar())

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

func TestWebhookEventSubscriber_SkipsTenantWithoutWebhook(t *testing.T) {
	manager := connector.NewManager(connector.Default(), zaptest.NewLogger(t).Sugar())
	sub := NewWebhookEventSubscriber(manager, zaptest.NewLogger(t).Sugar())

	// 租户 99 无任何 webhook 配置——应静默跳过而非报错
	event := map[string]interface{}{
		"eventType": "sla.breached",
		"tenantId":  "99",
	}
	require.NoError(t, sub.Handle(event))
}

func TestWebhookEventSubscriber_RejectsMissingTenant(t *testing.T) {
	manager := connector.NewManager(connector.Default(), zaptest.NewLogger(t).Sugar())
	sub := NewWebhookEventSubscriber(manager, zaptest.NewLogger(t).Sugar())

	err := sub.Handle(map[string]interface{}{"eventType": "sla.breached"})
	require.Error(t, err)
}

func TestWebhookEventTopics(t *testing.T) {
	assert.Contains(t, WebhookEventTopics(), "sla.breached")
}
