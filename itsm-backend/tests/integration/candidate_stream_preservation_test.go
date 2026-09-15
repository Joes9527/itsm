//go:build candidate_scope

package integration

import (
	"context"
	"encoding/json"
	"fmt"
	"net"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/ThreeDotsLabs/watermill-redisstream/pkg/redisstream"
	"github.com/ThreeDotsLabs/watermill/message"
	"github.com/google/uuid"
	"github.com/redis/go-redis/v9"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"go.uber.org/zap"
	"go.uber.org/zap/zaptest/observer"
	"itsm-backend/common/executionscope"
	"itsm-backend/config"
	"itsm-backend/pkg/eventbus"
)

type candidateStreamEvent struct {
	tenantID     int
	eventID      string
	WorkItemID   int    `json:"workItemId"`
	DeploymentID string `json:"deploymentId"`
	ScopeID      string `json:"scopeId"`
}

func (candidateStreamEvent) EventType() string     { return "sla.breached" }
func (e candidateStreamEvent) TenantID() string    { return strconv.Itoa(e.tenantID) }
func (candidateStreamEvent) OccurredAt() time.Time { return time.Unix(1700000000, 0).UTC() }

// Historical wire fixture predates persistent producer identity. A distinct
// defined type retains the old payload without claiming ExecutionEvent.
type historicalStreamEvent candidateStreamEvent

func (historicalStreamEvent) EventType() string     { return "sla.breached" }
func (e historicalStreamEvent) TenantID() string    { return strconv.Itoa(e.tenantID) }
func (historicalStreamEvent) OccurredAt() time.Time { return time.Unix(1700000000, 0).UTC() }

type candidateStreamObserver struct{ received chan interface{} }

func (h candidateStreamObserver) Handle(event interface{}) error {
	h.received <- event
	return nil
}

