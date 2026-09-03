# Task 7 Re-Review

**Verdict: NEEDS_CHANGES**

## Scope

- ITSM: `51c439ab..52e5ae93`
- KAF: `0d132ffc..df51f846`
- Prior review: `task-7-review.md`

## Findings

### P1 - Service Request creation can commit a classified WorkItem and start BPMN without its required extension

The new `record_class="service_request_item"` write happens inside
`ticket.EntRepository.Create` before `ServiceRequest` is inserted. The owning
`ServiceRequestService.Create` deliberately calls `TicketService.CreateTicket`
first, which starts the workflow asynchronously, then inserts the extension in
a separate operation. If the extension insert fails, the classified base
WorkItem and possibly its process instance remain committed without the one
required ServiceRequest extension.

This violates the unified WorkItem contract: a Service Request must create its
base WorkItem and its single professional extension in the same transaction.
It also means the new SSLVPN happy-path test cannot establish extension
exclusivity as an invariant; it only proves the no-error case.

References:

- `itsm-backend/repository/ticket/repository_impl.go:86-90`
- `itsm-backend/handlers/service_request/service.go:63-68`
- `itsm-backend/handlers/service_request/service.go:180-218`
- `itsm-backend/service/ticket_service.go:260-270`

Required correction: make Service Request base-record and extension creation
one transaction, and trigger BPMN only after that transaction commits. Add a
failure-injection test for extension persistence which proves that no Ticket
with `record_class=service_request_item`, no ServiceRequest row, and no process
instance survive a failed create.

### P1 - Mutable BPMN variables still override the authoritative WorkItem class, so KAF's new mismatch guard can accept the wrong class

For a ticket-backed workflow, `kafDelegationRecordClass` first trusts
`instance.Variables["record_class"]` and consults the persisted Ticket only
when that variable is absent or unsupported. `CustomProcessEngine.CompleteTask`
merges every caller-supplied variable into the process instance before advancing
to the next node. Thus an authorized approver can submit
`record_class="incident"` while completing a Service Request approval. The
outbox event and `GetTaskContext` both use the same mutable value, so KAF's new
event/context comparison sees two matching `incident` values and executes the
procedure for the wrong professional class.

The added KAF negative test only creates two synthetic inputs with different
top-level values. It does not cover this real ITSM source path, and the SSLVPN
test provides only the safe approval variables.

References:

- `itsm-backend/service/bpmn_process_engine.go:395-405`
- `itsm-backend/service/kaf_delegation_service.go:219-239`
- `itsm-backend/service/kaf_delegation_service.go:682-704`
- `tests/test_kaf_delegation_contract.py:145-181`

Required correction: for ticket-backed processes, load the tenant-scoped
WorkItem and derive `recordClass` exclusively from its persisted immutable
field; reject a present process variable when it disagrees. Add an integration
test that completes the real two-approval Service Request path with a
conflicting `record_class` variable and proves no wrong-class event can reach a
KAF procedure.

## Verified Coverage

The revised ITSM SSLVPN tests now call `ServiceRequestService.Create` and
`IncidentService.CreateIncident`, complete two real BPMN approval tasks for the
Service Request path, assert exactly one delegated task/outbox event, and check
the successful-path extension counts. KAF now rejects a differing event and
top-level context `recordClass` before invoking its procedure runner.

## Verification Evidence

- `go test ./handlers/service_request -run 'TestSSLVPN(Request|Incident)_' -count=1 -v` passed: 2 tests.
- `git diff --check 51c439ab..52e5ae93` passed.
- `git diff --check 0d132ffc..df51f846` passed.
- The KAF focused pytest command could not run: the specified worktree's
  `.venv/bin/python` exits with `No module named pytest`; neither that venv nor
  system Python has pytest installed. No dependencies were changed during this
  review.
