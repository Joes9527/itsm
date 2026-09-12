package eventbus

import (
	"context"
	"encoding/json"
	"errors"
	"net"
	"strconv"
	"sync/atomic"
	"testing"
	"time"

	"github.com/ThreeDotsLabs/watermill/message"
	"github.com/alicebob/miniredis/v2"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"go.uber.org/zap"
	"itsm-backend/config"
)

type blockingClosePublisher struct {
	entered, release chan struct{}
	err              error
}

func (*blockingClosePublisher) Publish(string, ...*message.Message) error { return nil }
func (p *blockingClosePublisher) Close() error                            { close(p.entered); <-p.release; return p.err }

func TestConcurrentCloseWaitsForCompleteShutdown(t *testing.T) {
	failure := errors.New("publisher close failure")
	p := &blockingClosePublisher{entered: make(chan struct{}), release: make(chan struct{}), err: failure}
	eb := &WatermillEventBus{publisher: p, subscriber: &lifecycleSubscriber{}, logger: zap.NewNop().Sugar()}
	first, second := make(chan error, 1), make(chan error, 1)
	go func() { first <- eb.Close() }()
	<-p.entered
	go func() { second <- eb.Close() }()
	select {
	case <-second:
		close(p.release)
		t.Fatal("second close returned before resource closure")
	case <-time.After(20 * time.Millisecond):
	}
	close(p.release)
	require.ErrorIs(t, <-first, failure)
	require.ErrorIs(t, <-second, failure)
}

func TestWatermillClientsCloseExactlyOnce(t *testing.T) {
	r := miniredis.RunT(t)
	host, portText, err := net.SplitHostPort(r.Addr())
	require.NoError(t, err)
	port, err := strconv.Atoi(portText)
	require.NoError(t, err)
	eb, err := NewWatermillEventBus(&config.RedisConfig{Host: host, Port: port}, zap.NewNop().Sugar())
	require.NoError(t, err)
	require.NoError(t, eb.Close())
	require.NoError(t, eb.Close())
}

type lifecycleSubscriber struct{ calls atomic.Int32 }

func (s *lifecycleSubscriber) Subscribe(ctx context.Context, _ string) (<-chan *message.Message, error) {
	s.calls.Add(1)
	out := make(chan *message.Message)
	go func() { <-ctx.Done(); close(out) }()
	return out, nil
}
func (*lifecycleSubscriber) Close() error { return nil }

type lifecycleHandler struct{}

func (lifecycleHandler) Handle(interface{}) error { return nil }

func TestEventSubscriptionsRequireExplicitRuntimeStart(t *testing.T) {
	sub := &lifecycleSubscriber{}
	eb := &WatermillEventBus{publisher: &fakePublisher{}, subscriber: sub, logger: zap.NewNop().Sugar()}
	require.NoError(t, eb.RegisterSubscription("ticket.created", lifecycleHandler{}))
	require.Zero(t, sub.calls.Load())
	require.Error(t, eb.Subscribe("ticket.created", lifecycleHandler{}))
	require.NoError(t, eb.Start(context.Background()))
	require.EqualValues(t, 1, sub.calls.Load())
	require.Error(t, eb.Start(context.Background()))
	require.NoError(t, eb.Close())
}

// fakePublisher 捕获发布的消息用于断言
type fakePublisher struct {
	topic    string
	messages []*message.Message
}

func (f *fakePublisher) Publish(topic string, messages ...*message.Message) error {
	f.topic = topic
	f.messages = append(f.messages, messages...)
	return nil
}

func (f *fakePublisher) Close() error { return nil }

// stableEventStub 测试用稳定事件
type stableEventStub struct {
	typ     string
	tenant  string
	at      time.Time
	Content string `json:"content"`
}

func (e *stableEventStub) EventType() string     { return e.typ }
func (e *stableEventStub) TenantID() string      { return e.tenant }
func (e *stableEventStub) OccurredAt() time.Time { return e.at }

// plainPayloadStub 不实现 stableEvent 的纯载荷
type plainPayloadStub struct {
	Value string `json:"value"`
}

