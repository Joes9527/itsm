package service

import (
	"context"
	"errors"
	"testing"
	"time"

	"itsm-backend/ent"
	"itsm-backend/ent/enttest"
	"itsm-backend/ent/processcallbackoutbox"
	"itsm-backend/service/bpmn"

	"github.com/stretchr/testify/require"
)

type fakeBPMNCallbackExecutor struct {
	err                 error
	keys                []string
	vars                []map[string]interface{}
	completionCommitted bool
}

func (e *fakeBPMNCallbackExecutor) executeClaimedCallback(ctx context.Context, _ string, row *ent.ProcessCallbackOutbox) (bpmnCallbackExecutionResult, error) {
	key, ok := bpmn.BPMNCallbackExecutionKey(ctx)
	if !ok {
		return bpmnCallbackExecutionResult{}, errors.New("missing execution key")
	}
	e.keys = append(e.keys, key)
	e.vars = append(e.vars, row.Variables)
	return bpmnCallbackExecutionResult{CompletionCommitted: e.completionCommitted}, e.err
}

func openBPMNCallbackOutboxClient(t *testing.T) *ent.Client {
	t.Helper()
	client := enttest.Open(t, "sqlite3", "file:bpmn_callback_outbox_lifecycle?mode=memory&cache=shared&_fk=1")
	t.Cleanup(func() { _ = client.Close() })
	require.NoError(t, client.Schema.Create(context.Background()))
	return client
}

func newBPMNCallbackOutboxForTest(client *ent.Client, executor bpmnCallbackExecutor, now time.Time) *bpmnCallbackOutbox {
	return &bpmnCallbackOutbox{
		client:   client,
		executor: executor,
		now:      func() time.Time { return now },
	}
}

func enqueueBPMNCallbackOutboxForTest(t *testing.T, outbox *bpmnCallbackOutbox, key string) *ent.ProcessCallbackOutbox {
	t.Helper()
	row, err := outbox.enqueue(context.Background(), outbox.client, bpmnCallbackEnqueueRequest{
		ExecutionKey:      key,
		TenantID:          7,
		ProcessInstanceID: 101,
		ProcessTaskID:     202,
		TaskID:            "task-202",
		CallbackKind:      "service_task",
		HandlerID:         "fake_handler",
		TaskType:          "fake_task",
		ElementID:         "Activity_Notify",
		Variables:         map[string]interface{}{"bpmn_callback_execution_key": "client-value"},
	})
	require.NoError(t, err)
	return row
}

func TestBPMNCallbackOutboxClaimUsesCAS(t *testing.T) {
	client := openBPMNCallbackOutboxClient(t)
	now := time.Date(2026, 8, 30, 12, 0, 0, 0, time.UTC)
	first := newBPMNCallbackOutboxForTest(client, &fakeBPMNCallbackExecutor{}, now)
	second := newBPMNCallbackOutboxForTest(client, &fakeBPMNCallbackExecutor{}, now)
	row := enqueueBPMNCallbackOutboxForTest(t, first, "callback-cas")

	claimedByFirst, err := first.claim(context.Background(), "worker-a", row)
	require.NoError(t, err)
	require.True(t, claimedByFirst)
	claimedBySecond, err := second.claim(context.Background(), "worker-b", row)
	require.NoError(t, err)
	require.False(t, claimedBySecond)

	saved := client.ProcessCallbackOutbox.Query().
		Where(processcallbackoutbox.ID(row.ID), processcallbackoutbox.TenantID(7)).
		OnlyX(context.Background())
	require.Equal(t, "processing", saved.Status)
	require.Equal(t, "worker-a", saved.LeaseOwner)
	require.Equal(t, 1, saved.AttemptCount)
}

func TestBPMNCallbackOutboxDoesNotClaimLiveLease(t *testing.T) {
	client := openBPMNCallbackOutboxClient(t)
	now := time.Date(2026, 8, 30, 12, 0, 0, 0, time.UTC)
	outbox := newBPMNCallbackOutboxForTest(client, &fakeBPMNCallbackExecutor{}, now)
	row := enqueueBPMNCallbackOutboxForTest(t, outbox, "callback-live-lease")
	_, err := client.ProcessCallbackOutbox.Update().
		Where(processcallbackoutbox.ID(row.ID), processcallbackoutbox.TenantID(7)).
		SetStatus("processing").SetLeaseOwner("worker-a").SetLeaseExpiresAt(now.Add(time.Minute)).Save(context.Background())
	require.NoError(t, err)

	claimed, err := outbox.claim(context.Background(), "worker-b", row)
	require.NoError(t, err)
	require.False(t, claimed)
}

