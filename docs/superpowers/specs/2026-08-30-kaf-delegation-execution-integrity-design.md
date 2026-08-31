# KAF Delegation Execution Integrity Design

> Status: Implemented and whole-branch reviewed; controlled Dev acceptance, production E2E pending
> Date: 2026-08-30
> Extends: `2026-08-28-kaf-itsm-autonomous-workitem-delegation-design.md`
> Trigger: final review findings for the 2026-08-29 delegation delivery plan

## 1. Goal

Make one autonomous KAF execution of one delegated ITSM `ProcessTask` durable
and recoverable, with exactly-once acceptance at the ITSM action boundary and
one convergent ITSM completion outcome. A full cross-engine ACID transaction is
not an existing capability: `CompleteTask` currently commits task state,
variable merging, process advancement, callbacks, and audit separately. This
design therefore uses a durable coordinator rather than claiming that those
operations are one transaction.

KAF still selects Procedures and governs Tools. ITSM remains authoritative for
task state, BPMN advancement, professional lifecycle transitions, tenant
isolation, and audit.

This design replaces the current interpretation that a Procedure callback
returning successfully is enough to complete a KAF delivery.

## 2. Boundaries

- KAF never writes ITSM storage directly. It reads task context and invokes the
  existing task-scoped typed action API.
- The task-scoped Go/BPMN ITSM integration uses dedicated
  `ITSM_KAF_URL`, `ITSM_KAF_AUTOMATION_TOKEN`, and
  `ITSM_KAF_WEBHOOK_SECRET` settings. It must not reuse the legacy Gazellio
  `ITSM_URL`, credentials, or webhook secret.
- KAF marks a delivery `completed` only after ITSM returns action result
  `applied` or `already_applied` for the same execution scope.
- ITSM action idempotency is an immutable, tenant-scoped persistence contract,
  not mutable `ProcessTask.task_variables` state. The existing
  `kaf_action_results` map and its read/write helpers are removed in the same
  migration; they are not retained as a fallback or dual-write path.
- KAF execution coordination is local to KAF. It uses one active lease per
  `(tenant_id, task_id, correlation_id)` and does not create a parallel BPMN
  or WorkItem state machine.
- Raw Tool input/output remains in KAF. ITSM receives only typed, redacted
  action payloads and audit references.

## 3. ITSM Action Ledger

### 3.1 Data model

ITSM adds `KafTaskActionLedger` with immutable request fields and coordinator
state:

```text
tenant_id, task_id, run_id, step_id, action,
idempotency_key, correlation_id, procedure_ref, procedure_version,
result_status, result_payload, lease_owner, lease_expires_at,
last_error_code, created_at, updated_at
```

The unique constraint is `(tenant_id, task_id, run_id, step_id)`. A second
unique constraint on `(tenant_id, idempotency_key)` protects malformed callers
from reusing a key for a different execution scope. `result_status` is one of
`pending`, `executing`, `applied`, `failed_retryable`, or `failed_terminal`.
Only terminal successful rows contain an immutable structured result payload;
payloads never contain raw evidence or Tool output.

The HTTP response contract replaces `KafActionResult.Applied bool` with
`resultStatus: "applied" | "already_applied"` plus `action`,
`idempotencyKey`, and `expectedVersion`. `already_applied` is a response
meaning, not a separately persisted ledger state. DTO, controller mapper, KAF
client, and contract tests change together.

### 3.2 Action algorithm

1. Derive tenant and actor from authentication, tenant-scope the `ProcessTask`,
   validate KAF per-task authorization, and validate the canonical
   `tenantId:taskId:runId:stepId` key.
2. In a short transaction, insert the immutable request as `pending`. A
   uniqueness conflict loads the existing row: a successful row returns
   `already_applied`; a live `pending`/`executing` lease returns a typed
   in-progress conflict; an expired or retryable row is eligible for recovery.
   A request whose action, correlation, procedure, or canonical key differs
   from the stored scope is rejected.
3. Atomically claim a `pending`, `failed_retryable`, or expired `executing` row
   by setting `executing` and a bounded lease. The concrete lease owner is a
   fencing token carried through the completion coordinator. Before engine
   completion, callback dispatch, every receipt transition, and ledger
   finalization, an atomic predicate must prove that this exact owner still
   owns an unexpired `executing` ledger. Receipt creation and every
   authoritative task/process completion write run in a short transaction that
   post-validates and locks that exact ledger owner before commit; reclaim or
   expiry rolls the write back.
