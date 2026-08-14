package eventbus

import (
	"encoding/json"
	"testing"
	"time"

	"github.com/ThreeDotsLabs/watermill/message"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"go.uber.org/zap"
)

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
