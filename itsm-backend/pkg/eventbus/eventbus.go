package eventbus

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"sync"
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
	EventID    string             `json:"eventId,omitempty"`
	Execution  *ExecutionIdentity `json:"execution,omitempty"`
	EventType  string             `json:"eventType"`
	TenantID   string             `json:"tenantId"`
	OccurredAt time.Time          `json:"occurredAt"`
	Payload    json.RawMessage    `json:"payload"`
}

// WatermillEventBus implements shared.EventBus interface using Watermill with Redis Stream
type WatermillEventBus struct {
	authority           EventAuthority
	routes              *streamRoutes
	publisher           message.Publisher
	subscriber          streamSubscriber
	newSubscriber       func(string) (streamSubscriber, error)
	ownedSubscribers    map[string]streamSubscriber
	establishing        sync.WaitGroup
	activeSubscriptions map[string]bool
	logger              *zap.SugaredLogger
	mu                  sync.Mutex
	started             bool
	closed              bool
	closeDone           chan struct{}
	closeErr            error
	ctx                 context.Context
	cancel              context.CancelFunc
	consumers           sync.WaitGroup
	subscriptions       []subscription
}

// ExecutionEnvelopeHandler requires persistent identity and source validation
// before invocation in every execution mode. It never accepts flattened payloads.
type ExecutionEnvelopeHandler interface {
	shared.EventHandler
	ExecutionEnvelopeRequired()
}

type ContextEventHandler interface {
	HandleContext(context.Context, interface{}) error
}

type streamSubscriber interface {
	Subscribe(context.Context, string) (<-chan *message.Message, error)
	Close() error
}
type subscription struct {
	consumer string
	topic    string
	handler  shared.EventHandler
}

// RegisterSubscription only describes runtime work; it does not touch Redis.
func (eb *WatermillEventBus) RegisterSubscription(topic string, handler shared.EventHandler) error {
	eb.mu.Lock()
	defer eb.mu.Unlock()
	if eb.started || eb.closed || topic == "" || handler == nil {
		return fmt.Errorf("cannot register event subscription")
	}
	consumer, err := eb.consumerIdentity(handler)
	if err != nil {
		return err
	}
	if consumer != "" {
		for _, existing := range eb.subscriptions {
			if existing.topic == topic && existing.consumer == consumer {
				return fmt.Errorf("duplicate durable event subscription")
			}
		}
	}
	eb.subscriptions = append(eb.subscriptions, subscription{consumer: consumer, topic: topic, handler: handler})
	return nil
}

func (eb *WatermillEventBus) Start(ctx context.Context) error {
	eb.mu.Lock()
	if ctx == nil || eb.started || eb.closed {
		eb.mu.Unlock()
		return fmt.Errorf("event runtime cannot start")
	}
	if err := ctx.Err(); err != nil {
		eb.mu.Unlock()
		return err
	}
	eb.ctx, eb.cancel = context.WithCancel(ctx)
	eb.started = true
	subscriptions := append([]subscription(nil), eb.subscriptions...)
	eb.mu.Unlock()
	for _, sub := range subscriptions {
		if err := eb.subscribe(sub.topic, sub.consumer, sub.handler); err != nil {
			_ = eb.Close()
			return err
		}
	}
	return nil
}

