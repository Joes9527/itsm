# KAF Delegation Execution Integrity Acceptance Report

Date: 2026-08-31

## Status

**PASS — stable Dev baseline.** The Live Dev closeout completed on 2026-08-31:
one official SSLVPN Service Request crossed ITSM BPMN, transactional Outbox,
KAF delivery, the governed Microsoft Graph Tool, exact-payload completion
replay, and final permission cleanup. The real PostgreSQL RLS suite completed
with zero skips after two fail-closed connection defects were repaired and
independently reviewed.

This is not production rollout approval. No request was sent to KAF PROD
`10.128.35.195`, and the separate unified Intake design has not begun.

## Full-Implementation Review Addendum

The 2026-08-31 whole-branch review found and remediated the following gaps:

| Finding | Resolution |
| --- | --- |
| KAF delegation reused legacy Gazellio `ITSM_URL` and `ITSM_WEBHOOK_SECRET`. One KAF deployment could not safely connect to both systems. | Delegation now uses dedicated `ITSM_KAF_URL`, `ITSM_KAF_AUTOMATION_TOKEN`, and `ITSM_KAF_WEBHOOK_SECRET`; the legacy lifecycle integration remains on its existing settings. A regression proves a legacy webhook secret cannot authorize a delegation event. |
| `lease_seconds` controlled heartbeat cadence but claim and renewal persisted a hard-coded 300-second lease. | Claim, heartbeat renewal, pre-action renewal, and completion replay now use the same configured TTL. |
| `kaf-context` declared attachment references but always returned an empty list. | ITSM now returns tenant-filtered opaque attachment IDs only. File names, paths, storage URLs, and signed URLs are not exposed. |
| Base KAF test collection imported the optional `sentence-transformers` package through the embedding evaluation CLI. | The optional dependency is loaded only when an embedding evaluation actually runs; its seven CLI tests pass without the embedding extra. |

The existing `acp-backend` Docker container is not acceptance evidence: it is
built from an older image and restarts because its ORM seed does not match the
current `operation_policies` schema. Dev verification must run the current
worktree source against the `kaf-dev` data-plane containers.

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

After the full-implementation review fixes, the delegation, webhook,
migration, and migration-DAG selection completed as follows:

```bash
ENV_FILE=/dev/null DEBUG=true PYTHONPATH=src python -m pytest \
  tests/test_itsm_webhook_auth.py tests/test_itsm_webhooks.py \
  tests/test_kaf_delegation_contract.py tests/test_kaf_delegation_pipeline.py \
  tests/test_kaf_delegation_delivery_migration.py tests/test_migration_dag.py -q
```

Result: `137 passed, 1 skipped`. The optional PostgreSQL probe is the skip.

The local Dev database initially reported revision `033_pending_interaction_uq`.
Running `alembic upgrade head` against `kaf-dev-postgres` applied revisions
034, 035, and 036. Current revision is `036_kaf_completion_replay`, and current
source then started on `127.0.0.1:8001`; `GET /health` returned
`{"status":"ok"}`.

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

The repository-wide KAF `pytest -q` command now collects and executes after the
optional-import fix. It produced `2450 passed, 13 skipped, 1 xfailed`, plus 96
failures and 32 setup errors. The dominant setup error is test-process settings
drift to `localhost:5432` instead of the explicitly supplied Dev DSN; many
remaining failures are outside this branch's delegation files. This is not a
passing full-suite result and must not be represented as one.

Both repositories passed `git diff --check`. Protected untracked historical
ITSM review files were not modified, staged, or removed.

## Residual Limits

- The KAF repository-wide suite is not green because test modules mutate or
  reload global settings and escape the supplied Dev database configuration.
  That repository-level isolation cleanup is separate from delegation behavior
  but remains a release-evidence gap.
- Production rollout requires provisioning the three dedicated `ITSM_KAF_*`
  settings and must not reuse legacy Gazellio credentials.
- The generic non-KAF `CompleteTask` API remains multi-step by design; the
  exact-scope advancement and owner-fenced recovery guarantees apply to the
  dedicated KAF completion path.

The historical RLS and live-path gaps are superseded by the live Graph path and
zero-skip PostgreSQL results below.

## Live Dev Closeout Addendum — 2026-08-31

### Environment and revisions

- Execution window: `2026-08-31T20:25:21+08:00` through
  `2026-08-31T20:54:31+08:00` (Asia/Shanghai).
- ITSM revision: `fa57719288bdf308e300196b9497daae9e219d4d`.
- KAF revision: `40dca9afa44fabb74d0609f7629ecc6de8a2049c`.
- ITSM Dev reached post-schema migration `019_kaf_execution_integrity_rls`;
  KAF Dev reached Alembic `036_kaf_completion_replay`.
- Current-source listeners were verified at `127.0.0.1:8090` for ITSM and
  `127.0.0.1:8001` for KAF. The pre-existing KAF listener on port 8000 was not
  replaced or used as acceptance evidence.
