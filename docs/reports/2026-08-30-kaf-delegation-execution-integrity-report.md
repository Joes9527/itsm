# KAF Delegation Execution Integrity Acceptance Report

Date: 2026-08-31

## Status

The breaker-residual remediation passed automated production-path tests in both
linked worktrees. ITSM commit `47c836ca` and KAF commit `7fde7e50`, on top of
the earlier final-fix commits, close the remaining execution-integrity
findings. No live SSLVPN deployment, external network change, or production
credential operation was performed.

## Corrected Acceptance Evidence

| Requirement | Evidence and outcome |
| --- | --- |
| Partial BPMN completion | `TestReconcileTaskOnlyCompletion_DoesNotReportApplied`, `TestReconcileActivityWrittenWithoutSuccessor_DoesNotReportApplied`, and `TestReconcileEndActivityWrittenWithoutTerminalProcess_DoesNotReportApplied` prove task-only state, a changed activity with no live successor task, and an end pointer on a running process all remain retryable. Reconciliation succeeds only with a live task at the exact current activity or a completed process. |
| ITSM owner fencing | Four deterministic mid-call reclaim tests cover receipt creation, source-task completion, successor-activity movement, and terminal completion; a fifth stale-owner test covers variable-merge rollback. Each authoritative write and receipt creation is committed only after the same transaction post-validates and locks the exact unexpired executing owner. |
| Monotonic receipts | `TestKafCompletionReceipt_LateFailureCannotRegressSuccess` proves a late failure cannot replace `callback_succeeded`. |
| Commit-then-error convergence | `TestExecuteAction_RealEngineCallbackFailureRecoversWithoutSecondBPMNCompletion` commits a ledger-scope-keyed domain timeline effect and then returns an error. Retry observes the same idempotency key and converges with one domain mutation, one audit, one timeline row, and one successful receipt. This replaces the prior report's weaker fail-before-mutation evidence. |
| Non-completing actions | `TestExecuteAction_NonCompletingFinalizationFailureRollsBackEffectAndRetriesOnce` forces ledger finalization failure inside the Ent transaction. The first attempt leaves the process version and timeline unchanged; retry produces one version increment, timeline effect, ledger result, and audit. |
| Recursive and outbound redaction | KAF contract/pipeline tests cover nested bearer credentials, userinfo/query credentials, oversized structured scalar strings, bounded `resultSummary`, and bounded/redacted `evidenceRefs`. |
| Remote-applied recovery | The remote-applied/lease-stolen crash test proves the exact persisted completion payload is replayed when ITSM's delegated-task list is empty. `test_sqlite_pending_completion_replay_never_reexecutes_listed_procedure` additionally proves transient/`in_progress` replay cannot fall through to a second Procedure when the task is still listed. |
| Long Procedure lease | Deterministic short-TTL tests prove periodic owner-fenced renewal and cancellation/fail-closed behavior when renewal loses ownership. |
| Legacy migration | `test_migration_036_supersedes_duplicates_from_already_stamped_035` starts from the 035 schema and proves forward revision 036 deterministically keeps the first `(received_at, event_id)` row and marks extras `superseded`; shipped revision 035 no longer carries the cleanup. |
| RLS | Registered migration `019_kaf_execution_integrity_rls` enables and forces policies for both new tenant tables. Deterministic migration SQL/checksum tests pass; the tagged PostgreSQL tenant-isolation tests compile but skip without `RLS_TEST_DSN`. |

The prior positive claim for task-only callback reconciliation is withdrawn: a
task row marked complete is not proof that the process token advanced. The
prior callback claim is also replaced by the commit-then-error regression
above. Application tenant checks are not presented as substitutes for RLS.

## ITSM Verification

Worktree: `/home/administrator/project/itsm/.worktrees/kaf-delegation-transactional-delivery`

```bash
cd itsm-backend && go test ./... -count=1
```

Result: PASS for every Go package; `service` passed in 22.946s and the command
exited 0.

```bash
cd itsm-backend && go build ./...
```

Result: PASS, exit code 0.

```bash
cd itsm-backend && go test -race ./service -run 'Test(CompleteKafDelegatedTask_ReclaimDuring(ReceiptCreate|TaskCompletion|ActivityWrite|TerminalWrite)|MergeKafCompletionVariables_StaleOwner|Reconcile(ActivityWrittenWithoutSuccessor|EndActivityWrittenWithoutTerminalProcess)_DoesNotReportApplied)' -count=1
```

