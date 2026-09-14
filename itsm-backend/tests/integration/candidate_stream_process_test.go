//go:build candidate_scope

package integration

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/redis/go-redis/v9"
	"github.com/stretchr/testify/require"
	"go.uber.org/zap"
	"itsm-backend/config"
	"itsm-backend/pkg/eventbus"
)

type streamProcessInput struct {
	Redis     config.RedisConfig
	Execution config.ExecutionConfig
	ServerPID string
	Receipt   string
	Hold      bool
}

type streamProcessHandler struct{ input streamProcessInput }

func (streamProcessHandler) EventConsumerID() string    { return "event_audit" }
func (h streamProcessHandler) Handle(interface{}) error { return fmt.Errorf("context required") }
func (h streamProcessHandler) HandleContext(ctx context.Context, value interface{}) error {
	env, ok := value.(eventbus.Envelope)
	if !ok {
		return fmt.Errorf("envelope required")
	}
	data, err := json.Marshal(env)
	if err != nil {
		return err
	}
	if err := os.WriteFile(h.input.Receipt, data, 0600); err != nil {
		return err
	}
	if h.input.Hold {
		<-ctx.Done()
		return ctx.Err()
	}
	return nil
}

// Executed only by the parent with private connection data supplied on stdin.
// This is a real consumer process, not a full application or business owner.
func TestCandidateStreamConsumerProcess(t *testing.T) {
	if os.Getenv("ITSM_TEST_STREAM_CHILD") != "1" {
		return
	}
	var input streamProcessInput
	require.NoError(t, json.NewDecoder(os.Stdin).Decode(&input))
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()
	client := redis.NewClient(&redis.Options{Addr: fmt.Sprintf("%s:%d", input.Redis.Host, input.Redis.Port), Password: input.Redis.Password})
	defer client.Close()
	info, err := client.Info(ctx, "server").Result()
	require.NoError(t, err)
	require.Contains(t, info, "process_id:"+input.ServerPID+"\r\n")
	bus, err := eventbus.NewWatermillEventBus(&input.Redis, input.Execution, candidateStreamFixtureAuthority{}, zap.NewNop().Sugar())
	require.NoError(t, err)
	defer bus.Close()
	require.NoError(t, bus.RegisterSubscription("sla.breached", streamProcessHandler{input}))
	require.NoError(t, bus.Start(ctx))
	if input.Hold {
		<-ctx.Done()
		t.Fatal("parent did not terminate unacknowledged consumer")
		return
	}
	topic := "candidate:" + input.Execution.DeploymentID + ":" + input.Execution.Scopes[0].ScopeID + ":sla.breached"
	require.Eventually(t, func() bool {
		if _, err := os.Stat(input.Receipt); err != nil {
			return false
		}
		pending, err := client.XPending(ctx, topic, "itsm:event_audit").Result()
		return err == nil && pending.Count == 0
	}, 8*time.Second, 20*time.Millisecond)
}

