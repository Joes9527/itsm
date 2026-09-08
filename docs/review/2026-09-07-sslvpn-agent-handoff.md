# SSLVPN coding agent handoff

**Status:** accepted handoff; implementation incomplete, paused at C1 review on 2026-09-07. This handoff does not certify release readiness.

## Assignment and objective

Continue the accepted plan to complete: KAF Web authenticated requester → Unified Intake → two sequential BPMN approvals → governed KAF Graph group grant → queried membership verification → atomic ITSM result/state/audit/completion receipt → KAF display. The next implementation task is **C1 fix round 1**, after completing the interrupted C1 independent review. Do not restart A1–A6 or the old Codex session investigation.

The user requested a pause after interruption, then requested this handoff and local commits. The current agent is not resuming implementation. When the user assigns this handoff with an explicit instruction to continue, that is the receiving agent's authorization to resume the accepted scope. It is not authorization to push, merge, deploy to a shared environment, or use production credentials.

## Worktrees and exact source

| Repository | Existing worktree | Branch | Last implementation commit |
| --- | --- | --- | --- |
| ITSM | `/home/administrator/project/itsm/.worktrees/sslvpn-unified-intake` | `codex/feat/sslvpn-unified-intake` | `37f6f1e48fc437fddd27565df9808f222b347e0f` |
| KAF | `/home/administrator/.worktrees/kaf-sslvpn-unified-intake` | `codex/feat/sslvpn-unified-intake` | `c2eeb496438b46543e441ef9861826786066f595` |

This handoff and the latest verification report are committed after the ITSM implementation commit above. Obtain their exact HEAD using `git rev-parse HEAD`; no source changes are hidden in this documentation commit. KAF was already clean and committed, so no empty commit is required.

ITSM original main baseline is `5b2dd2c62358fd6e7f07d1886a2c67f750d8422f`; main has not been changed by this task. KAF original baseline is `d07a178`. Preserve other worktrees and the original KAF runner checkout's user-owned `uv.lock` changes. No push/merge/deployment or real Graph grant has been performed in this task.

## Read first

1. ITSM `AGENTS.md`, `docs/agent-engineering-governance.md`, relevant `docs/DEVELOPMENT_GUIDE.md`, and KAF `AGENTS.md`.
2. [Accepted total plan](../superpowers/plans/2026-09-05-sslvpn-end-to-end-implementation.md) and [accepted design](../superpowers/specs/2026-09-05-sslvpn-kaf-intake-end-to-end-design.md).
3. [Current verification report](2026-09-05-sslvpn-end-to-end-verification-report.md).
4. The plan-specific local artifact directory below, especially `progress.md`, the C1 brief/report, and both review diffs.

ITSM skills are at `/home/administrator/project/itsm/.agents/skills`, not under the worktree. Apply `subagent-driven-development`, debugging/TDD and verification skills as relevant. One source implementation writer at a time; independent review for domain, authorization, workflow and migration changes. Do not dispatch an entire completed phase again. Agent identities from the interrupted session are not assumed resumable.

Artifact directory (`ART` in the commands below):

```text
/home/administrator/project/itsm/.worktrees/sslvpn-unified-intake/.superpowers/sdd/2026-09-05-sslvpn-end-to-end-implementation
```

This directory is git-ignored. It remains available on this machine but is not included by a Git clone. Preserve it; do not run `git clean -fdx`. For a handoff to another machine, transfer these local evidence artifacts separately, excluding secrets/runtime environment files. Missing logs do not convert prior results into passes; recreate necessary isolated evidence if unavailable.

## Progress and remaining order

| Phase | State |
| --- | --- |
| A1–A3 | Foundation and unified creation/numbering/transaction implementation landed; final whole-entry acceptance remains A7. |
| A4 | Entry adapters, frontend creation, Catalog current-session reader and native/MSP session projection implemented and independently reviewed. |
| A5 | General Catalog publication/version/preflight core reviewed. External-grant combinations were handed to C1. |
| A6 | Complete with independent review closure: main `1dca1558`, fix `a36fb02c`, documentation `703ec164`. Do not reopen as a new task. |
| C1 | Implementation committed in both repositories; independent review interrupted and blocking findings remain. **Not complete.** |
| A7 | Not started; requires C1 closure. |
| B1–B4 | User Intake client, confirmation persistence/card integration and live acceptance not implemented. C1 added only KAF result contracts. |
| C2–C4 | Actual governed grant, atomic completion consumption, recovery and external/browser acceptance remain. |

Continue in this order: **finish C1 review → C1 fixes/re-review → A7 → B1–B4 → C2–C4 → final whole-branch review and delivery**. Do not stop after the next phase or equate a stage test pass with whole-task completion.

## C1 implementation boundary

C1 adds three professional tables through migration `030_catalog_access_policy_result` (23 registered migrations total):

