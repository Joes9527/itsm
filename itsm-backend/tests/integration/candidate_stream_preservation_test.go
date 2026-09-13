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
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/redis/go-redis/v9"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"go.uber.org/zap"
	"itsm-backend/common/executionscope"
	"itsm-backend/config"
	"itsm-backend/pkg/eventbus"
)

type candidateStreamEvent struct {
	tenantID     int
	WorkItemID   int    `json:"workItemId"`
	DeploymentID string `json:"deploymentId"`
	ScopeID      string `json:"scopeId"`
}

func (candidateStreamEvent) EventType() string     { return "sla.breached" }
func (e candidateStreamEvent) TenantID() string    { return strconv.Itoa(e.tenantID) }
func (candidateStreamEvent) OccurredAt() time.Time { return time.Unix(1700000000, 0).UTC() }

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
	binary := os.Getenv("CANDIDATE_TEST_REDIS_BINARY")
	if binary == "" {
		t.Skip("requires explicit private Redis binary")
	}
	require.True(t, filepath.IsAbs(binary))
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()
	reservation, err := net.Listen("tcp", "127.0.0.1:0")
	require.NoError(t, err)
	port := reservation.Addr().(*net.TCPAddr).Port
	require.NoError(t, reservation.Close())
	directory := t.TempDir()
	password := uuid.NewString()
	command := exec.CommandContext(ctx, binary, "--requirepass", password, "--bind", "127.0.0.1", "--port", fmt.Sprint(port), "--save", "", "--appendonly", "no", "--dir", directory)
	log, err := os.Create(filepath.Join(directory, "redis.log"))
	require.NoError(t, err)
	defer log.Close()
	command.Stdout, command.Stderr = log, log
	require.NoError(t, command.Start())
	exited := make(chan struct{})
	go func() { _ = command.Wait(); close(exited) }()
	defer func() { _ = command.Process.Kill(); <-exited }()
	client := redis.NewClient(&redis.Options{Addr: fmt.Sprintf("127.0.0.1:%d", port), Password: password})
	defer client.Close()
	require.Eventually(t, func() bool {
		info, e := client.Info(ctx, "server").Result()
		return e == nil && strings.Contains(info, fmt.Sprintf("process_id:%d\r\n", command.Process.Pid))
	}, 5*time.Second, 20*time.Millisecond, "verify test Redis PID before writing fixtures")
	cfg := &config.RedisConfig{Host: "127.0.0.1", Port: port, Password: password}
	legacyPublisher, err := eventbus.NewWatermillEventBus(cfg, config.ExecutionConfig{Mode: "standard", DeploymentID: "legacy-test"}, nil, zap.NewNop().Sugar())
	require.NoError(t, err)
	require.NoError(t, legacyPublisher.Publish(candidateStreamEvent{tenantID: 1, WorkItemID: 100}))
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
	return fmt.Sprintf("fixture-%d", e.WorkItemID)
}
