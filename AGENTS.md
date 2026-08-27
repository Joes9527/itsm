# AGENTS.md

This file provides guidance to Codex (Codex.ai/code) when working with code in this repository.

## Project Overview

ITSM (IT Service Management) system with a Go/Gin backend and Next.js/TypeScript frontend. Features include:

- Ticket/Incident/Problem/Change management
- Service Catalog
- Knowledge Base with RAG
- BPMN Workflow engine
- SLA monitoring and escalation
- AI-powered triage and summarization

## Product Direction

This project is building an enterprise-grade, open-source, AI-Native ITSM platform for the China market. The long-term benchmark is ServiceNow-class process capability, but with lighter private deployment, stronger local enterprise integration, and open extensibility.

Core product goals:

- Cover complete ITIL v3/v4 service management workflows: ticket, incident, problem, change, release, service request, service catalog, SLA, knowledge, and CMDB.
- Make workflow customization a first-class capability through BPMN, process binding, form/config templates, and auditable task execution.
- Build AI into the service management lifecycle instead of adding a chatbot beside it: triage, summarization, knowledge retrieval, impact analysis, workflow recommendation, audit review, and controlled tool invocation.
- Prepare for Feishu, WeCom, DingTalk, Webhook, connector marketplace, skill marketplace, plugin marketplace, and CLI-driven operations.
- Support private deployment, SaaS, and SaaS + MSP modes without forking the core data model.

When making architecture choices, prefer enterprise correctness, auditability, tenant isolation, and extensibility over quick feature-only shortcuts.

## System Architecture

- **itsm-backend** is the source of truth for domain rules, RBAC, tenant isolation, workflow execution, audit logs, and API contracts.
- **itsm-frontend** is the operator, administrator, and requester experience layer. It must not duplicate backend business rules or infer authorization from UI state.
- **itsm-ai-service**, RAG services, connectors, skills, plugins, and CLI tools are extension surfaces. They must use established service, permission, tenant, audit, and event boundaries rather than embedding parallel business logic.
- Enterprise connectors use lifecycle, configuration, health-check, permission, secret-masking, and audit boundaries. Controllers must not make ad hoc external calls.

### Backend and Frontend Boundaries

- The backend contains legacy `controller/` + `service/` modules and newer vertical slices under `handlers/<domain>/`. When extending a domain, follow its existing style and do not implement the same endpoint in both styles.
- `handlers/<domain>/` packages own their handler, service, repository, and entity/DTO boundaries. Shared helpers belong in `handlers/common/` or `handlers/shared/`; do not call a domain repository implementation directly from another domain.
- Ent schemas live under `ent/schema/`; authentication, CORS, RBAC, logging, and tenant isolation belong to middleware; route registration belongs to `router/`.
- Frontend pages use Next.js App Router. API access goes through `src/lib/api/`; shared state uses the existing store conventions; domain components and hooks stay close to their route unless reuse is real.

### Domain Ownership

- Ticket, Incident, Problem, Change, Release, and Service Request remain distinct professional domains. The WorkItem contract unifies identity and cross-cutting capabilities, not professional state machines.
- BPMN/process execution is the orchestration layer for approvals, fulfillment, SLA escalation, and automation. Do not create a second approval engine.
- CMDB keeps CI type/schema, CI instance, relationships, relationship types, discovery source, reconciliation, topology, and impact analysis as separate concerns. Discovery and import must be idempotent and source-aware.
- Knowledge and RAG must preserve source attribution, version state, tenant scope, RBAC visibility, and permission filtering before retrieval and before response.
- Every new table, query, background job, migration, event consumer, menu item, and API must account for tenant/MSP boundaries.

### Security and Compliance

