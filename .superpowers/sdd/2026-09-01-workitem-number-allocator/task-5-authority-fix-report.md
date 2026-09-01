# P1-A Task 5 whole-branch authority fix report

## Status

Implemented the destructive development-only Unified Work Item cutover on baseline `ec36db69d4f5d0563343431c79b6cbe4e97aa528`. The delivery is the commit containing this report; its SHA is reported to the controller after commit creation.

The separately reviewed generic Ticket transaction finding is intentionally not implemented here. It is owned by commit `9542d2f0`; `repository/ticket/repository_impl.go`, `repository/ticket/repository_test.go`, and `handlers/service_request/service.go` remain byte-for-byte at this branch baseline.

## Implementation

- Removed every WorkItem-owned shared field from Incident, Problem, and Change Ent schemas and regenerated Ent. The extensions retain only professional fields and a mandatory, unique `work_item_id`.
- Made WorkItem authoritative for tenant, requester/opener, assignee, title, description, status, priority, category, shared version, lifecycle/public timestamps, and soft deletion across services, repositories, DTO projection, filtering, ordering, statistics, dashboard/search, BPMN handlers, and raw SQL investigation paths.
- Bound Ticket category projection to the existing `category_id` field. Incident parent/child category strings and Problem category strings are API projections over the structured WorkItem relation and fail closed on an unknown or cross-tenant category.
- Deleted `changes.related_tickets` physically and removed generated APIs. `relatedTickets` remains only an API projection over tenant-scoped `WorkItemRelation` rows.
- Removed the obsolete `backfill_legacy_pending_changes` command, service method, tests, and comments. No compatibility read/write or historical backfill remains.
- Replaced both fire-and-forget Incident creation goroutines with bounded deterministic post-commit execution. Nil dependencies return immediately; real rules/workflow calls complete before the service returns, and errors remain observable in logs.
- Added WorkItem-derived global soft-delete interception for Change, matching Incident and Problem.
- Expanded migration 022 in place (canonical registry plus retained apply/reset/verify assets). It removes the complete shared-column set, rejects duplicate extension ownership, enforces exact ready/valid one-column unique indexes, verifies record-class links, and removes `changes.related_tickets`. There is no migration 023 and no data backfill.
- Updated retained seed, RLS pilot/e2e, and index optimization scripts to the WorkItem physical model. Change RLS now resolves tenant through `changes.work_item_id -> tickets.id`; obsolete extension-column indexes were replaced by shared Ticket indexes or professional-only indexes.

## Principal files

- Schemas/generated model: `ent/schema/{incident,problem,change,ticket}.go`, generated `ent/**`, `ent/schema/professional_extension_shared_fields_test.go`
- Incident authority: `service/incident_service.go`, `service/incident_work_item_authority.go`, Incident rules/monitoring/escalation/alerting/callback/DTO tests and callers
- Problem authority: `handlers/problem/repository_impl.go`, `handlers/problem/conversion.go`, `service/problem_investigation_service.go`, related tests
- Change authority: `handlers/change/repository_impl.go`, `handlers/change/service.go`, `service/bpmn/change_handler.go`, related tests
- Cross-cutting visibility: `database/softdelete.go`, `database/softdelete_test.go`, global search/dashboard/tenant lookup callers
- Migration/scripts: `migration/migrations.go`, `migration/professional_extension_*_test.go`, `migrations/20260901_drop_professional_extension_shared_fields*.sql`, `migrations/add_missing_indexes.sql`, `database/rls/migrations/{002_pilot_policies,rls_r1_e2e}.sql`, `sql/{seed_test_data,extend_test_data}.sql`

## TDD and failure evidence

Observed RED before the corresponding fixes:

1. Live PostgreSQL migration gate:
   `go test -tags=integration -race ./migration -run 'TestProfessionalExtensionMigration|TestProfessionalExtensionVerification' -count=1 -v`
   failed because a wrong named index on `incidents.tenant_id` was implicitly removed by `DROP COLUMN`, so 022 did not fail closed; the verification negative fixture also tried to index the already-removed column. The index-shape preflight now runs before column removal, and the negative verification fixture uses the retained wrong `id` column.