- The `it-support` workspace has exactly one retained mapping to ITSM tenant
  `1`. The Dev RLS probe role `itsm_app` is `NOLOGIN`, non-superuser, and does
  not bypass RLS.

### Automated verification

- ITSM deterministic Task 8 breakers: 19 tests passed across service,
  controller, and Service Request handler scopes. They cover callback recovery,
  idempotency, task scope, tenant rejection, list audit, and attachment
  minimization.
- KAF deterministic lease/recovery/replay breakers: 7 passed in 16.97 seconds.
- Current KAF delegation/webhook/migration scope: 139 passed and 1 skipped in
  34.18 seconds. The one skip is a separate optional concurrency probe that
  requires a test-process database; the real KAF Dev PostgreSQL delivery was
  exercised by the live path below.
- Real ITSM PostgreSQL RLS: 15 passed, 0 skipped in 0.244 seconds. Tenant `1`
  saw 7 `changes` rows, tenant `999` saw 0, both execution-integrity tables
  rejected cross-tenant rows, missing tenant failed closed, and `DISCARD ALL`
  cleared pooled session state.
- The RLS run first exposed invalid parameter binding in `SET SESSION`; commit
  `821388ef` replaced it with session-scoped, parameterized `set_config`.
  Independent review then exposed dirty-connection reuse on cleanup errors;
  commit `fa577192` invalidates the physical connection through
  `driver.ErrBadConn`. Tests prove the old connection is closed and the next
  borrow uses a new physical connection. Final independent review approved the
  remediation with no findings.
- Final ITSM `go test ./... -count=1` passed 53 test-bearing packages, with 157
  packages reporting no test files, in 85 seconds. `go build ./...` passed in
  10 seconds.
- The recorded repository-wide KAF run remains nonzero: 2450 passed, 96 failed,
  13 skipped, 1 xfailed, and 32 setup errors. Those failures are outside the
  delegation closeout scope and are dominated by global test-settings/database
  isolation; this report does not call that suite green.

### Live Service Request and replay evidence

- Official Service Request `35` created WorkItem `18` and process `144`
  (`PI-sslvpn_approval_flow-1788179201869909767`). The process completed at
  `EndEvent_1`, version `6`.
- L1 task `199` and L2 task `200` completed before KAF task
  `TASK-3a9e3b28-e4c1-4677-bf10-e6472649c252` (database task `201`) entered the
  delegated wait state. Correlation ID:
  `cafe4a9a-54fb-4d5c-95d0-2b58422cfe91`.
- Task context exposed one opaque attachment reference, ID `2`, and no file
  name, storage path, or URL. An ordinary subject received HTTP 403 and a
  cross-tenant subject received HTTP 404.
- Authoritative records remained cardinality one: Outbox `1` is `published`,
  action ledger `1` is `applied`, completion receipt `1` is
  `callback_succeeded`, KAF delivery
  `35b9c18b-837e-456f-ad91-a92a7c57e39d` is `completed`, and governed external
  action `30c89247-6bf5-4154-a9d2-28d6518b7992` is `succeeded`.
- ITSM recorded one successful completion audit, three successful context-read
  audits, and one creation audit. KAF recorded one Graph grant action and one
  corresponding sanitized audit row; stored audit evidence contained zero
  secret markers.
- Membership lifecycle for user object
  `8e60c8c8-9687-416a-968d-81d978eba0eb` and group object
  `b7c7f066-3042-4a11-9e36-2ea80b979ae3` was
  `member=false → member=true → member=true → member=false → member=false`
  (baseline, grant, replay, cleanup, final readback).
- Replaying the exact persisted completion payload returned `already_applied`.
  Ledger, receipt, completion-audit, and Graph external-action counts remained
  one, and replay did not cause a second membership transition.

### Breakers and residual status

- L2 approval while KAF was stopped committed the decision, delegated task,
  creation audit, and pending Outbox atomically. Restart recovery published the
  same event and converged without manual process creation or direct business
  table repair.
- Callback failure, duplicate webhook, concurrent claim, lease theft,
  stale-owner finalization, remote-applied crash recovery, task scope, tenant
  isolation, and replay-only behavior all passed their deterministic breakers.
- Live diagnostics found two pre-existing API inconsistencies: lower-camel
  process query keys are not bound while exported Go field names are, and the
  process-variable endpoint accepts the numeric row ID rather than the engine
  process key. They did not weaken the accepted delegation guarantees and
  remain separate contract-hardening work.
- Final Azure AD state is `member=false`. No cleanup action remains pending.

**Closeout verdict: PASS.** This establishes a reproducible, audited Dev
baseline for KAF delegation execution integrity. It does not authorize KAF PROD
deployment. Unified Intake remains intentionally deferred to a separate
brainstorm and design phase.
