# BPMN Durable Callback Outbox Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Make BPMN callback delivery crash-recoverable, keep automatic counter-sign creation inside the owning transaction, and prove claim/outbox compare-and-set behavior against real PostgreSQL concurrency.

**Architecture:** Persist every synchronous BPMN handler invocation as a tenant-scoped outbox row in the same transaction as the task/token state that schedules it. A lease-based worker executes handlers with a stable idempotency key, then advances the BPMN token and completes the outbox row in one transaction; expired leases and failures are retried with bounded backoff. Transaction-bound engine clones rebind their task service and use client-bound counter-sign helpers so no nested transaction can observe uncommitted parents.

**Tech Stack:** Go 1.25.12, Gin, Ent, PostgreSQL, SQLite unit tests, `lib/pq`, Testify, Zap.

**Spec:** `docs/superpowers/specs/2026-08-30-architecture-assessment-remediation-execution-plan-design.md`

## Global Constraints

- `itsm-backend` remains the source of truth for workflow execution, tenant isolation, side effects, and audit.
- Every outbox row, claim, retry, handler execution context, token advance, query, and index is tenant-aware and fails closed when tenant identity is missing or inconsistent.
- Outbox delivery is at-least-once, not exactly-once; every execution carries one stable opaque execution key, and external handlers must propagate it as their idempotency key.
- A completed BPMN task is never changed back to active merely to retry a callback.
- Database state that schedules a callback and the outbox row are committed together; handler calls never run inside that database transaction.
- Handler success and BPMN token advancement use the same stable execution key. A crash after the external effect may repeat delivery, so receivers and built-in external adapters must deduplicate by that key.
- Callback failure after task commit is observable and retryable but does not turn the already-committed HTTP task completion into an error that invites resubmission.
- Ordinary synchronous ServiceTask callbacks use the outbox. Existing `AsyncServiceTaskHandler` implementations, including KAF delegation, retain their dedicated pause/resume contract and are not double-enqueued.
- Transaction-bound code must not open nested Ent transactions. Public mutation methods may own a transaction; private `WithClient` helpers only use the supplied client.
- Do not add a second workflow engine, generic job framework, compatibility callback path, dual write, or in-memory fallback.
- Do not merge assumptions or commits from `/home/administrator/project/itsm/.worktrees/kaf-delegation-transactional-delivery`; a later KAF rebase requires fresh authorization and concurrency review.
- Generated Ent files are committed after `go generate ./ent`; manual edits to generated files are forbidden.

---

### Task 1: Tenant-Scoped Durable Callback Record

**Files:**
- Create: `itsm-backend/ent/schema/process_callback_outbox.go`
- Modify: generated files under `itsm-backend/ent/`
- Create: `itsm-backend/service/bpmn_callback_outbox_schema_test.go`

**Interfaces:**
- Produces: Ent entity `ProcessCallbackOutbox` with callback kind, stable execution key, handler identity, payload, lease, retry, tenant, process, task, and lifecycle fields.
- Produces: constants in the later service task for statuses `pending`, `processing`, and `completed`, and kinds `service_task` and `user_task_callback`.
- Consumes: existing `ProcessInstance` and optional `ProcessTask` integer identities without adding an Ent edge that permits unscoped traversal.

- [ ] **Step 1: Write the failing schema contract test**

Create a SQLite Ent test that runs `client.Schema.Create`, inserts one row with all required fields, and asserts:

```go
func TestProcessCallbackOutboxSchemaEnforcesExecutionKeyAndTenant(t *testing.T) {
	client := openBPMNSchemaTestClient(t)
	ctx := context.Background()

	row := client.ProcessCallbackOutbox.Create().
		SetExecutionKey("bpmn-callback-00000001").
		SetTenantID(7).
		SetProcessInstanceID(101).
		SetProcessTaskID(202).
		SetCallbackKind("service_task").
		SetHandlerID("webhook_handler").
		SetTaskType("webhook").
		SetElementID("Activity_Notify").
		SetVariables(map[string]interface{}{"ticket_id": 42}).
		SetStatus("pending").
		SaveX(ctx)
	require.Equal(t, 7, row.TenantID)
	require.Equal(t, 0, row.AttemptCount)
	require.False(t, row.NextAttemptAt.IsZero())

	_, err := client.ProcessCallbackOutbox.Create().
		SetExecutionKey(row.ExecutionKey).
		SetTenantID(7).
		SetProcessInstanceID(101).
		SetCallbackKind("service_task").
		SetHandlerID("webhook_handler").
		SetTaskType("webhook").
		SetElementID("Activity_Notify").
		SetStatus("pending").
		Save(ctx)
	require.Error(t, err, "execution_key must be globally unique and opaque")
}
```