// This verifies transport isolation through the application's constructor
// with an explicit frozen candidate manifest.
// Payload scope fields are fixture data, never evidence of authorization.
// Membership validation and real audit persistence remain separate requirements.
func TestCandidateStreamPreservesLegacyTopicOnPublish(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()
	cfg, client := startCandidateStreamRedis(t, ctx)
	legacyPublisher, err := eventbus.NewWatermillEventBus(cfg, config.ExecutionConfig{Mode: "standard", DeploymentID: "legacy-test"}, nil, zap.NewNop().Sugar())
	require.NoError(t, err)
	require.NoError(t, legacyPublisher.Publish(historicalStreamEvent{tenantID: 1, WorkItemID: 100}))
	require.NoError(t, legacyPublisher.Close())
	const topic = "sla.breached"
	require.NoError(t, client.XGroupCreate(ctx, topic, "protected-history-group", "0").Err())
	_, err = client.XReadGroup(ctx, &redis.XReadGroupArgs{Group: "protected-history-group", Consumer: "protected-consumer", Streams: []string{topic, ">"}, Count: 1}).Result()
	require.NoError(t, err)
	before, err := client.XRange(ctx, topic, "-", "+").Result()
	require.NoError(t, err)
	groups, err := client.XInfoGroups(ctx, topic).Result()
	require.NoError(t, err)
	pending, err := client.XPending(ctx, topic, "protected-history-group").Result()
	require.NoError(t, err)
	pendingEntries := func() []redis.XPendingExt {
		rows, e := client.XPendingExt(ctx, &redis.XPendingExtArgs{Stream: topic, Group: "protected-history-group", Start: "-", End: "+", Count: 10}).Result()
		require.NoError(t, e)
		for i := range rows {
			rows[i].Idle = 0
		} // Wall-clock idle advances without a write.
		return rows
	}
	beforePendingEntries := pendingEntries()
	require.Len(t, beforePendingEntries, 1)

	scope, secondScope := uuid.NewString(), uuid.NewString()
	bus, err := eventbus.NewWatermillEventBus(cfg, config.ExecutionConfig{Mode: "candidate", DeploymentID: "stream-test", Scopes: []config.ExecutionScopeConfig{{TenantID: 1, ScopeID: scope}, {TenantID: 2, ScopeID: secondScope}}}, candidateStreamFixtureAuthority{}, zap.NewNop().Sugar())
	require.NoError(t, err)
	defer bus.Close()
	observed := make(chan interface{}, 4)
	require.NoError(t, bus.RegisterSubscription(topic, candidateStreamObserver{observed}))
	require.NoError(t, bus.Start(ctx))
	// Confirm the real subscriber is waiting before the positive-control publish.
	require.Eventually(t, func() bool {
		clients, e := client.ClientList(ctx).Result()
		return e == nil && strings.Count(clients, "cmd=xread") == 2
	}, 3*time.Second, 10*time.Millisecond)
	require.NoError(t, bus.Publish(candidateStreamEvent{tenantID: 1, WorkItemID: 200, DeploymentID: "payload-cannot-route", ScopeID: uuid.NewString()}))
	select {
	case event := <-observed:
		require.EqualValues(t, 200, event.(eventbus.Envelope).Execution.WorkItemID, "new event positive control")
	case <-ctx.Done():
		t.Fatal("subscriber did not process new event")
	}
	require.NoError(t, bus.Publish(candidateStreamEvent{tenantID: 2, WorkItemID: 300}))
	select {
	case event := <-observed:
		require.EqualValues(t, 300, event.(eventbus.Envelope).Execution.WorkItemID)
		require.Equal(t, "2", event.(eventbus.Envelope).TenantID)
	case <-ctx.Done():
		t.Fatal("second tenant subscriber did not process its new event")
	}
	require.NoError(t, bus.Close())
	secondRows, err := client.XRange(ctx, "candidate:stream-test:"+secondScope+":"+topic, "-", "+").Result()
	require.NoError(t, err)
	require.Len(t, secondRows, 1)
	var secondEnvelope eventbus.Envelope
	require.NoError(t, json.Unmarshal([]byte(secondRows[0].Values["payload"].(string)), &secondEnvelope))
	require.Equal(t, "2", secondEnvelope.TenantID)
	require.JSONEq(t, `{"workItemId":300,"deploymentId":"","scopeId":""}`, string(secondEnvelope.Payload))
	after, err := client.XRange(ctx, topic, "-", "+").Result()
	require.NoError(t, err)
	assert.Equal(t, before, after, "candidate publish must not append to the protected legacy topic")
	afterGroups, err := client.XInfoGroups(ctx, topic).Result()
	require.NoError(t, err)
	assert.Equal(t, groups, afterGroups, "legacy group metadata must remain unchanged")
	afterPending, err := client.XPending(ctx, topic, "protected-history-group").Result()
	require.NoError(t, err)
	assert.Equal(t, pending, afterPending, "legacy pending summary must remain unchanged")
	assert.Equal(t, beforePendingEntries, pendingEntries(), "legacy pending IDs, owners and delivery counts must remain unchanged")
	length, err := client.XLen(ctx, "candidate:stream-test:"+scope+":"+topic).Result()
	require.NoError(t, err)
	assert.EqualValues(t, 1, length, "new event belongs only in the candidate namespace")
	newRows, err := client.XRange(ctx, "candidate:stream-test:"+scope+":"+topic, "-", "+").Result()
	require.NoError(t, err)
	require.Len(t, newRows, 1)
	var envelope eventbus.Envelope
	require.NoError(t, json.Unmarshal([]byte(newRows[0].Values["payload"].(string)), &envelope))
	require.Equal(t, topic, envelope.EventType)
	require.Equal(t, "1", envelope.TenantID)
	var payload candidateStreamEvent
	require.NoError(t, json.Unmarshal(envelope.Payload, &payload))
	require.Equal(t, 200, payload.WorkItemID)
	require.Equal(t, "payload-cannot-route", payload.DeploymentID, "business payload cannot change transport namespace")
}

// Transport-only authority fixture. Real PostgreSQL source checks are covered
// by TestCandidateIntakeCreationBoundary, not by this deterministic test port.
type candidateStreamFixtureAuthority struct{}

func (candidateStreamFixtureAuthority) ValidateEvent(_ context.Context, ref executionscope.Ref, env eventbus.Envelope) error {
	if env.Execution == nil || env.Execution.WorkItemID != (ref.TenantID+1)*100 {
		return fmt.Errorf("unknown fixture source")
	}
	return nil
}
func (e candidateStreamEvent) ExecutionWorkItemID() int { return e.WorkItemID }
func (e candidateStreamEvent) PersistentEventID() string {
	if e.eventID != "" {
		return e.eventID
	}
	return fmt.Sprintf("fixture-%d", e.WorkItemID)
}