2. `go test ./database -run TestSoftDeleteInterceptorScopesChangeThroughWorkItem -count=1`
   failed with `Should be zero, but was 1`, proving Change remained visible after its WorkItem was soft-deleted. The Change interceptor now derives visibility from WorkItem.
3. `go test ./migration -run TestProfessionalExtensionOperationalAssetsUseWorkItemAuthority -count=1`
   failed on the old direct `changes.tenant_id` RLS policy/e2e insert and obsolete Incident/Problem/Change shared-column indexes. The retained scripts now use WorkItem authority.

## GREEN verification

- `go test ./database -run TestSoftDeleteInterceptorScopesChangeThroughWorkItem -count=1` — PASS.
- `go test ./migration -run TestProfessionalExtensionOperationalAssetsUseWorkItemAuthority -count=1` — PASS.
- `go test -tags=integration -race ./migration -run 'TestProfessionalExtensionMigration|TestProfessionalExtensionVerification' -count=1 -v` against local healthy `itsm-postgres-dev`, with an isolated UUID schema — PASS, five tests, zero skips. This covers duplicate rejection, exact-index rejection, apply → verify → idempotent apply → verify → reset → verify, and duplicate extension insert rejection for all three classes.
- `go test ./service -run TestIncidentService_CreateIncidentAllocatesSequentialWorkItemNumbers -count=10` — PASS (`0.287s`), no lock/panic.
- `go test -race ./service -run TestIncidentService_CreateIncidentAllocatesSequentialWorkItemNumbers -count=10` — PASS (`1.767s`), no race/lock/panic.
- `go test -race ./service -run TestIncidentService_CreateIncidentWaitsForPostCommitWorkflow -count=10` — PASS (`1.528s`); the blocking fake proves Create does not return while owned work is alive and returns immediately after release.
- `go test ./ent/schema ./migration ./service ./handlers/problem ./handlers/change -run 'ProfessionalExtension|IncidentService|Problem|Change' -count=1` — PASS.
- `go test ./handlers/change -count=1` — PASS after deleting the dead backfill path.
- `go test ./... -count=1` — PASS after the authority cutover. The service package's approximately 30-second aggregate duration is thousands of serial tests, not an Incident timeout: the Incident subset has no test above one second, and the two package tests above one second are unrelated existing auth/email tests.
- `go build ./...` — PASS.
- `npm run type-check` — PASS after installing the lockfile-defined dependencies with `npm ci --ignore-scripts`; `node_modules` is ignored and is not staged.
- RLS policy parse/catalog gate — PASS in a disposable local PostgreSQL database: `002_pilot_policies.sql` executed with `ON_ERROR_STOP=1`, created the Change policy, skipped absent vectors as designed, and `pg_get_expr` confirmed the policy is bound through `changes.work_item_id`. The disposable database was dropped by the command trap.
- `git diff --check` — PASS.
- Deletion scans — zero product/generated matches for `BackfillLegacyPendingChange`, `backfill_legacy_pending_changes`, Change Ent `RelatedTickets`/`related_tickets`, extension tenant predicates, or `go func` in `service/incident_service.go`. The three controller-owned transaction files have no diff from `ec36db69`.

## Self-review

- Reviewed the extension field inventory against the Unified Work Item contract rather than the earlier reviewer examples. `detected_at`, `escalated_at`, planned/actual implementation windows, risk/CAB fields, root-cause/workaround fields, and other professional lifecycle data remain on their owning extensions.
- Verified every extension create couples WorkItem plus extension in one transaction and every shared update targets WorkItem only. No compatibility setters/readers were regenerated.
- Verified migration 022 apply and canonical SQL remain byte-equivalent through the integration test, reset is schema-local, and verification binds schema/table/column/index identity.
- Reviewed generated Ent changes as mechanical consequences of the four schema edits; unrelated formatting-only schema files were restored before generation/commit.
- Confirmed no frontend contract change was needed: public DTO field names remain projections even though persistence authority moved.

