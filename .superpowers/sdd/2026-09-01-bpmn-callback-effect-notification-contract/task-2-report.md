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