func startCandidateStreamRedis(t *testing.T, ctx context.Context) (*config.RedisConfig, *redis.Client) {
	t.Helper()
	binary := os.Getenv("CANDIDATE_TEST_REDIS_BINARY")
	if binary == "" {
		t.Skip("requires explicit private Redis binary")
	}
	require.True(t, filepath.IsAbs(binary))
	reservation, err := net.Listen("tcp", "127.0.0.1:0")
	require.NoError(t, err)
	port := reservation.Addr().(*net.TCPAddr).Port
	require.NoError(t, reservation.Close())
	directory := t.TempDir()
	password := uuid.NewString()
	command := exec.CommandContext(ctx, binary, "--requirepass", password, "--bind", "127.0.0.1", "--port", fmt.Sprint(port), "--save", "", "--appendonly", "no", "--dir", directory)
	log, err := os.Create(filepath.Join(directory, "redis.log"))
	require.NoError(t, err)
	t.Cleanup(func() { _ = log.Close() })
	command.Stdout, command.Stderr = log, log
	require.NoError(t, command.Start())
	exited := make(chan struct{})
	go func() { _ = command.Wait(); close(exited) }()
	t.Cleanup(func() { _ = command.Process.Kill(); <-exited })
	client := redis.NewClient(&redis.Options{Addr: fmt.Sprintf("127.0.0.1:%d", port), Password: password})
	t.Cleanup(func() { _ = client.Close() })
	require.Eventually(t, func() bool {
		info, e := client.Info(ctx, "server").Result()
		return e == nil && strings.Contains(info, fmt.Sprintf("process_id:%d\r\n", command.Process.Pid))
	}, 5*time.Second, 20*time.Millisecond, "verify test Redis PID before writing fixtures")
	cfg := &config.RedisConfig{Host: "127.0.0.1", Port: port, Password: password}
	return cfg, client
}

func TestCandidateStreamDeliversOfflineMessages(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()
	cfg, client := startCandidateStreamRedis(t, ctx)
	scope := uuid.NewString()
	execution := config.ExecutionConfig{Mode: "candidate", DeploymentID: "offline-test", Scopes: []config.ExecutionScopeConfig{{TenantID: 1, ScopeID: scope}}}
	publisher, err := eventbus.NewWatermillEventBus(cfg, execution, candidateStreamFixtureAuthority{}, zap.NewNop().Sugar())
	require.NoError(t, err)
	require.NoError(t, publisher.Publish(candidateStreamEvent{tenantID: 1, WorkItemID: 200, eventID: "offline-event"}))
	require.NoError(t, publisher.Close())
	consumer, err := eventbus.NewWatermillEventBus(cfg, execution, candidateStreamFixtureAuthority{}, zap.NewNop().Sugar())
	require.NoError(t, err)
	defer consumer.Close()
	received := make(chan interface{}, 4)
	require.NoError(t, consumer.RegisterSubscription("sla.breached", candidateStreamObserver{received}))
	require.NoError(t, consumer.Start(ctx))
	require.Eventually(t, func() bool {
		list, e := client.ClientList(ctx).Result()
		return e == nil && strings.Contains(list, "cmd=xread")
	}, 3*time.Second, 10*time.Millisecond)
	require.NoError(t, consumer.Publish(candidateStreamEvent{tenantID: 1, WorkItemID: 200, eventID: "online-marker"}))
	counts := map[string]int{}
	for counts["online-marker"] == 0 {
		select {
		case event := <-received:
			counts[event.(eventbus.Envelope).EventID]++
		case <-ctx.Done():
			t.Fatal("online positive control was not delivered")
		}
	}
	assert.Equal(t, 1, counts["offline-event"], "an event committed before consumer startup must not be lost")
	groups, err := client.XInfoGroups(ctx, "candidate:offline-test:"+scope+":sla.breached").Result()
	require.NoError(t, err)
	assert.Len(t, groups, 1, "candidate consumption must have durable progress")
}

func (candidateStreamObserver) EventConsumerID() string { return "event_audit" }