## Concerns and integration gates

- Canonical fresh migration replay still has the pre-existing migration 009 blocker. This task does not alter or claim to pass that gate.
- P1-D owns migration 021. Integration must register/apply `020 -> 021 -> 022`; this branch intentionally registers 022 after 020 until the branches are combined.
- The RLS package remains a broader platform workstream, but its retained pilot policy and e2e script are no longer invalidated by the 022 schema cutover.
- `npm ci` reported the repository lockfile's existing two audit findings (one moderate, one high); no dependency or lockfile was changed by this task.

## Authority review round 1 fix

### Implementation

- Closed every raw-SQL Problem investigation path over the authoritative WorkItem boundary. Reads and mutations now require the caller tenant and `tickets.deleted_at IS NULL`; create statements use tenant-scoped `INSERT ... SELECT` so a soft delete or tenant mismatch cannot race a prior existence check. User references are tenant-validated as part of the same statement or before mutation.
- Extended migration 022 in place—there is still no 023. The apply SQL now upgrades a legacy unsuffixed, direct-column Change RLS policy before dropping `changes.tenant_id`, creates the sole canonical `tenant_isolation_changes` policy through the active WorkItem, and uses only `app.current_tenant`. Apply/verify enforce the exact named, validated, non-deferrable, one-column `work_item_id -> tickets(id)` foreign key for Incident, Problem, and Change; orphans and conflicting FK shapes fail closed.
- Aligned the retained 022 reset/verify assets, RLS pilot apply/rollback/e2e assets, and RLS integration fixture with the frozen policy naming/GUC contract. Verification rejects permissive/wrong-GUC policy shapes and requires exactly one Change policy.
- Deleted the complete legacy `IncidentService.executeIncidentRules` implementation and its private condition/action helpers. `NewIncidentService` always owns one authoritative `IncidentRuleEngine`; bootstrap passes that same instance to the controller, and every other composition root gets it by construction. Unsupported action types are parsed by the sole engine, return an error, and persist a `failed` execution rather than `completed`.
- Scoped both retained seed-script WorkItem-number existence checks by tenant.
- Repaired the canonical RLS connection boundary after the new live gate proved PostgreSQL rejects a bind placeholder in `SET SESSION`. `AcquireConn` now calls parameterized `set_config('app.current_tenant', $1, false)` with a decimal-string tenant ID; no tenant value is concatenated into SQL, no second GUC exists, and `ReleaseConn` still clears the session with `DISCARD ALL`.

### RED evidence

- `go test ./service -run 'TestProblemInvestigationRejectsSoftDeletedWorkItemReadsAndMutations|TestIncidentCreationUnknownRuleActionNeverCompletes' -count=1` — FAIL before implementation: a soft-deleted Problem investigation was returned successfully; the legacy Incident fallback silently ignored an unregistered action and did not create the expected authoritative failed execution.
- The initial full backend rerun exposed a real cross-dialect regression in the new tenant-safe `INSERT ... SELECT`: SQLite binds out-of-order `$n` occurrences differently from PostgreSQL, so `TestDualInvestigationEntryPoints` returned 500. The statements were rewritten with ordered-parameter input CTEs; the focused handler test then passed.
- The first disposable-PostgreSQL RLS run failed `TestAcquireConn_TenantScopeIsolation` and `TestReleaseConn_DiscardsSessionState` with SQLSTATE `42601`, `syntax error at or near "$1"`, at `SET SESSION app.current_tenant = $1`. This isolated the failure to the connection-setting statement before any policy evaluation; the parameter-safe `set_config` call is the single root-cause fix.

### GREEN evidence

