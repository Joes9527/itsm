package bootstrap

import (
	"context"
	"itsm-backend/config"
	"sync"
	"testing"

	"github.com/stretchr/testify/require"
)

type recordingToolQueueCloser struct {
	mu     *sync.Mutex
	events *[]string
}

func (c recordingToolQueueCloser) Start(context.Context) error {
	c.mu.Lock()
	defer c.mu.Unlock()
	*c.events = append(*c.events, "tool_start")
	return nil
}

func (c recordingToolQueueCloser) Close() {
	c.mu.Lock()
	defer c.mu.Unlock()
	*c.events = append(*c.events, "tool_queue")
}

func TestApplicationRuntimeStopsToolQueueBeforeDependencyShutdown(t *testing.T) {
	var mu sync.Mutex
	events := make([]string, 0, 2)
	app := &Application{
		Cfg:                      &config.Config{Execution: config.ExecutionConfig{Mode: "standard", DeploymentID: "lifecycle-test", Capabilities: map[string]string{"tool_queue": "enabled"}}},
		toolQueue:                recordingToolQueueCloser{mu: &mu, events: &events},
		startBackgroundTasksFunc: func(context.Context) {},
	}
	stopRuntime, err := app.startAPIRuntime(context.Background())
	require.NoError(t, err)
	stopAPIRuntimeBeforeDependencies(stopRuntime, func() {
		mu.Lock()
		defer mu.Unlock()
		events = append(events, "dependencies")
	})
	require.Equal(t, []string{"tool_start", "tool_queue", "dependencies"}, events)
}
