# KAF Delegation Execution Integrity Acceptance Report

Date: 2026-08-30

## Status

Task 5 fix-round acceptance passed in the requested linked worktrees. This is
automated local evidence only; no external SSLVPN deployment, network change,
or production credential operation was executed.

KAF commits in scope:

- `cf01648fb233c621f4785820a3e4297afdc69da4` - redact credential-bearing
  non-hierarchical URI suffixes without misclassifying URI syntax.
- `1ca123f831f2101e3d8876d8508953fe2dad2d3f` - initial delivery cardinality
  acceptance coverage.
- `903a69c2932fad667746928ca3a647f10f1149f1` - real SQLite SQLAlchemy delivery
  identity, interleaving, reclaim, and lease-owner-fencing coverage.

The ITSM fix-round commit is reported in the Task 5 scratch handoff because
this authoritative report is part of that commit.

## Acceptance Matrix

| Requirement | Production-boundary evidence and exact outcome |
| --- | --- |
| SSLVPN applied/replay cardinality | `TestSSLVPNKafDelegation_OneAppliedActionAdvancesBPMNOnce` passed. It proves first `applied`, exact replay `already_applied`, one completed BPMN task/process, one applied ledger, one successful receipt, one audit, and no legacy action-result task variable. |
| Callback failure recovery | `TestExecuteAction_RealEngineCallbackFailureRecoversWithoutSecondBPMNCompletion` passed against `CustomProcessEngine` and a registered fail-once callback handler. It proves one completion write/BPMN advancement, one persisted callback domain/timeline comment, one audit, one eventually successful receipt, and no callback or completion on exact replay. |
| Completed task with failed receipt | `TestReconcileCompletedTaskWithoutSuccessfulReceipt_DoesNotCompleteBPMNAgain` passed, proving callback-only reconciliation and no second completion. |
| Duplicate webhook identity | `test_sqlite_duplicate_webhooks_commit_one_unique_identity_and_one_execution` passed with two real SQLite `AsyncSession`s. Coordinated stale reads exercise the production unique index and commit conflict, leaving one persisted completed identity and one execution. |
| Webhook/recovery interleaving | `test_sqlite_webhook_and_recovery_interleaving_preserves_live_lease_owner` passed through the production pipeline/session factory, proving duplicate webhook plus recovery cannot replace a live owner and leave one completed persisted delivery/execution. |
| Expired KAF lease | `test_sqlite_recovery_reclaims_expired_running_delivery_and_fences_stale_owner` passed against real SQLite conditional updates and commits, proving one reclaimed identity, a new owner, rejected stale-owner finalization, and final `completed` state. |
| Expired/live ITSM ledger lease | `TestExecuteAction_RetryAfterAppliedFinalizationFailureCreatesOneAudit` and `TestExecuteAction_RejectsActiveLeaseWithoutCallingEngine` passed. Reclaim creates one audit; a live lease creates no engine, task, timeline, or audit effect. |
| Canonical mismatch | `TestExecuteAction_RejectsSameScopeWithDifferentKey` passed, proving no second ledger, engine, timeline, audit, or task effect. |
| Concurrent action cardinality | `TestExecuteAction_ConcurrentClaimsCompleteExactlyOnce` passed under `go test -race`, proving one ledger, audit, timeline comment, task completion, and engine call. |
| Tenant isolation | `TestKafContext_RejectsDifferentTenantAutomationActor`, `TestKafContext_RejectsValidKafActorWithDifferentRequestTenant`, `TestKafAction_RejectsValidKafActorWithDifferentRequestTenant`, `TestKafAction_IdempotentReplayRejectsValidKafActorWithDifferentRequestTenant`, and `TestAuthorizeTaskActor_KafDelegate_RejectsCrossTenantActor` all passed; cross-tenant context, action, replay, and engine authorization fail closed. |
| Authentication/actor scope | `TestAuthorizeTaskActor_KafDelegate_RejectsNonKafAutomationRole` and `TestAuthorizeTaskActor_KafDelegate_RejectsNoActorContext` passed; delegated completion rejects an unauthorized role and absent actor context. |
| Legacy state absent | The SSLVPN test rejects `task_variables["kaf_action_results"]`; the production Go source search below found no legacy identifier. |
| Persisted URI redaction | The focused persisted-`last_error` matrix passed for `mailto:`, `urn:`, `data:`, `s3:`, and generic RFC-style schemes while preserving sensitive `key=value` and `key:value` redaction. |

## Focused Red/Green Evidence

The SQLite identity test was mutation-checked by temporarily disabling the
production model's unique identity flag. The selected test failed because two
rows persisted instead of one (`1 failed, 101 deselected`); restoring the
constraint returned the three focused SQLite cases to `3 passed, 99 deselected`.

The expired-reclaim test was mutation-checked by temporarily removing
`lease_owner == lease_owner` from production finalization. It failed at the
stale owner's finalization assertion (`1 failed, 101 deselected`); restoring
owner fencing returned the focused suite to green. Neither mutation remains in
the committed tree.

```bash
ENV_FILE=/dev/null DEBUG=true PYTHONPATH=src /home/administrator/actions-runner/_work/kaf/kaf/.venv/bin/python -m pytest tests/test_kaf_delegation_pipeline.py -q -k 'sqlite_duplicate_webhooks or sqlite_webhook_and_recovery_interleaving or sqlite_recovery_reclaims_expired_running' -vv
```

