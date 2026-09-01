# SDD ledger — plan: docs/superpowers/plans/2026-09-01-bpmn-callback-effect-notification-contract.md

Base: 71e584326136e2531207d108079bdb4eb6feed27
Spec: docs/superpowers/specs/2026-09-01-architecture-hardening-agent-platform-evolution-design.md

## Preflight task consistency

| Tasks | Producer / consumer or shared surface | Finding |
|---|---|---|
| 1 | Callback effect value object | Self-consistent and independently testable. |
| 2 | Handler interface/action contracts | Must be committed atomically with Task 8 as the plan requires. |
| 3 | Strict optional metadata and migration 021 asset | Schema/script portion independent; registry substep waits for P1-A migration 020. |
| 4 | Strict enqueue plan | Load-bearing dependency on the pure action-contract types assigned to Task 2 creates a cycle with Task 8. |
| 5 | Notification delivery result | Self-consistent; controller is updated in the same task so signature changes compile. |
| 6 | Callback outcome/audit/metrics | Self-consistent; focused audit file avoids P1-C's audit-service hotspot. |
| 7 | Strict API/frontend eventType contract | Sequentially consumes Task 5's real service summary. |
| 8 | Engine effect gate | Must consume Tasks 1, 4, 6 and the Task 2 handler diff after P1-C. |
| 9 | Release gates | Self-consistent; verifies backend, frontend, deletion, and migration boundaries. |
| 1 → 2/4/6 | CallbackEffect and block-code allowlist | Ordered and single-source. |
| 2 ↔ 4 ↔ 8 | action-contract types, enqueue planning, handler/engine integration | Circular as written; ruled below by moving only the pure type declarations earlier. |
| 3 → 4/6/8 | optional_declared snapshot | Ordered; no extra outbox effect columns. |
| 5 ↔ 7 | DTO/controller/frontend | Sequential; no compatibility request aliases. |
| 2 ↔ 5 | ticket_handler.go | Same-plan sequential touch; final Task 2+8 removes ServiceTaskResult entirely. |
| 6 → 8 | terminal outcome policy | Ordered; engine interprets rather than duplicates policy. |
| 1-8 → 9 | complete callback/notification contract | Release gate covers all outputs. |

Ruling: Task 1 additionally creates only the behavior-free `CallbackActionContract` and `CallbackContractProvider` declarations; Task 2 still owns every handler implementation, action table test, old-policy deletion, and the atomic Task 8 commit — this breaks the Task 2/4/8 cycle without a compatibility path — cost if wrong: move two type declarations during the Task 2+8 integration and re-run Task 4 tests.
Ruling: Task 3's schema/generated-code/script commit proceeds now, but canonical `migrations.go` registration remains incomplete until P1-A migration 020 is integrated — this preserves migration order — cost if wrong: migration numbering/parity test rework on the integration branch.
Ruling: Tasks 2 and 8 are one SDD implementation/review unit because the approved plan forbids publishing a red intermediate interface revision — cost if wrong: a larger review package and longer fix cycle.
Ruling: P1-D does not edit `bpmn_process_engine.go` until the reviewed P1-C head is available — this preserves lifecycle/auth changes — cost if wrong: engine integration waits on P1-C critical path.
Ruling: the completed P1-D planning agent is reused as Task 1 implementer because the collaboration thread cap rejected a fresh third child; the isolated brief/report and an independent task reviewer remain mandatory — cost if wrong: plan-author bias may require an additional review fix round.

Baseline backend: `cd itsm-backend && go mod download && go test ./... -count=1` passed at 71e58432.
Baseline frontend: `npm ci` succeeded with pre-existing audit/deprecation warnings. `npm test -- --runInBand` failed before implementation: 2 suites failed, 211 passed; 49 tests failed, 13 skipped, 3319 passed. Failures are confined to `src/lib/api/__tests__/template-api.test.ts` (expected `/api/v1/templates`, implementation uses `/api/v1/tickets/templates`) and `src/lib/api/__tests__/sla-api.test.ts` (camelCase expectation versus snake_case request params); neither overlaps P1-D files.
Ruling: proceed with P1-D using its focused notification tests plus type-check/build gates while preserving the two baseline frontend failures as comparison evidence — the failures are unrelated and execution skill requires progress rather than repairing out-of-scope code — cost if wrong: a final full-suite regression could be masked unless the same two-suite baseline is explicitly diffed at release gate.

Ruling: the repository's three-child concurrency cap requires rotating agents between implementation and independent review; a reviewer may have implemented a different plan but never the task it reviews — independence is preserved at the task boundary — cost if wrong: less context isolation than a newly spawned reviewer, offset by the immutable brief/report/diff gate.

Task 1 fix round 1: Important — callback effect evidence aliases caller-owned mutable maps; constructors must defensively snapshot evidence and tests must prove post-construction caller mutation cannot change the effect.

Task 1: complete (commits 71e58432..35c5d421, review clean after fix round 1)
Ruling: Task 2 remains dependency-gated on reviewed P1-C engine integration, so execution advances to the first ready independent item, Task 3 — this preserves the plan's dependency graph rather than its numeric order — cost if wrong: ledger completion order is non-sequential but explicit.

