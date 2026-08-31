# KAF Delegation Execution Integrity Implementation Plan（历史执行记录）

> **状态警告（2026-09-01）：** 本计划对应的 Tasks 1–5 和两轮审查整改已经实施，并通过 `dc1233c8` 合并到 ITSM `main`；真实 Dev 跨进程 SSLVPN/RLS 证据随后由 release-closeout 计划闭合。不得再把本文交给 `executing-plans` 或 `subagent-driven-development` 从头执行；下方勾选项是历史完成记录。

**Goal:** Make KAF delegated-task completion durable and recoverable through an ITSM action ledger, completion receipts, propagated callback failures, and KAF delivery leases.

**Architecture:** ITSM persists one immutable action request per `(tenant_id, task_id, run_id, step_id)` and uses it as a short-transaction coordinator; it does not try to inject a transaction through the generic BPMN engine. A KAF-specific engine completion path persists callback receipts and returns callback errors. KAF claims one leased delivery per delegated-task identity, invokes ITSM's typed action API, and only completes its delivery after `applied` or `already_applied`.

**Tech Stack:** Go, Gin, Ent, SQLite enttest/PostgreSQL; Python, FastAPI, SQLAlchemy/Alembic, httpx, pytest.

**Spec:** [KAF Delegation Execution Integrity Design](../specs/2026-08-30-kaf-delegation-execution-integrity-design.md)

> **Implementation status (updated 2026-09-01): COMPLETE for this plan's scope.** ITSM implementation and documentation are on `main@dc1233c8`; the historical ITSM feature worktree is at `41b24068`. KAF implementation is on its feature worktree at `b533daea` (`afbc1645` plus formatting). The former live cross-process SSLVPN/RLS evidence gap was closed by the 2026-08-31 release-closeout plan and report. Unified Intake subsequently completed as a separate increment; Incident typed actions, KAF execution-model cleanup and UI remain separate product increments.

## Global Constraints

- ITSM is authoritative for tenant checks, KAF per-task authorization, BPMN state, WorkItem lifecycle and audit; KAF never writes ITSM storage directly.
- Do not make generic `CompleteTask` externally transaction-composable and do not add another workflow or approval engine.
- KAF action identity is exactly `tenantId:taskId:runId:stepId`; every persistence lookup and mutation is tenant-scoped and fails closed.
- `KafTaskActionLedger` replaces, rather than supplements, `ProcessTask.task_variables["kaf_action_results"]`.
- Raw Tool inputs/outputs remain in KAF. ITSM timeline, receipts, audit and API payloads contain only redacted structured information.
- The only BPMN-advancing action in scope is `complete_bpmn_task`; do not add `assign`, `resolve`, or `close` actions. Existing `update_progress` and `record_execution_failure` actions must atomically persist their effect, ledger finalization, timeline, and audit.
- KAF delivery status becomes `completed` only for an ITSM `resultStatus` of `applied` or `already_applied`.

---

## File Structure

| File | Responsibility |
| --- | --- |
| `itsm-backend/ent/schema/kaf_task_action_ledger.go` | Immutable action scope, coordinator status and lease uniqueness. |
| `itsm-backend/ent/schema/kaf_task_completion_receipt.go` | Durable callback outcome keyed to an action ledger. |
| `itsm-backend/service/kaf_delegation_service.go` | Canonical-key validation, action claim/finalization, typed response and audit. |
| `itsm-backend/service/bpmn_process_engine.go` | KAF-only completion entry point and error-returning callback dispatch. |
| `itsm-backend/controller/kaf_delegation_controller.go` | Thin mapping of typed action result and in-progress conflict. |
| `itsm-backend/service/kaf_delegation_service_test.go` | Ledger concurrency, canonical identity, receipt recovery and redaction tests. |
| `itsm-backend/service/bpmn_process_engine_ext_test.go` | Callback-failure propagation and no-second-completion tests. |
| `kaf-main/src/acp/models/kaf_delegation_delivery.py` | Delivery task identity and lease columns. |
| `kaf-main/alembic/versions/*_kaf_delegation_delivery_leases.py` | KAF migration for identity uniqueness and leases. |
| `kaf-main/src/acp/orchestration/headless_tasks/kaf_delegation_pipeline.py` | Atomic claim/recovery and typed-action completion gate. |
| `kaf-main/tests/test_kaf_delegation_pipeline.py` | Lease races, expiry recovery and action-result tests. |
| `itsm-backend/handlers/service_request/kaf_delegation_sslvpn_e2e_test.go` | End-to-end SSLVPN acceptance evidence. |

