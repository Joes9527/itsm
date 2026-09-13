package service

import (
	"context"
	"errors"
	"sync"
	"testing"
	"time"

	"itsm-backend/ent/enttest"

	_ "github.com/mattn/go-sqlite3"
	"github.com/stretchr/testify/require"
	"go.uber.org/zap"
	"go.uber.org/zap/zapcore"
	"go.uber.org/zap/zaptest/observer"
)

func newLifecycleTestQueue(t *testing.T, capacity int, processor func(context.Context, ToolJob) error, logger *zap.SugaredLogger) *ToolQueue {
	t.Helper()
	q := &ToolQueue{}
	q.initialize(capacity, logger, processor, allowLifecycleAdmission)
	require.NoError(t, q.Start(context.Background()))
	t.Cleanup(q.Close)
	return q
}

func TestToolQueueRequiresExplicitStartAndHonorsCancellation(t *testing.T) {
	q := &ToolQueue{}
	started := make(chan struct{})
	q.initialize(1, zap.NewNop().Sugar(), func(ctx context.Context, _ ToolJob) error {
		close(started)
		<-ctx.Done()
		return ctx.Err()
	}, allowLifecycleAdmission)
	t.Cleanup(q.Close)
	require.Error(t, q.Enqueue(ToolJob{InvocationID: 1, TenantID: 1}))
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	require.NoError(t, q.Start(ctx))
	require.Error(t, q.Start(ctx), "duplicate start must not create another worker")
	require.NoError(t, q.Enqueue(ToolJob{InvocationID: 1, TenantID: 1}))
	<-started
	cancel()
	q.Close()
	require.Error(t, q.Start(context.Background()))
}

func TestToolQueueCanCloseBeforeStart(t *testing.T) {
	q := &ToolQueue{}
	q.initialize(1, nil, func(context.Context, ToolJob) error { t.Fatal("unstarted processor ran"); return nil }, allowLifecycleAdmission)
	q.Close()
	require.Error(t, q.Start(context.Background()))
}

func TestToolQueueCloseWaitsForActiveJobAndRejectsNewWork(t *testing.T) {
	started := make(chan struct{})
	release := make(chan struct{})
	q := newLifecycleTestQueue(t, 2, func(context.Context, ToolJob) error {
		close(started)
		<-release
		return nil
	}, zap.NewNop().Sugar())

	require.NoError(t, q.Enqueue(ToolJob{InvocationID: 1, TenantID: 7}))
	<-started

	firstClose := make(chan struct{})
	secondClose := make(chan struct{})
	go func() { q.Close(); close(firstClose) }()
	go func() { q.Close(); close(secondClose) }()
	<-q.stopping

	require.ErrorIs(t, q.Enqueue(ToolJob{InvocationID: 2, TenantID: 7}), ErrToolQueueClosed)
	select {
	case <-firstClose:
		t.Fatal("Close returned while the active job was still running")
	default:
	}
	select {
	case <-secondClose:
		t.Fatal("concurrent Close returned while the active job was still running")
	default:
	}

	close(release)
	<-firstClose
	<-secondClose
	require.ErrorIs(t, q.Enqueue(ToolJob{InvocationID: 3, TenantID: 7}), ErrToolQueueClosed)
}

