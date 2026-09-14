package eventbus

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"sync"
	"time"

	"github.com/ThreeDotsLabs/watermill-redisstream/pkg/redisstream"
	"github.com/ThreeDotsLabs/watermill/message"
	"github.com/google/uuid"
	"github.com/redis/go-redis/v9"
	"go.uber.org/zap"
	"itsm-backend/config"
)

// redisDurableSubscriber owns only transport progress. NACK leaves the entry in
// the PEL for a later claim; it never prevents reading the next fresh entry.
// The existing business owner must remain idempotent under concurrent claims.
type redisDurableSubscriber struct {
	client          *redis.Client
	group, consumer string
	settings        config.EventStreamConfig
	logger          *zap.SugaredLogger
	mu              sync.Mutex
	closed          bool
	closeDone       chan struct{}
	closeErr        error
	topics          map[string]context.CancelFunc
	work            sync.WaitGroup
}

func newRedisDurableSubscriber(client *redis.Client, group string, settings config.EventStreamConfig, logger *zap.SugaredLogger) (*redisDurableSubscriber, error) {
	if client == nil || logger == nil || !strings.HasPrefix(group, "itsm:") || !consumerIDPattern.MatchString(strings.TrimPrefix(group, "itsm:")) {
		return nil, fmt.Errorf("durable subscriber dependencies required")
	}
	if settings.ClaimIdle < 0 || settings.ClaimInterval < 0 || settings.NackDelay < 0 {
		return nil, fmt.Errorf("invalid durable subscriber timing")
	}
	if settings.ClaimIdle == 0 {
		settings.ClaimIdle = 60 * time.Second
	}
	if settings.ClaimInterval == 0 {
		settings.ClaimInterval = 5 * time.Second
	}
	if settings.NackDelay == 0 {
		settings.NackDelay = time.Second
	}
	return &redisDurableSubscriber{client: client, group: group, consumer: uuid.NewString(), settings: settings, logger: logger, topics: map[string]context.CancelFunc{}, closeDone: make(chan struct{})}, nil
}

func (s *redisDurableSubscriber) Subscribe(parent context.Context, topic string) (<-chan *message.Message, error) {
	if parent == nil || topic == "" {
		return nil, fmt.Errorf("subscription context and topic required")
	}
	s.mu.Lock()
	if s.closed || s.topics[topic] != nil {
		s.mu.Unlock()
		return nil, fmt.Errorf("durable subscription unavailable")
	}
	ctx, cancel := context.WithCancel(parent)
	s.topics[topic] = cancel
	s.work.Add(1)
	s.mu.Unlock()
	cleanup := func() { cancel(); s.mu.Lock(); delete(s.topics, topic); s.mu.Unlock(); s.work.Done() }
	// Explicit startup requires Redis 6.2+ and actual recovery permission. The
	// first claim is handed to the normal delivery loop; it is never discarded.
	err := s.client.XGroupCreateMkStream(ctx, topic, s.group, "0").Err()
	if err != nil && err.Error() != "BUSYGROUP Consumer Group name already exists" {
		cleanup()
		return nil, fmt.Errorf("durable group unavailable: %w", err)
	}
	initial, cursor, err := s.client.XAutoClaim(ctx, &redis.XAutoClaimArgs{Stream: topic, Group: s.group, Consumer: s.consumer, MinIdle: s.settings.ClaimIdle, Start: "0-0", Count: 1}).Result()
	if err != nil {
		cleanup()
		return nil, fmt.Errorf("durable recovery command unavailable")
	}
	output := make(chan *message.Message)
	go func() { defer cleanup(); defer close(output); s.consume(ctx, topic, output, initial, cursor) }()
	return output, nil
}

