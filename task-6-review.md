# Task 6 Independent Review

**Verdict: NEEDS_CHANGES**

## Remediation Update (2026-08-30)

### Implemented in ITSM

- Commit `ee9a7976` adds opaque, bounded `nextCursor` pagination to `GET /kaf-delegated`. The existing `items` and `limit` response fields remain unchanged; the cursor is based on the immutable ProcessTask primary key and can drain pages beyond 100 without timestamp serialization ambiguity.
- `update_progress` and `record_execution_failure` now atomically persist an internal TicketComment on the authoritative WorkItem activity stream together with the action idempotency marker and process version. The comment contains the appropriate summary after the existing credential redaction, and execution failures leave the ProcessTask `delegated`.
- Added controller-level regressions for a 102-task two-page recovery response and for both redacted timeline records, including the delegated-state invariant.

### Still Blocked: KAF P1

The required KAF worktree named by the Task 6 ledger (`/mnt/d/SynologyDrive/kerry/KAF_Migration_Pack/kaf-main`) is not present. The only local KAF checkout is `/home/administrator/actions-runner/_work/kaf/kaf` on dirty `main`, does not contain `kaf_delegation_pipeline.py` or either reviewed KAF commit (`83df60ab`, `468b651c`), and its configured remote cannot authenticate in this environment. Per the worktree constraint, it was not modified.

As a result, the KAF-side continuation consumer and the required 401/403 pull-list transition to `failed_auth` with pipeline-consistent alerting remain unimplemented and unverified. This report retains `NEEDS_CHANGES` until the linked KAF worktree or an accessible source/ref is supplied.

Scope reviewed independently:

- ITSM: `681887c0..0570dcda`
- KAF: `83df60ab..468b651c`
- Brief: `docs/superpowers/plans/2026-08-29-kaf-delegation-transactional-delivery.md`, Task 6

## Findings

### P1: Recovery permanently misses delegated tasks after the first 100

ITSM accepts only `limit` and returns `{items, limit}`; it has no page, offset, cursor, or continuation token. The query is sorted ascending and limited to 100. KAF always calls that endpoint once per recovery pass with no paging. Consequently, once a tenant has more than 100 delegated tasks, every task after the oldest 100 is invisible to pull recovery, including tasks whose webhook was missed.

- ITSM: `itsm-backend/controller/kaf_delegation_controller.go:54-73`
- ITSM: `itsm-backend/service/kaf_delegation_service.go:229-263`
- KAF: `src/acp/orchestration/headless_tasks/kaf_delegation_pipeline.py:142-161`

Expected: bounded pagination with a cursor/continuation response, and KAF must consume every page before ending a recovery pass.

### P1: `update_progress` and `record_execution_failure` do not implement their required effects

For both non-completing actions, the implementation records only an idempotency marker in `ProcessTask.TaskVariables` and increments the process-instance version. It never persists `resultSummary` or `failureSummary`, never writes the mandated redacted WorkItem timeline entry, and therefore provides no operational progress/failure record beyond the generic action audit metadata. The failure action correctly leaves the task delegated, but the required failure summary is dropped.

- ITSM: `itsm-backend/service/kaf_delegation_service.go:324-325`
- ITSM: `itsm-backend/service/kaf_delegation_service.go:351-375`
- ITSM: `itsm-backend/service/kaf_delegation_service.go:377-394`

Expected: redact and persist the appropriate summary to the authoritative WorkItem activity/timeline, retain `delegated` for execution failures, and add focused assertions for the timeline contents and redaction.

### P1: Pull-list authentication failures are only logged, not recorded as `failed_auth`

When `GET /kaf-delegated` returns 401/403, `recover_delegated_tasks` logs and returns. It does not transition any affected durable delivery to `failed_auth`, unlike context-fetch authentication failures handled by `_execute`. This does not meet Task 6's explicit 401/403 failure-state requirement and leaves outstanding deliveries in `received`, `running`, or `retryable` even though KAF cannot authenticate to ITSM. There is also no alert integration beyond the log line.

- KAF: `src/acp/orchestration/headless_tasks/kaf_delegation_pipeline.py:148-155`
- KAF: `src/acp/orchestration/headless_tasks/kaf_delegation_pipeline.py:230-238`

Expected: make the failed authentication observable as an alert and transition the relevant unfinished delivery records to `failed_auth` (or persist an equivalent tenant-scoped recovery-auth failure state that operators can act on).

## Verified Requirements

- Authorization is fail-closed on the protected ITSM route: JWT, RBAC, and tenant middleware run before the endpoints. The extracted `AuthorizeTask` preserves the prior KAF role, actor-tenant, and delegated-status checks; the new HTTP boundary additionally rejects non-`kaf_delegate` task types.
- Context responses include the required task identity, correlation, record class, allowed actions, expected version, waiting-point summary, intake snapshot, WorkItem fields, and attachment references.
- The action allowlist, expected-version check, per-task idempotency marker, and audit metadata are implemented. The audit body contains execution metadata rather than raw action payload/tool output.
- KAF uses a typed ITSM HTTP envelope (`code == 0`, object `data`) and validates task, correlation, tenant, type, status, and allowed actions before procedure scheduling.
- The Task 6 KAF path accesses ITSM only through the HTTP client; no direct ITSM database access was found.

## Verification Evidence

- `go test ./... -count=1` passed after the ITSM remediation.
- `go build ./...` passed after the ITSM remediation.
- `go test ./controller -run 'TestKaf(Context|Action|DelegatedList)_' -count=1 -v` passed, including the new pagination and timeline/redaction regressions.
- `go test ./controller -run 'TestKaf(Context|Action|DelegatedList)_' -v` passed.
- `go test ./service -run 'Test(CreateDelegatedTask_|KafDelegation|CompleteTask_ResumesDelegatedTask)' -v` passed.
- `go build ./...` passed.
- `python3 -m py_compile` passed for the changed KAF Task 6 Python files and test module.
- KAF pytest could not be run: both `pytest` and the worktree `.venv` Python lack the `pytest` module; the repository documents `uv run pytest`, but `uv` is not installed in this environment. No dependencies or environment state were modified for this review.