func TestToolQueueCloseLeavesBufferedApprovedInvocationPendingAndRequeueable(t *testing.T) {
	client := enttest.Open(t, "sqlite3", "file:tool_queue_shutdown?mode=memory&cache=shared&_fk=1")
	t.Cleanup(func() { require.NoError(t, client.Close()) })
	ctx := context.Background()
	first := client.ToolInvocation.Create().
		SetTenantID(9).
		SetToolName("create_ticket").
		SetArguments(`{"title":"active"}`).
		SetNeedsApproval(true).
		SetApprovalState("approved").
		SetStatus("pending").
		SaveX(ctx)
	buffered := client.ToolInvocation.Create().
		SetTenantID(9).
		SetToolName("create_ticket").
		SetArguments(`{"title":"buffered"}`).
		SetNeedsApproval(true).
		SetApprovalState("approved").
		SetStatus("pending").
		SaveX(ctx)

	logCore, observed := observer.New(zapcore.WarnLevel)
	active := make(chan struct{})
	release := make(chan struct{})
	var processedMu sync.Mutex
	processed := make([]int, 0, 1)
	q := newLifecycleTestQueue(t, 2, func(_ context.Context, job ToolJob) error {
		processedMu.Lock()
		processed = append(processed, job.InvocationID)
		processedMu.Unlock()
		close(active)
		<-release
		return nil
	}, zap.New(logCore).Sugar())

	require.NoError(t, q.Enqueue(ToolJob{InvocationID: first.ID, TenantID: 9}))
	<-active
	require.NoError(t, q.Enqueue(ToolJob{InvocationID: buffered.ID, TenantID: 9}))
	closed := make(chan struct{})
	go func() { q.Close(); close(closed) }()
	<-q.stopping
	close(release)
	<-closed

	processedMu.Lock()
	require.Equal(t, []int{first.ID}, processed)
	processedMu.Unlock()
	recorded := client.ToolInvocation.GetX(ctx, buffered.ID)
	require.Equal(t, "approved", recorded.ApprovalState)
	require.Equal(t, "pending", recorded.Status)
	entries := observed.FilterMessage("Approved tool job remains pending after queue shutdown").All()
	require.Len(t, entries, 1)
	require.Equal(t, int64(buffered.ID), entries[0].ContextMap()["invocation_id"])
	require.Equal(t, int64(9), entries[0].ContextMap()["tenant_id"])

	requeued := make(chan ToolJob, 1)
	q2 := newLifecycleTestQueue(t, 1, func(_ context.Context, job ToolJob) error {
		requeued <- job
		return nil
	}, zap.NewNop().Sugar())
	require.NoError(t, q2.Enqueue(ToolJob{InvocationID: buffered.ID, TenantID: 9}))
	require.Equal(t, buffered.ID, (<-requeued).InvocationID)
}

func TestToolQueueEnqueueCompetesWithCloseAtSharedStartBarrier(t *testing.T) {
	client := enttest.Open(t, "sqlite3", "file:tool_queue_close_race?mode=memory&cache=shared&_fk=1")
	t.Cleanup(func() { require.NoError(t, client.Close()) })
	ctx := context.Background()
	activeInvocation := client.ToolInvocation.Create().
		SetTenantID(11).
		SetToolName("create_ticket").
		SetArguments(`{"title":"active"}`).
		SetNeedsApproval(true).
		SetApprovalState("approved").
		SetStatus("pending").
		SaveX(ctx)
	contendingInvocation := client.ToolInvocation.Create().
		SetTenantID(11).
		SetToolName("create_ticket").
		SetArguments(`{"title":"contending"}`).
		SetNeedsApproval(true).
		SetApprovalState("approved").
		SetStatus("pending").
		SaveX(ctx)

	logCore, observed := observer.New(zapcore.WarnLevel)
	active := make(chan struct{})
	releaseActive := make(chan struct{})
	processed := make(chan int, 1)
	q := newLifecycleTestQueue(t, 2, func(_ context.Context, job ToolJob) error {
		processed <- job.InvocationID
		close(active)
		<-releaseActive
		return nil
	}, zap.New(logCore).Sugar())
	require.NoError(t, q.Enqueue(ToolJob{InvocationID: activeInvocation.ID, TenantID: 11}))
	<-active

	start := make(chan struct{})
	var ready sync.WaitGroup
	ready.Add(3)
	enqueueResult := make(chan error, 1)
	closeResults := make(chan struct{}, 2)
	go func() {
		ready.Done()
		<-start
		enqueueResult <- q.Enqueue(ToolJob{InvocationID: contendingInvocation.ID, TenantID: 11})
	}()
	for range 2 {
		go func() {
			ready.Done()
			<-start
			q.Close()
			closeResults <- struct{}{}
		}()
	}
	ready.Wait()
	close(start)

	enqueueErr := <-enqueueResult
	acceptedBeforeStop := enqueueErr == nil
	if !acceptedBeforeStop {
		require.ErrorIs(t, enqueueErr, ErrToolQueueClosed)
	}
	<-q.stopping
	require.ErrorIs(t, q.Enqueue(ToolJob{InvocationID: contendingInvocation.ID, TenantID: 11}), ErrToolQueueClosed)
	recorded := client.ToolInvocation.GetX(ctx, contendingInvocation.ID)
	require.Equal(t, "approved", recorded.ApprovalState)
	require.Equal(t, "pending", recorded.Status)

	close(releaseActive)
	<-closeResults
	<-closeResults
	require.Equal(t, activeInvocation.ID, <-processed)
	select {
	case unexpected := <-processed:
		t.Fatalf("buffered invocation %d executed after stopping", unexpected)
	default:
	}
	require.ErrorIs(t, q.Enqueue(ToolJob{InvocationID: contendingInvocation.ID, TenantID: 11}), ErrToolQueueClosed)
	warnings := observed.FilterMessage("Approved tool job remains pending after queue shutdown").All()
	if acceptedBeforeStop {
		require.Len(t, warnings, 1)
		require.Equal(t, int64(contendingInvocation.ID), warnings[0].ContextMap()["invocation_id"])
	} else {
		require.Empty(t, warnings)
	}

	requeued := make(chan ToolJob, 1)
	q2 := newLifecycleTestQueue(t, 1, func(_ context.Context, job ToolJob) error {
		requeued <- job
		return nil
	}, zap.NewNop().Sugar())
	require.NoError(t, q2.Enqueue(ToolJob{InvocationID: contendingInvocation.ID, TenantID: 11}))
	require.Equal(t, contendingInvocation.ID, (<-requeued).InvocationID)
}

