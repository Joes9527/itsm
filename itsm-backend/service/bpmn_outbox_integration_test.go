//go:build integration

package service

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"os"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"itsm-backend/dto"
	"itsm-backend/ent"
	"itsm-backend/ent/migrate"
	"itsm-backend/ent/processcallbackoutbox"
	"itsm-backend/ent/ticketcc"
	"itsm-backend/service/bpmn"

	entgo "entgo.io/ent"
	"entgo.io/ent/dialect"
	entsql "entgo.io/ent/dialect/sql"
	sqlschema "entgo.io/ent/dialect/sql/schema"
	"github.com/google/uuid"
	"github.com/stretchr/testify/require"
	"go.uber.org/zap"
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

type postgresOutboxSnapshot struct {
	ID                int
	ExecutionKey      string
	TenantID          int
	ProcessInstanceID int
	ProcessTaskID     int
	TaskID            string
	CallbackKind      string
	HandlerID         string
	TaskType          string
	ElementID         string
	Variables         map[string]interface{}
	Status            string
	AttemptCount      int
	NextAttemptAt     time.Time
	LeaseOwner        string
	LeaseExpiresAt    time.Time
	LastErrorClass    string
	CompletedAt       time.Time
	CreatedAt         time.Time
	UpdatedAt         time.Time
}

