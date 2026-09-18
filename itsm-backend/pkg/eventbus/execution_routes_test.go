package eventbus

import (
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/stretchr/testify/require"
	"go.uber.org/zap"
	"itsm-backend/config"
)

func TestStreamRoutesFreezeCandidateManifest(t *testing.T) {
	scope := uuid.NewString()
	cfg := config.ExecutionConfig{Mode: "candidate", DeploymentID: "frozen", Scopes: []config.ExecutionScopeConfig{{TenantID: 7, ScopeID: scope}}}
	routes, err := newStreamRoutes(cfg)
	require.NoError(t, err)
	cfg.Mode = "standard"
	cfg.DeploymentID = "changed"
	cfg.Scopes[0].ScopeID = uuid.NewString()
	cfg.Scopes[0].TenantID = 8
	expected := "candidate:frozen:" + scope + ":sla.breached"
	physical, err := routes.publishTopic("sla.breached", "7")
	require.NoError(t, err)
	require.Equal(t, expected, physical)
	subscriptions, err := routes.subscriptionRoutes("sla.breached")
	require.NoError(t, err)
	require.Equal(t, []streamRoute{{topic: expected, tenantID: 7}}, subscriptions)
	_, err = routes.publishTopic("sla.breached", "8")
	require.Error(t, err)
}

func TestCandidatePublishRejectsInvalidRoutesBeforeRedis(t *testing.T) {
	routes, err := newStreamRoutes(config.ExecutionConfig{Mode: "candidate", DeploymentID: "routing-test", Scopes: []config.ExecutionScopeConfig{{TenantID: 1, ScopeID: uuid.NewString()}}})
	require.NoError(t, err)
	for _, tc := range []struct{ topic, tenant string }{
		{"sla.breached", "2"},
		{"sla.breached", "01"},
		{"sla.breached", "0"},
		{"sla.breached", ""},
		{"candidate:other:topic", "1"},
		{"", "1"},
		{"sla..breached", "1"},
		{"SLA.breached", "1"},
	} {
		t.Run(tc.topic+"/"+tc.tenant, func(t *testing.T) {
			publisher := &fakePublisher{}
			bus := &WatermillEventBus{routes: routes, publisher: publisher, logger: zap.NewNop().Sugar()}
			require.Error(t, bus.Publish(&stableEventStub{typ: tc.topic, tenant: tc.tenant, at: time.Now()}))
			require.Empty(t, publisher.messages)
		})
	}
	publisher := &fakePublisher{}
	bus := &WatermillEventBus{routes: routes, publisher: publisher, logger: zap.NewNop().Sugar()}
	require.Error(t, bus.Publish(&plainPayloadStub{Value: "no tenant"}))
	require.Empty(t, publisher.messages)
	_, err = routes.subscriptionRoutes("candidate:other:topic")
	require.Error(t, err)
}

func TestStreamRoutesRequireValidExplicitConfiguration(t *testing.T) {
	_, err := newStreamRoutes(config.ExecutionConfig{})
	require.Error(t, err)
	scope := uuid.NewString()
	_, err = newStreamRoutes(config.ExecutionConfig{Mode: "candidate", DeploymentID: "duplicate", Scopes: []config.ExecutionScopeConfig{{TenantID: 1, ScopeID: scope}, {TenantID: 2, ScopeID: scope}}})
	require.Error(t, err, "one physical scope cannot route two tenants")
}

// Source identity belongs to a persisted producer, not to arbitrary JSON fields.
type persistentEventStub struct {
	*stableEventStub
	item int
	id   string
}

func (e *persistentEventStub) ExecutionWorkItemID() int  { return e.item }
func (e *persistentEventStub) PersistentEventID() string { return e.id }

func TestCandidatePublishRequiresPersistentSubject(t *testing.T) {
	routes, err := newStreamRoutes(config.ExecutionConfig{Mode: "candidate", DeploymentID: "source-test", Scopes: []config.ExecutionScopeConfig{{TenantID: 1, ScopeID: uuid.NewString()}}})
	require.NoError(t, err)
	stable := &stableEventStub{typ: "sla.breached", tenant: "1", at: time.Now()}
	for _, value := range []interface{}{stable, &persistentEventStub{stable, 0, "existing-event"}, &persistentEventStub{stable, 1, ""}} {
		publisher := &fakePublisher{}
		bus := &WatermillEventBus{routes: routes, publisher: publisher, logger: zap.NewNop().Sugar()}
		require.Error(t, bus.Publish(value))
		require.Empty(t, publisher.messages)
	}
}
