# Task 7 Report: SSLVPN Delegation Contract Acceptance

## Scope

Task 7 adds deterministic acceptance coverage for the SSLVPN service-request
and incident delegation paths. Both paths use the same tenant-scoped outbox and
KAF delegation transport while retaining their respective professional
extensions. The P1 re-review remediation makes Service Request WorkItem and
extension persistence atomic, starts BPMN only after commit, and derives a
ticket-backed KAF delegation class exclusively from the tenant-scoped persisted
WorkItem.

No API difference from `docs/superpowers/specs/2026-08-28-kaf-itsm-autonomous-workitem-delegation-design.md` was found. The documented event shape,
task-scoped context endpoint, and `complete_bpmn_task` action remain accurate,
so the specification was not modified.

## Coverage

- `handlers/service_request/kaf_delegation_sslvpn_e2e_test.go` creates the
  SSLVPN Service Request through `ServiceRequestService.Create`, runs two real
  BPMN approval task completions, and proves those transitions create exactly
  one `kaf_delegate` task and one outbox event. It reloads persisted extensions
  to prove `service_request_item` is exclusive before and after KAF completion.
- `TestSSLVPNRequest_CreateRollsBackWorkItemAndDoesNotStartBPMNWhenExtensionPersistenceFails`
  injects a real Ent ServiceRequest create failure. It proves the failed
  aggregate leaves no `service_request_item` Ticket, no ServiceRequest
  extension, and no ProcessInstance.
- `TestSSLVPNRequest_ConflictingRecordClassVariableCannotReachKAF` completes
  the real two-approval SSLVPN flow with `record_class="incident"` on the
  second approval. ITSM rejects the conflicting mutable variable before KAF
  task/outbox creation, so a wrong-class event cannot reach KAF.
- Ticket-backed KAF workflows now tenant-scope the persisted Ticket lookup,
  use its immutable `record_class` as the only record-class authority, and
  reject any present process variable that differs.
- `TestSSLVPNIncident_UsesSameDelegationTransportWithoutServiceRequestConversion`
  creates the Incident through `IncidentService.CreateIncident`, delivers the
  same transport for an `incident`, and proves its persisted extension is
  exclusive of ServiceRequest.
- The KAF pipeline mismatch test and positive bound fixtures remain pending:
  the available KAF checkout at
  `/home/administrator/actions-runner/_work/kaf/kaf` is unrelated `main`
  (`1db6949`), does not contain the reviewed Task 7 files or commits, and has
  a user-owned `uv.lock` modification. No KAF files were changed in that
  checkout.

## Verification

- `go test ./handlers/service_request -run 'TestSSLVPNRequest_(ApprovalDelegationDeliveryAndCompletion|CreateRollsBackWorkItemAndDoesNotStartBPMNWhenExtensionPersistenceFails|ConflictingRecordClassVariableCannotReachKAF)$' -count=1 -v` passed.
- `go test ./handlers/service_request ./service -count=1` passed.
- `go test ./... -count=1` passed.
- `go build ./...` passed.
- KAF pytest and Ruff checks were not run because the specified Task 7 KAF
  source is absent from the available linked checkout.
