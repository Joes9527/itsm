# Atomic Tasks 2, 5, 7, and 8 report

Implemented the single callback-effect contract cutover. All synchronous BPMN handlers now return validated `CallbackEffect` values and publish handler-owned action contracts; the legacy `ServiceTaskResult`/`Success` and payload-policy path are removed. Invalid declarations produce sanitized durable blocked outbox rows, while valid effects gate token advancement. KAF keeps its existing fenced delegated completion boundary.

Task 5 notification delivery now returns a durable summary, validates every recipient before writes, provides pair-atomic in-app delivery, and exposes idempotent delivery keys. Task 7 accepts only `userIds`, `eventType`, and `content` with strict JSON unknown-field rejection; the frontend uses the event type contract only. Task 8 snapshots definition-declared optionality, records blocked/optional terminal outcomes with sanitized audit and bounded metric labels, and advances synchronous service/user callbacks only after an applied/idempotent or engine-derived optional effect.

RED/GREEN evidence:

- `go test ./service -run 'Callback|Outbox|UserTask' -count=1` — PASS.
- `go test ./service -run 'TestExecuteAction_RealEngineCallbackFailureRecoversWithoutSecondBPMNCompletion' -count=1 -v` — PASS.
- `go test ./service -run 'TestReleaseFlow_(StageBridges|RejectApproval)' -count=1` — PASS.
- `go test ./service -run 'Test(CompleteTaskBlocksInvalidCCChannelDurably|CABApproval)' -count=1` — PASS.
- `go test ./service/bpmn -run TestReleaseHandler_ApprovalIsIdempotentAfterAuthoritativeServiceDecision -count=1` — PASS.
- `go test ./...` — PASS.
- `npm run type-check` — PASS.
- focused frontend Jest API test assertions passed, but its standalone invocation exited 1 because repository-global coverage thresholds are enforced; this is a test-runner evidence limitation, not an assertion failure.

Self-review: deleted legacy result/payload-policy identifiers; no compatibility callback bridge or second approval engine remains. P1-C async/KAF fencing and authorized-actor fixes on HEAD `4c082b85` are preserved. `itsm-frontend/test-results/junit.xml` is pre-existing and intentionally excluded from staging.

Concern: full frontend Jest was not run; the focused standalone Jest command cannot satisfy the repository global coverage threshold by design. Migration 021 registration remains deferred.

## Fix round 1

Implemented the reviewed effect-gate corrections without adding an approval or
notification compatibility path. CAB approval remains owned by the professional
state machine and now reports `idempotent` after only its tenant-scoped
existence check. Stakeholder notification has no durable delivery implementation,
so it returns typed `blocked` (`handler_contract`). Incident auto-assignment with
no assignee returns typed `blocked` (`target_missing`); the engine alone decides
whether a persisted definition-declared optional snapshot can turn that into an
optional skip. The non-optional engine regression proves the token remains at a
running instance.

Release approval now verifies the existing `ProcessApprovalDecision` by trusted
tenant context and persisted ProcessTask identity: tenant, process instance,
task, definition node, and completed-task `approvalAction`. Missing task/action,
missing decision, and action mismatch block; a matching immutable decision is
`idempotent`. No release state is derived or written by the BPMN handler.

The Change handler reports `idempotent` for redeliveries that observe already
rejected, in-progress, verified, closed, or scheduled state and makes no write;
actual transition/date writes remain `applied`.

Notification strict-contract regressions cover rejection of legacy `type` and
`channel`, missing/unknown `eventType`, the public real-delivery summary, UI
definition-provided event type options, no legacy request fields, failure without
a fake notification, and successful refresh from persisted server state.

RED/GREEN evidence:

- RED: `go test ./service/bpmn -run 'Test(IncidentServiceTaskHandler_AssignIncident_NoAssignee_BlocksEffectGate|ReleaseHandler_ApprovalWithoutAuthoritativeDecisionBlocks|ChangeServiceTaskHandler_NotifyStakeholdersWithoutDurableDeliveryBlocks)' -count=1` failed exactly because all three effects were `applied`/`idempotent` rather than typed `blocked`.
- RED: `go test ./service/bpmn -run TestChangeServiceTaskHandler_RetryWithoutWriteIsIdempotent -count=1` failed for reject, implement, verify, close, and schedule because each returned `applied`.
- GREEN focused: `go test ./service/bpmn -run 'Test(ChangeServiceTaskHandler_(TenantScopedActions|ScheduleChangeAction_EmergencyStopsAtApproved|RetryWithoutWriteIsIdempotent)|ReleaseHandler_|IncidentServiceTaskHandler_AssignIncident_NoAssignee_BlocksEffectGate)' -count=1` — PASS.
- GREEN engine gate: `go test ./service -run TestHandleElement_ServiceTask_IncidentAutoAssign_NoAssignee_BlocksNonOptionalFlow -count=1` — PASS.
- GREEN controller strict contract: `go test ./controller -run 'TestTicketNotificationController_Send(RejectsLegacyFields|UsesStrictEventTypeContract)' -count=1` — PASS.
- GREEN frontend: `npm test -- --runInBand --coverage=false src/components/business/__tests__/TicketNotificationSection.test.tsx` — PASS, 6 tests. The run emits the pre-existing Ant Design `List` deprecation warning.
- `go test ./...` — PASS.
- `npm run type-check` — PASS.

Self-review: `git diff --check` is clean; Release decision predicates use only
persisted task data and trusted tenant context, no request-derived identity.
The retained P1-C CAS/KAF engine code was not modified. `junit.xml` remains
unstaged.

## Fix round 2

Change CAB approval now uses the same shared persisted-decision proof as
Release: it reads only the trusted tenant context and the completed
`ProcessTask`'s persisted `approvalAction`, and requires a matching immutable
`ProcessApprovalDecision` on tenant, process instance, task, node, and action.
Missing task/action/fact or action mismatch returns typed `blocked`; the handler
does not write Change status or introduce another approval lifecycle. The helper
is a real shared fact-query boundary used by both Release and Change handlers.

RED/GREEN evidence:

- RED: `go test ./service/bpmn -run TestChangeServiceTaskHandler_ApproveRequiresMatchingPersistedDecision -count=1` failed because a missing decision returned `idempotent`.
- GREEN handler: `go test ./service/bpmn -run 'Test(ChangeServiceTaskHandler_(TenantScopedActions|ApproveRequiresMatchingPersistedDecision|RetryWithoutWriteIsIdempotent)|ReleaseHandler_ApprovalRequiresMatchingPersistedDecision)' -count=1` — PASS.
- GREEN engine/outcome/KAF/CAS: `go test ./service -run 'Test(UserTaskWithServiceTaskTypeMetadataTriggersCallback|CABApproval|BPMNCallbackOutbox.*|.*KAF.*|.*CAS.*)' -count=1` — PASS. The CAB empty-completion regression proves its persisted non-optional callback row terminates blocked without token advance; existing optional-outcome coverage continues to permit skips only from `OptionalDeclared` on the durable row.
- `go test ./...` — PASS.

Self-review: no approval state was written by either handler; tenant-scoped
Change lookup remains fail-closed before decision proof; no KAF/CAS production
surface changed. `itsm-frontend/test-results/junit.xml` remains unstaged.
