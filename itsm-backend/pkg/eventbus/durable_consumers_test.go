package eventbus

import (
	"context"
	"errors"
	"sync/atomic"
	"testing"
	"time"

	"github.com/ThreeDotsLabs/watermill/message"
	"github.com/google/uuid"
	"github.com/stretchr/testify/require"
	"go.uber.org/zap"
	"itsm-backend/common/executionscope"
	"itsm-backend/config"
)

type namedConsumer struct{ name string }

func (h *namedConsumer) EventConsumerID() string { return h.name }
func (*namedConsumer) Handle(interface{}) error  { return nil }

type ownedSubscriberStub struct {
	subscribe func(context.Context, string) (<-chan *message.Message, error)
	closes    atomic.Int32
}

func (s *ownedSubscriberStub) Subscribe(ctx context.Context, topic string) (<-chan *message.Message, error) {
	return s.subscribe(ctx, topic)
}
func (s *ownedSubscriberStub) Close() error { s.closes.Add(1); return nil }

func durableTestBus(t *testing.T) *WatermillEventBus {
	t.Helper()
	routes, err := newStreamRoutes(config.ExecutionConfig{Mode: "candidate", DeploymentID: "durable-test", Scopes: []config.ExecutionScopeConfig{{TenantID: 1, ScopeID: uuid.NewString()}}})
	require.NoError(t, err)
	return &WatermillEventBus{routes: routes, publisher: &fakePublisher{}, logger: zap.NewNop().Sugar()}
}

func TestDurableConsumerIdentityFrozenAndOwnersIndependent(t *testing.T) {
	bus := durableTestBus(t)
	defer bus.Close()
	var groups []string
	bus.newSubscriber = func(group string) (streamSubscriber, error) {
		groups = append(groups, group)
		return &lifecycleSubscriber{}, nil
	}
	owner := &namedConsumer{name: "event_audit"}
	require.NoError(t, bus.RegisterSubscription("sla.breached", owner))
	require.Error(t, bus.RegisterSubscription("sla.breached", &namedConsumer{name: "event_audit"}))
	require.NoError(t, bus.RegisterSubscription("sla.breached", &namedConsumer{name: "webhook"}))
	require.Error(t, bus.RegisterSubscription("sla.breached", &namedConsumer{name: "Bad:owner"}))
	require.Error(t, bus.RegisterSubscription("sla.breached", lifecycleHandler{}))
	require.Empty(t, groups, "registration must not allocate or create groups")
	owner.name = "changed_after_registration"
	require.NoError(t, bus.Start(context.Background()))
	require.Equal(t, []string{"itsm:event_audit", "itsm:webhook"}, groups)
	require.Error(t, bus.Subscribe("sla.breached", &namedConsumer{name: "event_audit"}))
	require.NoError(t, bus.Subscribe("ticket.created", &namedConsumer{name: "event_audit"}))
	require.Len(t, groups, 2, "same owner reuses its subscriber across topics")
}

func TestDurableCloseCancelsEstablishmentBeforeClosingSubscriber(t *testing.T) {
	bus := durableTestBus(t)
	entered, release := make(chan struct{}), make(chan struct{})
	sub := &ownedSubscriberStub{subscribe: func(ctx context.Context, _ string) (<-chan *message.Message, error) {
		close(entered)
		<-ctx.Done()
		<-release
		return nil, ctx.Err()
	}}
	bus.newSubscriber = func(string) (streamSubscriber, error) { return sub, nil }
	require.NoError(t, bus.RegisterSubscription("sla.breached", &namedConsumer{name: "event_audit"}))
	started, closed := make(chan error, 1), make(chan error, 1)
	go func() { started <- bus.Start(context.Background()) }()
	select {
	case <-entered:
	case <-time.After(time.Second):
		t.Fatal("subscription did not start")
	}
	go func() { closed <- bus.Close() }()
	select {
	case <-closed:
		t.Error("Close returned before establishing subscription exited")
	case <-time.After(20 * time.Millisecond):
	}
	require.Zero(t, sub.closes.Load())
	close(release)
	select {
	case err := <-started:
		require.ErrorIs(t, err, context.Canceled)
	case <-time.After(time.Second):
		t.Fatal("Start did not exit")
	}
	select {
	case err := <-closed:
		require.NoError(t, err)
	case <-time.After(time.Second):
		t.Fatal("Close did not exit")
	}
	require.EqualValues(t, 1, sub.closes.Load())
}

func TestDurablePartialStartFailureClosesEveryOwnedSubscriber(t *testing.T) {
	bus := durableTestBus(t)
	failure := errors.New("subscription unavailable")
	first := &ownedSubscriberStub{subscribe: (&lifecycleSubscriber{}).Subscribe}
	second := &ownedSubscriberStub{subscribe: func(context.Context, string) (<-chan *message.Message, error) { return nil, failure }}
	bus.newSubscriber = func(group string) (streamSubscriber, error) {
		if group == "itsm:event_audit" {
			return first, nil
		}
		return second, nil
	}
	require.NoError(t, bus.RegisterSubscription("sla.breached", &namedConsumer{name: "event_audit"}))
	require.NoError(t, bus.RegisterSubscription("sla.breached", &namedConsumer{name: "webhook"}))
	require.ErrorIs(t, bus.Start(context.Background()), failure)
	require.EqualValues(t, 1, first.closes.Load())
	require.EqualValues(t, 1, second.closes.Load())
	require.NoError(t, bus.Close())
	require.Error(t, bus.Start(context.Background()))
}

