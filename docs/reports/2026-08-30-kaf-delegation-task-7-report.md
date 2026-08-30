# Task 7 Report: SSLVPN Delegation Contract Acceptance

## Scope

Task 7 adds deterministic acceptance coverage for the SSLVPN service-request
and incident delegation paths. Both paths use the same tenant-scoped outbox and
KAF delegation transport while retaining their respective professional
extensions.

No API difference from `docs/superpowers/specs/2026-08-28-kaf-itsm-autonomous-workitem-delegation-design.md` was found. The documented event shape,
task-scoped context endpoint, and `complete_bpmn_task` action remain accurate,
so the specification was not modified.

## Coverage

- `TestSSLVPNRequest_ApprovalDelegationDeliveryAndCompletion` persists two
  approval decisions, delivers the minimal signed KAF event, retrieves
  `service_request_item` context, completes the delegated BPMN task once, and
  verifies the ServiceRequest extension remains the only professional extension.
- `TestSSLVPNIncident_UsesSameDelegationTransportWithoutServiceRequestConversion`
  delivers the same event transport for an `incident`, verifies incident context
  and extension persistence, and proves no ServiceRequest extension is created.
- `tests/test_kaf_delegation_contract.py` runs the KAF durable receipt pipeline
  with a deterministic ITSM context client and procedure double for both record
  classes. It verifies record-class and WorkItem identity propagation to the
  selected procedure.

## Verification

- `go test ./service -run 'TestSSLVPN(Request|Incident)_' -count=1 -v` passed.
- `go test ./service/... ./controller/... -count=1` passed.
- `go test ./... -count=1` passed.
- `go build ./...` passed.
- `ENV_FILE=/dev/null DEBUG=true PYTHONPATH=src /home/administrator/actions-runner/_work/kaf/kaf/.venv/bin/python -m pytest tests/test_kaf_delegation_contract.py tests/test_kaf_delegation_pipeline.py -q` passed: `19 passed, 1 skipped`.
- `ENV_FILE=/dev/null DEBUG=true PYTHONPATH=src /home/administrator/actions-runner/_work/kaf/kaf/.venv/bin/python -m ruff check tests/test_kaf_delegation_contract.py` passed.

The local KAF worktree `.venv` does not include pytest; the available shared KAF
runner above was used with `PYTHONPATH=src` from this worktree, so the executed
test files and source were the Task 7 worktree versions.
