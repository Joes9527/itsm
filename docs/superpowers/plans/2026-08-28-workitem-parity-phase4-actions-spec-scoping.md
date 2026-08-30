# WorkItem 详情页能力对齐 · Phase 4：Incident/Problem/Change actions 计算 — 设计先行 Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:brainstorming to work through the
> open design questions in Task 1 with the user BEFORE writing the follow-up spec in Task 2. This
> plan does not produce shippable code — see "Why this phase looks different" below. Do not attempt
> to implement `BuildIncidentActions`/`BuildProblemActions`/`BuildChangeActions` from this document;
> none of the task/action lists below are authoritative until Task 2's spec is written and reviewed.

**Goal:** Produce a reviewed design spec for backend-computed `actions` maps
(`Record<string, ActionPermission>` — allowed/reason per action) for Incident, Problem, and Change
detail responses, so `WorkItemShell`'s action bar (built in Phase 3) has real data instead of the
`actions={{}}` every page currently hardcodes.

**Architecture:** N/A — this phase is design work, not implementation. See "Why this phase looks
different."

**Tech Stack:** N/A for this phase.

**Spec:** `docs/superpowers/specs/2026-08-28-work-item-detail-page-parity-design.md` §5.3, §7 point 4
(states this item's "工作量待评估，可能需要拆成独立 spec").

## Why this phase looks different from Phases 1-3

Phases 1-3 could be planned in full, bite-sized, TDD detail because the design spec already made
every decision an implementer needs: exact resource-name mapping, exact route list, exact backfill
semantics, exact component boundaries. Phase 4 is different — the design spec explicitly declines to
make those decisions (§7 point 4: "工作量待评估，可能需要拆成独立 spec"), and research done while
writing this plan confirmed why that deferral was correct, not just cautious:

- Ticket's existing `actions` map (`itsm-backend/service/ticket_authorization.go`) is **six pure
  RBAC gates** (`approve`/`reject`/`assign`/`edit`/`cc`/`delete`) — each one just checks
  "is-requester exclusion + terminal-status guard + `middleware.HasResourcePermission`." There is no
  status-transition logic in it at all.
- Incident/Problem/Change do **not** fit that shape. Each domain already has its own status
  transition table (`common.IsValidIncidentStatusTransition`,
  `handlers/problem/service.go:isValidProblemStatusTransition`,
  `service.IsValidChangeStatusTransition`) that's structurally different from the other two (Change's
  is even type-aware — standard/emergency/normal changes have different legal transitions). None of
  these three tables currently expose "what actions are available from this status," only "is this
  proposed transition legal."
- A meaningful chunk of the buttons these three domains render today aren't status transitions at
  all: 升级 (escalate), 升级为重大事件 (mark-major-incident), 转为问题 (convert-to-problem,
  gated on `!incident.problemId`, unrelated to status), 指派 (assign), 提交审批
  (submit-for-approval), and Change's 保存风险评估/影响分析/回滚计划 — each has its own ad-hoc
  eligibility rule, not a shared "next legal status" table.
- The frontend side isn't just "populate a prop" either: `IncidentDetail.tsx`, `ProblemDetail.tsx`,
  and `ChangeDetail.tsx` each have their own bespoke, independently-written action-button block
  (`IncidentDetail.tsx:572-621`, `ProblemDetail.tsx:124-160`, `ChangeDetail.tsx:298-329` plus
  `82`/`221-245`) that inline their own `status === X` conditionals — none of them read from
  `useWorkItemContext()` today. Wiring real `actions` through means refactoring three different
  button blocks, not just changing one prop value from `{}` to something real.

Writing bite-sized TDD tasks against this today would mean inventing the action verb list and each
action's eligibility rule myself, silently making a product/business decision that belongs to
whoever owns Incident/Problem/Change process design — exactly what the design spec's own author
declined to do. This plan instead produces the spec that decision needs, using
`superpowers:brainstorming`.

## Global Constraints

- Do not implement `BuildIncidentActions`/`BuildProblemActions`/`BuildChangeActions` or touch
  `IncidentDetail.tsx`/`ProblemDetail.tsx`/`ChangeDetail.tsx`'s button blocks as part of this plan —
  that's the follow-up plan Task 3 schedules, not this one.
