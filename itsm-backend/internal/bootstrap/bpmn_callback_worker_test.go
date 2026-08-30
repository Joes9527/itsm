package bootstrap

import (
	"context"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

type recordingBPMNCallbackWorker struct {
	starts  atomic.Int32
	started chan callbackWorkerStart
	stopped chan struct{}
}

type callbackWorkerStart struct {
	workerID string
	interval time.Duration
}

func (w *recordingBPMNCallbackWorker) RunCallbackOutboxWorker(ctx context.Context, workerID string, interval time.Duration) {
	w.starts.Add(1)
	w.started <- callbackWorkerStart{workerID: workerID, interval: interval}
	<-ctx.Done()
	close(w.stopped)
}

func TestApplicationStartsOneCallbackWorkerAndStopsOnCancellation(t *testing.T) {
	worker := &recordingBPMNCallbackWorker{
		started: make(chan callbackWorkerStart, 1),
		stopped: make(chan struct{}),
	}
	app := &Application{callbackWorker: worker}
	ctx, cancel := context.WithCancel(context.Background())
	app.startCallbackOutboxWorker(ctx)

	select {
	case start := <-worker.started:
		require.True(t, strings.HasPrefix(start.workerID, "bpmn-callback-"))
		require.Greater(t, len(start.workerID), len("bpmn-callback-"))
		require.Equal(t, 2*time.Second, start.interval)
	case <-time.After(time.Second):
		t.Fatal("application did not start callback worker immediately")
	}
	require.Equal(t, int32(1), worker.starts.Load())

	cancel()
	select {
	case <-worker.stopped:
	case <-time.After(time.Second):
		t.Fatal("application callback worker did not stop after cancellation")
	}
}
