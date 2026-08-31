//go:build integration

package service

import (
	"context"
	"errors"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"itsm-backend/ent"
	"itsm-backend/ent/migrate"
	"itsm-backend/ent/processcallbackoutbox"
	"itsm-backend/service/bpmn"

	entgo "entgo.io/ent"
	"github.com/google/uuid"
	"github.com/stretchr/testify/require"
)

type postgresIdempotentCallbackReceiver struct {
	mu         sync.Mutex
	deliveries []string
	workers    []string
	leaseOwner []string
	attempts   []int
	effects    map[string]int
}

func (r *postgresIdempotentCallbackReceiver) executeClaimedCallback(
	ctx context.Context,
	workerID string,
	row *ent.ProcessCallbackOutbox,
) (bpmnCallbackExecutionResult, error) {
	key, ok := bpmn.BPMNCallbackExecutionKey(ctx)
	if !ok {
		return bpmnCallbackExecutionResult{}, newBPMNCallbackExecutionError("unknown_error")
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	r.deliveries = append(r.deliveries, key)
	r.workers = append(r.workers, workerID)
	r.leaseOwner = append(r.leaseOwner, row.LeaseOwner)
	r.attempts = append(r.attempts, row.AttemptCount)
	if _, exists := r.effects[key]; !exists {
		r.effects[key] = 1
	}
	return bpmnCallbackExecutionResult{}, nil
}

func (r *postgresIdempotentCallbackReceiver) snapshot() ([]string, []string, []string, []int, int) {
	r.mu.Lock()
	defer r.mu.Unlock()
	deliveries := append([]string(nil), r.deliveries...)
	workers := append([]string(nil), r.workers...)
	leaseOwners := append([]string(nil), r.leaseOwner...)
	attempts := append([]int(nil), r.attempts...)
	effectCount := 0
	for _, count := range r.effects {
		effectCount += count
	}
	return deliveries, workers, leaseOwners, attempts, effectCount
}

type postgresOutboxClaimResult struct {
	worker    string
	completed int
	err       error
}

type postgresOutboxLoad struct {
	worker       string
	rowID        int
	tenantID     int
	executionKey string
	status       string
	attemptCount int
}

type postgresOutboxLoadBarrier struct {
	rowID   int
	arrived chan postgresOutboxLoad
	release chan struct{}
}

func (b *postgresOutboxLoadBarrier) interceptor(worker string) ent.Interceptor {
	var waitOnce sync.Once
	return ent.InterceptFunc(func(next ent.Querier) ent.Querier {
		return ent.QuerierFunc(func(ctx context.Context, query ent.Query) (ent.Value, error) {
			value, err := next.Query(ctx, query)
			if err != nil {
				return value, err
			}
			queryContext := entgo.QueryFromContext(ctx)
			if queryContext == nil || queryContext.Type != ent.TypeProcessCallbackOutbox || queryContext.Op != entgo.OpQueryAll {
				return value, nil
			}
			rows, ok := value.([]*ent.ProcessCallbackOutbox)
			if !ok {
				return value, nil
			}
			for _, row := range rows {
				if row.ID != b.rowID {
					continue
				}
				waitOnce.Do(func() {
					b.arrived <- postgresOutboxLoad{
						worker: worker, rowID: row.ID, tenantID: row.TenantID,
						executionKey: row.ExecutionKey, status: row.Status, attemptCount: row.AttemptCount,
					}
					select {
					case <-b.release:
					case <-ctx.Done():
					}
				})
				break
			}
			return value, nil
		})
	})
}

func failNextPostgresOutboxCompletion(client *ent.Client, failNext *atomic.Bool) {
	client.Use(func(next ent.Mutator) ent.Mutator {
		return ent.MutateFunc(func(ctx context.Context, mutation ent.Mutation) (ent.Value, error) {
			if outboxMutation, ok := mutation.(*ent.ProcessCallbackOutboxMutation); ok {
				if status, exists := outboxMutation.Status(); exists && status == bpmnCallbackStatusCompleted && failNext.CompareAndSwap(true, false) {
					return nil, errors.New("simulated process loss before outbox completion")
				}
			}
			return next.Mutate(ctx, mutation)
		})
	})
}

func TestBPMNCallbackOutboxLeaseRecoveryPostgres(t *testing.T) {
	setupClient, setupDB := openBPMNPostgresIntegrationClient(t)
	migrateBPMNPostgresIntegrationTables(t, setupClient, migrate.ProcessCallbackOutboxesTable)
	namespace := uuid.NewString()
	tenantIDs := make([]int, 0, 2)
	registerPostgresBPMNFixtureCleanup(t, setupDB, &tenantIDs)
	tenant := createPostgresIntegrationTenant(t, setupClient, namespace)
	tenantIDs = append(tenantIDs, tenant.ID)
	otherTenant := createPostgresIntegrationTenant(t, setupClient, namespace+"-other")
	tenantIDs = append(tenantIDs, otherTenant.ID)
	stableKey := "callback-" + namespace
	otherKey := stableKey + "-other"
	now := time.Date(2026, 8, 31, 12, 0, 0, 0, time.UTC)
	row, err := setupClient.ProcessCallbackOutbox.Create().
		SetExecutionKey(stableKey).
		SetTenantID(tenant.ID).
		SetProcessInstanceID(tenant.ID).
		SetTaskID("task-" + namespace).
		SetCallbackKind("service_task").
		SetHandlerID("postgres-idempotent-receiver").
		SetTaskType("postgres_integration_callback").
		SetElementID("callback-" + namespace).
		SetStatus(bpmnCallbackStatusPending).
		SetNextAttemptAt(now).
		Save(context.Background())
	require.NoError(t, err)
	_, err = setupClient.ProcessCallbackOutbox.Create().
		SetExecutionKey(otherKey).
		SetTenantID(otherTenant.ID).
		SetProcessInstanceID(otherTenant.ID).
		SetTaskID("task-" + namespace + "-other").
		SetCallbackKind("service_task").
		SetHandlerID("postgres-idempotent-receiver").
		SetTaskType("postgres_integration_callback").
		SetElementID("callback-" + namespace + "-other").
		SetStatus(bpmnCallbackStatusPending).
		SetNextAttemptAt(now).
		Save(context.Background())
	require.NoError(t, err)
	t.Cleanup(func() {
		ctx, cancel := context.WithTimeout(context.Background(), postgresIntegrationTimeout)
		defer cancel()
		for _, tenantID := range tenantIDs {
			_, cleanupErr := setupDB.ExecContext(
				ctx, "DELETE FROM process_callback_outboxes WHERE tenant_id = $1", tenantID,
			)
			require.NoError(t, cleanupErr)
		}
	})

	clientA, _ := openBPMNPostgresIntegrationClient(t)
	clientB, _ := openBPMNPostgresIntegrationClient(t)
	receiver := &postgresIdempotentCallbackReceiver{effects: make(map[string]int)}
	workerIDs := [2]string{"outbox-worker-a-" + namespace, "outbox-worker-b-" + namespace}
	workers := [2]*bpmnCallbackOutbox{
		{client: clientA, executor: receiver, now: func() time.Time { return now }},
		{client: clientB, executor: receiver, now: func() time.Time { return now }},
	}
	barrier := &postgresOutboxLoadBarrier{
		rowID: row.ID, arrived: make(chan postgresOutboxLoad, 2), release: make(chan struct{}),
	}
	clientA.ProcessCallbackOutbox.Intercept(barrier.interceptor(workerIDs[0]))
	clientB.ProcessCallbackOutbox.Intercept(barrier.interceptor(workerIDs[1]))
	var failCompletion atomic.Bool
	failCompletion.Store(true)
	failNextPostgresOutboxCompletion(clientA, &failCompletion)
	failNextPostgresOutboxCompletion(clientB, &failCompletion)

	start := make(chan struct{})
	claimResults := make(chan postgresOutboxClaimResult, 2)
	for i := range workers {
		go func(worker *bpmnCallbackOutbox, workerID string) {
			<-start
			workerCtx := context.WithValue(context.Background(), bpmn.BPMNTenantIDContextKey, tenant.ID)
			completed, claimErr := worker.processExecutionKeys(workerCtx, workerID, []string{stableKey})
			claimResults <- postgresOutboxClaimResult{worker: workerID, completed: completed, err: claimErr}
		}(workers[i], workerIDs[i])
	}
	close(start)
	released := false
	defer func() {
		if !released {
			close(barrier.release)
		}
	}()
	loads := make([]postgresOutboxLoad, 0, 2)
	for len(loads) < 2 {
		select {
		case load := <-barrier.arrived:
			loads = append(loads, load)
		case result := <-claimResults:
			require.NoError(t, result.err, "worker returned before both workers observed the due row")
			require.FailNow(t, "worker returned before PostgreSQL outbox barrier")
		case <-time.After(postgresIntegrationTimeout):
			require.FailNow(t, "timed out waiting for both PostgreSQL outbox workers")
		}
	}
	for _, load := range loads {
		require.Equal(t, row.ID, load.rowID)
		require.Equal(t, tenant.ID, load.tenantID)
		require.Equal(t, stableKey, load.executionKey)
		require.Equal(t, bpmnCallbackStatusPending, load.status)
		require.Zero(t, load.attemptCount)
	}
	require.ElementsMatch(t, workerIDs[:], []string{loads[0].worker, loads[1].worker})
	close(barrier.release)
	released = true

	results := make([]postgresOutboxClaimResult, 0, 2)
	claimTimeout := time.NewTimer(postgresIntegrationTimeout)
	defer claimTimeout.Stop()
	for len(results) < 2 {
		select {
		case result := <-claimResults:
			results = append(results, result)
		case <-claimTimeout.C:
			require.FailNow(t, "timed out waiting for PostgreSQL outbox claim results")
		}
	}
	claimedRow, err := setupClient.ProcessCallbackOutbox.Query().Where(
		processcallbackoutbox.ID(row.ID), processcallbackoutbox.TenantID(tenant.ID),
	).Only(context.Background())
	require.NoError(t, err)
	require.Equal(t, bpmnCallbackStatusProcessing, claimedRow.Status)
	winner := claimedRow.LeaseOwner
	require.Contains(t, workerIDs, winner)
	loser := workerIDs[0]
	if winner == loser {
		loser = workerIDs[1]
	}
	require.Equal(t, 1, claimedRow.AttemptCount)
	require.False(t, failCompletion.Load(), "the lease holder did not reach the completion boundary")
	for _, result := range results {
		require.Zero(t, result.completed)
		if result.worker == winner {
			require.Error(t, result.err, "the simulated lease-holder loss did not interrupt completion")
		} else {
			require.NoError(t, result.err)
		}
	}
	deliveries, deliveryWorkers, leaseOwners, attempts, effectCount := receiver.snapshot()
	require.Equal(t, []string{stableKey}, deliveries)
	require.Equal(t, []string{winner}, deliveryWorkers)
	require.Equal(t, []string{winner}, leaseOwners)
	require.Equal(t, []int{1}, attempts)
	require.Equal(t, 1, effectCount)

	now = now.Add(bpmnCallbackLeaseDuration + time.Second)
	recoveryIndex := 0
	if workerIDs[1] == loser {
		recoveryIndex = 1
	}
	recoveryCtx := context.WithValue(context.Background(), bpmn.BPMNTenantIDContextKey, tenant.ID)
	completed, err := workers[recoveryIndex].processExecutionKeys(recoveryCtx, loser, []string{stableKey})
	require.NoError(t, err)
	require.Equal(t, 1, completed)

	completedRow, err := setupClient.ProcessCallbackOutbox.Query().Where(
		processcallbackoutbox.ID(row.ID), processcallbackoutbox.TenantID(tenant.ID),
	).Only(context.Background())
	require.NoError(t, err)
	require.Equal(t, bpmnCallbackStatusCompleted, completedRow.Status)
	require.Equal(t, 2, completedRow.AttemptCount)
	require.Empty(t, completedRow.LeaseOwner)
	deliveries, deliveryWorkers, leaseOwners, attempts, effectCount = receiver.snapshot()
	require.Equal(t, []string{stableKey, stableKey}, deliveries)
	require.Equal(t, []string{winner, loser}, deliveryWorkers)
	require.Equal(t, []string{winner, loser}, leaseOwners)
	require.Equal(t, []int{1, 2}, attempts)
	require.Equal(t, 1, effectCount)

	otherRow, err := setupClient.ProcessCallbackOutbox.Query().Where(
		processcallbackoutbox.TenantID(otherTenant.ID), processcallbackoutbox.ExecutionKey(otherKey),
	).Only(context.Background())
	require.NoError(t, err)
	require.Equal(t, bpmnCallbackStatusPending, otherRow.Status)
	require.Zero(t, otherRow.AttemptCount)
	require.Empty(t, otherRow.LeaseOwner)
}