func TestPersistentStreamRejectionEvidenceSurvivesConsumerRestart(t *testing.T) {
	for _, mode := range []string{"candidate", "standard"} {
		t.Run(mode, func(t *testing.T) {
			ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
			defer cancel()
			cfg, client := startCandidateStreamRedis(t, ctx)
			cfg.EventStream = config.EventStreamConfig{ClaimIdle: 200 * time.Millisecond, ClaimInterval: 20 * time.Millisecond, NackDelay: 20 * time.Millisecond}
			execution := config.ExecutionConfig{Mode: mode, DeploymentID: "rejection-test"}
			topic := "sla.breached"
			if mode == "candidate" {
				scope := uuid.NewString()
				execution.Scopes = []config.ExecutionScopeConfig{{TenantID: 1, ScopeID: scope}}
				topic = "candidate:rejection-test:" + scope + ":sla.breached"
			}
			protectedID, err := client.XAdd(ctx, &redis.XAddArgs{Stream: "protected.history", Values: map[string]interface{}{"payload": "protected"}}).Result()
			require.NoError(t, err)
			bad := message.NewMessage("untrusted-secret-uuid", []byte(`{"credential":"never-copy-this-secret"}`))
			bad.Metadata.Set("event_type", "sla.breached")
			values, err := (redisstream.DefaultMarshallerUnmarshaller{}).Marshal(topic, bad)
			require.NoError(t, err)
			entryID, err := client.XAdd(ctx, &redis.XAddArgs{Stream: topic, Values: values}).Result()
			require.NoError(t, err)
			received := make(chan interface{}, 8)
			evidenceKey := topic + ":rejections:event_audit"
			require.NoError(t, client.Set(ctx, evidenceKey, "private-storage-fault", 0).Err())
			core, logs := observer.New(zap.ErrorLevel)
			newConsumer := func() *eventbus.WatermillEventBus {
				bus, e := eventbus.NewWatermillEventBus(cfg, execution, candidateStreamFixtureAuthority{}, zap.New(core).Sugar())
				require.NoError(t, e)
				require.NoError(t, bus.RegisterSubscription("sla.breached", standardExecutionObserver{candidateStreamObserver{received}}))
				require.NoError(t, bus.Start(ctx))
				return bus
			}
			bus := newConsumer()
			defer bus.Close()
			require.Eventually(t, func() bool { return logs.FilterMessage("Persistent event rejection evidence unavailable").Len() > 0 }, 3*time.Second, 20*time.Millisecond)
			require.Equal(t, "private-storage-fault", client.Get(ctx, evidenceKey).Val())
			require.Empty(t, received)
			require.EqualValues(t, 1, client.XPending(ctx, topic, "itsm:event_audit").Val().Count)
			require.NoError(t, client.Del(ctx, evidenceKey).Err())
			require.Eventually(t, func() bool { n, e := client.HLen(ctx, evidenceKey).Result(); return e == nil && n == 1 }, 3*time.Second, 20*time.Millisecond, "rejected message needs persistent evidence")
			evidence, err := client.HGetAll(ctx, evidenceKey).Result()
			require.NoError(t, err)
			require.Empty(t, received)
			for _, value := range evidence {
				require.NotContains(t, value, "never-copy-this-secret")
				require.NotContains(t, value, "untrusted-secret-uuid")
				var record map[string]interface{}
				require.NoError(t, json.Unmarshal([]byte(value), &record))
				require.Equal(t, "rejected", record["status"])
				require.Equal(t, "envelope_invalid", record["reason"])
			}
			pending, err := client.XPendingExt(ctx, &redis.XPendingExtArgs{Stream: topic, Group: "itsm:event_audit", Start: "-", End: "+", Count: 10}).Result()
			require.NoError(t, err)
			require.Len(t, pending, 1)
			require.Equal(t, entryID, pending[0].ID)
			oldConsumer := pending[0].Consumer
			require.NoError(t, bus.Close())
			restarted := newConsumer()
			defer restarted.Close()
			require.Eventually(t, func() bool {
				p, e := client.XPendingExt(ctx, &redis.XPendingExtArgs{Stream: topic, Group: "itsm:event_audit", Start: "-", End: "+", Count: 10}).Result()
				return e == nil && len(p) == 1 && p[0].ID == entryID && p[0].Consumer != oldConsumer
			}, 3*time.Second, 20*time.Millisecond)
			after, err := client.HGetAll(ctx, evidenceKey).Result()
			require.NoError(t, err)
			require.Equal(t, evidence, after, "first rejection fact must be immutable across retries")
			require.Empty(t, received, "malformed source never reaches its owner")
			require.NoError(t, restarted.Publish(candidateStreamEvent{tenantID: 1, WorkItemID: 200, eventID: "valid-after-rejection"}))
			select {
			case value := <-received:
				require.Equal(t, "valid-after-rejection", value.(eventbus.Envelope).EventID)
			case <-time.After(2 * time.Second):
				t.Fatal("rejected message blocked valid source")
			}
			require.Eventually(t, func() bool {
				p, e := client.XPendingExt(ctx, &redis.XPendingExtArgs{Stream: topic, Group: "itsm:event_audit", Start: "-", End: "+", Count: 10}).Result()
				return e == nil && len(p) == 1 && p[0].ID == entryID
			}, 3*time.Second, 20*time.Millisecond)

			protected, err := client.XRange(ctx, "protected.history", "-", "+").Result()
			require.NoError(t, err)
			require.Len(t, protected, 1)
			require.Equal(t, protectedID, protected[0].ID)
			require.Equal(t, "protected", protected[0].Values["payload"])
		})
	}
}