func TestToolQueueRejectsInvalidIdentity(t *testing.T) {
	q := newLifecycleTestQueue(t, 1, func(context.Context, ToolJob) error {
		return errors.New("must not run")
	}, zap.NewNop().Sugar())
	require.Error(t, q.Enqueue(ToolJob{}))
}

func allowLifecycleAdmission(ctx context.Context, _ ToolJob) error { return ctx.Err() }

func TestToolQueueCloseWaitsForAdmissionAndPreventsLateEnqueue(t *testing.T) {
	for _, lateSuccess := range []bool{false, true} {
		name := "honors cancellation"
		if lateSuccess {
			name = "returns success after cancellation"
		}
		t.Run(name, func(t *testing.T) {
			q := &ToolQueue{}
			entered, canceled, release := make(chan struct{}), make(chan struct{}), make(chan struct{})
			var releaseOnce sync.Once
			processed := make(chan struct{}, 1)
			q.initialize(1, nil, func(context.Context, ToolJob) error { processed <- struct{}{}; return nil }, func(ctx context.Context, _ ToolJob) error {
				close(entered)
				<-ctx.Done()
				close(canceled)
				<-release
				if lateSuccess {
					return nil
				}
				return ctx.Err()
			})
			require.NoError(t, q.Start(context.Background()))
			t.Cleanup(func() { releaseOnce.Do(func() { close(release) }); q.Close() })
			await := func(ch <-chan struct{}) {
				t.Helper()
				select {
				case <-ch:
				case <-time.After(5 * time.Second):
					t.Fatal("queue lifecycle barrier timed out")
				}
			}
			enqueueDone := make(chan error, 1)
			go func() { enqueueDone <- q.Enqueue(ToolJob{InvocationID: 1, TenantID: 1}) }()
			await(entered)
			closed := make(chan struct{})
			go func() { q.Close(); close(closed) }()
			await(canceled)
			select {
			case <-closed:
				t.Error("Close returned before admission exited")
			default:
			}
			releaseOnce.Do(func() { close(release) })
			select {
			case err := <-enqueueDone:
				if lateSuccess {
					require.ErrorIs(t, err, ErrToolQueueClosed)
				} else {
					require.ErrorIs(t, err, context.Canceled)
				}
			case <-time.After(5 * time.Second):
				t.Fatal("enqueue did not return")
			}
			await(closed)
			require.Empty(t, processed)
			require.ErrorIs(t, q.Enqueue(ToolJob{InvocationID: 2, TenantID: 1}), ErrToolQueueClosed)
		})
	}
}