func TestResolveTopic_StableEventUsesEventType(t *testing.T) {
	ev := &stableEventStub{typ: "ticket.created", tenant: "1", at: time.Now()}
	assert.Equal(t, "ticket.created", resolveTopic(ev))
}

func TestResolveTopic_EmptyEventTypeFallsBackToGoType(t *testing.T) {
	ev := &stableEventStub{typ: "", tenant: "1", at: time.Now()}
	assert.Equal(t, "*eventbus.stableEventStub", resolveTopic(ev))
}

func TestResolveTopic_PlainPayloadUsesGoType(t *testing.T) {
	payload := &plainPayloadStub{Value: "x"}
	assert.Equal(t, "*eventbus.plainPayloadStub", resolveTopic(payload))
}

func TestPublish_StableEventWrapsEnvelopeWithStableTopic(t *testing.T) {
	fp := &fakePublisher{}
	eb := &WatermillEventBus{publisher: fp, logger: zap.NewNop().Sugar()}

	occurred := time.Date(2026, 8, 14, 10, 0, 0, 0, time.UTC)
	ev := &stableEventStub{typ: "ticket.created", tenant: "42", at: occurred, Content: "hello"}

	err := eb.Publish(ev)
	require.NoError(t, err)
	require.Equal(t, "ticket.created", fp.topic)
	require.Len(t, fp.messages, 1)

	var env Envelope
	require.NoError(t, json.Unmarshal(fp.messages[0].Payload, &env))
	assert.Equal(t, "ticket.created", env.EventType)
	assert.Equal(t, "42", env.TenantID)
	assert.Equal(t, occurred, env.OccurredAt)
	assert.JSONEq(t, `{"content":"hello"}`, string(env.Payload))
}

func TestPublish_PlainPayloadNoEnvelope(t *testing.T) {
	fp := &fakePublisher{}
	eb := &WatermillEventBus{publisher: fp, logger: zap.NewNop().Sugar()}

	err := eb.Publish(&plainPayloadStub{Value: "raw"})
	require.NoError(t, err)
	require.Equal(t, "*eventbus.plainPayloadStub", fp.topic)

	var out map[string]interface{}
	require.NoError(t, json.Unmarshal(fp.messages[0].Payload, &out))
	assert.Equal(t, "raw", out["value"])
	// 纯载荷不应带信封字段
	assert.NotContains(t, out, "eventType")
}

func TestPublish_NilEventRejected(t *testing.T) {
	fp := &fakePublisher{}
	eb := &WatermillEventBus{publisher: fp, logger: zap.NewNop().Sugar()}
	err := eb.Publish(nil)
	require.Error(t, err)
	assert.Len(t, fp.messages, 0)
}

func TestUnwrapEnvelope_MergesMetadataWithPayload(t *testing.T) {
	raw := []byte(`{"eventType":"ticket.created","tenantId":"7","occurredAt":"2026-08-14T10:00:00Z","payload":{"ticketId":"100","title":"测试"}}`)
	got, err := unwrapEnvelope(raw)
	require.NoError(t, err)

	m, ok := got.(map[string]interface{})
	require.True(t, ok)
	assert.Equal(t, "ticket.created", m["eventType"])
	assert.Equal(t, "7", m["tenantId"])
	assert.Equal(t, "2026-08-14T10:00:00Z", m["occurredAt"])
	assert.Equal(t, "100", m["ticketId"])
	assert.Equal(t, "测试", m["title"])
}

func TestUnwrapEnvelope_PlainPayloadPassthrough(t *testing.T) {
	raw := []byte(`{"value":"no-envelope"}`)
	got, err := unwrapEnvelope(raw)
	require.NoError(t, err)

	m, ok := got.(map[string]interface{})
	require.True(t, ok)
	assert.Equal(t, "no-envelope", m["value"])
	assert.NotContains(t, m, "eventType")
}

func TestUnwrapEnvelope_InvalidJSON(t *testing.T) {
	_, err := unwrapEnvelope([]byte(`{invalid`))
	require.Error(t, err)
}