- `catalog_access_policies`: one policy per Catalog; typed Graph/external workspace/group, finite duration field/options.
- `service_request_access_snapshots`: one immutable requested-terms record per WorkItem; policy FK/version, trusted subject, target and selected duration. It is not approval evidence; BPMN remains the approval owner.
- `service_request_access_results`: one verified result per WorkItem, linked to delegated ProcessTask; baseline/outcome/verification/evidence and nullable managed expiry. C2 will supply its production writer in the existing completion transaction.

Existing registered KAF task handler supplies publication validation; no second capability/Procedure registry. A6 reads fulfillment through SR/workflow/result owners. KAF `ApprovedAccessSnapshot`/verified result contracts are strict. `already_present` means verification of existing membership, not a new managed grant; its managed expiry is null. Automatic revocation is out of scope.

Read `entry-access-policy-result-brief.md`, `takeover-C1-contract-notes.md`, `takeover-C2-source-notes.md`, `entry-access-policy-result-report.md`, and `entry-access-policy-result-review-brief.md` in ART.

C1 review bases/packages:

- ITSM `703ec16419f3432dd8271d7abb0e7820b7dea6ea..37f6f1e48fc437fddd27565df9808f222b347e0f`; `review-703ec164..37f6f1e4.diff` (735687 bytes).
- KAF `d07a178..c2eeb496438b46543e441ef9861826786066f595`; `review-kaf-d07a178c..c2eeb496.diff` (10446 bytes).
- **`entry-access-policy-result-review.md` was not written before interruption. There is no final independent approval.** Complete the review and retain the two received findings below before starting the fix round.

## Blocking findings to preserve

### F1 — CHECK constraints disappear on repeated initialization (confirmed P1)

Parent exact-source PG17 validation used `37f6f1e4`. In both an existing-data upgrade and an empty database:

1. First `bootstrap.InitializeStorage` passes: migration23/030, original data byte-identical, all three tables each have one CHECK plus expected RLS/foreign keys/unique indexes/triggers.
2. Second `InitializeStorage` leaves all three tables with **zero CHECK constraints**. RLS/FORCE/policies and foreign keys remain.

The independent reviewer confirmed the cause: CHECKs exist only in migration SQL, not Ent schema declarations. Each initialization runs `Schema.Create`; reconciliation drops the SQL-only CHECKs, while the migration ledger skips already-applied030. Fix schema/migration ownership so constraints survive synchronization, including fresh creation and existing030 installations. Do not disable schema verification or weaken the assertion.

Evidence: `entry-c1-runtime-results-37f6f1e4.json` (build0/upgrade1/empty1), `entry-c1-{upgrade,empty}-37f6f1e4-runtime.log`, `runtime-storage-harness/post_c1_check/main.go`. Immutable source snapshot: `/tmp/sslvpn-post-c1-37f6f1e4`.

The original failed databases were `sslvpn_upgrade_c1` and `sslvpn_empty_c1`; preserve them as recovery cases if still present. Before interruption a new pre-C1 fixture **completed successfully**: `entry-pre-c1-fix1-runtime-results.json` build0/fixture0, exact old source `703ec164`, database `sslvpn_upgrade_c1_fix1`, manifest `pre-c1-fix1-manifest.json`. This is only a preparation fixture, not a passing fix test.

After the fix, verify fresh initialization twice, upgrade from029 twice with original data preservation, and recovery of existing030 databases missing CHECKs. The old runner guards against overwriting evidence/databases: adapt new labels/owned fixtures rather than rerunning it blindly. Verify all three unique relation indexes as well as CHECKs/RLS.

### F2 — One inaccessible task can fail the entire recovery list (review finding awaiting formal report)

At reviewed C1 HEAD, `KafDelegationService.GetTaskContext` returns a new `ReadApprovedAccess` error directly (`service/kaf_delegation_service.go`, approximately274–276). `ListDelegatedTaskPage` calls this for each record (approximately339–342). A revoked mapping, suspended workflow or failed ledger for one access task can therefore fail the page. KAF's context client aggregates pages before returning; `recover_delegated_tasks` exits the recovery round on that failure.

Verify and fix this concrete caller regression: keep individual access fail-closed and visibly blocked, while healthy same-tenant tasks can still be enumerated/recovered. Do not silently skip required work or hide infrastructure outages. Add a mixed blocked/healthy task regression and retain current authorization, task/lease and tenant boundaries. The review has not yet chosen a final API design; inspect actual callers rather than blindly implementing a guessed wrapper.

## Existing evidence and its limits

Historical results, not rerun during this handoff:

- A6 independent fix review approved; real identity/nonce/RLS and HTTP option/error contract evidence saved. Opaque Catalog option keys are generated only by ITSM FieldDefinition owner; clients return keys verbatim. Mapping and permissions remain current even for replay.
- C1 final six owning Go packages passed. Broader service/BPMN/controller/database and corrected `tests/contract` checks passed; one combined command had an incorrect plural path and overall exit1, recorded in the implementation report.
- C1 default-tag full Go compile passed before the final one-line multi-delegation correction; that correction was subsequently compiled/exercised by affected owner/broader package runs. This was compile-only, not a full backend test execution.
- C1 PG16 actual030 twice, runtime nonowner RLS, uniqueness, immutable records, cross-owner rejection and rollback passed; cleanup0. It does **not** substitute for the failed PG17 full initialization test above.
- C1 application SSLVPN E2E actually ran two approvals to KAF delegated, exit0. It used SQLite and is neither live-browser nor Graph proof.
- Frontend26 API tests and type check passed. Browser fixture was updated but not run.
- KAF final2588passed/1failed/12skipped/1xfailed/45warnings, exit1. The sole failure is the pre-existing hardcoded032head assertion; no full-suite-green claim.17 new result-contract tests passed.