---

### Task 1: Persist the ITSM action ledger and replace the response contract

**Files:**
- Create: `itsm-backend/ent/schema/kaf_task_action_ledger.go`
- Modify: `itsm-backend/ent/` generated files via `go generate ./ent`
- Modify: `itsm-backend/service/kaf_delegation_service.go:110-536`
- Test: `itsm-backend/service/kaf_delegation_service_test.go`

**Interfaces:**
- Produces `KafActionResult{Action string, IdempotencyKey string, ResultStatus string, ExpectedVersion int}` where `ResultStatus` is `applied` or `already_applied`.
- Produces `ClaimKafAction(ctx, task, req) (*ent.KafTaskActionLedger, bool, error)`, where `bool` is true only for a newly claimed lease.
- Consumes the existing `KafActionRequest` and `KafDelegationService.AuthorizeTask` behavior.

- [x] **Step 1: Write failing ledger and response-contract tests**

```go
func TestExecuteAction_ConcurrentScopeReturnsAppliedThenAlreadyApplied(t *testing.T) {
    svc, task, ctx := newKafActionFixture(t)
    req := validCompleteRequest(task, "run-1", "finish")
    first, err := svc.ExecuteAction(ctx, task.TaskID, req, fakeCompletedKafEngine{})
    require.NoError(t, err)
    second, err := svc.ExecuteAction(ctx, task.TaskID, req, fakeCompletedKafEngine{})
    require.NoError(t, err)
    assert.Equal(t, "applied", first.ResultStatus)
    assert.Equal(t, "already_applied", second.ResultStatus)
    assert.Equal(t, 1, countKafActionLedgers(t, svc.client, task.TenantID))
}

func TestExecuteAction_RejectsSameScopeWithDifferentKey(t *testing.T) {
    svc, task, ctx := newKafActionFixture(t)
    _, err := svc.ExecuteAction(ctx, task.TaskID, validCompleteRequest(task, "run-1", "finish"), fakeCompletedKafEngine{})
    require.NoError(t, err)
    conflicting := validCompleteRequest(task, "run-1", "finish")
    conflicting.Execution.IdempotencyKey = "wrong-key"
    _, err = svc.ExecuteAction(ctx, task.TaskID, conflicting, fakeCompletedKafEngine{})
    require.ErrorIs(t, err, ErrKafActionInvalid)
}
```

- [x] **Step 2: Run tests and confirm the old boolean contract fails**

Run: `cd itsm-backend && go test ./service -run 'TestExecuteAction_(ConcurrentScope|RejectsSameScope)' -v`

Expected: FAIL because `ResultStatus` and `KafTaskActionLedger` do not yet exist.

- [x] **Step 3: Add the Ent schema and generate Ent**

Create the schema with non-empty immutable request fields `tenant_id`, `task_id`, `run_id`, `step_id`, `action`, `idempotency_key`, `correlation_id`, `procedure_ref`, and `procedure_version`; coordinator fields `result_status`, `result_payload`, `lease_owner`, `lease_expires_at`, and `last_error_code`; plus timestamps. Add unique indexes exactly on `(tenant_id, task_id, run_id, step_id)` and `(tenant_id, idempotency_key)`. `result_status` defaults to `pending` and is constrained by service code to `pending`, `executing`, `applied`, `failed_retryable`, or `failed_terminal`.

