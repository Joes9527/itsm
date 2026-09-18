# 🛣️ ITSM Roadmap

> **Source of truth for what is shipping, what is shipping next, and what
> is parked.** Updated as part of every release. Last synced: 2026-06-28.
>
> Cross-references:
> - PRD library: [prd/](./prd)
> - v1.0 GA readiness: [docs/v1-ga-readiness.md](./docs/v1-ga-readiness.md)
> - Architecture: [docs/architecture/](./docs/architecture)
> - Open issues & milestones: GitHub [Issues](https://github.com/heidsoft/itsm/issues) and [Projects](https://github.com/heidsoft/itsm/projects)

---

## 🎯 North Star

**Become the de-facto open-source AI-Native ITSM for enterprises that need
ServiceNow-class workflows without the lock-in or the footprint.**

Concretely that means:
1. **Process parity** with the ITIL v4 core (already at ~95% with v1.0 GA).
2. **AI that earns its seat** — classification, summarization, RAG, and
   impact analysis that are measurable, not vibes.
3. **Native integration surface** — Feishu / DingTalk / WeCom / Webhook
   ship as first-class connectors, not bolt-ons.
4. **Operational discipline** — coverage, observability, security, and
   release hygiene as defaults, not afterthoughts.

---

## 📅 Release Timeline

| Version | Target | Theme | Status |
|:---|:---|:---|:---|
| **v1.0 GA** | 2026-Q2 | ITIL core + AI-Native scaffolding + private deploy | ✅ Shipped |
| **v1.1**     | 2026-Q3 | Coverage backfill + connector marketplace v1 + RBAC hardening | 🟡 In progress |
| **v1.5**     | 2026-Q4 | Incremental coverage gate (60%) + AI evaluator v1 + Feishu/DingTalk | 🟢 Planned |
| **v2.0**     | 2027-Q2 | Coverage 70% + AI auto-triage GA + MSP billing + multi-region | 🔵 Roadmap |
| **v3.0**     | 2027-Q4 | Self-hostable AI inference + Plugin marketplace v2 + agent ecosystem | ⚪ Parked |

---

## 🟢 v1.0 GA — Shipped (2026-Q2)

**Theme:** Get the foundation right.

### Capability

- [x] **ITIL core flows** — ticket / incident / problem / change / release / service request
- [x] **Service catalog** — request templates, approval routing, SLA binding
- [x] **BPMN workflow engine** — process definitions, instances, user tasks,
      variable persistence, candidateGroups-driven approval (replaces the
      old dual-track approval system)
- [x] **CMDB v1** — CI types, configurations items, relationships, impact
      analysis, cloud discovery scaffold
- [x] **Knowledge base** — articles, versioning, RAG retrieval
- [x] **SLA** — multi-level policies, escalation matrix, alert rules
- [x] **AI capabilities (scaffold)** — Guidance-Harness-Skill framework,
      LLM Gateway, Triage / Summarize / KB skills
- [x] **RBAC + multi-tenant** — roles, permissions, menu gating, MSP mode
- [x] **Deployment** — Docker Compose (private / saas / saas_msp), GHCR
      images, multi-platform Release zip

### Quality

- [x] GA gate (4 checks): backend tests, frontend build, compose health,
      E2E smoke (11 core APIs)
- [x] Staticcheck + gofumpt + ESLint + tsc
- [x] Dependabot weekly scans
- [x] Security policy + Code of Conduct

### Debt that lands in v1.1

- 🟡 Backend coverage 2% → 40%
- 🟡 Backend controller files > 25k LOC need splitting
- 🟡 Ent schema `.bak` cleanup (handled in v1.0.x hotfix)
- 🟡 Connector marketplace: only Feishu/DingTalk/WeCom/Webhook stubs

---

## 🟡 v1.1 — In Progress (2026-Q3)

**Theme:** Cover the seams and harden the foundation.

### Engineering

- [ ] **Coverage backfill sprint** — bring `service/*` and `controller/*`
      packages from 2% → **40%** overall, focusing on ticket / incident /
      change / approval / auth (the user-facing critical paths)
- [ ] **Controller split** — break up `incident_controller.go` (45k),
      `ticket_controller.go` (27k), `cmdb_controller.go` (28k),
      `bpmn_workflow_controller.go` (30k) into feature-scoped sub-controllers
- [ ] **Integration test suite** — RBAC cross-tenant, BPMN happy paths,
      CMDB impact analysis, SLA escalation. Lives at `itsm-backend/tests/integration/`.
- [ ] **itsm-cli / itsm-skill / itsm-agent** in CI (path-scoped workflows
      + coverage)
- [x] **Unified WorkItem model (Incident/Problem/Change/ServiceRequest)** —
      Incident, Problem, and Change now create their `tickets` row
      (`record_class`) transactionally alongside the professional record;
      BPMN `businessId`/`businessKey` for all three converged onto the
      WorkItem id instead of each domain's own primary key; cross-record
      associations (Problem↔Ticket, Change↔Ticket) moved off ad hoc JSON/
      edges onto the structured `WorkItemRelation` table. ServiceRequest
      needed no equivalent change (`ticket_id` was already required from
      day one). Design: `docs/superpowers/specs/2026-08-26-unified-work-item-model-design.md`;
      execution record: `docs/superpowers/specs/2026-08-26-unified-work-item-multi-agent-execution-plan.md`.
      **Not yet done**: `ticket_number`'s unique constraint is still global
      instead of `(tenant_id, ticket_number)`, so the four generators that
      write into it (Ticket/Incident/Problem/Change, all counting per-tenant)
      can still collide with each other — tracked, not fixed. Phase 6
      physical cleanup (old approval-workflow fields on `ticket_type`,
      `tickets`→`work_items` rename decision) also still open.

### Product

- [ ] **Connector marketplace v1** — Feishu (IM + Approval), DingTalk
      (IM + Work Notice), WeCom (IM), Webhook. Lifecycles via
      `/api/v1/connectors/lifecycle`.
- [ ] **AI Audit console** — review every AI suggestion, accept/reject,
      feed back to evaluator.
- [ ] **Standard change templates** — pre-baked change templates for
      common ops (network, OS patch, DB migration).

### Quality

- [ ] **Incremental coverage gate** (60% on new/modified lines) — already
      landed via `coverage-diff.yml`.
- [ ] **Dependabot auto-merge** — patch-level updates auto-merge after
      green CI (handled in v1.0.x hotfix).

---

## 🟢 v1.5 — Planned (2026-Q4)

**Theme:** AI earns its seat, integrations go live.

### Engineering

- [ ] **AI Evaluator v1** — classification accuracy ≥85%, summarization
      ROUGE ≥0.6, RAG hit-rate ≥70%. Regression suite in CI.
- [ ] **AI telemetry** — capture prompt/response/cost/latency for every
      skill invocation; dashboard at `/api/v1/ai/audit`.
- [ ] **Knowledge base RAG v2** — chunking strategy improvements,
      re-ranking, hybrid search (BM25 + vector).
- [ ] **Skill registry v1** — declarative skill manifests, hot-pluggable
      pipeline, registry UI.

### Product

- [ ] **Feishu / DingTalk / WeCom native connectors** — end-to-end:
      account / approval / IM notification / webhook relay.
- [ ] **Auto-triage (human-in-the-loop)** — AI suggests category,
      assignee, SLA tier; engineer accepts with one click.
- [ ] **SLA forecast skill** — predict SLA breach risk per ticket,
      surface on dashboards.

### Quality

- [ ] **Backend coverage** 40% → **55%** overall.
- [ ] **Performance budgets** — k6 baselines for top 10 endpoints,
      enforced in CI.
- [ ] **Trivy + govulncheck** — daily scans, high-severity blockers.

---

## 🔵 v2.0 — Roadmap (2027-Q2)

**Theme:** MSP-friendly, AI-assisted, multi-region.

### Engineering

- [ ] **Coverage 55% → 70%**.
- [ ] **Service decomposition** — split monolithic `itsm-backend` into
      `core` + `workflow` + `ai` + `cmdb` services along bounded contexts.
- [ ] **Event-driven architecture** — Watermill is already in deps;
      promote to first-class pub/sub for incident events.
- [ ] **Multi-region active-active** — Redis Streams + region-aware
      routing.

### Product

- [ ] **MSP billing** — usage metering, invoicing, allocation reports.
- [ ] **AI auto-triage (full)** — replaces the human-in-the-loop step
      from v1.5 with confidence-based auto-accept.
- [ ] **Impact analysis skill** — given a change, predict affected CIs,
      tickets, and downstream SLAs.
- [ ] **Plugin marketplace v2** — signed plugins, sandboxed execution,
      revenue share for authors.

### Quality

- [ ] **SOC 2 Type II readiness** — control mapping, evidence collection,
      audit-ready logging.
- [ ] **Customer-managed keys (BYOK)** for LLM Gateway.

---

## ⚪ v3.0 — Parked (2027-Q4)

**Theme:** Self-hostable AI, agent ecosystem.

- Self-hostable LLM inference (Ollama, vLLM, llama.cpp) — drop the
  external OpenAI dependency for privacy-sensitive deployments.
- Agent marketplace — third-party agents that can act on the ITSM
  data model under strict RBAC.
- Mobile PWA with offline-first ticket intake.
- Multilingual UI (zh-CN baseline; en-US, ja-JP, ko-KR planned).

---

## 🛠️ Always-On Tracks

These don't belong to a single release; they ship incrementally:

### Testing & Quality

- Incremental coverage gate (60% on new code) — landed v1.1
- End-to-end smoke on every PR — landed v1.0
- Frontend visual regression — planned v1.5
- Property-based tests for critical parsers (BPMN XML, RAG chunking)
  — planned v1.5

### Security

- CodeQL + Trivy + govulncheck — landed v1.1
- **Follow-up (open): remove the time-boxed `GO-2026-6452` govulncheck exclusion.**
  `github.com/xuri/excelize/v2` has no fixed release yet (advisory published
  2026-09-16: affected range starts at `0` with no `fixed` event; v2.11.0, the
  newest release, is still affected). The workflow allows that single advisory and
  warns; every other advisory stays fail-closed. **Done when** a fixed excelize
  release exists and `.github/workflows/security.yml` runs plain
  `govulncheck ./...` again. Impact meanwhile: a malformed workbook can panic the
  operator-run CLI `itsm-backend/cmd/sync_ehr_master_data` (denial of service of
  that CLI only; not reachable from the API surface).
- Quarterly threat-model review
- Annual pen-test

### Open-Source Governance

- Issue triage SLA (48h first response, 14d close-or-fix) — landed v1.1
- Monthly community digest
- Quarterly maintainer rotation review

### Developer Experience

- `make dev-*` unified dev environment (already landed v1.0)
- `itsm-cli` for ops (deploy/seed/inspect) — landed v1.0
- `itsm-skill` for OpenClaw / Codex agents — landed v1.0
- Container image size reduction (distroless base) — planned v1.5

---

## 🧊 Backlog — Unscheduled (needs a product decision)

Work lines that are deliberately **not** scheduled into a release. Triage recorded
**2026-09-18**: the maintainer split the outstanding work lines into "continue"
(which keep a live worktree) and "backlog" (these entries). **Every branch ref
listed here is preserved**; recreate a working copy with
`git worktree add <path> <branch>` — none of them was deleted or rewritten.

### BL-GENERIC-FULFILLMENT-GATE — admission and completion gates for generic fulfilment

- **Outcome / persona:** process compliance plus first-line clarity. A generic
  WorkItem (`recordClass=generic`) fulfilled through BPMN must not be able to skip
  its process or close without the evidence that process requires, and an engineer
  must be able to see *why* a task cannot be completed yet.
  (Persona: process administrator, front-line engineer.)
- **Scope — seven candidate cuts, none merged.** Counts and conflict lists below were
  measured against `origin/main` on 2026-09-18; re-measure with the commands in
  *Evidence anchors* before relying on them, since `docs/README.md` moves on every
  documentation merge.

  | Branch | Business coverage | Commits | Against the superset | Merge into current main |
  |:---|:---|---:|:---|:---|
  | `codex/fix/a3-start-contract` | admit generic workflows from frozen creation evidence; reject reserved public inputs | 25 | fully absorbed | conflict: `docs/README.md` |
  | `codex/fix/a3-contract-pipeline` | validate the fulfilment definition contract (one process per lifecycle contract) | 21 | fully absorbed | conflict: `docs/README.md` |
  | `codex/fix/a3-lifecycle-gate` | lifecycle evidence gate before completion | 25 | adds later work | conflicts: `docs/README.md`, `dto/ticket_dto.go` |
  | `codex/fix/a3-legacy-gates` | reject legacy mutations for gated generic workflows | 24 | adds later work | conflicts: `docs/README.md`, `service/ticket_service.go` |
  | `codex/fix/a3-assignment-ui` | use the versioned command for generic assignment | 27 | adds later work | conflicts: `docs/README.md`, `lib/api/ticket-api.ts` |
  | `codex/fix/a4-blocked-ui` | surface callback block reasons in ticket task views | 21 | adds later work | conflict: `docs/README.md` |
  | `codex/fix/a4-frozen-callback` | block invalid frozen-callback input before execution | 20 | fully absorbed | conflict: `docs/README.md` |

- **The superset is not in the table, and it is not the whole feature.**
  `codex/chore/dev-restoration-schema-validation` (30 commits, the only branch pushed to
  origin) is the integration branch these cuts were taken from. Three cuts
  (`a3-start-contract`, `a3-contract-pipeline`, `a4-frozen-callback`) are **fully absorbed**
  by it — merging them back changes nothing. The other four continued on 2026-09-17 after
  the superset's last integration merge (2026-09-16 17:29) and **carry work it does not
  have**; `a3-legacy-gates` and `a3-assignment-ui` overlay it cleanly, while
  `a3-lifecycle-gate` (both `generic_workflow_gate*.go`) and `a4-blocked-ui` (the task-view
  test and `bpmn-workflow-api.ts`) conflict in two files each. The consolidation question
  is therefore *how to assemble the superset with those four*, not *which single branch to
  pick*.

- **Non-goals:** no second approval engine, no change to the BPMN engine core, no
  replacement of the Incident / Problem / Change lifecycles.
- **Owning module:** `itsm-backend/service` (BPMN fulfilment and WorkItem command
  boundaries) and the `itsm-frontend` task view; evidence keeps using each domain's
  existing audit channel.
- **Dependencies / migration risk:** must agree with the existing `process_bindings`
  and lifecycle-contract wording. The superset bundles two different reviewable goals —
  the fulfilment-gate product code, and the Dev 047 restoration / schema-clone
  operational record — so landing it as one branch conflicts with the
  one-branch-one-goal rule; split it before review, or accept and record the bundling
  explicitly.
- **Measured state, 2026-09-18 — the work line does not have a green suite.**
  Replaying the superset's `itsm-backend` / `itsm-frontend` commits onto current
  `origin/main` (15 commits, 42 files, +2927/−16, branch
  `codex/feat/generic-fulfillment-gate`) builds, but fails **26 existing tests**: 24 in
  `handlers/intake`, 1 in `service`, 1 in `tests/integration`. The same suite on pristine
  `origin/main` is green (63 packages, `MAIN_EXIT=0`), and the failing test alone fails in
  0.21s standalone — a deterministic regression, not load-sensitive flakiness.
  The trigger is that the branch **adds a `ParseXML` call to the creation path**, which
  never parsed a definition before. `genericBindingConfig`
  (`service/bpmn_generic_binding_contract.go`) parses the stored definition, and the
  parser itself rejects a definition with zero processes —
  `validateBPMN` in `service/bpmn_xml_parser.go` returns "BPMN定义必须包含至少一个流程".
  `ResolveCreationWorkflow` (`service/bpmn_creation.go`) then wraps that as
  `invalid workflow lifecycle contract`, so creation rejects definitions it previously
  resolved, **independent of record class**.
  The branch's own guard for this case, `hasGenericFulfillmentContract` in
  `service/bpmn_workitem_lifecycle_contract.go`, is **not** the trigger: every caller
  reaches it only after `ParseXML`, which has already rejected the empty definition, so
  that branch is defensive code rather than a live gate.
  The superset carries the same code and does not touch the failing test files, so the
  same failures apply to it. **Acceptance criterion (3) is therefore not met.**
- **Fix prepared, 2026-09-18 — narrowed to the contract's declared scope.** Branch
  `codex/fix/generic-gate-scope` (based on `codex/feat/generic-fulfillment-gate`) makes
  creation detect the contract without re-validating a stored definition: a definition
  that does not parse cannot declare `generic_fulfillment_v1`, so it freezes no flags and
  creation keeps its pre-contract behavior. Definition validity stays owned by publication
  validation. A regression test covers all six record classes against both a
  parser-rejected and a malformed definition. The design this implements is recorded in
  `docs/superpowers/specs/2026-09-18-generic-fulfillment-gate-design.md` (branch
  `codex/docs/generic-fulfillment-gate-design`, status draft — a BPMN contract change needs
  an independent reviewer, so the implementation branch cannot approve it).
- **Measured state, 2026-09-18 — the cuts do not compose textually.** Merging the four
  cuts that the superset does not already contain, in the order `a3-legacy-gates`,
  `a3-assignment-ui`, `a3-lifecycle-gate`, `a4-blocked-ui`, needs conflict resolution in
  4 files — 3 hunks where the cut is the stricter side, plus `a4-blocked-ui` carrying an
  older `lib/api/bpmn-workflow-api.ts` that lacks `completionNoteRequired` — and the
  result **does not compile**:
  `dto/bpmn_task_dto.go` declares `BPMNTaskCallbackBlock` twice, because two merged sides
  added the same struct and text merge cannot detect the duplicate. The assembled tree
  also conflicts with `origin/main` in 4 files, not 1. Treat the assembly as work for the
  owning agent, not as a mechanical merge.
- **Acceptance criteria:** (1) a generic fulfilment record cannot be closed without
  the required evidence, and the rejection reason is readable; (2) the task view
  shows the block reason; (3) Incident / Problem / Change behaviour and their
  regression tests are unchanged; (4) PostgreSQL lifecycle cases exist for the gate.
- **Evidence anchors:** the branch refs above (`git log -1 <branch>` for the last
  commit). Re-check conflicts with
  `git merge-tree --write-tree --name-only origin/main <branch>`. Re-derive the
  *Against the superset* column by **merge simulation, not by patch-id**: run
  `git merge-tree --write-tree <branch> codex/chore/dev-restoration-schema-validation`
  and compare the resulting tree with each side's own tree — equal to the superset's
  means the branch is fully absorbed, equal to the branch's means the superset is the
  smaller one, anything else means both sides hold work the other lacks. A patch-id
  comparison over `--no-merges` commits **under-counts**: content a branch received
  through its own merge commits belongs to no single commit, so it looks absent from
  every branch at once.
- **Status:** proposed

### BL-AGENT-GUIDANCE-CONVERGENCE — finish the agent guidance and shared-convention refactor

- **Outcome / persona:** one authoritative set of governance rules, less rework and
  fewer rule conflicts for later changes. (Persona: maintainer, every coding agent.)
- **Current state:** branch `codex/docs/agent-guidance-refactor` (2 commits, pushed).
  `docs/engineering-conventions.md` is already **byte-identical** to main, but
  `docs/agent-engineering-governance.md`, `AGENTS.md` and `CLAUDE.md` carry
  **unmerged deltas** and have diverged from main (the merge conflicts).
- **Scope:** rule on those deltas one by one — accept them (as their own PR that says
  whether it is a correction or an expansion) or close them explicitly so they stop
  hanging.
- **Non-goals:** do not change contract semantics; do not duplicate the `AGENTS.md`
  summary in a second place.
- **Acceptance criteria:** governance docs and the `AGENTS.md` / `CLAUDE.md` summaries
  agree, and the branch can be archived.
- **Evidence anchors:** the branch ref plus
  `git diff origin/main codex/docs/agent-guidance-refactor -- docs/agent-engineering-governance.md AGENTS.md CLAUDE.md`.
- **Status:** proposed

### BL-ARCH-HARDENING-REVIEW — dispose of the Agent-platform architecture review

- **Outcome:** the review either becomes a design/plan, or is explicitly labelled
  history, so it is not later cited as a delivered capability.
- **Current state:** branch `docs/architecture-agent-platform-evolution-20260901`
  (2 commits, pushed, merges cleanly: 1 file, +69) — the agent-platform evolution
  design review.
- **Scope:** decide between (1) adopt — convert into a design, plan or roadmap item;
  (2) archive — move under `docs/archive/`.
- **Acceptance criteria:** the document has an explicit state in main (present with a
  status marker, or archived); it no longer hangs as a branch.
- **Evidence anchors:** the branch ref and the single added document.
- **Status:** proposed

---

## 📊 Key Metrics

We track these on every release. Numbers below are post-v1.0 GA baseline
and the **target** for the next major release.

| Metric | v1.0 GA | v1.5 target | v2.0 target |
|:---|---:|---:|---:|
| Backend coverage | ~2% | 55% | 70% |
| Frontend coverage | ~10% (UI only) | 30% | 60% |
| E2E smoke coverage | 11 APIs | 25 APIs | 50 APIs |
| Mean PR → first review | TBD | < 48h | < 24h |
| Mean issue → first response | TBD | < 48h | < 24h |
| AI triage accuracy | — | 85% | 92% |
| Open stale issues | varies | < 30 | < 15 |

---

## 🤝 How to Influence the Roadmap

1. **File an issue** with the `feature-request` template and link to
   the milestone you think it belongs in.
2. **Vote** on issues with 👍 — we sort milestone backlogs by reactions.
3. **Propose a major change** via the RFC process (lands v1.5):
   `docs/rfcs/0000-template.md`.
4. **Pick up a "good first issue"** — every track has at least one.

---

## 📜 Changelog

Major releases are tracked in [CHANGELOG.md](./CHANGELOG.md) and via
GitHub [Releases](https://github.com/heidsoft/itsm/releases).