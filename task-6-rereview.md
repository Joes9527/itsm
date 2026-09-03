# Task 6 KAF Re-Review

**Verdict: NEEDS_CHANGES**

## Scope

- KAF worktree: `/mnt/d/SynologyDrive/kerry/KAF_Migration_Pack/kaf-worktrees/kaf-delegation-transactional-delivery`
- Reviewed range: `468b651c..33747bcf`
- Prior P1 source: `task-6-review.md`

## Findings

### P1: List-auth failure can overwrite an in-flight delivery state

`_recoverable_deliveries` reads `received` and `retryable` rows, then
`_mark_recoverable_deliveries_failed_auth` mutates the ORM objects and commits
later. The eventual updates are not conditional on the original status and the
read has no row lock. A concurrently scheduled Task 5 execution can advance a
selected row to `running` or `completed` between the read and the commit; this
handler can then overwrite that newer state with `failed_auth`. The reverse
ordering can also lose the authentication failure when the executor commits
afterward.

- KAF: `src/acp/orchestration/headless_tasks/kaf_delegation_pipeline.py:246-256`
- KAF: `src/acp/orchestration/headless_tasks/kaf_delegation_pipeline.py:365-374`
- Concurrent execution is normal production behavior: `asyncio.create_task`
  opens a separate session at `src/acp/orchestration/headless_tasks/kaf_delegation_pipeline.py:278-288`.

The single `commit()` makes the selected batch all-or-nothing, but not a safe
state transition. Implement this as a conditional database update restricted to
the unfinished statuses (and return the rows actually updated for the alert),
or use an equivalent transaction/locking strategy. Add a real-session
regression that interleaves the auth-failure transition with an execution state
advance.

### P1: The production alert remains only a log record

The injected `RecoveryAuthAlert` is asserted only through a test callback. The
application constructs `KafDelegationPipeline()` with its default callback,
which emits a structured `critical` log entry; no configured notifier, alert
sink, or composition-time callback is wired. This does not yet establish the
required operator alert integration beyond logging.

- KAF: `src/acp/orchestration/headless_tasks/kaf_delegation_pipeline.py:121-129`
- KAF: `src/acp/routers/itsm_webhooks.py:27`

Wire the callback to the project's production alert mechanism and cover that
composition path. This can remain independent of the ITSM database; the local
KAF delivery ledger is the appropriate persistence boundary.

## Verified

- Cursor pagination forwards opaque `nextCursor` values with HTTP query params,
  returns only on `nextCursor: null`, and rejects empty, malformed, and repeated
  cursors before another request. A runtime probe confirmed a repeated cursor
  stops after two requests with `itsm_delegated_tasks_invalid_cursor`.
- The KAF Task 6 path continues to use the typed HTTP client for ITSM; no direct
  ITSM database fallback was introduced.
- The focused Task 5 webhook and delegation regression suite passed, so no
  regression was found in receipt, duplicate, context-validation, or execution
  behavior.

## Verification Evidence

- `ENV_FILE=/dev/null DEBUG=true PYTHONPATH=src /home/administrator/actions-runner/_work/kaf/kaf/.venv/bin/python -m pytest tests/test_itsm_webhooks.py tests/test_itsm_webhook_auth.py tests/test_kaf_delegation_pipeline.py -q` -> `37 passed`.
- `ENV_FILE=/dev/null DEBUG=true PYTHONPATH=src /home/administrator/actions-runner/_work/kaf/kaf/.venv/bin/python -m compileall -q src/acp/orchestration/headless_tasks/kaf_delegation_pipeline.py tests/test_kaf_delegation_pipeline.py` -> passed.
- `ENV_FILE=/dev/null DEBUG=true PYTHONPATH=src /home/administrator/actions-runner/_work/kaf/kaf/.venv/bin/python -m ruff check src/acp/orchestration/headless_tasks/kaf_delegation_pipeline.py tests/test_kaf_delegation_pipeline.py` -> `All checks passed!`.
