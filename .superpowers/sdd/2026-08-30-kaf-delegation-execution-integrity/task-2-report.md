# Task 2 Report: KAF Completion Receipts

## Status

Implemented and verified in the ITSM linked worktree only.

Implementation commit: `1e6d3174 feat(bpmn): coordinate KAF completion receipts`

## Delivered Behavior

- Added tenant-scoped `KafTaskCompletionReceipt`, uniquely keyed by the Task 1 action ledger ID. It records `callback_pending`, `callback_succeeded`, or `callback_failed`, the authoritative task and tenant scope, a redacted error code, and timestamps.
- Added `CustomProcessEngine.CompleteKafDelegatedTask(ctx, ledgerID, taskID, variables) error`. It creates or loads the receipt before completing a delegated task, preserves callback metadata in the KAF task variables, returns callback errors, and writes receipt success only after the callback succeeds.
- Refactored the internal completion path so the public generic `ProcessEngine` interface and `CompleteTask` behavior remain unchanged. Generic callers retain their established callback-error logging behavior.
- Changed `dispatchUserTaskCallback` to return handler errors. A missing registry, absent callback metadata, or missing handler remains the pre-existing explicit no-op.
- Replaced Task 1's completed-task fallback with receipt-aware reconciliation. A completed KAF task is never sent through generic `CompleteTask` again: a successful receipt finalizes the action; a pending or failed receipt invokes only the callback recovery path under the action-ledger context.
- Receipt failures persist only the stable code `callback_failed`; callback error text, including bearer credentials, is not stored.

## Tests And Verification

- TDD red phase: `go test ./service -run 'Test(CompleteKafDelegatedTask|ReconcileCompletedTask)' -v` initially failed because `ent/kaftaskcompletionreceipt` did not exist.
- Focused suite passed: `go test ./service -run 'Test(CompleteKafDelegatedTask|ReconcileCompletedTask|ExecuteAction_)' -v`.
- Generated Ent artifacts: `go generate ./ent`.
- Backend compilation passed: `go build ./...`.
- `git diff --check` passed before the implementation commit.

## Regression Evidence

- `TestCompleteKafDelegatedTask_CallbackFailureReturnsErrorAndWritesReceipt` proves a real callback error reaches the caller and leaves a `callback_failed` receipt without bearer material.
- `TestReconcileCompletedTaskWithoutSuccessfulReceipt_DoesNotCompleteBPMNAgain` proves recovery of a completed task promotes the receipt via callback recovery rather than re-completing BPMN.
- Existing action lease and audit-failure recovery tests remain green with the receipt-aware completion boundary.

## Scope Controls

- No KAF repository files were changed.
- The public generic `ProcessEngine` interface was not changed.
- The protected, user-owned untracked historical review artifacts were not modified or staged.
- No delegation or routine approval wait was used, per the task brief.

## Round 1 Review Fixes

- Added `bpmn.KafActionScope`: an exported typed scope with immutable private fields, read-only accessors, `WithKafActionScope`, and `KafActionScopeFromContext`. The action coordinator attaches the ledger-derived scope for both first completion and recovery; `CustomProcessEngine.CompleteKafDelegatedTask` also derives and installs the same scope, so direct engine callers and recovery callbacks cannot lose it.
- Callback handlers can now read the ledger ID, tenant, task, run/step, action, canonical idempotency key, correlation ID, and procedure identity from context without trusting callback variables.
- Replaced raw callback-error logging and propagation with the stable `user task callback failed` error and `user_task_callback_failed` log code. Receipt persistence remains the stable `callback_failed` code. The callback's original error text is neither logged nor persisted.
- Strengthened recovery evidence with the real `CustomProcessEngine`: an Ent hook counts `ProcessTask` transitions to `completed` after setup, and recovery asserts zero attempts while the callback handler runs once and receives the immutable action scope.
- TDD red phase for this round: focused tests failed because `bpmn.KafActionScope` and `bpmn.KafActionScopeFromContext` did not exist. After adding the shared contract and propagation, the focused tests passed.