func TestDurableNilSubscriberFailsClosed(t *testing.T) {
	bus := durableTestBus(t)
	bus.newSubscriber = func(string) (streamSubscriber, error) { return nil, nil }
	require.NoError(t, bus.RegisterSubscription("sla.breached", &namedConsumer{name: "event_audit"}))
	require.ErrorContains(t, bus.Start(context.Background()), "factory returned nil")
	require.NoError(t, bus.Close())
}

func TestDurableDynamicSubscriptionFailureStopsPartialRoutes(t *testing.T) {
	bus := durableTestBus(t)
	defer bus.Close()
	ref := bus.routes.refs[0]
	ref.TenantID = 2
	ref.ScopeID = uuid.NewString()
	bus.routes.refs = append(bus.routes.refs, ref)
	calls := 0
	failure := errors.New("second tenant group unavailable")
	sub := &ownedSubscriberStub{subscribe: func(ctx context.Context, topic string) (<-chan *message.Message, error) {
		calls++
		if calls == 2 {
			return nil, failure
		}
		return (&lifecycleSubscriber{}).Subscribe(ctx, topic)
	}}
	bus.newSubscriber = func(string) (streamSubscriber, error) { return sub, nil }
	require.NoError(t, bus.Start(context.Background()))
	require.ErrorIs(t, bus.Subscribe("sla.breached", &namedConsumer{name: "event_audit"}), failure)
	require.Equal(t, 2, calls)
	require.EqualValues(t, 1, sub.closes.Load(), "partial subscription must not stay live after failure")
	require.Error(t, bus.ctx.Err())
}

type typedNamedConsumer struct{ *namedConsumer }

func (typedNamedConsumer) ExecutionEnvelopeRequired() {}

func TestStandardTypedOwnersUseFrozenDurableGroups(t *testing.T) {
	routes, err := newStreamRoutes(config.ExecutionConfig{Mode: "standard", DeploymentID: "standard-durable"})
	require.NoError(t, err)
	bus := &WatermillEventBus{authority: eventAuthorityFunc(func(context.Context, executionscope.Ref, Envelope) error { return nil }), routes: routes, publisher: &fakePublisher{}, subscriber: &lifecycleSubscriber{}, logger: zap.NewNop().Sugar()}
	defer bus.Close()
	var groups []string
	bus.newSubscriber = func(group string) (streamSubscriber, error) {
		groups = append(groups, group)
		return &lifecycleSubscriber{}, nil
	}
	owner := typedNamedConsumer{&namedConsumer{name: "webhook"}}
	require.NoError(t, bus.RegisterSubscription("sla.breached", owner))
	require.Error(t, bus.RegisterSubscription("sla.breached", typedNamedConsumer{&namedConsumer{name: "webhook"}}))
	require.Error(t, bus.RegisterSubscription("sla.breached", typedNamedConsumer{&namedConsumer{name: "Invalid:owner"}}))
	require.NoError(t, bus.RegisterSubscription("sla.breached", lifecycleHandler{}))
	owner.name = "changed-after-registration"
	require.Empty(t, groups)
	require.NoError(t, bus.Start(context.Background()))
	require.Equal(t, []string{"itsm:webhook"}, groups)
	require.Error(t, bus.Subscribe("sla.breached", typedNamedConsumer{&namedConsumer{name: "webhook"}}))
	require.NoError(t, bus.Subscribe("ticket.created", typedNamedConsumer{&namedConsumer{name: "webhook"}}))
	require.Len(t, groups, 1)
}

func TestStandardTypedOwnerRequiresAuthorityBeforeSubscription(t *testing.T) {
	routes, err := newStreamRoutes(config.ExecutionConfig{Mode: "standard", DeploymentID: "missing-authority"})
	require.NoError(t, err)
	bus := &WatermillEventBus{routes: routes, publisher: &fakePublisher{}, subscriber: &lifecycleSubscriber{}, logger: zap.NewNop().Sugar()}
	defer bus.Close()
	var allocations int
	bus.newSubscriber = func(string) (streamSubscriber, error) { allocations++; return &lifecycleSubscriber{}, nil }
	owner := typedNamedConsumer{&namedConsumer{name: "webhook"}}
	require.Error(t, bus.RegisterSubscription("sla.breached", owner))
	require.NoError(t, bus.RegisterSubscription("ticket.created", lifecycleHandler{}))
	require.NoError(t, bus.Start(context.Background()))
	require.Error(t, bus.Subscribe("sla.breached", owner))
	require.Zero(t, allocations, "missing authority must fail before durable group allocation")
}
