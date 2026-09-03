# Task 7 Independent Review

**Verdict: NEEDS_CHANGES**

Scope reviewed independently:

- ITSM: `45bf0f72..51c439ab`
- KAF: `45188311..0d132ffc`
- Brief: `docs/superpowers/plans/2026-08-29-kaf-delegation-transactional-delivery.md`, Task 7

## Findings

### P1 - The SSLVPN Service Request test does not exercise the approved delegation path or professional creation path

`TestSSLVPNRequest_ApprovalDelegationDeliveryAndCompletion` begins with a
generic `Ticket` and manually inserts its `ServiceRequest` extension. Its
process instance is also seeded directly at `Activity_KafDelegate`. The helper
named `approveBothSSLVPNLevels` only inserts completed `ProcessTask` and
`ProcessApprovalDecision` rows; it does not call the approval transition or
advance BPMN. The test then calls `KafDelegationService.CreateDelegatedTask`
directly.

Consequently it can pass when the real SSLVPN approval completion path never
creates a delegation task, or when Service Request / Incident creation regresses
to a generic Ticket without the correct extension. The Incident case has the
same creation blind spot: it directly persists a Ticket and Incident extension
instead of exercising the owning professional creation route/service.

References:

- `itsm-backend/service/kaf_delegation_sslvpn_e2e_test.go:91-110`
- `itsm-backend/service/kaf_delegation_sslvpn_e2e_test.go:119-135`

Required correction: construct both work items through their authoritative
Service Request and Incident creation paths. For the Service Request, run the
two real approval completions and assert the resulting BPMN transition creates
exactly one `kaf_delegate` task/outbox event. Query the persisted extensions
after that flow and assert the expected extension exists exclusively, rather
than using seeded extension rows as the evidence.

### P2 - The KAF test cannot detect record-class disagreement between the delivered event and ITSM context

The KAF test independently creates a synthetic delegation event and a synthetic
context using the same parameter. `KafDelegationPipeline._validated_context`
checks task ID, correlation ID, tenant, type, status, and allowed actions, but
does not compare `recordClass` from the event with `recordClass` from the
context. Therefore the test passes if a malformed/cross-class event is accepted
and the procedure runs with a different class supplied by context. This is a
false-positive gap in the claimed cross-system record-class propagation.

References:

- `tests/test_kaf_delegation_contract.py:58-87`
- `tests/test_kaf_delegation_contract.py:107-141`
- `src/acp/orchestration/headless_tasks/kaf_delegation_pipeline.py:427-454`

Required correction: add a negative contract case proving a record-class
mismatch is rejected before procedure execution, then make the positive test
derive the context expectation from the delivered event or otherwise bind both
values through the same contract fixture.

## API Contract Check

No test-only API shape divergence was found. The KAF fixture uses the internal
`KafItsmContextClient` contract, whose concrete HTTP client unwraps ITSM's
standard `{code: 0, data: ...}` envelope before returning a dictionary. Its
camelCase context fields (`taskId`, `recordClass`, `allowedActions`,
`expectedVersion`, and `workItem`) match `KafTaskContext` in ITSM. The test
does omit context fields not consumed by the pipeline, which is compatible with
the current client protocol but is not an end-to-end HTTP contract assertion.

## Verification Evidence

- `cd itsm-backend && go test ./service -run 'TestSSLVPN(Request|Incident)_' -count=1 -v` -> passed (2 tests).
- `ENV_FILE=/dev/null DEBUG=true PYTHONPATH=src /home/administrator/actions-runner/_work/kaf/kaf/.venv/bin/python -m pytest tests/test_kaf_delegation_contract.py tests/test_kaf_delegation_pipeline.py -q` -> passed (`19 passed, 1 skipped`).
- `git diff --check` passed for both reviewed commit ranges.