type recoverableStreamAuthority struct{ rejected atomic.Bool }

func (a *recoverableStreamAuthority) ValidateEvent(ctx context.Context, ref executionscope.Ref, env eventbus.Envelope) error {
	if a.rejected.Load() {
		return fmt.Errorf("private source temporarily denied")
	}
	return (candidateStreamFixtureAuthority{}).ValidateEvent(ctx, ref, env)
}

func TestPersistentStreamRejectionCanRecoverWithoutLosingEvidence(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()
	cfg, client := startCandidateStreamRedis(t, ctx)
	cfg.EventStream = config.EventStreamConfig{ClaimIdle: 200 * time.Millisecond, ClaimInterval: 20 * time.Millisecond, NackDelay: 20 * time.Millisecond}
	scope := uuid.NewString()
	execution := config.ExecutionConfig{Mode: "candidate", DeploymentID: "source-recovery", Scopes: []config.ExecutionScopeConfig{{TenantID: 1, ScopeID: scope}}}
	authority := &recoverableStreamAuthority{}
	bus, err := eventbus.NewWatermillEventBus(cfg, execution, authority, zap.NewNop().Sugar())
	require.NoError(t, err)
	defer bus.Close()
	require.NoError(t, bus.Publish(candidateStreamEvent{tenantID: 1, WorkItemID: 200, eventID: "same-source-recovery"}))
	authority.rejected.Store(true)
	received := make(chan interface{}, 1)
	require.NoError(t, bus.RegisterSubscription("sla.breached", candidateStreamObserver{received}))
	require.NoError(t, bus.Start(ctx))
	topic := "candidate:source-recovery:" + scope + ":sla.breached"
	key := topic + ":rejections:event_audit"
	require.Eventually(t, func() bool { return client.HLen(ctx, key).Val() == 1 }, 3*time.Second, 20*time.Millisecond)
	before, err := client.HGetAll(ctx, key).Result()
	require.NoError(t, err)
	for _, value := range before {
		var record map[string]interface{}
		require.NoError(t, json.Unmarshal([]byte(value), &record))
		require.Equal(t, "source_rejected", record["reason"])
	}
	require.Empty(t, received)
	require.EqualValues(t, 1, client.XPending(ctx, topic, "itsm:event_audit").Val().Count)
	authority.rejected.Store(false)
	select {
	case value := <-received:
		require.Equal(t, "same-source-recovery", value.(eventbus.Envelope).EventID)
	case <-ctx.Done():
		t.Fatal("same message did not recover after source authorization")
	}
	require.Eventually(t, func() bool { return client.XPending(ctx, topic, "itsm:event_audit").Val().Count == 0 }, 3*time.Second, 20*time.Millisecond)
	after, err := client.HGetAll(ctx, key).Result()
	require.NoError(t, err)
	require.Equal(t, before, after)
	require.EqualValues(t, 1, client.XLen(ctx, topic).Val(), "recovery must not republish source")
}