Run: `cd itsm-backend && go generate ./ent`

- [x] **Step 4: Implement canonical validation and short-transaction claim/finalize methods**

Use `fmt.Sprintf("%d:%s:%s:%s", task.TenantID, task.TaskID, req.Execution.RunID, req.Execution.StepID)` as the only accepted key. In a transaction, insert `pending`; on scope-key uniqueness conflict load the ledger and compare action, correlation, procedure reference/version, and canonical key. Return `already_applied` only for `applied`; return `ErrKafActionConflict` for a live lease; conditionally claim `pending`, `failed_retryable`, or expired `executing` with `Where(result_status in ..., lease_expires_at < now)`.

Replace `KafActionResult.Applied` with:

```go
const (
    KafActionApplied        = "applied"
    KafActionAlreadyApplied = "already_applied"
)

type KafActionResult struct {
    Action          string `json:"action"`
    IdempotencyKey  string `json:"idempotencyKey"`
    ResultStatus    string `json:"resultStatus"`
    ExpectedVersion int    `json:"expectedVersion"`
}
```

- [x] **Step 5: Remove the obsolete mutable idempotency mechanism**

Delete `kafActionResult`, `putKafActionResult`, all `kaf_action_results` writes, and the old boolean replay behavior. Preserve `kaf_execution` only as non-authoritative BPMN context if the engine still requires it; it must never be queried for idempotency.

- [x] **Step 6: Run focused tests, build, and commit**

Run:

```bash
cd itsm-backend && go test ./service -run 'TestExecuteAction_|TestKafDelegation' -v
cd itsm-backend && go build ./...
git add itsm-backend/ent/schema/kaf_task_action_ledger.go itsm-backend/ent/ itsm-backend/service/kaf_delegation_service.go itsm-backend/service/kaf_delegation_service_test.go
git commit -m "feat(kaf): persist delegated action ledger"
```

### Task 2: Add durable KAF completion receipts and callback error propagation

**Files:**
- Create: `itsm-backend/ent/schema/kaf_task_completion_receipt.go`
- Modify: `itsm-backend/ent/` generated files via `go generate ./ent`
- Modify: `itsm-backend/service/bpmn_process_engine.go:322-443,489-526`
- Modify: `itsm-backend/service/kaf_delegation_service.go`
- Test: `itsm-backend/service/bpmn_process_engine_ext_test.go`
- Test: `itsm-backend/service/kaf_delegation_service_test.go`

**Interfaces:**
- Produces `CompleteKafDelegatedTask(ctx context.Context, ledgerID int, taskID string, variables map[string]interface{}) error` on `CustomProcessEngine`.
- Produces one receipt per ledger with `callback_pending`, `callback_succeeded`, or `callback_failed`.
- Consumes Task 1 ledger lease; generic `ProcessEngine.CompleteTask` interface remains unchanged for non-KAF callers.

- [x] **Step 1: Write failing callback-failure and recovery tests**

```go
func TestCompleteKafDelegatedTask_CallbackFailureReturnsErrorAndWritesReceipt(t *testing.T) {
    engine, task, ledger, ctx := delegatedTaskWithFailingCallback(t)
    err := engine.CompleteKafDelegatedTask(ctx, ledger.ID, task.TaskID, map[string]interface{}{"kaf_result_summary": "done"})
    require.Error(t, err)
    receipt := loadCompletionReceipt(t, engine.client, ledger.ID)
    assert.Equal(t, "callback_failed", receipt.Status)
    assert.NotContains(t, receipt.ErrorCode, "Bearer ")
}

func TestReconcileCompletedTaskWithoutSuccessfulReceipt_DoesNotCompleteBPMNAgain(t *testing.T) {
    svc, engine, task, ledger, ctx := completedTaskWithPendingReceipt(t)
    _, err := svc.ExecuteAction(ctx, task.TaskID, requestForLedger(ledger), engine)
    require.NoError(t, err)
    assert.Equal(t, 1, countCompletionAttempts(t, engine.client, task.ID))
}
```

