# KAF Delegation Execution Integrity Evidence

Date: 2026-08-30

## Outcome

Task 5 acceptance passed across the ITSM and KAF linked worktrees. The SSLVPN
completion returns `applied` on first execution and `already_applied` on
replay, with one completed BPMN task, one completed process instance, one
applied action ledger, one successful completion receipt, and one action audit.
No production `kaf_action_results`, `kafActionResult`, or
`putKafActionResult` state remains.

The carried KAF security finding is fixed. Persisted delivery errors redact
credential suffixes containing non-hierarchical and generic RFC-style URI
schemes after delimiters while preserving ordinary sensitive `key=value` and
`key:value` redaction.

## Acceptance Evidence

| Requirement | Automated evidence |
| --- | --- |
| SSLVPN applied then replay | `TestSSLVPNKafDelegation_OneAppliedActionAdvancesBPMNOnce` asserts `applied`, `already_applied`, and one task, process, ledger, receipt, and audit. |
| Duplicate webhook and recovery interleaving | `test_webhook_and_recovery_interleaving_keeps_one_active_execution` holds one execution open while duplicate webhook and recovery run, then asserts one delivery, execution, ITSM action, and enqueue. |
| Expired KAF lease | `test_recovery_reclaims_expired_delivery_without_new_event_identity` asserts the existing delivery is completed and exactly one ITSM action is sent. |
| Live ITSM ledger lease | `TestExecuteAction_RejectsActiveLeaseWithoutCallingEngine` asserts one executing ledger, a delegated task, and zero engine, timeline, or audit effects. |
| Forced callback failure and reconciliation | `TestExecuteAction_CallbackFailureRecoversWithoutSecondBPMNCompletion` asserts one BPMN completion write, one successful receipt, one callback timeline comment, and one audit after recovery. |
| Completed task with failed receipt | `TestReconcileCompletedTaskWithoutSuccessfulReceipt_DoesNotCompleteBPMNAgain` asserts callback-only recovery and zero second task completion writes. |
| Canonical key mismatch | `TestExecuteAction_RejectsSameScopeWithDifferentKey` asserts rejection with one original ledger, engine call, completed task, timeline comment, and audit. |
| Concurrent same-scope execution | `TestExecuteAction_ConcurrentClaimsCompleteExactlyOnce` asserts one applied result, one in-progress loser, one replay result, and one ledger, completed task, timeline comment, and audit. |
| Legacy mutable state absent | SSLVPN persistence asserts no `task_variables["kaf_action_results"]`; a production-source search returned no legacy identifiers. |
| Persisted URI credential redaction | `test_pipeline_redacts_non_hierarchical_uri_suffixes_from_persisted_errors` covers `mailto:`, `urn:`, `data:`, `s3:`, and generic schemes across five delimiters. The assignment-boundary test covers both `=` and `:`. |

## Commands And Results

ITSM worktree:
`/home/administrator/project/itsm/.worktrees/kaf-delegation-transactional-delivery`

1. Targeted SSLVPN acceptance:

   `cd itsm-backend && go test ./handlers/service_request ./service -run 'TestSSLVPNKafDelegation_OneAppliedActionAdvancesBPMNOnce' -v`

   Result: PASS. One selected handler test passed; the service package had no
   matching test. The initial red run failed because the fixture refreshed
   delegated-only context before replay; reusing the original immutable action
   request made the acceptance path green.

2. Exact focused ITSM suite from the brief:

   `cd itsm-backend && go test ./service ./controller ./handlers/service_request -run 'Test(Kaf|SSLVPN)' -v`

   Result: PASS, 28 tests total: 11 service, 12 controller, and 5 SSLVPN handler
   tests.

3. Explicit execution-integrity service suite:

   `cd itsm-backend && go test ./service -run 'Test(ExecuteAction_|ReconcileCompletedTask|CompleteKafDelegatedTask)' -v`

   Result: PASS, 9 tests.

4. Exact build from the brief:

   `cd itsm-backend && go build ./...`

   Result: PASS.

5. Legacy production-state search:

   `rg -n 'kaf_action_results|kafActionResult|putKafActionResult' itsm-backend --glob '!**/*_test.go' || true`

   Result: PASS, no matches.

KAF worktree:
`/mnt/d/SynologyDrive/kerry/KAF_Migration_Pack/kaf-worktrees/kaf-delegation-transactional-delivery`

1. Redaction mutation check:

   `ENV_FILE=/dev/null DEBUG=true PYTHONPATH=src /home/administrator/actions-runner/_work/kaf/kaf/.venv/bin/python -m pytest tests/test_kaf_delegation_pipeline.py -k 'non_hierarchical_uri_suffixes or preserves_real_sensitive_assignment_boundaries' -q`

   Result with the old URI boundary predicate temporarily restored: expected
   FAIL, 25 failed and 7 passed. Result after restoring the fix: PASS, 32
   passed and 66 deselected.

2. Exact KAF suite from the brief:

   `ENV_FILE=/dev/null DEBUG=true PYTHONPATH=src /home/administrator/actions-runner/_work/kaf/kaf/.venv/bin/python -m pytest tests/test_kaf_delegation_contract.py tests/test_kaf_delegation_pipeline.py -q`

   Result: PASS, 101 passed and 1 skipped in 8.26 seconds. The skip is
   `test_recovery_auth_transition_preserves_a_concurrently_running_real_delivery`,
   which intentionally requires a configured external test database; the
   SQLite concurrency and lease-fencing tests ran.

3. Repository checks:

   `git diff --check`

   Result: PASS in both repositories.

## Scope

No live SSLVPN infrastructure or deployment credential check was executed;
those are explicitly out of scope in the design. The environment-dependent KAF
real-session database regression was skipped as described above. User-owned
untracked historical ITSM review files were not modified, staged, or deleted.
