# AGENTS.md

## Purpose and authority

ITSM is an enterprise-grade, open-source, AI-Native platform for the China market, built with Go/Gin/Ent and Next.js/TypeScript. Target ServiceNow-class ITIL v3/v4 capability with lighter private deployment, local enterprise integrations, and one core model supporting private deployment, SaaS, and SaaS + MSP.

This file is the authoritative cross-domain architecture and design contract for all coding agents. [CLAUDE.md](CLAUDE.md) provides a synchronized summary and Claude's entry point; DTO, naming, frontend, and operational rules apply equally to every agent. Update its summary in the same change when this contract changes. Maintain detailed rules once and link them from both entry points.

Read [engineering governance](docs/agent-engineering-governance.md) before making changes. Follow the task-specific reading map below. Release plans belong in [ROADMAP.md](ROADMAP.md); check its date and current implementation/verification evidence before treating a capability as delivered. This file defines constraints, not a release or deployment status report.

## System and code boundaries

- **Backend authority:** `itsm-backend` owns domain rules, authorization, tenant isolation, workflow execution, audit, and API contracts. The frontend presents and collects information; UI state or hidden menus never grant permission or authorize a transition.
- **Existing ownership:** follow the owning domain's legacy `controller/` + `service/` or vertical `handlers/<domain>/` structure. Do not implement the same endpoint in both. A domain exposes services/contracts; other domains must not call its repository implementation directly. Shared helpers belong in `handlers/common/` or `handlers/shared/`; reuse established shared services such as dynamic-field services.
- **Dependency direction:** HTTP/router layers call application/domain services; services use repositories and infrastructure ports; infrastructure does not call upward into controllers or domain orchestration. Controllers bind/validate input, use established authorization, call services, map DTOs, and return responses. Business rules, transactions, persistence, and external side effects belong below HTTP.
- **Code locations:** Ent schemas live in `ent/schema/`, route registration in `router/`, and request authentication/CORS/logging in middleware. Tenant enforcement also belongs at service, association, database, and background execution boundaries; middleware alone is insufficient.
- **Frontend:** use Next.js App Router, `src/lib/api/` for API access, and existing store conventions. Keep domain components/hooks near their route; share only where reuse is real. Backend DTOs define the wire contract; do not hide contract defects with frontend business-rule copies.
- **Extensions:** AI/RAG services, KAF, connectors, skills, plugins, and CLI use existing service, permission, tenant, audit, and event boundaries. Connectors own lifecycle, configuration, health checks, secret masking, and external transport. Controllers must not make ad hoc provider calls; CLI must not become a second business implementation.

## Architecture and design principles

### One authority and explicit domain ownership

- One business concept or field has one authoritative owner and write location. Do not maintain long-term dual reads/writes, duplicate business queries/abstractions, or JSON relationships alongside structured relations. Derived projections must remain traceable to their source and cannot become independent authorities.
- WorkItem unifies identity and cross-cutting operations, not professional state machines. Incident, Problem, Change Request, Service Request, and Release retain their professional responsibilities; Ticket is WorkItem's product-facing name. The WorkItem extension contract below defines its specific classes and does not implicitly add a Release class.
- BPMN/process binding owns approval and fulfillment orchestration, escalation, and automation. Do not create another approval engine. Preserve definition, instance, task, variable, history, and audit integrity; professional services validate lifecycle changes. SLA uses authoritative timestamps and policy bindings.
- CMDB separates CI type/schema, instance, relationships/types, discovery source, reconciliation, topology, and impact analysis. Imports/discovery must be source-aware and idempotent; mutations preserve history/audit, and topology traversal must be bounded.
- Knowledge/RAG preserves source attribution, version state, tenant scope, and RBAC visibility, with permission filtering before retrieval and before response. Known Error is knowledge; a Catalog Item is a service definition, not an execution record.

### Proportionate abstraction and configuration

