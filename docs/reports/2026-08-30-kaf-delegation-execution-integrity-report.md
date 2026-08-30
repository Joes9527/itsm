# KAF Delegation Execution Integrity Acceptance Report

Date: 2026-08-30

## Status

Task 5 final acceptance passed in the two requested linked worktrees. This is
automated local acceptance evidence only. No real external SSLVPN deployment,
network change, or production credential operation was executed or claimed.

KAF Task 5 commits:

- `cf01648fb233c621f4785820a3e4297afdc69da4` - redact non-hierarchical URI credential suffixes.
- `1ca123f831f2101e3d8876d8508953fe2dad2d3f` - verify webhook/recovery execution cardinality.

The ITSM commit is reported in the Task 5 handoff because this report is part
of that commit.

## Acceptance Matrix

| Requirement | Evidence |
| --- | --- |
| SSLVPN applied/replay cardinality | `TestSSLVPNKafDelegation_OneAppliedActionAdvancesBPMNOnce` proves `applied`, then `already_applied`, with one completed BPMN task, completed process, applied ledger, successful receipt, and action audit. |
| Callback failure recovery | `TestExecuteAction_CallbackFailureRecoversWithoutSecondBPMNCompletion` proves a failed receipt is recovered through the scoped callback with one BPMN completion write, one domain comment, one successful receipt, and one audit. |
| Completed task with failed receipt | `TestReconcileCompletedTaskWithoutSuccessfulReceipt_DoesNotCompleteBPMNAgain` proves callback-only reconciliation and zero second completion writes. |
| Webhook/recovery interleaving | `test_webhook_and_recovery_interleaving_keeps_one_active_execution` pauses the webhook owner while duplicate webhook and recovery run, then proves one delivery, procedure execution, enqueue, and ITSM action. |
| Expired leases | `test_recovery_reclaims_expired_delivery_without_new_event_identity` reclaims the existing KAF delivery; `TestExecuteAction_RetryAfterAppliedFinalizationFailureCreatesOneAudit` reclaims an expired ITSM ledger lease. |
| Live ITSM ledger lease | `TestExecuteAction_RejectsActiveLeaseWithoutCallingEngine` proves one executing ledger and zero engine, timeline, audit, or task-transition effects. |
| Canonical mismatch | `TestExecuteAction_RejectsSameScopeWithDifferentKey` proves the mismatched replay adds no ledger, engine, timeline, audit, or task effect beyond the original action. |
| One audit/timeline/domain effect | `TestExecuteAction_ConcurrentClaimsCompleteExactlyOnce` checks one ledger, audit, timeline comment, task completion, and engine call after contention and replay. |
| Legacy state absent | The SSLVPN test rejects `task_variables["kaf_action_results"]`; the production Go source search below returned no legacy identifier. |
| Persisted URI redaction | The KAF persisted-`last_error` matrix covers `mailto:`, `urn:`, `data:`, `s3:`, and generic valid schemes after pipe, slash, comma, semicolon, and ampersand, while sensitive `key=value` and `key:value` boundaries remain detected. |

## TDD Evidence

KAF RED:

```bash
ENV_FILE=/dev/null DEBUG=true PYTHONPATH=src /home/administrator/actions-runner/_work/kaf/kaf/.venv/bin/python -m pytest tests/test_kaf_delegation_pipeline.py -q -k 'non_hierarchical_uri_suffixes or preserves_real_sensitive_assignment_boundaries'
```

Result before the production fix: expected failure, `20 failed, 7 passed, 66 deselected`.
Every parser-recognized opaque URI suffix remained in persisted `last_error`;
the real sensitive assignment controls passed.

KAF GREEN:

```bash
ENV_FILE=/dev/null DEBUG=true PYTHONPATH=src /home/administrator/actions-runner/_work/kaf/kaf/.venv/bin/python -m pytest tests/test_kaf_delegation_pipeline.py -q -k 'non_hierarchical_uri_suffixes or preserves_real_sensitive_assignment_boundaries or ambiguous_delimiter_suffixes or bounded_text_redaction'
```

Result: `46 passed, 52 deselected in 5.01s`.

ITSM RED:

```bash
cd itsm-backend && go test ./handlers/service_request -run 'TestSSLVPNRequest_ApprovalDelegationDeliveryAndCompletion' -count=1 -v
```

Result before Task 5 fixture wiring: expected failure at canonical validation
because the SSLVPN fixture used the tenant code instead of the authoritative
numeric tenant ID. The immutable canonical request was then reused for replay.