func TestPersistentStreamRetainsPELWhenRedisRejectsAck(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()
	cfg, client := startCandidateStreamRedis(t, ctx)
	cfg.EventStream = config.EventStreamConfig{ClaimIdle: 200 * time.Millisecond, ClaimInterval: 20 * time.Millisecond, NackDelay: 20 * time.Millisecond}
	scope := uuid.NewString()
	execution := config.ExecutionConfig{Mode: "candidate", DeploymentID: "ack-fault", Scopes: []config.ExecutionScopeConfig{{TenantID: 1, ScopeID: scope}}}
	core, logs := observer.New(zap.ErrorLevel)
	bus, err := eventbus.NewWatermillEventBus(cfg, execution, candidateStreamFixtureAuthority{}, zap.New(core).Sugar())
	require.NoError(t, err)
	defer bus.Close()
	received := make(chan interface{}, 16)
	require.NoError(t, bus.RegisterSubscription("sla.breached", candidateStreamObserver{received}))
	require.NoError(t, bus.Start(ctx))
	require.NoError(t, client.Do(ctx, "ACL", "SETUSER", "default", "-xack").Err())
	defer func() {
		restoreCtx, done := context.WithTimeout(context.Background(), time.Second)
		defer done()
		require.NoError(t, client.Do(restoreCtx, "ACL", "SETUSER", "default", "+xack").Err())
	}()
	require.NoError(t, bus.Publish(candidateStreamEvent{tenantID: 1, WorkItemID: 200, eventID: "ack-denied-source"}))
	select {
	case value := <-received:
		require.Equal(t, "ack-denied-source", value.(eventbus.Envelope).EventID)
	case <-ctx.Done():
		t.Fatal("business handler not reached")
	}
	require.Eventually(t, func() bool {
		for _, entry := range logs.All() {
			if entry.ContextMap()["reason"] == "ack_failed" {
				return true
			}
		}
		return false
	}, 3*time.Second, 20*time.Millisecond)
	topic := "candidate:ack-fault:" + scope + ":sla.breached"
	pending, err := client.XPendingExt(ctx, &redis.XPendingExtArgs{Stream: topic, Group: "itsm:event_audit", Start: "-", End: "+", Count: 10}).Result()
	require.NoError(t, err)
	require.Len(t, pending, 1)
	entries, err := client.XRange(ctx, topic, "-", "+").Result()
	require.NoError(t, err)
	require.Len(t, entries, 1)
	require.Equal(t, entries[0].ID, pending[0].ID)
	require.NoError(t, client.Do(ctx, "ACL", "SETUSER", "default", "+xack").Err())
	require.Eventually(t, func() bool {
		p, e := client.XPending(ctx, topic, "itsm:event_audit").Result()
		return e == nil && p.Count == 0
	}, 3*time.Second, 20*time.Millisecond)
	select {
	case value := <-received:
		require.Equal(t, "ack-denied-source", value.(eventbus.Envelope).EventID)
	default:
		t.Fatal("unacknowledged source was not redelivered")
	}
	after, err := client.XRange(ctx, topic, "-", "+").Result()
	require.NoError(t, err)
	require.Equal(t, entries, after)
}

type selectiveStreamAuthority struct{ denied atomic.Bool }

func (a *selectiveStreamAuthority) ValidateEvent(ctx context.Context, ref executionscope.Ref, env eventbus.Envelope) error {
	if env.EventID == "recover-me" && a.denied.Load() {
		return fmt.Errorf("temporarily denied")
	}
	return (candidateStreamFixtureAuthority{}).ValidateEvent(ctx, ref, env)
}

func TestPersistentStreamRecoversPendingDuringFreshTraffic(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()
	cfg, client := startCandidateStreamRedis(t, ctx)
	cfg.EventStream = config.EventStreamConfig{ClaimIdle: 200 * time.Millisecond, ClaimInterval: 20 * time.Millisecond, NackDelay: 20 * time.Millisecond}
	scope := uuid.NewString()
	execution := config.ExecutionConfig{Mode: "candidate", DeploymentID: "fair-recovery", Scopes: []config.ExecutionScopeConfig{{TenantID: 1, ScopeID: scope}}}
	authority := &selectiveStreamAuthority{}
	bus, err := eventbus.NewWatermillEventBus(cfg, execution, authority, zap.NewNop().Sugar())
	require.NoError(t, err)
	defer bus.Close()
	require.NoError(t, bus.Publish(candidateStreamEvent{tenantID: 1, WorkItemID: 200, eventID: "recover-me"}))
	authority.denied.Store(true)
	received := make(chan interface{}, 256)
	require.NoError(t, bus.RegisterSubscription("sla.breached", candidateStreamObserver{received}))
	require.NoError(t, bus.Start(ctx))
	topic := "candidate:fair-recovery:" + scope + ":sla.breached"
	require.Eventually(t, func() bool { return client.HLen(ctx, topic+":rejections:event_audit").Val() == 1 }, 3*time.Second, 20*time.Millisecond)
	produceCtx, stop := context.WithCancel(ctx)
	producerDone := make(chan error, 1)
	go func() {
		ticker := time.NewTicker(10 * time.Millisecond)
		defer ticker.Stop()
		for n := 0; ; n++ {
			select {
			case <-produceCtx.Done():
				producerDone <- nil
				return
			case <-ticker.C:
				if e := bus.Publish(candidateStreamEvent{tenantID: 1, WorkItemID: 200, eventID: fmt.Sprintf("fresh-%d", n)}); e != nil {
					producerDone <- e
					return
				}
			}
		}
	}()
	defer func() { stop(); require.NoError(t, <-producerDone) }()
	for i := 0; i < 5; i++ {
		select {
		case value := <-received:
			require.NotEqual(t, "recover-me", value.(eventbus.Envelope).EventID)
		case <-ctx.Done():
			t.Fatal("fresh traffic blocked")
		}
	}
	authority.denied.Store(false)
	recovered := false
	deadline := time.NewTimer(3 * time.Second)
	defer deadline.Stop()
	for !recovered {
		select {
		case value := <-received:
			recovered = value.(eventbus.Envelope).EventID == "recover-me"
		case <-deadline.C:
			t.Fatal("continuous fresh traffic starved pending recovery")
		}
	}
}

