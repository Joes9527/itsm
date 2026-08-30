# KAF Delegation Execution Integrity Design

> Status: Draft for review
> Date: 2026-08-30
> Extends: `2026-08-28-kaf-itsm-autonomous-workitem-delegation-design.md`
> Trigger: final review findings for the 2026-08-29 delegation delivery plan

## 1. Goal

Make one autonomous KAF execution of one delegated ITSM `ProcessTask` durable,
recoverable, and exactly-once from the perspective of ITSM side effects. KAF
still selects Procedures and governs Tools. ITSM remains authoritative for task
state, BPMN advancement, professional lifecycle transitions, tenant isolation,
and audit.

This design replaces the current interpretation that a Procedure callback
returning successfully is enough to complete a KAF delivery.

## 2. Boundaries

- KAF never writes ITSM storage directly. It reads task context and invokes the
  existing task-scoped typed action API.
- KAF marks a delivery `completed` only after ITSM returns action result
  `applied` or `already_applied` for the same execution scope.
- ITSM action idempotency is an immutable, tenant-scoped persistence contract,
  not mutable `ProcessTask.task_variables` state.
- KAF execution coordination is local to KAF. It uses one active lease per
  `(tenant_id, task_id, correlation_id)` and does not create a parallel BPMN
  or WorkItem state machine.
- Raw Tool input/output remains in KAF. ITSM receives only typed, redacted
  action payloads and audit references.

## 3. ITSM Action Ledger

### 3.1 Data model

ITSM adds `KafTaskActionLedger` with immutable fields:

```text
tenant_id, task_id, run_id, step_id, action,
idempotency_key, correlation_id, procedure_ref, procedure_version,
result_status, result_payload, created_at
```

The unique constraint is `(tenant_id, task_id, run_id, step_id)`. A second
unique constraint on `(tenant_id, idempotency_key)` protects malformed callers
from reusing a key for a different execution scope. Result payloads contain
only structured action result metadata, never raw evidence or Tool output.

### 3.2 Action algorithm

1. Derive tenant and actor from authentication, tenant-scope the `ProcessTask`,
   and validate KAF per-task authorization.
2. Validate that the caller key is the canonical
   `tenantId:taskId:runId:stepId` representation. Reject any mismatch.
3. Begin one transaction. Insert a pending ledger row using the execution-scope
   unique constraint.
4. If insert conflicts, load the immutable result and return `already_applied`;
   never repeat a domain transition, timeline write, BPMN completion, or audit.
5. For a fresh row, validate version, allowed action, correlation, and domain
   rules; write the domain side effect, redacted timeline, audit, and final
   `applied` result in the same transaction.
6. If validation or a side effect fails, roll back the ledger insert and every
   side effect. The caller receives the existing typed rejection response.

`complete_bpmn_task` remains the only action that advances BPMN. The action
ledger result is committed atomically with `CompleteTask` and its audit.

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

Recovery queries the ITSM delegated-task list, resolves each task identity to
the existing delivery, and claims or resumes that delivery. It never creates a
second synthetic event identity for a task that already has a receipt. A
completed delivery is excluded from recovery. An expired running lease is
recoverable and carries its prior error context into the new attempt.

### 4.3 Procedure and action reporting

The production Procedure runner receives a typed action client in its
`KafExecutionContext`. After Tool execution it invokes a permitted task action
with the fixed execution scope. A delivery becomes `completed` only when the
client returns `applied` or `already_applied` for that scope. Network errors,
typed rejections, lease loss, and action responses inconsistent with the scope
leave the delivery `retryable` or `failed_auth` as appropriate; they do not
silently complete it.

The runner must include `runId`, `stepId`, `procedureRef`,
`procedureVersion`, `correlationId`, expected version, and the canonical
idempotency key in the typed action request. KAF refreshes ITSM context before
retrying a stale-version rejection.

## 5. Failure Behavior

| Condition | KAF delivery | ITSM task / BPMN |
| --- | --- | --- |
| Duplicate webhook or recovery sees active lease | No new execution | Unchanged |
| KAF crash after claim | `running` becomes claimable after expiry | Unchanged |
| Tool/procedure error | `retryable` with redacted detail | Unchanged unless a separate failure action is applied |
| ITSM `applied` / `already_applied` | `completed` | Effect committed once; BPMN advances only for completion |
| ITSM `failed_auth` / 401 / 403 | `failed_auth`, configured alert | Unchanged |
| ITSM domain rejection or stale version | `retryable`, context refresh before retry | Unchanged |

## 6. Acceptance Evidence

1. A production-composition SSLVPN Procedure test proves KAF calls ITSM
   `complete_bpmn_task` and the ProcessTask/BPMN advances exactly once.
2. A webhook/recovery interleaving test proves one active KAF execution for one
   tenant/task/correlation identity.
3. An expired-running-lease test proves recovery resumes the existing delivery.
4. Concurrent same-scope ITSM actions yield one `applied` and one
   `already_applied`, with one timeline/audit/domain side effect.
5. Same run/step with a different idempotency key is rejected before side
   effects; key reuse for a different scope is rejected.
6. All new persistence paths are tenant-scoped, redacted, and covered by
   schema/transaction tests.

## 7. Out Of Scope

- Adding typed `assign`, `resolve`, or `close` domain actions.
- Altering KAF Procedure/Tool policy selection.
- Creating a second ITSM approval or workflow engine.
- Live SSLVPN infrastructure execution or deployment credentials.