func TestBPMNCallbackOutboxReclaimsExpiredLease(t *testing.T) {
	client := openBPMNCallbackOutboxClient(t)
	now := time.Date(2026, 8, 30, 12, 0, 0, 0, time.UTC)
	outbox := newBPMNCallbackOutboxForTest(client, &fakeBPMNCallbackExecutor{}, now)
	row := enqueueBPMNCallbackOutboxForTest(t, outbox, "callback-expired-lease")
	_, err := client.ProcessCallbackOutbox.Update().
		Where(processcallbackoutbox.ID(row.ID), processcallbackoutbox.TenantID(7)).
		SetStatus("processing").SetAttemptCount(1).SetLeaseOwner("worker-a").SetLeaseExpiresAt(now.Add(-time.Second)).Save(context.Background())
	require.NoError(t, err)

	claimed, err := outbox.claim(context.Background(), "worker-b", row)
	require.NoError(t, err)
	require.True(t, claimed)
	saved := client.ProcessCallbackOutbox.Query().
		Where(processcallbackoutbox.ID(row.ID), processcallbackoutbox.TenantID(7)).OnlyX(context.Background())
	require.Equal(t, "worker-b", saved.LeaseOwner)
	require.Equal(t, 2, saved.AttemptCount)
}

func TestBPMNCallbackOutboxFailureReturnsToPendingWithBackoff(t *testing.T) {
	client := openBPMNCallbackOutboxClient(t)
	now := time.Date(2026, 8, 30, 12, 0, 0, 0, time.UTC)
	executor := &fakeBPMNCallbackExecutor{err: newBPMNCallbackHandlerError(errors.New("tenant-7-secret-sql"))}
	outbox := newBPMNCallbackOutboxForTest(client, executor, now)
	row := enqueueBPMNCallbackOutboxForTest(t, outbox, "callback-retry")

	completed, err := outbox.processPending(context.Background(), "worker-a", 1)
	require.Zero(t, completed)
	require.Error(t, err)
	saved := client.ProcessCallbackOutbox.Query().
		Where(processcallbackoutbox.ID(row.ID), processcallbackoutbox.TenantID(7)).OnlyX(context.Background())
	require.Equal(t, "pending", saved.Status)
	require.Equal(t, 1, saved.AttemptCount)
	require.Equal(t, now.Add(time.Second), saved.NextAttemptAt)
	require.Equal(t, "handler_error", saved.LastErrorClass)
	require.NotContains(t, saved.LastErrorClass, "tenant-7-secret-sql")

	_, err = client.ProcessCallbackOutbox.Update().
		Where(processcallbackoutbox.ID(row.ID), processcallbackoutbox.TenantID(7)).
		SetAttemptCount(10).SetStatus("processing").SetLeaseOwner("worker-a").SetLeaseExpiresAt(now.Add(time.Minute)).Save(context.Background())
	require.NoError(t, err)
	saved = client.ProcessCallbackOutbox.Query().
		Where(processcallbackoutbox.ID(row.ID), processcallbackoutbox.TenantID(7)).OnlyX(context.Background())
	require.NoError(t, outbox.retry(context.Background(), "worker-a", saved, "handler_error"))
	saved = client.ProcessCallbackOutbox.Query().
		Where(processcallbackoutbox.ID(row.ID), processcallbackoutbox.TenantID(7)).OnlyX(context.Background())
	require.Equal(t, now.Add(300*time.Second), saved.NextAttemptAt)
	require.Equal(t, 300*time.Second, bpmnCallbackRetryDelay(1000))
}

