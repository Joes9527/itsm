# KAF/ITSM Execution Integrity Final-Fix Report

Date: 2026-08-31

## Outcome

All two Critical and seven Important findings in
`final-whole-branch-review.md` were verified against production source and
accepted. No finding required technical rejection. The plan's statement that
only `complete_bpmn_task` was "in scope" conflicted with the existing typed API;
the safe correction is that it is the only BPMN-advancing action, while
`update_progress` and `record_execution_failure` remain supported with atomic
effect convergence.

Code commits:

- ITSM `adb2b904` - `fix(kaf): fence delegated execution integrity`
- KAF `6e4bedb7` - `fix: close KAF delegation recovery gaps`

The spec, plan, corrected acceptance evidence, and this report are committed
separately from the ITSM code so the production remediation remains reviewable.

## Finding Map

| Finding | Verification and remediation |
| --- | --- |
| Critical: partial BPMN completion | Confirmed. Reconciliation now requires exact `kaf_execution` task scope and authoritative process advancement beyond that task. A task-only completed row remains retryable; a legitimately advanced instance reconciles. |
| Critical: structured exception disclosure | Confirmed by ordinary-key nested scalar paths. KAF now recursively sanitizes and bounds every scalar string before persistence/logging. Nested bearer, URI credential, and oversized payload tests pass. |
| Important: ITSM lease fencing | Confirmed. The concrete ledger owner is threaded into the engine. Completion/callback, every receipt mutation, and ledger finalization atomically require the current unexpired executing owner. Receipt success cannot regress. |
| Important: non-completing crash window | Confirmed. Process version, timeline effect, ledger applied payload, and action audit now share one Ent transaction. Forced finalization failure rolls everything back; retry applies exactly once without version mismatch. |
| Important: remote applied/local running | Confirmed. KAF persists the exact bounded completion payload before transport and locally scans/replays recoverable rows independently of ITSM's delegated list. Remote-applied plus stolen-lease crash converges. |
| Important: missing RLS | Confirmed. Registered migration `019_kaf_execution_integrity_rls` enables and forces tenant policies with `USING`/`WITH CHECK` for both new tables. Deterministic SQL and tagged PostgreSQL isolation tests were added. |
| Important: long Procedure lease | Confirmed. Procedure execution has owner-fenced periodic heartbeat; ownership loss cancels execution and fails closed before the final action. Deterministic short-TTL loss tests pass. |
| Important: legacy duplicates | Confirmed. Migration and runtime adoption rank by `(received_at, event_id)`, retain one canonical row, and mark extras `superseded` with `legacy_identity_superseded`. |
| Important: evidence defects/outbound redaction | Confirmed. `resultSummary` and `evidenceRefs` are redacted and bounded before transport. The evidence report withdraws task-only success and replaces fail-before-mutation with a real commit-then-error callback test. |

Source nuance: the production KAF BPMN callback currently records no separate
professional-domain mutation. The commit-then-error regression therefore uses
the real engine and a registered production callback handler that persists a
ledger-scope-keyed timeline effect before returning an error. This tests the
dangerous contract directly and proves one mutation/audit/timeline/receipt;
it is not treated as pushback against the finding.

## Verification

ITSM commands and outcomes:

```bash
cd itsm-backend && go test ./... -count=1
cd itsm-backend && go build ./...
cd itsm-backend && go test -race ./service -run 'Test(ExecuteAction_ConcurrentClaimsCompleteExactlyOnce|CompleteKafDelegatedTask_RejectsStaleLedgerOwnerBeforeCallback|KafCompletionReceipt_LateFailureCannotRegressSuccess)' -count=1
cd itsm-backend && go test ./service -run 'Test(ReconcileTaskOnlyCompletion_DoesNotReportApplied|ExecuteAction_RealEngineCallbackFailureRecoversWithoutSecondBPMNCompletion|ExecuteAction_NonCompletingFinalizationFailureRollsBackEffectAndRetriesOnce|CompleteKafDelegatedTask_RejectsStaleLedgerOwnerBeforeCallback)' -count=1 -v
cd itsm-backend && go test -tags integration_rls ./database/rls -count=1 -v
```

