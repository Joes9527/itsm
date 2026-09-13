package service

import (
	"context"

	"itsm-backend/common/executionscope"
	"itsm-backend/connector"
	"itsm-backend/database"
	"itsm-backend/ent"

	"go.uber.org/zap"
)

// WebhookEventSubscriber validates persistent sources and atomically enqueues
// per-target intents in both modes. Only the shared outbox worker sends them.
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
	return s.consumeExecutionWebhook(ctx, event)
}

// WebhookEventTopics 需要推送的事件 topic 列表
func WebhookEventTopics() []string {
	return []string{
		"sla.breached",
	}
}

func (*WebhookEventSubscriber) EventConsumerID() string { return "webhook" }

func (*WebhookEventSubscriber) ExecutionEnvelopeRequired() {}
