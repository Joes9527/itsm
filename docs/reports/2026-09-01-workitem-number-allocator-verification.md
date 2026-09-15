# WorkItem NumberAllocator Verification Report

Date: 2026-09-01

Application baseline: `71e584326136e2531207d108079bdb4eb6feed27`

Task 6 verification baseline / verified P1-A implementation head: `b67ad2c28ca66360f9d8dffa19dec95d78a57b6a`

## Assessment

**Conditional — P1-A implementation gates pass, but release/integration is blocked.**

The NumberAllocator cutover, tenant-scoped number contract, retained `020`/`022` assets,
domain callers, deletion gates, live concurrency, transaction rollback, default test suite,
race checks, build, and frontend type check all passed.

Release approval is intentionally withheld because a canonical fresh replay from an empty
migration ledger fails in the pre-P1-A migration stream at `009_enable_rls_tenant_isolation`.
Migration `009` references the absent relation `sla_policies` after the current Ent schema is
created. Task 6 does not change that unrelated migration. The integration owner must repair or
formally replace the fresh-bootstrap/migration ordering contract and rerun the canonical replay.

P1-D reserves migration `021`. This branch intentionally contains `020`, then `022`; the
integrated migration stream must insert and register P1-D `021` between them before any release.

## Scope and invariants

- PostgreSQL is the only `tickets.ticket_number` sequence authority.
- Number format is `TKT-YYYYMM-NNNNNN`, where the month is derived from `issuedAt.UTC()`.
- Sequence scope is `(tenant_id, period)` and number uniqueness is
  `(tenant_id, ticket_number)`.
- A transaction-owning caller passes `tx.Client()` so number allocation and WorkItem creation
  commit or roll back together.
- Redis, ticket-table max scans, timestamps, random/hash suffixes, collision retries, dual paths,
  compatibility constructors, and historical backfills are not number authorities.
- `SequenceService` remains only for the professional Incident identifier `incident_number`.

## Deletion gates

All commands ran from `itsm-backend`; all three returned exit `0` and zero matches without
exclusions:

```text
$ test -z "$(rg -n 'sequence:ticket|generateWorkItemTicketNumber|generateBackfillTicketNumber|GenerateTicketNumberGlobal|func .*GenerateTicketNumber|TKT-FS-|TK-%d-%s' --glob '*.go' --glob '*.md' . || true)"
exit=0 matches=0

$ test -z "$(rg -n 'NewTicketCoreService|TicketDomainService' --glob '*.go' . || true)"
exit=0 matches=0

$ test -z "$(rg -n 'backfill_(incident|problem|change)_work_item' service handlers dto ../itsm-frontend/src || true)"
exit=0 matches=0
```

The deleted Incident/Problem/Change WorkItem backfill commands were not replaced. There is no
history migration or compatibility read/write path.

## Tenant-scoped human-number lookup audit

The production-source audit command was:

```text
$ rg -n 'TicketNumber\(|TicketNumberEQ|TicketNumberIn|GetByNumber\(' \
    --glob '*.go' --glob '!*_test.go' .
```

The only WorkItem lookups by human ticket number are tenant-bound:

| Lookup | Tenant binding |
|---|---|
| `repository/ticket/repository_impl.go` `GetByNumber(ctx, ticketNumber, tenantID)` | Same Ent `Where` includes `TicketNumber`, `TenantID`, and `DeletedAtIsNil` |
| `handlers/change/repository_impl.go` related-number resolution | Same Ent `Where` includes `TicketNumberIn` and `TenantID` |

Other matches are WorkItem creation, immutable number projections into audit/SLA/RCA records,
or generated Ent builders/predicates; none performs a global Ticket lookup.

## Authoritative interface for P1-B

P1-B must consume the interface at verified implementation head `b67ad2c2` without introducing
another creation semantic:

```go
type Allocator interface {
    Allocate(
        ctx context.Context,
        client *ent.Client,
        tenantID int,
        issuedAt time.Time,
    ) (string, error)
}
```

`PostgreSQLAllocator` is stateless. Application bootstrap constructs one process-wide instance
and injects it into Ticket, Service Request, Incident, Problem, Change, and Feishu synchronization.
The separate internal container and maintenance CLI are independent composition roots, not
number authorities; they construct the same stateless implementation whose state is solely in
PostgreSQL.

P1-B dependencies remain:

1. Inventory every `TicketService.CreateTicket` caller, including normal/quick controllers,
   MS Graph email ingress, Service Request, Tool Queue, and internal service creation.
2. Give each caller either an explicit owning transaction or the single top-level WorkItem
   application service transaction. Do not retain two `CreateTicket` meanings.