- [x] **Step 2: Run the focused tests and confirm they fail**

Run: `cd itsm-backend && go test ./service -run 'Test(CompleteKafDelegatedTask|ReconcileCompletedTask)' -v`

Expected: FAIL because there is no receipt or KAF-specific engine method.

- [x] **Step 3: Add receipt schema and a KAF-only completion coordinator**

Create `KafTaskCompletionReceipt` with a unique `ledger_id`, tenant ID, task ID, `status`, optional redacted `error_code`, and timestamps. In `CompleteKafDelegatedTask`, create/read the receipt before execution. Change `dispatchUserTaskCallback` to return `error`; keep missing handler as the existing explicit no-op behavior, but return `handler.Execute` errors. For KAF completion, persist `callback_failed` before returning that error and `callback_succeeded` only after the handler succeeds.

- [x] **Step 4: Implement reconciliation rather than a second BPMN completion**

When a claimed ledger sees a completed task, load its receipt and verify that the authoritative process instance advanced beyond the exact task definition. If it is `callback_succeeded` and process advancement is proven, finalize the ledger without calling `CompleteTask`. If the receipt is pending or failed but advancement is proven, run only the receipt-scoped callback recovery path, passing the ledger scope in context; do not call generic `CompleteTask` again. If the task is still delegated, invoke `CompleteKafDelegatedTask` once. `applied` is legal only after a successful receipt, exact task scope, and authoritative process advancement are observed; task status alone remains retryable.

- [x] **Step 5: Run tests, full service build, and commit**

Run:

```bash
cd itsm-backend && go generate ./ent
cd itsm-backend && go test ./service -run 'Test(CompleteKafDelegatedTask|ReconcileCompletedTask|ExecuteAction_)' -v
cd itsm-backend && go build ./...
git add itsm-backend/ent/schema/kaf_task_completion_receipt.go itsm-backend/ent/ itsm-backend/service/bpmn_process_engine.go itsm-backend/service/bpmn_process_engine_ext_test.go itsm-backend/service/kaf_delegation_service.go itsm-backend/service/kaf_delegation_service_test.go
git commit -m "feat(bpmn): coordinate KAF completion receipts"
```

### Task 3: Complete the ITSM API mapping, audit, and concurrency coverage

**Files:**
- Modify: `itsm-backend/controller/kaf_delegation_controller.go`
- Modify: `itsm-backend/controller/kaf_delegation_controller_test.go`
- Modify: `itsm-backend/service/kaf_delegation_service.go`
- Test: `itsm-backend/service/kaf_delegation_service_test.go`

**Interfaces:**
- Consumes Task 1 `KafActionResult.ResultStatus` and Task 2 completion receipt.
- Produces HTTP 200 for `applied`/`already_applied`, 409 with a typed `in_progress` code for a live ledger lease, and existing authorization/validation status mapping.

- [x] **Step 1: Write controller contract tests**

```go
func TestExecuteAction_ReturnsResultStatusForReplay(t *testing.T) {
    response := performKafActionTwice(t, validActionJSON())
    assert.Equal(t, http.StatusOK, response.Code)
    assert.JSONEq(t, `{"code":0,"data":{"action":"complete_bpmn_task","idempotencyKey":"1:TASK-1:run-1:finish","resultStatus":"already_applied","expectedVersion":1}}`, response.Body.String())
}

func TestExecuteAction_ReturnsConflictForLiveLedgerLease(t *testing.T) {
    response := performKafAction(t, actionJSONForLiveLease())
    assert.Equal(t, http.StatusConflict, response.Code)
    assert.Contains(t, response.Body.String(), "in_progress")
}
```