4. The coordinator invokes the dedicated KAF delegated-completion entry point.
   `complete_bpmn_task` remains the only action that advances BPMN. It must
   return synchronous UserTask callback failures; logging and returning `nil`
   is forbidden for this path.
5. On a successful completion result, a short transaction writes the redacted
   timeline, audit reference, and ledger `applied` payload. The response is
   `applied`. A retry that finds this row returns `already_applied` without
   repeating the completion request.
6. On an engine or callback failure, the coordinator records
   `failed_retryable` (or `failed_terminal` for authorization/validation) and
   clears its lease. It never marks the action applied merely because an
   earlier partial BPMN write exists.

This is a durable coordination protocol, not a promise that today's generic
`CompleteTask` is externally transaction-composable. Recovery reconciles the
ledger with the authoritative task and completion receipt before selecting a
retry or a final result.

### 3.3 BPMN completion and callback contract

The implementation adds a KAF-specific completion coordinator at the BPMN
boundary rather than attempting to pass one caller transaction through every
`executeStep` and `ServiceTaskHandlerInterface` implementation.

- `dispatchUserTaskCallback` returns an error. For KAF-delegated completion,
  handler failure is persisted as a failed completion receipt and is returned
  to the action coordinator; it cannot be log-only success.
- A completion receipt is durable and keyed by the action-ledger ID. It records
  `callback_pending`, `callback_succeeded`, or `callback_failed`, the task ID,
  and a redacted error code. It lets recovery distinguish "task already moved"
  from "all required business callbacks succeeded".
- Receipt transitions are monotonic and owner-fenced. A stale owner cannot
  create or update a receipt, and a late failure cannot regress a successful
  receipt.
- Callback handlers that mutate ITSM domain state receive the immutable action
  scope and must use it as their own idempotency boundary. They either commit
  their effect once and report success, or report an error without claiming
  success. The coordinator may only finalize `applied` after the receipt is
  `callback_succeeded` and the task plus authoritative process instance reflect
  advancement beyond the exact task definition. A running process proves
  advancement only when a non-terminal successor task exists for the instance's
  exact `current_activity_id`; a terminal outcome requires the process status
  itself to be `completed`. A changed activity pointer or task status alone is
  never sufficient evidence of completion.
- If reconciliation finds a completed task with no successful receipt, it
  leaves the ledger retryable and routes the callback through its same scoped
  idempotency boundary. It does not invoke generic `CompleteTask` again.

The existing generic `CompleteTask` remains a multi-step engine API. Its
non-KAF callers retain existing semantics; KAF's dedicated entry point is the
only scope changed by this design.

The existing non-BPMN-advancing actions `update_progress` and
`record_execution_failure` remain supported. Their process-version update,
timeline effect, ledger finalization, and audit write are one database
transaction, so a failed finalization rolls back the effect and a retry can
converge without a permanent version mismatch.

## 4. KAF Execution Lease And Completion

### 4.1 Delivery identity

`KafDelegationDelivery` remains the immutable webhook-receipt ledger keyed by
`event_id`. It gains `correlation_id`, `lease_owner`, and `lease_expires_at`.
A database uniqueness constraint on `(tenant_id, task_id, correlation_id)`
prevents a webhook and recovery scan from creating independent executions for
the same delegated task.

### 4.2 Claim and recovery

An atomic conditional update claims a receipt only when it is `received` or
`retryable`, or when `running` has an expired lease. The claim sets `running`,
a generated lease owner, and a bounded expiry. A webhook duplicate locates the
existing task identity record and does not enqueue another Procedure.

Claim, periodic heartbeat renewal, pre-action renewal, and completion replay
must derive expiry from the same configured lease TTL. A test-only short TTL
cannot change only the heartbeat cadence while leaving a 300-second database
lease behind.

Recovery queries the ITSM delegated-task list, resolves each task identity to
the existing delivery, and claims or resumes that delivery. It never creates a
second synthetic event identity for a task that already has a receipt. A
completed delivery is excluded from recovery. An expired running lease is
recoverable and carries its prior error context into the new attempt.

Before invoking ITSM, KAF persists the exact bounded action payload. A local
recovery scan claims replayable incomplete deliveries before consulting the
delegated-task list, replays that payload, and converges the row after remote
`applied` even when ITSM no longer advertises the task. Once a delivery has a
persisted completion payload it is replay-only forever: delegated-list recovery
must never run its Procedure or Tools again, including after transient or
`in_progress` replay responses. Multiple pre-lease legacy rows are ranked
deterministically by forward revision `036_kaf_completion_replay`; one canonical
row remains adoptable and the others become observable `superseded` rows. This
cleanup does not rely on rerunning shipped revision 035.