func TestPersistentStreamRejectsMissingRecoveryPermissionAtStart(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	cfg, client := startCandidateStreamRedis(t, ctx)
	execution := config.ExecutionConfig{Mode: "candidate", DeploymentID: "recovery-acl", Scopes: []config.ExecutionScopeConfig{{TenantID: 1, ScopeID: uuid.NewString()}}}
	bus, err := eventbus.NewWatermillEventBus(cfg, execution, candidateStreamFixtureAuthority{}, zap.NewNop().Sugar())
	require.NoError(t, err)
	defer bus.Close()
	require.NoError(t, bus.RegisterSubscription("sla.breached", candidateStreamObserver{make(chan interface{}, 1)}))
	require.NoError(t, client.Do(ctx, "ACL", "SETUSER", "default", "-xautoclaim").Err())
	defer func() {
		restoreCtx, done := context.WithTimeout(context.Background(), time.Second)
		defer done()
		require.NoError(t, client.Do(restoreCtx, "ACL", "SETUSER", "default", "+xautoclaim").Err())
	}()
	require.Error(t, bus.Start(ctx), "missing recovery permission must prevent startup")
}

func TestPersistentStreamPreservesPendingScanCursor(t *testing.T) {
	for _, count := range []int{1, 21} {
		t.Run(fmt.Sprint(count), func(t *testing.T) {
			ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
			defer cancel()
			cfg, client := startCandidateStreamRedis(t, ctx)
			cfg.EventStream = config.EventStreamConfig{ClaimIdle: time.Hour, ClaimInterval: 20 * time.Millisecond, NackDelay: 20 * time.Millisecond}
			scope := uuid.NewString()
			execution := config.ExecutionConfig{Mode: "candidate", DeploymentID: "cursor-test", Scopes: []config.ExecutionScopeConfig{{TenantID: 1, ScopeID: scope}}}
			bus, err := eventbus.NewWatermillEventBus(cfg, execution, candidateStreamFixtureAuthority{}, zap.NewNop().Sugar())
			require.NoError(t, err)
			defer bus.Close()
			for i := 0; i < count; i++ {
				require.NoError(t, bus.Publish(candidateStreamEvent{tenantID: 1, WorkItemID: 200, eventID: fmt.Sprintf("pending-%d", i)}))
			}
			topic := "candidate:cursor-test:" + scope + ":sla.breached"
			require.NoError(t, client.XGroupCreate(ctx, topic, "itsm:event_audit", "0").Err())
			rows, err := client.XReadGroup(ctx, &redis.XReadGroupArgs{Group: "itsm:event_audit", Consumer: "previous", Streams: []string{topic, ">"}, Count: int64(count)}).Result()
			require.NoError(t, err)
			require.Len(t, rows, 1)
			require.Len(t, rows[0].Messages, count)
			last := rows[0].Messages[count-1].ID
			require.NoError(t, client.Do(ctx, "XCLAIM", topic, "itsm:event_audit", "previous", 0, last, "IDLE", 7200000).Err())
			received := make(chan interface{}, 1)
			require.NoError(t, bus.RegisterSubscription("sla.breached", candidateStreamObserver{received}))
			require.NoError(t, bus.Start(ctx))
			select {
			case value := <-received:
				require.Equal(t, fmt.Sprintf("pending-%d", count-1), value.(eventbus.Envelope).EventID)
			case <-ctx.Done():
				t.Fatal("eligible pending was lost during initial claim or empty scan page")
			}
			require.Eventually(t, func() bool {
				p, e := client.XPending(ctx, topic, "itsm:event_audit").Result()
				return e == nil && p.Count == int64(count-1)
			}, 3*time.Second, 20*time.Millisecond)
			pending, err := client.XPendingExt(ctx, &redis.XPendingExtArgs{Stream: topic, Group: "itsm:event_audit", Start: "-", End: "+", Count: 30}).Result()
			require.NoError(t, err)
			for i, p := range pending {
				require.Equal(t, rows[0].Messages[i].ID, p.ID)
				require.Equal(t, "previous", p.Consumer)
				require.EqualValues(t, 1, p.RetryCount)
			}
		})
	}
}

