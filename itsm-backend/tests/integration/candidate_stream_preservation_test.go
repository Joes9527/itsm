//go:build candidate_scope

package integration

import (
	"context"
	"fmt"
	"net"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/redis/go-redis/v9"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"go.uber.org/zap"
	"itsm-backend/config"
	"itsm-backend/pkg/eventbus"
)

type candidateStreamEvent struct {
	WorkItemID   int    `json:"workItemId"`
	DeploymentID string `json:"deploymentId"`
	ScopeID      string `json:"scopeId"`
}

func (candidateStreamEvent) EventType() string     { return "sla.breached" }
func (candidateStreamEvent) TenantID() string      { return "1" }
func (candidateStreamEvent) OccurredAt() time.Time { return time.Unix(1700000000, 0).UTC() }

type candidateStreamObserver struct{ received chan interface{} }

func (h candidateStreamObserver) Handle(event interface{}) error {
	h.received <- event
	return nil
}

// This is the transport isolation RED. It deliberately uses the application's
// existing constructor, which currently cannot accept a frozen scope policy.
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
	legacyPublisher, err := eventbus.NewWatermillEventBus(cfg, zap.NewNop().Sugar())
	require.NoError(t, err)
	require.NoError(t, legacyPublisher.Publish(candidateStreamEvent{WorkItemID: 100}))
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

	bus, err := eventbus.NewWatermillEventBus(cfg, zap.NewNop().Sugar())
	require.NoError(t, err)
	defer bus.Close()
	observed := make(chan interface{}, 4)
	require.NoError(t, bus.RegisterSubscription(topic, candidateStreamObserver{observed}))
	require.NoError(t, bus.Start(ctx))
	// Confirm the real subscriber is waiting before the positive-control publish.
	require.Eventually(t, func() bool {
		clients, e := client.ClientList(ctx).Result()
		return e == nil && strings.Contains(clients, "cmd=xread")
	}, 3*time.Second, 10*time.Millisecond)
	scope := uuid.NewString()
	require.NoError(t, bus.Publish(candidateStreamEvent{WorkItemID: 200, DeploymentID: "stream-test", ScopeID: scope}))
	select {
	case event := <-observed:
		require.EqualValues(t, 200, event.(map[string]interface{})["workItemId"], "new event positive control")
	case <-ctx.Done():
		t.Fatal("subscriber did not process new event")
	}
	require.NoError(t, bus.Close())
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
}