- [ ] **Step 2: Run the test to verify the entity is absent**

Run: `cd itsm-backend && go test ./service -run TestProcessCallbackOutboxSchemaEnforcesExecutionKeyAndTenant -count=1 -v`

Expected: FAIL because `ProcessCallbackOutbox` has not been generated.

- [ ] **Step 3: Define the Ent schema**

Implement these exact fields:

```text
execution_key        string, unique, not empty
tenant_id            positive int
process_instance_id  positive int
process_task_id       optional positive int
task_id               optional string
callback_kind         non-empty string
handler_id            non-empty string
task_type             non-empty string
element_id            non-empty string
variables             optional JSON map[string]interface{}
status                string default "pending"
attempt_count         non-negative int default 0
next_attempt_at       time default time.Now
lease_owner           optional string
lease_expires_at      optional time
last_error_class      optional string, max length 128
completed_at          optional time
created_at            time default time.Now, immutable
updated_at            time default/update-default time.Now
```

Add indexes for `(tenant_id, status, next_attempt_at)`, `(status, lease_expires_at)`, `(process_instance_id, status)`, `process_task_id`, and the unique `execution_key`. Do not store raw error text, parsed BPMN objects, handler pointers, secrets, or actor tokens.

- [ ] **Step 4: Generate Ent code and run schema tests**

Run: `cd itsm-backend && go generate ./ent`

Run: `cd itsm-backend && go test ./service -run TestProcessCallbackOutboxSchemaEnforcesExecutionKeyAndTenant -count=1 -v`

Expected: PASS.

- [ ] **Step 5: Verify generated schema integration**

Run: `cd itsm-backend && go test ./ent/... ./migration/... -count=1`

Run: `cd itsm-backend && git diff --check`

Expected: PASS with no handwritten changes inside generated files.

- [ ] **Step 6: Commit**

```bash
git add itsm-backend/ent itsm-backend/service/bpmn_callback_outbox_schema_test.go
git commit -m "feat(bpmn): add durable callback outbox schema"
```

### Task 2: Lease, Retry, and Stable Execution Context

**Files:**
- Create: `itsm-backend/service/bpmn_callback_outbox.go`
- Create: `itsm-backend/service/bpmn_callback_outbox_test.go`
- Modify: `itsm-backend/service/bpmn/handler_base.go`
- Modify: `itsm-backend/service/bpmn/webhook_handler.go`
- Test: `itsm-backend/service/bpmn/webhook_handler_test.go`

**Interfaces:**
- Produces: `BPMNCallbackExecutionKey(ctx context.Context) (string, bool)` in package `service/bpmn` using a private typed context key.
- Produces: `bpmnCallbackOutbox.enqueue(ctx, client, request) (*ent.ProcessCallbackOutbox, error)`.
- Produces: `bpmnCallbackOutbox.processPending(ctx, workerID string, limit int) (int, error)` and `processExecutionKeys(ctx, workerID string, keys []string)`.
- Consumes: a private engine callback `executeClaimedCallback(context.Context, *ent.ProcessCallbackOutbox) error`, implemented in Task 3.

- [ ] **Step 1: Write failing lease and retry tests**

Add named tests with an injected clock and deterministic backoff:

```text
TestBPMNCallbackOutboxClaimUsesCAS
TestBPMNCallbackOutboxDoesNotClaimLiveLease
TestBPMNCallbackOutboxReclaimsExpiredLease
TestBPMNCallbackOutboxFailureReturnsToPendingWithBackoff
TestBPMNCallbackOutboxSuccessRequiresMatchingLeaseOwner
TestBPMNCallbackExecutionKeyIsStableAcrossRetry
```

Use two service instances with different worker IDs against the same Ent client. Assert exactly one owner transitions a row from `pending` to `processing`; a non-owner cannot complete or reschedule it; an expired `processing` row becomes claimable; attempts increment once per claim; and `last_error_class` contains only a sanitized class such as `handler_error`, never the injected sensitive message.