func snapshotPostgresOutbox(row *ent.ProcessCallbackOutbox) postgresOutboxSnapshot {
	variables := make(map[string]interface{}, len(row.Variables))
	for key, value := range row.Variables {
		variables[key] = value
	}
	return postgresOutboxSnapshot{
		ID: row.ID, ExecutionKey: row.ExecutionKey, TenantID: row.TenantID,
		ProcessInstanceID: row.ProcessInstanceID, ProcessTaskID: row.ProcessTaskID,
		TaskID: row.TaskID, CallbackKind: row.CallbackKind, HandlerID: row.HandlerID,
		TaskType: row.TaskType, ElementID: row.ElementID, Variables: variables,
		Status: row.Status, AttemptCount: row.AttemptCount, NextAttemptAt: row.NextAttemptAt,
		LeaseOwner: row.LeaseOwner, LeaseExpiresAt: row.LeaseExpiresAt,
		LastErrorClass: row.LastErrorClass, CompletedAt: row.CompletedAt,
		CreatedAt: row.CreatedAt, UpdatedAt: row.UpdatedAt,
	}
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

func TestTicketCCMigrationCompatibilityPostgres(t *testing.T) {
	dsn := os.Getenv("ITSM_TEST_DB")
	require.NotEmpty(t, dsn, "ITSM_TEST_DB is required for PostgreSQL integration tests")
	db, err := sql.Open("postgres", dsn)
	require.NoError(t, err)
	db.SetMaxOpenConns(1)
	db.SetMaxIdleConns(1)
	require.NoError(t, db.PingContext(context.Background()))

	schemaName := "ticket_cc_task3_" + strings.ReplaceAll(uuid.NewString(), "-", "_")
	_, err = db.ExecContext(context.Background(), fmt.Sprintf(`CREATE SCHEMA "%s"`, schemaName))
	require.NoError(t, err)
	var client *ent.Client
	t.Cleanup(func() {
		ctx, cancel := context.WithTimeout(context.Background(), postgresIntegrationTimeout)
		defer cancel()
		_, resetErr := db.ExecContext(ctx, "SET search_path TO public")
		require.NoError(t, resetErr)
		_, dropErr := db.ExecContext(ctx, fmt.Sprintf(`DROP SCHEMA "%s" CASCADE`, schemaName))
		require.NoError(t, dropErr)
		if client != nil {
			require.NoError(t, client.Close())
		} else {
			require.NoError(t, db.Close())
		}
	})
	_, err = db.ExecContext(context.Background(), fmt.Sprintf(`SET search_path TO "%s"`, schemaName))
	require.NoError(t, err)

	_, err = db.ExecContext(context.Background(), `CREATE TABLE tickets (id bigint PRIMARY KEY)`)
	require.NoError(t, err)
	_, err = db.ExecContext(context.Background(), `
		CREATE TABLE ticket_ccs (
			id bigint GENERATED BY DEFAULT AS IDENTITY PRIMARY KEY,
			user_id bigint NOT NULL,
			added_by bigint NOT NULL,
			tenant_id bigint NOT NULL,
			added_at timestamptz NOT NULL,
			is_active boolean NOT NULL DEFAULT true,
			ticket_id bigint NOT NULL REFERENCES tickets(id)
		)
	`)
	require.NoError(t, err)
	_, err = db.ExecContext(context.Background(), `INSERT INTO tickets (id) VALUES ($1)`, 41)
	require.NoError(t, err)
	for _, active := range []bool{false, false, false} {
		_, err = db.ExecContext(context.Background(), `
			INSERT INTO ticket_ccs (user_id, added_by, tenant_id, added_at, is_active, ticket_id)
			VALUES ($1, $2, $3, $4, $5, $6)
		`, 73, 11, 29, time.Date(2026, 8, 31, 12, 0, 0, 0, time.UTC), active, 41)
		require.NoError(t, err)
	}

	client = ent.NewClient(ent.Driver(entsql.OpenDB(dialect.Postgres, db)))
	migrationCtx, cancel := context.WithTimeout(context.Background(), postgresIntegrationTimeout)
	defer cancel()
	require.NoError(t, migrate.Create(
		migrationCtx,
		client.Schema,
		[]*sqlschema.Table{ticketCCMigrationTableWithoutForeignKeys()},
	))

	var rowCount, nullDeliveryKeys int
	require.NoError(t, db.QueryRowContext(context.Background(), "SELECT COUNT(*) FROM ticket_ccs").Scan(&rowCount))
	require.NoError(t, db.QueryRowContext(context.Background(), "SELECT COUNT(*) FROM ticket_ccs WHERE delivery_key IS NULL").Scan(&nullDeliveryKeys))
	require.Equal(t, 3, rowCount)
	require.Equal(t, rowCount, nullDeliveryKeys)

	_, err = db.ExecContext(context.Background(), `
		INSERT INTO ticket_ccs (user_id, added_by, tenant_id, added_at, is_active, ticket_id)
		VALUES ($1, $2, $3, $4, $5, $6)
	`, 73, 11, 29, time.Date(2026, 8, 31, 13, 0, 0, 0, time.UTC), true, 41)
	require.NoError(t, err, "inactive history must allow one active ordinary re-add")
	_, err = db.ExecContext(context.Background(), `
		INSERT INTO ticket_ccs (user_id, added_by, tenant_id, added_at, is_active, ticket_id)
		VALUES ($1, $2, $3, $4, $5, $6)
	`, 73, 12, 29, time.Date(2026, 8, 31, 14, 0, 0, 0, time.UTC), true, 41)
	require.Error(t, err, "a second active ordinary relation must be rejected")

	var firstID, secondID int
	require.NoError(t, db.QueryRowContext(context.Background(), "SELECT id FROM ticket_ccs ORDER BY id LIMIT 1").Scan(&firstID))
	require.NoError(t, db.QueryRowContext(context.Background(), "SELECT id FROM ticket_ccs ORDER BY id OFFSET 1 LIMIT 1").Scan(&secondID))
	_, err = db.ExecContext(context.Background(), "UPDATE ticket_ccs SET delivery_key = $1 WHERE id = $2", "stable-callback-delivery", firstID)
	require.NoError(t, err)
	_, err = db.ExecContext(context.Background(), "UPDATE ticket_ccs SET delivery_key = $1 WHERE id = $2", "stable-callback-delivery", secondID)
	require.Error(t, err, "same tenant, delivery key, and user must be unique")
}

func TestCCTicketConcurrentOrdinaryPostgresCommitsExactlyOneEffectSet(t *testing.T) {
	dsn := os.Getenv("ITSM_TEST_DB")
	require.NotEmpty(t, dsn, "ITSM_TEST_DB is required for PostgreSQL integration tests")
	ctx, cancel := context.WithTimeout(context.Background(), postgresIntegrationTimeout)
	defer cancel()

	schemaName := "ticket_cc_race_" + strings.ReplaceAll(uuid.NewString(), "-", "_")
	setupDB, setupClient := openPostgresEntClientInSchema(t, dsn, schemaName, true)
	require.NoError(t, setupClient.Schema.Create(ctx))
	namespace := strings.ReplaceAll(uuid.NewString(), "-", "")
	tenant, err := createTicketWorkflowTestTenant(ctx, setupClient, "cc-race-"+namespace)
	require.NoError(t, err)
	operator, err := createTicketWorkflowTestUser(ctx, setupClient, tenant.ID, "cc-race-operator-"+namespace)
	require.NoError(t, err)
	recipient, err := createTicketWorkflowTestUser(ctx, setupClient, tenant.ID, "cc-race-recipient-"+namespace)
	require.NoError(t, err)
	tk, err := createTicketWorkflowTestTicket(ctx, setupClient, tenant.ID, operator.ID, "open")
	require.NoError(t, err)

	_, clientOne := openPostgresEntClientInSchema(t, dsn, schemaName, false)
	_, clientTwo := openPostgresEntClientInSchema(t, dsn, schemaName, false)
	arrived := make(chan struct{}, 2)
	release := make(chan struct{})
	installTicketCCCreateBarrier(clientOne, arrived, release)
	installTicketCCCreateBarrier(clientTwo, arrived, release)

	services := []*TicketWorkflowService{
		NewTicketWorkflowService(clientOne, zap.NewNop().Sugar()),
		NewTicketWorkflowService(clientTwo, zap.NewNop().Sugar()),
	}
	results := make(chan error, len(services))
	for _, workflowService := range services {
		go func(svc *TicketWorkflowService) {
			results <- svc.CCTicket(context.Background(), &dto.CCTicketRequest{
				TicketID: tk.ID,
				CCUsers:  []int{recipient.ID},
			}, operator.ID, tenant.ID)
		}(workflowService)
	}
	for range services {
		select {
		case <-arrived:
		case <-time.After(5 * time.Second):
			t.Fatal("both ordinary CC transactions did not reach the create barrier")
		}
	}
	close(release)

	var successes, conflicts int
	for range services {
		select {
		case callErr := <-results:
			if callErr == nil {
				successes++
			} else {
				conflicts++
			}
		case <-time.After(5 * time.Second):
			t.Fatal("ordinary CC race did not complete")
		}
	}
	require.Equal(t, 1, successes)
	require.Equal(t, 1, conflicts)
	require.Equal(t, 1, setupClient.TicketCC.Query().Where(ticketcc.IsActive(true)).CountX(ctx))
	require.Equal(t, 1, setupClient.TicketNotification.Query().CountX(ctx))
	require.Equal(t, 1, setupClient.Notification.Query().CountX(ctx))
	require.Equal(t, 1, setupClient.TicketWorkflowRecord.Query().CountX(ctx))

	_ = setupDB
}

func TestTicketNotificationWorkerPostgresCASAndExpiredLeaseRecovery(t *testing.T) {
	dsn := os.Getenv("ITSM_TEST_DB")
	require.NotEmpty(t, dsn, "ITSM_TEST_DB is required for PostgreSQL integration tests")
	ctx, cancel := context.WithTimeout(context.Background(), postgresIntegrationTimeout)
	defer cancel()

	schemaName := "ticket_notification_worker_" + strings.ReplaceAll(uuid.NewString(), "-", "_")
	_, setupClient := openPostgresEntClientInSchema(t, dsn, schemaName, true)
	require.NoError(t, setupClient.Schema.Create(ctx))
	namespace := strings.ReplaceAll(uuid.NewString(), "-", "")
	tenant, err := createTicketWorkflowTestTenant(ctx, setupClient, "notification-worker-"+namespace)
	require.NoError(t, err)
	operator, err := createTicketWorkflowTestUser(ctx, setupClient, tenant.ID, "notification-worker-operator-"+namespace)
	require.NoError(t, err)
	recipient, err := createTicketWorkflowTestUser(ctx, setupClient, tenant.ID, "notification-worker-recipient-"+namespace)
	require.NoError(t, err)
	tk, err := createTicketWorkflowTestTicket(ctx, setupClient, tenant.ID, operator.ID, "open")
	require.NoError(t, err)
	workflow := NewTicketWorkflowService(setupClient, zap.NewNop().Sugar())
	require.NoError(t, workflow.CCTicket(ctx, &dto.CCTicketRequest{
		TicketID:       tk.ID,
		CCUsers:        []int{recipient.ID},
		NotifyChannels: []string{"webhook"},
	}, operator.ID, tenant.ID))
	row := setupClient.TicketNotification.Query().OnlyX(ctx)

	_, workerClientOne := openPostgresEntClientInSchema(t, dsn, schemaName, false)
	_, workerClientTwo := openPostgresEntClientInSchema(t, dsn, schemaName, false)
	workerOne := NewTicketNotificationService(workerClientOne, zap.NewNop().Sugar())
	workerTwo := NewTicketNotificationService(workerClientTwo, zap.NewNop().Sugar())
	release := make(chan struct{})
	fake := &durableNotificationConnector{entered: make(chan struct{}, 1), release: release}
	configureDurableNotificationConnector(t, workerOne, tenant.ID, fake)
	configureDurableNotificationConnector(t, workerTwo, tenant.ID, fake)
	now := time.Now().Add(time.Hour)
	workerOne.now = func() time.Time { return now }
	workerTwo.now = func() time.Time { return now }

	firstResult := make(chan error, 1)
	go func() {
		_, processErr := workerOne.ProcessPendingDeliveries(context.Background(), "postgres-notification-worker-one", 10)
		firstResult <- processErr
	}()
	select {
	case <-fake.entered:
	case <-time.After(5 * time.Second):
		t.Fatal("first PostgreSQL notification worker did not dispatch after claiming")
	}
	completed, err := workerTwo.ProcessPendingDeliveries(ctx, "postgres-notification-worker-two", 10)
	require.NoError(t, err)
	require.Zero(t, completed)
	close(release)
	require.NoError(t, <-firstResult)
	require.Len(t, fake.sentMessages(), 1)

	row = setupClient.TicketNotification.GetX(ctx, row.ID)
	require.Equal(t, "sent", row.Status)
	setupClient.TicketNotification.UpdateOneID(row.ID).
		SetStatus("processing").
		ClearSentAt().
		SetLeaseOwner("expired-postgres-worker").
		SetLeaseExpiresAt(now.Add(-time.Second)).
		ExecX(ctx)
	now = now.Add(2 * time.Minute)
	completed, err = workerTwo.ProcessPendingDeliveries(ctx, "postgres-notification-worker-recovery", 10)
	require.NoError(t, err)
	require.Equal(t, 1, completed)
	row = setupClient.TicketNotification.GetX(ctx, row.ID)
	require.Equal(t, "sent", row.Status)
	require.Equal(t, 2, row.AttemptCount)
	require.Len(t, fake.sentMessages(), 2)
	require.Equal(t, fake.sentMessages()[0].Metadata["delivery_key"], fake.sentMessages()[1].Metadata["delivery_key"])
}

func openPostgresEntClientInSchema(t *testing.T, dsn, schemaName string, createSchema bool) (*sql.DB, *ent.Client) {
	t.Helper()
	db, err := sql.Open("postgres", dsn)
	require.NoError(t, err)
	db.SetMaxOpenConns(1)
	db.SetMaxIdleConns(1)
	ctx := context.Background()
	require.NoError(t, db.PingContext(ctx))
	if createSchema {
		_, err = db.ExecContext(ctx, fmt.Sprintf(`CREATE SCHEMA "%s"`, schemaName))
		require.NoError(t, err)
	}
	_, err = db.ExecContext(ctx, fmt.Sprintf(`SET search_path TO "%s"`, schemaName))
	require.NoError(t, err)
	client := ent.NewClient(ent.Driver(entsql.OpenDB(dialect.Postgres, db)))
	t.Cleanup(func() {
		cleanupCtx, cancel := context.WithTimeout(context.Background(), postgresIntegrationTimeout)
		defer cancel()
		if createSchema {
			_, _ = db.ExecContext(cleanupCtx, `SET search_path TO public`)
			_, _ = db.ExecContext(cleanupCtx, fmt.Sprintf(`DROP SCHEMA IF EXISTS "%s" CASCADE`, schemaName))
		}
		_ = client.Close()
	})
	return db, client
}

func installTicketCCCreateBarrier(client *ent.Client, arrived chan<- struct{}, release <-chan struct{}) {
	client.Use(func(next entgo.Mutator) entgo.Mutator {
		return entgo.MutateFunc(func(ctx context.Context, mutation entgo.Mutation) (entgo.Value, error) {
			if _, ok := mutation.(*ent.TicketCCMutation); ok && mutation.Op().Is(entgo.OpCreate) {
				arrived <- struct{}{}
				select {
				case <-release:
				case <-ctx.Done():
					return nil, ctx.Err()
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
		SetVariables(map[string]interface{}{"fixture": stableKey}).
		SetStatus(bpmnCallbackStatusPending).
		SetNextAttemptAt(now).
		SetLastErrorClass("handler_error").
		Save(context.Background())
	require.NoError(t, err)
	otherRowBefore, err := setupClient.ProcessCallbackOutbox.Create().
		SetExecutionKey(otherKey).
		SetTenantID(otherTenant.ID).
		SetProcessInstanceID(otherTenant.ID).
		SetTaskID("task-" + namespace + "-other").
		SetCallbackKind("service_task").
		SetHandlerID("postgres-idempotent-receiver").
		SetTaskType("postgres_integration_callback").
		SetElementID("callback-" + namespace + "-other").
		SetVariables(map[string]interface{}{"fixture": otherKey}).
		SetStatus(bpmnCallbackStatusPending).
		SetAttemptCount(4).
		SetNextAttemptAt(now).
		SetLastErrorClass("unknown_error").
		Save(context.Background())
	require.NoError(t, err)
	targetBefore, err := setupClient.ProcessCallbackOutbox.Query().Where(
		processcallbackoutbox.ID(row.ID), processcallbackoutbox.TenantID(tenant.ID),
	).Only(context.Background())
	require.NoError(t, err)
	controlBefore, err := setupClient.ProcessCallbackOutbox.Query().Where(
		processcallbackoutbox.ID(otherRowBefore.ID), processcallbackoutbox.TenantID(otherTenant.ID),
	).Only(context.Background())
	require.NoError(t, err)
	targetBeforeSnapshot := snapshotPostgresOutbox(targetBefore)
	controlBeforeSnapshot := snapshotPostgresOutbox(controlBefore)
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
	require.Equal(t, stableKey, claimedRow.ExecutionKey)
	require.Equal(t, now.Add(bpmnCallbackLeaseDuration), claimedRow.LeaseExpiresAt)
	require.Equal(t, targetBeforeSnapshot.NextAttemptAt, claimedRow.NextAttemptAt)
	require.Equal(t, targetBeforeSnapshot.LastErrorClass, claimedRow.LastErrorClass)
	require.True(t, claimedRow.CompletedAt.IsZero())
	require.True(t, claimedRow.UpdatedAt.After(targetBeforeSnapshot.UpdatedAt))
	expectedClaimed := targetBeforeSnapshot
	expectedClaimed.Status = bpmnCallbackStatusProcessing
	expectedClaimed.AttemptCount = 1
	expectedClaimed.LeaseOwner = winner
	expectedClaimed.LeaseExpiresAt = now.Add(bpmnCallbackLeaseDuration)
	expectedClaimed.UpdatedAt = claimedRow.UpdatedAt
	require.Equal(t, expectedClaimed, snapshotPostgresOutbox(claimedRow))
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
	require.Equal(t, stableKey, completedRow.ExecutionKey)
	require.Equal(t, bpmnCallbackStatusCompleted, completedRow.Status)
	require.Equal(t, 2, completedRow.AttemptCount)
	require.False(t, completedRow.CompletedAt.IsZero())
	require.Equal(t, now, completedRow.CompletedAt)
	require.Empty(t, completedRow.LeaseOwner)
	require.True(t, completedRow.LeaseExpiresAt.IsZero())
	require.Empty(t, completedRow.LastErrorClass)
	require.Equal(t, targetBeforeSnapshot.NextAttemptAt, completedRow.NextAttemptAt)
	require.True(t, completedRow.UpdatedAt.After(claimedRow.UpdatedAt))
	expectedCompleted := targetBeforeSnapshot
	expectedCompleted.Status = bpmnCallbackStatusCompleted
	expectedCompleted.AttemptCount = 2
	expectedCompleted.LastErrorClass = ""
	expectedCompleted.CompletedAt = now
	expectedCompleted.UpdatedAt = completedRow.UpdatedAt
	require.Equal(t, expectedCompleted, snapshotPostgresOutbox(completedRow))
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
	require.Equal(t, controlBeforeSnapshot, snapshotPostgresOutbox(otherRow))
}