func TestBPMNCallbackOutboxClassifiesExecutorErrors(t *testing.T) {
	const errorSentinel = "tenant-7-secret-sql"
	tests := []struct {
		name string
		err  error
		want string
	}{
		{
			name: "handler error",
			err:  newBPMNCallbackHandlerError(errors.New(errorSentinel)),
			want: "handler_error",
		},
		{
			name: "advance error",
			err:  newBPMNCallbackAdvanceError(errors.New(errorSentinel)),
			want: "advance_error",
		},
		{
			name: "untyped error",
			err:  errors.New(errorSentinel),
			want: "unknown_error",
		},
		{
			name: "invalid typed error",
			err:  newBPMNCallbackExecutionError("invalid_error_class"),
			want: "unknown_error",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			client := openBPMNCallbackOutboxClient(t)
			now := time.Date(2026, 8, 30, 12, 0, 0, 0, time.UTC)
			outbox := newBPMNCallbackOutboxForTest(client, &fakeBPMNCallbackExecutor{err: tt.err}, now)
			row := enqueueBPMNCallbackOutboxForTest(t, outbox, "callback-class-"+tt.name)

			completed, err := outbox.processPending(context.Background(), "worker-a", 1)
			require.Zero(t, completed)
			require.Error(t, err)
			saved := client.ProcessCallbackOutbox.Query().
				Where(processcallbackoutbox.ID(row.ID), processcallbackoutbox.TenantID(7)).OnlyX(context.Background())
			require.Equal(t, tt.want, saved.LastErrorClass)
			require.NotContains(t, saved.LastErrorClass, errorSentinel)
		})
	}
}

func TestBPMNCallbackOutboxSuccessRequiresMatchingLeaseOwner(t *testing.T) {
	client := openBPMNCallbackOutboxClient(t)
	now := time.Date(2026, 8, 30, 12, 0, 0, 0, time.UTC)
	outbox := newBPMNCallbackOutboxForTest(client, &fakeBPMNCallbackExecutor{}, now)
	row := enqueueBPMNCallbackOutboxForTest(t, outbox, "callback-owner")
	_, err := client.ProcessCallbackOutbox.Update().
		Where(processcallbackoutbox.ID(row.ID), processcallbackoutbox.TenantID(7)).
		SetStatus("processing").SetLeaseOwner("worker-a").SetLeaseExpiresAt(now.Add(time.Minute)).Save(context.Background())
	require.NoError(t, err)

	completed, err := outbox.complete(context.Background(), "worker-b", row)
	require.NoError(t, err)
	require.False(t, completed)
	saved := client.ProcessCallbackOutbox.Query().
		Where(processcallbackoutbox.ID(row.ID), processcallbackoutbox.TenantID(7)).OnlyX(context.Background())
	require.Equal(t, "processing", saved.Status)
	require.Equal(t, "worker-a", saved.LeaseOwner)
}

func TestBPMNCallbackOutboxProcessExecutionKeysReclaimsExpiredLease(t *testing.T) {
	client := openBPMNCallbackOutboxClient(t)
	now := time.Date(2026, 8, 30, 12, 0, 0, 0, time.UTC)
	outbox := newBPMNCallbackOutboxForTest(client, &fakeBPMNCallbackExecutor{}, now)
	row := enqueueBPMNCallbackOutboxForTest(t, outbox, "callback-targeted-expired-lease")
	_, err := client.ProcessCallbackOutbox.Update().
		Where(processcallbackoutbox.ID(row.ID), processcallbackoutbox.TenantID(7)).
		SetStatus("processing").SetLeaseOwner("worker-a").SetLeaseExpiresAt(now.Add(-time.Second)).Save(context.Background())
	require.NoError(t, err)

	ctx := context.WithValue(context.Background(), bpmn.BPMNTenantIDContextKey, 7)
	completed, err := outbox.processExecutionKeys(ctx, "worker-b", []string{row.ExecutionKey})
	require.NoError(t, err)
	require.Equal(t, 1, completed)
	saved := client.ProcessCallbackOutbox.Query().
		Where(processcallbackoutbox.ID(row.ID), processcallbackoutbox.TenantID(7)).OnlyX(context.Background())
	require.Equal(t, "completed", saved.Status)
	require.Equal(t, 1, saved.AttemptCount)
}

func TestBPMNCallbackOutboxProcessExecutionKeysRejectsBlankWorker(t *testing.T) {
	client := openBPMNCallbackOutboxClient(t)
	now := time.Date(2026, 8, 30, 12, 0, 0, 0, time.UTC)
	outbox := newBPMNCallbackOutboxForTest(client, &fakeBPMNCallbackExecutor{}, now)
	row := enqueueBPMNCallbackOutboxForTest(t, outbox, "callback-targeted-blank-worker")

	ctx := context.WithValue(context.Background(), bpmn.BPMNTenantIDContextKey, 7)
	completed, err := outbox.processExecutionKeys(ctx, "  ", []string{row.ExecutionKey})
	require.Zero(t, completed)
	require.Error(t, err)
	saved := client.ProcessCallbackOutbox.Query().
		Where(processcallbackoutbox.ID(row.ID), processcallbackoutbox.TenantID(7)).OnlyX(context.Background())
	require.Equal(t, "pending", saved.Status)
	require.Empty(t, saved.LeaseOwner)
	require.Zero(t, saved.AttemptCount)
}