- [ ] **Step 2: Run tests to verify the service is absent**

Run: `cd itsm-backend && go test ./service -run 'TestBPMNCallbackOutbox|TestBPMNCallbackExecutionKey' -count=1 -v`

Expected: FAIL because the outbox service and context accessor do not exist.

- [ ] **Step 3: Implement enqueue, CAS claim, lease ownership, and retry**

Use these concrete contracts:

```go
type bpmnCallbackEnqueueRequest struct {
	ExecutionKey       string
	TenantID           int
	ProcessInstanceID  int
	ProcessTaskID      int
	TaskID             string
	CallbackKind       string
	HandlerID          string
	TaskType           string
	ElementID          string
	Variables          map[string]interface{}
}

type bpmnCallbackExecutor interface {
	executeClaimedCallback(context.Context, *ent.ProcessCallbackOutbox) error
}
```

Generate `ExecutionKey` with `github.com/google/uuid` when enqueue receives an empty key. Claim rows ordered by `next_attempt_at, id`; use an Ent conditional update requiring either `pending` with `next_attempt_at <= now` or `processing` with an expired lease. Set a 60-second lease, increment `attempt_count`, and require exactly one affected row. Retry delays are `min(2^(attempt-1), 300)` seconds. Store only one of these error classes: `handler_error`, `advance_error`, `lease_lost`, or `unknown_error`.

`processPending` must continue after one row fails, return the number successfully completed, and aggregate only sanitized operational errors. It must not query across tenant through a request-scoped context; the worker is an explicit system operation and every claimed row carries its authoritative tenant.

- [ ] **Step 4: Add the stable execution key handler contract**

Add a private typed context key and accessor in `service/bpmn/handler_base.go`. The outbox processor injects the row's `execution_key` before every handler call. Also add `variables["bpmn_callback_execution_key"]` from the persisted value, overriding any client-supplied value.

Update `WebhookHandler` to set HTTP header `Idempotency-Key` from `BPMNCallbackExecutionKey(ctx)` and reject an outbox-driven execution if the key is unexpectedly absent. Preserve direct non-outbox validation calls that do not execute a side effect.

- [ ] **Step 5: Test webhook propagation and sensitive error redaction**

Add an `httptest.Server` assertion that two retries carry the same non-empty `Idempotency-Key`, and that neither persisted `last_error_class` nor logs contain a response-body sentinel such as `tenant-7-secret-sql`.

Run: `cd itsm-backend && go test ./service/bpmn ./service -run 'TestBPMNCallback|TestWebhook.*Idempotency' -count=1 -v`

Expected: PASS.

- [ ] **Step 6: Commit**

```bash
git add itsm-backend/service/bpmn_callback_outbox.go itsm-backend/service/bpmn_callback_outbox_test.go itsm-backend/service/bpmn/handler_base.go itsm-backend/service/bpmn/webhook_handler.go itsm-backend/service/bpmn/webhook_handler_test.go
git commit -m "feat(bpmn): process callback outbox with leases"
```

### Task 3: Replace In-Memory Callbacks with Durable Delivery

**Files:**
- Modify: `itsm-backend/service/bpmn_process_engine.go`
- Modify: `itsm-backend/service/bpmn_callback_outbox.go`
- Modify: `itsm-backend/service/bpmn_final_fix_test.go`
- Create: `itsm-backend/service/bpmn_callback_recovery_test.go`
- Modify: `itsm-backend/internal/bootstrap/app.go`
- Test: `itsm-backend/internal/bootstrap/bpmn_callback_worker_test.go`

**Interfaces:**
- Produces: `(*CustomProcessEngine).RunCallbackOutboxWorker(ctx context.Context, workerID string, interval time.Duration)`.
- Produces: `(*CustomProcessEngine).ProcessPendingCallbacks(ctx context.Context, workerID string, limit int) (int, error)` for deterministic tests and operational invocation.
- Consumes: Task 2 outbox enqueue, lease, retry, and execution-key contracts.
- Removes: `deferredProcessCallback`, `runDeferredProcessCallbacks`, `advanceAfterDeferredCallback`, and warning-only `dispatchUserTaskCallback` side effects.

- [ ] **Step 1: Write failing crash and recovery tests**

Add these named tests using a counting idempotent fake handler and injected transaction hooks:

