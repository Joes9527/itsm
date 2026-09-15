# P1 integration contract convergence report

## Scope

- Retire the TicketApproval schema, generated Ent runtime, service mutations,
  routes, Swagger surface, frontend clients, controls, and direct transition
  command.
- Keep BPMN `ProcessTask` / `ProcessApprovalDecision` as the sole ticket
  approval execution and history contract.
- Preserve callback effect truthfulness: a persisted write is `applied`, a
  proven retry is `idempotent`, and an unavailable mandatory effect is
  `blocked`.
- Return Incident workflow mutation outcomes from the owning domain service,
  keep assignment/status atomic, and make alert delivery bounded by the caller
  lifecycle.
- Preserve the canonical migration order `020 -> 021 -> 022` and retained
  apply/reset/verify assets.

## TDD evidence

The frontend transition regression was first added against the existing code.
It failed because `PENDING_APPROVAL -> REJECTED` returned `true` and the action
projection returned `rejectTicket`. The production change then removed that
direct transition and command; the focused suite passed 18/18.

## Fresh verification evidence

Backend:

- `go test ./controller ./router ./migration -count=1` — pass.
- `go test ./service/bpmn ./service -count=1` — pass.
- `go test ./... -count=1` — pass, zero package failures.
- `go build ./...` — pass.
- `go test -race ./controller ./service ./service/bpmn ./handlers/change -count=1`
  — pass for all four packages, no race report.
- Local disposable PostgreSQL:
  `go test -tags=integration -race ./migration -run
  'TestMigration021CallbackOptionalDeclaredIsIdempotent|TestProfessionalExtension(Migration|Verification|Reset|Apply|Policies)'
  -count=1 -v` — pass, zero skips. This exercised 021 apply/reset/verify and
  the 022 one-to-one, FK/index, exact RLS policy, tenant behavior, and retained
  asset lifecycle gates.

Frontend:

- Focused approval/ticket/API/state-machine Jest: 7 suites, 146 tests passed.
- `npm run type-check` — pass.
- `npm run lint:check` — exit 0 with three pre-existing unused-disable warnings
  outside this convergence diff.
- `npm run build` — pass; Next.js compiled, type/lint validation completed, 133
  static pages generated, and standalone runtime prepared.

Repository checks:

- `git diff --check` — pass.
- No ticket approve/reject HTTP route remains.
- No runtime `TicketApproval` Ent schema/client/API/component remains.
- No runtime `approveTicket` or `rejectTicket` command remains.
- The only `ticket_approvals` references are the 022 removal/verification SQL
  and migration regression fixtures; BPMN `ProcessApprovalDecision` remains
  present in backend and frontend projections.
- Generated Jest `test-results/junit.xml` was restored and is not part of the
  change.

## Canonical fresh-bootstrap base divergence

The local disposable fresh run was attempted and failed at migration 007 with
`pq: column c.tenant_id does not exist at position 98:17 (42703)`. This branch
does not contain the separately owned migration-007 authority fix
`9d25278c`; the final Ent Change schema already owns tenant through
`changes.work_item_id -> tickets.tenant_id`. Migration 007 was intentionally
not edited in this task. The disposable database was removed and its absence
was verified. Fresh must be rerun after the integration base includes that
owner commit.