func TestPersistentStreamRecoversAfterConsumerProcessKill(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 25*time.Second)
	defer cancel()
	cfg, client := startCandidateStreamRedis(t, ctx)
	cfg.EventStream = config.EventStreamConfig{ClaimIdle: 200 * time.Millisecond, ClaimInterval: 20 * time.Millisecond}
	info, err := client.Info(ctx, "server").Result()
	require.NoError(t, err)
	var pid string
	for _, line := range strings.Split(info, "\r\n") {
		if strings.HasPrefix(line, "process_id:") {
			pid = strings.TrimPrefix(line, "process_id:")
		}
	}
	require.NotEmpty(t, pid)
	execution := config.ExecutionConfig{Mode: "candidate", DeploymentID: "process-recovery", Scopes: []config.ExecutionScopeConfig{{TenantID: 1, ScopeID: uuid.NewString()}}}
	topic := "candidate:process-recovery:" + execution.Scopes[0].ScopeID + ":sla.breached"
	publisher, err := eventbus.NewWatermillEventBus(cfg, execution, candidateStreamFixtureAuthority{}, zap.NewNop().Sugar())
	require.NoError(t, err)
	require.NoError(t, publisher.Publish(candidateStreamEvent{tenantID: 1, WorkItemID: 200, eventID: "process-kill-source"}))
	require.NoError(t, publisher.Close())
	before, err := client.XRange(ctx, topic, "-", "+").Result()
	require.NoError(t, err)
	require.Len(t, before, 1)
	directory := t.TempDir()
	executable, err := os.Executable()
	require.NoError(t, err)
	launch := func(name string, hold bool) (*exec.Cmd, <-chan error, string) {
		receipt := filepath.Join(directory, name+".json")
		input, err := json.Marshal(streamProcessInput{Redis: *cfg, Execution: execution, ServerPID: pid, Receipt: receipt, Hold: hold})
		require.NoError(t, err)
		cmd := exec.CommandContext(ctx, executable, "-test.run=^TestCandidateStreamConsumerProcess$", "-test.count=1")
		cmd.Env = append(os.Environ(), "ITSM_TEST_STREAM_CHILD=1")
		cmd.Stdin = strings.NewReader(string(input))
		log, err := os.Create(filepath.Join(directory, name+".log"))
		require.NoError(t, err)
		cmd.Stdout, cmd.Stderr = log, log
		require.NoError(t, cmd.Start())
		done := make(chan error, 1)
		exited := make(chan struct{})
		go func() { done <- cmd.Wait(); close(exited); _ = log.Close() }()
		t.Cleanup(func() { _ = cmd.Process.Kill(); <-exited })
		return cmd, done, receipt
	}
	first, firstDone, firstReceipt := launch("first", true)
	var firstBody []byte
	require.Eventually(t, func() bool {
		body, err := os.ReadFile(firstReceipt)
		if err != nil {
			return false
		}
		var envelope eventbus.Envelope
		if json.Unmarshal(body, &envelope) != nil || envelope.EventID != "process-kill-source" || envelope.Execution == nil || envelope.Execution.WorkItemID != 200 {
			return false
		}
		firstBody = body
		return true
	}, 5*time.Second, 20*time.Millisecond)
	pending, err := client.XPendingExt(ctx, &redis.XPendingExtArgs{Stream: topic, Group: "itsm:event_audit", Start: "-", End: "+", Count: 10}).Result()
	require.NoError(t, err)
	require.Len(t, pending, 1)
	require.Equal(t, before[0].ID, pending[0].ID)
	require.NoError(t, first.Process.Kill())
	require.Error(t, <-firstDone, "forced termination must not be a graceful exit")
	afterKill, err := client.XPendingExt(ctx, &redis.XPendingExtArgs{Stream: topic, Group: "itsm:event_audit", Start: "-", End: "+", Count: 10}).Result()
	require.NoError(t, err)
	require.Len(t, afterKill, 1)
	require.Equal(t, pending[0].ID, afterKill[0].ID)
	require.Equal(t, pending[0].Consumer, afterKill[0].Consumer)
	second, secondDone, secondReceipt := launch("second", false)
	require.NotEqual(t, first.Process.Pid, second.Process.Pid)
	require.NoError(t, <-secondDone)
	secondBody, err := os.ReadFile(secondReceipt)
	require.NoError(t, err)
	require.JSONEq(t, string(firstBody), string(secondBody))
	remaining, err := client.XPending(ctx, topic, "itsm:event_audit").Result()
	require.NoError(t, err)
	require.Zero(t, remaining.Count)
	after, err := client.XRange(ctx, topic, "-", "+").Result()
	require.NoError(t, err)
	require.Equal(t, before, after)
	consumers, err := client.XInfoConsumers(ctx, topic, "itsm:event_audit").Result()
	require.NoError(t, err)
	require.Len(t, consumers, 2)
	require.NotEqual(t, consumers[0].Name, consumers[1].Name)
}