- `go test ./service -run 'TestProblemInvestigationRejectsSoftDeletedWorkItemReadsAndMutations|TestIncidentCreationUnknownRuleActionNeverCompletes|TestIncidentCreationUsesFormalRuleEngine' -count=1` — PASS.
- `go test -race ./service -run 'TestProblemInvestigationRejectsSoftDeletedWorkItemReadsAndMutations|TestIncidentCreationUnknownRuleActionNeverCompletes|TestIncidentCreationUsesFormalRuleEngine|TestIncidentService_CreateIncidentAllocatesSequentialWorkItemNumbers' -count=10` — PASS.
- `go test -race ./controller ./service ./handlers/change -count=1` — PASS on the final code (`controller 76.558s`, `service 98.447s`, `handlers/change 4.567s`), including the previously known Incident detached-work race gate.
- `go test ./handlers/problem -run TestDualInvestigationEntryPoints -count=1` — PASS.
- `go test -tags=integration -race ./migration -run 'TestProfessionalExtensionMigration|TestProfessionalExtensionVerification' -count=1 -v` against local `itsm-postgres-dev` with per-test UUID schemas — PASS, nine tests, zero skips. Coverage includes no-FK upgrade, all three orphan SQLSTATE `23503` rejections, legacy unsuffixed/direct policy upgrade, real non-owner-role tenant/soft-delete/`WITH CHECK` behavior, exact-one canonical policy, conflicting named FK rejection, unvalidated FK rejection, permissive policy rejection, idempotent apply, reset, and verify.
- `go test ./... -count=1 && go build ./...` — PASS after the focused regression repair.
- `npm run type-check` — PASS.
- `go test ./database/rls -run 'TestAcquireConnUsesParameterSafeCanonicalTenantSetting|TestWithTenantAndRoundTrip|TestTenantMissing|TestSystemBypass' -count=1` — PASS. The recording driver proves one canonical GUC call with tenant `"42"` as a bound value.
- `go test -tags=integration_rls -race ./database/rls -count=1 -v` against a disposable local PostgreSQL database — PASS, fifteen tests, zero skips, including Change tenant/soft-delete visibility, both KAF RLS tables, NoTenant, SystemBypass, and session DISCARD. The database was created solely for this command and dropped by its shell trap.
- `git diff --exit-code d42a6277 -- repository/ticket/repository_impl.go repository/ticket/repository_test.go handlers/service_request/service.go` — PASS; the separately reviewed transaction implementation remains untouched.
- `git diff --check` — PASS.

### Integration note

- The frozen incoming 009 contract is `tenant_isolation_<table>` plus the sole GUC `app.current_tenant`, with `ENABLE` rather than broadly applying `FORCE`. This fix uses `tenant_isolation_changes` everywhere it owns. The old 009 body visible on this isolated branch still contains the pre-existing `app.current_tenant_id` implementation and remains the already-declared fresh-replay blocker; it must be replaced by the incoming 009 owner during integration, not patched here with a dual-GUC compatibility policy.

## Authority review round 2 fix

### Implementation

- Hardened migration 022 without adding a new migration. Apply and development reset now require the authoritative WorkItem table, reject orphans, establish the exact named `work_item_id -> tickets(id)` foreign keys, validate their complete shape, and fail closed when any extension has an additional FK involving `work_item_id`. Verify independently requires exactly one such FK per extension.
- Changed Change RLS verification from substring matching to an exact whitespace-normalized `pg_get_expr` comparison for both `USING` and `WITH CHECK`. The verifier still requires exactly one policy and the frozen `ENABLE`/no-`FORCE` table state, so `OR true`, extra branches, wrong GUCs, and additional policies fail closed.
- Made Problem investigation creation one transaction. The tenant/active-WorkItem/investigator/existing-investigation gates are in the insert, the authoritative WorkItem status update is tenant and soft-delete scoped, `RowsAffected` must be exactly one, and every failure rolls the new investigation back. Root-cause and solution deletion are each one scoped delete with an exact `RowsAffected` check; there is no select-then-delete success window.
- Made `ReleaseConn` run `DISCARD ALL` under an independent five-second cleanup context. If cleanup fails, the `database/sql` physical connection is marked with `driver.ErrBadConn` through `Conn.Raw` and closed, so a dirty session cannot re-enter the pool.

