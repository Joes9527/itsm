# CLAUDE.md

## Required shared instructions

Read [AGENTS.md](AGENTS.md) before making repository changes. It is the authoritative product, architecture, security, and WorkItem contract for all coding agents. This file is a synchronized entry-point summary, not a second set of architecture or agent-specific engineering rules.

Also read [agent engineering governance](docs/agent-engineering-governance.md) and the documents required for the task in AGENTS.md's reading map. API/DTO, TypeScript/Ant Design, and file naming rules are shared in [engineering conventions](docs/engineering-conventions.md). Commands, testing procedures, migrations, and Docker operations are in [the development guide](docs/DEVELOPMENT_GUIDE.md) and [command reference](docs/dev-commands-reference.md). For maintained Windows/WSL instances, read [local environment](docs/development-environment.md) before operating services or data.

For Dev workflow recovery or schema-based migration validation, follow the [AGENTS.md task reading map](AGENTS.md#required-reading-by-task), including the restoration design, both task packages and the single execution ledger. Accepted direction does not imply that draft implementation gates or shared-environment acceptance have passed.

When architecture/domain constraints change, update AGENTS.md first and this summary in the same change. Update detailed engineering/operational rules in their shared source and link them; do not copy command tables, port defaults, release snapshots, or historical verification claims here.

## Architecture and design summary

- Build enterprise-grade, open-source, AI-Native ITSM for the China market, with private deployment and SaaS/MSP on one core model. Follow current roadmap and verification evidence, not a copied release status.
- Backend services own domain rules, authorization, tenant boundaries, workflow, and audit. Frontend, AI, KAF, connectors, plugins, and CLI use those boundaries. Follow each domain's existing legacy or vertical slice; do not add parallel endpoints or call another domain's repository implementation.
- Keep controllers thin and dependencies downward. Each field/concept has one authoritative owner. BPMN owns approval/fulfillment orchestration; professional services own their transitions. Preserve CMDB concerns and permission-filtered, attributed/versioned knowledge retrieval.
- Use configuration for real product variation and code/types for stable invariants. Add abstractions only for real responsibilities or boundaries. Split by cohesion and transaction ownership; avoid unrelated refactoring.
- Services own transactions. Reuse WorkItem intake/domain creators and existing outbox delivery. Define idempotency, replay conflicts, and concurrency protection. Commit, delivery, execution, verification, workflow termination, and professional completion are distinct; unknown results cannot be reported as success.
- Evolve public/persistence contracts through explicit migration and compatibility decisions with verification and retirement/remediation boundaries. Distinguish target architecture, code, and deployed/accepted behavior.
- Actor/tenant/privileged scope comes from trusted authentication and authorization. Tenant execution and restricted system capabilities remain separate. Enforce scope at services, associations, database, and jobs as well as HTTP. Mask secrets and audit high-risk actions with actor/source metadata.
- Unknown dispatch fails closed across workflows, connectors, skills, AI tools, and consumers. Optional steps must be declared in advance and their skips audited and observable.
- AI proposes; code applies policy and side effects. Use the existing gateway and versioned, auditable structured output; do not add parallel keyword classifiers or silent success fallbacks. KAF follows the [verified completion contract](docs/contracts/kaf-verified-access-completion.md).

## WorkItem summary

- WorkItem uses `tickets`; Ticket is the product name, not another lifecycle. Each professional extension has exactly one WorkItem created in the same transaction. Shared fields live only on WorkItem; extensions hold professional fields.
- `recordClass` is separate from subtype and immutable after extension creation. The full class vocabulary and channel-support caveat are in AGENTS.md; do not introduce a Release class by inference.
- Cross-domain relationships create target records and explicit relations, preserving source identity/history; they are not type conversions. Known Error and Catalog Item are not WorkItems. Catalog definitions, operational categories, and internal templates have separate responsibilities.
- Incident, Problem, Change, and Service Request services own their professional lifecycles; do not consolidate them into a giant `switch recordClass` state machine.

## Runtime and accepted convergence decisions

- Construction has no runtime side effects: start explicitly, cancel/join workers before shutting down dependencies. Candidate scope supplements tenant/RBAC and professional authorization; enroll only new WorkItems atomically at creation, never historical records for acceptance. Execution configuration/roles must agree; disabled journeys remain unvalidated and configured attachment storage cannot silently fall back.
- Controlled-migration admission uses a separate read-only inspection identity on the same database/schema/deployment, with ledger/evidence SELECT only; business identity has no global evidence access. Existing targets use canonical migrations without Ent overlays. Structural admission is not full privileged data verification or business acceptance.
- BPMN identity is `recordClass` plus `{recordClass}:{workItemId}`, defined only by `common/workitemidentity`; retired `ticket`/`change`/`service_request` vocabulary must not be written, interpreted, or mapped back. Release retains `release` and is not a WorkItem.
- Problem root-cause text lives only in `problems.root_cause`; RCA owns metadata and projects that text. Update text/metadata atomically; Known Error reads the same text. No second text column or dual writes.
- Follow [AGENTS.md's accepted decisions](AGENTS.md#accepted-workitem-decisions-and-migration-boundaries): Incident recovery is independent of Problem completion; Problem resolution requires verified permanent fix; authorized reopen zeroes a new SLA cycle while preserving prior results and original creation time. First Incident assignment moves to assigned, later reassignment preserves state, acknowledge is optional before start. Change/Problem reassignment preserves professional progress/evidence; reasons/audits and versioned receipts remain required. BL-RESP-01 and BL-CHG-WO-01 remain backlog; do not infer response recording from assignment/start.
- Convergence does not migrate historical business data but still requires schema changes. Preserve migration SQL/checksums, stage dependencies for all writes including rollback/reset, truthful receipts, and read-only classification before bootstrap. Retire only after sole-path cutover, validation, and observation. General pre-preparation active-process retirement remains unaccepted under BL-WI-PROCESS-AUDIT-CONTINUITY. Successor review is not independent third-party review; isolated verification does not authorize target deployment/retirement or deletion. Read the linked accepted designs and evidence plans; open questions are not implementation requirements.

Explicit WorkItem-bound fulfillment tasks follow the current WorkItem owner; approval, requester and candidate rules stay separate. Preserve terminal actor evidence and main professional command authorization, operation receipts and scope; do not add another owner write or rewrite history. See [AGENTS assignment contract](AGENTS.md#accepted-workitem-decisions-and-migration-boundaries) and [implementation handoff](docs/review/2026-09-15-work-item-task-assignment-report.md).