func TestPersistentStreamTwoLiveConsumersShareOneGroup(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	cfg, client := startCandidateStreamRedis(t, ctx)
	cfg.EventStream = config.EventStreamConfig{ClaimIdle: time.Hour}
	scope := uuid.NewString()
	execution := config.ExecutionConfig{Mode: "candidate", DeploymentID: "two-consumers", Scopes: []config.ExecutionScopeConfig{{TenantID: 1, ScopeID: scope}}}
	received := make(chan interface{}, 32)
	makeBus := func() *eventbus.WatermillEventBus {
		bus, err := eventbus.NewWatermillEventBus(cfg, execution, candidateStreamFixtureAuthority{}, zap.NewNop().Sugar())
		require.NoError(t, err)
		require.NoError(t, bus.RegisterSubscription("sla.breached", candidateStreamObserver{received}))
		require.NoError(t, bus.Start(ctx))
		return bus
	}
	first, second := makeBus(), makeBus()
	defer first.Close()
	defer second.Close()
	for i := 0; i < 12; i++ {
		require.NoError(t, first.Publish(candidateStreamEvent{tenantID: 1, WorkItemID: 200, eventID: fmt.Sprintf("live-%d", i)}))
	}
	seen := map[string]bool{}
	for len(seen) < 12 {
		select {
		case value := <-received:
			id := value.(eventbus.Envelope).EventID
			require.False(t, seen[id])
			seen[id] = true
		case <-ctx.Done():
			t.Fatal("shared consumers did not deliver all sources")
		}
	}
	topic := "candidate:two-consumers:" + scope + ":sla.breached"
	require.Eventually(t, func() bool {
		p, e := client.XPending(ctx, topic, "itsm:event_audit").Result()
		return e == nil && p.Count == 0
	}, 3*time.Second, 20*time.Millisecond)
	consumers, err := client.XInfoConsumers(ctx, topic, "itsm:event_audit").Result()
	require.NoError(t, err)
	require.Len(t, consumers, 2)
	require.NotEqual(t, consumers[0].Name, consumers[1].Name)
	require.NoError(t, first.Close())
	require.NoError(t, second.Close())
	require.Empty(t, received, "closed consumers must not leave duplicate deliveries buffered")
}

func TestPersistentStreamMalformedWireDoesNotBlockFollowingSource(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	cfg, client := startCandidateStreamRedis(t, ctx)
	execution := config.ExecutionConfig{Mode: "candidate", DeploymentID: "bad-wire", Scopes: []config.ExecutionScopeConfig{{TenantID: 1, ScopeID: uuid.NewString()}}}
	topic := "candidate:bad-wire:" + execution.Scopes[0].ScopeID + ":sla.breached"
	for _, values := range []map[string]interface{}{{"payload": "wire-secret"}, {redisstream.UUIDHeaderKey: "wire-secret-id", "payload": "{}", "metadata": "invalid-msgpack"}} {
		_, err := client.XAdd(ctx, &redis.XAddArgs{Stream: topic, Values: values}).Result()
		require.NoError(t, err)
	}
	bus, err := eventbus.NewWatermillEventBus(cfg, execution, candidateStreamFixtureAuthority{}, zap.NewNop().Sugar())
	require.NoError(t, err)
	defer bus.Close()
	received := make(chan interface{}, 1)
	require.NoError(t, bus.RegisterSubscription("sla.breached", candidateStreamObserver{received}))
	require.NoError(t, bus.Start(ctx))
	require.NoError(t, bus.Publish(candidateStreamEvent{tenantID: 1, WorkItemID: 200, eventID: "after-bad-wire"}))
	select {
	case value := <-received:
		require.Equal(t, "after-bad-wire", value.(eventbus.Envelope).EventID)
	case <-ctx.Done():
		t.Fatal("bad wire blocked valid source")
	}
	require.Eventually(t, func() bool {
		p, e := client.XPending(ctx, topic, "itsm:event_audit").Result()
		return e == nil && p.Count == 2
	}, 3*time.Second, 20*time.Millisecond)
	evidence, err := client.HGetAll(ctx, topic+":rejections:event_audit").Result()
	require.NoError(t, err)
	require.Len(t, evidence, 2)
	for _, value := range evidence {
		require.NotContains(t, value, "wire-secret")
		var record map[string]interface{}
		require.NoError(t, json.Unmarshal([]byte(value), &record))
		require.Equal(t, "wire_invalid", record["reason"])
	}
}