### RED evidence

- `go test ./database/rls ./service -run 'TestReleaseConn|TestCreateProblemInvestigationRollsBackWhenWorkItemIsSoftDeletedDuringMutation' -count=1` — FAIL before implementation: canceled request context prevented `DISCARD ALL`; a forced cleanup error reused the same physical connection; and the Problem create path returned success while leaving an investigation after the WorkItem became soft-deleted.
- `go test -tags=integration ./migration -run 'TestProfessionalExtension(MigrationRejectsAdditionalForeignKey|VerificationRejectsAdditionalForeignKey|ResetEstablishesExactForeignKeys|ResetRejectsAdditionalForeignKey|VerificationRejectsCanonicalPolicyWithPermissiveBranch|VerificationRejectsAdditionalPolicy)' -count=1 -v` against local `itsm-postgres-dev` — five expected failures before implementation: canonical-plus-extra FK passed apply and verify, reset did not create FKs, reset accepted an extra FK, and canonical `OR true` passed policy verification. The existing exact-one-policy guard already rejected the extra-policy fixture.

### GREEN evidence

- `go test -race ./database/rls ./service -run 'TestReleaseConn|TestCreateProblemInvestigationRollsBackWhenWorkItemIsSoftDeletedDuringMutation|TestProblemInvestigationRejectsSoftDeleted' -count=1` — PASS.
- `go test -race ./service -run 'TestCreateProblemInvestigationRollsBackWhenWorkItemIsSoftDeletedDuringMutation|TestProblemInvestigationRejectsSoftDeletedWorkItemReadsAndMutations' -count=10` — PASS (`2.414s`); repeated trigger-driven soft-delete interleavings leave no investigation and never report success.
- `go test -tags=integration -race ./migration -run 'TestProfessionalExtensionMigration|TestProfessionalExtensionVerification' -count=1 -v` — PASS against local `itsm-postgres-dev`, thirteen tests, zero skips, including canonical-plus-extra FK rejection and exact `OR true`/extra-policy rejection.
- `go test -tags=integration -race ./migration -run 'TestProfessionalExtensionReset' -count=1 -v` — PASS against local `itsm-postgres-dev`, two tests, zero skips; standalone reset establishes all three exact FKs and rejects canonical-plus-extra FK state.
- `go test -tags=integration_rls -race ./database/rls -run TestReleaseConn_DiscardsSessionState -count=1 -v` — PASS against local `itsm-postgres-dev`, zero skips. The request context is canceled before release, `DISCARD ALL` still succeeds, and the next borrower sees an empty tenant GUC.
- `go test ./service -count=1` — PASS (`28.684s`).
- `go test ./... -count=1` — PASS.
- `go build ./...` — PASS.
- `go test -race ./controller ./service ./handlers/change -count=1` — PASS (`controller 80.155s`, `service 92.112s`, `handlers/change 5.954s`).
- `npm run type-check` — PASS.
- `go test ./migration -run 'TestProfessionalExtensionOperationalAssetsUseWorkItemAuthority|TestProfessionalExtensionMigrationRegistry' -count=1` and `git diff --check` — PASS.
- The frozen transaction files `repository/ticket/repository_impl.go`, `repository/ticket/repository_test.go`, and `handlers/service_request/service.go` have no diff in this round.

### Evidence boundary

- A broad `go test -tags=integration_rls -race ./database/rls -count=1 -v` run had one environment-data failure: the pre-existing `TestAcquireConn_TenantScopeIsolation` assumes tenant 1 already owns at least one Change, but the current local dev database returned zero. All other cases passed, including the new canceled-context live cleanup test. This is recorded separately and is not represented as a clean full RLS run; the focused zero-skip release boundary above is the authoritative round-2 live evidence.
- The earlier fresh-replay 009 integration blocker and the required `020 -> 021 -> 022` combined ordering remain unchanged.

