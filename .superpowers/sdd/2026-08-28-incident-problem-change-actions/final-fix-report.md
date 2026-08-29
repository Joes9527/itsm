# Final Fix Report: Incident/Problem/Change Actions

Date: 2026-08-29
Worktree: `/home/administrator/project/itsm/.worktrees/incident-problem-change-actions`
Branch: `feat/incident-problem-change-actions`
Base before fixes: `92217ae4`

## Commits

- `ef500e4d fix(backend): harden action invariants`
- `19718324 fix(frontend): refresh incident detail actions`

## Fixes

### 1. SQLite-compatible incident/problem relation preflight

Changed files:

- `itsm-backend/internal/bootstrap/incident_problem_relation_migration.go`
- `itsm-backend/internal/bootstrap/incident_problem_relation_migration_test.go`

Result:

- Replaced the PostgreSQL-only `information_schema/current_schema()` table probe with a portable zero-row table read.
- Missing `work_item_relations` now skips cleanly on fresh SQLite and PostgreSQL-style missing-table errors.
- Existing duplicate detection and diagnostics remain unchanged, including tenant, source WorkItem, target WorkItem IDs, and relation IDs.
- Added real SQLite tests for missing table skip, existing table without duplicate live relations, and duplicate live relation failure.

### 2. Problem close write/read alignment

Changed files:

- `itsm-backend/handlers/problem/authorization.go`
- `itsm-backend/handlers/problem/service.go`
- `itsm-backend/handlers/problem/service_test.go`
- `itsm-backend/handlers/problem/handler_test.go`

Result:

- Added shared `canCloseProblemStatus` predicate.
- `CanCloseProblem`, generic `Service.Update`, and dedicated `CloseProblem` now agree that only `resolved` can close.
- Direct close from `open`, `investigating`, `identified`, and legacy `in_progress` is rejected.
- Existing valid lifecycle transitions are preserved, including resolving from legacy `in_progress` and closing from `resolved`.

### 3. Incident action freshness

Changed files:

- `itsm-frontend/src/app/(main)/incidents/[id]/page.tsx`
- `itsm-frontend/src/components/incident/IncidentDetail.tsx`
- `itsm-frontend/src/components/incident/__tests__/IncidentDetail.test.tsx`

Result:

- Added `onIncidentLoaded` callback plumbing matching the existing Problem/Change parent-owned action pattern.
- Incident page now owns and refreshes latest Incident/actions through `syncIncidentSummary`.
- Removed implicit `data.actions` as a third action source inside `IncidentDetail`; provider actions remain authoritative, with explicit `fallbackActions` for no-provider mode.
- Added regression coverage where pre-command provider actions are replaced after resolve refetch, enabling close and disabling stale resolve.

### 4. MSP-aware Problem/Change detail lookup and actions

Changed files:

- `itsm-backend/handlers/problem/handler.go`
- `itsm-backend/handlers/problem/handler_test.go`
- `itsm-backend/handlers/change/handler.go`
- `itsm-backend/handlers/change/handler_test.go`

Result:

- `Problem Handler.Get` and `Change Handler.GetChange` now resolve the effective tenant with `middleware.ResolveRequestTenantID`.
- Resolver errors abort through `middleware.AbortIfTenantError`.
- The resolved tenant is used for both record lookup and `service.ActionActor.TenantID`.
- Added selected-customer MSP success tests, unauthorized MSP customer denial tests, and same-tenant OK assertions for both handlers.

## Verification

Focused red/green evidence:

- SQLite preflight tests first failed on `no such table: information_schema.tables`, then passed after portable table handling.
- Problem close tests first failed because unresolved direct close returned nil error, then passed after shared close predicate enforcement.
- Incident freshness test first failed because close action never appeared after refetch, then passed after parent callback/provider refresh.
- Problem/Change MSP tests first failed with `404` from raw home-tenant lookup, then passed with resolver-based tenant lookup and `403` unauthorized-customer denial.