- Authentication, RBAC, menu permissions, endpoint ACLs, row scope, and tenant filters must agree. Hiding a menu is never authorization.
- Cross-tenant access and associations fail closed. Connector secrets, JWTs, API keys, passwords, prompt secrets, and unprotected sensitive content must never appear in logs or API responses.
- Upload, import, connector callback, webhook, and AI tool-invocation endpoints require validation, size limits, permission checks, tenant checks, audit records, and observable failure handling.
- High-risk actions initiated by AI, workflow, connectors, or bulk operations require explicit actor/source metadata and audit records.

## Architecture Principles

- Prefer architectural refactoring over compatibility layers, wrappers, bridge services, temporary fallbacks, or parallel implementations. When a new path replaces an old path, remove the old path in the same change unless backward compatibility is an explicit requirement.
- Keep one authoritative source for each business concept and field. Do not maintain long-term dual reads, dual writes, duplicated queries, duplicated abstractions, or JSON fields alongside structured relations.
- Prefer configuration-driven, registry-based, policy-based, and strategy-based behavior over hardcoded routing, tenant data, business vocabulary, thresholds, or keyword heuristics. Put variable product behavior in configuration or domain metadata.
- Keep dependency direction downward: HTTP/router layers call application/domain services, services use repositories and infrastructure ports, and infrastructure adapters do not call upward into domain or controller code.
- Keep controllers thin. They bind and validate input, authorize through established middleware/services, call the owning service, and map DTOs. Business rules, transactions, persistence, workflow identity, and external side effects belong below the controller boundary.
- Do not add layers such as Manager, Facade, Proxy, or Adapter unless they remove real complexity or define a necessary external boundary. Avoid empty implementations, silent success fallbacks, and deprecated aliases that preserve an obsolete design.
- Keep modules understandable and traceable. Split oversized files when responsibility boundaries are clear; do not solve size by moving the same logic into another ambiguous layer.

### AI and Automation Boundary

- AI is decision support by default. It may understand intent, extract entities, rank options, generate explanations, and propose actions; it must not silently bypass authorization, workflow, tenant isolation, or audit requirements.
- Code enforces policy and performs side effects: permission checks, risk gates, policy lookup, state transitions, transaction boundaries, audit writes, and external calls. Do not re-derive domain meaning with a second classifier or keyword scan after an AI/domain service has produced structured output.
- When structured AI output is insufficient, improve the typed schema, prompt, or configuration rather than adding a parallel classifier or hardcoded semantic branch. AI suggestions must retain confidence, model/provider, prompt version, actor/source, decision, and feedback where applicable.

### Development Documentation

Operational development procedures are maintained separately:

- [Development and Operations Guide](docs/DEVELOPMENT_GUIDE.md): setup, commands, testing, API/DTO conventions, naming, deployment, troubleshooting, and review lessons.
- [Development Command Reference](docs/dev-commands-reference.md): detailed Make, Docker Compose, local-service, health-check, and migration commands.
- [Code Review Guide](docs/code-review-guide.md): review workflow and quality checklist.
- [E2E Testing Guide](docs/e2e-testing-guide.md): browser and real-path verification details.

 Keep `AGENTS.md` focused on architecture and product/domain constraints. Update the linked operational documentation when commands or development procedures change.
## Current Product Stage

The repository is past v1.0 GA foundation work and is moving through v1.1 hardening:

- v1.0 delivered ITIL core flows, BPMN workflow engine, CMDB v1, knowledge/RAG scaffold, SLA, RBAC, multi-tenant/MSP foundations, Docker Compose, GHCR images, and basic AI/connector scaffolding.
- v1.1 focus is coverage backfill, controller splitting, connector marketplace v1, RBAC hardening, AI audit console, and integration test coverage.
- v1.5+ focus is measurable AI evaluator, Feishu/DingTalk/WeCom production connectors, Skill registry, performance budgets, and stronger security scans.

For new work, align with the roadmap rather than creating parallel mechanisms. If a feature overlaps with workflow, connector, AI skill, or marketplace direction, extend the existing extension point.

## Unified Work Item Domain Contract