## Authority review round 3 fix

### Implementation

- Migration 022 apply and development reset now replace direct-column policies for all three professional extension tables, not only Change. Their only final policies are `tenant_isolation_incidents`, `tenant_isolation_problems`, and `tenant_isolation_changes`; each resolves tenant and active-record scope through `extension.work_item_id -> tickets.id`, uses only `app.current_tenant`, is explicitly `AS PERMISSIVE FOR ALL TO PUBLIC`, and leaves RLS enabled without `FORCE`.
- Verification is table-driven across Incident, Problem, and Change. For every table it requires the exact canonical policy name, exactly one policy, PUBLIC roles (`polroles = {0}`), ALL command (`polcmd = '*'`), permissive mode, exact whitespace-normalized `USING` and `WITH CHECK` expressions, and `ENABLE`/no-`FORCE`. Missing, extra, role-scoped, command-scoped, restrictive, or permissive-expression policies fail closed.
- Apply, reset, and verify continue to require the exact single extension `work_item_id -> tickets(id)` FK. Live adverse coverage now exercises canonical-plus-wrong FK state against every gate and every professional extension.
- No compatibility policy, alternate GUC, parallel name, new migration, or transaction-layer change was introduced.

### RED evidence

- The independent live reproduction rebuilt `tenant_isolation_changes` with identical expressions but `TO itsm_admin`; the previous verifier exited zero because it did not inspect `polroles`, `polcmd`, or `polpermissive`.
- Before the tuple fix, `go test -tags=integration ./migration -run 'TestProfessionalExtensionVerificationRejects(RoleScoped|CommandScoped|Restrictive)CanonicalPolicy' -count=1 -v` failed all three cases because verification returned nil.
- The fresh-009 integration precheck established that direct policies existed for all three extensions before shared-column removal while the previous 022 only rebuilt Change, leaving Incident and Problem without final policies. This external RED was encoded as a retained fresh-009-direct-policy upgrade test plus exact three-table catalog and behavior checks.

### GREEN evidence

- `go test -tags=integration -race ./migration -run 'TestProfessionalExtension(Migration|Verification|Reset|Apply)' -count=1 -v` against local `itsm-postgres-dev` — PASS, zero skips. It covers all three tables for missing/extra policy, `OR true`, wrong role, wrong command, restrictive mode, and apply/verify/reset additional-FK rejection while preserving the earlier lifecycle and FK/index gates.
- `go test -tags=integration -race ./migration -run 'TestProfessionalExtensionMigrationUpgradesFresh009DirectPoliciesForEveryExtension|TestProfessionalExtensionPoliciesEnforceWorkItemScopeForEveryExtension' -count=1 -v` — PASS, zero skips. All three fresh-009 direct policies upgrade in place; a real non-owner role sees only its active tenant WorkItem and cross-tenant inserts fail `WITH CHECK` for Incident, Problem, and Change.
- `go test ./migration -run 'TestProfessionalExtensionsDropSharedFieldsIsVersioned|TestProfessionalExtensionVerificationBindsExactReadyValidUniqueIndexes' -count=1` — PASS; the embedded apply SQL and retained asset remain equal, and Go source checks pin the tuple fields.
- `go test ./... -count=1` — PASS.
- `go build ./...` — PASS.
- `go test -race ./controller ./service ./handlers/change -count=1` — PASS (`controller 78.436s`, `service 96.366s`, `handlers/change 7.876s`).
- `npm run type-check` — PASS.
- `git diff --check` and the frozen transaction-file diff gate — PASS.

### Remaining integration contract

- Integration must still order `020 -> 021 -> 022`. The incoming corrected 009 remains responsible for its own canonical direct-policy creation; 022 is now proven to replace those direct policies with the final indirect WorkItem policies for every professional extension.
