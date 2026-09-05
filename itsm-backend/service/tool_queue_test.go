package service

import (
	"context"
	"errors"
	"sync"
	"testing"

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
	q.start(capacity, logger, processor)
	t.Cleanup(q.Close)
	return q
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

func TestToolQueueRejectsInvalidIdentity(t *testing.T) {
	q := newLifecycleTestQueue(t, 1, func(context.Context, ToolJob) error {
		return errors.New("must not run")
	}, zap.NewNop().Sugar())
	require.Error(t, q.Enqueue(ToolJob{}))
}