func (s *redisDurableSubscriber) consume(ctx context.Context, topic string, output chan<- *message.Message, initial []redis.XMessage, cursor string) {
	for _, entry := range initial {
		if !s.deliver(ctx, topic, entry, output) {
			return
		}
	}
	nextClaim := time.Now().Add(s.settings.ClaimInterval)
	for ctx.Err() == nil {
		// One bounded recovery page, then one bounded fresh read. A nonzero cursor
		// is preserved even when a scan returns no eligible entry.
		if !time.Now().Before(nextClaim) {
			entries, next, err := s.client.XAutoClaim(ctx, &redis.XAutoClaimArgs{Stream: topic, Group: s.group, Consumer: s.consumer, MinIdle: s.settings.ClaimIdle, Start: cursor, Count: 1}).Result()
			nextClaim = time.Now().Add(s.settings.ClaimInterval)
			if err != nil {
				if !s.backoff(ctx, "claim_failed") {
					return
				}
			} else {
				cursor = next
				for _, entry := range entries {
					if !s.deliver(ctx, topic, entry, output) {
						return
					}
				}
			}
		}
		streams, err := s.client.XReadGroup(ctx, &redis.XReadGroupArgs{Group: s.group, Consumer: s.consumer, Streams: []string{topic, ">"}, Count: 1, Block: 100 * time.Millisecond}).Result()
		if err == redis.Nil {
			continue
		}
		if err != nil {
			if !s.backoff(ctx, "read_failed") {
				return
			}
			continue
		}
		for _, stream := range streams {
			for _, entry := range stream.Messages {
				if !s.deliver(ctx, topic, entry, output) {
					return
				}
			}
		}
	}
}

func (s *redisDurableSubscriber) deliver(ctx context.Context, topic string, entry redis.XMessage, output chan<- *message.Message) bool {
	msg, err := decodeDurableEntry(entry)
	if err != nil {
		// Hash raw fields for a traceable observation; never repair a malformed wire
		// into a valid envelope or invoke its business owner.
		raw, _ := json.Marshal(entry.Values)
		invalid := message.NewMessage(entry.ID, raw)
		recordStreamRejection(ctx, s.client, s.logger, strings.TrimPrefix(s.group, "itsm:"), streamRoute{topic: topic}, invalid, "wire_invalid")
		return ctx.Err() == nil
	}
	deliveryCtx, cancel := context.WithCancel(ctx)
	defer cancel()
	msg.SetContext(deliveryCtx)
	select {
	case output <- msg:
	case <-ctx.Done():
		return false
	}
	select {
	case <-msg.Acked():
		if err := s.client.XAck(ctx, topic, s.group, entry.ID).Err(); err != nil {
			return s.backoff(ctx, "ack_failed")
		}
	case <-msg.Nacked(): // Intentionally no XACK; future claim revalidates the source.
	case <-ctx.Done():
		return false
	}
	return ctx.Err() == nil
}

func decodeDurableEntry(entry redis.XMessage) (*message.Message, error) {
	id, ok := entry.Values[redisstream.UUIDHeaderKey].(string)
	if !ok || id == "" {
		return nil, fmt.Errorf("invalid stream identity")
	}
	if _, ok := entry.Values["payload"].(string); !ok {
		return nil, fmt.Errorf("invalid stream payload")
	}
	if metadata, exists := entry.Values["metadata"]; exists {
		if _, ok := metadata.(string); !ok {
			return nil, fmt.Errorf("invalid stream metadata")
		}
	}
	return (redisstream.DefaultMarshallerUnmarshaller{}).Unmarshal(entry.Values)
}

func (s *redisDurableSubscriber) backoff(ctx context.Context, reason string) bool {
	if ctx.Err() != nil {
		return false
	}
	s.logger.Errorw("Durable stream operation failed", "reason", reason, "consumer_group", s.group)
	timer := time.NewTimer(s.settings.NackDelay)
	defer timer.Stop()
	select {
	case <-timer.C:
		return true
	case <-ctx.Done():
		return false
	}
}

func (s *redisDurableSubscriber) Close() error {
	s.mu.Lock()
	if s.closed {
		done := s.closeDone
		s.mu.Unlock()
		<-done
		return s.closeErr
	}
	s.closed = true
	for _, cancel := range s.topics {
		cancel()
	}
	s.mu.Unlock()
	s.work.Wait()
	err := s.client.Close()
	s.mu.Lock()
	s.closeErr = err
	close(s.closeDone)
	s.mu.Unlock()
	return err
}