Focused commands:

- `cd itsm-backend && go test ./internal/bootstrap ./ent/schema -run 'Test(PrepareIncidentProblemRelation|WorkItemRelation)' -count=1` passed.
- `cd itsm-backend && go test ./handlers/problem -count=1` passed.
- `cd itsm-frontend && npx jest --runInBand --coverage=false src/components/incident/__tests__/IncidentDetail.test.tsx` passed.
- `cd itsm-frontend && npm run type-check` passed.
- `cd itsm-backend && go test ./handlers/change -count=1` passed.

Combined commands:

- `cd itsm-backend && go test ./internal/bootstrap ./ent/schema ./handlers/problem ./handlers/change ./controller ./service -count=1` passed.
- `cd itsm-frontend && npx jest --runInBand --coverage=false src/components/incident/__tests__/IncidentDetail.test.tsx src/components/problem/__tests__/ProblemDetail.test.tsx src/components/change/__tests__/ChangeDetail.test.tsx src/lib/utils/__tests__/workflow-state-machine.test.ts src/lib/__tests__/message-channel-shim.test.ts` passed: 5 suites, 36 tests.
- `cd itsm-frontend && npm run type-check` passed.
- `cd itsm-frontend && npm run lint:check` passed with 0 errors and 3 unrelated warnings for unused eslint-disable directives.
- `cd itsm-backend && go vet ./...` passed.
- `cd itsm-backend && go build ./...` passed.
- `git diff --check` passed.

## Residuals

- `itsm-frontend/test-results/junit.xml` was dirty before this wave and was not staged or committed.
- Focused Jest runs still emit existing Ant Design static `message` context warnings from Incident/Problem success paths. They do not fail the requested Jest bundle and were outside this fix wave.
- Optional Incident backend relation-check context threading was not included because it would require changing authorization signatures and tests outside the exact touched backend finding scope.

## Final Re-review Fix

Date: 2026-08-29
Head before fix: `c8f0ec60`
Fix commit: `8f280d8c fix(bootstrap): narrow missing relation table detection`

Changed files:

- `itsm-backend/internal/bootstrap/incident_problem_relation_migration.go`
- `itsm-backend/internal/bootstrap/incident_problem_relation_migration_test.go`

Result:

- Removed broad `does not exist` and `undefined_table` string matching from `isMissingTableError`.
- Added a production-local `SQLState() string` interface and `errors.As` classification for PostgreSQL undefined-table errors only when SQLSTATE is `42P01`.
- Restricted SQLite missing-table classification to the target-specific normalized message `no such table: work_item_relations`.
- Added focused classifier tests for nil, target SQLite missing table, PostgreSQL SQLSTATE `42P01`, wrapped variants, unrelated `resource does not exist`, different SQLite table, other SQLSTATE, syntax error, and connection failure.
- Retained the real SQLite preflight coverage for fresh missing table, valid existing table, and duplicate live relation diagnostics.

Verification:

- Red: `cd itsm-backend && go test ./internal/bootstrap -run TestIsMissingTableErrorStrictClassification -count=1` failed before the fix because unrelated `resource does not exist` and `no such table: service_requests` returned true.
- Green: `cd itsm-backend && go test ./internal/bootstrap -run 'Test(IsMissingTableErrorStrictClassification|PrepareIncidentProblemRelationMigration)' -count=1` passed.
- `cd itsm-backend && go test ./internal/bootstrap -count=1` passed.
- `cd itsm-backend && go test ./internal/bootstrap ./ent/schema ./handlers/problem ./handlers/change ./controller ./service -count=1` passed.
- `cd itsm-backend && go vet ./...` passed.
- `cd itsm-backend && go build ./...` passed.
- `git diff --check` passed.

Residuals:

- `itsm-frontend/test-results/junit.xml` remains dirty and was not staged or committed.