Task 3 fix round 1: Important — retain migration 021 reset and verify scripts in addition to apply.
Task 3 fix round 1: Important — duplicate `callback_optional` metadata is ambiguous and must fail closed for both User Task and Service Task, with regression tests.

Task 3: complete (commits 35c5d421..d48fda4b, review clean after fix round 1)

Task 4 fix round 1: Important — `ConfigRef` lacks contract-declared requirement/format/length validation and survives blocked plans; validate it as a bounded non-secret reference before enqueue and strip it from blocked diagnostic output.

Task 4: complete (commits d48fda4b..1e53883f, review clean after fix round 1)

Ruling: Task 5 is split at the frozen handler-interface boundary: Task 5A now replaces the ordinary notification service/controller/caller contract; `service/bpmn/ticket_handler.go` and its effect-mapping tests move into the already-atomic Task 2+8 unit where `Execute` changes from `ServiceTaskResult` to `CallbackEffect` — do not add a transitional wrapper, unused mapper, or blocked-as-error compatibility behavior — cost if wrong: the Task 5 review must explicitly treat handler mapping as dependency-deferred and the final 2+8 review package is larger.

Ruling: Task 5A is superseded after focused compilation proved bootstrap cannot accept the new notification signature while the legacy BPMN handler interface remains frozen; defer the entire Task 5 into the atomic Task 2+5+8 cutover, save the uncommitted attempt as a recoverable ignored SDD diff, and restore only Task-5-owned files to HEAD — this avoids an adapter/second method/Success bridge — cost if wrong: notification work is re-applied after P1-C and the atomic review grows.

Task 5: dependency-deferred (no commit; recoverable WIP at `task-5-deferred-wip.diff`; baseline restored and green)
Ruling: proceed to ready Task 6 while Tasks 2+5+8 wait for P1-C engine release — Task 6 consumes only reviewed Tasks 1 and 3 contracts and does not edit the engine — cost if wrong: non-sequential ledger order, explicitly recorded.

Task 6 fix round 1: Important — live PostgreSQL lease-recovery executor still returns a nil effect, so the mandatory effect contract terminally blocks and breaks the completion/fencing gate; return explicit applied/idempotent evidence while preserving the completion-failure barrier.
Task 6 fix round 1: Minor accepted for repair — add worker-level evidence that terminal outcome metrics increment exactly once across restart/redelivery.

Task 6: complete (commits 1e53883f..4336ce68, review clean after fix round 1)
Ruling: Task 7 also depends on the deferred authoritative notification API and therefore joins the atomic Task 2+5+7+8 cutover after P1-C release — this avoids implementing a frontend contract against a backend bridge — cost if wrong: larger atomic integration/review, but no temporary API duality.

Ruling: P1-C conditionally releases its frozen engine boundary at 66388f33 after every scoped gate passed; cherry-pick its reviewed commits before atomic Task 2+5+7+8, preserve its signatures/CAS behavior, and carry the pre-existing Incident detached-rule race as a final integration blocker — cost if wrong: rebase the atomic unit after Incident ownership lands.

Atomic Tasks 2+5+7+8 fix round 1: Critical — CAB existence-only, stakeholder log-only, and Incident no-assignee callbacks report `applied` without durable effect, allowing token advance; return typed `blocked` unless the BPMN definition declared the step optional.
Atomic Tasks 2+5+7+8 fix round 1: Important — Release approval returns `idempotent` without proving the same-tenant/process/node approval decision; absence or mismatch must block.
Atomic Tasks 2+5+7+8 fix round 1: Important — handler retry branches with already-present target state report `applied`; use `idempotent` and preserve effect-before-ack semantics.
Atomic Tasks 2+5+7+8 fix round 1: Minor — complete notification strict-contract and frontend interaction regression matrix for legacy/missing/invalid `eventType`, real summary, options, failure, and refresh behavior.

Atomic Tasks 2+5+7+8 fix round 2: Critical — CAB callback still returns `idempotent` after only a Change existence check; prove the same-tenant/process/task/node/action `ProcessApprovalDecision` as for Release, block absence/mismatch, and prove a non-optional CAB node cannot advance on empty completion variables.

Atomic Tasks 2+5+7+8 fix round 1: complete pending independent review. Handlers now distinguish actual writes (`applied`), no-write redelivery (`idempotent`), and unavailable/undeclared effects (`blocked`); Release approval verifies the existing immutable decision by trusted tenant and persisted task identity; strict notification backend/frontend regressions and the non-optional engine gate are green. Full backend and frontend type-check passed. `junit.xml` remains intentionally unstaged.

Atomic Tasks 2+5+7+8 fix round 2: complete pending independent review. Change CAB now shares the persisted ProcessApprovalDecision proof with Release (tenant/process/task/node/action), blocks empty/missing/mismatched evidence, and never writes approval state; a non-optional empty CAB completion terminally blocks without advancing. Focused Change/CAB/outcome/KAF/CAS and full backend tests passed. `junit.xml` remains unstaged.