Results: full Go suite PASS; build PASS; three selected race tests PASS in
1.196s; four focused crash/fencing tests PASS in 0.114s. Deterministic RLS tests
PASS; five PostgreSQL integration tests skip because `RLS_TEST_DSN` is unset.
All nine changed Go files produce no output from `gofmt -l`.

KAF commands and outcomes:

```bash
ENV_FILE=/dev/null DEBUG=true PYTHONPATH=src /home/administrator/actions-runner/_work/kaf/kaf/.venv/bin/python -m pytest tests/test_kaf_delegation_contract.py tests/test_kaf_delegation_pipeline.py tests/test_kaf_delegation_delivery_migration.py -q
ENV_FILE=/dev/null DEBUG=true PYTHONPATH=src /home/administrator/actions-runner/_work/kaf/kaf/.venv/bin/python -m pytest tests/test_migration_dag.py -q
ENV_FILE=/dev/null DEBUG=true PYTHONPATH=src /home/administrator/actions-runner/_work/kaf/kaf/.venv/bin/python -m ruff check alembic/versions/035_kaf_delegation_delivery_leases.py alembic/versions/036_kaf_delivery_completion_replay.py src/acp/models/kaf_delegation_delivery.py src/acp/orchestration/headless_tasks/kaf_delegation_pipeline.py tests/test_kaf_delegation_contract.py tests/test_kaf_delegation_pipeline.py tests/test_kaf_delegation_delivery_migration.py
ENV_FILE=/dev/null DEBUG=true PYTHONPATH=src /home/administrator/actions-runner/_work/kaf/kaf/.venv/bin/python -m ruff format --check alembic/versions/035_kaf_delegation_delivery_leases.py alembic/versions/036_kaf_delivery_completion_replay.py src/acp/models/kaf_delegation_delivery.py src/acp/orchestration/headless_tasks/kaf_delegation_pipeline.py tests/test_kaf_delegation_contract.py tests/test_kaf_delegation_pipeline.py tests/test_kaf_delegation_delivery_migration.py
ENV_FILE=/dev/null DEBUG=true PYTHONPATH=src /home/administrator/actions-runner/_work/kaf/kaf/.venv/bin/python -m alembic heads
```

Results: focused suite `110 passed, 1 skipped in 10.84s`; migration DAG
`2 passed in 4.71s`; Ruff check/format PASS for seven files; Alembic head
`036_kaf_completion_replay`. Targeted compileall also passed.

Both repositories passed `git diff --check`. KAF was clean after its commit.
Protected untracked historical ITSM review files were not modified, staged, or
deleted.

## External Limits And Residual Risk

- Repository-wide KAF `pytest -q` stops during collection because optional
  dependency `sentence_transformers` is unavailable in this environment.
- PostgreSQL RLS/concurrency probes require configured credentials. Their tests
  compile and skip; deterministic policy SQL and real SQLite transaction paths
  pass, but no live PostgreSQL claim is made.
- No live SSLVPN infrastructure, deployment, or production credentials were
  exercised.
- Generic non-KAF `CompleteTask` remains intentionally multi-step. The hardened
  advancement/fencing protocol is the dedicated KAF completion boundary.

## Self-Review

- Re-read each changed production path against all nine findings and the binding
  design; no parallel workflow, fallback, controller business logic, or
  tenant/actor bypass was introduced.
- Verified every owner-sensitive write uses current owner plus unexpired
  `executing` state, and successful receipts cannot transition backward.
- Verified KAF replay uses the persisted typed payload rather than re-running a
  Procedure, and recovery scans it before the remote delegated list.
- Corrected a PostgreSQL portability issue during review by avoiding JSON
  equality in the replay-finalization predicate.
- Rechecked staged scope so user-owned untracked historical reviews remain
  untouched.