- [x] **Step 2: Run controller tests to verify failure**

Run: `cd itsm-backend && go test ./controller -run 'TestExecuteAction_(ReturnsResultStatus|ReturnsConflict)' -v`

Expected: FAIL because the controller does not map the new result and conflict semantics.

- [x] **Step 3: Keep the controller thin and make audit refer to the ledger**

Map `ErrKafActionConflict` caused by a live lease to HTTP 409 and code `in_progress`; do not expose lease owner, internal receipt details, raw exception text, or Tool output. Update `recordActionAudit` so an applied action records its immutable ledger ID, task ID, correlation ID, procedure reference/version, and `resultStatus`; replay records a separate audit event only if current audit policy requires it, never a second domain timeline entry.

- [x] **Step 4: Add race and redaction regression tests**

Use two goroutines and a barrier around the ledger claim to assert exactly one engine completion. Assert an error containing a fake bearer token is stored only as a safe code, never in `KafTaskCompletionReceipt`, `AuditLog`, JSON response, or `TicketComment`.

- [x] **Step 5: Run verification and commit**

Run:

```bash
cd itsm-backend && go test ./controller ./service -run 'Test(ExecuteAction_|CompleteKafDelegatedTask|KafDelegation)' -v
cd itsm-backend && go build ./...
git add itsm-backend/controller/kaf_delegation_controller.go itsm-backend/controller/kaf_delegation_controller_test.go itsm-backend/service/kaf_delegation_service.go itsm-backend/service/kaf_delegation_service_test.go
git commit -m "fix(kaf): expose durable action outcomes"
```

### Task 4: Lease KAF delivery identity and invoke the ITSM action API

**Files:**
- Modify: `kaf-main/src/acp/models/kaf_delegation_delivery.py`
- Create: `kaf-main/alembic/versions/035_kaf_delegation_delivery_leases.py`
- Modify: `kaf-main/src/acp/orchestration/headless_tasks/kaf_delegation_pipeline.py`
- Test: `kaf-main/tests/test_kaf_delegation_pipeline.py`

**Interfaces:**
- Consumes ITSM `POST /api/v1/bpmn/process-tasks/{taskId}/actions` response `resultStatus`.
- Produces `KafExecutionContext` action client method `complete_bpmn_task(...) -> KafActionResponse`.
- Produces one active delivery lease per `(tenant_id, task_id, correlation_id)`.

- [x] **Step 1: Write failing KAF lease and action-gate tests**

```python
@pytest.mark.asyncio
async def test_pipeline_completes_delivery_only_after_itsm_applied(session, event):
    client = FakeItsmClient(context=valid_context(event), action_result={"resultStatus": "applied"})
    pipeline = KafDelegationPipeline(session=session, itsm_client=client, inline_execution=True)
    await pipeline.accept(event)
    assert (await delivery_by_event(session, event.event_id)).status == STATUS_COMPLETED
    assert client.action_calls == 1

@pytest.mark.asyncio
async def test_recovery_reclaims_expired_delivery_without_new_event_identity(session, event):
    delivery = await seed_running_delivery(session, event, expired=True)
    await KafDelegationPipeline(session=session, itsm_client=FakeItsmClient()).recover_delegated_tasks()
    assert (await delivery_by_task(session, event.tenant_id, event.task_id, event.correlation_id)).id == delivery.id
```

- [x] **Step 2: Run the focused tests and confirm they fail**

Run: `cd /mnt/d/SynologyDrive/kerry/KAF_Migration_Pack/kaf-worktrees/kaf-delegation-transactional-delivery && ENV_FILE=/dev/null DEBUG=true PYTHONPATH=src /home/administrator/actions-runner/_work/kaf/kaf/.venv/bin/python -m pytest tests/test_kaf_delegation_pipeline.py -q`

Expected: FAIL because delivery has no correlation/lease fields and the runner has no action client.