The unified Work Item model is the shared business language for Ticket, Service Catalog, Service Request, Incident, Problem, Known Error, Change, and fulfillment work. The detailed design is maintained in `docs/superpowers/specs/2026-08-26-unified-work-item-model-design.md`; this section is the implementation contract for all coding agents.

### Business Vocabulary

- **WorkItem** is the unified base record for assignable, trackable, auditable ITSM work. Product UI may continue to say Ticket, but new backend shared interfaces use `WorkItem`.
- **Ticket** is the product-level name for a WorkItem, not a separate professional lifecycle.
- **Incident** restores service after an unplanned interruption or degradation.
- **Problem** investigates and removes the root cause of one or more Incidents.
- **Known Error** is a knowledge record with a known root cause or workaround; it is not a WorkItem.
- **Change Request** is a controlled change to a service, application, infrastructure, or configuration.
- **Requested Item** is one concrete Service Catalog request instance and is a WorkItem.
- **Catalog Task** is an optional approval, fulfillment, validation, or delivery task split from a Requested Item and is a WorkItem.
- **Service Catalog / Catalog Item** defines services, forms, target class, process, fulfillment, and SLA; it is not an execution record.
- **Ticket Category** is the operational classification tree for routing, SLA, reporting, knowledge, automation, and AI classification.
- **Ticket Template** is an internal rapid-entry or execution template; it does not replace a Catalog Item.
- **Request Header** is optional and should only be introduced when multi-item requests are a real product requirement.

### Model Principles

- Reuse the existing `tickets` table as the first-phase WorkItem base table. Do not physically rename it to `work_items` without a separate migration decision.
- Every Incident, Problem, Change Request, Requested Item, and Catalog Task must have exactly one WorkItem. Create the base record and its one-to-one professional extension in the same transaction.
- WorkItem owns shared identity and cross-cutting fields: number, title, description, record class, status storage, priority, requester, opener, assignee, assignment group, category, tenant, timestamps, version, SLA references, workflow references, comments, attachments, timeline, audit, and notifications.
- Professional extensions own only domain-specific fields. Do not copy shared title, description, status, priority, assignee, tenant, creator, or public timestamps into extension tables.
- `recordClass` identifies the professional class (`generic`, `service_request_item`, `incident`, `problem`, `change_request`, `catalog_task`). It is immutable after an extension exists; classification of a generic item must create the extension atomically.
- Do not use `type` for both professional class and business subtype. Use `recordClass` for class and keep professional subtypes in the extension model.
- A relationship is not a lifecycle conversion. Incident does not become Problem by changing a type, and Problem does not become Change. Create the target WorkItem and an explicit relation while preserving the source record and history.
- Known Error and Catalog Item remain separate concepts: knowledge record and service definition respectively, not WorkItems.
- One authoritative field has one write location. Do not maintain duplicate public fields, long-term dual writes, or JSON relationship fields alongside structured relations.

### Professional Lifecycle Ownership

- WorkItem provides shared operations such as assignment, comments, attachments, followers, SLA projection, workflow references, activity timeline, and audit.
- `IncidentService` owns acknowledge, resolve, close, reopen, pending, cancellation, and major-incident rules.
- `ProblemService` owns assessment, investigation, root cause, workaround, known-error, resolve, close, and reopen rules.
- `ChangeService` owns assessment, authorization, scheduling, implementation, review, rollback, closure, risk, CAB, and implementation-window rules.
- `ServiceRequestService` owns catalog validation, approval, fulfillment, delivery, and Requested Item lifecycle rules.
- Do not create a giant service or `switch recordClass` that implements every professional state machine. Shared services coordinate common behavior; professional services validate professional transitions and side effects.

Operational commands, testing procedures, naming details, DTO examples, deployment operations, and troubleshooting belong in [docs/DEVELOPMENT_GUIDE.md](docs/DEVELOPMENT_GUIDE.md).


