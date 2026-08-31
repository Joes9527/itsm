# 🛣️ ITSM Roadmap

> **Source of truth for what is shipping, what is shipping next, and what
> is parked.** Updated as part of every release. Last synced: 2026-09-01.
>
> Cross-references:
> - PRD library: [prd/](./prd)
> - v1.0 GA readiness: [v1-ga-readiness.md](./v1-ga-readiness.md)
> - Architecture: [architecture/](./architecture)
> - Open issues & milestones: GitHub [Issues](https://github.com/heidsoft/itsm/issues) and [Projects](https://github.com/heidsoft/itsm/projects)

---

## 🎯 North Star

**Become the de-facto open-source AI-Native ITSM for enterprises that need
ServiceNow-class workflows without the lock-in or the footprint.**

Concretely that means:
1. **Process parity** with the ITIL v4 core, backed by lifecycle and E2E evidence.
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
- [ ] **Controller split** — break up `incident_controller.go` (1,392 lines),
      `ticket_controller.go` (1,211), `cmdb_controller.go` (1,879), and
      `bpmn_workflow_controller.go` (1,111), measured 2026-09-01, into
      feature-scoped sub-controllers
- [ ] **Integration test suite** — RBAC cross-tenant, BPMN happy paths,
      CMDB impact analysis, SLA escalation. Lives at `itsm-backend/tests/integration/`.
- [x] **itsm-cli / itsm-skill / itsm-agent path-scoped CI** —
      `.github/workflows/extensions-ci.yml` builds/tests the tracked extension
      modules; `itsm-skill` remains skipped when it is not present in a checkout
- [x] **Unified Intake transactional creation boundary** — authenticated ITSM
      and KAF creation for Incident/Service Request Item, exact replay,
      tenant-scoped identity exchange, frozen workflow Outbox, metrics and real
      PostgreSQL/RLS evidence. Owner: `handlers/intake`, BPMN Outbox worker and
      KAF typed client. Non-goals: new Intake UI and additional professional
      classes. Evidence: [implementation report](./reports/2026-08-31-unified-intake-implementation-report.md).

### Product

- [ ] **Connector marketplace v1** — Feishu (IM + Approval), DingTalk
      (IM + Work Notice), WeCom (IM), Webhook. Lifecycles via
      `/api/v1/connectors/lifecycle`.
- [ ] **AI Audit console** — review every AI suggestion, accept/reject,
      feed back to evaluator.
- [ ] **Standard change templates** — pre-baked change templates for
      common ops (network, OS patch, DB migration).

### Quality

- [ ] **Incremental line-coverage gate** — enforce 60% on new/modified lines.
      Current `test-coverage-guard` only requires source/test co-change and CI
      uploads aggregate coverage; neither proves the 60% diff threshold.
- [ ] **Dependabot auto-merge decision** — current policy deliberately requires
      normal CI and maintainer review and does not auto-merge. Add automation
      only after branch protection and rollback ownership are documented.

### Unified Intake follow-on queue

- [ ] **Upgrade-safe Incident WorkItem backfill** — outcome: operators can
      upgrade old databases through migration `021` without direct SQL. Scope:
      restore/replace a dry-run, idempotent and audited Incident backfill tool;
      non-goal: bypassing integrity checks. Owner: migration/ops commands.
      Dependency/risk: legacy Incident rows and ticket-number collisions.
      Acceptance: repeated backfill is cardinality-stable,
      `check_work_item_integrity` is clean, and `021` → `022` passes on a
      production copy. Status: planned. Evidence:
      [implementation report §7](./reports/2026-08-31-unified-intake-implementation-report.md).
      Score (impact/security/ITIL/alignment/effort): `5/4/5/5/3`.
- [ ] **KAF Intake release configuration and test isolation** — outcome: tenant
      operators can enable `bastion_permission_apply` and
      `business_system_account_request`, and maintainers get a
      green repository suite. Scope: real Catalog Item IDs plus isolated
      PostgreSQL fixtures; non-goal: fallback creation. Owner: KAF config/test
      bootstrap. Dependencies: tenant catalog provisioning and test database.
      Acceptance: real Dev create/replay succeeds and full `pytest -q` exits 0
      without hiding failures. Status: planned.
      Score: `5/3/5/5/3`.
- [ ] **Typed professional actions and creator expansion** — outcome: agents
      can perform version-fenced Incident actions and create Problem, Change
      Request and Catalog Task through owning domain services. Non-goal: a giant
      record-class state machine. Owner: professional services + Intake
      registry. Dependencies: action audit contract and per-domain migrations.
      Acceptance: expected-version conflicts, tenant/RBAC denial, atomic
      extension creation and lifecycle tests. Status: proposed.
      Score: `5/4/5/5/5`.
- [ ] **Problem/Change WorkItem authority convergence** — outcome: operators
      and reports see one authoritative title/status/priority/assignment/tenant
      value. Scope: move reads/writes to WorkItem, make `work_item_id` required,
      replace extension RLS with ticket joins and migrate Change relationship
      JSON; non-goal: merging professional state machines. Owner:
      Problem/Change services and migrations. Dependencies: audited backfills
      and running-process identity review. Acceptance: no shared extension
      columns or dual writes, structured relations only, PostgreSQL RLS and
      lifecycle suites pass. Status: planned. Score: `5/3/5/5/5`.
- [ ] **Unified Intake OpenAPI contract** — outcome: integrators can discover
      create, identity exchange/mapping and retry contracts from generated
      Swagger. Owner: Intake handlers + generated backend docs. Dependency:
      stable auth/error annotations. Acceptance: JSON/YAML/Go artifacts contain
      every Intake route and CI detects drift. Status: planned. Score:
      `3/2/3/3/2`.
- [ ] **Intake interaction experience** — outcome: requesters get Intake UI,
      draft/session support, attachment staging, knowledge deflection and
      multi-channel status feedback. Owner: Intake application/frontend.
      Dependencies: retention policy, upload security, permission-filtered RAG
      and channel identity. Acceptance: browser E2E, audit/redaction evidence,
      abandoned-session cleanup and no alternate WorkItem creation path.
      Status: proposed.
      Score: `4/4/3/5/5`.

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

- Source/test co-change guard — landed v1.1; true 60% diff coverage remains planned
- End-to-end smoke on every PR — landed v1.0
- Frontend visual regression — planned v1.5
- Property-based tests for critical parsers (BPMN XML, RAG chunking)
  — planned v1.5

### Security

- gosec + Trivy + govulncheck — landed v1.1; CodeQL analysis remains planned
- Quarterly threat-model review
- Annual pen-test
- 连接器凭据（`connector_configs.credentials`）AES 加密存储 — backlog（当前 JSON 明文，含 Azure client_secret）

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

Major releases are tracked in the repository-root `CHANGELOG.md` and via
GitHub [Releases](https://github.com/heidsoft/itsm/releases).