```text
TestCompleteTaskCommitsCallbackOutboxAtomically
TestCompleteTaskAuditFailureLeavesNoCallbackOutbox
TestCompleteTaskReturnsSuccessWhenInlineCallbackAttemptFails
TestCallbackWorkerRetriesAfterHandlerFailureWithSameExecutionKey
TestCallbackWorkerRecoversExpiredLeaseAfterSimulatedCrash
TestCallbackHandlerSuccessThenAdvanceFailureRetriesAndCompletesToken
TestCallbackCompletionAndTokenAdvanceRollbackTogether
TestUserTaskCallbackFailureRemainsDurablyRetryable
TestAsyncKafHandlerIsNotEnqueuedAsSynchronousCallback
```

For the handler-success/advance-failure case, make the fake handler deduplicate by execution key, fail the first token-advance transaction through an Ent hook, then assert: the external effect count remains one, the outbox returns to `pending`, the task remains completed, the process has not advanced, a retry completes the same outbox row, and the token advances exactly once.

- [ ] **Step 2: Run tests to demonstrate the current in-memory loss**

Run: `cd itsm-backend && go test ./service -run 'TestCompleteTask.*CallbackOutbox|TestCallbackWorker|TestCallbackHandlerSuccess|TestUserTaskCallbackFailure|TestAsyncKafHandlerIsNotEnqueued' -count=1 -v`

Expected: FAIL because callbacks are still held in an in-memory slice and have no recovery worker.

- [ ] **Step 3: Enqueue service and user-task callbacks in the owning transaction**

Change transactional `handleElement` behavior for a registered synchronous ServiceTask:

```text
1. Resolve handler by registered handler ID/task type.
2. Persist one service_task outbox row using the current transaction client.
3. Leave CurrentActivityID at that ServiceTask.
4. Return without calling the handler or advancing the outgoing sequence flow.
```

When `CompleteTask` sees a completed UserTask with `service_task_type`, persist one `user_task_callback` row before the task transaction commits. Use the completed ProcessTask identity and the sanitized callback-variable snapshot. Do not call `dispatchUserTaskCallback` after commit.

Collect newly created execution keys during the transaction. After commit, make one best-effort `processExecutionKeys` call for low latency. Log only execution key, tenant ID, callback kind, attempt count, and sanitized class. Whether that attempt succeeds or fails, return success for the already committed task completion; the worker owns recovery.

- [ ] **Step 4: Execute a claimed callback and advance the token atomically**

For `user_task_callback`, tenant-scope load the completed ProcessTask, execute the handler with that task and stable key, then mark the outbox completed using its lease owner.

For `service_task`, reload the tenant-scoped ProcessInstance and ProcessDefinition after the handler succeeds, parse the current process, and in one Ent transaction:

```text
- verify the outbox row is still processing under the same owner;
- verify CurrentActivityID still equals outbox.element_id;
- call executeStep with the transaction-bound engine;
- mark the outbox completed and clear the lease;
- enqueue any downstream synchronous callbacks through the same transaction client;
- commit last.
```

If the handler or advance fails, reschedule the same row with its original execution key. A duplicate worker delivery after the external effect relies on the stable idempotency key; never generate a replacement key.

- [ ] **Step 5: Start one worker from application bootstrap**

Store the concrete process engine on `Application` through a narrow interface exposing `RunCallbackOutboxWorker`. In `startBackgroundTasks`, start one loop with a unique process worker ID, an immediate first sweep, a 2-second ticker, and batches of 50. The loop must honor context cancellation in tests; do not use an unbounded goroutine with no stop path.

Add a bootstrap test with a cancellable context proving startup performs an immediate sweep and shutdown exits without leaking a goroutine. Do not start a second worker from `internal/container` unless that container owns a long-lived server lifecycle.

- [ ] **Step 6: Run callback, KAF, and bootstrap tests**

Run: `cd itsm-backend && go test ./service ./internal/bootstrap -run 'TestCompleteTask.*Callback|TestCallback|TestUserTaskCallback|TestAsyncKaf|TestApplication.*CallbackWorker' -count=1 -v`

Run: `cd itsm-backend && go test -race ./service ./internal/bootstrap -run 'TestCallback|TestCompleteTask.*Callback' -count=1`

Expected: PASS with no raw callback error in logs and no in-memory callback queue remaining.