- Put variable product behavior in existing configuration, registries, policies, or strategies. Encode stable domain invariants directly in code/types. Introduce an abstraction only for a real variant, reuse need, or external boundary; do not build a configurable framework for a single fixed rule.
- Prefer cohesive refactoring over wrappers, bridge services, empty implementations, and parallel mechanisms. A Manager, Facade, Proxy, or Adapter is justified by a necessary responsibility or boundary, not its name. Remove obsolete paths when replacing them, subject to the explicit migration/compatibility decision below.
- Split modules by responsibility, transaction ownership, and reasons to change, not merely file length. Keep contracts understandable without reading all implementations; do not move the same tangled logic into a vaguely named layer.

### Transactions, delivery, and recovery

- The owning application service defines the transaction. Persist authoritative records, required audit, and reliable-delivery intent atomically where the operation requires them. Reuse existing outbox/delivery mechanisms for external side effects; a remote call cannot be made atomic merely by placing it inside a database transaction.
- Reuse [WorkItem intake](itsm-backend/handlers/intake/service.go) and the existing domain creators for new creation channels. Preserve its transactional creation, authorization, idempotency, snapshots, audit, and workflow-start outbox instead of introducing another base-record creation path.
- Retriable creation, callbacks, consumers, and external actions need stable action identity and explicit replay semantics. Replays must not duplicate business effects; changed content under the same identity must follow the owning contract's conflict rules. Use established versions, compare-and-swap, locks, or fenced leases for concurrency; a prior read is not a concurrency guarantee.
- Distinguish database commit, message delivery, workflow termination, external execution, verification, and professional completion. At-least-once transport requires receiver deduplication. Unknown external results remain pending/unknown/manual intervention as defined by the contract; retries must not blindly repeat an irreversible action or fabricate success evidence.

### Runtime lifecycle and candidate execution

- Service construction must not start consumers, deploy default workflows, create storage resources, or initialize schema. Runtime startup is explicit; cancellation and worker completion precede dependency shutdown.
- Candidate execution scope is an additional deployment restriction, never a replacement for tenant/RBAC or professional lifecycle authorization. Only new WorkItems may be enrolled in their creation transaction; historical records must not be enrolled, claimed, acknowledged, or rewritten to enable acceptance.
- Execution configuration and database role bindings must agree. Unknown capabilities fail closed; disabled required journeys remain unvalidated. A configured attachment backend must not silently fall back to another storage location.
- Controlled-migration runtime admission uses an independently configured, read-only inspection identity bound to the same database/schema/deployment, with ledger/evidence SELECT only. The business identity receives no global evidence access. Structural admission is distinct from privileged full preparation/retirement data verification and business acceptance; existing targets advance through canonical migrations without Ent overlays.

### Contract evolution and change scope

- Distinguish target invariants, current implementation, and migration/deployment state. Historical designs and green unit tests alone do not prove a feature is deployed or fully accepted.
- Change persistence and public APIs through an explicit migration/compatibility decision with verification and rollback/remediation boundaries. A necessary temporary projection or transition path must have one authoritative source, a documented scope, and a retirement condition; it is not permission for permanent dual ownership.
- Fix architectural violations needed for the authorized task. Record unrelated debt for separate work rather than silently expanding into a domain-wide rewrite. Major decisions and exceptions belong in a status-bearing authoritative design/contract linked from the documentation index.

## Security, tenant, and execution policy