KAF baseline/runtime issues reserved for B4:

1. `tests/test_external_action_fk.py:79` requires032 as sole head, but current head is036. Replace with a meaningful migration-chain check; do not change production history to appease the test.
2. Loading repository `docker/infra/postgres/schema.sql` into a separate empty PG17 database succeeds, then actual Alembic upgrade fails at005: SQL already contains `users.azure_oid`, while migration renames absent `keycloak_sub`. Evidence: `kaf-migration-baseline-results.json` and logs, immutable `/tmp/sslvpn-kaf-migration-d07a178`. No forced stamp/bypass was used. Establish supported fresh/upgrade bootstrap before C4.

Other deferred review items are in the ledger/A7 brief, notably A5 display-field edits surviving a later activation failure,028 exact RLS policy-expression verification, and frontend/KAF test-warning and remaining live-browser coverage. Do not lose them or broaden a C1 fix into unrelated refactoring.

## Isolated environments and safe recovery

Recheck container identity, loopback bindings, current database contents and process ownership after any restart. The names/ports below describe the task-owned environment, not proof that data survived:

| Resource | Name / binding | Use |
| --- | --- | --- |
| PG16 | `codex-sslvpn-intake-pg-20260905`,127.0.0.1:36430 | ITSM isolated integration tests. `run-owned-postgres-test.py` temporarily forwards36444 and closes it. No pgvector/full app storage bootstrap. |
| PG17 | `codex-sslvpn-runtime-pg17-20260905`,127.0.0.1:36446 | pgvector full InitializeStorage/upgrade evidence, plus separately owned KAF fixture databases. tmpfs-backed; data may be lost across a container/host restart. |
| Redis | `codex-sslvpn-intake-redis-20260905`,127.0.0.1:36445 | DB0 ITSM, DB1 KAF. Nonpersistent. |

No use of shared/default localhost5432 or6379. Coordinate one writer per database. Preserve evidence and check live transactions before retrying a timeout. No unchanged-suite polling/restart loops.

KAF unit helper: `python3 ART/run-kaf-owned-tests.py UNIQUE_LABEL [pytest args]`. Read `takeover-KAF-test-environment-notes.md` first. It uses isolated `sslvpn_kaf_unit_baseline`, an empty ENV_FILE, mock backend, development schema-management flag, and synthetic it-support/kco workspaces. This ORM fixture is **not** Alembic evidence. The helper records actual exits and prevents log overwrites. Existing KAF worktree venv has declared pypinyin0.55.0 installed; avoid unnecessary dependency resolution. The project uv index points at prohibited production host10.128.35.195; use existing dependencies/offline mode or explicitly configured public PyPI, not that host.

C4 external actions remain gated by actual readiness and [the dedicated fixture](../testing/kaf-delegation-release-closeout-fixture.md). Production10.128.35.195 and LDAP substitution are prohibited. Candidate Dev configuration was located at `/mnt/d/SynologyDrive/kerry/KAF_Migration_Pack/kaf-main/.env`; only key presence was inspected. Its backend is not graph and VPN_USERS_GROUP_ID is absent. Never source it wholesale or expose secret values. Validate Dev provenance and the exact approved fixture before any external query/mutation, perform recorded cleanup, and do not claim automatic expiry revocation.

## Next-agent execution instructions

1. Read the authorities and this handoff; inspect both worktrees and the latest ledger. Preserve local artifacts and unrelated work.
2. Complete C1's interrupted independent review, carrying F1/F2. Then perform fix round1 with one source writer and meaningful RED/GREEN regressions; preserve the recorded scope rulings.
3. Run fixed exact-source PG17 fresh/upgrade/recovery checks. Independently re-review the fix, record actual exits and source hashes, and close C1 only when blocking findings are resolved.
4. Continue A7 → B1–B4 → C2–C4 using the prepared `entry-A7-*`, `entry-KAF-B*`, `entry-C3-*` and `entry-C4-*` briefs in ART and the accepted plans. Do not restart completed tasks or claim no-op/skipped integrations passed.
5. Finish the whole-branch review, reconcile all ledger `Ruling:` decisions and deferred findings before artifact cleanup, and report remaining limitations honestly. Push/merge/shared deployment is a separate action requiring the user's authorization.

The old session loop investigation is closed; [its diagnosis](2026-09-05-codex-session-takeover-diagnosis.md) is historical context only. Resume from the saved C1 state, not the beginning.