- [ ] **Step 7: Commit**

```bash
git add itsm-backend/service/bpmn_process_engine.go itsm-backend/service/bpmn_callback_outbox.go itsm-backend/service/bpmn_final_fix_test.go itsm-backend/service/bpmn_callback_recovery_test.go itsm-backend/internal/bootstrap/app.go itsm-backend/internal/bootstrap/bpmn_callback_worker_test.go
git commit -m "fix(bpmn): recover callback delivery durably"
```

### Task 4: Transaction-Bound Automatic Counter-Sign Creation

**Files:**
- Modify: `itsm-backend/service/bpmn_process_engine.go`
- Create: `itsm-backend/service/bpmn_counter_sign_transaction_test.go`

**Interfaces:**
- Produces: `createCounterSignTasksWithClient(ctx context.Context, client *ent.Client, parentTask *ent.ProcessTask, req *CounterSignRequest, actorID int) ([]*ent.ProcessTask, error)`.
- Consumes: the supplied transaction client and transaction-bound audit service; it never calls `client.Tx`.
- Preserves: public `CreateCounterSignTasks` signature and behavior by wrapping the private helper in one top-level transaction.

- [ ] **Step 1: Write failing transaction-bound progression tests**

Create BPMN definitions where completing the current task enters an approval UserTask configured for parallel and serial counter-sign. Add:

```text
TestCompleteTaskCreatesParallelCounterSignInsideOwningTransaction
TestCompleteTaskCreatesSerialCounterSignInsideOwningTransaction
TestCounterSignCreationFailureRollsBackSourceCompletionAndChildren
TestTransactionEngineRebindsTaskServiceToTransactionClient
```

Assert the source task completion, instance variable/version update, new parent, all children, parent status, first serial child activation, and `counter_sign_created` audit either all commit or all remain absent/unchanged. Inject `ProcessAuditLog.Create` failure after child creation to prove rollback.

- [ ] **Step 2: Run tests to reproduce the nested transaction defect**

Run: `cd itsm-backend && go test ./service -run 'TestCompleteTaskCreates.*CounterSign|TestCounterSignCreationFailure|TestTransactionEngineRebindsTaskService' -count=1 -v`

Expected: FAIL because `forClient` retains the root-client task service and `createCounterSignTasks` opens a nested transaction.

- [ ] **Step 3: Split public transaction ownership from private writes**

Refactor `CreateCounterSignTasks` to:

```go
tx, err := s.client.Tx(ctx)
// load and authorize the tenant-scoped parent through tx.Client()
// call createCounterSignTasksWithClient(ctx, tx.Client(), parent, req, actorID)
// commit last
```

Move all parent/child/audit writes into `createCounterSignTasksWithClient`. In `createUserTask`, call the private helper with `e.client`, which is already the owning transaction client during completion/token advancement.

Update `forClient` so the clone receives a new `bpmnTaskService{client: client, engine: clone}` and all resolver/audit dependencies use the same supplied client. Add an invariant assertion in tests that the clone's task service client is the transaction client.

- [ ] **Step 4: Run counter-sign and Vote regression suites**

Run: `cd itsm-backend && go test ./service -run 'TestCompleteTaskCreates.*CounterSign|TestCounterSign|TestVote|TestTaskMutationAuditRollback' -count=1 -v`

Run: `cd itsm-backend && go test -race ./service -run 'TestCounterSign|TestVote' -count=1`

Expected: PASS without SQLite lock errors or nested transaction creation.

- [ ] **Step 5: Commit**

```bash
git add itsm-backend/service/bpmn_process_engine.go itsm-backend/service/bpmn_counter_sign_transaction_test.go
git commit -m "fix(bpmn): bind counter-sign writes to transaction"
```

### Task 5: Real PostgreSQL CAS Evidence and Release Gate

**Files:**
- Create: `itsm-backend/service/bpmn_claim_cas_integration_test.go`
- Create: `itsm-backend/service/bpmn_outbox_integration_test.go`
- Modify: `itsm-backend/service/bpmn_final_fix_test.go`
- Modify: `docs/DEVELOPMENT_GUIDE.md`

**Interfaces:**
- Produces: build-tagged integration tests using `ITSM_TEST_DB` and independent PostgreSQL connections.
- Consumes: production Ent schema, claim CAS, outbox lease CAS, retry, tenant scope, and stable execution-key behavior.

