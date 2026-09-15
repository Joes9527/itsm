# P1-A generic Ticket creation transaction fix

## Scope

Implemented only the whole-branch review finding that generic Ticket number allocation
could commit before the Ticket row. No allocator authority, extension schema, or migration
022 files were changed.

## Implementation

- `itsm-backend/repository/ticket/repository_impl.go`
  - `EntRepository.Create` is the sole ordinary Ticket creation entry point. It opens one
    Ent transaction and commits only after both allocation and Ticket insertion succeed.
  - Added the narrow `TransactionalCreator` port. Its only operation accepts an explicitly
    caller-owned `*ent.Client`; it has no non-transactional fallback.
  - The primitive performs the allocator write and Ticket insert through exactly that client.
- `itsm-backend/handlers/service_request/service.go`
  - The already-owning Service Request transaction calls `TransactionalCreator` with
    `tx.Client()`, then creates its extension in that same transaction. It does not start
    a nested transaction.
- `itsm-backend/repository/ticket/repository_test.go`
  - Replaced the obsolete expectation that a failed Ticket insert leaves `last_value=1`.
  - Added rollback/reuse evidence and an outer-transaction rollback test.
- `itsm-backend/repository/ticket/repository_integration_test.go`
  - Added an isolated-schema, real PostgreSQL regression: force the Ticket insert to fail
    after allocation, assert no sequence row remains, delete the deliberately conflicting
    fixture, and prove the next successful Ticket reuses `000001`.

## Transaction ownership inventory

- Ordinary controller/API, MS Graph email adapter, and Tool Queue creation all call
  `TicketService.CreateTicket`, which uses the public atomic repository `Create`.
- `ServiceRequest.createWorkItemAndExtension` is the only production outer transaction
  owner. It is the only non-repository use of `CreateInTransaction` and passes `tx.Client()`.
- A source search after the change found no application use of `ent.ErrTxStarted`, no
  transaction probing, and no second transaction-bound Ticket creator callsite.

## RED evidence

Before the implementation, the focused regression failed as expected:

```text
$ go test -v ./repository/ticket -run TestRepository_Create_RollsBackAllocationWhenTicketInsertFails -count=1
FAIL: failed Ticket insert left sequenceCount=1
FAIL: next creation received TKT-202609-000002 instead of TKT-202609-000001
```

This proves the original implementation committed allocation before the deliberately
failing Ticket insert.

## GREEN and verification evidence

```text
$ go test -v ./repository/ticket -run 'TestRepository_Create(_RollsBackAllocationWhenTicketInsertFails|_FirstNumbersAreTenantScoped|$)|TestTransactionalCreator_UsesCallerOwnedTransaction' -count=1
PASS

$ go test ./handlers/service_request -run 'Test.*ServiceRequest' -count=1
PASS

$ ITSM_TEST_DB=(local itsm-postgres-dev disposable-schema DSN, redacted) go test -tags=integration -race ./repository/ticket ./repository/workitemnumber -count=1 -v
PASS
  TestEntRepository_CreatePostgreSQLRollsBackAllocationAfterTicketInsertFailure
  TestPostgreSQLAllocator_ConcurrentTenantMonthlyAllocation
  TestPostgreSQLAllocator_RollsBackWithWorkItem
  TestPostgreSQLAllocator_FailsClosedWhenMonthlySequenceIsExhausted

$ go test -race ./repository/ticket ./handlers/service_request ./internal/bootstrap -count=1
PASS

$ go test ./service -run 'TestTicketService_CreateTicket|TestCreateTicket_RequiredFieldValidation|TestTicketStoreAdapter_CreateTicket' -count=1
PASS

$ go test ./internal/bootstrap -run 'TestTicketStoreAdapter_CreateTicket' -count=1
PASS

$ go test ./... -count=1
PASS

$ go build ./...
PASS

$ git diff --check
PASS
```

The live test derived the DSN from the local `itsm-postgres-dev` container without
printing credentials. Each test used a UUID-named PostgreSQL schema and cleanup dropped it.

## Self-review

- Public `Repository.Create` has one meaning: allocate and insert atomically.
- The outer Service Request flow uses the same primitive and its existing rollback covers
  allocation, WorkItem, and extension together.
- No `ErrTxStarted` probe remains, so no transaction is intentionally attempted inside a
  transaction.
- No optional transaction flag, compatibility constructor, retry, fallback allocator, or
  migration/schema change was introduced.
- `git diff --check` is clean. The only code changes are Ticket creation and its existing
  Service Request outer-transaction callsite; the only added test is the PostgreSQL rollback
  regression.

## Concerns

- During an earlier broad focused command, the pre-existing detached Incident rule goroutine
  intermittently locked SQLite (`TestIncidentService_CreateIncidentAllocatesSequentialWorkItemNumbers`:
  `database table is locked: tickets`). The unchanged baseline incident code launches that
  goroutine after its first create; a three-run reproduction passed, failed, passed. No
  Incident code was modified. A subsequent full `go test ./... -count=1` passed.
- The new transaction-bound creator is intentionally a narrow exported port because
  Service Request is in another package. Source search pins the current production callers:
  only public `Create` and the Service Request outer transaction invoke it.

## Commit

This report is included in the single task commit; its final SHA is recorded in the task handoff.