Result: PASS; seven selected race-enabled breaker regressions, package time
1.554s.

```bash
cd itsm-backend && go test ./service -run 'Test(Reconcile(ActivityWrittenWithoutSuccessor|EndActivityWrittenWithoutTerminalProcess)_DoesNotReportApplied|CompleteKafDelegatedTask_ReclaimDuring(ReceiptCreate|TaskCompletion|ActivityWrite|TerminalWrite)|MergeKafCompletionVariables_StaleOwner)' -count=1 -v
```

Result: PASS for all seven named production regressions.

```bash
cd itsm-backend && go test -tags integration_rls ./database/rls -count=1 -v
```

Result: nine deterministic tests PASS; five PostgreSQL integration tests,
including the new KAF cross-tenant table test, skipped because `RLS_TEST_DSN`
was not set.

```bash
cd itsm-backend && gofmt -l service/bpmn_process_engine.go service/bpmn_process_engine_ext_test.go service/kaf_delegation_service.go service/kaf_delegation_service_test.go
```

Result: no output; all four changed Go files were formatted.

## KAF Verification

Worktree: `/mnt/d/SynologyDrive/kerry/KAF_Migration_Pack/kaf-worktrees/kaf-delegation-transactional-delivery`

```bash
ENV_FILE=/dev/null DEBUG=true PYTHONPATH=src /home/administrator/actions-runner/_work/kaf/kaf/.venv/bin/python -m pytest tests/test_kaf_delegation_contract.py tests/test_kaf_delegation_pipeline.py tests/test_kaf_delegation_delivery_migration.py -q
```

Result: `111 passed, 1 skipped in 11.81s`. The skip is the optional configured
PostgreSQL probe; all SQLite production SQLAlchemy tests ran.

```bash
ENV_FILE=/dev/null DEBUG=true PYTHONPATH=src /home/administrator/actions-runner/_work/kaf/kaf/.venv/bin/python -m pytest tests/test_migration_dag.py -q
```

Result: `2 passed in 4.78s`.

```bash
ENV_FILE=/dev/null DEBUG=true PYTHONPATH=src /home/administrator/actions-runner/_work/kaf/kaf/.venv/bin/python -m ruff check alembic/versions/035_kaf_delegation_delivery_leases.py alembic/versions/036_kaf_delivery_completion_replay.py src/acp/models/kaf_delegation_delivery.py src/acp/orchestration/headless_tasks/kaf_delegation_pipeline.py tests/test_kaf_delegation_contract.py tests/test_kaf_delegation_pipeline.py tests/test_kaf_delegation_delivery_migration.py
ENV_FILE=/dev/null DEBUG=true PYTHONPATH=src /home/administrator/actions-runner/_work/kaf/kaf/.venv/bin/python -m ruff format --check alembic/versions/035_kaf_delegation_delivery_leases.py alembic/versions/036_kaf_delivery_completion_replay.py src/acp/models/kaf_delegation_delivery.py src/acp/orchestration/headless_tasks/kaf_delegation_pipeline.py tests/test_kaf_delegation_contract.py tests/test_kaf_delegation_pipeline.py tests/test_kaf_delegation_delivery_migration.py
```

Results: Ruff checks passed; all seven files were formatted.

```bash
ENV_FILE=/dev/null DEBUG=true PYTHONPATH=src /home/administrator/actions-runner/_work/kaf/kaf/.venv/bin/python -m alembic heads
```

Result: `036_kaf_completion_replay (head)`.

The repository-wide KAF `pytest -q` command did not collect tests because the
environment lacks optional dependency `sentence_transformers`, imported by
`tests/test_eval_embedding_models_cli.py`. This is an external environment
limitation, not a passing full-suite result.

Both repositories passed `git diff --check`. Protected untracked historical
ITSM review files were not modified, staged, or removed.

## Residual Limits

- PostgreSQL RLS and concurrency probes need configured test credentials to run
  rather than skip; deterministic SQL and SQLite transaction paths passed.
- No live SSLVPN infrastructure or external deployment was exercised.
- The generic non-KAF `CompleteTask` API remains multi-step by design; the
  exact-scope advancement and owner-fenced recovery guarantees apply to the
  dedicated KAF completion path.