- [ ] **Step 1: Replace the misleading single-connection claim race test**

Keep a fast SQLite conflict test but rename it to `TestClaimTaskSecondClaimReturnsConflict`. Remove language claiming it proves simultaneous CAS competition.

Create `bpmn_claim_cas_integration_test.go` with `//go:build integration`. Use `ITSM_TEST_DB`, a unique tenant/fixture namespace, two independent Ent clients, and an Ent query hook/barrier so both claim transactions load the same unassigned task before either conditional update proceeds. Release both goroutines together and assert:

```text
exactly one ClaimTask call succeeds;
exactly one returns common.ErrCodeConflict;
the winner is the persisted assignee;
exactly one task_claimed ProcessAuditLog exists;
no cross-tenant row changed.
```

- [ ] **Step 2: Add a real PostgreSQL outbox lease/recovery race**

Create `bpmn_outbox_integration_test.go` under the same build tag. Two independent worker clients race to claim one due row. Assert one handler invocation for the initial claim, then simulate lease-holder process loss, advance the test clock/lease expiry, and assert the other worker reclaims the same execution key. The idempotent fake receiver must observe one business effect despite two deliveries.

- [ ] **Step 3: Run real PostgreSQL integration tests**

Run:

```bash
cd itsm-backend
ITSM_TEST_DB='host=127.0.0.1 port=5432 user=itsm dbname=itsm sslmode=disable password=itsm_password_2026' \
  go test -tags integration ./service -run 'TestClaimTaskConcurrentCASPostgres|TestBPMNCallbackOutboxLeaseRecoveryPostgres' -count=1 -v
```

Expected: PASS. If the shared development database is unavailable, start the repository PostgreSQL service and rerun; do not replace this gate with SQLite or mark the task complete without real PostgreSQL evidence.

- [ ] **Step 4: Document operation and recovery**

Add a BPMN callback outbox section to `docs/DEVELOPMENT_GUIDE.md` covering:

```text
- at-least-once semantics and the Idempotency-Key contract;
- pending/processing/completed states and 60-second lease recovery;
- 2-second worker sweep, batch 50, and capped 5-minute backoff;
- safe diagnostic queries filtered by tenant and execution_key;
- prohibition on logging variables, raw handler errors, secrets, or callback bodies;
- the exact unit, race, integration, and build commands in this plan;
- requirement to rerun authorization/KAF tests after rebasing the separate KAF branch.
```

- [ ] **Step 5: Run the complete verification gate**

Run each command fresh and record exit code and key test names:

```bash
cd itsm-backend
go test ./service/bpmn ./service ./controller -run 'BPMN|ProcessTask|ProcessInstance|KafDelegate|Callback|CounterSign' -count=1
go test -race ./service ./internal/bootstrap -run 'TestBPMNAuthorization|TestTaskMutation|TestProcessInstanceMutation|TestCounterSign|TestCallback|TestClaimTask' -count=1
go test ./middleware ./router ./internal/bootstrap -count=1
go test ./... -count=1
go build ./...
git diff c15af6eda1febd47a75fb1e621907b16bbaac336..HEAD --check
```

Expected: every command exits 0.

- [ ] **Step 6: Perform final whole-branch review**

Review `c15af6eda1febd47a75fb1e621907b16bbaac336..HEAD` for tenant isolation, callback durability, idempotency propagation, worker lifecycle, transaction ownership, audit atomicity, KAF ordering, sensitive logging, migration safety, and truthful tests. Any Critical or Important finding blocks merge and push.

- [ ] **Step 7: Commit documentation and integration evidence**

```bash
git add itsm-backend/service/bpmn_claim_cas_integration_test.go itsm-backend/service/bpmn_outbox_integration_test.go itsm-backend/service/bpmn_final_fix_test.go docs/DEVELOPMENT_GUIDE.md
git commit -m "test(bpmn): prove callback and claim recovery"
```

- [ ] **Step 8: Merge and push only after the gate is green**

From the main worktree, confirm `main` still points at the recorded base or rebase the feature branch and rerun Step 5. Then merge `feat/bpmn-instance-authorization` into `main` without force, rerun `cd itsm-backend && go test ./... -count=1 && go build ./...` on the merged result, and push `main` to its configured upstream. Preserve the feature worktree until the remote push is confirmed.
