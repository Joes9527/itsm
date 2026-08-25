package eventbus

import (
	"context"
	"encoding/json"
	"fmt"
	"time"

	"itsm-backend/config"
	"itsm-backend/handlers/shared"

	"github.com/ThreeDotsLabs/watermill"
	"github.com/ThreeDotsLabs/watermill-redisstream/pkg/redisstream"
	"github.com/ThreeDotsLabs/watermill/message"
	"github.com/redis/go-redis/v9"
	"go.uber.org/zap"
)

// stableEvent 声明稳定事件契约：EventType() 返回稳定 topic 名（如 "ticket.created"），
// 而非 Go 类型名。实现了该接口的事件将获得跨重构/跨服务的稳定 topic 与元数据信封。
type stableEvent interface {
	EventType() string
	TenantID() string
	OccurredAt() time.Time
}

// Envelope 事件信封：解决 BaseEvent 未导出字段被 JSON 序列化丢失的问题。
// 字段按 API 契约使用 camelCase。
type Envelope struct {
	EventType  string          `json:"eventType"`
	TenantID   string          `json:"tenantId"`
	OccurredAt time.Time       `json:"occurredAt"`
	Payload    json.RawMessage `json:"payload"`
}

// WatermillEventBus implements shared.EventBus interface using Watermill with Redis Stream
type WatermillEventBus struct {
	publisher  message.Publisher
	subscriber *redisstream.Subscriber
	logger     *zap.SugaredLogger
}

// NewWatermillEventBus creates a new WatermillEventBus instance
func NewWatermillEventBus(cfg *config.RedisConfig, logger *zap.SugaredLogger) (*WatermillEventBus, error) {
	// Create Redis client
	rdb := redis.NewClient(&redis.Options{
		Addr:     fmt.Sprintf("%s:%d", cfg.Host, cfg.Port),
		Password: cfg.Password,
		DB:       cfg.DB,
	})

	// Watermill logger
	watermillLogger := NewZapLoggerAdapter(logger)

	// Create publisher
	publisher, err := redisstream.NewPublisher(
		redisstream.PublisherConfig{
			Client: rdb,
		},
		watermillLogger,
	)
	if err != nil {
		return nil, fmt.Errorf("failed to create publisher: %w", err)
	}

	// Create subscriber
	subscriber, err := redisstream.NewSubscriber(
		redisstream.SubscriberConfig{
			Client: rdb,
		},
		watermillLogger,
	)
	if err != nil {
		_ = publisher.Close()
		return nil, fmt.Errorf("failed to create subscriber: %w", err)
	}

	return &WatermillEventBus{
		publisher:  publisher,
		subscriber: subscriber,
		logger:     logger,
	}, nil
}

// resolveTopic 解析事件 topic：
//   - 实现 stableEvent 的事件使用 EventType()（稳定、可文档化）
//   - 其余退化为 Go 类型名（兼容纯载荷发布，如 `map[string]interface{}`）
func resolveTopic(event interface{}) string {
	if se, ok := event.(stableEvent); ok {
		if t := se.EventType(); t != "" {
			return t
		}
	}
	return fmt.Sprintf("%T", event)
}

// Publish publishes an event to the event bus.
// 稳定事件（实现 EventType/TenantID/OccurredAt）包装为 Envelope 发送；
// 纯载荷按原样 JSON 序列化。
func (eb *WatermillEventBus) Publish(event interface{}) error {
	if event == nil {
		return fmt.Errorf("cannot publish nil event")
	}

	se, isStable := event.(stableEvent)

	var payload []byte
	var err error
	if isStable {
		raw, marshalErr := json.Marshal(event)
		if marshalErr != nil {
			return fmt.Errorf("failed to marshal event: %w", marshalErr)
		}
		env := Envelope{
			EventType:  se.EventType(),
			TenantID:   se.TenantID(),
			OccurredAt: se.OccurredAt(),
			Payload:    raw,
		}
		payload, err = json.Marshal(env)
		if err != nil {
			return fmt.Errorf("failed to marshal envelope: %w", err)
		}
	} else {
		payload, err = json.Marshal(event)
		if err != nil {
			return fmt.Errorf("failed to marshal event: %w", err)
		}
	}

	topic := resolveTopic(event)

	// Create message
	msg := message.NewMessage(watermill.NewUUID(), payload)
	msg.Metadata.Set("event_type", topic)

	// Publish to Redis Stream
	if err := eb.publisher.Publish(topic, msg); err != nil {
		return fmt.Errorf("failed to publish event: %w", err)
	}

	eb.logger.Debugw("Event published", "event_type", topic, "event_id", msg.UUID)
	return nil
}

// Subscribe subscribes to events of a specific type.
// 订阅 topic 使用稳定事件类型名（如 "ticket.created"）。
func (eb *WatermillEventBus) Subscribe(eventType string, handler shared.EventHandler) error {
	// Subscribe to the topic
	messages, err := eb.subscriber.Subscribe(context.Background(), eventType)
	if err != nil {
		return fmt.Errorf("failed to subscribe to event type %s: %w", eventType, err)
	}

	// Start message processing goroutine
	go func() {
		for msg := range messages {
			// Unwrap envelope (if present) and pass the raw payload JSON to the handler
			deliver, err := unwrapEnvelope(msg.Payload)
			if err != nil {
				eb.logger.Errorw("Failed to unwrap event payload", "event_type", eventType, "error", err)
				msg.Nack()
				continue
			}

			// Call handler
			if err := handler.Handle(deliver); err != nil {
				eb.logger.Errorw("Failed to handle event", "event_type", eventType, "error", err)
				msg.Nack()
				continue
			}

			// Acknowledge message
			msg.Ack()
		}
	}()

	eb.logger.Infow("Subscribed to event type", "event_type", eventType)
	return nil
}

// unwrapEnvelope 解包信封：若 payload 是 Envelope（含 eventType 字段），
// 将信封与载荷合体返回 map（含 eventType/tenantId/occurredAt/payload 展平后的字段）；
// 否则原样返回原始 JSON map。
// 订阅方 handler 收到的永远是 camelCase map，可直接按契约读取。
func unwrapEnvelope(raw []byte) (interface{}, error) {
	var probe map[string]interface{}
	if err := json.Unmarshal(raw, &probe); err != nil {
		return nil, fmt.Errorf("failed to unmarshal event payload: %w", err)
	}
	// 信封探测：同时存在 eventType 与 payload 字段
	if _, hasType := probe["eventType"]; hasType {
		if inner, ok := probe["payload"].(map[string]interface{}); ok {
			merged := make(map[string]interface{}, len(inner)+3)
			for k, v := range inner {
				merged[k] = v
			}
			merged["eventType"] = probe["eventType"]
			merged["tenantId"] = probe["tenantId"]
			merged["occurredAt"] = probe["occurredAt"]
			return merged, nil
		}
	}
	return probe, nil
}

// Close closes the event bus
func (eb *WatermillEventBus) Close() error {
	if err := eb.publisher.Close(); err != nil {
		return err
	}
	if err := eb.subscriber.Close(); err != nil {
		return err
	}
	return nil
}

// Global event bus instance
var globalEventBus shared.EventBus

// SetGlobalEventBus sets the global event bus instance
func SetGlobalEventBus(eb shared.EventBus) {
	globalEventBus = eb
}

// GetGlobalEventBus returns the global event bus instance
func GetGlobalEventBus() shared.EventBus {
	return globalEventBus
}
