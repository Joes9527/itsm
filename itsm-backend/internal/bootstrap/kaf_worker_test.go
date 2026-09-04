package bootstrap

import (
	"context"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
)

func TestKAFWorkerApplicationRunStartsDispatcherAndStopsWithContext(t *testing.T) {
	runner := &blockingKafOutboxRunner{started: make(chan struct{})}
	health := &blockingWorkerHealthRunner{started: make(chan struct{})}
	app := &KAFWorkerApplication{
		dispatcher:   runner,
		healthRunner: health,
		closeDB:      func() {},
	}
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() { done <- app.Run(ctx) }()

	select {
	case <-runner.started:
	case <-time.After(time.Second):
		t.Fatal("KAF worker did not start dispatcher")
	}
	select {
	case <-health.started:
	case <-time.After(time.Second):
		t.Fatal("KAF worker did not start health server")
	}
	cancel()

	assert.NoError(t, <-done)
	assert.Equal(t, int32(1), runner.runs.Load())
}

type blockingWorkerHealthRunner struct {
	started chan struct{}
}

func (r *blockingWorkerHealthRunner) Run(ctx context.Context) error {
	close(r.started)
	<-ctx.Done()
	return nil
}