- [x] **Step 3: Migrate delivery identity and implement atomic claims**

Add `correlation_id`, `lease_owner`, and `lease_expires_at`; add a unique index for `(tenant_id, task_id, correlation_id)`. Use SQLAlchemy conditional `UPDATE` for `received`, `retryable`, or an expired `running` lease. A duplicate webhook resolves the existing identity record. Recovery must query the existing identity row and must never synthesize a new event ID for a task already represented locally.

- [x] **Step 4: Add the typed action client and completion gate**

Extend `KafItsmContextClient` with `complete_kaf_task(task_id: str, payload: dict[str, Any]) -> dict[str, Any]`; implement it in `HttpKafItsmContextClient` with the configured KAF automation bearer token. Add a typed action callable to `KafExecutionContext`. Build the canonical action key from context and Procedure run/step IDs. Mark delivery completed only for `resultStatus in {"applied", "already_applied"}`. On transport, stale-version, in-progress, or invalid result keep/release a retryable delivery lease; on 401/403 use `failed_auth` and existing configured alert behavior.

- [x] **Step 5: Run KAF verification and commit in the KAF worktree**

Run:

```bash
cd /mnt/d/SynologyDrive/kerry/KAF_Migration_Pack/kaf-worktrees/kaf-delegation-transactional-delivery
ENV_FILE=/dev/null DEBUG=true PYTHONPATH=src /home/administrator/actions-runner/_work/kaf/kaf/.venv/bin/python -m pytest tests/test_kaf_delegation_contract.py tests/test_kaf_delegation_pipeline.py -q
PYTHONPATH=src /home/administrator/actions-runner/_work/kaf/kaf/.venv/bin/python -m compileall -q src/acp
git add src/acp/models/kaf_delegation_delivery.py alembic/versions/035_kaf_delegation_delivery_leases.py src/acp/orchestration/headless_tasks/kaf_delegation_pipeline.py tests/test_kaf_delegation_pipeline.py tests/test_kaf_delegation_contract.py
git commit -m "feat(kaf): lease delegated task execution"
```

### Task 5: Prove SSLVPN completion, recovery, and absence of legacy state

**Files:**
- Modify: `itsm-backend/handlers/service_request/kaf_delegation_sslvpn_e2e_test.go`
- Modify: `itsm-backend/service/kaf_delegation_service_test.go`
- Modify: `kaf-main/tests/test_kaf_delegation_pipeline.py`
- Create: `docs/reports/2026-08-30-kaf-delegation-execution-integrity-report.md`

**Interfaces:**
- Consumes Tasks 1-4.
- Produces automated acceptance evidence for the design §6 and a concise report of commands/results.

- [x] **Step 1: Add failing cross-system acceptance tests**

```go
func TestSSLVPNKafDelegation_OneAppliedActionAdvancesBPMNOnce(t *testing.T) {
    fixture := newSSLVPNKafDelegationFixture(t)
    first := fixture.ExecuteKafCompletion("run-sslvpn", "finish")
    replay := fixture.ExecuteKafCompletion("run-sslvpn", "finish")
    require.Equal(t, "applied", first.ResultStatus)
    require.Equal(t, "already_applied", replay.ResultStatus)
    assert.Equal(t, 1, fixture.CompletedTaskCount())
    assert.Equal(t, 1, fixture.SuccessfulReceiptCount())
    assert.Equal(t, 0, fixture.LegacyKafActionResultCount())
}
```

- [x] **Step 2: Run the targeted acceptance tests and confirm failure before final wiring**

Run: `cd itsm-backend && go test ./handlers/service_request ./service -run 'TestSSLVPNKafDelegation_OneAppliedActionAdvancesBPMNOnce' -v`

Expected: FAIL until Tasks 1-4 are fully integrated.

- [x] **Step 3: Add recovery and callback-failure assertions**

