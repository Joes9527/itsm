package service

import (
	"context"
	"encoding/json"
	"fmt"
	"strconv"
	"time"

	"itsm-backend/connector"

	"go.uber.org/zap"
)

// WebhookEventSubscriber 事件驱动的 Webhook 推送订阅方。
//
// 订阅领域事件（如 sla.breached），按租户查找已配置的 webhook 连接器实例，
// 将事件信封 JSON 以 HTTP POST 推送（复用 connector 的 HMAC 签名与重试语义）。
// 推送失败返回错误触发 Watermill Nack 重投。
type WebhookEventSubscriber struct {
	manager *connector.Manager
	logger  *zap.SugaredLogger
}

// NewWebhookEventSubscriber 创建 Webhook 事件推送订阅方
func NewWebhookEventSubscriber(manager *connector.Manager, logger *zap.SugaredLogger) *WebhookEventSubscriber {
	return &WebhookEventSubscriber{manager: manager, logger: logger}
}

// Handle implements shared.EventHandler。
func (s *WebhookEventSubscriber) Handle(event interface{}) error {
	raw, ok := event.(map[string]interface{})
	if !ok {
		return fmt.Errorf("unexpected event shape %T", event)
	}

	eventType, _ := raw["eventType"].(string)
	tenantID := 0
	if v, ok := raw["tenantId"].(string); ok {
		tenantID, _ = strconv.Atoi(v)
	}
	if tenantID <= 0 {
		return fmt.Errorf("event missing valid tenantId")
	}

	payload, err := json.Marshal(raw)
	if err != nil {
		return fmt.Errorf("marshal event: %w", err)
	}

	// 按租户查找已配置的 webhook 连接器实例
	configs := s.manager.ListByTenant(tenantID)
	sent := 0
	for _, cfg := range configs {
		if cfg.Name != "webhook" {
			continue
		}

		ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		msg := &connector.Message{
			Type:    "text",
			Title:   fmt.Sprintf("ITSM 事件: %s", eventType),
			Content: string(payload),
			Metadata: map[string]interface{}{
				"eventType": eventType,
			},
		}
		err := s.manager.Send(ctx, tenantID, "webhook", msg)
		cancel()
		if err != nil {
			s.logger.Warnw("webhook event push failed", "error", err, "tenant_id", tenantID, "event_type", eventType)
			return err // Nack 重投
		}
		sent++
	}

	if sent == 0 {
		// 该租户未配置 webhook——静默跳过，不阻塞事件流
		s.logger.Debugw("no webhook connector configured for tenant, skip", "tenant_id", tenantID, "event_type", eventType)
		return nil
	}

	s.logger.Debugw("webhook event pushed", "tenant_id", tenantID, "event_type", eventType, "instances", sent)
	return nil
}

// WebhookEventTopics 需要推送的事件 topic 列表
func WebhookEventTopics() []string {
	return []string{
		"sla.breached",
	}
}
