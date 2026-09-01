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
