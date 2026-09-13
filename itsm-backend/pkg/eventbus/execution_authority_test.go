package eventbus

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/ThreeDotsLabs/watermill/message"
	"github.com/google/uuid"
	"github.com/stretchr/testify/require"
	"go.uber.org/zap"
	"itsm-backend/common/executionscope"
	"itsm-backend/config"
)

type eventAuthorityFunc func(context.Context, executionscope.Ref, Envelope) error

func (f eventAuthorityFunc) ValidateEvent(ctx context.Context, ref executionscope.Ref, env Envelope) error {
	return f(ctx, ref, env)
}

func TestCandidatePublishValidatesSourceAndReusesPersistentID(t *testing.T) {
	scope := uuid.NewString()
	routes, err := newStreamRoutes(config.ExecutionConfig{Mode: "candidate", DeploymentID: "source-test", Scopes: []config.ExecutionScopeConfig{{TenantID: 1, ScopeID: scope}}})
	require.NoError(t, err)
	publisher := &fakePublisher{}
	rejection := errors.New("source revoked")
	rejected := false
	calls := 0
	bus := &WatermillEventBus{routes: routes, publisher: publisher, logger: zap.NewNop().Sugar(), authority: eventAuthorityFunc(func(ctx context.Context, ref executionscope.Ref, env Envelope) error {
		calls++
		require.NoError(t, ctx.Err())
		require.Equal(t, executionscope.Ref{DeploymentID: "source-test", ScopeID: scope, TenantID: 1}, ref)
		require.Equal(t, 41, env.Execution.WorkItemID)
		require.Equal(t, "persisted-41", env.EventID)
		if rejected {
			return rejection
		}
		return nil
	})}
	event := &persistentEventStub{&stableEventStub{typ: "sla.breached", tenant: "1", at: time.Now()}, 41, "persisted-41"}
	require.NoError(t, bus.Publish(event))
	require.NoError(t, bus.Publish(event))
	require.Len(t, publisher.messages, 2)
	require.Equal(t, "persisted-41", publisher.messages[0].UUID)
	require.Equal(t, publisher.messages[0].UUID, publisher.messages[1].UUID)
	require.Equal(t, publisher.messages[0].Payload, publisher.messages[1].Payload)
	rejected = true
	require.ErrorIs(t, bus.Publish(event), rejection)
	require.Len(t, publisher.messages, 2, "rejected source never reaches Redis")
	require.Equal(t, 3, calls)
}

func TestCandidateEnvelopeRejectsAmbiguousIdentity(t *testing.T) {
	env := Envelope{EventID: "persisted-event", Execution: &ExecutionIdentity{DeploymentID: "test", ScopeID: uuid.NewString(), WorkItemID: 1}, EventType: "sla.breached", TenantID: "1", OccurredAt: time.Now().UTC(), Payload: json.RawMessage(`{"ticket_id":"1"}`)}
	wire, err := json.Marshal(env)
	require.NoError(t, err)
	_, err = DecodeExecutionEnvelope(wire)
	require.NoError(t, err)
	for _, bad := range []string{
		strings.Replace(string(wire), `"eventId":"persisted-event"`, `"eventId":"a","EventId":"persisted-event"`, 1),
		strings.Replace(string(wire), `"workItemId":1`, `"workItemId":2,"WorkItemId":1`, 1),
		strings.Replace(string(wire), `"tenantId":"1"`, `"TenantId":"1"`, 1),
		strings.Replace(string(wire), `"tenantId":"1"`, `"tenantId":"2","tenantId":"1"`, 1),
		strings.Replace(string(wire), `"ticket_id":"1"`, `"ticket_id":"2","ticket_id":"1"`, 1),
		strings.Replace(string(wire), `"ticket_id":"1"`, `"tenantId":"2"`, 1),
		strings.Replace(string(wire), `"ticket_id":"1"`, `"execution":{}`, 1),
		strings.Replace(string(wire), `"eventId":"persisted-event"`, `"eventId":""`, 1),
		strings.Replace(string(wire), `"workItemId":1`, `"workItemId":0`, 1),
		strings.Replace(string(wire), `"ticket_id":"1"`, `"nested":{"k":1,"k":2}`, 1),
		strings.Replace(string(wire), `"eventId":"persisted-event"`, `"extra":1,"eventId":"persisted-event"`, 1),
		string(wire) + `{}`,
	} {
		_, err := DecodeExecutionEnvelope([]byte(bad))
		require.Error(t, err, bad)
	}
	require.Error(t, validateEnvelopeRoute(env, executionscope.Ref{DeploymentID: "test", ScopeID: env.Execution.ScopeID, TenantID: 2}, env.EventType))
}