- Authentication, RBAC, menu permissions, endpoint ACLs, row scope, and tenant filters must agree. Actor, tenant, and elevated scope come from trusted authentication/authorization context, never unchecked request fields or model output. Cross-tenant access and associations fail closed.
- Every new table, query, migration, job, consumer, menu, and API must account for tenant/MSP boundaries. Ownership may be through the canonical WorkItem relation; do not duplicate tenant fields in professional extensions. Keep tenant execution separate from restricted system directory/transport capabilities; follow [runtime database boundaries](itsm-backend/database/runtime_clients.go) and the [RLS execution guide](docs/DEVELOPMENT_GUIDE.md#rls-execution-boundary) before using system access.
- Uploads, imports, callbacks, webhooks, and AI tool endpoints need input/size validation, authorization, tenant checks, audit, and observable failures. High-risk AI/workflow/connector/bulk actions retain explicit actor/source metadata. Permission resource names must match the permission registry.
- Never commit or expose secrets, JWTs, API keys, passwords, connector credentials, prompt secrets, or unprotected sensitive content in logs/API responses. Connector APIs return masked metadata and health, not secrets; sensitive audit content requires an explicitly protected design.
- **Fail-closed dispatch:** unknown, unregistered, or unsupported BPMN tasks, connector/skill capabilities, AI tools, event/webhook consumers, and automation actions must produce an error or visible blocked/manual-intervention state. Never silently no-op or report success. A step is optional only when declared in its definition in advance; a skip still requires audit and an observable warning/metric.

## AI and automation boundary

- AI supports decisions: intent, extraction, ranking, triage, explanation, knowledge retrieval, impact analysis, and action proposals. Code enforces authorization, risk gates, policy lookup, transitions, transaction boundaries, audit, and side effects. AI cannot bypass professional workflow or tenant scope.
- Use the existing LLM gateway/AI abstraction. Version and test prompts/skills; retain confidence, model/provider, prompt version, actor/source, decision, and feedback where applicable. Failure, timeout, disabled provider, or low confidence must produce explicit safe behavior or manual review, never silent success or weakened authorization.
- Improve typed schemas, prompts, or configuration when structured output is insufficient. Do not add a second keyword classifier to re-derive domain meaning after structured AI/domain output.
- KAF/external executors consume authenticated task scope and existing approval evidence; they do not own ITSM approval or professional completion. Changes to delegated execution must follow the [verified completion contract](docs/contracts/kaf-verified-access-completion.md), including uncertain-result recovery and replay boundaries.

## Unified Work Item domain contract

- **Identity:** WorkItem is the assignable, trackable, auditable base record; the product may call it Ticket. Reuse `tickets`; a physical rename requires a separate migration decision. New shared backend interfaces use `WorkItem`.
- **Extensions:** each Incident, Problem, Change Request, Requested Item, and Catalog Task has exactly one WorkItem, created atomically with its one-to-one professional extension. Professional extensions contain domain-specific fields only.
- **Shared ownership:** WorkItem owns number, title, description, record class, status storage, priority, requester/opener, assignee/group, category, tenant, timestamps/version, SLA/workflow references, comments, attachments, followers, timeline, audit, and notifications. Do not duplicate shared public fields in extensions.
- **Class:** `recordClass` is `generic`, `service_request_item`, `incident`, `problem`, `change_request`, or `catalog_task`; it is immutable after an extension exists. Classifying generic work creates the extension atomically. Keep business subtype separate from class; do not overload `type`. This vocabulary does not claim that every creation channel supports every class: unsupported dispatch fails closed.
- **Relations:** Incident → Problem or Problem → Change creates a target WorkItem plus an explicit relation, preserving source identity/history; changing a type is not a lifecycle conversion.
- **Vocabulary:** Requested Item is one Catalog request instance; Catalog Task is optional split approval/fulfillment/validation/delivery work. Catalog Item defines service/form/class/process/fulfillment/SLA; Ticket Category classifies operational work; Ticket Template supports internal rapid entry/execution and does not replace Catalog Item. Known Error and Catalog Item are not WorkItems. Introduce a Request Header only for an actual multi-item requirement.
- **Lifecycle owners:** `IncidentService` owns acknowledge/pending/resolve/close/reopen/cancel/major-incident rules; `ProblemService` owns assessment/investigation/root cause/workaround/known error/resolve/close/reopen; `ChangeService` owns risk, assessment, authorization/CAB, scheduling/windows, implementation, review/PIR, rollback, and closure; `ServiceRequestService` owns catalog validation, approval, fulfillment, delivery, and Requested Item transitions. Do not implement these as one giant service or `switch recordClass` state machine.

- **Process identity:** at every BPMN boundary, business type is the WorkItem `recordClass` and the key is `{recordClass}:{workItemId}` with a WorkItem ID. The legacy vocabulary (`ticket`/`change`/`service_request`) is retired: never write or re-interpret it, or add a mapping back to legacy values. Use the single authority `common/workitemidentity`. Release retains its explicit `release` identity and is not a WorkItem.
- **Problem root cause:** `problems.root_cause` is the sole authoritative text. RCA records own analysis metadata (method, evidence, confidence, reviewer) and project the Problem text. RCA mutations update that text and metadata atomically; Known Error creation reads the same text. Do not restore another root-cause text column or dual writes.

The [original WorkItem design](docs/superpowers/specs/2026-08-26-unified-work-item-model-design.md) supplies rationale and detailed vocabulary. Its historical baseline and rollout checklist are not current implementation evidence; this section is the implementation contract.

## Accepted WorkItem decisions and migration boundaries

These decisions extend the contract above. Read the linked designs before changing the affected behavior; their plans track implementation and evidence. Isolated validation is not authorization for target deployment or retirement. Open questions and backlog items are not approved implementation requirements.

- **Convergence:** Incident recovery is independent of Problem completion; Problem resolution requires a verified permanent fix. Authorized reopen starts a zeroed SLA cycle while preserving prior results and original creation time. For this convergence, historical business data is not migrated, while schema changes remain required. Retire old structures only after sole-path cutover, validation, and observation; no parallel lifecycle, relationship, SLA, or approval implementation. [Design](docs/superpowers/specs/2026-09-09-workitem-convergence-design.md), [plan](docs/superpowers/plans/2026-09-09-workitem-convergence.md).
- **Assignments:** first assignment of a new Incident moves it to assigned; later reassignment preserves its allowed nonterminal state. Starting work does not require acknowledge; acknowledge stays optional. All entrypoints use Incident domain rules and versioned transactional receipts. First-response measurement remains BL-RESP-01: do not change response recording or implicitly count assignment/start as a response. Change reassignment preserves stage and approval results without replacing independently assigned task actors; explicitly bound fulfillment tasks follow the binding contract below. Problem reassignment preserves progress and evidence. Every reassignment requires reason and audit. Change multi-WorkOrder execution remains BL-CHG-WO-01. [Incident decision](docs/superpowers/specs/2026-09-11-workitem-incident-assignment-convergence-design.md), [cross-domain decisions](docs/superpowers/specs/2026-09-11-workitem-convergence-development-input.md), [next-stage design](docs/superpowers/specs/2026-09-11-workitem-convergence-next-stage-design.md), [plan](docs/superpowers/plans/2026-09-11-workitem-next-stage.md).
- **Bound fulfillment tasks:** explicit `assigneeSource=work_item_assignee` uses the current WorkItem owner, with no second mutable task owner. This binding does not replace approval/requester/candidate participation rules. Freeze terminal responsible person and actual actor; missing evidence never falls back to the current owner. Preserve professional command authorization, operation receipts, reason/version and tenant/MSP/execution scope; one transactional owner write, audit and Outbox. Do not rewrite old runs. [Binding design](docs/superpowers/specs/2026-09-14-work-item-task-assignment-design.md), [decisions and evidence](docs/review/2026-09-15-work-item-task-assignment-report.md).
- **Controlled retirement:** preserve historical SQL/checksums and truthful receipts. Separate transactional structure preparation from full business acceptance and controlled retirement; all migration write paths, including rollback/reset, enforce stage dependencies. Read-only classification precedes bootstrap writes. General pre-preparation active-process retirement remains unaccepted while BL-WI-PROCESS-AUDIT-CONTINUITY is deferred. Successor rereview must not be represented as independent third-party review. Real target deployment/retirement and environment deletion require separate authorization; isolated or dedicated-database evidence does not grant it. [Design](docs/superpowers/specs/2026-09-11-workitem-controlled-retirement-design.md), [plan and validation evidence](docs/superpowers/plans/2026-09-11-workitem-controlled-retirement.md).

## CTI governance contract

The [CTI governance design](docs/superpowers/specs/2026-09-17-cti-governance-design.md) records the accepted direction and review clarifications. Read it before changing classification, catalog defaults or completion gates.

**Code is delivered; no tenant is enabled.** The implementation landed through PR #48 with unit, HTTP-contract and isolated-PostgreSQL evidence, and the shared development database `itsm_config_baseline_20260908` received migration `048_cti_governance` during an authorized acceptance run. The completion gate remains **not enabled for any tenant**, and later classification work (for example PR #53) is still pending independent review. Never describe this as "upgraded everywhere"; separate code delivery, target migration and tenant enablement in status reports and docs.

Contract points that follow from the design and must hold in every change:

- A WorkItem stores only the **deepest selected classification node**; the Category/Type/Item path is a derived projection, never a second written authority.
- Classification trees are at most three levels, tenant-scoped, with codes unique per tenant and immutable, and nodes that are still referenced cannot be deleted or moved. Deactivation stops new selection without rewriting history.
- The completion quality gate applies at **completion** time, per tenant, against an immutable first-enable cutoff, and is dispatched by record class plus action with explicit fail-closed handling of unknown combinations. Service recovery actions stay ungated.
- Completing a professional record requires a complete three-level classification; correcting a classification requires a reason and records the before/after path. Requested Items additionally may not be reclassified to a partial or empty classification.
- Rule `category_id` conditions are exact by default; subtree matching must be declared explicitly and unknown scopes fail closed. Reference listings expose names and counts only to callers holding that module's read permission, while reference-based maintenance protection always uses the unfiltered scan.

Rollout, backfill, staged switches, pause and re-verification steps live in the [CTI governance rollout checklist](docs/operations/cti-governance-rollout-checklist.md); step-by-step execution evidence lives in the [implementation plan](docs/superpowers/plans/2026-09-17-cti-governance.md).

Two related items are registered but **not implemented**: **BL-CTI-02** (retire the creation-time classification name slots so creation accepts only the deepest node id) and **BL-CTI-03** (classify list/monitoring/dashboard filters by node id with strict query-parameter validation). None of them writes to a shared database, runs a migration or deploys anything; see the [backlog design drafts](docs/superpowers/specs/2026-09-12-backlog-design-drafts.md).

## Required reading by task

| Task | Read before acting |
| --- | --- |
| Any repository change | [Agent engineering governance](docs/agent-engineering-governance.md): placement, tests, worktrees, review, shared-environment and delivery rules |
| API, DTO, frontend, naming | [Shared engineering conventions](docs/engineering-conventions.md) |
| Local services, database, migration, deployment | [Development guide](docs/DEVELOPMENT_GUIDE.md), [command reference](docs/dev-commands-reference.md); for maintained Windows/WSL instances, [local environment](docs/development-environment.md) first |
| WorkItem fields, creation, lifecycle, relations | Contract above, owning domain code, and [WorkItem design](docs/superpowers/specs/2026-08-26-unified-work-item-model-design.md) with its status caveat |
| KAF/delegated execution and completion | [Verified completion contract](docs/contracts/kaf-verified-access-completion.md) and owning Service Request/BPMN services |
| Review and real user-path verification | [Code review guide](docs/code-review-guide.md), [E2E guide](docs/e2e-testing-guide.md) |
| Product scope and current decisions | [Root roadmap](ROADMAP.md), [documentation index](docs/README.md); reconcile stale status against current evidence |