Cover: duplicate webhook plus recovery interleaving; expired KAF lease; live ITSM ledger lease; forced callback failure; reconciliation of a completed task with a failed receipt; and a canonical-key mismatch. Each test must prove one domain transition/timeline/audit, never merely one HTTP request.

- [x] **Step 4: Run the final verification suite and write the evidence report**

Run:

```bash
cd itsm-backend && go test ./service ./controller ./handlers/service_request -run 'Test(Kaf|SSLVPN)' -v
cd itsm-backend && go build ./...
cd /mnt/d/SynologyDrive/kerry/KAF_Migration_Pack/kaf-worktrees/kaf-delegation-transactional-delivery && ENV_FILE=/dev/null DEBUG=true PYTHONPATH=src /home/administrator/actions-runner/_work/kaf/kaf/.venv/bin/python -m pytest tests/test_kaf_delegation_contract.py tests/test_kaf_delegation_pipeline.py -q
git diff --check
```

Record exact commands, pass counts, and any intentionally unexecuted environment-dependent check in `docs/reports/2026-08-30-kaf-delegation-execution-integrity-report.md`.

- [x] **Step 5: Commit verification evidence**

```bash
git add itsm-backend/handlers/service_request/kaf_delegation_sslvpn_e2e_test.go itsm-backend/service/kaf_delegation_service_test.go docs/reports/2026-08-30-kaf-delegation-execution-integrity-report.md
git commit -m "test: verify KAF delegation execution integrity"
```

---

## Final Review Remediation Amendment (2026-08-31)

- [x] Require exact-scope task identity plus a durable task at the process instance's exact current activity or a successful terminal process outcome before reconciliation can return `applied`; cover task-only, successor-activity-without-task, and end-activity-without-completion crashes.
- [x] Carry the concrete ITSM ledger owner through completion, transactionally post-validate receipt creation and every authoritative task/process completion write, fence callback/receipt/finalization predicates, and make receipt states monotonic.
- [x] Put non-completing action effect, process version, timeline, ledger, and audit in one Ent transaction; prove rollback and retry convergence after forced finalization failure.
- [x] Recursively redact and bound structured exception strings and outbound summaries/evidence references, including nested credentials and oversized values.
- [x] Persist the outbound KAF completion payload and make that delivery payload-replay-only forever, including transient/`in_progress` replay followed by delegated-list recovery.
- [x] Register forced-RLS policies for both new ITSM tenant tables and add deterministic SQL plus PostgreSQL tenant-isolation coverage.
- [x] Heartbeat the KAF lease during long Procedure execution and cancel/fail closed on ownership loss.
- [x] In forward revision `036_kaf_completion_replay`, deterministically adopt one pre-lease legacy delivery and mark additional rows `superseded` with an observable remediation code for databases already stamped at 035.
- [x] Correct the spec, plan, and evidence to report only behavior proven by production-path tests.

## Whole-Branch Review Amendment (2026-08-31)

- [x] Separate the Go/BPMN delegation endpoint and webhook secret from the
  legacy Gazellio ITSM configuration through `ITSM_KAF_URL`,
  `ITSM_KAF_AUTOMATION_TOKEN`, and `ITSM_KAF_WEBHOOK_SECRET`.
- [x] Use the configured delivery lease TTL consistently for claim, heartbeat
  renewal, pre-action renewal, and completion replay.
- [x] Return same-tenant opaque WorkItem attachment IDs from `kaf-context`
  without exposing names, paths, or URLs.
- [x] Make the embedding evaluator's optional dependency lazy so the base KAF
  suite can collect without the `embedding` extra.
- [x] Upgrade the local KAF Dev PostgreSQL database from revision 033 through
  036 and start current source successfully against it.
- [x] Run a live cross-process SSLVPN flow with a real Dev `kaf_automation`
  principal and execute PostgreSQL RLS probes with `RLS_TEST_DSN`; these remain
  release gates rather than inferred passes.