- The follow-up spec (Task 2's deliverable) must reuse the existing `dto.ActionPermission` shape
  (`Allowed bool`, `Reason string`) and the existing `WorkItemActionState`/`WorkItemContextValue`
  frontend contract (`itsm-frontend/src/components/work-item/WorkItemTypes.ts`,
  `WorkItemContext.tsx`) — both are already locked in as the cross-domain action contract by Phase 3
  and by the pre-existing Ticket/ServiceRequest precedent (`service/ticket_authorization.go`,
  `handlers/service_request/handler.go:103-105`). Do not propose a different response shape.

---

## Task 1: Brainstorm the open design questions with the user

**Files:** none — this is a conversation, not a code change.

**Interfaces:** none.

- [ ] **Step 1: Invoke `superpowers:brainstorming`**

Use the `superpowers:brainstorming` skill (not ad hoc questions) to work through, at minimum, these
open questions the research for this plan surfaced — each one is a real product decision, not
something derivable from existing code:

1. **Action inventory per domain.** For each of Incident/Problem/Change, which buttons currently
   rendered client-side (see the file:line list in "Why this phase looks different" above) become
   backend-computed `actions` entries, and what's each one's canonical action-name key (e.g. is it
   `escalate` or `escalate_incident`)? Some candidates are genuinely Ticket-shape RBAC gates
   (`edit`), some are status-transition-shaped (`resolve`, `close`, `reopen`, `start_processing`,
   `approve`, `reject`, `start_implementation`, `complete_implementation`), and some are neither
   (`convert_to_problem`, `mark_major_incident`, `assign`, `submit_for_approval`,
   `save_risk_assessment`) — confirm the shape-classification for each one, since it determines
   which of Task 2's eligibility-predicate patterns applies.
2. **Eligibility source of truth.** For the status-transition-shaped actions, should the new
   `CanXxx` predicates *call into* the existing `IsValidXxxStatusTransition` tables (treating them as
   the source of truth for "next legal status," with the predicate just checking whether the specific
   target status the action implies is in that set), or should they define independent eligibility
   rules that happen to usually agree with the transition tables? This determines whether the three
   existing transition tables become shared infrastructure for this feature or stay untouched.
3. **RBAC resource names.** Confirm `middleware.HasResourcePermission` is callable with
   `"incident"`/`"problem"`/`"change"` as a resource name today (the same three resource names Phase
   1's `resourceForRecordClass` produces) — i.e. that RBAC permission rows for these resources
   already exist in the permission model, not just as a routing concept.
4. **Frontend refactor scope and sequencing.** Should `IncidentDetail.tsx`/`ProblemDetail.tsx`/
   `ChangeDetail.tsx`'s existing inline button blocks be refactored to read from
   `useWorkItemContext().actions` in the same change that adds backend computation, or should the
   backend land first (additive, `actions` populated but unread) with the three frontend refactors
   as separate follow-up changes per domain? Given each is a genuinely different, non-trivial
   component refactor, splitting may be the better call — but that's exactly the kind of "is this one
   PR or four" scope decision brainstorming should settle before a plan locks in task boundaries.
5. **Rollout order across the three domains.** Ship Incident, Problem, and Change's `actions`
   computation as one combined change, or three independent ones (each independently testable and
   revertable, matching this whole design's "每阶段独立验证，不合并到一个大 PR" principle from §7
   point 5)? Given how different each domain's transition table already is, three independent
   sub-phases is the more consistent choice with how Phases 1-3 were structured — but confirm rather
   than assume.

- [ ] **Step 2: Capture the outcome**

The brainstorming session's conclusions become the input to Task 2 — don't proceed to writing the
spec until these five questions have real answers, not placeholders.

### Brainstorming decisions log (updated as the conversation progresses)

**Confirmed — eligibility source of truth (settles open question 2):** `AGENTS.md:33` ("itsm-frontend
... must not duplicate backend business rules or infer authorization from UI state") and `AGENTS.md:61`
("When a new path replaces an old path, remove the old path in the same change unless backward
compatibility is an explicit requirement") together settle this: the new `CanXxx` predicates MUST
delegate to the existing `IsValidXxxStatusTransition` tables as the sole source of truth for "next
legal status" — they must not define independent eligibility rules that merely happen to agree.

**Confirmed — old client-side logic must be removed, not left parallel:**
`itsm-frontend/src/lib/utils/workflow-state-machine.ts` contains an independent, already-drifted set
of Incident/Change transition tables (different status vocabulary/casing than the real backend
tables) that `IncidentDetail.tsx` currently uses for a client-side pre-check before calling resolve.
Per `AGENTS.md:61`, when a domain's frontend button block is refactored to read
`useWorkItemContext().actions`, that domain's corresponding dead/parallel logic in
`workflow-state-machine.ts` must be deleted in the same change — not deferred.

**Confirmed — Problem's authoritative status vocabulary wins over the frontend's current (incorrect)
one:** The frontend's Problem buttons (`ProblemDetail.tsx`) currently transition through
`open → in_progress → resolved → closed`, but the real backend table
(`handlers/problem/service.go:isValidProblemStatusTransition`) uses
`open → investigating/identified → resolved → closed`, treating `in_progress` only as a legacy-compat
bucket. Per the same AGENTS.md principle, the new design must use the real backend vocabulary — the
frontend's target-status values for these buttons need correcting as part of this work, not
perpetuated.

**Confirmed — Change `rollback`/`cancel` and Incident `cancel` are OUT OF SCOPE for this phase.**
Research found: (a) Change rollback's actual implementation only flips the `status` column to
`rolled_back` — it never reads the free-text "回滚计划" field, restores no state, and produces no
audit record (the repo-wide `AuditMiddleware` is never actually registered anywhere); (b) neither
Change rollback nor Change cancel nor Incident cancel has ever had a frontend UI trigger —
`ChangeApi.rollbackChange`/`cancelChange` are called only from tests; Incident has no dedicated
cancel route at all; (c) no document anywhere in the repo explains the operator-facing decision
criteria for rollback vs. `failed` vs. `cancelled`. Building action-bar buttons for these now would
be inventing new product capability, not surfacing an existing one that just lacks a permission gate
— out of this phase's stated goal. Left for a future, separately-designed phase if/when the product
actually needs it.

**Correction to the plan's own initial shape-classification:** `submit_for_approval` (Change,
`draft → submitted`) is status-transition-shaped, not "neither RBAC nor transition" as this plan
text originally guessed in Step 1 question 1 — `draft → submitted` is a legal entry in
`service.IsValidChangeStatusTransition`'s own table.

**Confirmed — `escalate` (Incident) is ad-hoc-shaped, not a status transition:**
`IncidentService.EscalateIncident` (`itsm-backend/service/incident_service.go:926-966`) never touches
the `status` column — it only writes `escalation_level`/`escalated_at`, gated by "not terminal
(closed/cancelled) + requested level > current level." Same shape category as `mark_major_incident`/
`convert_to_problem`: ad-hoc eligibility, unrelated to the top-level status-transition table.

**Confirmed — RBAC granularity: reuse existing coarse permissions, do not add fine-grained rows.**
The new `CanXxx` predicates check the existing coarse `incident:write`/`problem:write`/`change:write`
permissions as the outer gate (mirroring `ticket_authorization.go`'s pattern of checking coarse
`ticket:update` for e.g. `CanEdit`) — no new permission rows added to the seeder for
resolve/close/assign/escalate/etc. What actually decides whether a specific button is clickable right
now is the status-transition table or the action's own ad-hoc rule, not a dedicated permission per
action. This phase's goal is moving existing button-visibility logic from frontend `status===X`
checks to backend-computed `actions` — not redesigning the three domains' RBAC model, which is a
separate product decision.

**Confirmed — per-domain, backend computation + frontend refactor + old-logic removal land in the
SAME change, not split into a backend-first/frontend-later sequence.** Per `AGENTS.md:61` ("remove
the old path in the same change unless backward compatibility is an explicit requirement") — there is
no compatibility requirement here (`workflow-state-machine.ts`'s per-domain tables are pure
client-side pre-checks with no external contract), so each domain's change bundles: new
`BuildXActions` + that domain's button-block refactor to read `useWorkItemContext().actions` + deletion
of that domain's now-dead parallel logic in `workflow-state-machine.ts`, all together, verified as one
unit.

**Confirmed — rollout order: three independent, separately-revertable changes (one per domain), same
cadence as Phases 1-3.** No cross-domain code dependency exists (`BuildIncidentActions` shares nothing
with Problem/Change), so each domain ships, is reviewed, and can be reverted independently. Also
confirmed: this feature is pure read-time computation over existing columns (status, permission,
escalation_level, problemId, etc.) — there is no new schema field and no historical-data migration/
backfill step required for any of the three domains, unlike Phase 2's `ticket_comments` backfill.

**Confirmed — per-action eligibility must mirror the REAL server-side method's actual guard, not
naively the generic per-domain transition table when the two disagree.** Deep-dive research into
every current Incident action button surfaced concrete backend/frontend mismatches that the new
design must resolve, not preserve:
- **`resolve`:** `IncidentService.ResolveIncident` (`incident_service.go:1399-1450`) only accepts
  `in_progress → resolved` per the shared table — but the frontend currently shows the button for
  7 other statuses that would be rejected by the backend today. Fix: `CanResolve` reflects the real
  `in_progress`-only rule.
- **`close`:** matches already (`resolved`-only, both sides agree). No change needed.
- **`reopen`:** `IncidentService.ReopenIncident` (`incident_service.go:1680-1710`) is a hand-written
  guard that does NOT call the shared `IsValidIncidentStatusTransition` table at all — it independently
  allows `resolved|closed → in_progress`, contradicting the shared table's claim that `closed` is
  terminal. The frontend's current button visibility already matches this method's real behavior.
  Resolution: `CanReopen` must mirror `ReopenIncident`'s own bespoke logic (`status ∈ {resolved,
  closed}`), not the generic table — establishing the general principle that "authoritative source"
  means the actual method the action calls, which is usually but not always the shared per-domain
  transition table.
- **`assign` / `convert_to_problem`:** `AssignIncident` and `CreateProblemFromIncident` have NO status
  guard in the backend at all today (assign would succeed even on a closed incident; convert-to-problem
  has no dedup check and could create a second Problem from an already-converted Incident). The
  frontend currently supplies the only real-world restriction (`status ∉ {resolved, closed}` for
  assign; `status !== closed && !problemId` for convert). **Decision: codify the frontend's existing
  restrictions into the new `CanAssign`/`CanConvertToProblem` as the backend rule** — this both
  preserves current UX and closes a latent duplicate-Problem-creation gap, rather than "honestly"
  exposing the backend's current unrestricted behavior as newly-official.
- `mark_major_incident`/`escalate` (level-increment): already match between frontend and backend
  guards (or "升级" needs a small widening — currently always shown regardless of status, but
  `EscalateIncident` rejects `closed`/`cancelled`; `CanEscalate` should reflect that real restriction).

**Confirmed — Problem/Change findings from the same audit, all three resolved:**
- **Problem `open → in_progress` ("开始处理") is 100% broken today** — `in_progress` is never a legal
  *target* in `isValidProblemStatusTransition` (only a legacy-compat *source* bucket); every click
  is guaranteed to be rejected by the backend, and `itsm-frontend/src/constants/problem.ts:9` already
  marks `IN_PROGRESS` `@deprecated 仅用于兼容历史数据，新请求使用 INVESTIGATING`. **Decision:** rename
  the action key from `start_processing` to `start_investigation`, targeting the real legal next
  state `investigating` (`open → investigating`). The other two Problem buttons (`in_progress →
  resolved`, `resolved → closed`) are unaffected — they already hit legal transitions.
- **Problem's dedicated `investigate`/`root-cause`/`solution`/`close` endpoints are explicitly OUT OF
  SCOPE.** They exist server-side (`handlers/problem/service.go:135-164`, wired to real routes) but
  every corresponding frontend API client method is a stub that unconditionally throws `'功能开发中'`
  (`itsm-frontend/src/lib/api/problem-api.ts:166-189`) — wiring these up is a separate, larger feature
  (real forms for root-cause/solution capture), not part of computing `actions` for buttons that
  already exist and work today.
- **Change `start_implementation` must be type-aware, not just status-aware** —
  `approved → in_progress` is legal for `standard`/`emergency` change types but genuinely **illegal
  for `normal`** (`normal`'s `approved` bucket only allows `{scheduled, cancelled}` per
  `service/change_service.go`'s per-type table) — today's frontend button ignores `change.type`
  entirely and would let a user click a guaranteed-to-fail action. **Fix:** `CanStartImplementation`
  must branch on `c.Type` the same way the underlying `IsValidChangeStatusTransition` does.
- **Confirmed gap, decision made — Change gets an explicit, unconditional self-approval block, closing
  a real (if narrow) protection gap.** Unlike Ticket's `CanApprove` (unconditional `RequesterID ==
  actor.UserID` check), Change today relies only on a BPMN-side candidate-list exclusion that is
  conditional (skipped if the node hardcodes `candidateGroups`/`candidateUsers`, or if the
  `requester_id` process variable fails to resolve) — meaning self-approval is possible in some real
  paths today. **Decision (Option A, adopted):** add an unconditional `CreatedBy == actor.UserID` check
  to the new `CanApprove`/`CanReject` for Change, mirroring Ticket's `isRequester` pattern exactly,
  independent of whatever the BPMN engine's candidate-exclusion does or doesn't catch.
- **Confirmed scope boundary — Change's approval eligibility stays status-only, no live-BPMN-task
  query added.** `TransitionStatus`'s actual approve/reject call additionally requires a genuinely
  pending `Activity_CABApproval` BPMN task to exist (`completeChangeApprovalTask`,
  `service.go:667-737`) — a data-integrity edge case (`submitted` status but no live task) could show
  `CanApprove.allowed=true` yet still fail on click. **Decision (Option A, adopted):** do not add an
  extra BPMN-liveness query for this phase — match the depth Ticket's own `CanApprove` uses (status +
  permission only, no process-instance check beyond `CanDelete`'s narrow special case), keeping this
  phase's scope to "reflect the real status/type rule," not "eliminate every rare click-time failure."
- **Change's `assign` button doesn't exist in the current UI at all** (`AssignChange` has no status
  guard server-side either, mirroring Incident's gap, but there is no frontend trigger to preserve or
  fix) — out of scope, not part of this phase's action inventory for Change.
- **Problem `close` stays narrower than what the backend table would technically permit.** The table
  allows `closed` as a target from `open`/`investigating`/`identified`/`resolved`, but the frontend
  today only ever shows "关闭问题" from `resolved`. **Decision (Option A, adopted):** `CanClose` matches
  today's `resolved`-only frontend gate — skipping straight to closed from an earlier investigation
  stage is a new product capability, not something to open as a side effect of this refactor.

All Task 1 open questions are now resolved. Proceeding to Task 2 (writing the design spec).

---

## Task 2: Write the follow-up design spec

**Files:**
- Create: `docs/superpowers/specs/<date>-incident-problem-change-actions-design.md` (exact filename
  chosen at write time per the repo's existing `docs/superpowers/specs/YYYY-MM-DD-<slug>.md`
  convention — see e.g. `docs/superpowers/specs/2026-08-28-work-item-detail-page-parity-design.md`
  itself)

**Interfaces:** none — this produces a document, not code.

- [ ] **Step 1: Write the spec**

Using Task 1's brainstorming outcome, write a design spec with the same rigor as
`2026-08-28-work-item-detail-page-parity-design.md` — concrete action inventory per domain (not
"TBD"), concrete eligibility rules per action (either "delegates to `IsValidXxxStatusTransition`
checking target status Y" or the specific ad-hoc predicate, spelled out), the exact `BuildXActions`
function signatures per domain, and where each gets wired into the response (mirroring
`ToTicketResponseWithCustomFieldsAndActions` in `itsm-backend/service/ticket_service.go:1487-1498`
as the existing precedent).

- [ ] **Step 2: Review with the user**

Confirm the spec is approved before treating it as ready for a `writing-plans` pass — same gate this
phase's own parent spec went through.

---

## Task 3: Write the implementation plan(s) from the new spec

**Files:** none yet — this task's deliverable is one or more new files under
`docs/superpowers/plans/`.

**Interfaces:** none.

- [ ] **Step 1: Invoke `superpowers:writing-plans` against the Task 2 spec**

Once Task 2's spec is approved, run the `writing-plans` skill against it the same way this document
was produced — likely yielding either one combined plan or three domain-scoped plans (Incident
actions, Problem actions, Change actions), per whatever Task 1's question 5 concluded. This task is
listed here for completeness of the roadmap; do not pre-write those plans now — they depend on
decisions Task 1/2 haven't made yet.