type controlledStreamSubscriber struct{ messages chan *message.Message }

func (s *controlledStreamSubscriber) Subscribe(ctx context.Context, _ string) (<-chan *message.Message, error) {
	go func() { <-ctx.Done(); close(s.messages) }()
	return s.messages, nil
}
func (*controlledStreamSubscriber) Close() error { return nil }

type observedStreamHandler struct {
	received chan interface{}
	contexts chan context.Context
}

func (h observedStreamHandler) HandleContext(ctx context.Context, event interface{}) error {
	h.contexts <- ctx
	return h.Handle(event)
}
func (h observedStreamHandler) Handle(event interface{}) error { h.received <- event; return nil }

func TestCandidateSubscriberNacksUntrustedMessagesBeforeHandler(t *testing.T) {
	scope := uuid.NewString()
	routes, err := newStreamRoutes(config.ExecutionConfig{Mode: "candidate", DeploymentID: "source-test", Scopes: []config.ExecutionScopeConfig{{TenantID: 1, ScopeID: scope}}})
	require.NoError(t, err)
	subscription := &controlledStreamSubscriber{messages: make(chan *message.Message, 1)}
	received := make(chan interface{}, 1)
	contexts := make(chan context.Context, 1)
	bus := &WatermillEventBus{routes: routes, publisher: &fakePublisher{}, newSubscriber: func(string) (streamSubscriber, error) { return subscription, nil }, logger: zap.NewNop().Sugar(), authority: eventAuthorityFunc(func(_ context.Context, _ executionscope.Ref, env Envelope) error {
		if env.EventID != "persisted-41" {
			return errors.New("source missing")
		}
		return nil
	})}
	require.NoError(t, bus.RegisterSubscription("sla.breached", observedStreamHandler{received, contexts}))
	require.NoError(t, bus.Start(context.Background()))
	defer bus.Close()
	env := Envelope{EventID: "persisted-41", Execution: &ExecutionIdentity{DeploymentID: "source-test", ScopeID: scope, WorkItemID: 41}, EventType: "sla.breached", TenantID: "1", OccurredAt: time.Now().UTC(), Payload: json.RawMessage(`{"ticket_id":"41"}`)}
	wire, err := json.Marshal(env)
	require.NoError(t, err)
	makeMessage := func(id string, payload []byte) *message.Message {
		msg := message.NewMessage(id, payload)
		msg.Metadata.Set("event_type", "sla.breached")
		return msg
	}
	valid := makeMessage(env.EventID, wire)
	subscription.messages <- valid
	select {
	case <-valid.Acked():
	case <-time.After(time.Second):
		t.Fatal("valid source was not acknowledged")
	}
	require.Equal(t, "persisted-41", (<-received).(Envelope).EventID)
	deliveryContext := <-contexts
	require.NoError(t, deliveryContext.Err())
	for _, bad := range []*message.Message{
		makeMessage("different-id", wire),
		makeMessage(env.EventID, []byte(strings.Replace(string(wire), `"tenantId":"1"`, `"tenantId":"2"`, 1))),
		makeMessage(env.EventID, []byte(strings.Replace(string(wire), scope, uuid.NewString(), 1))),
		makeMessage("unknown-event", []byte(strings.Replace(string(wire), "persisted-41", "unknown-event", 1))),
		makeMessage(env.EventID, []byte(strings.Replace(string(wire), `"workItemId":41`, `"WorkItemId":41`, 1))),
	} {
		subscription.messages <- bad
		select {
		case <-bad.Nacked():
		case <-time.After(time.Second):
			t.Fatal("untrusted source was not rejected")
		}
		select {
		case <-bad.Acked():
			t.Fatal("rejected source was acknowledged")
		default:
		}
		select {
		case <-received:
			t.Fatal("untrusted source reached handler")
		default:
		}
	}
	require.NoError(t, bus.Close())
	require.ErrorIs(t, deliveryContext.Err(), context.Canceled)
}

func (observedStreamHandler) EventConsumerID() string { return "event_audit" }