// NewWatermillEventBus creates a new WatermillEventBus instance
func NewWatermillEventBus(cfg *config.RedisConfig, execution config.ExecutionConfig, authority EventAuthority, logger *zap.SugaredLogger) (*WatermillEventBus, error) {
	routes, err := newStreamRoutes(execution)
	if err != nil {
		return nil, fmt.Errorf("invalid event execution configuration: %w", err)
	}
	if routes.candidate && authority == nil {
		return nil, fmt.Errorf("persistent event authority required")
	}
	if cfg == nil || logger == nil {
		return nil, fmt.Errorf("event Redis configuration and logger required")
	}
	stream := cfg.EventStream
	if stream.ClaimIdle < 0 || stream.ClaimInterval < 0 || stream.NackDelay < 0 {
		return nil, fmt.Errorf("event stream durations cannot be negative")
	}
	if stream.NackDelay == 0 {
		stream.NackDelay = time.Second
	}
	// Publisher and each logical subscriber own and close their Redis client.
	options := &redis.Options{
		Addr:     fmt.Sprintf("%s:%d", cfg.Host, cfg.Port),
		Password: cfg.Password,
		DB:       cfg.DB,
	}
	publisherClient := redis.NewClient(options)

	// Watermill logger
	watermillLogger := NewZapLoggerAdapter(logger)

	// Create publisher
	publisher, err := redisstream.NewPublisher(
		redisstream.PublisherConfig{
			Client: publisherClient,
		},
		watermillLogger,
	)
	if err != nil {
		_ = publisherClient.Close()
		return nil, fmt.Errorf("failed to create publisher: %w", err)
	}

	makeSubscriber := func(group string) (streamSubscriber, error) {
		client := redis.NewClient(options)
		settings := redisstream.SubscriberConfig{Client: client, DisableIndefiniteInitialBlock: true}
		if group != "" {
			settings.ConsumerGroup = group
			settings.OldestId = "0"
			settings.ClaimInterval = stream.ClaimInterval
			settings.MaxIdleTime = stream.ClaimIdle
			settings.NackResendSleep = stream.NackDelay
		}
		subscriber, err := redisstream.NewSubscriber(settings, watermillLogger)
		if err != nil {
			_ = client.Close()
			return nil, err
		}
		return subscriber, nil
	}
	bus := &WatermillEventBus{authority: authority, routes: routes, publisher: publisher, logger: logger}
	bus.newSubscriber = makeSubscriber
	if !routes.candidate {
		bus.subscriber, err = makeSubscriber("")
		if err != nil {
			_ = publisher.Close()
			return nil, fmt.Errorf("failed to create subscriber: %w", err)
		}
	}
	return bus, nil
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
	if _, persistent := event.(ExecutionEvent); persistent && !isStable {
		return fmt.Errorf("persistent event must declare stable topic and tenant")
	}
	if eb.routes == nil {
		return fmt.Errorf("event transport execution configuration required")
	}
	if eb.routes.candidate && !isStable {
		return fmt.Errorf("candidate event must declare a stable topic and tenant")
	}
	tenant := ""
	if isStable {
		tenant = se.TenantID()
	}
	topic := resolveTopic(event)
	physicalTopic, routeErr := eb.routes.publishTopic(topic, tenant)
	if routeErr != nil {
		return routeErr
	}

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
		source, persistent := event.(ExecutionEvent)
		if eb.routes.candidate || persistent {
			ok := persistent
			if !ok {
				return fmt.Errorf("candidate event requires a persistent source")
			}
			ref, routeErr := eb.routes.refFor(tenant)
			if routeErr != nil {
				return routeErr
			}
			env.EventID = source.PersistentEventID()
			env.Execution = &ExecutionIdentity{DeploymentID: ref.DeploymentID, ScopeID: ref.ScopeID, WorkItemID: source.ExecutionWorkItemID()}
			wire, encodeErr := json.Marshal(env)
			if encodeErr != nil {
				return encodeErr
			}
			if _, decodeErr := DecodeExecutionEnvelope(wire); decodeErr != nil {
				return decodeErr
			}
			if eb.authority == nil {
				return fmt.Errorf("persistent event authority required")
			}
			checkCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
			checkErr := eb.authority.ValidateEvent(checkCtx, ref, env)
			cancel()
			if checkErr != nil {
				return fmt.Errorf("event source rejected: %w", checkErr)
			}
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

	// Create message
	messageID := watermill.NewUUID()
	if source, persistent := event.(ExecutionEvent); isStable && persistent {
		messageID = source.PersistentEventID()
	}
	msg := message.NewMessage(messageID, payload)
	msg.Metadata.Set("event_type", topic)

	// Publish to Redis Stream
	if err := eb.publisher.Publish(physicalTopic, msg); err != nil {
		return fmt.Errorf("failed to publish event: %w", err)
	}

	eb.logger.Debugw("Event published", "event_type", topic, "event_id", msg.UUID)
	return nil
}

// Subscribe subscribes to events of a specific type.
// 订阅 topic 使用稳定事件类型名（如 "ticket.created"）。
func (eb *WatermillEventBus) Subscribe(eventType string, handler shared.EventHandler) error {
	consumer, err := eb.consumerIdentity(handler)
	if err != nil {
		return err
	}
	return eb.subscribe(eventType, consumer, handler)
}

func (eb *WatermillEventBus) subscribe(eventType, consumer string, handler shared.EventHandler) error {
	routes, err := eb.routes.subscriptionRoutes(eventType)
	if err != nil {
		return err
	}
	for index, route := range routes {
		if err := eb.subscribeRoute(eventType, consumer, route, handler); err != nil {
			// A partially established candidate subscription cannot remain live
			// after reporting failure. Stop the runtime without resetting groups.
			if eb.routes.candidate && index > 0 {
				return errors.Join(err, eb.Close())
			}
			return err
		}
	}
	return nil
}

func (eb *WatermillEventBus) subscribeRoute(eventType, consumer string, route streamRoute, handler shared.EventHandler) error {
	eb.mu.Lock()
	if !eb.started || eb.closed || handler == nil || eventType == "" {
		eb.mu.Unlock()
		return fmt.Errorf("event runtime is not accepting subscriptions")
	}
	key := consumer + ":" + route.topic
	if consumer != "" && eb.activeSubscriptions[key] {
		eb.mu.Unlock()
		return fmt.Errorf("durable event subscription already started")
	}
	subscriber, err := eb.subscriberForLocked(consumer)
	if err != nil {
		eb.mu.Unlock()
		return fmt.Errorf("event subscriber unavailable: %w", err)
	}
	if subscriber == nil {
		eb.mu.Unlock()
		return fmt.Errorf("event subscriber unavailable")
	}
	if eb.activeSubscriptions == nil {
		eb.activeSubscriptions = map[string]bool{}
	}
	eb.activeSubscriptions[key] = true
	ctx := eb.ctx
	eb.consumers.Add(1)
	eb.establishing.Add(1)
	eb.mu.Unlock()
	defer eb.establishing.Done()
	// Subscribe may create only a group in the frozen candidate namespace.
	messages, err := subscriber.Subscribe(ctx, route.topic)
	if err != nil {
		eb.mu.Lock()
		delete(eb.activeSubscriptions, key)
		eb.mu.Unlock()
		eb.consumers.Done()
		return fmt.Errorf("failed to subscribe to event type %s: %w", eventType, err)
	}

	// Start message processing goroutine
	go func() {
		defer eb.consumers.Done()
		for msg := range messages {
			var deliver interface{}
			// Required persistent identity is checked before invoking its owner.
			_, typed := handler.(ExecutionEnvelopeHandler)
			if eb.routes.candidate || typed {
				env, decodeErr := DecodeExecutionEnvelope(msg.Payload)
				tenant := env.TenantID
				if eb.routes.candidate {
					tenant = fmt.Sprint(route.tenantID)
				}
				ref, refErr := eb.routes.refFor(tenant)
				if decodeErr == nil && refErr == nil {
					decodeErr = validateEnvelopeRoute(env, ref, eventType)
				}
				if decodeErr == nil && refErr == nil && (msg.UUID != env.EventID || msg.Metadata.Get("event_type") != eventType) {
					decodeErr = fmt.Errorf("event transport identity mismatch")
				}
				if decodeErr == nil && refErr == nil {
					if eb.authority == nil {
						decodeErr = fmt.Errorf("persistent event authority required")
					} else {
						decodeErr = eb.authority.ValidateEvent(ctx, ref, env)
					}
				}
				if decodeErr != nil || refErr != nil {
					eb.logger.Errorw("Persistent event rejected", "event_type", eventType, "error", errors.Join(decodeErr, refErr))
					msg.Nack()
					continue
				}
				deliver = env
			} else {
				var err error
				deliver, err = unwrapEnvelope(msg.Payload)
				if err != nil {
					eb.logger.Errorw("Failed to unwrap event payload", "event_type", eventType, "error", err)
					msg.Nack()
					continue
				}
			}

			var handleErr error
			if contextual, ok := handler.(ContextEventHandler); ok {
				handleErr = contextual.HandleContext(ctx, deliver)
			} else {
				handleErr = handler.Handle(deliver)
			}
			if err := handleErr; err != nil {
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
			if execution, ok := probe["execution"]; ok {
				merged["execution"] = execution
				merged["eventId"] = probe["eventId"]
			}
			return merged, nil
		}
	}
	return probe, nil
}

// Close closes the event bus
func (eb *WatermillEventBus) Close() error {
	eb.mu.Lock()
	if eb.closed {
		done := eb.closeDone
		eb.mu.Unlock()
		<-done
		return eb.closeErr
	}
	eb.closed = true
	eb.closeDone = make(chan struct{})
	if eb.cancel != nil {
		eb.cancel()
	}
	eb.mu.Unlock()
	eb.establishing.Wait()
	var err error
	if eb.subscriber != nil {
		err = eb.subscriber.Close()
	}
	for _, subscriber := range eb.ownedSubscribers {
		err = errors.Join(err, subscriber.Close())
	}
	eb.consumers.Wait()
	err = errors.Join(err, eb.publisher.Close())
	eb.mu.Lock()
	eb.closeErr = err
	close(eb.closeDone)
	eb.mu.Unlock()
	return err
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