3. Preserve the frozen allocator interface and `tx.Client()` rule while physically completing
   WorkItem/SLA ownership. Do not add fallback, compatibility, or dual-write behavior.

## Domain creation evidence without Redis number authority

The focused matrix exercised all WorkItem-number callers with `PostgreSQLAllocator`; these tests
do not provision or depend on Redis:

| Path | Evidence |
|---|---|
| Ticket | `TestRepository_Create_FirstNumbersAreTenantScoped` and same-tenant duplicate rejection |
| Requested Item | `TestService_Create_CommitsWorkItemExtensionAndNumberTogether` proves WorkItem, extension, and counter commit together |
| Incident | `TestIncidentService_CreateIncidentAllocatesSequentialWorkItemNumbers` proves `TKT-...-000001/000002` |
| Problem | `TestProblemServiceAllocatesTenantScopedWorkItemNumbers` proves independent tenant sequences |
| Change | `TestChangeRepositoryAllocatesSequentialWorkItemNumbers` proves sequential WorkItem numbers |
| Feishu inbound | `TestFeishuSyncService_UsesWorkItemNumberAllocator` proves rollback and `TKT-202609-000001/000002` |

Incident has two deliberately distinct numbers:

- Its WorkItem number uses `PostgreSQLAllocator`, exactly like every other WorkItem.
- Its professional `incident_number` remains `INC-YYYYMM-NNNNNN`; when no
  `SequenceService` is injected (the focused tests' Redis-unavailable condition),
  `IncidentService` uses its explicitly retained tenant-scoped database fallback.

No other production domain uses `SequenceService` for a WorkItem number.

## Isolated PostgreSQL migration evidence

All database checks used a disposable database on local container `itsm-postgres-dev` through
`127.0.0.1`. The prohibited shared database host was not contacted. Credentials were derived
inside the shell from container configuration and were not printed.

### Canonical fresh replay — failed, integration blocker

The disposable database was created empty, the current complete Ent schema was installed, and
the canonical CLI was run with an empty migration ledger:

```text
$ go run -tags migrate ./cmd/migrate -up
007_add_change_execution_tables: applied
008_add_initialization_ledger: applied
009_enable_rls_tenant_isolation: FAIL
pq: relation "sla_policies" does not exist (42P01)
exit=1
```

This result is not counted as a P1-A migration pass and is not hidden by the later upgrade
fixture. It establishes a pre-P1-A bootstrap/migration ordering dependency that blocks release.

### Explicit pre-P1-A upgrade fixture — `020` and `022` passed

To isolate the migrations owned by this branch, the same disposable database retained the
actually applied `007`/`008` ledger rows and explicitly marked `009`–`019` as pre-existing
upgrade-fixture rows with empty checksums. The canonical CLI then actually applied `020` and
`022`:

```text
$ go run -tags migrate ./cmd/migrate -up
020_work_item_number_allocator: applied
022_drop_professional_extension_shared_fields: applied
Applied 2 migration(s)
exit=0
```

Retained verification scripts both returned zero:

```text
20260901_work_item_number_allocator_verify.sql: exit=0
20260901_drop_professional_extension_shared_fields_verify.sql: exit=0
```

The migration ledger stored non-empty canonical checksums:

```text
020_work_item_number_allocator=5bec8fc09daa852ba36f5b5aa96a06118a57adf3ee24a1150a856efd015159e9
022_drop_professional_extension_shared_fields=3b730aaaecbdb146d5112ed840eae46acb19088bc14b44a8c9622afb5771546c
```

### Development reset contract

On the empty disposable database, the retained `020` reset script was idempotent:

```text
first empty reset: exit=0
second empty reset: exit=0
```

After inserting a constraint-valid Tenant, User, and Ticket fixture, reset failed closed:

```text
populated reset: exit=3
message matched: reset requires an empty tickets table
```

The disposable database was dropped and recreated before live allocator tests. No historical
data was migrated. It was removed after all database gates completed, and a catalog query
confirmed that the database no longer existed.

### Migration 022 apply/verify/reset lifecycle

Review-round evidence ran against `itsm-postgres-dev` through `127.0.0.1` in a fresh UUID-named
schema. `ITSM_TEST_DB` was derived from the local container configuration without printing its
credentials. The integration command was exactly:

```text
$ go test -tags=integration -race ./migration \
    -run 'TestProfessionalExtensionMigration|TestProfessionalExtensionVerification' \
    -count=1 -v
exit=0; tests passed=5; failed=0; skipped=0
```

`TestProfessionalExtensionMigrationEnforcesOneToOneAndAssetLifecycle` executes the retained
assets in this exact order: apply, verify, idempotent apply, verify, development reset, verify.
The test passed under `-race` with zero skips. A separate disposable-schema `psql` execution of
the same lifecycle recorded each boundary independently:

```text
$ PGOPTIONS='-c search_path=<uuid-schema>' psql "$ITSM_TEST_DB" \
    -v ON_ERROR_STOP=1 -f migrations/20260901_drop_professional_extension_shared_fields.sql
022 apply: exit=0

$ PGOPTIONS='-c search_path=<uuid-schema>' psql "$ITSM_TEST_DB" \
    -v ON_ERROR_STOP=1 -f migrations/20260901_drop_professional_extension_shared_fields_verify.sql
022 verify after apply: exit=0

$ PGOPTIONS='-c search_path=<uuid-schema>' psql "$ITSM_TEST_DB" \
    -v ON_ERROR_STOP=1 -f migrations/20260901_drop_professional_extension_shared_fields.sql
022 idempotent apply: exit=0

$ PGOPTIONS='-c search_path=<uuid-schema>' psql "$ITSM_TEST_DB" \
    -v ON_ERROR_STOP=1 -f migrations/20260901_drop_professional_extension_shared_fields_verify.sql
022 verify after idempotent apply: exit=0

$ PGOPTIONS='-c search_path=<uuid-schema>' psql "$ITSM_TEST_DB" \
    -v ON_ERROR_STOP=1 -f migrations/20260901_drop_professional_extension_shared_fields_dev_reset.sql
022 development reset: exit=0

$ PGOPTIONS='-c search_path=<uuid-schema>' psql "$ITSM_TEST_DB" \
    -v ON_ERROR_STOP=1 -f migrations/20260901_drop_professional_extension_shared_fields_verify.sql
022 verify after development reset: exit=0
```

The 022 development reset intentionally does not restore obsolete shared columns. Its script
reasserts the authoritative target schema for this development-only cutover, so post-reset
verification is expected to pass. The final catalog assertions were
`shared_columns=0` and `ready_valid_unique_indexes=3`. The UUID schema was then dropped.

This lifecycle evidence is an explicit P1-A upgrade fixture result. It does not change or mask
the canonical fresh-replay failure at migration 009, and it does not make the release approved.

## Test and build matrix

### Focused packages

```text
$ go test ./ent/schema ./migration ./repository/workitemnumber ./repository/ticket \
    ./handlers/service_request ./handlers/problem ./handlers/change ./service \
    ./internal/bootstrap ./internal/container -count=1
exit=0; 9 package targets passed, 1 package had no tests; failures=0
```

### Live PostgreSQL allocator race gate

```text
$ ITSM_TEST_DB=(local disposable DSN, redacted) go test -tags=integration -race \
    ./repository/workitemnumber -count=1 -v
exit=0; top-level pass=7; fail=0; skip=0
```

The concurrent test committed 128 independent Ent transactions: 64 for tenant `101` and 64 for
tenant `202`. Each tenant received exactly `TKT-202609-000001` through
`TKT-202609-000064`; PostgreSQL retained exactly two `(tenant_id, period)` rows and each
`last_value` was `64`. Rollback tests proved both the Ticket and counter increment disappear,
and the next committed transaction receives `000001`. Exhaustion at `999999` failed closed.

### Detached-goroutine race regression

The requested known-race probe did not reproduce a race on this head:

```text
$ go test -race ./controller ./service ./handlers/change -count=1
exit=0; controller/service/handlers-change packages passed; race reports=0
```

Task 6 made no product-code change related to detached goroutines.

### Full matrix

```text
$ go test ./... -count=1
exit=0; failures=0

$ go build ./...
exit=0

$ npm run type-check
> tsc --noEmit
exit=0

$ git diff --check
exit=0
```

## Exit checklist

- [x] `ticket_number` is immutable and unique by tenant and number.
- [x] One sequence row exists per tenant and UTC month.
- [x] Ticket, Incident, Problem, Change, Requested Item, and Feishu inbound use the allocator.
- [x] Transaction owners pass `tx.Client()` and live rollback evidence passes.
- [x] Old Redis Ticket keys, max scans, timestamp formats, and collision retry paths have zero matches.
- [x] Dead TicketCore/DDD/global generators and WorkItem backfills are deleted.
- [x] `020`/`022` apply/reset/verification assets pass on an explicit isolated upgrade fixture.
- [x] PostgreSQL concurrency/race tests run with zero skips.
- [x] Full tests, race probe, build, frontend type check, and diff check pass.
- [x] P1-B receives the exact allocator interface and verified implementation commit.
- [ ] Canonical fresh migration replay passes. Blocked at pre-P1-A migration `009`.
- [ ] Integrated catalog contains P1-D `021` between P1-A `020` and `022`.

Release decision: **not approved until both unchecked integration gates are resolved and the
canonical isolated replay is rerun.**
