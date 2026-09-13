package service

import (
	"context"
	"encoding/json"
	"fmt"
	"strconv"
	"time"

	"itsm-backend/common/executionscope"
	"itsm-backend/common/tenantctx"
	"itsm-backend/connector"
	"itsm-backend/database"
	"itsm-backend/ent"

	"go.uber.org/zap"
)

// WebhookEventSubscriber 事件驱动的 Webhook 推送订阅方。
//
// 订阅领域事件（如 sla.breached），按租户查找已配置的 webhook 连接器实例，
// 将事件信封 JSON 以 HTTP POST 推送（复用 connector 的 HMAC 签名与重试语义）。
// 推送失败返回错误触发 Watermill Nack 重投。
type WebhookEventSubscriber struct {
	client  *ent.Client
	policy  *database.ExecutionPolicy
	manager *connector.Manager
	logger  *zap.SugaredLogger
}

// NewWebhookEventSubscriber 创建 Webhook 事件推送订阅方
func NewWebhookEventSubscriber(manager *connector.Manager, logger *zap.SugaredLogger, client *ent.Client, policy *database.ExecutionPolicy) *WebhookEventSubscriber {
	return &WebhookEventSubscriber{manager: manager, logger: logger, client: client, policy: policy}
}

// Handle implements shared.EventHandler。
func (s *WebhookEventSubscriber) Handle(event interface{}) error {
	return s.HandleContext(context.Background(), event)
}

func (s *WebhookEventSubscriber) HandleContext(ctx context.Context, event interface{}) error {
	if s == nil || s.manager == nil || s.policy == nil || ctx == nil {
		return executionscope.ErrDenied
	}
	if err := ctx.Err(); err != nil {
		return err
	}
	if s.policy.IsCandidate() {
		return s.consumeExecutionWebhook(ctx, event)
	}
	raw, ok := event.(map[string]interface{})
	if !ok {
		return fmt.Errorf("unexpected event shape %T", event)
	}

	eventType, _ := raw["eventType"].(string)
	known := false
	for _, topic := range WebhookEventTopics() {
		if topic == eventType {
			known = true
			break
		}
	}
	if !known {
		return fmt.Errorf("unsupported webhook event type")
	}
	tenantID := 0
	if v, ok := raw["tenantId"].(string); ok {
		tenantID, _ = strconv.Atoi(v)
	}
	if tenantID <= 0 {
		return fmt.Errorf("event missing valid tenantId")
	}
	if tenant, ok := tenantctx.TenantID(ctx); ok && tenant != tenantID {
		return executionscope.ErrDenied
	}
	if tenantctx.IsSystemBypass(ctx) {
		return executionscope.ErrDenied
	}
	ctx = tenantctx.WithTenantID(ctx, tenantID)

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

		ctx, cancel := context.WithTimeout(ctx, 10*time.Second)
		msg := &connector.Message{
			Type:    "text",
			Title:   fmt.Sprintf("ITSM 事件: %s", eventType),
			Content: string(payload),
			Metadata: map[string]interface{}{
				"eventType": eventType,
			},
		}
		err := s.manager.SendToInstance(ctx, tenantID, "webhook", cfg.Provider, msg)
		cancel()
		if err != nil {
			s.logger.Warnw("webhook event push failed", "error", err, "tenant_id", tenantID, "event_type", eventType)
			return err // Nack 重投
		}
		sent++
	}

	if sent == 0 {
		return fmt.Errorf("webhook event has no configured target")
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

func (*WebhookEventSubscriber) EventConsumerID() string { return "webhook" }