Result: `3 passed, 99 deselected in 5.85s`.

```bash
ENV_FILE=/dev/null DEBUG=true PYTHONPATH=src /home/administrator/actions-runner/_work/kaf/kaf/.venv/bin/python -m pytest tests/test_kaf_delegation_pipeline.py -q -k 'non_hierarchical_uri_suffixes or preserves_real_sensitive_assignment_boundaries or ambiguous_delimiter_suffixes or bounded_text_redaction'
```

Result: `46 passed, 56 deselected in 4.96s`.

## ITSM Verification

Worktree: `/home/administrator/project/itsm/.worktrees/kaf-delegation-transactional-delivery`

```bash
cd itsm-backend && go test ./handlers/service_request ./service -run 'TestSSLVPNKafDelegation_OneAppliedActionAdvancesBPMNOnce' -count=1 -v
```

Result: PASS; one handler test passed and service had no matching test.

```bash
cd itsm-backend && go test ./service ./controller ./handlers/service_request -run 'Test(Kaf|SSLVPN)' -count=1 -v
```

Result: PASS; 28 tests passed: 11 service, 12 controller, and 5 handler tests.

```bash
cd itsm-backend && go test ./service -run 'Test(ExecuteAction_|ReconcileCompletedTask|CompleteKafDelegatedTask)' -count=1 -v
```

Result: PASS; 9 tests passed, including the real-engine fail-once callback test.

```bash
cd itsm-backend && go test ./controller ./service -run 'Test(KafContext_Rejects(DifferentTenantAutomationActor|ValidKafActorWithDifferentRequestTenant)|KafAction_(RejectsValidKafActorWithDifferentRequestTenant|IdempotentReplayRejectsValidKafActorWithDifferentRequestTenant)|AuthorizeTaskActor_KafDelegate_Rejects(NonKafAutomationRole|NoActorContext|CrossTenantActor))' -count=1 -v
```

Result: PASS; all 7 named tenant/auth actor-scope tests passed.

```bash
cd itsm-backend && go test -race ./service -run 'TestExecuteAction_ConcurrentClaimsCompleteExactlyOnce' -count=1 -v
```

Result: PASS; one race-enabled test passed.

```bash
cd itsm-backend && go build ./...
```

Result: PASS, exit code 0.

```bash
rg -n 'kaf_action_results|kafActionResult|putKafActionResult' itsm-backend --glob '*.go' --glob '!*_test.go'
```

Result: no matches; expected `rg` exit code 1.

## KAF Verification

Worktree: `/mnt/d/SynologyDrive/kerry/KAF_Migration_Pack/kaf-worktrees/kaf-delegation-transactional-delivery`

```bash
ENV_FILE=/dev/null DEBUG=true PYTHONPATH=src /home/administrator/actions-runner/_work/kaf/kaf/.venv/bin/python -m pytest tests/test_kaf_delegation_contract.py tests/test_kaf_delegation_pipeline.py -q
```

Result: `104 passed, 1 skipped in 10.30s`. The pre-existing optional
PostgreSQL concurrency probe skipped after configured credentials were rejected;
all real SQLite SQLAlchemy acceptance tests ran.

```bash
ENV_FILE=/dev/null DEBUG=true PYTHONPATH=src /home/administrator/actions-runner/_work/kaf/kaf/.venv/bin/python -m ruff check src/acp/orchestration/headless_tasks/kaf_delegation_pipeline.py src/acp/models/kaf_delegation_delivery.py tests/test_kaf_delegation_pipeline.py
ENV_FILE=/dev/null DEBUG=true PYTHONPATH=src /home/administrator/actions-runner/_work/kaf/kaf/.venv/bin/python -m ruff format --check src/acp/orchestration/headless_tasks/kaf_delegation_pipeline.py src/acp/models/kaf_delegation_delivery.py tests/test_kaf_delegation_pipeline.py
```

Results: `All checks passed!`; all 3 files already formatted.

```bash
ENV_FILE=/dev/null DEBUG=true PYTHONPATH=src /home/administrator/actions-runner/_work/kaf/kaf/.venv/bin/python -m compileall -q src/acp/orchestration/headless_tasks/kaf_delegation_pipeline.py tests/test_kaf_delegation_pipeline.py
ENV_FILE=/dev/null DEBUG=true PYTHONPATH=src /home/administrator/actions-runner/_work/kaf/kaf/.venv/bin/python -m pytest tests/test_migration_dag.py -q
ENV_FILE=/dev/null DEBUG=true PYTHONPATH=src /home/administrator/actions-runner/_work/kaf/kaf/.venv/bin/python -m alembic heads
```

Results: compile PASS; migration DAG `2 passed in 7.42s`; head is
`035_kaf_delivery_leases`.

Both repositories passed `git diff --check` before commit. The KAF worktree was
clean after commit. Protected historical untracked ITSM review files were not
modified, staged, or removed.

## Residual Scope

- No live SSLVPN infrastructure or external deployment was exercised.
- The optional PostgreSQL concurrency probe remains environment-blocked by the
  configured test credentials; real SQLite production SQLAlchemy boundaries
  provide the required deterministic acceptance evidence.
