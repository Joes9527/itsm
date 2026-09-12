package bootstrap

import (
	"context"
	"github.com/stretchr/testify/require"
	"go.uber.org/zap"
	"itsm-backend/config"
	"net"
	"sync"
	"testing"
)

func TestOccupiedPortDoesNotStartRuntime(t *testing.T) {
	listener, err := net.Listen("tcp", ":0")
	require.NoError(t, err)
	defer listener.Close()
	var mu sync.Mutex
	events := []string{}
	app := &Application{Cfg: &config.Config{Execution: config.ExecutionConfig{Mode: "standard", DeploymentID: "startup-test", Capabilities: map[string]string{"tool_queue": "enabled"}}}, Logger: zap.NewNop().Sugar(), toolQueue: recordingToolQueueCloser{&mu, &events}}
	app.Cfg.Server.Port = listener.Addr().(*net.TCPAddr).Port
	require.Error(t, app.Run())
	require.Empty(t, events)
}

func TestServeFailureStopsRuntime(t *testing.T) {
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	require.NoError(t, err)
	require.NoError(t, listener.Close())
	var mu sync.Mutex
	events := []string{}
	app := &Application{Cfg: &config.Config{Execution: config.ExecutionConfig{Mode: "standard", DeploymentID: "startup-test", Capabilities: map[string]string{"tool_queue": "enabled"}}}, Logger: zap.NewNop().Sugar(), toolQueue: recordingToolQueueCloser{&mu, &events}, startBackgroundTasksFunc: func(context.Context) {}}
	require.Error(t, app.runHTTPRuntime(context.Background(), listener))
	require.Equal(t, []string{"tool_start", "tool_queue"}, events)
}

func TestEnabledCapabilityRequiresRunnerBeforeStartingAnything(t *testing.T) {
	for _, capability := range []string{"tool_queue", "outbox", "callback", "notification", "event_audit", "webhook", "connector_poll", "embedding", "sla", "escalation"} {
		t.Run(capability, func(t *testing.T) {
			app := &Application{Cfg: &config.Config{Execution: config.ExecutionConfig{Mode: "standard", DeploymentID: "startup-test", Capabilities: map[string]string{capability: "enabled"}}}, startBackgroundTasksFunc: func(context.Context) { t.Error("background work started before admission") }}
			stop, err := app.startAPIRuntime(context.Background())
			if stop != nil {
				stop()
			}
			require.Error(t, err)
		})
	}
}