### 4.3 Procedure and action reporting

The production Procedure runner receives a typed action client in its
`KafExecutionContext`. After Tool execution it invokes a permitted task action
with the fixed execution scope. A delivery becomes `completed` only when the
client returns `applied` or `already_applied` for that scope. Network errors,
typed rejections, lease loss, and action responses inconsistent with the scope
leave the delivery `retryable` or `failed_auth` as appropriate; they do not
silently complete it.

While Procedure or Tool execution is active, KAF renews the delivery lease at
a bounded interval. Renewal uses the same owner token; ownership loss cancels
the local Procedure and fails closed before any final action is submitted.

The runner must include `runId`, `stepId`, `procedureRef`,
`procedureVersion`, `correlationId`, expected version, and the canonical
idempotency key in the typed action request. KAF refreshes ITSM context before
retrying a stale-version rejection.

Every scalar string in structured exceptions is recursively redacted and
bounded before logging or persistence. `resultSummary` and every
`evidenceRefs` element use the same policy before ITSM transport; summary,
reference length, and reference count are bounded.

Task context returns only tenant-filtered opaque attachment IDs. It never
returns attachment file names, storage paths, direct URLs, or signed URLs.

Both ITSM ledger tables are covered by a registered production migration that
enables and forces tenant RLS with `USING` and `WITH CHECK` policies following
the repository's `app.current_tenant` convention.

## 5. Failure Behavior

| Condition | KAF delivery | ITSM task / BPMN |
| --- | --- | --- |
| Duplicate webhook or recovery sees active lease | No new execution | Unchanged |
| KAF crash after claim | `running` becomes claimable after expiry | Unchanged |
| Tool/procedure error | `retryable` with redacted detail | Unchanged unless a separate failure action is applied |
| ITSM `applied` / `already_applied` | `completed` | Completion receipt succeeded; BPMN advances only once |
| ITSM completion/callback failure | `retryable` | Ledger is not applied; receipt and task are reconciled before retry |
| ITSM `failed_auth` / 401 / 403 | `failed_auth`, configured alert | Unchanged |
| ITSM domain rejection or stale version | `retryable`, context refresh before retry | Unchanged |

## 6. Acceptance Evidence

1. A production-composition SSLVPN Procedure test proves KAF calls ITSM
   `complete_bpmn_task` and the ProcessTask/BPMN advances exactly once.
2. A webhook/recovery interleaving test proves one active KAF execution for one
   tenant/task/correlation identity.
3. An expired-running-lease test proves recovery resumes the existing delivery.
4. Concurrent same-scope ITSM actions yield one `applied` and one
   `already_applied`, with one timeline/audit/domain side effect. A live lease
   returns an in-progress response rather than pretending to be applied.
5. Same run/step with a different idempotency key is rejected before side
   effects; key reuse for a different scope is rejected.
6. A forced KAF callback failure is observable to the action caller, leaves a
   non-applied completion receipt, and is recoverable without a second BPMN
   completion or duplicate domain mutation.
7. `kaf_action_results`, `kafActionResult`, `putKafActionResult`, and all
   `task_variables` idempotency writes are absent after migration.
8. All new persistence paths are tenant-scoped, redacted, and covered by
   schema/transaction tests.
9. Failure after writing a successor/end activity cannot reconcile as applied
   without a durable successor task or completed process, and deterministic
   mid-call reclaim tests prove stale owners cannot commit receipt, task,
   activity, or terminal writes.
10. A transient completion replay followed by delegated-list recovery invokes
    the Procedure once total, and an already-stamped 035 schema executes legacy
    duplicate cleanup through forward revision 036.
11. A delegation signed with only the legacy ITSM webhook secret is rejected;
    the dedicated KAF secret is required.
12. A configured short delivery TTL is persisted by claim and renewal, not
    used only as a heartbeat interval.
13. `kaf-context` returns opaque same-tenant attachment IDs and excludes names,
    paths, and URLs.

## 7. Out Of Scope

- Adding typed `assign`, `resolve`, or `close` domain actions.
- Altering KAF Procedure/Tool policy selection.
- Creating a second ITSM approval or workflow engine.
- Live SSLVPN infrastructure execution or deployment credentials.
- Wiring production alert delivery channels; this increment records the
  configured alert condition but does not select or operate a notifier.
