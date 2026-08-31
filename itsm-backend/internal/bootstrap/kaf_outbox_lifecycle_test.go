package bootstrap

import (
	"context"
	"net"
	"net/http"
	"sync/atomic"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
)

type blockingKafOutboxRunner struct {
	started chan struct{}
	runs    atomic.Int32
}

func (r *blockingKafOutboxRunner) Run(ctx context.Context) {
	r.runs.Add(1)
	close(r.started)
	<-ctx.Done()
}

func TestApplication_StartKafOutboxDispatcherRunsOnceAndWaitsForCancellation(t *testing.T) {
	runner := &blockingKafOutboxRunner{started: make(chan struct{})}
	app := &Application{KAFOutboxDispatcher: runner}
	ctx, cancel := context.WithCancel(context.Background())
	wait := app.startKafOutboxDispatcher(ctx)

	select {
	case <-runner.started:
	case <-time.After(time.Second):
		t.Fatal("KAF outbox dispatcher did not start")
	}
	cancel()
	wait()

	assert.Equal(t, int32(1), runner.runs.Load())
}

func TestApplication_StartWorkflowOutboxDispatcherRunsOnceAndWaitsForCancellation(t *testing.T) {
	runner := &blockingKafOutboxRunner{started: make(chan struct{})}
	app := &Application{WorkflowOutboxDispatcher: runner}
	ctx, cancel := context.WithCancel(context.Background())
	wait := app.startWorkflowOutboxDispatcher(ctx)

	select {
	case <-runner.started:
	case <-time.After(time.Second):
		t.Fatal("workflow outbox dispatcher did not start")
	}
	cancel()
	wait()
	assert.Equal(t, int32(1), runner.runs.Load())
}

func TestServeUntilContextCancelledShutsDownServer(t *testing.T) {
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	server := &http.Server{Handler: http.NewServeMux()}
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() {
		done <- serveUntilContextCancelled(ctx, server, listener)
	}()
	cancel()

	select {
	case err := <-done:
		assert.NoError(t, err)
	case <-time.After(time.Second):
		t.Fatal("HTTP server did not stop after context cancellation")
	}
}
