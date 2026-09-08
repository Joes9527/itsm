package bootstrap

import (
	"context"
	"sync"
	"testing"

	"github.com/stretchr/testify/require"
)

type recordingToolQueueCloser struct {
	mu     *sync.Mutex
	events *[]string
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
		toolQueue:                recordingToolQueueCloser{mu: &mu, events: &events},
		startBackgroundTasksFunc: func(context.Context) {},
	}
	stopRuntime := app.startAPIRuntime(context.Background())
	stopAPIRuntimeBeforeDependencies(stopRuntime, func() {
		mu.Lock()
		defer mu.Unlock()
		events = append(events, "dependencies")
	})
	require.Equal(t, []string{"tool_queue", "dependencies"}, events)
}