func TestBPMNCallbackOutboxRejectsStaleOwnerTransitions(t *testing.T) {
	client := openBPMNCallbackOutboxClient(t)
	now := time.Date(2026, 8, 30, 12, 0, 0, 0, time.UTC)
	outbox := newBPMNCallbackOutboxForTest(client, &fakeBPMNCallbackExecutor{}, now)
	row := enqueueBPMNCallbackOutboxForTest(t, outbox, "callback-stale-owner")

	claimed, err := outbox.claim(context.Background(), "worker-a", row)
	require.NoError(t, err)
	require.True(t, claimed)
	outbox.now = func() time.Time { return now.Add(bpmnCallbackLeaseDuration + time.Second) }
	claimed, err = outbox.claim(context.Background(), "worker-b", row)
	require.NoError(t, err)
	require.True(t, claimed)

	completed, err := outbox.complete(context.Background(), "worker-a", row)
	require.NoError(t, err)
	require.False(t, completed)
	require.Error(t, outbox.retry(context.Background(), "worker-a", row, "handler_error"))
	saved := client.ProcessCallbackOutbox.Query().
		Where(processcallbackoutbox.ID(row.ID), processcallbackoutbox.TenantID(7)).OnlyX(context.Background())
	require.Equal(t, "processing", saved.Status)
	require.Equal(t, "worker-b", saved.LeaseOwner)

	completed, err = outbox.complete(context.Background(), "worker-b", row)
	require.NoError(t, err)
	require.True(t, completed)
	saved = client.ProcessCallbackOutbox.Query().
		Where(processcallbackoutbox.ID(row.ID), processcallbackoutbox.TenantID(7)).OnlyX(context.Background())
	require.Equal(t, "completed", saved.Status)
}

func TestBPMNCallbackExecutionKeyIsStableAcrossRetry(t *testing.T) {
	client := openBPMNCallbackOutboxClient(t)
	now := time.Date(2026, 8, 30, 12, 0, 0, 0, time.UTC)
	executor := &fakeBPMNCallbackExecutor{err: errors.New("first attempt fails")}
	outbox := newBPMNCallbackOutboxForTest(client, executor, now)
	enqueueBPMNCallbackOutboxForTest(t, outbox, "callback-stable-key")

	_, err := outbox.processPending(context.Background(), "worker-a", 1)
	require.Error(t, err)
	executor.err = nil
	outbox.now = func() time.Time { return now.Add(time.Second) }
	completed, err := outbox.processPending(context.Background(), "worker-b", 1)
	require.NoError(t, err)
	require.Equal(t, 1, completed)
	require.Equal(t, []string{"callback-stable-key", "callback-stable-key"}, executor.keys)
	require.Equal(t, "callback-stable-key", executor.vars[0]["bpmn_callback_execution_key"])
	require.Equal(t, "callback-stable-key", executor.vars[1]["bpmn_callback_execution_key"])
}

func TestBPMNCallbackOutboxVerifiesExecutorCommittedCompletion(t *testing.T) {
	client := openBPMNCallbackOutboxClient(t)
	now := time.Date(2026, 8, 30, 12, 0, 0, 0, time.UTC)
	executor := &fakeBPMNCallbackExecutor{completionCommitted: true}
	outbox := newBPMNCallbackOutboxForTest(client, executor, now)
	row := enqueueBPMNCallbackOutboxForTest(t, outbox, "callback-unpersisted-completion")

	completed, err := outbox.processPending(context.Background(), "worker-a", 1)
	require.Error(t, err)
	require.Zero(t, completed)
	saved := client.ProcessCallbackOutbox.Query().
		Where(processcallbackoutbox.ID(row.ID), processcallbackoutbox.TenantID(7)).
		OnlyX(context.Background())
	require.Equal(t, bpmnCallbackStatusPending, saved.Status)
	require.Equal(t, "lease_lost", saved.LastErrorClass)
}