## Final ITSM Verification

Worktree: `/home/administrator/project/itsm/.worktrees/kaf-delegation-transactional-delivery`

```bash
cd itsm-backend && go test ./handlers/service_request ./service -run 'TestSSLVPNKafDelegation_OneAppliedActionAdvancesBPMNOnce' -count=1 -v
```

Result: PASS; one handler test passed and the service package reported no matching tests.

```bash
cd itsm-backend && go test ./service ./controller ./handlers/service_request -run 'Test(Kaf|SSLVPN)' -count=1 -v
```

Result: PASS; 28 tests passed: 11 service, 12 controller, and 5 handler tests.

```bash
cd itsm-backend && go test ./service -run 'Test(ExecuteAction_|ReconcileCompletedTask|CompleteKafDelegatedTask)' -count=1 -v
```

Result: PASS; 9 tests passed.

```bash
cd itsm-backend && go test -race ./service -run 'TestExecuteAction_ConcurrentClaimsCompleteExactlyOnce' -count=1 -v
```

Result: PASS; 1 race-enabled test passed.

```bash
cd itsm-backend && go build ./...
```

Result: PASS, exit code 0.

```bash
rg -n 'kaf_action_results|kafActionResult|putKafActionResult' itsm-backend --glob '*.go' --glob '!*_test.go'
```

Result: no matches; expected `rg` exit code 1.

## Final KAF Verification

Worktree: `/mnt/d/SynologyDrive/kerry/KAF_Migration_Pack/kaf-worktrees/kaf-delegation-transactional-delivery`

```bash
ENV_FILE=/dev/null DEBUG=true PYTHONPATH=src /home/administrator/actions-runner/_work/kaf/kaf/.venv/bin/python -m pytest tests/test_kaf_delegation_contract.py tests/test_kaf_delegation_pipeline.py -q -rs
```

Result: `101 passed, 1 skipped in 9.62s`. The skip is the pre-existing
PostgreSQL-only real-session concurrency probe; configured credentials failed
with `InvalidPasswordError`. SQLite SQLAlchemy contention and lease-fencing
tests executed.

```bash
ENV_FILE=/dev/null DEBUG=true PYTHONPATH=src /home/administrator/actions-runner/_work/kaf/kaf/.venv/bin/python -m ruff check src/acp/orchestration/headless_tasks/kaf_delegation_pipeline.py tests/test_kaf_delegation_pipeline.py
```

Result: `All checks passed!`

```bash
ENV_FILE=/dev/null DEBUG=true PYTHONPATH=src /home/administrator/actions-runner/_work/kaf/kaf/.venv/bin/python -m ruff format --check src/acp/orchestration/headless_tasks/kaf_delegation_pipeline.py tests/test_kaf_delegation_pipeline.py
```

Result: `2 files already formatted`.

```bash
ENV_FILE=/dev/null DEBUG=true PYTHONPATH=src /home/administrator/actions-runner/_work/kaf/kaf/.venv/bin/python -m compileall -q src/acp/orchestration/headless_tasks/kaf_delegation_pipeline.py tests/test_kaf_delegation_pipeline.py
```

Result: PASS, no output, exit code 0.

Migration files were not changed in Task 5. The existing graph was still checked:

```bash
ENV_FILE=/dev/null DEBUG=true PYTHONPATH=src /home/administrator/actions-runner/_work/kaf/kaf/.venv/bin/python -m pytest tests/test_migration_dag.py -q
ENV_FILE=/dev/null DEBUG=true PYTHONPATH=src /home/administrator/actions-runner/_work/kaf/kaf/.venv/bin/python -m alembic heads
ENV_FILE=/dev/null DEBUG=true PYTHONPATH=src /home/administrator/actions-runner/_work/kaf/kaf/.venv/bin/python -m alembic upgrade 035_kaf_delivery_leases --sql
```

Results: migration DAG `2 passed in 5.08s`; head is
`035_kaf_delivery_leases`; offline PostgreSQL SQL generation exited 0. No live
database migration was applied.

```bash
git diff --check
git diff --cached --check
```

Result: PASS in both repositories before final commit; no output, exit code 0.

## Concerns

- The optional real-PostgreSQL KAF concurrency probe did not run because the configured test database rejected credentials.
- No live SSLVPN infrastructure or external deployment was exercised; this is intentionally out of scope.
- Protected historical untracked ITSM review artifacts were not modified, staged, or removed.
